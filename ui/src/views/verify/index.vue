<script setup lang="ts">
// Verify a signed PDF (signing:read): upload a file, or pick a submission and
// one of its versions. Every signature is listed with its integrity (the
// signed bytes are unchanged), trust (the certificate chains to a trusted CA),
// revocation (checked against the CRL / OCSP) and method (tenant certificate
// or qualified card), under an overall verdict. A document changed after its
// last signature is called out and explained.
import { computed, ref } from 'vue'
import { useAbility } from '@casl/vue'
import { UiAlert, UiButton, UiCard, UiDataTable, UiEmptyState, UiPage, UiStatusChip, type Column } from '@go-tangra/ui'
import type { DocumentSource, VerifySignature } from '@/api/types'
import { useVerify } from '@/stores/verify'
import { when } from '@/utils/format'
import DocumentSourcePicker from '@/components/DocumentSourcePicker.vue'

const store = useVerify()
const ability = useAbility()
const canRead = computed(() => ability.can('read', 'SigningVerification'))

const source = ref<DocumentSource | null>(null)
const sourceError = ref('')

async function verify(): Promise<void> {
  sourceError.value = ''
  if (!source.value) {
    sourceError.value = 'Choose a PDF or a submission.'
    return
  }
  await store.verify(source.value)
}

const INTEGRITY: Record<string, string> = { valid: 'Intact', modified: 'Modified' }
const INTEGRITY_COLORS = { valid: 'success', modified: 'error' } as const
const TRUST: Record<string, string> = { trusted: 'Trusted', untrusted: 'Not trusted', unknown: 'Unknown' }
const TRUST_COLORS = { trusted: 'success', untrusted: 'error', unknown: 'warning' } as const
const REVOCATION: Record<string, string> = { good: 'Not revoked', revoked: 'Revoked', unknown: 'Not checked' }
const REVOCATION_COLORS = { good: 'success', revoked: 'error', unknown: 'warning' } as const
const METHOD: Record<string, string> = { tenant: 'Tenant certificate', qualified: 'Qualified card' }

const sigs = computed(() => store.result?.signatures ?? [])
const modifiedAfter = computed(() => !!store.result?.modified_after_last_signature)

type Verdict = { kind: 'success' | 'warning' | 'error'; title: string; text: string }
const verdict = computed<Verdict | null>(() => {
  const r = store.result
  if (!r) return null
  const list = r.signatures ?? []
  if (!list.length) return { kind: 'warning', title: 'No signatures found', text: 'This PDF carries no digital signature.' }
  if (list.some((s) => s.integrity === 'modified')) return { kind: 'error', title: 'Not valid: a signed part was modified', text: 'At least one signature no longer matches the document.' }
  if (list.some((s) => s.revocation === 'revoked')) return { kind: 'error', title: 'Not valid: a certificate is revoked', text: 'At least one signature was made with a revoked certificate.' }
  if (r.modified_after_last_signature) return { kind: 'warning', title: 'Signatures intact, but the document changed afterwards', text: 'See the explanation below.' }
  if (list.every((s) => s.trust === 'trusted' && s.revocation === 'good')) {
    return { kind: 'success', title: list.length === 1 ? 'The signature is valid' : `All ${list.length} signatures are valid`, text: 'Every signature is intact, from a trusted certificate that is not revoked.' }
  }
  return { kind: 'warning', title: 'Signatures intact, but not fully confirmed', text: 'Not every certificate could be confirmed as trusted and not revoked.' }
})

type Row = VerifySignature & Record<string, unknown> & { n: number }
const rows = computed<Row[]>(() => sigs.value.map((s, i) => ({ ...s, n: i + 1 }) as Row))
const columns: Column<Row>[] = [
  { key: 'n', label: '#', width: 'sm', format: (s) => String(s.n) },
  { key: 'signer', label: 'Signer' },
  { key: 'integrity', label: 'Integrity', width: 'sm' },
  { key: 'trust', label: 'Trust', width: 'sm' },
  { key: 'revocation', label: 'Revocation', width: 'sm' },
  { key: 'method', label: 'Method', hideOnStack: true, format: (s) => METHOD[s.method ?? ''] ?? (s.method || 'Other') },
  { key: 'issuer', label: 'Issued by', hideOnStack: true },
]
</script>

