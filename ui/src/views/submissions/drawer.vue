<script setup lang="ts">
// New submission: pick an ACTIVE template, then exactly one signer per template
// party (a tenant member, each person once), the signing mode and — for a
// sequential submission — the order, optional prefilled values for the
// text-valued fields, an expiry and reminders. The submission is created as a
// draft; the drawer then offers to send it right away.
import { computed, reactive, ref, watch } from 'vue'
import { UiAlert, UiButton, UiCheckbox, UiDrawer, UiInput, UiNumberInput, UiSelect, type SelectOption } from '@go-tangra/ui'
import { api, describe, describeRefusal, refusalField } from '@/api/client'
import type { Field, Party, Submission, SubmissionCreate, SubmissionMode, Template, TemplatePage } from '@/api/types'
import { useSubmissions } from '@/stores/submissions'
import { useTemplates } from '@/stores/templates'
import { TYPE_LABELS } from '@/utils/fields'
import { isTextValued, valueError } from '@/utils/values'
import UserPicker from '@/components/UserPicker.vue'

const props = defineProps<{ open: boolean; templateId?: string | undefined }>()
const emit = defineEmits<{ (e: 'close'): void; (e: 'created', s: Submission): void; (e: 'sent', s: Submission): void }>()

const store = useSubmissions()
const templates = useTemplates()

const active = ref<Template[]>([])
const template = ref<Template | null>(null)
const loadingTemplates = ref(false)
const draft = reactive({
  template_id: '',
  name: '',
  mode: 'sequential' as SubmissionMode,
  /** Party keys in signing order. */
  order: [] as string[],
  /** Party key → user id. */
  signers: {} as Record<string, string>,
  prefill: {} as Record<string, string>,
  expires: '',
  remind: false,
  interval: 3 as number | '',
  max: 3 as number | '',
})
const errors = ref<Record<string, string>>({})
const banner = ref('')
const saving = ref(false)
const created = ref<Submission | null>(null)
const sending = ref(false)

async function loadTemplates(): Promise<void> {
  loadingTemplates.value = true
  try {
    const res = await api<TemplatePage>('GET', 'templates', undefined, { query: { status: 'active', page: 1, page_size: 100 } })
    active.value = res.items ?? []
  } catch (e) {
    banner.value = describe(e)
  } finally {
    loadingTemplates.value = false
  }
}

watch(() => props.open, (open) => {
  if (!open) return
  Object.assign(draft, { template_id: '', name: '', mode: 'sequential', order: [], signers: {}, prefill: {}, expires: '', remind: false, interval: 3, max: 3 })
  template.value = null
  errors.value = {}
  banner.value = ''
  created.value = null
  void loadTemplates().then(() => { if (props.templateId) void pickTemplate(props.templateId) })
}, { immediate: true })

const templateOptions = computed<SelectOption[]>(() => active.value.map((t) => ({ title: t.name, value: t.id })))
const modeOptions: SelectOption[] = [
  { title: 'One after another (in order)', value: 'sequential' },
  { title: 'All at once (any order)', value: 'parallel' },
]

async function pickTemplate(v: unknown): Promise<void> {
  const id = typeof v === 'string' ? v : ''
  draft.template_id = id
  errors.value = {}
  template.value = null
  if (!id) return
  try {
    const t = await templates.get(id)
    if (draft.template_id !== id) return
    template.value = t
    if (!draft.name.trim()) draft.name = t.name
    draft.order = (t.parties ?? []).map((p) => p.key)
    draft.signers = {}
    draft.prefill = {}
    if (t.default_expiry_days) draft.expires = localInput(new Date(Date.now() + t.default_expiry_days * 86400000))
    if (t.default_reminder) Object.assign(draft, { remind: true, interval: t.default_reminder.interval_days, max: t.default_reminder.max })
  } catch (e) {
    banner.value = describe(e)
  }
}

const parties = computed<Party[]>(() => draft.order.map((k) => template.value?.parties?.find((p) => p.key === k)).filter((p): p is Party => !!p))
const prefillFields = computed<Field[]>(() => (template.value?.fields ?? []).filter((f) => isTextValued(f.type)).sort((a, b) => a.page - b.page || a.y - b.y || a.x - b.x))
const partyName = (key: string) => template.value?.parties?.find((p) => p.key === key)?.name ?? key
const picked = computed(() => Object.values(draft.signers).filter(Boolean))

function move(i: number, d: -1 | 1): void {
  const j = i + d
  if (j < 0 || j >= draft.order.length) return
  const next = [...draft.order]
  ;[next[i], next[j]] = [next[j]!, next[i]!]
  draft.order = next
}

