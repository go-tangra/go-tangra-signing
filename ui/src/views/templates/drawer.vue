<script setup lang="ts">
// Template drawer: upload a new PDF template (file, name, description, folder,
// tags) or edit an existing template's details (rename, move, re-tag). The
// upload is multipart/form-data; the server refuses non-PDF, encrypted or
// oversized files with a reason shown here. A refusal naming a field lands on it.
// Editing also sets the defaults new submissions of the template start with:
// an expiry (1–365 days) and reminders (every 1–30 days, at most 1–20), each
// optional (null clears it).
import { computed, reactive, ref, watch } from 'vue'
import { UiAlert, UiButton, UiCheckbox, UiDrawer, UiFilePicker, UiInput, UiNumberInput, UiSelect, UiTextarea } from '@go-tangra/ui'
import { ApiError, describe, refusalField } from '@/api/client'
import type { Folder, Reminder, Template } from '@/api/types'
import { MAX_PDF_BYTES, useTemplates } from '@/stores/templates'
import { folderOptions } from '@/utils/folderTree'

const props = defineProps<{
  open: boolean
  /** null uploads a new template. */
  template: Template | null
  folders: Folder[]
  /** Folder preselected for a new upload (the one open in the tree). */
  folderId?: string | undefined
}>()
const emit = defineEmits<{ (e: 'close'): void; (e: 'saved', t: Template, created: boolean): void }>()

const store = useTemplates()
const draft = reactive({
  file: null as File | null, name: '', description: '', folder_id: '', tags: '',
  expire: false, expiry_days: 30 as unknown, remind: false, interval: 3 as unknown, max: 3 as unknown,
})
const errors = ref<Record<string, string>>({})
const banner = ref('')
const saving = ref(false)

watch(() => [props.open, props.template?.id], () => {
  if (!props.open) return
  const t = props.template
  Object.assign(draft, { file: null, name: t?.name ?? '', description: t?.description ?? '', folder_id: t ? t.folder_id ?? '' : props.folderId ?? '', tags: (t?.tags ?? []).join(', ') })
  const days = t?.default_expiry_days
  const rem = t?.default_reminder
  Object.assign(draft, {
    expire: typeof days === 'number', expiry_days: typeof days === 'number' ? days : 30,
    remind: !!rem, interval: rem?.interval_days ?? 3, max: rem?.max ?? 3,
  })
  errors.value = {}
  banner.value = ''
}, { immediate: true })

const options = computed(() => folderOptions(props.folders))
const tagList = () => draft.tags.split(',').map((s) => s.trim()).filter(Boolean)

function pick(f: File | null): void {
  draft.file = f
  errors.value = { ...errors.value, file: '' }
  if (f && !draft.name.trim()) draft.name = f.name.replace(/\.pdf$/i, '')
}

const int = (v: unknown): number => (typeof v === 'number' ? v : v === '' || v === null || v === undefined ? NaN : Number(v))

function check(): boolean {
  const e: Record<string, string> = {}
  if (props.template && draft.expire) {
    const d = int(draft.expiry_days)
    if (!Number.isInteger(d) || d < 1 || d > 365) e.default_expiry_days = 'Between 1 and 365 days.'
  }
  if (props.template && draft.remind) {
    const iv = int(draft.interval)
    const mx = int(draft.max)
    if (!Number.isInteger(iv) || iv < 1 || iv > 30) e.interval = 'Between 1 and 30 days.'
    if (!Number.isInteger(mx) || mx < 1 || mx > 20) e.max = 'Between 1 and 20.'
  }
  if (!props.template && !draft.file) e.file = 'Choose a PDF file.'
  if (!draft.name.trim()) e.name = 'Enter a name.'
  else if (draft.name.trim().length > 200) e.name = 'At most 200 characters.'
  errors.value = e
  return Object.keys(e).length === 0
}

