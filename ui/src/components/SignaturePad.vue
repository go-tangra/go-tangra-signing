<script setup lang="ts">
// The signer's signature image: drawn with a mouse, pen or finger (pointer
// events on a canvas) or typed and rendered in a script-like font. Every change
// emits the image as a PNG Blob (null when empty); a transparent background
// lets the module lay it over the document. Undo removes the last stroke
// (Ctrl+Z / Cmd+Z inside the pad), Clear empties it. Keyboard users type their
// name (the "Type" mode); drawing needs a pointer.
import { nextTick, onMounted, ref, watch } from 'vue'
import { UiButton, UiInput } from '@go-tangra/ui'

export interface Point { x: number; y: number }
type Mode = 'draw' | 'type'

const props = withDefaults(defineProps<{
  /** Name suggested in the Type mode (the signer's display name). */
  name?: string | undefined
  disabled?: boolean | undefined
}>(), { name: '', disabled: false })
const emit = defineEmits<{ (e: 'change', png: Blob | null): void }>()

/** Internal canvas size (CSS scales it to the pad's width, 3 : 1). */
const WIDTH = 600
const HEIGHT = 200
const INK = '#0f172a'
/** Script-like system fonts only: the edge CSP allows no font downloads. */
const SCRIPT_FONT = '"Segoe Script", "Brush Script MT", "Snell Roundhand", "URW Chancery L", "Apple Chancery", cursive'

const canvas = ref<HTMLCanvasElement | null>(null)
const mode = ref<Mode>('draw')
const strokes = ref<Point[][]>([])
const typed = ref('')
let current: Point[] | null = null

const empty = () => (mode.value === 'draw' ? strokes.value.length === 0 : typed.value.trim() === '')

function ctx(): CanvasRenderingContext2D | null {
  return canvas.value?.getContext('2d') ?? null
}

function drawStroke(c: CanvasRenderingContext2D, s: Point[]): void {
  if (!s.length) return
  c.beginPath()
  c.moveTo(s[0]!.x, s[0]!.y)
  if (s.length === 1) c.lineTo(s[0]!.x + 0.1, s[0]!.y + 0.1)
  for (const p of s.slice(1)) c.lineTo(p.x, p.y)
  c.stroke()
}

/** Redraws the pad from the model (strokes or the typed name). */
function render(): void {
  const c = ctx()
  if (!c) return
  c.clearRect(0, 0, WIDTH, HEIGHT)
  c.strokeStyle = INK
  c.fillStyle = INK
  c.lineWidth = 3
  c.lineCap = 'round'
  c.lineJoin = 'round'
  if (mode.value === 'draw') {
    for (const s of strokes.value) drawStroke(c, s)
  } else if (typed.value.trim()) {
    let size = 72
    c.font = `${size}px ${SCRIPT_FONT}`
    // Shrink long names to fit the pad.
    while (size > 24 && c.measureText(typed.value.trim()).width > WIDTH - 40) {
      size -= 4
      c.font = `${size}px ${SCRIPT_FONT}`
    }
    c.textAlign = 'center'
    c.textBaseline = 'middle'
    c.fillText(typed.value.trim(), WIDTH / 2, HEIGHT / 2)
  }
}

/** The pad as a PNG (null when empty). */
function toBlob(): Promise<Blob | null> {
  const el = canvas.value
  if (!el || empty()) return Promise.resolve(null)
  return new Promise((resolve) => {
    try {
      el.toBlob((b) => resolve(b && b.size > 0 ? b : null), 'image/png')
    } catch {
      resolve(null)
    }
  })
}

async function changed(): Promise<void> {
  render()
  emit('change', await toBlob())
}

// --- drawing ---
function point(e: PointerEvent): Point | null {
  const r = canvas.value?.getBoundingClientRect()
  if (!r || r.width <= 0 || r.height <= 0) return null
  const x = Math.min(WIDTH, Math.max(0, ((e.clientX - r.left) / r.width) * WIDTH))
  const y = Math.min(HEIGHT, Math.max(0, ((e.clientY - r.top) / r.height) * HEIGHT))
  return { x: Math.round(x * 10) / 10, y: Math.round(y * 10) / 10 }
}

