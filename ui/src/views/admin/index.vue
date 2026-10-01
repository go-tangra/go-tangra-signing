<script setup lang="ts">
// Certificate administration (certificates:manage): every certificate of the
// tenant (CA, system, signer, administrator) filtered by kind, status and a
// search; a row opens its details (PEM, revocation). Administrators create
// administrator certificates, download the CA's CRL, and sign documents with
// an administrator certificate (panel below the list). With backup:manage the
// tenant backup (export / import) is at the bottom of the page.
import { computed, onMounted, reactive, ref } from 'vue'
import { useAbility } from '@casl/vue'
import { UiAlert, UiButton, UiCard, UiDataTable, UiDialog, UiEmptyState, UiIcon, UiInput, UiPage, UiSelect, UiStatusChip, useToast, type Column, type SelectOption } from '@go-tangra/ui'
import { describe, refusalField } from '@/api/client'
import type { Certificate, CertificateKind, CertificateStatus } from '@/api/types'
import { CERTIFICATE_KINDS, CERTIFICATE_STATUSES } from '@/api/types'
import { PAGE_SIZE, useCertificates } from '@/stores/admin'
import { useServerTable } from '@/composables/useServerTable'
import { CERT_KIND_LABELS, CERT_STATUS_COLORS, CERT_STATUS_LABELS } from '@/utils/certificates'
import { when } from '@/utils/format'
import CertificateDrawer from './drawer.vue'
import SignDocument from './SignDocument.vue'
import BackupPanel from './BackupPanel.vue'

const store = useCertificates()
const ability = useAbility()
const toast = useToast()

const canManage = computed(() => ability.can('manage', 'SigningCertificate'))
const canBackup = computed(() => ability.can('manage', 'SigningBackup'))

const kindOptions: SelectOption[] = CERTIFICATE_KINDS.map((k) => ({ title: CERT_KIND_LABELS[k], value: k }))
const statusOptions: SelectOption[] = CERTIFICATE_STATUSES.map((s) => ({ title: CERT_STATUS_LABELS[s], value: s }))

const f = reactive({ q: '', kind: '' as CertificateKind | '', status: '' as CertificateStatus | '' })
const filters = () => ({ q: f.q.trim() || undefined, kind: f.kind || undefined, status: f.status || undefined })
// Server-paged and server-sorted; page, size and sort are kept in the URL.
const table = useServerTable('certificates', { sortable: ['subject', 'kind', 'status', 'not_after', 'created_at'], defaultSort: { key: 'created_at', dir: 'desc' }, defaultSize: PAGE_SIZE },
  (q) => (canManage.value ? store.list(filters(), q) : Promise.resolve(null)))
const lq = table.lq
/** A filter changed: back to the first page. */
function apply(): void {
  table.search()
}
function setKind(v: unknown): void {
  f.kind = CERTIFICATE_KINDS.includes(v as CertificateKind) ? (v as CertificateKind) : ''
  apply()
}
function setStatus(v: unknown): void {
  f.status = CERTIFICATE_STATUSES.includes(v as CertificateStatus) ? (v as CertificateStatus) : ''
  apply()
}

onMounted(() => { if (canManage.value) void table.reload() })
const crlUrl = computed(() => store.crlUrl())

// --- details ---
const selected = ref<Certificate | null>(null)
const drawerOpen = ref(false)
function open(c: Certificate): void {
  selected.value = c
  drawerOpen.value = true
}
const signPanel = ref<InstanceType<typeof SignDocument> | null>(null)
function onRevoked(): void {
  void store.reload()
  void signPanel.value?.reloadCertificates()
}

// --- new administrator certificate ---
const VALIDITY: SelectOption[] = [1, 2, 3, 4, 5].map((y) => ({ title: y === 1 ? '1 year' : `${y} years`, value: String(y) }))
const create = reactive({ open: false, subject_cn: '', email: '', years: '2', errors: {} as Record<string, string>, error: '', saving: false })
function askCreate(): void {
  Object.assign(create, { open: true, subject_cn: '', email: '', years: '2', errors: {}, error: '', saving: false })
}
async function doCreate(): Promise<void> {
  const e: Record<string, string> = {}
  const cn = create.subject_cn.trim()
  const email = create.email.trim()
  const years = Number(create.years)
  if (!cn) e.subject_cn = 'Enter a name.'
  else if (cn.length > 120) e.subject_cn = 'At most 120 characters.'
  if (email && (email.length > 254 || !/^[^\s@]+@[^\s@]+\.[^\s@]+$/.test(email))) e.email = 'Enter a valid e-mail address.'
  if (!Number.isInteger(years) || years < 1 || years > 5) e.validity_years = 'Between 1 and 5 years.'
  create.errors = e
  create.error = ''
  if (Object.keys(e).length) return
  create.saving = true
  try {
    const c = await store.create({ subject_cn: cn, ...(email ? { email } : {}), validity_years: years })
    create.open = false
    toast.show({ kind: 'success', title: 'Administrator certificate created', text: `${c.subject_cn}, valid until ${when(c.not_after)}.` })
    void signPanel.value?.reloadCertificates()
  } catch (err) {
    const field = refusalField(err)
    if (field && ['subject_cn', 'email', 'validity_years'].includes(field)) create.errors = { [field]: describe(err) }
    else create.error = describe(err)
  } finally {
    create.saving = false
  }
}

