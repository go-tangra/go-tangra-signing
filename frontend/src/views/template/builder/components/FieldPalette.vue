<script setup lang="ts">
import { ref, computed } from 'vue';
import {
  LucideType,
  LucideHash,
  LucidePenTool,
  LucideLetterText,
  LucideCalendar,
  LucideCheckSquare,
  LucideList,
  LucideCircleDot,
  LucideImage,
  LucidePaperclip,
  LucideColumns3,
  LucideStamp,
  LucideCreditCard,
} from 'shell/vben/icons';
import type { FieldType } from '../../../../models/field';
import { fieldNames, submitterColors, submitterNames } from '../../../../models/field';

interface Submitter {
  readonly id: string;
  readonly name?: string;
}

interface Props {
  submitters: readonly Submitter[];
}

const props = defineProps<Props>();

const emit = defineEmits<{
  'add-party': [];
  'remove-party': [];
}>();

const activeSubmitterIndex = ref(0);

const FIELD_GROUPS = [
  {
    label: 'Common',
    types: ['text', 'number', 'signature', 'initials', 'date'] as FieldType[],
  },
  {
    label: 'Form',
    types: ['checkbox', 'select', 'radio'] as FieldType[],
  },
  {
    label: 'Media',
    types: ['image', 'file'] as FieldType[],
  },
  {
    label: 'Special',
    types: ['cells', 'stamp', 'payment'] as FieldType[],
  },
] as const;

const FIELD_ICONS: Record<string, any> = {
  text: LucideType,
  number: LucideHash,
  signature: LucidePenTool,
  initials: LucideLetterText,
  date: LucideCalendar,
  checkbox: LucideCheckSquare,
  select: LucideList,
  radio: LucideCircleDot,
  image: LucideImage,
  file: LucidePaperclip,
  cells: LucideColumns3,
  stamp: LucideStamp,
  payment: LucideCreditCard,
};

function handleDragStart(event: DragEvent, type: FieldType) {
  if (!event.dataTransfer) return;
  event.dataTransfer.setData('field-type', type);
  event.dataTransfer.setData('submitter-index', String(activeSubmitterIndex.value));
  event.dataTransfer.effectAllowed = 'copy';
}

const activeSubmitter = computed(() =>
  props.submitters[activeSubmitterIndex.value],
);
</script>

<template>
  <div class="field-palette">
    <!-- Submitter selector -->
    <div class="palette-section">
      <div class="palette-section__header">Party</div>
      <div class="submitter-tabs">
        <button
          v-for="(sub, idx) in submitters"
          :key="sub.id"
          :class="[
            'submitter-tab',
            { 'submitter-tab--active': idx === activeSubmitterIndex },
          ]"
          :style="{
            '--tab-color': `var(--submitter-color-${idx}, hsl(0, 84%, 60%))`,
          }"
          @click="activeSubmitterIndex = idx"
        >
          <span
            class="submitter-tab__dot"
            :class="submitterColors[idx]?.dot ?? 'bg-gray-400'"
          />
          <span class="submitter-tab__name">
            {{ sub.name || submitterNames[idx] || `Party ${idx + 1}` }}
          </span>
        </button>
      </div>
      <div class="submitter-actions">
        <button class="palette-btn" @click="emit('add-party')">
          + Add Party
        </button>
        <button
          v-if="submitters.length > 1"
          class="palette-btn palette-btn--danger"
          @click="emit('remove-party')"
        >
          Remove Last
        </button>
      </div>
    </div>

    <!-- Field type groups -->
    <div
      v-for="group in FIELD_GROUPS"
      :key="group.label"
      class="palette-section"
    >
      <div class="palette-section__header">{{ group.label }}</div>
      <div class="palette-grid">
        <button
          v-for="type in group.types"
          :key="type"
          class="palette-field-btn"
          draggable="true"
          @dragstart="(e) => handleDragStart(e, type)"
        >
          <component
            :is="FIELD_ICONS[type]"
            v-if="FIELD_ICONS[type]"
            class="palette-field-btn__icon"
          />
          <span class="palette-field-btn__label">{{ fieldNames[type] }}</span>
        </button>
      </div>
    </div>

    <div class="palette-hint">
      Drag fields onto the PDF to place them.
    </div>
  </div>
</template>

<style scoped>
.field-palette {
  display: flex;
  flex-direction: column;
  gap: 12px;
  height: 100%;
  overflow-y: auto;
  padding: 12px;
}

.palette-section {
  display: flex;
  flex-direction: column;
  gap: 6px;
}

.palette-section__header {
  font-size: 0.6875rem;
  font-weight: 600;
  text-transform: uppercase;
  letter-spacing: 0.05em;
  color: hsl(var(--muted-foreground));
}

.submitter-tabs {
  display: flex;
  flex-direction: column;
  gap: 2px;
}

.submitter-tab {
  display: flex;
  align-items: center;
  gap: 6px;
  padding: 4px 8px;
  border-radius: 4px;
  border: 1px solid transparent;
  background: transparent;
  color: hsl(var(--foreground));
  font-size: 0.75rem;
  cursor: pointer;
  transition: all 0.15s;
  text-align: left;
}

.submitter-tab:hover {
  background: hsl(var(--accent));
}

.submitter-tab--active {
  border-color: hsl(var(--primary));
  background: hsl(var(--accent));
}

.submitter-tab__dot {
  width: 8px;
  height: 8px;
  border-radius: 50%;
  flex-shrink: 0;
}

.submitter-tab__name {
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.submitter-actions {
  display: flex;
  gap: 4px;
}

.palette-btn {
  flex: 1;
  padding: 3px 6px;
  border-radius: 4px;
  border: 1px solid hsl(var(--border));
  background: hsl(var(--card));
  color: hsl(var(--foreground));
  font-size: 0.6875rem;
  cursor: pointer;
  transition: all 0.15s;
}

.palette-btn:hover {
  border-color: hsl(var(--primary));
  color: hsl(var(--primary));
}

.palette-btn--danger {
  border-color: hsl(0, 84%, 60%);
  color: hsl(0, 84%, 60%);
}

.palette-btn--danger:hover {
  background: hsl(0 84% 60% / 0.1);
}

.palette-grid {
  display: grid;
  grid-template-columns: 1fr 1fr;
  gap: 4px;
}

.palette-field-btn {
  display: flex;
  align-items: center;
  gap: 4px;
  padding: 6px 8px;
  border-radius: 4px;
  border: 1px solid hsl(var(--border));
  background: hsl(var(--card));
  color: hsl(var(--foreground));
  font-size: 0.6875rem;
  cursor: grab;
  transition: all 0.15s;
  user-select: none;
}

.palette-field-btn:hover {
  border-color: hsl(var(--primary));
  color: hsl(var(--primary));
  background: hsl(var(--accent));
}

.palette-field-btn:active {
  cursor: grabbing;
}

.palette-field-btn__icon {
  width: 14px;
  height: 14px;
  flex-shrink: 0;
}

.palette-field-btn__label {
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.palette-hint {
  font-size: 0.6875rem;
  color: hsl(var(--muted-foreground));
  text-align: center;
  padding: 8px 0;
  border-top: 1px solid hsl(var(--border));
  margin-top: auto;
}
</style>
