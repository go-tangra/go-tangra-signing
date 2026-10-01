<script setup lang="ts">
// The caller's signing inbox: "To sign" (their slots waiting for them) and
// "Signed by me". A signing.inbox event on the module stream refreshes the
// open tab, so a new invitation or a finished signature shows up at once.
// Both tabs are server-paged and server-sorted (page, size and sort in the URL).
import { computed, inject, onBeforeUnmount, onMounted } from 'vue'
import { routerKey } from 'vue-router'
import { UiAlert, UiButton, UiCard, UiDataTable, UiLiveIndicator, UiPage, UiStatusChip, UiTabs, type Column, type TabItem } from '@go-tangra/ui'
import type { InboxItem, InboxState } from '@/api/types'
import { INBOX_PAGE_SIZE, useInbox } from '@/stores/inbox'
import { useServerTable } from '@/composables/useServerTable'
import { coalesce, useLive } from '@/stores/live'
import { when } from '@/utils/format'
import { SIGNER_STATUS_COLORS, SIGNER_STATUS_LABELS, SUBMISSION_STATUS_COLORS, SUBMISSION_STATUS_LABELS } from '@/utils/submission'

const store = useInbox()
const live = useLive()
const router = inject(routerKey, null)

const tabs = computed<TabItem[]>(() => [
  { key: 'to_sign', label: 'To sign', icon: 'mdi-inbox-outline', ...(store.state === 'to_sign' ? { count: store.total } : {}) },
  { key: 'signed', label: 'Signed by me', icon: 'mdi-check-circle-outline' },
])
const table = useServerTable('inbox', { sortable: ['created_at', 'title', 'status'], defaultSort: { key: 'created_at', dir: 'desc' }, defaultSize: INBOX_PAGE_SIZE },
  (q) => store.list(store.state, q))
const lq = table.lq
/** Another tab: its first page. */
function setTab(k: string): void {
  if (k !== 'to_sign' && k !== 'signed') return
  store.state = k as InboxState
  table.search()
}

const refresh = coalesce(() => void store.reload(), 300)
let release: (() => void) | null = null
let unsubscribe: (() => void) | null = null
onMounted(() => {
  void table.reload()
  release = live.connect()
  unsubscribe = live.on((e) => { if (e.type === 'signing.inbox') refresh.trigger() })
})
onBeforeUnmount(() => {
  refresh.cancel()
  unsubscribe?.()
  release?.()
})

function open(i: InboxItem): void {
  void router?.push({ name: 'signing-sign', params: { signerId: i.signer_id } })
}

const signerLabel = (s: string) => SIGNER_STATUS_LABELS[s as keyof typeof SIGNER_STATUS_LABELS] ?? s
const submissionLabel = (s: string | undefined) => (s ? SUBMISSION_STATUS_LABELS[s as keyof typeof SUBMISSION_STATUS_LABELS] ?? s : '')

type Row = InboxItem & Record<string, unknown>
const columns = computed<Column<Row>[]>(() => [
  { key: 'title', label: 'Document', sortable: true },
  // "signed" lists only signed slots and shows the document's status: nothing to sort by.
  { key: 'status', label: store.state === 'to_sign' ? 'Your status' : 'Document status', width: 'sm', sortable: store.state === 'to_sign' },
  { key: 'created_at', label: 'Sent', sortable: true, defaultDir: 'desc', hideOnStack: true, format: (i) => when(i.created_at) },
  { key: 'sender', label: 'From', hideOnStack: true, format: (i) => i.sender ?? '' },
  store.state === 'to_sign'
    ? { key: 'expires_at', label: 'Expires', hideOnStack: true, format: (i) => when(i.expires_at) }
    : { key: 'signed_at', label: 'Signed', format: (i) => when(i.signed_at) },
])
const rows = computed(() => store.items as Row[])
</script>

<template>
  <UiPage title="To sign" subtitle="Documents waiting for your signature and the ones you signed">
    <template #badges>
      <UiLiveIndicator :connected="live.connected" />
    </template>
    <template #actions>
      <UiButton variant="text" icon="mdi-refresh" icon-only label="Refresh" data-test="inbox-refresh" @click="store.reload()" />
    </template>

    <div class="flex min-w-0 flex-col gap-3">
      <UiTabs :model-value="store.state" :tabs="tabs" data-test="inbox-tabs" @update:model-value="setTab" />
      <UiAlert v-if="store.error" kind="error" data-test="inbox-error">{{ store.error }}</UiAlert>
      <UiCard :padded="false">
        <UiDataTable
          :items="rows"
          :columns="columns"
          row-key="signer_id"
          :total="store.total"
          :page="lq.page.value"
          :page-size="lq.pageSize.value"
          :sort="lq.sort.value"
          :loading="store.loading"
          :caption="store.state === 'to_sign' ? 'Documents waiting for your signature' : 'Documents you signed'"
          :empty-title="store.state === 'to_sign' ? 'Nothing to sign' : 'Nothing signed yet'"
          :empty-text="store.state === 'to_sign' ? 'New documents appear here as soon as someone sends them to you.' : undefined"
          clickable
          :row-attrs="(i) => ({ 'data-test': 'inbox-row-' + i.signer_id })"
          data-test="inbox-table"
          @update:page="lq.setPage"
          @update:page-size="lq.setPageSize"
          @update:sort="lq.setSort"
          @row-click="open($event)"
        >
          <template #cell-title="{ row }">
            <span class="font-medium">{{ row.submission_name }}</span>
            <span v-if="row.party" class="block text-xs text-base-content/70">as {{ row.party }}<template v-if="row.created_at"> · {{ when(row.created_at) }}</template></span>
          </template>
          <template #cell-status="{ row }">
            <UiStatusChip v-if="store.state === 'to_sign'" :status="row.status" :label="signerLabel(row.status)" :colors="SIGNER_STATUS_COLORS" :data-test="'inbox-status-' + row.signer_id" />
            <UiStatusChip v-else-if="row.submission_status" :status="row.submission_status" :label="submissionLabel(row.submission_status)" :colors="SUBMISSION_STATUS_COLORS" :data-test="'inbox-status-' + row.signer_id" />
          </template>
          <template #actions="{ row }">
            <div class="flex justify-end" @click.stop>
              <UiButton v-if="store.state === 'to_sign'" size="xs" icon="mdi-pencil" :data-test="'inbox-sign-' + row.signer_id" @click="open(row)">Open</UiButton>
              <UiButton v-else size="xs" variant="text" icon="mdi-eye-outline" icon-only label="View" :data-test="'inbox-view-' + row.signer_id" @click="open(row)" />
            </div>
          </template>
        </UiDataTable>
      </UiCard>
    </div>
  </UiPage>
</template>
