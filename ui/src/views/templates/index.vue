<script setup lang="ts">
// Signing templates: the folder tree on the left (all templates, those outside
// any folder, then the nested folders with create / rename-move / delete), and
// on the right a filterable, server-paged table (name, status, tags, pages,
// last update) with row actions: open the builder, edit details, clone,
// activate / archive and delete. Upload opens the template drawer. Actions the
// user may not perform are hidden (CASL abilities from the shell).
import { computed, inject, onMounted, reactive, ref } from 'vue'
import { routerKey } from 'vue-router'
import { useAbility } from '@casl/vue'
import {
  UiAlert, UiBadge, UiButton, UiCard, UiDataTable, UiDialog, UiInput, UiPage, UiPagination, UiSelect, UiStatusChip, UiTree,
  useConfirm, useToast, type Column, type SelectOption, type TreeNode,
} from '@go-tangra/ui'
import { describe, describeRefusal } from '@/api/client'
import type { Folder, Template, TemplateStatus } from '@/api/types'
import { TEMPLATE_STATUSES } from '@/api/types'
import { useTemplates } from '@/stores/templates'
import { useFolders } from '@/stores/folders'
import { folderLabel, folderOptions } from '@/utils/folderTree'
import { when } from '@/utils/format'
import TemplateDrawer from './drawer.vue'

const store = useTemplates()
const folders = useFolders()
const ability = useAbility()
const confirm = useConfirm()
const toast = useToast()
const router = inject(routerKey, null)

const canCreate = computed(() => ability.can('create', 'SigningTemplate'))
const canUpdate = computed(() => ability.can('update', 'SigningTemplate'))
const canDelete = computed(() => ability.can('delete', 'SigningTemplate'))

const STATUS_LABELS: Record<TemplateStatus, string> = { draft: 'Draft', active: 'Active', archived: 'Archived' }
const STATUS_COLORS = { draft: 'warning', active: 'success', archived: 'neutral' } as const
const statusOptions: SelectOption[] = TEMPLATE_STATUSES.map((s) => ({ title: STATUS_LABELS[s], value: s }))

// --- folder tree ---
const ALL = '__all'
const ROOT = '__root'
const folderSel = ref(ALL)
const treeItems = computed<TreeNode[]>(() => [
  { id: ALL, label: 'All templates', icon: 'mdi-file-document-multiple-outline' },
  { id: ROOT, label: 'Not in a folder', icon: 'mdi-tray' },
  ...folders.tree,
])
const folderOf = (n: TreeNode) => (n.meta?.folder as Folder | undefined) ?? null
function pickFolder(n: TreeNode): void {
  folderSel.value = n.id
  apply()
}

// --- filters ---
const f = reactive({ q: '', status: '' as TemplateStatus | '', tag: '' })
function apply(p = 1): void {
  const folder_id = folderSel.value === ALL ? undefined : folderSel.value === ROOT ? 'root' : folderSel.value
  void store.list({ folder_id, q: f.q.trim() || undefined, status: f.status || undefined, tag: f.tag.trim() || undefined }, p)
}
function setStatus(v: unknown): void {
  f.status = typeof v === 'string' ? (v as TemplateStatus | '') : ''
  apply()
}

onMounted(() => {
  apply()
  void folders.list()
})

const pages = computed(() => Math.max(1, Math.ceil(store.total / store.pageSize)))
const pageLabel = computed(() => `Page ${store.page} of ${pages.value} · ${store.total} template${store.total === 1 ? '' : 's'}`)

