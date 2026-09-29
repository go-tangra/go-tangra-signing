<script setup lang="ts">
// Details of one certificate (loaded with its PEM): subject, e-mail, serial,
// fingerprint, issuer, validity, status, revocation, PIN lock; the PEM can be
// copied. A certificate that is not the CA's and not revoked can be revoked
// with a reason after the kit confirmation.
import { computed, ref, watch } from 'vue'
import { UiAlert, UiButton, UiCopyButton, UiDrawer, UiKeyValueTable, UiSelect, UiSkeleton, UiStatusChip, useConfirm, useToast, type KeyValue, type SelectOption } from '@go-tangra/ui'
import { describeRefusal } from '@/api/client'
import type { Certificate, RevocationReason } from '@/api/types'
import { REVOCATION_REASONS } from '@/api/types'
import { useCertificates } from '@/stores/admin'
import { CERT_KIND_LABELS, CERT_STATUS_COLORS, CERT_STATUS_LABELS, REVOCATION_LABELS } from '@/utils/certificates'
import { when } from '@/utils/format'

const props = defineProps<{ open: boolean; certificate: Certificate | null }>()
const emit = defineEmits<{ (e: 'close'): void; (e: 'revoked', c: Certificate): void }>()

const store = useCertificates()
const confirm = useConfirm()
const toast = useToast()

const detail = ref<Certificate | null>(null)
const loading = ref(false)
const error = ref('')
const reason = ref<RevocationReason>('unspecified')
const revoking = ref(false)
const revokeError = ref('')

watch(() => [props.open, props.certificate?.id], async () => {
  if (!props.open || !props.certificate) return
  detail.value = props.certificate
  reason.value = 'unspecified'
  revokeError.value = ''
  error.value = ''
  loading.value = true
  const id = props.certificate.id
  try {
    const full = await store.get(id)
    if (props.certificate?.id === id) detail.value = full
  } catch (e) {
    error.value = describeRefusal(e)
  } finally {
    loading.value = false
  }
}, { immediate: true })

const c = computed(() => detail.value)
const reasonOptions: SelectOption[] = REVOCATION_REASONS.map((r) => ({ title: REVOCATION_LABELS[r], value: r }))
const canRevoke = computed(() => !!c.value && c.value.kind !== 'ca' && c.value.status !== 'revoked')

const items = computed<KeyValue[]>(() => {
  const x = c.value
  if (!x) return []
  const out: KeyValue[] = [
    { label: 'Subject', value: x.subject_cn },
    { label: 'Kind', value: CERT_KIND_LABELS[x.kind] },
  ]
  if (x.email) out.push({ label: 'E-mail', value: x.email })
  out.push({ label: 'Serial number', value: x.serial, copyable: true })
  if (x.fingerprint_sha256) out.push({ label: 'SHA-256 fingerprint', value: x.fingerprint_sha256, copyable: true })
  if (x.issuer_cn) out.push({ label: 'Issuer', value: x.issuer_cn })
  out.push({ label: 'Valid from', value: when(x.not_before) }, { label: 'Valid until', value: when(x.not_after) })
  if (x.revoked_at) out.push({ label: 'Revoked', value: when(x.revoked_at) })
  if (x.revocation_reason) out.push({ label: 'Revocation reason', value: REVOCATION_LABELS[x.revocation_reason as RevocationReason] ?? x.revocation_reason })
  if (x.locked_until && new Date(x.locked_until).getTime() > Date.now()) out.push({ label: 'PIN locked until', value: when(x.locked_until) })
  if (x.created_at) out.push({ label: 'Created', value: when(x.created_at) })
  return out
})

function setReason(v: unknown): void {
  reason.value = REVOCATION_REASONS.includes(v as RevocationReason) ? (v as RevocationReason) : 'unspecified'
}

async function revoke(): Promise<void> {
  const x = c.value
  if (!x || !canRevoke.value) return
  const ok = await confirm.ask({
    title: 'Revoke this certificate?',
    text: `${x.subject_cn} (serial ${x.serial}) can no longer sign, and the revocation is published in the CRL. Reason: ${REVOCATION_LABELS[reason.value]}. This cannot be undone.`,
    confirmLabel: 'Revoke',
    danger: true,
  })
  if (!ok) return
  revoking.value = true
  revokeError.value = ''
  try {
    const next = await store.revoke(x.id, reason.value)
    detail.value = { ...detail.value, ...next }
    toast.show({ kind: 'success', title: 'Certificate revoked' })
    emit('revoked', next)
  } catch (e) {
    revokeError.value = describeRefusal(e)
  } finally {
    revoking.value = false
  }
}
</script>

<template>
  <UiDrawer :model-value="open" title="Certificate" size="lg" data-test="cert-drawer" @update:model-value="emit('close')">
    <div v-if="c" class="flex flex-col gap-4">
      <div class="flex flex-wrap items-center gap-2">
        <span class="text-lg font-semibold">{{ c.subject_cn }}</span>
        <UiStatusChip :status="c.status" :label="CERT_STATUS_LABELS[c.status]" :colors="CERT_STATUS_COLORS" data-test="cert-drawer-status" />
      </div>
      <UiAlert v-if="error" kind="error" data-test="cert-drawer-error">{{ error }}</UiAlert>
      <div data-test="cert-drawer-details"><UiKeyValueTable :items="items" /></div>

      <section aria-labelledby="cert-pem-title" class="flex flex-col gap-2">
        <div class="flex items-center justify-between gap-2">
          <h3 id="cert-pem-title" class="font-medium">Certificate (PEM)</h3>
          <UiCopyButton v-if="c.pem" :value="c.pem" label="Copy PEM" size="xs" data-test="cert-pem-copy" />
        </div>
        <UiSkeleton v-if="loading && !c.pem" kind="text" :lines="4" />
        <pre v-else-if="c.pem" class="max-h-64 overflow-auto rounded-box bg-base-200 p-3 text-xs" data-test="cert-pem">{{ c.pem }}</pre>
        <p v-else class="text-sm text-base-content/70">Not available.</p>
      </section>

      <section v-if="canRevoke" aria-labelledby="cert-revoke-title" class="flex flex-col gap-2 rounded-box border border-error/30 p-3" data-test="cert-revoke-section">
        <h3 id="cert-revoke-title" class="font-medium">Revoke</h3>
        <p class="text-sm text-base-content/80">A revoked certificate can no longer sign; documents it signed show it as revoked when verified.</p>
        <UiAlert v-if="revokeError" kind="error" data-test="cert-revoke-error">{{ revokeError }}</UiAlert>
        <UiSelect id="cert-revoke-reason" :model-value="reason" label="Reason" :options="reasonOptions" data-test="cert-revoke-reason" @update:model-value="setReason" />
        <div><UiButton color="error" icon="mdi-cancel" :loading="revoking" data-test="cert-revoke" @click="revoke">Revoke certificate</UiButton></div>
      </section>
      <p v-else-if="c.kind === 'ca'" class="text-sm text-base-content/70" data-test="cert-ca-note">The tenant's certificate authority cannot be revoked here.</p>
    </div>
    <template #actions>
      <UiButton variant="text" @click="emit('close')">Close</UiButton>
    </template>
  </UiDrawer>
</template>
