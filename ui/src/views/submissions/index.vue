<script setup lang="ts">
// Submissions: a filterable, server-paged table (name, status, signing
// progress, mode, sent, expiry). Readers with signing:read see every
// submission of the tenant ("Only mine" narrows it); everyone else sees the
// ones they sent. "New submission" opens the create drawer; a row opens the
// submission's page. Completed / cancelled / expired events refresh the list.
import { computed, inject, onBeforeUnmount, onMounted, reactive, ref } from 'vue'
import { routeLocationKey, routerKey } from 'vue-router'
import { useAbility } from '@casl/vue'
import { UiAlert, UiButton, UiCard, UiCheckbox, UiDataTable, UiInput, UiPage, UiPagination, UiSelect, UiStatusChip, useToast, type Column, type SelectOption } from '@go-tangra/ui'
import { api } from '@/api/client'
import type { Submission, SubmissionStatus, Template, TemplatePage } from '@/api/types'
import { SUBMISSION_STATUSES } from '@/api/types'
import { useSubmissions } from '@/stores/submissions'
import { coalesce, useLive } from '@/stores/live'
import { when } from '@/utils/format'
import { SUBMISSION_STATUS_COLORS, SUBMISSION_STATUS_LABELS, progress } from '@/utils/submission'
import SubmissionDrawer from './drawer.vue'

const store = useSubmissions()
const live = useLive()
const ability = useAbility()
const toast = useToast()
const router = inject(routerKey, null)
const route = inject(routeLocationKey, null)

const canCreate = computed(() => ability.can('create', 'SigningSubmission'))
const canReadAll = computed(() => ability.can('read', 'SigningSubmission') || ability.can('manage', 'SigningSubmission'))

const statusOptions: SelectOption[] = SUBMISSION_STATUSES.map((s) => ({ title: SUBMISSION_STATUS_LABELS[s], value: s }))
const templates = ref<Template[]>([])
const templateOptions = computed<SelectOption[]>(() => templates.value.map((t) => ({ title: t.name, value: t.id })))

const f = reactive({ q: '', status: '' as SubmissionStatus | '', template_id: '', mine: false })
function apply(p = 1): void {
  void store.list({ q: f.q.trim() || undefined, status: f.status || undefined, template_id: f.template_id || undefined, mine: f.mine || undefined }, p)
}
function setStatus(v: unknown): void {
  f.status = typeof v === 'string' ? (v as SubmissionStatus | '') : ''
  apply()
}
function setTemplate(v: unknown): void {
  f.template_id = typeof v === 'string' ? v : ''
  apply()
}
function setMine(v: boolean): void {
  f.mine = v
  apply()
}

const refresh = coalesce(() => void store.reload(), 500)
let release: (() => void) | null = null
let unsubscribe: (() => void) | null = null

onMounted(() => {
  const tid = route?.query.template_id
  if (typeof tid === 'string' && tid) f.template_id = tid
  apply()
  if (ability.can('read', 'SigningTemplate')) {
    api<TemplatePage>('GET', 'templates', undefined, { query: { page: 1, page_size: 100 } }).then((r) => (templates.value = r.items ?? [])).catch(() => {})
  }
  release = live.connect()
  unsubscribe = live.on((e) => { if (e.type !== 'signing.inbox') refresh.trigger() })
})
onBeforeUnmount(() => {
  refresh.cancel()
  unsubscribe?.()
  release?.()
})

const pages = computed(() => Math.max(1, Math.ceil(store.total / store.pageSize)))
const pageLabel = computed(() => `Page ${store.page} of ${pages.value} · ${store.total} submission${store.total === 1 ? '' : 's'}`)

// --- drawer ---
const drawerOpen = ref(false)
function open(s: Submission): void {
  void router?.push({ name: 'signing-submission', params: { id: s.id } })
}
function onSent(s: Submission): void {
  toast.show({ kind: 'success', title: 'Submission sent', text: s.mode === 'sequential' ? 'The first signer is invited.' : 'Every signer is invited.' })
  void store.reload()
}

