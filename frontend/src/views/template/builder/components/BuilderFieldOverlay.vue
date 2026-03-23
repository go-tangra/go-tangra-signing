<script setup lang="ts">
import { ref, computed } from 'vue';
import type { BuilderField } from '../composables/useFieldBuilder';
import { fieldNames } from '../../../../models/field';

interface Props {
  field: BuilderField;
  selected: boolean;
  pageWidth: number;
  pageHeight: number;
}

const props = defineProps<Props>();

const emit = defineEmits<{
  select: [id: string];
  move: [id: string, xPercent: number, yPercent: number];
  resize: [id: string, widthPercent: number, heightPercent: number];
}>();

const SUBMITTER_COLORS = [
  'hsl(0, 84%, 60%)',
  'hsl(199, 89%, 48%)',
  'hsl(160, 84%, 39%)',
  'hsl(48, 96%, 53%)',
  'hsl(271, 76%, 53%)',
  'hsl(330, 81%, 60%)',
  'hsl(187, 72%, 51%)',
  'hsl(24, 95%, 53%)',
  'hsl(84, 78%, 46%)',
  'hsl(239, 84%, 67%)',
];

const MIN_WIDTH_PCT = 2;
const MIN_HEIGHT_PCT = 1;

const borderColor = computed(() =>
  SUBMITTER_COLORS[props.field.submitterIndex % SUBMITTER_COLORS.length] ?? SUBMITTER_COLORS[0],
);

const overlayStyle = computed(() => ({
  left: `${props.field.xPercent}%`,
  top: `${props.field.yPercent}%`,
  width: `${props.field.widthPercent}%`,
  height: `${props.field.heightPercent}%`,
  borderColor: borderColor.value,
  '--overlay-color': borderColor.value,
}));

const isDragging = ref(false);
const isResizing = ref(false);

function clamp(value: number, min: number, max: number): number {
  return Math.min(max, Math.max(min, value));
}

function handleMouseDown(event: MouseEvent) {
  if (isResizing.value) return;
  event.preventDefault();
  event.stopPropagation();
  emit('select', props.field.id);

  isDragging.value = true;
  const startX = event.clientX;
  const startY = event.clientY;
  const startFieldX = props.field.xPercent;
  const startFieldY = props.field.yPercent;

  function onMouseMove(e: MouseEvent) {
    if (!isDragging.value) return;
    const deltaXPx = e.clientX - startX;
    const deltaYPx = e.clientY - startY;
    const deltaXPct = (deltaXPx / props.pageWidth) * 100;
    const deltaYPct = (deltaYPx / props.pageHeight) * 100;
    const newX = clamp(startFieldX + deltaXPct, 0, 100 - props.field.widthPercent);
    const newY = clamp(startFieldY + deltaYPct, 0, 100 - props.field.heightPercent);
    emit('move', props.field.id, newX, newY);
  }

  function onMouseUp() {
    isDragging.value = false;
    document.removeEventListener('mousemove', onMouseMove);
    document.removeEventListener('mouseup', onMouseUp);
  }

  document.addEventListener('mousemove', onMouseMove);
  document.addEventListener('mouseup', onMouseUp);
}

function handleResizeMouseDown(event: MouseEvent) {
  event.preventDefault();
  event.stopPropagation();
  isResizing.value = true;

  const startX = event.clientX;
  const startY = event.clientY;
  const startW = props.field.widthPercent;
  const startH = props.field.heightPercent;

  function onMouseMove(e: MouseEvent) {
    if (!isResizing.value) return;
    const deltaXPx = e.clientX - startX;
    const deltaYPx = e.clientY - startY;
    const deltaWPct = (deltaXPx / props.pageWidth) * 100;
    const deltaHPct = (deltaYPx / props.pageHeight) * 100;
    const newW = clamp(startW + deltaWPct, MIN_WIDTH_PCT, 100 - props.field.xPercent);
    const newH = clamp(startH + deltaHPct, MIN_HEIGHT_PCT, 100 - props.field.yPercent);
    emit('resize', props.field.id, newW, newH);
  }

  function onMouseUp() {
    isResizing.value = false;
    document.removeEventListener('mousemove', onMouseMove);
    document.removeEventListener('mouseup', onMouseUp);
  }

  document.addEventListener('mousemove', onMouseMove);
  document.addEventListener('mouseup', onMouseUp);
}

function handleClick(event: MouseEvent) {
  event.stopPropagation();
  emit('select', props.field.id);
}
</script>

<template>
  <div
    :class="['field-overlay', { 'field-overlay--selected': selected }]"
    :style="overlayStyle"
    @mousedown="handleMouseDown"
    @click="handleClick"
  >
    <span class="field-overlay__label">
      {{ field.name || fieldNames[field.type] }}
    </span>

    <!-- Resize handle (bottom-right corner) -->
    <div
      v-if="selected"
      class="field-overlay__resize-handle"
      @mousedown="handleResizeMouseDown"
    />
  </div>
</template>

<style scoped>
.field-overlay {
  position: absolute;
  border: 2px solid var(--overlay-color, hsl(var(--primary)));
  background: color-mix(in srgb, var(--overlay-color, hsl(var(--primary))) 12%, transparent);
  border-radius: 2px;
  cursor: grab;
  user-select: none;
  display: flex;
  align-items: center;
  justify-content: center;
  overflow: hidden;
  transition: box-shadow 0.1s;
  z-index: 1;
}

.field-overlay:hover {
  box-shadow: 0 0 0 1px var(--overlay-color, hsl(var(--primary)));
}

.field-overlay--selected {
  box-shadow: 0 0 0 2px var(--overlay-color, hsl(var(--primary)));
  z-index: 2;
}

.field-overlay__label {
  font-size: 10px;
  font-weight: 500;
  color: var(--overlay-color, hsl(var(--foreground)));
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
  padding: 0 4px;
  pointer-events: none;
}

.field-overlay__resize-handle {
  position: absolute;
  bottom: -3px;
  right: -3px;
  width: 10px;
  height: 10px;
  background: var(--overlay-color, hsl(var(--primary)));
  border: 1px solid hsl(var(--card, 0 0% 100%));
  border-radius: 2px;
  cursor: nwse-resize;
  z-index: 3;
}
</style>
