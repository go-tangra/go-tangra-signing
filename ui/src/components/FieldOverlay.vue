<script setup lang="ts">
// The fields of one page, laid over its canvas. Boxes are positioned in
// percent of the page (the stored fractions), so they follow any zoom.
// Pointer: press a box to select it and drag to move it; drag the corner
// handle to resize; with a palette type armed, a click on the page places a
// field there; palette items can also be dropped onto the page.
// Keyboard (focused box): arrows move (Shift: larger steps), Alt+arrows
// resize, Delete removes, Escape clears the selection.
import { computed, ref } from 'vue'
import type { Field, FieldType, Party } from '@/api/types'
import { FIELD_TYPES } from '@/api/types'
import { clamp, moveBox, resizeBox, type Box } from '@/utils/geometry'
import { DRAG_TYPE, TYPE_LABELS, partyColor } from '@/utils/fields'
import { vCssom } from '@/utils/cssom'

const props = withDefaults(defineProps<{
  page: number
  fields: Field[]
  parties: Party[]
  selectedId?: string | undefined
  proposed?: ReadonlySet<string> | undefined
  readonly?: boolean | undefined
  /** Palette type waiting for a click on the page. */
  armed?: FieldType | '' | undefined
}>(), { selectedId: '', proposed: () => new Set<string>(), readonly: false, armed: '' })
const emit = defineEmits<{
  (e: 'select', id: string): void
  (e: 'box', id: string, box: Box): void
  /** Keyboard steps as deltas, applied to the current geometry (key repeat never works on a stale box). */
  (e: 'nudge', id: string, dx: number, dy: number): void
  (e: 'grow', id: string, dw: number, dh: number): void
  (e: 'remove', id: string): void
  (e: 'place', type: FieldType, page: number, fx: number, fy: number): void
}>()

const root = ref<HTMLElement | null>(null)
const onPage = computed(() => props.fields.filter((f) => f.page === props.page))
const partyName = (key: string) => props.parties.find((p) => p.key === key)?.name ?? key

/** Pointer position as page fractions (clamped to the page). */
function fraction(e: { clientX: number; clientY: number }): { fx: number; fy: number } | null {
  const r = root.value?.getBoundingClientRect()
  if (!r || r.width <= 0 || r.height <= 0) return null
  return { fx: clamp((e.clientX - r.left) / r.width, 0, 1), fy: clamp((e.clientY - r.top) / r.height, 0, 1) }
}

function onPagePointer(e: PointerEvent): void {
  if (e.target !== root.value) return
  if (props.armed && !props.readonly) {
    const p = fraction(e)
    if (p) emit('place', props.armed, props.page, p.fx, p.fy)
    return
  }
  emit('select', '')
}

function onDrop(e: DragEvent): void {
  if (props.readonly) return
  const t = e.dataTransfer?.getData(DRAG_TYPE) ?? ''
  const p = fraction(e)
  if (p && (FIELD_TYPES as readonly string[]).includes(t)) emit('place', t as FieldType, props.page, p.fx, p.fy)
}

// --- drag to move / resize ---
let drag: { id: string; mode: 'move' | 'resize'; sx: number; sy: number; box: Box; w: number; h: number } | null = null

function start(e: PointerEvent, f: Field, mode: 'move' | 'resize'): void {
  emit('select', f.id)
  ;(e.currentTarget as HTMLElement).closest<HTMLElement>('[data-field-id]')?.focus()
  if (props.readonly || e.button !== 0) return
  const r = root.value?.getBoundingClientRect()
  if (!r || r.width <= 0 || r.height <= 0) return
  e.preventDefault()
  ;(e.currentTarget as HTMLElement).setPointerCapture?.(e.pointerId)
  drag = { id: f.id, mode, sx: e.clientX, sy: e.clientY, box: { x: f.x, y: f.y, w: f.w, h: f.h }, w: r.width, h: r.height }
}

function onMove(e: PointerEvent): void {
  if (!drag) return
  const dx = (e.clientX - drag.sx) / drag.w
  const dy = (e.clientY - drag.sy) / drag.h
  emit('box', drag.id, drag.mode === 'move' ? moveBox(drag.box, dx, dy) : resizeBox(drag.box, dx, dy))
}

function end(e: PointerEvent): void {
  if (!drag) return
  ;(e.currentTarget as HTMLElement).releasePointerCapture?.(e.pointerId)
  drag = null
}

// --- keyboard ---
const ARROWS: Record<string, [number, number]> = { ArrowLeft: [-1, 0], ArrowRight: [1, 0], ArrowUp: [0, -1], ArrowDown: [0, 1] }
function onKey(e: KeyboardEvent, f: Field): void {
  const a = ARROWS[e.key]
  if (a) {
    if (props.readonly) return
    e.preventDefault()
    const step = e.shiftKey ? 0.05 : 0.005
    if (e.altKey) emit('grow', f.id, a[0] * step, a[1] * step)
    else emit('nudge', f.id, a[0] * step, a[1] * step)
  } else if ((e.key === 'Delete' || e.key === 'Backspace') && !props.readonly) {
    e.preventDefault()
    emit('remove', f.id)
  } else if (e.key === 'Escape') {
    emit('select', '')
  } else if (e.key === 'Enter' || e.key === ' ') {
    e.preventDefault()
    emit('select', f.id)
  }
}

const pct = (v: number) => `${(v * 100).toFixed(3)}%`
const label = (f: Field) => `${TYPE_LABELS[f.type]} field ${f.name} for ${partyName(f.party)}, page ${f.page}${f.required ? ', required' : ''}${props.proposed.has(f.id) ? ', proposed' : ''}`
</script>

<template>
  <div
    ref="root"
    class="absolute inset-0"
    :class="armed && !readonly ? 'cursor-crosshair' : ''"
    :data-test="'overlay-' + page"
    @pointerdown="onPagePointer"
    @dragover.prevent
    @drop.prevent="onDrop"
  >
    <div
      v-for="f in onPage"
      :key="f.id"
      v-cssom="{ left: pct(f.x), top: pct(f.y), width: pct(f.w), height: pct(f.h) }"
      role="button"
      tabindex="0"
      :data-field-id="f.id"
      :data-test="'field-' + f.id"
      :aria-label="label(f)"
      :aria-pressed="selectedId === f.id"
      class="absolute flex touch-none items-start overflow-hidden rounded-sm border-2 text-[10px] leading-tight outline-none select-none focus-visible:ring-2 focus-visible:ring-primary"
      :class="[partyColor(parties, f.party).box, proposed.has(f.id) ? 'border-dashed' : '', selectedId === f.id ? 'z-10 ring-2 ring-base-content/60' : '', readonly ? '' : 'cursor-move']"
      @pointerdown.stop="start($event, f, 'move')"
      @pointermove="onMove"
      @pointerup="end"
      @pointercancel="end"
      @keydown="onKey($event, f)"
    >
      <span class="truncate px-0.5 text-black/80">{{ f.name }}<template v-if="f.required">*</template></span>
      <span
        v-if="!readonly && selectedId === f.id"
        class="absolute right-0 bottom-0 size-2.5 cursor-se-resize bg-black/70"
        aria-hidden="true"
        :data-test="'resize-' + f.id"
        @pointerdown.stop="start($event, f, 'resize')"
        @pointermove="onMove"
        @pointerup="end"
        @pointercancel="end"
      />
    </div>
  </div>
</template>