function setSigner(party: string, userId: string): void {
  draft.signers = { ...draft.signers, [party]: userId }
  errors.value = { ...errors.value, ['signer-' + party]: '', signers: '' }
}
function setPrefill(id: string, v: unknown): void {
  const s = v === true ? 'true' : v === false ? '' : String(v ?? '')
  draft.prefill = { ...draft.prefill, [id]: s }
  errors.value = { ...errors.value, ['prefill-' + id]: '' }
}
const optionList = (f: Field): SelectOption[] => (f.options ?? []).map((o) => ({ title: o, value: o }))
/** A date as the value of a datetime-local input (local time, minutes). */
function localInput(d: Date): string {
  const two = (n: number) => String(n).padStart(2, '0')
  return `${d.getFullYear()}-${two(d.getMonth() + 1)}-${two(d.getDate())}T${two(d.getHours())}:${two(d.getMinutes())}`
}
const intOr = (v: number | '' | unknown): number => (typeof v === 'number' ? v : Number(v))

function check(): boolean {
  const e: Record<string, string> = {}
  if (!draft.template_id || !template.value) e.template = 'Choose an active template.'
  if (draft.name.trim().length > 200) e.name = 'At most 200 characters.'
  const seen = new Set<string>()
  for (const p of parties.value) {
    const u = draft.signers[p.key] ?? ''
    if (!u) e['signer-' + p.key] = 'Choose who signs for this party.'
    else if (seen.has(u)) e['signer-' + p.key] = 'This person already signs for another party.'
    seen.add(u)
  }
  for (const f of prefillFields.value) {
    const bad = valueError(f, draft.prefill[f.id] ?? '')
    if (bad) e['prefill-' + f.id] = bad
  }
  if (draft.expires) {
    const t = new Date(draft.expires)
    if (Number.isNaN(t.getTime()) || t.getTime() <= Date.now()) e.expires = 'Choose a time in the future.'
  }
  if (draft.remind) {
    const iv = intOr(draft.interval)
    const mx = intOr(draft.max)
    if (!Number.isInteger(iv) || iv < 1 || iv > 30) e.interval = 'Between 1 and 30 days.'
    if (!Number.isInteger(mx) || mx < 1 || mx > 20) e.max = 'Between 1 and 20.'
  }
  errors.value = e
  return Object.keys(e).length === 0
}

function body(): SubmissionCreate {
  const b: SubmissionCreate = {
    template_id: draft.template_id,
    mode: draft.mode,
    signers: parties.value.map((p, i) => (draft.mode === 'sequential' ? { user_id: draft.signers[p.key]!, party: p.key, position: i } : { user_id: draft.signers[p.key]!, party: p.key })),
  }
  if (draft.name.trim()) b.name = draft.name.trim()
  const prefill = Object.fromEntries(Object.entries(draft.prefill).filter(([id, v]) => v !== '' && prefillFields.value.some((f) => f.id === id)))
  if (Object.keys(prefill).length) b.prefill = prefill
  if (draft.expires) b.expires_at = new Date(draft.expires).toISOString()
  if (draft.remind) b.reminder = { interval_days: intOr(draft.interval), max: intOr(draft.max) }
  return b
}

async function save(): Promise<void> {
  banner.value = ''
  if (!check()) return
  saving.value = true
  try {
    const s = await store.create(body())
    created.value = s
    emit('created', s)
  } catch (e) {
    const field = refusalField(e) ?? ''
    if (field && prefillFields.value.some((f) => f.id === field)) errors.value = { ['prefill-' + field]: describe(e) }
    banner.value = field && prefillFields.value.some((f) => f.id === field) ? `${describe(e)} (${prefillFields.value.find((f) => f.id === field)!.name})` : describeRefusal(e)
  } finally {
    saving.value = false
  }
}

async function sendNow(): Promise<void> {
  if (!created.value) return
  sending.value = true
  banner.value = ''
  try {
    const s = await store.send(created.value.id)
    emit('sent', s)
    emit('close')
  } catch (e) {
    banner.value = describeRefusal(e)
  } finally {
    sending.value = false
  }
}
</script>

