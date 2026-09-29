<script setup lang="ts">
// One submission: its status and settings, every signer's state with the
// times, the certificate that signed and delivery problems (mail_error as a
// warning), the event history, and the downloads (current document, final
// document and audit trail once they exist). The sender (or a submissions
// manager: can_control) may send a draft, cancel with a reason, resend an
// invitation, replace a signer who has not finished, and delete. Live events
// for this submission reload it.
import { computed, inject, onBeforeUnmount, onMounted, reactive, ref, watch } from 'vue'
import { routeLocationKey, routerKey } from 'vue-router'
import {
  UiAlert, UiBadge, UiButton, UiCard, UiDataTable, UiDialog, UiEmptyState, UiErrorState, UiIcon, UiKeyValueTable, UiPage, UiSkeleton, UiStatusChip, UiTextarea,
  useConfirm, useToast, type Column, type KeyValue,
} from '@go-tangra/ui'
import { describe, describeRefusal } from '@/api/client'
import type { SignerView, Submission, SubmissionEvent } from '@/api/types'
import { useSubmissions } from '@/stores/submissions'
import { coalesce, useLive } from '@/stores/live'
import { when } from '@/utils/format'
import { SIGNER_STATUS_COLORS, SIGNER_STATUS_LABELS, SUBMISSION_STATUS_COLORS, SUBMISSION_STATUS_LABELS, eventLabel, progress, signerFinal, submissionOpen } from '@/utils/submission'
import UserPicker from '@/components/UserPicker.vue'

const store = useSubmissions()
const live = useLive()
const confirm = useConfirm()
const toast = useToast()
const route = inject(routeLocationKey, null)
const router = inject(routerKey, null)

const id = computed(() => (typeof route?.params.id === 'string' ? route.params.id : ''))
const sub = ref<Submission | null>(null)
const events = ref<SubmissionEvent[]>([])
const loading = ref(false)
const loadError = ref('')
const error = ref('')
const busy = ref('')

async function load(): Promise<void> {
  if (!id.value) return
  loading.value = true
  loadError.value = ''
  try {
    const [s, ev] = await Promise.all([store.get(id.value), store.events(id.value).catch(() => [] as SubmissionEvent[])])
    sub.value = s
    events.value = [...ev].sort((a, b) => b.at.localeCompare(a.at))
  } catch (e) {
    sub.value = null
    loadError.value = describe(e)
  } finally {
    loading.value = false
  }
}
watch(id, () => void load(), { immediate: true })

const refresh = coalesce(() => void load(), 400)
let release: (() => void) | null = null
let unsubscribe: (() => void) | null = null
onMounted(() => {
  release = live.connect()
  unsubscribe = live.on((e) => { if (e.data.submission_id === id.value) refresh.trigger() })
})
onBeforeUnmount(() => {
  refresh.cancel()
  unsubscribe?.()
  release?.()
})

const canControl = computed(() => !!sub.value?.can_control)
const signerName = (sid: string | null | undefined) => sub.value?.signers.find((s) => s.id === sid)?.name ?? ''

const summary = computed<KeyValue[]>(() => {
  const s = sub.value
  if (!s) return []
  const out: KeyValue[] = [
    { label: 'Signing order', value: s.mode === 'sequential' ? 'One after another' : 'All at once' },
    { label: 'Progress', value: progress(s) },
    { label: 'Created', value: when(s.created_at) },
    { label: 'Sent', value: when(s.sent_at) || 'Not sent' },
  ]
  if (s.completed_at) out.push({ label: 'Completed', value: when(s.completed_at) })
  out.push({ label: 'Expires', value: when(s.expires_at) || 'Never' })
  out.push({ label: 'Reminders', value: s.reminder ? `Every ${s.reminder.interval_days} day(s), at most ${s.reminder.max}` : 'Off' })
  if (s.cancel_reason) out.push({ label: 'Cancel reason', value: s.cancel_reason })
  return out
})

// --- downloads ---
const currentUrl = computed(() => (sub.value ? store.documentUrl(sub.value.id) : ''))
const finalUrl = computed(() => (sub.value && sub.value.final_version !== null && sub.value.final_version !== undefined ? store.documentUrl(sub.value.id, sub.value.final_version) : ''))
const auditUrl = computed(() => (sub.value?.audit_trail ? store.auditTrailUrl(sub.value.id) : ''))

