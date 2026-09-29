<script setup lang="ts">
// My signing certificate: the personal certificate the tenant's signing CA
// issues for signing documents, protected by a PIN the user chooses. Without
// one (or after revoking it) the page sets it up (PIN twice); with one it shows
// the details, changes the PIN, renews (PIN) and revokes (confirmed), and lists
// the documents signed with it. The PIN rules are the server's; its refusals
// are shown as they come. Opened from the signing page (?return=…), it leads
// back to the document once the certificate is ready.
import { computed, inject, onMounted, reactive } from 'vue'
import { routeLocationKey, routerKey } from 'vue-router'
import { UiAlert, UiButton, UiCard, UiDataTable, UiKeyValueTable, UiPage, UiSecretField, UiSkeleton, UiStatusChip, useConfirm, useToast, type Column, type KeyValue } from '@go-tangra/ui'
import { ApiError, describe, describeReason, describeRefusal, refusalDetail } from '@/api/client'
import type { SignedEntry } from '@/api/types'
import { useMyCertificate } from '@/stores/certificate'
import { when } from '@/utils/format'
import PinDialog from '@/components/PinDialog.vue'

const store = useMyCertificate()
const confirm = useConfirm()
const toast = useToast()
const route = inject(routeLocationKey, null)
const router = inject(routerKey, null)

onMounted(() => void store.load())

const cert = computed(() => store.certificate)
const needsSetup = computed(() => store.loaded && (!cert.value || cert.value.status === 'revoked'))
const usable = computed(() => cert.value?.status === 'active')
/** Where to go back to after setting up (only a signing page of this module). */
const back = computed(() => {
  const r = route?.query.return
  return typeof r === 'string' && /^\/signing\/sign\/[A-Za-z0-9-]{1,64}$/.test(r) ? r : ''
})

const STATUS_LABELS: Record<string, string> = { active: 'Active', revoked: 'Revoked', expired: 'Expired', needs_reissue: 'Needs renewal' }
const STATUS_COLORS = { active: 'success', revoked: 'error', expired: 'neutral', needs_reissue: 'warning' } as const

const details = computed<KeyValue[]>(() => {
  const c = cert.value
  if (!c) return []
  const out: KeyValue[] = [
    { label: 'Subject', value: c.subject_cn },
  ]
  if (c.email) out.push({ label: 'E-mail', value: c.email })
  out.push({ label: 'Serial number', value: c.serial, copyable: true })
  if (c.issuer_cn) out.push({ label: 'Issuer', value: c.issuer_cn })
  out.push({ label: 'Valid from', value: when(c.not_before) }, { label: 'Valid until', value: when(c.not_after) })
  if (c.fingerprint_sha256) out.push({ label: 'SHA-256 fingerprint', value: c.fingerprint_sha256, copyable: true })
  if (c.locked_until && new Date(c.locked_until).getTime() > Date.now()) out.push({ label: 'Locked until', value: when(c.locked_until) })
  if (c.revoked_at) out.push({ label: 'Revoked', value: when(c.revoked_at) })
  return out
})

// --- setup ---
const setup = reactive({ pin: '', pin2: '', errors: {} as Record<string, string>, error: '', saving: false })
async function doSetup(): Promise<void> {
  const e: Record<string, string> = {}
  if (!setup.pin) e.pin = 'Choose a PIN.'
  if (setup.pin && setup.pin2 !== setup.pin) e.pin2 = 'The PINs do not match.'
  setup.errors = e
  setup.error = ''
  if (Object.keys(e).length) return
  setup.saving = true
  try {
    await store.setup(setup.pin)
    Object.assign(setup, { pin: '', pin2: '' })
    toast.show({ kind: 'success', title: 'Certificate ready', text: 'Remember your PIN: it is needed for every signature.' })
  } catch (err) {
    setup.error = describeRefusal(err)
  } finally {
    setup.saving = false
  }
}