// --- table ---
type Row = Submission & Record<string, unknown>
const columns: Column<Row>[] = [
  { key: 'name', label: 'Name' },
  { key: 'status', label: 'Status', width: 'sm' },
  { key: 'progress', label: 'Signatures', format: (s) => progress(s) },
  { key: 'mode', label: 'Order', width: 'sm', hideOnStack: true, format: (s) => (s.mode === 'sequential' ? 'In order' : 'Any order') },
  { key: 'sent_at', label: 'Sent', hideOnStack: true, format: (s) => when(s.sent_at) || (s.status === 'draft' ? 'Not sent' : '') },
  { key: 'expires_at', label: 'Expires', hideOnStack: true, format: (s) => when(s.expires_at) },
]
const rows = computed(() => store.items as Row[])
</script>

<template>
  <UiPage title="Submissions" subtitle="Documents sent out for signature and their progress">
    <template #actions>
      <UiButton v-if="canCreate" icon="mdi-plus" data-test="submission-new" @click="drawerOpen = true">New submission</UiButton>
      <UiButton variant="text" icon="mdi-refresh" icon-only label="Refresh" @click="store.reload()" />
    </template>

    <div class="flex min-w-0 flex-col gap-3">
      <UiCard>
        <div class="grid grid-cols-2 gap-2 md:grid-cols-12 md:items-end" data-test="submission-filters">
          <div class="col-span-2 md:col-span-4"><UiInput id="submission-filter-q" v-model="f.q" label="Search by name" type="search" size="sm" data-test="submission-filter-q" @enter="apply()" /></div>
          <div class="md:col-span-3"><UiSelect id="submission-filter-status" :model-value="f.status" label="Status" :options="statusOptions" placeholder="Any" size="sm" data-test="submission-filter-status" @update:model-value="setStatus" /></div>
          <div class="md:col-span-3"><UiSelect id="submission-filter-template" :model-value="f.template_id" label="Template" :options="templateOptions" placeholder="Any" size="sm" data-test="submission-filter-template" @update:model-value="setTemplate" /></div>
          <div v-if="canReadAll" class="col-span-2 pb-2 md:col-span-2"><UiCheckbox id="submission-filter-mine" :model-value="f.mine" label="Only mine" data-test="submission-filter-mine" @update:model-value="setMine" /></div>
        </div>
      </UiCard>

      <UiAlert v-if="store.error" kind="error" data-test="submission-error">{{ store.error }}</UiAlert>

      <UiCard :padded="false">
        <UiDataTable
          :items="rows"
          :columns="columns"
          :loading="store.loading"
          caption="Submissions — select one to see its signers and history"
          empty-title="No submissions"
          :empty-text="canCreate ? 'Create one from an active template.' : 'No submission matches.'"
          clickable
          :row-attrs="(s) => ({ 'data-test': 'submission-row-' + s.id })"
          data-test="submissions-table"
          @row-click="open($event)"
        >
          <template #cell-name="{ row }">
            <span class="font-medium">{{ row.name }}</span>
            <span v-if="row.created_at" class="block text-xs text-base-content/70">Created {{ when(row.created_at) }}</span>
          </template>
          <template #cell-status="{ row }">
            <UiStatusChip :status="row.status" :label="SUBMISSION_STATUS_LABELS[row.status]" :colors="SUBMISSION_STATUS_COLORS" :data-test="'submission-status-' + row.id" />
          </template>
          <template #actions="{ row }">
            <div class="flex justify-end" @click.stop>
              <UiButton size="xs" variant="text" icon="mdi-arrow-right" icon-only label="Open" :data-test="'submission-open-' + row.id" @click="open(row)" />
            </div>
          </template>
        </UiDataTable>
      </UiCard>
      <div class="flex justify-end">
        <UiPagination :has-prev="store.page > 1" :has-next="store.page < pages" :label="pageLabel" data-test="submission-pager" @prev="store.list(store.filter, store.page - 1)" @next="store.list(store.filter, store.page + 1)" />
      </div>
    </div>

    <SubmissionDrawer :open="drawerOpen" :template-id="f.template_id || undefined" @close="drawerOpen = false" @created="store.reload()" @sent="onSent" />
  </UiPage>
</template>