// --- folder dialog (create / rename + move) ---
const error = ref('')
const folderDlg = reactive({ open: false, id: '', name: '', parent: '', error: '', saving: false })
const parentOptions = computed(() => folderOptions(folders.items, folderDlg.id || undefined))
function newFolder(parent: Folder | null): void {
  Object.assign(folderDlg, { open: true, id: '', name: '', parent: parent?.id ?? '', error: '', saving: false })
}
function editFolder(fo: Folder): void {
  Object.assign(folderDlg, { open: true, id: fo.id, name: fo.name, parent: fo.parent_id ?? '', error: '', saving: false })
}
async function saveFolder(): Promise<void> {
  if (!folderDlg.name.trim()) {
    folderDlg.error = 'Enter a name.'
    return
  }
  folderDlg.saving = true
  folderDlg.error = ''
  try {
    if (folderDlg.id) {
      const cur = folders.items.find((x) => x.id === folderDlg.id)
      const body: { name?: string; parent_id?: string | null } = {}
      if (cur?.name !== folderDlg.name.trim()) body.name = folderDlg.name.trim()
      if ((cur?.parent_id ?? '') !== folderDlg.parent) body.parent_id = folderDlg.parent || null
      if (Object.keys(body).length) await folders.update(folderDlg.id, body)
      toast.show({ kind: 'success', title: 'Folder saved' })
    } else {
      await folders.create(folderDlg.name, folderDlg.parent || null)
      toast.show({ kind: 'success', title: 'Folder created' })
    }
    folderDlg.open = false
  } catch (e) {
    folderDlg.error = describe(e)
  } finally {
    folderDlg.saving = false
  }
}
async function removeFolder(fo: Folder): Promise<void> {
  if (!(await confirm.ask({ title: `Delete folder ${fo.name}?`, text: 'Only an empty folder can be deleted.', danger: true, confirmLabel: 'Delete' }))) return
  error.value = ''
  try {
    await folders.remove(fo.id)
    if (folderSel.value === fo.id) pickFolder(treeItems.value[0]!)
    toast.show({ kind: 'success', title: 'Folder deleted' })
  } catch (e) {
    error.value = describe(e)
  }
}

// --- template drawer ---
const drawerOpen = ref(false)
const drawerTemplate = ref<Template | null>(null)
const uploadFolder = computed(() => (folderSel.value === ALL || folderSel.value === ROOT ? undefined : folderSel.value))
function openDrawer(t: Template | null): void {
  drawerTemplate.value = t
  drawerOpen.value = true
}
function onSaved(t: Template, created: boolean): void {
  toast.show({ kind: 'success', title: created ? 'Template uploaded' : 'Template saved', ...(created ? { text: 'Open the builder to place its fields.' } : {}) })
  void store.reload()
  if (created && canUpdate.value) openBuilder(t)
}

// --- row actions ---
const busy = ref('')
function openBuilder(t: Template): void {
  void router?.push({ name: 'signing-builder', params: { id: t.id } })
}
async function toggleStatus(t: Template): Promise<void> {
  const next: TemplateStatus = t.status === 'active' ? 'archived' : 'active'
  if (next === 'archived' && !(await confirm.ask({ title: `Archive ${t.name}?`, text: 'It can no longer be used for new submissions until it is activated again.', confirmLabel: 'Archive' }))) return
  error.value = ''
  busy.value = t.id
  try {
    await store.patch(t.id, { status: next })
    toast.show({ kind: 'success', title: next === 'active' ? 'Template activated' : 'Template archived' })
  } catch (e) {
    error.value = describeRefusal(e)
  } finally {
    busy.value = ''
  }
}
async function remove(t: Template): Promise<void> {
  if (!(await confirm.ask({ title: `Delete ${t.name}?`, text: 'The template and its PDF are deleted. Completed submissions keep their documents.', danger: true, confirmLabel: 'Delete' }))) return
  error.value = ''
  try {
    await store.remove(t.id)
    toast.show({ kind: 'success', title: 'Template deleted' })
  } catch (e) {
    error.value = describe(e)
  }
}

// --- clone dialog ---
const cloneDlg = reactive({ open: false, source: null as Template | null, name: '', folder: '', error: '', saving: false })
function askClone(t: Template): void {
  Object.assign(cloneDlg, { open: true, source: t, name: `Copy of ${t.name}`, folder: t.folder_id ?? '', error: '', saving: false })
}
async function doClone(): Promise<void> {
  const src = cloneDlg.source
  if (!src) return
  if (!cloneDlg.name.trim()) {
    cloneDlg.error = 'Enter a name.'
    return
  }
  cloneDlg.saving = true
  cloneDlg.error = ''
  try {
    await store.clone(src.id, cloneDlg.name.trim(), cloneDlg.folder || null)
    cloneDlg.open = false
    toast.show({ kind: 'success', title: 'Template cloned' })
    void store.reload()
  } catch (e) {
    cloneDlg.error = describe(e)
  } finally {
    cloneDlg.saving = false
  }
}
const allFolderOptions = computed(() => folderOptions(folders.items))

