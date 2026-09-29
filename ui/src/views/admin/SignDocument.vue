<script setup lang="ts">
// "Sign a document" with an administrator certificate: a PDF (uploaded, or a
// stored submission version), an active administrator certificate, the
// optional reason / location / contact, and an optional timestamp authority
// (URL plus the Warden secret id holding its credentials — signing has no
// picker for Warden secrets, so the id is typed). The signed copy is kept for
// an hour; the download link shows until when. Refusals land on the input they
// name (tsa_url, tsa_secret_ref, the certificate, the document).
import { computed, onMounted, reactive, ref } from 'vue'
import { UiAlert, UiButton, UiCard, UiIcon, UiInput, UiSelect, type SelectOption } from '@go-tangra/ui'
import { ApiError, describe, refusalField } from '@/api/client'
import type { Certificate, DocumentSource, SignedDocument } from '@/api/types'
import { useCertificates } from '@/stores/admin'
import { when } from '@/utils/format'
import DocumentSourcePicker from '@/components/DocumentSourcePicker.vue'

const store = useCertificates()

const certs = ref<Certificate[]>([])
const certsError = ref('')
async function loadCerts(): Promise<void> {
  certsError.value = ''
  try {
    certs.value = (await store.signingCertificates()).filter((c) => c.kind === 'admin' && c.status === 'active')
  } catch (e) {
    certsError.value = describe(e)
  }
}
onMounted(() => void loadCerts())
defineExpose({ reloadCertificates: loadCerts })

const certOptions = computed<SelectOption[]>(() => certs.value.map((c) => ({ title: `${c.subject_cn} · serial ${c.serial} · until ${new Date(c.not_after).toLocaleDateString()}`, value: c.id })))

const source = ref<DocumentSource | null>(null)
const draft = reactive({ certificate_id: '', reason: '', location: '', contact: '', tsa_url: '', tsa_secret_ref: '' })
const errors = ref<Record<string, string>>({})
const banner = ref('')
const saving = ref(false)
const signed = ref<SignedDocument | null>(null)

function check(): boolean {
  const e: Record<string, string> = {}
  if (!source.value) e.source = 'Choose a PDF or a submission.'
  if (!draft.certificate_id) e.certificate_id = 'Choose an administrator certificate.'
  const url = draft.tsa_url.trim()
  if (url && (!/^https?:\/\/\S+$/i.test(url) || url.length > 500)) e.tsa_url = 'Enter an http(s) URL.'
  for (const k of ['reason', 'location', 'contact'] as const) if (draft[k].trim().length > 200) e[k] = 'At most 200 characters.'
  errors.value = e
  return Object.keys(e).length === 0
}

/** Wording of a refusal in this panel (the registered wording speaks of the personal certificate). */
function wording(e: unknown, field: string | undefined): string {
  const reason = e instanceof ApiError ? e.reason : ''
  if (reason === 'certificate_unusable') return 'That administrator certificate is expired or revoked.'
  if (reason === 'forbidden' && field === 'tsa_secret_ref') return 'You may not use that Warden secret (or it does not exist).'
  if (reason === 'validation_failed' && field === 'tsa_url') return 'The timestamp authority URL is not accepted.'
  if (reason === 'validation_failed' && field === 'tsa_secret_ref') return 'No usable Warden secret with that id.'
  return describe(e)
}

async function sign(): Promise<void> {
  banner.value = ''
  signed.value = null
  if (!check() || !source.value) return
  saving.value = true
  try {
    signed.value = await store.signDocument(source.value, { ...draft })
  } catch (e) {
    const field = refusalField(e)
    const reason = e instanceof ApiError ? e.reason : ''
    const msg = wording(e, field)
    if (field && ['certificate_id', 'reason', 'location', 'contact', 'tsa_url', 'tsa_secret_ref'].includes(field)) errors.value = { [field]: msg }
    else if (field === 'file' || field === 'submission_id' || field === 'version' || reason === 'invalid_pdf' || reason === 'payload_too_large') errors.value = { source: msg }
    else if (reason === 'certificate_unusable') errors.value = { certificate_id: msg }
    else banner.value = msg
    if (reason === 'certificate_unusable') void loadCerts()
  } finally {
    saving.value = false
  }
}

const downloadUrl = computed(() => (signed.value ? store.documentUrl(signed.value.id) : ''))
</script>

<template>
  <UiCard title="Sign a document" subtitle="Sign a PDF with an administrator certificate" data-test="doc-sign">
    <div class="flex flex-col gap-3">
      <UiAlert v-if="banner" kind="error" data-test="doc-sign-error">{{ banner }}</UiAlert>
      <UiAlert v-if="signed" kind="success" title="Document signed" data-test="doc-sign-done">
        <p>
          <a :href="downloadUrl" class="link link-primary inline-flex items-center gap-1" download data-test="doc-sign-download"><UiIcon name="mdi-download" size="sm" />Download the signed PDF</a>
        </p>
        <p class="text-sm">The link works for one hour<template v-if="signed.expires_at">, until {{ when(signed.expires_at) }}</template>.</p>
      </UiAlert>

      <DocumentSourcePicker id-prefix="doc-sign" :error="errors.source" :disabled="saving" @update:model-value="source = $event" />

      <UiAlert v-if="certsError" kind="error" data-test="doc-sign-certs-error">{{ certsError }}</UiAlert>
      <UiSelect
        id="doc-sign-cert"
        v-model="draft.certificate_id"
        label="Administrator certificate"
        :options="certOptions"
        :placeholder="certOptions.length ? 'Choose a certificate' : 'No active administrator certificate'"
        required
        :hint="certOptions.length ? undefined : 'Create an administrator certificate first.'"
        :error="errors.certificate_id || undefined"
        data-test="doc-sign-cert"
      />
      <div class="grid grid-cols-1 gap-3 md:grid-cols-3">
        <UiInput id="doc-sign-reason" v-model="draft.reason" label="Reason" hint="Optional" :error="errors.reason || undefined" data-test="doc-sign-reason" />
        <UiInput id="doc-sign-location" v-model="draft.location" label="Location" hint="Optional" :error="errors.location || undefined" data-test="doc-sign-location" />
        <UiInput id="doc-sign-contact" v-model="draft.contact" label="Contact" hint="Optional" :error="errors.contact || undefined" data-test="doc-sign-contact" />
      </div>
      <div class="grid grid-cols-1 gap-3 md:grid-cols-2">
        <UiInput id="doc-sign-tsa-url" v-model="draft.tsa_url" label="Timestamp authority URL" type="url" placeholder="https://tsa.example.com/tsr" hint="Optional. Adds a trusted timestamp (RFC 3161)." :error="errors.tsa_url || undefined" data-test="doc-sign-tsa-url" />
        <UiInput id="doc-sign-tsa-secret" v-model="draft.tsa_secret_ref" label="TSA credentials (Warden secret id)" hint="Optional. The id of the Warden secret with the timestamp authority's user name and password (and its URL, when the field above is empty)." :error="errors.tsa_secret_ref || undefined" data-test="doc-sign-tsa-secret" />
      </div>
      <div class="flex justify-end">
        <UiButton icon="mdi-file-certificate-outline" :loading="saving" data-test="doc-sign-submit" @click="sign">Sign document</UiButton>
      </div>
    </div>
  </UiCard>
</template>
