<script setup lang="ts">
// Signing page of one signer slot: the current document (pdf.js) with ONLY the
// signer's own fields laid over it as inputs, a side panel listing those fields
// with their validation, the signature pad (draw or type), and Sign (PIN of the
// personal certificate) / Decline. Banners explain why signing is not possible
// (not your turn, already signed, submission closed) and the certificate state
// (none → set it up and come back; locked → until when). An invited slot is
// marked opened once when the page loads. "Sign with qualified card" signs
// with a smart card through B-Trust BISS instead (BissButton).
import { computed, inject, nextTick, onBeforeUnmount, reactive, ref, watch } from 'vue'
import { routeLocationKey, routerKey } from 'vue-router'
import { UiAlert, UiBadge, UiButton, UiCard, UiDialog, UiErrorState, UiPage, UiSkeleton, UiTextarea, useToast } from '@go-tangra/ui'
import { describeReason, describeRefusal } from '@/api/client'
import type { Field, SignResult } from '@/api/types'
import { useSession } from '@/stores/session'
import { TYPE_LABELS } from '@/utils/fields'
import { when } from '@/utils/format'
import { MAX_IMAGE_BYTES, isSignatureLike, isUpload } from '@/utils/values'
import PdfPages from '@/components/PdfPages.vue'
import SignOverlay from '@/components/SignOverlay.vue'
import SignaturePad from '@/components/SignaturePad.vue'
import PinDialog from '@/components/PinDialog.vue'
import BissButton from '@/components/BissButton.vue'

const s = useSession()
const toast = useToast()
const route = inject(routeLocationKey, null)
const router = inject(routerKey, null)

const signerId = computed(() => (typeof route?.params.signerId === 'string' ? route.params.signerId : ''))
const pdfUrl = ref('')

watch(signerId, async (v) => {
  if (!v) return
  s.signature = null
  await s.load(v)
  if (s.session?.signer_id === v) {
    pdfUrl.value = s.documentUrl(v)
    void s.markOpened()
  }
}, { immediate: true })

const sess = computed(() => s.session)
const cert = computed(() => sess.value?.certificate)
const canSign = computed(() => !!sess.value?.can_sign)
const certUsable = computed(() => cert.value?.state === 'active')
const signed = computed(() => sess.value?.status === 'signed')
const declined = computed(() => sess.value?.status === 'declined')
const requiredSet = computed(() => new Set(s.fields.filter((f) => s.isRequired(f)).map((f) => f.id)))
const fileNames = computed(() => Object.fromEntries(Object.entries(s.uploads).map(([k, f]) => [k, f.name])))

/** Wording of why the slot cannot sign right now (the session's reason). */
const blocked = computed(() => {
  const r = sess.value?.reason
  if (canSign.value || !r) return ''
  if (r === 'already_signed' || signed.value) return ''
  return describeReason(r)
})

// --- certificate set-up round trip ---
const setupRoute = computed(() => ({ name: 'signing-certificate', query: { return: `/signing/sign/${signerId.value}` } }))
function setUpCertificate(): void {
  void router?.push(setupRoute.value)
}

// --- signature image ---
const signatureUrl = ref('')
function onSignature(png: Blob | null): void {
  s.signature = png
  if (signatureUrl.value) URL.revokeObjectURL(signatureUrl.value)
  signatureUrl.value = png && typeof URL.createObjectURL === 'function' ? URL.createObjectURL(png) : ''
  if (png) padError.value = ''
}
onBeforeUnmount(() => { if (signatureUrl.value) URL.revokeObjectURL(signatureUrl.value) })
const padCard = ref<HTMLElement | null>(null)
function goToPad(): void {
  padCard.value?.scrollIntoView({ block: 'center', behavior: 'smooth' })
  padCard.value?.querySelector<HTMLElement>('button')?.focus()
}
const padError = ref('')