<template>
  <UiDrawer :model-value="open" title="New submission" size="lg" data-test="submission-drawer" @update:model-value="emit('close')">
    <div v-if="created" class="flex flex-col gap-4" data-test="submission-created">
      <UiAlert kind="success" title="Submission created">
        “{{ created.name }}” is saved as a draft. Send it now to invite {{ created.mode === 'sequential' ? 'the first signer' : 'every signer' }}, or send it later from its page.
      </UiAlert>
      <UiAlert v-if="banner" kind="error" data-test="submission-form-error">{{ banner }}</UiAlert>
    </div>

    <div v-else class="flex flex-col gap-4">
      <UiAlert v-if="banner" kind="error" data-test="submission-form-error">{{ banner }}</UiAlert>
      <UiSelect id="submission-template" :model-value="draft.template_id" label="Template" :options="templateOptions" :placeholder="loadingTemplates ? 'Loading…' : 'Choose an active template'" required :error="errors.template || undefined" data-test="submission-template" @update:model-value="pickTemplate" />

      <template v-if="template">
        <UiInput id="submission-name" v-model="draft.name" label="Name" hint="Shown to the signers." :error="errors.name || undefined" data-test="submission-name" />
        <UiSelect id="submission-mode" v-model="draft.mode" label="Signing order" :options="modeOptions" :clearable="false" data-test="submission-mode" />

        <fieldset class="flex flex-col gap-3" data-test="submission-signers">
          <legend class="mb-1 text-sm font-medium">Signers — one per party</legend>
          <div v-for="(p, i) in parties" :key="p.key" class="flex items-start gap-2" :data-test="'signer-row-' + p.key">
            <span v-if="draft.mode === 'sequential'" class="badge badge-soft mt-8 shrink-0" :aria-label="`Signs ${i + 1}.`">{{ i + 1 }}</span>
            <div class="min-w-0 grow">
              <UserPicker
                :id="'signer-' + p.key"
                :model-value="draft.signers[p.key] ?? ''"
                :label="p.name"
                required
                :exclude="picked"
                :error="errors['signer-' + p.key] || undefined"
                :data-test="'signer-picker-' + p.key"
                @update:model-value="setSigner(p.key, $event)"
              />
            </div>
            <div v-if="draft.mode === 'sequential' && parties.length > 1" class="mt-7 flex shrink-0 gap-0.5">
              <UiButton size="xs" variant="text" icon="mdi-chevron-up" icon-only :label="`Move ${p.name} earlier`" :disabled="i === 0" :data-test="'signer-up-' + p.key" @click="move(i, -1)" />
              <UiButton size="xs" variant="text" icon="mdi-chevron-down" icon-only :label="`Move ${p.name} later`" :disabled="i === parties.length - 1" :data-test="'signer-down-' + p.key" @click="move(i, 1)" />
            </div>
          </div>
        </fieldset>

        <details v-if="prefillFields.length" class="rounded-box border border-base-300 p-3" data-test="submission-prefill">
          <summary class="cursor-pointer text-sm font-medium">Prefill values (optional)</summary>
          <div class="mt-3 flex flex-col gap-3">
            <template v-for="f in prefillFields" :key="f.id">
              <UiSelect v-if="f.type === 'select' || f.type === 'radio'" :id="'prefill-' + f.id" :model-value="draft.prefill[f.id] ?? ''" :label="`${f.name} (${partyName(f.party)})`" :options="optionList(f)" placeholder="Not prefilled" :error="errors['prefill-' + f.id] || undefined" :data-test="'prefill-' + f.id" @update:model-value="setPrefill(f.id, $event)" />
              <UiCheckbox v-else-if="f.type === 'checkbox'" :id="'prefill-' + f.id" :model-value="draft.prefill[f.id] === 'true'" :label="`${f.name} (${partyName(f.party)}) — checked`" :data-test="'prefill-' + f.id" @update:model-value="setPrefill(f.id, $event)" />
              <UiInput
                v-else
                :id="'prefill-' + f.id"
                :model-value="draft.prefill[f.id] ?? ''"
                :label="`${f.name} (${partyName(f.party)})`"
                :type="f.type === 'date' ? 'date' : 'text'"
                :inputmode="f.type === 'number' ? 'decimal' : undefined"
                :hint="TYPE_LABELS[f.type]"
                :error="errors['prefill-' + f.id] || undefined"
                :data-test="'prefill-' + f.id"
                @update:model-value="setPrefill(f.id, $event)"
              />
            </template>
          </div>
        </details>

        <UiInput id="submission-expires" v-model="draft.expires" label="Expires" type="datetime-local" hint="Optional. Unsigned slots can no longer be signed after this time." :error="errors.expires || undefined" data-test="submission-expires" />
        <UiCheckbox id="submission-remind" v-model="draft.remind" label="Send reminders to signers who have not signed" data-test="submission-remind" />
        <div v-if="draft.remind" class="grid grid-cols-2 gap-3">
          <UiNumberInput id="submission-interval" v-model="draft.interval" label="Every (days)" :min="1" :max="30" :step="1" :error="errors.interval || undefined" data-test="submission-interval" />
          <UiNumberInput id="submission-max" v-model="draft.max" label="At most (reminders)" :min="1" :max="20" :step="1" :error="errors.max || undefined" data-test="submission-max" />
        </div>
      </template>
    </div>

    <template #actions>
      <template v-if="created">
        <UiButton variant="text" data-test="submission-later" @click="emit('close')">Send later</UiButton>
        <UiButton icon="mdi-send-outline" :loading="sending" data-test="submission-send-now" @click="sendNow">Send now</UiButton>
      </template>
      <template v-else>
        <UiButton variant="text" @click="emit('close')">Cancel</UiButton>
        <UiButton icon="mdi-check" :loading="saving" :disabled="!template" data-test="submission-save" @click="save">Create</UiButton>
      </template>
    </template>
  </UiDrawer>
</template>
