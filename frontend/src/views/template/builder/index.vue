<script setup lang="ts">
import { ref, computed } from 'vue';
import { useRoute } from 'vue-router';
import { Page } from 'shell/vben/common-ui';
import { LucideSave, LucideScanSearch } from 'shell/vben/icons';
import { notification, Modal } from 'ant-design-vue';
import { useSigningTemplateStore } from '../../../stores/signing-template.state';
import type { FieldType, Field, FieldConditionGroup } from '../../../models/field';
import { submitterNames } from '../../../models/field';
import { useFieldBuilder } from './composables/useFieldBuilder';
import type { BuilderField } from './composables/useFieldBuilder';
import BuilderPdfViewer from './components/BuilderPdfViewer.vue';
import FieldPalette from './components/FieldPalette.vue';
import FieldPropertiesPanel from './components/FieldPropertiesPanel.vue';
import ConditionBuilder from './components/ConditionBuilder.vue';
import FormulaBuilder from './components/FormulaBuilder.vue';

const route = useRoute();
const templateStore = useSigningTemplateStore();

const templateId = route.params.id as string;
const loading = ref(false);
const pdfUrl = ref('');

// Dynamic submitters
const submitters = ref<readonly { readonly id: string; readonly name: string }[]>([
  { id: 'submitter_1', name: 'First Party' },
]);

function addSubmitter(): void {
  const idx = submitters.value.length + 1;
  submitters.value = [
    ...submitters.value,
    { id: `submitter_${idx}`, name: submitterNames[idx - 1] ?? `Party ${idx}` },
  ];
}

function removeLastSubmitter(): void {
  if (submitters.value.length <= 1) return;
  const lastIndex = submitters.value.length - 1;
  fieldBuilder.removeFieldsBySubmitterIndex(lastIndex);
  submitters.value = submitters.value.slice(0, -1);
}

// Field builder composable
const fieldBuilder = useFieldBuilder();

// Condition / Formula modal state
const showConditionModal = ref(false);
const showFormulaModal = ref(false);
const editingFieldId = ref<string | undefined>();

// Build a minimal Field object for the condition/formula builders
const editingFieldAsField = computed<Field | null>(() => {
  if (!editingFieldId.value) return null;
  const bf = fieldBuilder.fields.value.find((f) => f.id === editingFieldId.value);
  if (!bf) return null;
  return {
    id: bf.id,
    submitter_id: submitters.value[bf.submitterIndex]?.id ?? 'submitter_1',
    name: bf.name,
    type: bf.type,
    required: bf.required,
    areas: [{
      attachment_id: '',
      page: bf.pageNumber,
      x: bf.xPercent,
      y: bf.yPercent,
      w: bf.widthPercent,
      h: bf.heightPercent,
    }],
  };
});

const availableFieldsForConditions = computed<Field[]>(() =>
  fieldBuilder.fields.value
    .filter((f) => f.id !== editingFieldId.value)
    .map((bf) => ({
      id: bf.id,
      submitter_id: submitters.value[bf.submitterIndex]?.id ?? 'submitter_1',
      name: bf.name,
      type: bf.type,
      required: bf.required,
    })),
);

const availableFieldsForFormulas = computed<Field[]>(() =>
  fieldBuilder.fields.value
    .filter((f) => f.id !== editingFieldId.value && (f.type === 'number' || f.type === 'text'))
    .map((bf) => ({
      id: bf.id,
      submitter_id: submitters.value[bf.submitterIndex]?.id ?? 'submitter_1',
      name: bf.name,
      type: bf.type,
      required: bf.required,
    })),
);

// PDF viewer event handlers
function handleDropField(
  type: string,
  pageNumber: number,
  xPercent: number,
  yPercent: number,
  submitterIndex: number = 0,
): void {
  fieldBuilder.addField(type as FieldType, pageNumber, xPercent, yPercent, submitterIndex);
}

function handleSelectField(id: string): void {
  fieldBuilder.selectField(id);
}

function handleDeselect(): void {
  fieldBuilder.selectField(undefined);
}

function handleMoveField(id: string, x: number, y: number): void {
  fieldBuilder.moveField(id, x, y);
}

function handleResizeField(id: string, w: number, h: number): void {
  fieldBuilder.resizeField(id, w, h);
}