function down(e: PointerEvent): void {
  if (props.disabled || mode.value !== 'draw' || (e.pointerType === 'mouse' && e.button !== 0)) return
  const p = point(e)
  if (!p) return
  e.preventDefault()
  canvas.value?.setPointerCapture?.(e.pointerId)
  current = [p]
  strokes.value = [...strokes.value, current]
  render()
}

function move(e: PointerEvent): void {
  if (!current) return
  const p = point(e)
  if (!p) return
  current.push(p)
  const c = ctx()
  const prev = current[current.length - 2]
  if (c && prev) {
    c.beginPath()
    c.moveTo(prev.x, prev.y)
    c.lineTo(p.x, p.y)
    c.stroke()
  }
}

function up(e: PointerEvent): void {
  if (!current) return
  canvas.value?.releasePointerCapture?.(e.pointerId)
  current = null
  strokes.value = [...strokes.value]
  void changed()
}

// --- actions ---
function undo(): void {
  if (mode.value === 'draw' && strokes.value.length) {
    strokes.value = strokes.value.slice(0, -1)
    void changed()
  }
}

function clear(): void {
  if (mode.value === 'draw') strokes.value = []
  else typed.value = ''
  void changed()
}

function setMode(m: Mode): void {
  if (mode.value === m) return
  mode.value = m
  if (m === 'type' && !typed.value) typed.value = props.name
  void changed()
}

function setTyped(v: unknown): void {
  typed.value = String(v ?? '').slice(0, 80)
  void changed()
}

function onKey(e: KeyboardEvent): void {
  if ((e.ctrlKey || e.metaKey) && e.key.toLowerCase() === 'z' && mode.value === 'draw') {
    e.preventDefault()
    undo()
  }
}

watch(() => props.name, (n, old) => {
  if (mode.value === 'type' && (!typed.value || typed.value === old)) setTyped(n)
})

onMounted(() => void nextTick(render))
defineExpose({ clear, undo, toBlob, strokes, mode, typed })
</script>

<template>
  <div class="flex flex-col gap-2" data-test="signature-pad" @keydown="onKey">
    <div class="join" role="group" aria-label="How to sign">
      <button type="button" class="btn btn-sm join-item" :class="mode === 'draw' ? 'btn-primary' : 'btn-soft'" :aria-pressed="mode === 'draw'" :disabled="disabled" data-test="signature-mode-draw" @click="setMode('draw')">Draw</button>
      <button type="button" class="btn btn-sm join-item" :class="mode === 'type' ? 'btn-primary' : 'btn-soft'" :aria-pressed="mode === 'type'" :disabled="disabled" data-test="signature-mode-type" @click="setMode('type')">Type your name</button>
    </div>

    <canvas
      ref="canvas"
      :width="WIDTH"
      :height="HEIGHT"
      class="aspect-[3/1] w-full touch-none rounded-box border border-base-300 bg-white"
      :class="mode === 'draw' && !disabled ? 'cursor-crosshair' : ''"
      role="img"
      :aria-label="mode === 'draw' ? (strokes.length ? 'Your drawn signature' : 'Signature pad: draw your signature with a mouse, pen or finger') : typed.trim() ? `Your typed signature: ${typed.trim()}` : 'Typed signature (empty)'"
      data-test="signature-canvas"
      @pointerdown="down"
      @pointermove="move"
      @pointerup="up"
      @pointercancel="up"
    />

    <UiInput v-if="mode === 'type'" id="signature-typed" :model-value="typed" label="Your name" autocomplete="name" :disabled="disabled" data-test="signature-typed" @update:model-value="setTyped" />

    <div class="flex flex-wrap gap-2">
      <UiButton v-if="mode === 'draw'" size="xs" variant="soft" icon="mdi-restart" :disabled="disabled || !strokes.length" data-test="signature-undo" @click="undo">Undo</UiButton>
      <UiButton size="xs" variant="soft" icon="mdi-close" :disabled="disabled || (mode === 'draw' ? !strokes.length : !typed)" data-test="signature-clear" @click="clear">Clear</UiButton>
    </div>
  </div>
</template>