// --- table ---
type Row = Certificate & Record<string, unknown>
const columns: Column<Row>[] = [
  { key: 'subject', label: 'Subject', sortable: true },
  { key: 'kind', label: 'Kind', width: 'sm', sortable: true, format: (c) => CERT_KIND_LABELS[c.kind] },
  { key: 'status', label: 'Status', width: 'sm', sortable: true },
  { key: 'serial', label: 'Serial', hideOnStack: true },
  { key: 'not_after', label: 'Valid until', hideOnStack: true, sortable: true, defaultDir: 'desc', format: (c) => when(c.not_after) },
  { key: 'created_at', label: 'Issued', hideOnStack: true, sortable: true, defaultDir: 'desc', format: (c) => when(c.created_at) },
]
const rows = computed(() => store.items as Row[])
</script>

<template>
  <UiPage title="Certificates" subtitle="The tenant's signing CA and every certificate it issued">
    <template v-if="canManage" #actions>
      <UiButton icon="mdi-plus" data-test="cert-new" @click="askCreate">New administrator certificate</UiButton>
      <a :href="crlUrl" class="btn btn-soft btn-secondary" download data-test="cert-crl"><UiIcon name="mdi-download" size="sm" />Download CRL</a>
      <UiButton variant="text" icon="mdi-refresh" icon-only label="Refresh" @click="store.reload()" />
    </template>

    <UiEmptyState v-if="!canManage && !canBackup" icon="mdi-shield-off-outline" title="Not available" text="Managing certificates needs the certificates:manage permission." data-test="cert-forbidden" />

    <div v-else class="flex min-w-0 flex-col gap-3">
      <template v-if="canManage">
        <UiCard>
          <div class="grid grid-cols-2 gap-2 md:grid-cols-12 md:items-end" data-test="cert-filters">
            <div class="col-span-2 md:col-span-6"><UiInput id="cert-filter-q" v-model="f.q" label="Search subject, e-mail or serial" type="search" size="sm" data-test="cert-filter-q" @enter="apply()" /></div>
            <div class="md:col-span-3"><UiSelect id="cert-filter-kind" :model-value="f.kind" label="Kind" :options="kindOptions" placeholder="Any" size="sm" data-test="cert-filter-kind" @update:model-value="setKind" /></div>
            <div class="md:col-span-3"><UiSelect id="cert-filter-status" :model-value="f.status" label="Status" :options="statusOptions" placeholder="Any" size="sm" data-test="cert-filter-status" @update:model-value="setStatus" /></div>
          </div>
        </UiCard>

        <UiAlert v-if="store.error" kind="error" data-test="cert-error">{{ store.error }}</UiAlert>

        <UiCard :padded="false">
          <UiDataTable
            :items="rows"
            :columns="columns"
            :total="store.total"
            :page="lq.page.value"
            :page-size="lq.pageSize.value"
            :sort="lq.sort.value"
            :loading="store.loading"
            caption="Certificates — select one to see its details"
            empty-title="No certificates"
            empty-text="No certificate matches."
            clickable
            :row-attrs="(c) => ({ 'data-test': 'cert-row-' + c.id })"
            data-test="certs-table"
            @update:page="lq.setPage"
            @update:page-size="lq.setPageSize"
            @update:sort="lq.setSort"
            @row-click="open($event)"
          >
            <template #cell-subject="{ row }">
              <span class="font-medium">{{ row.subject_cn }}</span>
              <span v-if="row.email" class="block text-xs text-base-content/70">{{ row.email }}</span>
            </template>
            <template #cell-status="{ row }">
              <UiStatusChip :status="row.status" :label="CERT_STATUS_LABELS[row.status]" :colors="CERT_STATUS_COLORS" :data-test="'cert-status-' + row.id" />
            </template>
            <template #actions="{ row }">
              <div class="flex justify-end" @click.stop>
                <UiButton size="xs" variant="text" icon="mdi-eye-outline" icon-only label="Details" :data-test="'cert-open-' + row.id" @click="open(row)" />
              </div>
            </template>
          </UiDataTable>
        </UiCard>

        <SignDocument ref="signPanel" />
      </template>

      <BackupPanel v-if="canBackup" />
    </div>

    <CertificateDrawer v-if="canManage" :open="drawerOpen" :certificate="selected" @close="drawerOpen = false" @revoked="onRevoked" />

    <UiDialog v-model="create.open" title="New administrator certificate" size="sm" data-test="cert-create-dialog">
      <div class="flex flex-col gap-3">
        <UiAlert v-if="create.error" kind="error" data-test="cert-create-error">{{ create.error }}</UiAlert>
        <p class="text-sm text-base-content/80">Issued by the tenant's signing CA; used to sign documents from this page.</p>
        <UiInput id="cert-create-cn" v-model="create.subject_cn" label="Name (subject)" required :error="create.errors.subject_cn || undefined" data-test="cert-create-cn" />
        <UiInput id="cert-create-email" v-model="create.email" label="E-mail" type="email" hint="Optional" :error="create.errors.email || undefined" data-test="cert-create-email" />
        <UiSelect id="cert-create-years" v-model="create.years" label="Valid for" :options="VALIDITY" :error="create.errors.validity_years || undefined" data-test="cert-create-years" />
      </div>
      <template #actions>
        <UiButton variant="text" @click="create.open = false">Cancel</UiButton>
        <UiButton icon="mdi-check" :loading="create.saving" data-test="cert-create-submit" @click="doCreate">Create</UiButton>
      </template>
    </UiDialog>
  </UiPage>
</template>