// Properties panel handlers
function handleUpdateField(
  id: string,
  changes: Partial<Pick<BuilderField, 'name' | 'type' | 'required' | 'submitterIndex'>>,
): void {
  fieldBuilder.updateField(id, changes);
}

function handleDeleteField(id: string): void {
  fieldBuilder.removeField(id);
}

function handleOpenConditions(): void {
  if (!fieldBuilder.selectedFieldId.value) return;
  editingFieldId.value = fieldBuilder.selectedFieldId.value;
  showConditionModal.value = true;
}

function handleOpenFormula(): void {
  if (!fieldBuilder.selectedFieldId.value) return;
  editingFieldId.value = fieldBuilder.selectedFieldId.value;
  showFormulaModal.value = true;
}

function handleConditionsUpdate(_conditions: FieldConditionGroup[]): void {
  // Conditions are stored externally for now; the builder focuses on placement
}

function handleFormulaUpdate(_formula: string): void {
  // Formula is stored externally for now; the builder focuses on placement
}

// Auto-detect placeholders
const detecting = ref(false);

async function handleDetectFields(): Promise<void> {
  detecting.value = true;
  try {
    const result = await templateStore.detectFields(templateId);
    if (!result.fields || result.fields.length === 0) {
      notification.info({ message: 'No placeholders detected in the PDF' });
      return;
    }

    for (const f of result.fields) {
      fieldBuilder.addDetectedField(
        f.name,
        (f.type ?? 'text') as FieldType,
        f.pageNumber,
        f.xPercent,
        f.yPercent,
        f.widthPercent,
        f.heightPercent,
        f.font,
        f.fontSize,
      );
    }

    notification.success({
      message: `Detected ${result.fields.length} placeholder${result.fields.length === 1 ? '' : 's'}`,
    });
  } catch (e: any) {
    notification.error({ message: e?.message ?? 'Failed to detect fields' });
  } finally {
    detecting.value = false;
  }
}

// Load template data
async function loadTemplate(): Promise<void> {
  loading.value = true;
  try {
    const resp = await templateStore.getTemplate(templateId);
    const tpl = resp.template;

    if (tpl?.fields && tpl.fields.length > 0) {
      // Ensure enough submitters exist for the loaded fields
      const maxSubmitterIdx = Math.max(...tpl.fields.map((f) => f.submitterIndex));
      while (submitters.value.length <= maxSubmitterIdx) {
        addSubmitter();
      }
      fieldBuilder.loadFields(tpl.fields);
    }

    if (tpl?.id) {
      pdfUrl.value = await templateStore.getTemplatePdfUrl(tpl.id);
    }
  } catch (e: any) {
    notification.error({ message: e?.message ?? 'Failed to load template' });
  } finally {
    loading.value = false;
  }
}

// Save
async function handleSave(): Promise<void> {
  loading.value = true;
  try {
    const apiFields = fieldBuilder.toTemplateFields();
    await templateStore.updateTemplateFields(templateId, apiFields);
    fieldBuilder.markClean();
    notification.success({ message: 'Fields saved' });
  } catch (e: any) {
    notification.error({ message: e?.message ?? 'Failed to save' });
  } finally {
    loading.value = false;
  }
}

loadTemplate();
</script>

