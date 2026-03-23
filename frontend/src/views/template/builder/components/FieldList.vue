<script setup lang="ts">
import { h } from 'vue';
import { Button, Tag, Tooltip, Divider, Space } from 'ant-design-vue';
import { LucidePlus, LucideTrash, LucidePencil } from 'shell/vben/icons';
import { $t } from 'shell/locales';
import type { Field, FieldType } from '../../../../models/field';
import { fieldNames, submitterColors, submitterNames } from '../../../../models/field';

interface Props {
  fields: Field[];
  submitters: { id: string; name?: string }[];
  selectedFieldId?: string;
}

const props = defineProps<Props>();

const emit = defineEmits<{
  'select': [fieldId: string];
  'add': [type: FieldType, submitterIndex: number];
  'remove': [fieldId: string];
  'edit': [fieldId: string];
}>();

const fieldTypeOptions: FieldType[] = [
  'text', 'number', 'signature', 'initials', 'date',
  'checkbox', 'select', 'radio', 'image', 'file',
  'cells', 'stamp', 'payment',
];
</script>

<template>
  <div class="field-list">
    <!-- Submitter sections -->
    <div v-for="(submitter, subIdx) in submitters" :key="submitter.id" class="mb-4">
      <div class="mb-2 flex items-center gap-2">
        <div :class="['size-3 rounded-full', submitterColors[subIdx]?.dot ?? 'bg-gray-400']" />
        <span class="text-sm font-medium">{{ submitter.name || submitterNames[subIdx] }}</span>
      </div>

      <!-- Fields for this submitter -->
      <div class="space-y-1">
        <div
          v-for="field in fields.filter(f => f.submitter_id === submitter.id)"
          :key="field.id"
          :class="[
            'field-row flex cursor-pointer items-center justify-between rounded-lg border px-3 py-2 transition-colors',
            field.id === selectedFieldId
              ? 'field-row--selected'
              : 'field-row--default'
          ]"
          @click="emit('select', field.id)"
        >
          <div class="flex items-center gap-2">
            <Tag :color="submitterColors[subIdx]?.dot?.replace('bg-', '') ?? 'default'" class="text-xs">
              {{ fieldNames[field.type] ?? field.type }}
            </Tag>
            <span class="text-sm">{{ field.name || field.id }}</span>
            <Tag v-if="field.required" color="red" class="text-xs">Required</Tag>
            <Tag v-if="field.condition_groups?.length" color="purple" class="text-xs">Conditional</Tag>
            <Tag v-if="field.preferences?.formula || field.formula" color="cyan" class="text-xs">Formula</Tag>
          </div>
          <Space :size="4">
            <Tooltip title="Edit field">
              <Button type="text" size="small" :icon="h(LucidePencil)" @click.stop="emit('edit', field.id)" />
            </Tooltip>
            <Tooltip title="Remove field">
              <Button type="text" danger size="small" :icon="h(LucideTrash)" @click.stop="emit('remove', field.id)" />
            </Tooltip>
          </Space>
        </div>
      </div>

      <!-- Add field buttons -->
      <div class="mt-2 flex flex-wrap gap-1">
        <button
          v-for="fieldType in fieldTypeOptions"
          :key="fieldType"
          class="field-add-btn"
          @click="emit('add', fieldType, subIdx)"
        >
          <LucidePlus class="size-3" />
          {{ fieldNames[fieldType] }}
        </button>
      </div>

      <Divider v-if="subIdx < submitters.length - 1" />
    </div>
  </div>
</template>

<style scoped>
.field-row--default {
  border-color: hsl(var(--border));
}
.field-row--default:hover {
  border-color: hsl(var(--primary));
  background: hsl(var(--accent));
}
.field-row--selected {
  border-color: hsl(var(--primary));
  background: hsl(var(--accent));
}
.field-add-btn {
  display: inline-flex;
  align-items: center;
  gap: 0.25rem;
  border-radius: 0.25rem;
  border: 1px solid hsl(var(--border));
  background: hsl(var(--card));
  color: hsl(var(--foreground));
  padding: 0.25rem 0.5rem;
  font-size: 0.75rem;
  line-height: 1rem;
  cursor: pointer;
  transition: all 0.15s;
}
.field-add-btn:hover {
  border-color: hsl(var(--primary));
  color: hsl(var(--primary));
  background: hsl(var(--accent));
}
</style>