// --- sender actions ---
async function run(key: string, fn: () => Promise<unknown>, done: string): Promise<boolean> {
  error.value = ''
  busy.value = key
  try {
    await fn()
    toast.show({ kind: 'success', title: done })
    await load()
    return true
  } catch (e) {
    error.value = describeRefusal(e)
    return false
  } finally {
    busy.value = ''
  }
}

async function send(): Promise<void> {
  const s = sub.value
  if (s) await run('send', () => store.send(s.id), 'Submission sent')
}

async function resend(sg: SignerView): Promise<void> {
  const s = sub.value
  if (s) await run('resend-' + sg.id, () => store.resend(s.id, sg.id), `Invitation sent again to ${sg.name}`)
}

async function remove(): Promise<void> {
  const s = sub.value
  if (!s) return
  if (!(await confirm.ask({ title: `Delete ${s.name}?`, text: 'The submission, its documents and its history are deleted. This cannot be undone.', danger: true, confirmLabel: 'Delete' }))) return
  error.value = ''
  busy.value = 'delete'
  try {
    await store.remove(s.id)
    toast.show({ kind: 'success', title: 'Submission deleted' })
    void router?.push({ name: 'signing-submissions' })
  } catch (e) {
    error.value = describeRefusal(e)
  } finally {
    busy.value = ''
  }
}

const cancelDlg = reactive({ open: false, reason: '', error: '', saving: false })
function askCancel(): void {
  Object.assign(cancelDlg, { open: true, reason: '', error: '', saving: false })
}
async function doCancel(): Promise<void> {
  const s = sub.value
  if (!s) return
  const reason = cancelDlg.reason.trim()
  if (!reason) {
    cancelDlg.error = 'Enter a reason; the signers see it.'
    return
  }
  if (reason.length > 500) {
    cancelDlg.error = 'At most 500 characters.'
    return
  }
  cancelDlg.saving = true
  cancelDlg.error = ''
  try {
    await store.cancel(s.id, reason)
    cancelDlg.open = false
    toast.show({ kind: 'success', title: 'Submission cancelled' })
    await load()
  } catch (e) {
    cancelDlg.error = describeRefusal(e)
  } finally {
    cancelDlg.saving = false
  }
}

const replaceDlg = reactive({ open: false, signer: null as SignerView | null, user: '', error: '', saving: false })
function askReplace(sg: SignerView): void {
  Object.assign(replaceDlg, { open: true, signer: sg, user: '', error: '', saving: false })
}
async function doReplace(): Promise<void> {
  const s = sub.value
  const sg = replaceDlg.signer
  if (!s || !sg) return
  if (!replaceDlg.user) {
    replaceDlg.error = 'Choose the new signer.'
    return
  }
  if (replaceDlg.user === sg.user_id) {
    replaceDlg.error = 'Choose a different person.'
    return
  }
  replaceDlg.saving = true
  replaceDlg.error = ''
  try {
    await store.replaceSigner(s.id, sg.id, replaceDlg.user)
    replaceDlg.open = false
    toast.show({ kind: 'success', title: 'Signer replaced' })
    await load()
  } catch (e) {
    replaceDlg.error = describeRefusal(e)
  } finally {
    replaceDlg.saving = false
  }
}
const otherSigners = computed(() => (sub.value?.signers ?? []).filter((x) => x.id !== replaceDlg.signer?.id).map((x) => x.user_id))

const canResend = (sg: SignerView) => canControl.value && sub.value?.status === 'in_progress' && (sg.status === 'invited' || sg.status === 'opened')
const canReplace = (sg: SignerView) => canControl.value && !!sub.value && submissionOpen(sub.value) && !signerFinal(sg)

// --- signers table ---
type Row = SignerView & Record<string, unknown>
const columns = computed<Column<Row>[]>(() => [
  ...(sub.value?.mode === 'sequential' ? [{ key: 'position', label: '#', width: 'sm' as const, format: (s: Row) => String(s.position + 1) }] : []),
  { key: 'name', label: 'Signer' },
  { key: 'status', label: 'Status', width: 'sm' },
  { key: 'times', label: 'Times', hideOnStack: true },
  { key: 'certificate', label: 'Signed with', hideOnStack: true },
])
const rows = computed(() => [...(sub.value?.signers ?? [])].sort((a, b) => a.position - b.position) as Row[])
</script>