<template>
  <UiPage title="Verify a document" subtitle="Check the digital signatures of a PDF">
    <UiEmptyState v-if="!canRead" icon="mdi-shield-off-outline" title="Not available" text="Verifying documents needs the signing:read permission." data-test="verify-forbidden" />

    <div v-else class="flex min-w-0 flex-col gap-3">
      <UiCard title="Document">
        <div class="flex flex-col gap-3">
          <DocumentSourcePicker id-prefix="verify" :error="sourceError" :disabled="store.loading" @update:model-value="source = $event; sourceError = ''" />
          <div class="flex justify-end">
            <UiButton icon="mdi-shield-check-outline" :loading="store.loading" data-test="verify-submit" @click="verify">Verify</UiButton>
          </div>
        </div>
      </UiCard>

      <UiAlert v-if="store.error" kind="error" data-test="verify-error">{{ store.error }}</UiAlert>

      <template v-if="store.result && verdict">
        <UiAlert :kind="verdict.kind" :title="verdict.title" data-test="verify-verdict">{{ verdict.text }}</UiAlert>

        <UiAlert v-if="modifiedAfter" kind="warning" title="Modified after the last signature" data-test="verify-modified-after">
          Content was added to the PDF after its last signature (for example annotations, form changes or an extra page). The signed versions are still intact,
          but what the PDF shows now may differ from what was signed. To see exactly what the last signer signed, open the version that ends with that signature.
        </UiAlert>

        <UiCard v-if="rows.length" :padded="false">
          <UiDataTable :items="rows" :columns="columns" caption="Signatures in the document, oldest first" row-key="n" :row-attrs="(s) => ({ 'data-test': 'verify-row-' + s.n })" data-test="verify-table">
            <template #cell-signer="{ row }">
              <span class="font-medium">{{ row.signer || 'Unknown signer' }}</span>
              <span v-if="row.time" class="block text-xs text-base-content/70">{{ when(row.time) }}</span>
              <span v-if="row.reason || row.location" class="block text-xs text-base-content/70">{{ [row.reason, row.location].filter(Boolean).join(' · ') }}</span>
              <span v-if="row.serial" class="block text-xs text-base-content/70">Serial {{ row.serial }}</span>
            </template>
            <template #cell-integrity="{ row }">
              <UiStatusChip :status="row.integrity ?? 'unknown'" :label="INTEGRITY[row.integrity ?? ''] ?? 'Unknown'" :colors="INTEGRITY_COLORS" :data-test="'verify-integrity-' + row.n" />
            </template>
            <template #cell-trust="{ row }">
              <UiStatusChip :status="row.trust ?? 'unknown'" :label="TRUST[row.trust ?? 'unknown']" :colors="TRUST_COLORS" :data-test="'verify-trust-' + row.n" />
            </template>
            <template #cell-revocation="{ row }">
              <UiStatusChip :status="row.revocation ?? 'unknown'" :label="REVOCATION[row.revocation ?? 'unknown']" :colors="REVOCATION_COLORS" :data-test="'verify-revocation-' + row.n" />
            </template>
          </UiDataTable>
        </UiCard>

        <details class="text-sm text-base-content/80">
          <summary class="cursor-pointer">What the columns mean</summary>
          <ul class="mt-2 list-disc ps-5">
            <li><strong>Integrity</strong>: whether the bytes a signature covers are unchanged.</li>
            <li><strong>Trust</strong>: whether the certificate chains to this tenant's signing CA or a trusted qualified provider.</li>
            <li><strong>Revocation</strong>: whether the certificate was revoked (“Not checked” when no revocation information was available).</li>
          </ul>
        </details>
      </template>
    </div>
  </UiPage>
</template>