// --- field list ---
const pages = ref<InstanceType<typeof PdfPages> | null>(null)
function focusField(f: Field): void {
  pages.value?.scrollToPage(f.page)
  setTimeout(() => document.getElementById('sign-field-' + f.id)?.focus(), 50)
}
function status(f: Field): string {
  if (s.errors[f.id]) return s.errors[f.id]!
  if (isSignatureLike(f.type)) return f.type === 'stamp' ? 'Filled from your certificate' : s.signature ? 'Signature ready' : 'From your signature'
  if (isUpload(f.type)) return s.uploads[f.id] ? s.uploads[f.id]!.name : ''
  const v = s.values[f.id] ?? ''
  return f.type === 'checkbox' ? (v === 'true' ? 'Checked' : '') : v
}
function onUpload(id: string, file: File | null): void {
  const f = s.fields.find((x) => x.id === id)
  if (file && f?.type === 'image' && (file.size > MAX_IMAGE_BYTES || !['image/png', 'image/jpeg'].includes(file.type))) {
    s.errors = { ...s.errors, [id]: 'Choose a PNG or JPEG image of at most 1 MB.' }
    return
  }
  s.setUpload(id, file)
}

// --- sign ---
const banner = ref('')
const certMissing = ref(false)
const pin = reactive({ open: false, error: '', attemptsLeft: null as number | null, lockedUntil: '' })

/** Checks the signer's fields and signature before either way of signing; highlights what is wrong. */
function checkFields(): boolean {
  banner.value = ''
  padError.value = ''
  const ok = s.validate()
  const needsPad = s.needsSignature && !s.signature
  if (needsPad) padError.value = 'Draw or type your signature.'
  if (!ok || needsPad) {
    banner.value = 'Check the highlighted fields before signing.'
    const first = s.fields.find((f) => s.errors[f.id])
    if (first) focusField(first)
    else if (needsPad) goToPad()
    return false
  }
  return true
}

function startSign(): void {
  if (!checkFields()) return
  if (cert.value?.state === 'none') {
    certMissing.value = true
    return
  }
  Object.assign(pin, { open: true, error: '', attemptsLeft: null, lockedUntil: cert.value?.state === 'locked' ? cert.value.locked_until ?? '' : '' })
}

async function submitPin(value: string): Promise<void> {
  const out = await s.sign(value)
  switch (out.kind) {
    case 'signed':
      pin.open = false
      toast.show({ kind: 'success', title: 'Document signed', ...(out.result.submission_status === 'completed' ? { text: 'Every party has signed; the document is complete.' } : {}) })
      break
    case 'pin_invalid':
      pin.error = describeReason('pin_invalid')
      pin.attemptsLeft = out.attemptsLeft
      break
    case 'locked':
      pin.lockedUntil = out.lockedUntil || 'later'
      break
    case 'certificate_missing':
      pin.open = false
      certMissing.value = true
      break
    case 'field': {
      pin.open = false
      banner.value = out.message
      const f = s.fields.find((x) => x.id === out.field)
      if (f) focusField(f)
      break
    }
    default:
      pin.open = false
      banner.value = out.message
  }
}

// --- qualified card (BISS) ---
const qesBusy = ref(false)
function onQesSigned(result: SignResult): void {
  toast.show({ kind: 'success', title: 'Document signed with your qualified card', ...(result.submission_status === 'completed' ? { text: 'Every party has signed; the document is complete.' } : {}) })
}
function onQesField(id: string): void {
  const f = s.fields.find((x) => x.id === id)
  if (f) focusField(f)
}
/** The document moved on meanwhile: reload the session and the PDF. */
async function onDocumentChanged(): Promise<void> {
  const id = signerId.value
  if (!id) return
  await s.load(id)
  pdfUrl.value = ''
  await nextTick()
  pdfUrl.value = s.documentUrl(id)
}