<template>
  <UiPage :title="sub?.name ?? 'Submission'" :subtitle="sub ? progress(sub) : undefined">
    <template #before-title>
      <UiButton variant="text" icon="mdi-arrow-left" icon-only label="Back to submissions" data-test="submission-back" @click="router?.push({ name: 'signing-submissions' })" />
    </template>
    <template #badges>
      <UiStatusChip v-if="sub" :status="sub.status" :label="SUBMISSION_STATUS_LABELS[sub.status]" :colors="SUBMISSION_STATUS_COLORS" data-test="submission-status" />
    </template>
    <template v-if="sub && canControl" #actions>
      <UiButton v-if="sub.status === 'draft'" icon="mdi-send-outline" :loading="busy === 'send'" data-test="submission-send" @click="send">Send</UiButton>
      <UiButton v-if="sub.status === 'in_progress'" variant="soft" color="warning" icon="mdi-cancel" data-test="submission-cancel" @click="askCancel">Cancel submission</UiButton>
      <UiButton variant="soft" color="error" icon="mdi-delete-outline" :loading="busy === 'delete'" data-test="submission-delete" @click="remove">Delete</UiButton>
    </template>

    <UiSkeleton v-if="loading && !sub" kind="card" :lines="8" />
    <UiErrorState v-else-if="!sub" :text="loadError || 'The submission could not be loaded.'" data-test="submission-load-error" @retry="load" />

    <div v-else class="flex min-w-0 flex-col gap-4">
      <UiAlert v-if="error" kind="error" data-test="submission-action-error">{{ error }}</UiAlert>

      <div class="grid min-w-0 grid-cols-1 gap-4 lg:grid-cols-[minmax(0,1fr)_20rem]">
        <div class="flex min-w-0 flex-col gap-4">
          <UiCard title="Signers" :padded="false">
            <UiDataTable :items="rows" :columns="columns" caption="Signers and their progress" empty-title="No signers" :row-attrs="(s) => ({ 'data-test': 'signer-' + s.id })" data-test="signers-table">
              <template #cell-name="{ row }">
                <span class="font-medium">{{ row.name }}</span>
                <span class="block text-xs text-base-content/70">{{ row.party }}</span>
                <span v-if="row.decline_reason" class="block text-xs text-error">Declined: {{ row.decline_reason }}</span>
              </template>
              <template #cell-status="{ row }">
                <div class="flex flex-col items-start gap-1">
                  <UiStatusChip :status="row.status" :label="SIGNER_STATUS_LABELS[row.status]" :colors="SIGNER_STATUS_COLORS" :data-test="'signer-status-' + row.id" />
                  <UiBadge v-if="row.mail_error" color="warning" size="xs" :data-test="'signer-mail-error-' + row.id">
                    <span class="sr-only">E-mail problem: </span>{{ row.mail_error }}
                  </UiBadge>
                </div>
              </template>
              <template #cell-times="{ row }">
                <dl class="grid grid-cols-[auto_1fr] gap-x-2 text-xs">
                  <template v-if="row.invited_at"><dt class="text-base-content/70">Invited</dt><dd>{{ when(row.invited_at) }}</dd></template>
                  <template v-if="row.opened_at"><dt class="text-base-content/70">Opened</dt><dd>{{ when(row.opened_at) }}</dd></template>
                  <template v-if="row.signed_at"><dt class="text-base-content/70">Signed</dt><dd>{{ when(row.signed_at) }}</dd></template>
                  <template v-if="row.declined_at"><dt class="text-base-content/70">Declined</dt><dd>{{ when(row.declined_at) }}</dd></template>
                  <template v-if="row.reminders_sent"><dt class="text-base-content/70">Reminders</dt><dd>{{ row.reminders_sent }}</dd></template>
                </dl>
              </template>
              <template #cell-certificate="{ row }">
                <div v-if="row.cert_subject || row.method" class="text-xs">
                  <span v-if="row.method" class="block font-medium">{{ row.method }}</span>
                  <span v-if="row.cert_subject" class="block">{{ row.cert_subject }}</span>
                  <span v-if="row.cert_issuer" class="block text-base-content/70">Issued by {{ row.cert_issuer }}</span>
                  <span v-if="row.cert_serial" class="block break-all text-base-content/70">Serial {{ row.cert_serial }}</span>
                </div>
              </template>
              <template #actions="{ row }">
                <div class="flex flex-wrap justify-end gap-0.5">
                  <UiButton v-if="canResend(row)" size="xs" variant="text" icon="mdi-email-outline" icon-only :label="`Send the invitation again to ${row.name}`" :loading="busy === 'resend-' + row.id" :data-test="'signer-resend-' + row.id" @click="resend(row)" />
                  <UiButton v-if="canReplace(row)" size="xs" variant="text" icon="mdi-account-arrow-right" icon-only :label="`Replace ${row.name}`" :data-test="'signer-replace-' + row.id" @click="askReplace(row)" />
                </div>
              </template>
            </UiDataTable>
          </UiCard>

          <UiCard title="History">
            <UiEmptyState v-if="!events.length" icon="mdi-history" title="No events yet" />
            <ol v-else class="flex flex-col gap-3 border-s border-base-300 ps-4" data-test="submission-events">
              <li v-for="ev in events" :key="ev.id" class="relative" :data-test="'event-' + ev.id">
                <span class="absolute -start-[1.3rem] top-1.5 size-2.5 rounded-full bg-primary" aria-hidden="true" />
                <p class="text-sm font-medium">{{ eventLabel(ev.type) }}<template v-if="signerName(ev.signer_id)"> · {{ signerName(ev.signer_id) }}</template></p>
                <p class="text-xs text-base-content/70">{{ when(ev.at) }}</p>
              </li>
            </ol>
          </UiCard>
        </div>

        <aside class="flex min-w-0 flex-col gap-4">
          <UiCard title="Details">
            <UiKeyValueTable :items="summary" :columns="1" />
          </UiCard>
          <UiCard title="Downloads">
            <ul class="flex flex-col gap-2 text-sm" data-test="submission-downloads">
              <li><a :href="currentUrl" class="link link-primary inline-flex items-center gap-1" download data-test="download-current"><UiIcon name="mdi-download" size="sm" />Current document (version {{ sub.current_version ?? 0 }})</a></li>
              <li v-if="finalUrl"><a :href="finalUrl" class="link link-primary inline-flex items-center gap-1" download data-test="download-final"><UiIcon name="mdi-download" size="sm" />Final signed document</a></li>
              <li v-if="auditUrl"><a :href="auditUrl" class="link link-primary inline-flex items-center gap-1" download data-test="download-audit"><UiIcon name="mdi-download" size="sm" />Audit trail</a></li>
              <li v-else-if="sub.status === 'completed'" class="text-xs text-base-content/70">The audit trail is being generated.</li>
            </ul>
          </UiCard>
        </aside>
      </div>
    </div>

    <UiDialog v-model="cancelDlg.open" :title="`Cancel ${sub?.name ?? ''}`" size="sm" data-test="cancel-dialog">
      <div class="flex flex-col gap-3">
        <UiAlert v-if="cancelDlg.error" kind="error" data-test="cancel-error">{{ cancelDlg.error }}</UiAlert>
        <p class="text-sm text-base-content/80">Signers who have not signed can no longer sign. The reason is sent to them.</p>
        <UiTextarea id="cancel-reason" v-model="cancelDlg.reason" label="Reason" required :rows="3" data-test="cancel-reason" />
      </div>
      <template #actions>
        <UiButton variant="text" @click="cancelDlg.open = false">Keep it</UiButton>
        <UiButton color="warning" icon="mdi-cancel" :loading="cancelDlg.saving" data-test="cancel-confirm" @click="doCancel">Cancel submission</UiButton>
      </template>
    </UiDialog>

    <UiDialog v-model="replaceDlg.open" :title="`Replace ${replaceDlg.signer?.name ?? 'signer'}`" size="sm" data-test="replace-dialog">
      <div class="flex flex-col gap-3">
        <UiAlert v-if="replaceDlg.error" kind="error" data-test="replace-error">{{ replaceDlg.error }}</UiAlert>
        <p class="text-sm text-base-content/80">The new signer takes over the {{ replaceDlg.signer?.party }} party and gets the invitation when it is their turn.</p>
        <UserPicker v-if="replaceDlg.open" id="replace-user" v-model="replaceDlg.user" label="New signer" required :exclude="otherSigners" data-test="replace-user" />
      </div>
      <template #actions>
        <UiButton variant="text" @click="replaceDlg.open = false">Cancel</UiButton>
        <UiButton icon="mdi-check" :loading="replaceDlg.saving" data-test="replace-confirm" @click="doReplace">Replace</UiButton>
      </template>
    </UiDialog>
  </UiPage>
</template>
