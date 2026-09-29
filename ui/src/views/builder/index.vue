<script setup lang="ts">
// Template builder: the PDF pages with the fields laid over them, the field
// palette and the parties on the left, the selected field's properties on the
// right. Save stores parties and fields with the version read (a newer save by
// someone else is reported and must be reloaded); Auto-detect proposes fields
// at the placeholder lines for the selected party; Activate / Archive change
// the status. Leaving with unsaved changes asks first. Without template
// management permission the builder is read-only.
import { computed, inject, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import { matchedRouteKey, onBeforeRouteLeave, routeLocationKey, routerKey } from 'vue-router'
import { useAbility } from '@casl/vue'
import { UiAlert, UiBadge, UiButton, UiCard, UiEmptyState, UiErrorState, UiPage, UiSkeleton, UiStatusChip, useConfirm, useToast } from '@go-tangra/ui'
import type { FieldType, TemplateStatus } from '@/api/types'
import { useBuilder } from '@/stores/builder'
import { useTemplates } from '@/stores/templates'
import { TYPE_LABELS, partyColor } from '@/utils/fields'
import PdfPages from '@/components/PdfPages.vue'
import FieldOverlay from '@/components/FieldOverlay.vue'
import FieldPalette from '@/components/FieldPalette.vue'
import FieldProps from '@/components/FieldProps.vue'
import PartiesEditor from '@/components/PartiesEditor.vue'

const b = useBuilder()
const templates = useTemplates()
const ability = useAbility()
const confirm = useConfirm()
const toast = useToast()
const route = inject(routeLocationKey, null)
const router = inject(routerKey, null)

const id = computed(() => (typeof route?.params.id === 'string' ? route.params.id : ''))
const canEdit = computed(() => ability.can('update', 'SigningTemplate'))
const readonly = computed(() => !canEdit.value)
const pdfUrl = computed(() => (id.value ? templates.pdfUrl(id.value) : ''))

const STATUS_LABELS: Record<TemplateStatus, string> = { draft: 'Draft', active: 'Active', archived: 'Archived' }

watch(id, (v) => { if (v) void b.load(v) }, { immediate: true })

// --- placing fields ---
const armed = ref<FieldType | ''>('')
/** The page new keyboard-added fields go to: the last page interacted with. */
const currentPage = ref(1)
function place(type: FieldType, page: number, fx: number, fy: number): void {
  b.addField(type, page, fx, fy)
  currentPage.value = page
  armed.value = ''
}
function addCentered(type: FieldType): void {
  place(type, currentPage.value, 0.5, 0.5)
}
function select(fid: string): void {
  b.select(fid)
  const f = b.fields.find((x) => x.id === fid)
  if (f) currentPage.value = f.page
}
function onKey(e: KeyboardEvent): void {
  if (e.key === 'Escape' && armed.value) armed.value = ''
}

const counts = computed(() => Object.fromEntries(b.parties.map((p) => [p.key, b.fieldCount(p.key)])))
const pages = ref<InstanceType<typeof PdfPages> | null>(null)
function focusField(fid: string): void {
  select(fid)
  const f = b.fields.find((x) => x.id === fid)
  if (f) pages.value?.scrollToPage(f.page)
}
const sortedFields = computed(() => [...b.fields].sort((x, y) => x.page - y.page || x.y - y.y || x.x - y.x))

// --- toolbar ---
async function save(): Promise<void> {
  if (await b.save()) toast.show({ kind: 'success', title: 'Template saved' })
}
async function detect(): Promise<void> {
  const n = await b.detect()
  if (!b.error) toast.show({ kind: n ? 'success' : 'info', title: n ? `${n} field${n === 1 ? '' : 's'} proposed` : 'No new placeholders found', ...(n ? { text: 'Review them and remove the ones you do not need, then save.' } : {}) })
}
async function setStatus(status: TemplateStatus): Promise<void> {
  if (status === 'archived' && !(await confirm.ask({ title: 'Archive this template?', text: 'It can no longer be used for new submissions until it is activated again.', confirmLabel: 'Archive' }))) return
  if (await b.setStatus(status)) toast.show({ kind: 'success', title: status === 'active' ? 'Template activated' : 'Template archived' })
}
async function reload(): Promise<void> {
  if (id.value) await b.load(id.value)
}
function back(): void {
  void router?.push({ name: 'signing-templates' })
}

// --- unsaved changes guard ---
if (inject(matchedRouteKey, null)) {
  onBeforeRouteLeave(async () => {
    if (!b.dirty || readonly.value) return true
    return confirm.ask({ title: 'Discard unsaved changes?', text: 'Your changes to the fields and parties are not saved.', danger: true, confirmLabel: 'Discard', cancelLabel: 'Keep editing' })
  })
}
function beforeUnload(e: BeforeUnloadEvent): void {
  if (b.dirty && !readonly.value) e.preventDefault()
}
onMounted(() => window.addEventListener('beforeunload', beforeUnload))
onBeforeUnmount(() => window.removeEventListener('beforeunload', beforeUnload))
</script>

<template>
  <UiPage :title="b.template?.name ?? 'Template builder'" :subtitle="b.template ? `${b.template.pdf_pages ?? '?'} page(s) · ${b.fields.length} field(s) · version ${b.version}` : undefined">
    <template #before-title>
      <UiButton variant="text" icon="mdi-arrow-left" icon-only label="Back to templates" data-test="builder-back" @click="back" />
    </template>
    <template #badges>
      <UiStatusChip v-if="b.template" :status="b.template.status" :label="STATUS_LABELS[b.template.status]" data-test="builder-status" />
      <UiBadge v-if="b.dirty && canEdit" color="warning" data-test="builder-dirty">Unsaved changes</UiBadge>
    </template>
    <template v-if="canEdit && b.template" #actions>
      <UiButton variant="soft" icon="mdi-magnify-scan" :loading="b.detecting" data-test="builder-detect" @click="detect">Auto-detect fields</UiButton>
      <UiButton v-if="b.template.status !== 'active'" variant="soft" color="success" icon="mdi-play" data-test="builder-activate" @click="setStatus('active')">Activate</UiButton>
      <UiButton v-else variant="soft" icon="mdi-package-down" data-test="builder-archive" @click="setStatus('archived')">Archive</UiButton>
      <UiButton icon="mdi-check" :loading="b.saving" :disabled="!b.dirty" data-test="builder-save" @click="save">Save</UiButton>
    </template>

    <UiSkeleton v-if="b.loading && !b.template" kind="card" :lines="8" />
    <UiErrorState v-else-if="!b.template" :text="b.error || 'The template could not be loaded.'" data-test="builder-load-error" @retry="reload" />

    <div v-else class="flex min-w-0 flex-col gap-3" @keydown="onKey">
      <UiAlert v-if="b.conflict" kind="warning" title="Saved by someone else meanwhile" data-test="builder-conflict">
        <p>Another save of this template happened after you opened it, so yours was not stored. Reload to get the current version; your unsaved changes here are lost.</p>
        <UiButton size="sm" class="mt-2" icon="mdi-reload" data-test="builder-reload" @click="reload">Reload</UiButton>
      </UiAlert>
      <UiAlert v-if="b.error" kind="error" data-test="builder-error">{{ b.error }}</UiAlert>

      <div class="grid min-w-0 grid-cols-1 gap-4 lg:grid-cols-[15rem_minmax(0,1fr)_18rem]">
        <aside class="flex min-w-0 flex-col gap-4">
          <UiCard v-if="canEdit" title="Fields">
            <FieldPalette :armed="armed" @arm="armed = $event" @add="addCentered" />
          </UiCard>
          <UiCard title="Parties">
            <PartiesEditor :parties="b.parties" :current="b.party" :counts="counts" :errors="b.partyNameErrors" :readonly="readonly" @select="b.party = $event" @add="b.addParty()" @rename="b.renameParty" @remove="b.removeParty" />
          </UiCard>
        </aside>

        <section class="min-w-0 overflow-auto rounded-box bg-base-200 p-2 md:p-4 lg:max-h-[calc(100vh-12rem)]" aria-label="Document pages">
          <PdfPages v-if="pdfUrl" ref="pages" :url="pdfUrl">
            <template #page="{ page }">
              <FieldOverlay
                :page="page"
                :fields="b.fields"
                :parties="b.parties"
                :selected-id="b.selectedId"
                :proposed="b.proposed"
                :readonly="readonly"
                :armed="armed"
                @select="select"
                @box="b.setBox"
                @nudge="b.nudge"
                @grow="b.resize"
                @remove="b.removeField"
                @place="place"
              />
            </template>
          </PdfPages>
        </section>

        <aside class="flex min-w-0 flex-col gap-4">
          <UiCard :title="b.selected ? 'Field' : 'Field properties'">
            <FieldProps
              v-if="b.selected"
              :key="b.selected.id"
              :field="b.selected"
              :parties="b.parties"
              :name-error="b.fieldNameErrors[b.selected.id]"
              :readonly="readonly"
              @update="(patch, unset) => b.updateField(b.selectedId, patch, unset)"
              @type="b.changeType(b.selectedId, $event)"
              @remove="b.removeField(b.selectedId)"
            />
            <p v-else class="text-sm text-base-content/70">Select a field on a page to edit it.</p>
          </UiCard>
          <UiCard :title="`All fields (${b.fields.length})`">
            <UiEmptyState v-if="!b.fields.length" icon="mdi-form-select" title="No fields yet" :text="canEdit ? 'Place fields from the palette or run auto-detect.' : undefined" />
            <ul v-else class="flex max-h-80 flex-col gap-0.5 overflow-y-auto text-sm" data-test="builder-field-list">
              <li v-for="f in sortedFields" :key="f.id">
                <button type="button" class="flex w-full items-center gap-2 rounded-field px-2 py-1 text-start hover:bg-base-200" :class="b.selectedId === f.id ? 'bg-primary/10' : ''" :aria-pressed="b.selectedId === f.id" :data-test="'field-item-' + f.id" @click="focusField(f.id)">
                  <span class="size-2.5 shrink-0 rounded-full" :class="partyColor(b.parties, f.party).dot" aria-hidden="true" />
                  <span class="min-w-0 grow truncate">{{ f.name }}</span>
                  <span class="text-xs text-base-content/70">{{ TYPE_LABELS[f.type] }} · p{{ f.page }}</span>
                  <UiBadge v-if="b.proposed.has(f.id)" size="xs" color="info">proposed</UiBadge>
                </button>
              </li>
            </ul>
          </UiCard>
        </aside>
      </div>
    </div>
  </UiPage>
</template>
