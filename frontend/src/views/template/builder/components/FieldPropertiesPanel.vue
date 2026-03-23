<script setup lang="ts">
import { computed } from 'vue';
import { Button, Form, Input, Select, Switch, InputNumber } from 'ant-design-vue';
import { LucideTrash } from 'shell/vben/icons';
import type { BuilderField } from '../composables/useFieldBuilder';
import type { FieldType } from '../../../../models/field';
import { fieldNames } from '../../../../models/field';

interface Props {
  field: BuilderField | null;
  submitterCount: number;
}

const props = defineProps<Props>();

const emit = defineEmits<{
  update: [id: string, changes: Partial<Pick<BuilderField, 'name' | 'type' | 'required' | 'submitterIndex'>>];
  delete: [id: string];
  'open-conditions': [];
  'open-formula': [];
}>();

const fieldTypeOptions = computed(() =>
  (Object.keys(fieldNames) as FieldType[]).map((type) => ({
    value: type,
    label: fieldNames[type],
  })),
);

function handleNameChange(event: Event) {
  if (!props.field) return;
  const target = event.target as HTMLInputElement;
  emit('update', props.field.id, { name: target.value });
}

function handleTypeChange(value: FieldType) {
  if (!props.field) return;
  emit('update', props.field.id, { type: value });
}

function handleRequiredChange(value: boolean) {
  if (!props.field) return;
  emit('update', props.field.id, { required: value });
}

function handleSubmitterChange(value: number) {
  if (!props.field) return;
  emit('update', props.field.id, { submitterIndex: value });
}

function handleDelete() {
  if (!props.field) return;
  emit('delete', props.field.id);
}
</script>

<template>
  <div class="properties-panel">
    <!-- No field selected -->
    <div v-if="!field" class="properties-panel__empty">
      <p>Select a field to edit its properties</p>
    </div>

    <!-- Field properties -->
    <template v-else>
      <div class="properties-panel__header">
        <span class="properties-panel__title">Field Properties</span>
        <Button
          type="text"
          danger
          size="small"
          @click="handleDelete"
        >
          <LucideTrash class="size-4" />
        </Button>
      </div>

      <Form layout="vertical" class="properties-panel__form">
        <Form.Item label="Name">
          <Input
            :value="field.name"
            @change="handleNameChange"
          />
        </Form.Item>

        <Form.Item label="Type">
          <Select
            :value="field.type"
            :options="fieldTypeOptions"
            @change="handleTypeChange"
          />
        </Form.Item>

        <Form.Item label="Required">
          <Switch
            :checked="field.required"
            @change="handleRequiredChange"
          />
        </Form.Item>

        <Form.Item label="Assigned Party">
          <InputNumber
            :value="field.submitterIndex + 1"
            :min="1"
            :max="submitterCount"
            style="width: 100%"
            @change="(v: number | null) => handleSubmitterChange((v ?? 1) - 1)"
          />
        </Form.Item>

        <!-- Font info (text fields with detected font) -->
        <template v-if="field.type === 'text' && (field.font || field.fontSize)">
          <div class="properties-panel__info properties-panel__font-info">
            <div class="info-row" v-if="field.font">
              <span class="info-row__label">Font</span>
              <span class="info-row__value">{{ field.font }}</span>
            </div>
            <div class="info-row" v-if="field.fontSize">
              <span class="info-row__label">Size</span>
              <span class="info-row__value">{{ field.fontSize.toFixed(1) }} pt</span>
            </div>
          </div>
        </template>

        <!-- Read-only info -->
        <div class="properties-panel__info">
          <div class="info-row">
            <span class="info-row__label">Page</span>
            <span class="info-row__value">{{ field.pageNumber }}</span>
          </div>
          <div class="info-row">
            <span class="info-row__label">Position</span>
            <span class="info-row__value">
              {{ field.xPercent.toFixed(1) }}%, {{ field.yPercent.toFixed(1) }}%
            </span>
          </div>
          <div class="info-row">
            <span class="info-row__label">Size</span>
            <span class="info-row__value">
              {{ field.widthPercent.toFixed(1) }}% &times; {{ field.heightPercent.toFixed(1) }}%
            </span>
          </div>
        </div>

        <!-- Actions -->
        <div class="properties-panel__actions">
          <Button block @click="emit('open-conditions')">
            Conditional Logic
          </Button>
          <Button
            v-if="field.type === 'number' || field.type === 'text'"
            block
            @click="emit('open-formula')"
          >
            Formula
          </Button>
        </div>
      </Form>
    </template>
  </div>
</template>

<style scoped>
.properties-panel {
  display: flex;
  flex-direction: column;
  height: 100%;
  overflow-y: auto;
  padding: 12px;
}

.properties-panel__empty {
  display: flex;
  align-items: center;
  justify-content: center;
  height: 200px;
  color: hsl(var(--muted-foreground));
  font-size: 0.875rem;
  text-align: center;
}

.properties-panel__header {
  display: flex;
  align-items: center;
  justify-content: space-between;
  margin-bottom: 12px;
  padding-bottom: 8px;
  border-bottom: 1px solid hsl(var(--border));
}

.properties-panel__title {
  font-size: 0.875rem;
  font-weight: 600;
  color: hsl(var(--foreground));
}

.properties-panel__form {
  display: flex;
  flex-direction: column;
  gap: 0;
}

.properties-panel__info {
  display: flex;
  flex-direction: column;
  gap: 4px;
  padding: 8px;
  margin-bottom: 12px;
  border-radius: 6px;
  background: hsl(var(--muted));
  border: 1px solid hsl(var(--border));
}

.info-row {
  display: flex;
  justify-content: space-between;
  font-size: 0.75rem;
}

.info-row__label {
  color: hsl(var(--muted-foreground));
}

.info-row__value {
  color: hsl(var(--foreground));
  font-weight: 500;
  font-variant-numeric: tabular-nums;
}

.properties-panel__font-info {
  background: hsl(210, 40%, 96%);
  border-color: hsl(210, 40%, 80%);
}

:deep(.dark) .properties-panel__font-info,
.dark .properties-panel__font-info {
  background: hsl(210, 30%, 20%);
  border-color: hsl(210, 30%, 35%);
}

.properties-panel__actions {
  display: flex;
  flex-direction: column;
  gap: 6px;
}
</style>