// --- decline ---
const decline = reactive({ open: false, reason: '', error: '', saving: false })
function askDecline(): void {
  Object.assign(decline, { open: true, reason: '', error: '', saving: false })
}
async function doDecline(): Promise<void> {
  const r = decline.reason.trim()
  if (!r) {
    decline.error = 'Tell the sender why you decline.'
    return
  }
  if (r.length > 500) {
    decline.error = 'At most 500 characters.'
    return
  }
  decline.saving = true
  decline.error = ''
  try {
    await s.decline(r)
    decline.open = false
    toast.show({ kind: 'info', title: 'You declined to sign', text: 'The sender is informed.' })
  } catch (e) {
    decline.error = describeRefusal(e)
  } finally {
    decline.saving = false
  }
}

async function retry(): Promise<void> {
  if (signerId.value) await s.load(signerId.value)
}
</script>

<template>
  <UiPage :title="sess?.submission_name ?? 'Sign document'" :subtitle="sess ? `You sign as ${sess.party} · document version ${sess.document_version}` : undefined">
    <template #before-title>
      <UiButton variant="text" icon="mdi-arrow-left" icon-only label="Back to documents to sign" data-test="sign-back" @click="router?.push({ name: 'signing-inbox' })" />
    </template>
    <template #badges>
      <UiBadge v-if="signed" color="success" data-test="sign-signed-badge">Signed</UiBadge>
      <UiBadge v-else-if="declined" color="error">Declined</UiBadge>
    </template>
    <template v-if="sess && canSign" #actions>
      <UiButton variant="soft" color="error" icon="mdi-cancel" data-test="sign-decline" @click="askDecline">Decline</UiButton>
    </template>

    <UiSkeleton v-if="s.loading && !sess" kind="card" :lines="8" />
    <UiErrorState v-else-if="!sess" :text="s.error || 'This document could not be loaded.'" data-test="sign-load-error" @retry="retry" />

    <div v-else class="flex min-w-0 flex-col gap-3">
      <UiAlert v-if="signed" kind="success" title="You signed this document" data-test="sign-done">
        The other parties are informed when it is their turn. The signed document is in “Signed by me”.
      </UiAlert>
      <UiAlert v-else-if="declined" kind="info" title="You declined to sign" data-test="sign-declined" />
      <UiAlert v-else-if="blocked" kind="info" :title="blocked" data-test="sign-blocked">
        <template v-if="sess.reason === 'not_your_turn'">You get an e-mail when the signers before you have signed.</template>
      </UiAlert>

      <UiAlert v-if="!signed && !declined && (certMissing || cert?.state === 'none')" kind="warning" title="Set up your signing certificate" data-test="sign-cert-missing">
        <p>Signing uses your personal certificate, protected by a PIN you choose. Set it up once, then come back to this document.</p>
        <UiButton size="sm" class="mt-2" icon="mdi-file-certificate-outline" data-test="sign-cert-setup" @click="setUpCertificate">Set up your certificate</UiButton>
      </UiAlert>
      <UiAlert v-else-if="!signed && !declined && cert?.state === 'locked'" kind="error" title="Your certificate is locked" data-test="sign-cert-locked">
        Too many wrong PINs. You can sign again after {{ when(cert.locked_until) || 'a while' }}.
      </UiAlert>
      <UiAlert v-else-if="!signed && !declined && (cert?.state === 'expired' || cert?.state === 'revoked')" kind="warning" :title="cert?.state === 'expired' ? 'Your certificate has expired' : 'Your certificate is revoked'" data-test="sign-cert-unusable">
        <p>Renew it or set up a new one on your certificate page, then come back.</p>
        <UiButton size="sm" class="mt-2" icon="mdi-file-certificate-outline" data-test="sign-cert-renew" @click="setUpCertificate">My certificate</UiButton>
      </UiAlert>

      <UiAlert v-if="banner" kind="error" data-test="sign-error">{{ banner }}</UiAlert>

      <div class="grid min-w-0 grid-cols-1 gap-4 lg:grid-cols-[minmax(0,1fr)_22rem]">
        <section class="min-w-0 overflow-auto rounded-box bg-base-200 p-2 md:p-4 lg:max-h-[calc(100vh-12rem)]" aria-label="Document pages">
          <PdfPages v-if="pdfUrl" ref="pages" :url="pdfUrl">
            <template #page="{ page }">
              <SignOverlay
                :page="page"
                :fields="canSign ? s.fields : []"
                :values="s.values"
                :files="fileNames"
                :errors="s.errors"
                :required="requiredSet"
                :signature-url="signatureUrl"
                :disabled="s.signing"
                @value="s.setValue"
                @upload="onUpload"
                @signature="goToPad"
              />
            </template>
          </PdfPages>
        </section>

        <aside v-if="canSign" class="flex min-w-0 flex-col gap-4">
          <UiCard :title="`Your fields (${s.fields.length})`">
            <p v-if="!s.fields.length" class="text-sm text-base-content/70">Nothing to fill in: just sign.</p>
            <ul v-else class="flex flex-col gap-0.5 text-sm" data-test="sign-field-list">
              <li v-for="f in s.fields" :key="f.id">
                <button type="button" class="flex w-full flex-col items-start rounded-field px-2 py-1 text-start hover:bg-base-200" :data-test="'sign-item-' + f.id" @click="focusField(f)">
                  <span class="flex w-full items-center gap-2">
                    <span class="min-w-0 grow truncate font-medium">{{ f.name }}<template v-if="requiredSet.has(f.id)"><span class="text-error" aria-hidden="true"> *</span><span class="sr-only"> (required)</span></template></span>
                    <span class="text-xs text-base-content/70">{{ TYPE_LABELS[f.type] }} · p{{ f.page }}</span>
                  </span>
                  <span v-if="status(f)" class="w-full truncate text-xs" :class="s.errors[f.id] ? 'text-error' : 'text-base-content/70'" :data-test="'sign-item-status-' + f.id">{{ status(f) }}</span>
                </button>
              </li>
            </ul>
          </UiCard>

          <div ref="padCard">
            <UiCard title="Your signature">
              <SignaturePad :disabled="s.signing" @change="onSignature" />
              <p v-if="padError" class="mt-2 text-xs text-error" role="alert" data-test="sign-pad-error">{{ padError }}</p>
              <p v-else-if="!s.needsSignature" class="mt-2 text-xs text-base-content/70">Optional: shown in the signature appearance.</p>
            </UiCard>
          </div>

          <UiCard title="Sign">
            <div class="flex flex-col gap-2">
              <UiButton block icon="mdi-key" :loading="s.signing" :disabled="qesBusy || (!certUsable && cert?.state !== 'none')" data-test="sign-submit" @click="startSign">Sign with my certificate</UiButton>
              <div data-test="sign-qes-slot">
                <BissButton :signer-id="signerId" :check="checkFields" :disabled="s.signing" @signed="onQesSigned" @field="onQesField" @changed="onDocumentChanged" @busy="qesBusy = $event" />
              </div>
            </div>
          </UiCard>
        </aside>
      </div>
    </div>

    <PinDialog
      v-model="pin.open"
      title="Sign with your certificate"
      text="Enter the PIN of your signing certificate."
      confirm-label="Sign"
      :loading="s.signing"
      :error="pin.error"
      :attempts-left="pin.attemptsLeft"
      :locked-until="pin.lockedUntil"
      @submit="submitPin"
    />

    <UiDialog v-model="decline.open" title="Decline to sign" size="sm" data-test="decline-dialog">
      <div class="flex flex-col gap-3">
        <UiAlert v-if="decline.error" kind="error" data-test="decline-error">{{ decline.error }}</UiAlert>
        <p class="text-sm text-base-content/80">The submission is cancelled for everyone and the sender gets your reason.</p>
        <UiTextarea id="decline-reason" v-model="decline.reason" label="Reason" required :rows="3" data-test="decline-reason" />
      </div>
      <template #actions>
        <UiButton variant="text" @click="decline.open = false">Back</UiButton>
        <UiButton color="error" icon="mdi-cancel" :loading="decline.saving" data-test="decline-confirm" @click="doDecline">Decline</UiButton>
      </template>
    </UiDialog>
  </UiPage>
</template>