// --- change PIN ---
const change = reactive({ old: '', pin: '', pin2: '', errors: {} as Record<string, string>, error: '', saving: false })
async function doChange(): Promise<void> {
  const e: Record<string, string> = {}
  if (!change.old) e.old = 'Enter your current PIN.'
  if (!change.pin) e.pin = 'Choose a new PIN.'
  else if (change.pin2 !== change.pin) e.pin2 = 'The PINs do not match.'
  change.errors = e
  change.error = ''
  if (Object.keys(e).length) return
  change.saving = true
  try {
    await store.changePin(change.old, change.pin)
    Object.assign(change, { old: '', pin: '', pin2: '' })
    toast.show({ kind: 'success', title: 'PIN changed' })
  } catch (err) {
    change.error = pinMessage(err)
    if (err instanceof ApiError && err.reason === 'certificate_locked') void store.load()
  } finally {
    change.saving = false
  }
}

/** A PIN refusal with the attempts left or the lock end. */
function pinMessage(err: unknown): string {
  if (err instanceof ApiError && err.reason === 'pin_invalid') {
    const left = refusalDetail(err, 'attempts_left')
    return typeof left === 'number' ? `${describe(err)} ${left} attempt${left === 1 ? '' : 's'} left before the certificate is locked.` : describe(err)
  }
  if (err instanceof ApiError && err.reason === 'certificate_locked') {
    const until = refusalDetail(err, 'locked_until')
    return typeof until === 'string' && until ? `${describe(err)} Try again after ${when(until)}.` : describe(err)
  }
  return describeRefusal(err)
}

// --- renew ---
const renew = reactive({ open: false, error: '', attemptsLeft: null as number | null, lockedUntil: '', saving: false })
function askRenew(): void {
  Object.assign(renew, { open: true, error: '', attemptsLeft: null, lockedUntil: '', saving: false })
}
async function doRenew(pin: string): Promise<void> {
  renew.saving = true
  try {
    await store.renew(pin)
    renew.open = false
    toast.show({ kind: 'success', title: 'Certificate renewed' })
  } catch (err) {
    if (err instanceof ApiError && err.reason === 'pin_invalid') {
      renew.error = describeReason('pin_invalid')
      const left = refusalDetail(err, 'attempts_left')
      renew.attemptsLeft = typeof left === 'number' ? left : null
    } else if (err instanceof ApiError && err.reason === 'certificate_locked') {
      renew.lockedUntil = String(refusalDetail(err, 'locked_until') ?? '') || 'later'
    } else {
      renew.error = describeRefusal(err)
      renew.attemptsLeft = null
    }
  } finally {
    renew.saving = false
  }
}

// --- revoke ---
const revoking = reactive({ busy: false, error: '' })
async function doRevoke(): Promise<void> {
  if (!(await confirm.ask({ title: 'Revoke your signing certificate?', text: 'It can no longer sign. Documents you already signed stay valid. You can set up a new certificate afterwards.', danger: true, confirmLabel: 'Revoke' }))) return
  revoking.busy = true
  revoking.error = ''
  try {
    await store.revoke()
    toast.show({ kind: 'success', title: 'Certificate revoked' })
  } catch (err) {
    revoking.error = describeRefusal(err)
  } finally {
    revoking.busy = false
  }
}

// --- signed documents ---
type Row = SignedEntry & Record<string, unknown>
const columns: Column<Row>[] = [
  { key: 'submission_name', label: 'Document', format: (r) => r.submission_name ?? '' },
  { key: 'signed_at', label: 'Signed', format: (r) => when(r.signed_at) },
]
const rows = computed(() => store.signed.map((s, i) => ({ ...s, key: s.submission_id ?? String(i) })) as Row[])
</script>

