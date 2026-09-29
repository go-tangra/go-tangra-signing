<script setup lang="ts">
// Field types to place. Drag an item onto a page to drop it there, or click it
// to arm it and then click the spot on a page (Escape disarms). Activated from
// the keyboard, an item is added at once in the middle of the current page.
import type { FieldType } from '@/api/types'
import { FIELD_TYPES } from '@/api/types'
import { UiIcon } from '@go-tangra/ui'
import { DRAG_TYPE, TYPE_ICONS, TYPE_LABELS } from '@/utils/fields'

defineProps<{ armed: FieldType | '' }>()
const emit = defineEmits<{ (e: 'arm', t: FieldType | ''): void; (e: 'add', t: FieldType): void }>()

function onDragStart(e: DragEvent, t: FieldType): void {
  if (!e.dataTransfer) return
  e.dataTransfer.setData(DRAG_TYPE, t)
  e.dataTransfer.effectAllowed = 'copy'
  emit('arm', '')
}

function onClick(e: MouseEvent, t: FieldType, armed: FieldType | ''): void {
  // detail 0: activated with Enter/Space rather than a pointer.
  if (e.detail === 0) emit('add', t)
  else emit('arm', armed === t ? '' : t)
}
</script>

<template>
  <div class="flex flex-col gap-2" data-test="field-palette">
    <div class="grid grid-cols-2 gap-1" role="group" aria-label="Field types">
      <button
        v-for="t in FIELD_TYPES"
        :key="t"
        type="button"
        draggable="true"
        class="btn btn-sm justify-start gap-1.5 px-2"
        :class="armed === t ? 'btn-primary' : 'btn-soft'"
        :aria-pressed="armed === t"
        :data-test="'palette-' + t"
        @dragstart="onDragStart($event, t)"
        @click="onClick($event, t, armed)"
      >
        <UiIcon :name="TYPE_ICONS[t]" size="sm" />
        <span class="truncate">{{ TYPE_LABELS[t] }}</span>
      </button>
    </div>
    <p class="text-xs text-base-content/70" aria-live="polite">
      <template v-if="armed">Click a page to place the {{ TYPE_LABELS[armed].toLowerCase() }} field (Esc cancels).</template>
      <template v-else>Drag a type onto a page, or click it and then the spot.</template>
    </p>
  </div>
</template>
