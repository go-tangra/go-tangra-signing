<script setup lang="ts">
import { ref, computed, watch } from 'vue';
import { Button, Input, Tag, Alert } from 'ant-design-vue';
import { $t } from 'shell/locales';
import { useFormulas } from '../../../../composables/useFormulas';
import type { Field } from '../../../../models/field';

interface Props {
  field: Field;
  availableFields: Field[];
}

const props = defineProps<Props>();

const emit = defineEmits<{
  'update:formula': [formula: string];
}>();

const formula = ref(props.field.preferences?.formula ?? props.field.formula ?? '');
const validationError = ref<string | null>(null);

// Convert formula field IDs to display names
function formulaToDisplay(formulaStr: string): string {
  let out = formulaStr;
  const sorted = [...props.availableFields].sort((a, b) => b.id.length - a.id.length);
  for (const f of sorted) {
    const name = f.name || f.id;
    const re = new RegExp(f.id.replace(/[.*+?^${}()|[\]\\]/g, '\\$&'), 'g');
    out = out.replace(re, `[[${name}]]`);
  }
  return out;
}

// Parse display names back to field IDs
function displayToFormula(displayStr: string): string {
  return displayStr.replace(/\[\[([^\]]*?)\]\]/g, (_, name) => {
    const f = props.availableFields.find(x => (x.name || x.id) === name);
    return f ? f.id : `[[${name}]]`;
  });
}

const displayFormula = computed(() => formulaToDisplay(formula.value));

function onFormulaInput(e: Event) {
  const target = e.target as HTMLTextAreaElement;
  formula.value = displayToFormula(target.value);
}

// Sample data for preview
const sampleFormData = computed(() => {
  const data: Record<string, any> = {};
  for (const field of props.availableFields) {
    data[field.id] = 10;
  }
  return data;
});

const { evaluateFormula } = useFormulas(
  computed(() => props.availableFields),
  computed(() => sampleFormData.value)
);

const previewResult = computed(() => {
  if (!formula.value || validationError.value) return null;
  const result = evaluateFormula(formula.value);
  return result !== null ? result.toFixed(2) : null;
});

const functions = [
  { name: 'SUM', syntax: 'SUM(a, b)', desc: 'Sum of values' },
  { name: 'IF', syntax: 'IF(a > 100, b, 0)', desc: 'Conditional value' },
  { name: 'MAX', syntax: 'MAX(a, b)', desc: 'Maximum value' },
  { name: 'MIN', syntax: 'MIN(a, b)', desc: 'Minimum value' },
  { name: 'ROUND', syntax: 'ROUND(a, 2)', desc: 'Round to decimals' },
];

const examples = [
  { label: 'Sum two fields', formula: 'field_1 + field_2' },
  { label: 'Calculate tax (20%)', formula: 'field_1 * 1.2' },
  { label: 'Conditional discount', formula: 'IF(field_1 > 1000, field_1 * 0.9, field_1)' },
];

function insertField(fieldId: string) {
  formula.value += fieldId;
  validate();
}

function insertFunction(syntax: string) {
  formula.value += syntax;
  validate();
}

function applyExample(exampleFormula: string) {
  formula.value = exampleFormula;
  validate();
}

function validate() {
  if (!formula.value.trim()) {
    validationError.value = null;
    emit('update:formula', '');
    return;
  }
  try {
    const result = evaluateFormula(formula.value);
    if (result !== null) {
      validationError.value = null;
      emit('update:formula', formula.value);
    } else {
      validationError.value = 'Formula returned null';
    }
  } catch (error: any) {
    validationError.value = error.message || 'Invalid formula';
  }
}

watch(formula, () => validate(), { immediate: true });
</script>

<template>
  <div class="formula-builder space-y-4">
    <p class="fb-muted text-sm">
      Use field IDs and operators to compute a value. Click fields and functions below to insert.
    </p>

    <!-- Formula editor -->
    <div>
      <label class="mb-1 block text-sm font-medium">Formula</label>
      <Input.TextArea
        :value="displayFormula"
        placeholder="e.g. field_1 + field_2 * 0.2"
        :rows="3"
        style="font-family: monospace"
        @input="onFormulaInput"
      />
      <Alert
        v-if="validationError"
        type="error"
        :message="validationError"
        class="mt-2"
        show-icon
        banner
      />
      <Alert
        v-else-if="previewResult !== null"
        type="success"
        :message="`Preview (sample=10): ${previewResult}`"
        class="mt-2"
        show-icon
        banner
      />
    </div>

    <!-- Insert fields -->
    <div class="fb-section">
      <div class="fb-section-title">Fields</div>
      <div class="flex flex-wrap gap-1">
        <Button
          v-for="f in availableFields"
          :key="f.id"
          size="small"
          @click="insertField(f.id)"
        >
          {{ f.name || f.id }}
        </Button>
        <span v-if="!availableFields.length" class="fb-muted text-sm">
          No number/text fields available.
        </span>
      </div>
    </div>

    <!-- Functions -->
    <div class="fb-section">
      <div class="fb-section-title">Functions</div>
      <div class="flex flex-wrap gap-1">
        <Button
          v-for="func in functions"
          :key="func.name"
          size="small"
          :title="func.desc"
          @click="insertFunction(func.syntax)"
        >
          {{ func.name }}()
        </Button>
      </div>
    </div>

    <!-- Examples -->
    <div class="fb-section">
      <div class="fb-section-title">Examples</div>
      <div class="space-y-1">
        <button
          v-for="example in examples"
          :key="example.label"
          type="button"
          class="fb-example"
          @click="applyExample(example.formula)"
        >
          <Tag color="blue" class="shrink-0 font-mono text-xs">
            {{ formulaToDisplay(example.formula) }}
          </Tag>
          <span class="fb-muted">{{ example.label }}</span>
        </button>
      </div>
    </div>
  </div>
</template>

<style scoped>
.fb-muted { color: hsl(var(--muted-foreground)); }
.fb-section {
  border-radius: 0.5rem;
  border: 1px solid hsl(var(--border));
  background: hsl(var(--muted));
  padding: 0.75rem;
}
.fb-section-title {
  margin-bottom: 0.5rem;
  font-size: 0.75rem;
  font-weight: 600;
  text-transform: uppercase;
  letter-spacing: 0.05em;
  color: hsl(var(--muted-foreground));
}
.fb-example {
  display: flex;
  width: 100%;
  align-items: flex-start;
  gap: 0.5rem;
  border-radius: 0.25rem;
  padding: 0.5rem;
  text-align: left;
  font-size: 0.875rem;
  cursor: pointer;
  background: transparent;
  border: none;
  color: hsl(var(--foreground));
  transition: background 0.15s;
}
.fb-example:hover {
  background: hsl(var(--accent));
}
</style>