async function save(): Promise<void> {
  banner.value = ''
  if (!check()) return
  saving.value = true
  try {
    if (props.template) {
      const reminder: Reminder | null = draft.remind ? { interval_days: int(draft.interval), max: int(draft.max) } : null
      const t = await store.patch(props.template.id, {
        name: draft.name.trim(), description: draft.description.trim(), folder_id: draft.folder_id || null, tags: tagList(),
        default_expiry_days: draft.expire ? int(draft.expiry_days) : null, default_reminder: reminder,
      })
      emit('saved', t, false)
    } else {
      const t = await store.create(draft.file!, { name: draft.name, description: draft.description, folder_id: draft.folder_id || undefined, tags: tagList() })
      emit('saved', t, true)
    }
    emit('close')
  } catch (e) {
    const field = refusalField(e)
    const target = field && ['name', 'description', 'folder_id', 'tags', 'file', 'default_expiry_days'].includes(field) ? field : field === 'default_reminder' ? 'interval' : ''
    if (target) errors.value = { [target]: describe(e) }
    else if (!props.template && e instanceof ApiError && ['invalid_pdf', 'payload_too_large'].includes(e.reason)) errors.value = { file: describe(e) }
    else banner.value = describe(e)
  } finally {
    saving.value = false
  }
}
</script>

<template>
  <UiDrawer :model-value="open" :title="template ? 'Template details' : 'Upload template'" size="lg" data-test="template-drawer" @update:model-value="emit('close')">
    <div class="flex flex-col gap-4">
      <UiAlert v-if="banner" kind="error" data-test="template-form-error">{{ banner }}</UiAlert>
      <UiFilePicker
        v-if="!template"
        id="template-file"
        :model-value="draft.file"
        label="PDF file"
        accept="application/pdf"
        :max-bytes="MAX_PDF_BYTES"
        hint="A PDF of up to 50 MB, not encrypted."
        required
        :error="errors.file || undefined"
        data-test="template-file"
        @update:model-value="pick"
        @too-large="errors = { ...errors, file: 'The file is larger than 50 MB.' }"
      />
      <UiInput id="template-name" v-model="draft.name" label="Name" required :error="errors.name || undefined" data-test="template-name" />
      <UiTextarea id="template-description" v-model="draft.description" label="Description" :rows="3" :error="errors.description || undefined" data-test="template-description" />
      <UiSelect id="template-folder" v-model="draft.folder_id" label="Folder" :options="options" placeholder="No folder" :error="errors.folder_id || undefined" data-test="template-folder" />
      <UiInput id="template-tags" v-model="draft.tags" label="Tags" hint="Separate tags with commas." placeholder="hr, contracts" :error="errors.tags || undefined" data-test="template-tags" />
      <fieldset v-if="template" class="flex flex-col gap-3 rounded-box border border-base-300 p-3" data-test="template-defaults">
        <legend class="px-1 text-sm font-medium">Defaults for new submissions</legend>
        <p class="text-xs text-base-content/70">Senders start with these and can change them per submission.</p>
        <UiCheckbox id="template-expire" v-model="draft.expire" label="Unsigned submissions expire" data-test="template-expire" />
        <UiNumberInput v-if="draft.expire" id="template-expiry-days" v-model="draft.expiry_days" label="Expire after (days)" :min="1" :max="365" :step="1" hint="Counted from sending, 1 to 365 days." :error="errors.default_expiry_days || undefined" data-test="template-expiry-days" />
        <UiCheckbox id="template-remind" v-model="draft.remind" label="Remind signers who have not signed" data-test="template-remind" />
        <div v-if="draft.remind" class="grid grid-cols-2 gap-3">
          <UiNumberInput id="template-interval" v-model="draft.interval" label="Every (days)" :min="1" :max="30" :step="1" :error="errors.interval || undefined" data-test="template-interval" />
          <UiNumberInput id="template-max" v-model="draft.max" label="At most (reminders)" :min="1" :max="20" :step="1" :error="errors.max || undefined" data-test="template-max" />
        </div>
      </fieldset>
    </div>
    <template #actions>
      <UiButton variant="text" @click="emit('close')">Cancel</UiButton>
      <UiButton :icon="template ? 'mdi-check' : 'mdi-upload'" :loading="saving" data-test="template-save" @click="save">{{ template ? 'Save' : 'Upload' }}</UiButton>
    </template>
  </UiDrawer>
</template>