<template>
  <UiPage title="My signing certificate" subtitle="Your personal certificate for signing documents, protected by your PIN">
    <template #badges>
      <UiStatusChip v-if="cert" :status="cert.status" :label="STATUS_LABELS[cert.status] ?? cert.status" :colors="STATUS_COLORS" data-test="cert-status" />
    </template>

    <UiSkeleton v-if="store.loading && !store.loaded" kind="card" :lines="6" />
    <UiAlert v-else-if="store.error && !store.loaded" kind="error" data-test="cert-load-error">{{ store.error }}</UiAlert>

    <div v-else class="flex min-w-0 flex-col gap-4">
      <UiAlert v-if="back && usable" kind="success" title="Your certificate is ready" data-test="cert-return">
        <p>Go back to the document you were signing.</p>
        <UiButton size="sm" class="mt-2" icon="mdi-arrow-left" data-test="cert-return-button" @click="router?.push(back)">Back to the document</UiButton>
      </UiAlert>
      <UiAlert v-if="store.error" kind="error">{{ store.error }}</UiAlert>

      <UiCard v-if="needsSetup" title="Set up your certificate" data-test="cert-setup">
        <div class="flex max-w-md flex-col gap-3">
          <p class="text-sm text-base-content/80">
            <template v-if="cert?.status === 'revoked'">Your previous certificate is revoked. </template>
            Choose a PIN. You enter it every time you sign; nobody else — not even an administrator — can sign with your certificate.
          </p>
          <UiAlert v-if="setup.error" kind="error" data-test="cert-setup-error">{{ setup.error }}</UiAlert>
          <UiSecretField id="cert-setup-pin" v-model="setup.pin" label="PIN" hint="Usually at least 6 characters; the server tells you if it is too weak." required autocomplete="new-password" :error="setup.errors.pin" data-test="cert-setup-pin" />
          <UiSecretField id="cert-setup-pin2" v-model="setup.pin2" label="Repeat the PIN" required autocomplete="new-password" :error="setup.errors.pin2" data-test="cert-setup-pin2" @enter="doSetup" />
          <div><UiButton icon="mdi-key-plus" :loading="setup.saving" data-test="cert-setup-submit" @click="doSetup">Create certificate</UiButton></div>
        </div>
      </UiCard>

      <div v-if="cert" class="grid min-w-0 grid-cols-1 gap-4 lg:grid-cols-2">
        <UiCard title="Certificate" data-test="cert-details">
          <UiAlert v-if="cert.status === 'needs_reissue'" kind="warning" class="mb-3">The signing CA changed: renew your certificate to keep signing.</UiAlert>
          <UiAlert v-else-if="cert.status === 'expired'" kind="warning" class="mb-3">Your certificate has expired. Renew it to sign again.</UiAlert>
          <UiKeyValueTable :items="details" :columns="1" />
          <UiAlert v-if="revoking.error" kind="error" class="mt-3" data-test="cert-revoke-error">{{ revoking.error }}</UiAlert>
          <div v-if="cert.status !== 'revoked'" class="mt-4 flex flex-wrap gap-2">
            <UiButton variant="soft" icon="mdi-autorenew" data-test="cert-renew" @click="askRenew">Renew</UiButton>
            <UiButton variant="soft" color="error" icon="mdi-shield-off-outline" :loading="revoking.busy" data-test="cert-revoke" @click="doRevoke">Revoke</UiButton>
          </div>
        </UiCard>

        <UiCard v-if="cert.status !== 'revoked'" title="Change PIN" data-test="cert-change">
          <div class="flex flex-col gap-3">
            <UiAlert v-if="change.error" kind="error" data-test="cert-change-error">{{ change.error }}</UiAlert>
            <UiSecretField id="cert-old-pin" v-model="change.old" label="Current PIN" required autocomplete="current-password" :error="change.errors.old" data-test="cert-old-pin" />
            <UiSecretField id="cert-new-pin" v-model="change.pin" label="New PIN" required autocomplete="new-password" :error="change.errors.pin" data-test="cert-new-pin" />
            <UiSecretField id="cert-new-pin2" v-model="change.pin2" label="Repeat the new PIN" required autocomplete="new-password" :error="change.errors.pin2" data-test="cert-new-pin2" @enter="doChange" />
            <div><UiButton icon="mdi-key-variant" :loading="change.saving" data-test="cert-change-submit" @click="doChange">Change PIN</UiButton></div>
          </div>
        </UiCard>
      </div>

      <UiCard v-if="store.loaded" title="Documents signed with your certificate" :padded="false">
        <UiDataTable :items="rows" :columns="columns" row-key="key" caption="Documents you signed with this certificate" empty-title="No signed documents yet" data-test="cert-signed" />
      </UiCard>
    </div>

    <PinDialog
      v-model="renew.open"
      title="Renew your certificate"
      text="A new certificate replaces the current one; your PIN stays the same."
      confirm-label="Renew"
      :loading="renew.saving"
      :error="renew.error"
      :attempts-left="renew.attemptsLeft"
      :locked-until="renew.lockedUntil"
      @submit="doRenew"
    />
  </UiPage>
</template>