// --- table ---
type Row = Template & Record<string, unknown>
const columns: Column<Row>[] = [
  { key: 'name', label: 'Name' },
  { key: 'status', label: 'Status', width: 'sm' },
  { key: 'tags', label: 'Tags', hideOnStack: true },
  { key: 'pdf_pages', label: 'Pages', width: 'sm', align: 'end', hideOnStack: true, format: (t) => String(t.pdf_pages ?? '') },
  { key: 'updated_at', label: 'Updated', format: (t) => when(t.updated_at) },
]
const rows = computed(() => store.items as Row[])
</script>

<template>
  <UiPage title="Templates" subtitle="PDF templates with the fields each party fills in and signs">
    <template #actions>
      <UiButton v-if="canCreate" icon="mdi-file-upload-outline" data-test="template-upload" @click="openDrawer(null)">Upload template</UiButton>
      <UiButton variant="text" icon="mdi-refresh" icon-only label="Refresh" @click="store.reload(); folders.list()" />
    </template>

    <div class="grid min-w-0 grid-cols-1 gap-4 lg:grid-cols-[16rem_minmax(0,1fr)]">
      <UiCard title="Folders" data-test="folder-card">
        <template v-if="canUpdate" #header>
          <UiButton size="xs" variant="text" icon="mdi-folder-plus-outline" icon-only label="New folder" data-test="folder-new" @click="newFolder(null)" />
        </template>
        <UiTree :items="treeItems" :selected="folderSel" data-test="folder-tree" @select="pickFolder">
          <template v-if="canUpdate" #actions="{ node }">
            <span v-if="folderOf(node)" class="flex gap-0.5">
              <UiButton size="xs" variant="text" icon="mdi-folder-plus-outline" icon-only :label="`New folder in ${node.label}`" :data-test="'folder-add-' + node.id" @click="newFolder(folderOf(node))" />
              <UiButton size="xs" variant="text" icon="mdi-pencil-outline" icon-only :label="`Rename or move ${node.label}`" :data-test="'folder-edit-' + node.id" @click="editFolder(folderOf(node)!)" />
              <UiButton size="xs" variant="text" color="error" icon="mdi-delete-outline" icon-only :label="`Delete ${node.label}`" :data-test="'folder-delete-' + node.id" @click="removeFolder(folderOf(node)!)" />
            </span>
          </template>
        </UiTree>
        <UiAlert v-if="folders.error" kind="error" class="mt-2">{{ folders.error }}</UiAlert>
      </UiCard>

      <div class="flex min-w-0 flex-col gap-3">
        <UiCard>
          <div class="grid grid-cols-2 gap-2 md:grid-cols-12 md:items-end" data-test="template-filters">
            <div class="col-span-2 md:col-span-6"><UiInput id="template-filter-q" v-model="f.q" label="Search by name" type="search" size="sm" data-test="template-filter-q" @enter="apply()" /></div>
            <div class="md:col-span-3"><UiSelect id="template-filter-status" :model-value="f.status" label="Status" :options="statusOptions" placeholder="Any" size="sm" data-test="template-filter-status" @update:model-value="setStatus" /></div>
            <div class="md:col-span-3"><UiInput id="template-filter-tag" v-model="f.tag" label="Tag" size="sm" data-test="template-filter-tag" @enter="apply()" /></div>
          </div>
        </UiCard>

        <UiAlert v-if="error" kind="error" data-test="template-error">{{ error }}</UiAlert>
        <UiAlert v-if="store.error" kind="error">{{ store.error }}</UiAlert>

        <UiCard :padded="false">
          <UiDataTable
            :items="rows"
            :columns="columns"
            :loading="store.loading"
            caption="Signing templates — select one to open it in the builder"
            empty-title="No templates"
            :empty-text="canCreate ? 'Upload a PDF to create a template.' : 'No template matches.'"
            clickable
            :row-attrs="(t) => ({ 'data-test': 'template-row-' + t.id })"
            data-test="templates-table"
            @row-click="openBuilder($event)"
          >
            <template #cell-name="{ row }">
              <span class="font-medium">{{ row.name }}</span>
              <span v-if="row.folder_id" class="block text-xs text-base-content/70">{{ folderLabel(folders.items, row.folder_id) }}</span>
            </template>
            <template #cell-status="{ row }">
              <UiStatusChip :status="row.status" :label="STATUS_LABELS[row.status]" :colors="STATUS_COLORS" :data-test="'template-status-' + row.id" />
            </template>
            <template #cell-tags="{ row }">
              <span class="flex flex-wrap gap-1">
                <UiBadge v-for="t in row.tags ?? []" :key="t" size="xs">{{ t }}</UiBadge>
              </span>
            </template>
            <template #actions="{ row }">
              <div class="flex flex-wrap justify-end gap-0.5" @click.stop>
                <UiButton size="xs" variant="text" icon="mdi-file-document-edit-outline" icon-only :label="canUpdate ? 'Open builder' : 'View fields'" :data-test="'template-open-' + row.id" @click="openBuilder(row)" />
                <UiButton v-if="canUpdate" size="xs" variant="text" icon="mdi-pencil-outline" icon-only label="Edit details" :data-test="'template-edit-' + row.id" @click="openDrawer(row)" />
                <UiButton v-if="canCreate" size="xs" variant="text" icon="mdi-content-copy" icon-only label="Clone" :data-test="'template-clone-' + row.id" @click="askClone(row)" />
                <template v-if="canUpdate">
                  <UiButton v-if="row.status !== 'active'" size="xs" variant="text" color="success" icon="mdi-play" icon-only label="Activate" :disabled="busy === row.id" :data-test="'template-activate-' + row.id" @click="toggleStatus(row)" />
                  <UiButton v-else size="xs" variant="text" icon="mdi-package-down" icon-only label="Archive" :disabled="busy === row.id" :data-test="'template-archive-' + row.id" @click="toggleStatus(row)" />
                </template>
                <UiButton v-if="canDelete" size="xs" variant="text" color="error" icon="mdi-delete-outline" icon-only label="Delete" :data-test="'template-delete-' + row.id" @click="remove(row)" />
              </div>
            </template>
          </UiDataTable>
        </UiCard>
        <div class="flex justify-end">
          <UiPagination :has-prev="store.page > 1" :has-next="store.page < pages" :label="pageLabel" data-test="template-pager" @prev="store.list(store.filter, store.page - 1)" @next="store.list(store.filter, store.page + 1)" />
        </div>
      </div>
    </div>

    <TemplateDrawer :open="drawerOpen" :template="drawerTemplate" :folders="folders.items" :folder-id="uploadFolder" @close="drawerOpen = false" @saved="onSaved" />

    <UiDialog v-model="folderDlg.open" :title="folderDlg.id ? 'Rename or move folder' : 'New folder'" size="sm" data-test="folder-dialog">
      <div class="flex flex-col gap-3">
        <UiAlert v-if="folderDlg.error" kind="error" data-test="folder-error">{{ folderDlg.error }}</UiAlert>
        <UiInput id="folder-name" v-model="folderDlg.name" label="Name" required data-test="folder-name" @enter="saveFolder" />
        <UiSelect id="folder-parent" v-model="folderDlg.parent" label="Inside" :options="parentOptions" placeholder="Top level" data-test="folder-parent" />
      </div>
      <template #actions>
        <UiButton variant="text" @click="folderDlg.open = false">Cancel</UiButton>
        <UiButton icon="mdi-check" :loading="folderDlg.saving" data-test="folder-save" @click="saveFolder">{{ folderDlg.id ? 'Save' : 'Create' }}</UiButton>
      </template>
    </UiDialog>

    <UiDialog v-model="cloneDlg.open" :title="`Clone ${cloneDlg.source?.name ?? ''}`" size="sm" data-test="clone-dialog">
      <div class="flex flex-col gap-3">
        <UiAlert v-if="cloneDlg.error" kind="error" data-test="clone-error">{{ cloneDlg.error }}</UiAlert>
        <p class="text-sm text-base-content/70">The copy gets the PDF and every field, and starts as a draft.</p>
        <UiInput id="clone-name" v-model="cloneDlg.name" label="Name of the copy" required data-test="clone-name" @enter="doClone" />
        <UiSelect id="clone-folder" v-model="cloneDlg.folder" label="Folder" :options="allFolderOptions" placeholder="No folder" data-test="clone-folder" />
      </div>
      <template #actions>
        <UiButton variant="text" @click="cloneDlg.open = false">Cancel</UiButton>
        <UiButton icon="mdi-content-copy" :loading="cloneDlg.saving" data-test="clone-save" @click="doClone">Clone</UiButton>
      </template>
    </UiDialog>
  </UiPage>
</template>