<template>
  <Page auto-content-height>
    <div class="builder-layout">
      <!-- Toolbar -->
      <div class="builder-toolbar">
        <h2 class="builder-toolbar__title">Template Field Builder</h2>
        <div class="builder-toolbar__actions">
          <span v-if="fieldBuilder.isDirty.value" class="builder-toolbar__dirty">
            Unsaved changes
          </span>
          <button
            class="builder-btn-secondary"
            :disabled="detecting"
            @click="handleDetectFields"
          >
            <LucideScanSearch class="size-4" />
            {{ detecting ? 'Detecting...' : 'Auto-detect Fields' }}
          </button>
          <button
            class="builder-btn-primary"
            :disabled="loading"
            @click="handleSave"
          >
            <LucideSave class="size-4" />
            Save Fields
          </button>
        </div>
      </div>

      <!-- Main content area -->
      <div class="builder-content">
        <!-- Left sidebar: Field palette -->
        <div class="builder-sidebar builder-sidebar--left">
          <FieldPalette
            :submitters="submitters"
            @add-party="addSubmitter"
            @remove-party="removeLastSubmitter"
          />
        </div>

        <!-- Center: PDF viewer -->
        <div class="builder-center">
          <BuilderPdfViewer
            :pdf-url="pdfUrl"
            :fields="fieldBuilder.fields.value"
            :selected-field-id="fieldBuilder.selectedFieldId.value"
            @drop-field="handleDropField"
            @select-field="handleSelectField"
            @deselect="handleDeselect"
            @move-field="handleMoveField"
            @resize-field="handleResizeField"
          />
        </div>

        <!-- Right sidebar: Properties panel -->
        <div class="builder-sidebar builder-sidebar--right">
          <FieldPropertiesPanel
            :field="fieldBuilder.selectedField.value ?? null"
            :submitter-count="submitters.length"
            @update="handleUpdateField"
            @delete="handleDeleteField"
            @open-conditions="handleOpenConditions"
            @open-formula="handleOpenFormula"
          />
        </div>
      </div>
    </div>

    <!-- Condition Builder Modal -->
    <Modal
      v-model:open="showConditionModal"
      title="Conditional Logic"
      width="700px"
      :footer="null"
    >
      <ConditionBuilder
        v-if="editingFieldAsField"
        :field="editingFieldAsField"
        :available-fields="availableFieldsForConditions"
        @update:conditions="handleConditionsUpdate"
      />
    </Modal>

    <!-- Formula Builder Modal -->
    <Modal
      v-model:open="showFormulaModal"
      title="Formula Builder"
      width="600px"
      :footer="null"
    >
      <FormulaBuilder
        v-if="editingFieldAsField"
        :field="editingFieldAsField"
        :available-fields="availableFieldsForFormulas"
        @update:formula="handleFormulaUpdate"
      />
    </Modal>
  </Page>
</template>

<style scoped>
.builder-layout {
  display: flex;
  flex-direction: column;
  height: 100%;
  min-height: 0;
}

.builder-toolbar {
  display: flex;
  align-items: center;
  justify-content: space-between;
  padding: 8px 0;
  margin-bottom: 8px;
  border-bottom: 1px solid hsl(var(--border));
  flex-shrink: 0;
}

.builder-toolbar__title {
  font-size: 1.125rem;
  font-weight: 600;
  color: hsl(var(--foreground));
  margin: 0;
}

.builder-toolbar__actions {
  display: flex;
  align-items: center;
  gap: 12px;
}

.builder-toolbar__dirty {
  font-size: 0.75rem;
  color: hsl(38, 92%, 50%);
  font-weight: 500;
}

.builder-content {
  display: flex;
  flex: 1;
  min-height: 0;
  gap: 0;
}

.builder-sidebar {
  flex-shrink: 0;
  border: 1px solid hsl(var(--border));
  background: hsl(var(--card));
  border-radius: 8px;
  overflow: hidden;
}

.builder-sidebar--left {
  width: 200px;
  margin-right: 8px;
}

.builder-sidebar--right {
  width: 280px;
  margin-left: 8px;
}

.builder-center {
  flex: 1;
  min-width: 0;
  border-radius: 8px;
  overflow: hidden;
}

.builder-btn-primary {
  display: inline-flex;
  align-items: center;
  gap: 0.375rem;
  border-radius: 0.375rem;
  background: hsl(var(--primary));
  color: hsl(var(--primary-foreground, 0 0% 100%));
  padding: 0.375rem 1rem;
  font-size: 0.875rem;
  font-weight: 500;
  cursor: pointer;
  border: none;
  transition: opacity 0.15s;
}

.builder-btn-primary:hover {
  opacity: 0.9;
}

.builder-btn-primary:disabled {
  opacity: 0.5;
  cursor: not-allowed;
}

.builder-btn-secondary {
  display: inline-flex;
  align-items: center;
  gap: 0.375rem;
  border-radius: 0.375rem;
  background: hsl(var(--muted));
  color: hsl(var(--foreground));
  padding: 0.375rem 1rem;
  font-size: 0.875rem;
  font-weight: 500;
  cursor: pointer;
  border: 1px solid hsl(var(--border));
  transition: opacity 0.15s;
}

.builder-btn-secondary:hover {
  opacity: 0.85;
}

.builder-btn-secondary:disabled {
  opacity: 0.5;
  cursor: not-allowed;
}
</style>
