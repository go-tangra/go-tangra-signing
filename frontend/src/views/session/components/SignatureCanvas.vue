<script setup lang="ts">
/**
 * Signature component with three signing methods:
 * 1. Draw — freehand drawing on HTML5 canvas
 * 2. Type — type name rendered in cursive font
 * 3. BISS — Qualified Electronic Signature via B-Trust BISS
 */

import { ref, watch, onMounted, onUnmounted, nextTick } from 'vue';
import { useBiss } from '../composables/useBiss';

interface Props {
  width?: number;
  height?: number;
}

const props = withDefaults(defineProps<Props>(), {
  width: 500,
  height: 200,
});

const emit = defineEmits<{
  'update:signature': [data: string];
  'biss-sign': [];
}>();

const mode = ref<'draw' | 'type' | 'biss'>('draw');

// --- Draw mode ---
const canvasRef = ref<HTMLCanvasElement | null>(null);
const isDrawing = ref(false);
const hasDrawContent = ref(false);
let ctx: CanvasRenderingContext2D | null = null;

function initCanvas(): void {
  const canvas = canvasRef.value;
  if (!canvas) return;
  canvas.width = props.width;
  canvas.height = props.height;
  ctx = canvas.getContext('2d');
  if (!ctx) return;
  // Use foreground color from theme for the drawing stroke
  const rootStyles = getComputedStyle(document.documentElement);
  const fg = rootStyles.getPropertyValue('--foreground').trim();
  ctx.strokeStyle = fg ? `hsl(${fg})` : '#1a1a1a';
  ctx.lineWidth = 2.5;
  ctx.lineCap = 'round';
  ctx.lineJoin = 'round';
}

function getPosition(event: MouseEvent | Touch): { x: number; y: number } {
  const canvas = canvasRef.value;
  if (!canvas) return { x: 0, y: 0 };
  const rect = canvas.getBoundingClientRect();
  const scaleX = canvas.width / rect.width;
  const scaleY = canvas.height / rect.height;
  return {
    x: (event.clientX - rect.left) * scaleX,
    y: (event.clientY - rect.top) * scaleY,
  };
}

function startDrawing(event: MouseEvent): void {
  if (!ctx) return;
  isDrawing.value = true;
  const pos = getPosition(event);
  ctx.beginPath();
  ctx.moveTo(pos.x, pos.y);
}

function draw(event: MouseEvent): void {
  if (!isDrawing.value || !ctx) return;
  const pos = getPosition(event);
  ctx.lineTo(pos.x, pos.y);
  ctx.stroke();
  hasDrawContent.value = true;
}

function stopDrawing(): void {
  if (!isDrawing.value) return;
  isDrawing.value = false;
  emitDrawSignature();
}

function handleTouchStart(event: TouchEvent): void {
  event.preventDefault();
  if (!ctx || !event.touches[0]) return;
  isDrawing.value = true;
  const pos = getPosition(event.touches[0]);
  ctx.beginPath();
  ctx.moveTo(pos.x, pos.y);
}

function handleTouchMove(event: TouchEvent): void {
  event.preventDefault();
  if (!isDrawing.value || !ctx || !event.touches[0]) return;
  const pos = getPosition(event.touches[0]);
  ctx.lineTo(pos.x, pos.y);
  ctx.stroke();
  hasDrawContent.value = true;
}

function handleTouchEnd(event: TouchEvent): void {
  event.preventDefault();
  isDrawing.value = false;
  emitDrawSignature();
}

function emitDrawSignature(): void {
  const canvas = canvasRef.value;
  if (!canvas || !hasDrawContent.value) return;
  const data = canvas.toDataURL('image/png');
  emit('update:signature', data);
}

function clearCanvas(): void {
  const canvas = canvasRef.value;
  if (!canvas || !ctx) return;
  ctx.clearRect(0, 0, canvas.width, canvas.height);
  hasDrawContent.value = false;
  emit('update:signature', '');
}

function preventScroll(event: TouchEvent): void {
  if (isDrawing.value) event.preventDefault();
}

// --- Type mode ---
const typedText = ref('');

function renderTypedSignature(): string {
  if (!typedText.value) return '';
  const tempCanvas = document.createElement('canvas');
  tempCanvas.width = 400;
  tempCanvas.height = 100;
  const tCtx = tempCanvas.getContext('2d')!;
  tCtx.font = 'italic 36px "Brush Script MT", "Segoe Script", "Dancing Script", cursive';
  // Typed signature always renders as dark text (it becomes a PNG image on the PDF)
  tCtx.fillStyle = '#1a1a1a';
  tCtx.textBaseline = 'middle';
  tCtx.fillText(typedText.value, 10, 50);
  return tempCanvas.toDataURL('image/png');
}

watch(typedText, () => {
  if (mode.value === 'type') {
    emit('update:signature', renderTypedSignature());
  }
});

// --- BISS mode ---
const biss = useBiss();
const bissDetected = ref(false);

async function detectBiss(): Promise<void> {
  const result = await biss.detect();
  bissDetected.value = result.available;
}

async function selectBissCertificate(): Promise<void> {
  await biss.selectCertificate();
}

// --- Mode switching ---
watch(mode, (newMode) => {
  emit('update:signature', '');
  if (newMode === 'draw') {
    nextTick(() => initCanvas());
  } else if (newMode === 'biss') {
    detectBiss();
  }
});

onMounted(() => {
  initCanvas();
  document.addEventListener('touchmove', preventScroll, { passive: false });
});

onUnmounted(() => {
  document.removeEventListener('touchmove', preventScroll);
});
</script>

<template>
  <div class="sig-component">
    <!-- Mode tabs -->
    <div class="sig-component__tabs">
      <button
        type="button"
        :class="['sig-component__tab', { 'sig-component__tab--active': mode === 'draw' }]"
        @click="mode = 'draw'"
      >
        &#9998; Draw
      </button>
      <button
        type="button"
        :class="['sig-component__tab', { 'sig-component__tab--active': mode === 'type' }]"
        @click="mode = 'type'"
      >
        Aa Type
      </button>
      <button
        type="button"
        :class="['sig-component__tab', { 'sig-component__tab--active': mode === 'biss' }]"
        @click="mode = 'biss'"
      >
        &#128274; BISS (QES)
      </button>
    </div>

    <!-- Draw mode -->
    <div v-if="mode === 'draw'" class="sig-component__content">
      <div class="sig-component__canvas-wrap">
        <canvas
          ref="canvasRef"
          class="sig-component__canvas"
          @mousedown="startDrawing"
          @mousemove="draw"
          @mouseup="stopDrawing"
          @mouseleave="stopDrawing"
          @touchstart="handleTouchStart"
          @touchmove="handleTouchMove"
          @touchend="handleTouchEnd"
        />
      </div>
      <div class="sig-component__actions">
        <button
          type="button"
          class="sig-component__btn"
          :disabled="!hasDrawContent"
          @click="clearCanvas"
        >
          Clear
        </button>
      </div>
    </div>

    <!-- Type mode -->
    <div v-else-if="mode === 'type'" class="sig-component__content">
      <input
        v-model="typedText"
        type="text"
        class="sig-component__type-input"
        placeholder="Type your full name"
      />
      <div v-if="typedText" class="sig-component__type-preview">
        <p class="sig-component__type-preview-text">{{ typedText }}</p>
      </div>
    </div>

    <!-- BISS mode -->
    <div v-else-if="mode === 'biss'" class="sig-component__content">
      <div class="sig-component__biss">
        <!-- Detection -->
        <div v-if="biss.detecting.value" class="sig-component__biss-status">
          Detecting BISS...
        </div>

        <div v-else-if="!biss.status.value.available" class="sig-component__biss-unavailable">
          <p class="sig-component__biss-error">
            BISS is not running.
          </p>
          <p class="sig-component__biss-hint">
            Please start the B-Trust BISS application and insert your smart card.
          </p>
          <button
            type="button"
            class="sig-component__btn sig-component__btn--primary"
            @click="detectBiss"
          >
            Retry Detection
          </button>
        </div>

        <div v-else class="sig-component__biss-ready">
          <div class="sig-component__biss-info">
            <span class="sig-component__biss-badge">&#10003; BISS v{{ biss.status.value.version }}</span>
            <span class="sig-component__biss-port">Port {{ biss.status.value.port }}</span>
          </div>

          <!-- Certificate selection -->
          <div v-if="!biss.certificate.value" class="sig-component__biss-step">
            <p>Step 1: Select your signing certificate</p>
            <button
              type="button"
              class="sig-component__btn sig-component__btn--primary"
              :disabled="biss.signing.value"
              @click="selectBissCertificate"
            >
              <template v-if="biss.signing.value">Waiting for certificate...</template>
              <template v-else>Select Certificate</template>
            </button>
          </div>

          <!-- Certificate selected, ready to sign -->
          <div v-else class="sig-component__biss-step">
            <p>&#10003; Certificate selected</p>
            <button
              type="button"
              class="sig-component__btn sig-component__btn--primary"
              :disabled="biss.signing.value"
              style="width: 100%; padding: 10px;"
              @click="emit('biss-sign')"
            >
              <template v-if="biss.signing.value">Signing in progress...</template>
              <template v-else>&#128274; Sign with QES</template>
            </button>
            <p class="sig-component__biss-hint" style="margin-top: 8px;">
              Your smartcard PIN will be requested by BISS.
            </p>
          </div>
        </div>

        <div v-if="biss.error.value" class="sig-component__biss-error-msg">
          {{ biss.error.value }}
        </div>
      </div>
    </div>
  </div>
</template>

<style scoped>
.sig-component {
  display: flex;
  flex-direction: column;
  gap: 12px;
}

.sig-component__tabs {
  display: flex;
  gap: 4px;
}

.sig-component__tab {
  padding: 6px 14px;
  font-size: 0.8125rem;
  border: 1px solid hsl(var(--border));
  border-radius: var(--radius);
  background: hsl(var(--background));
  color: hsl(var(--foreground));
  cursor: pointer;
  transition: all 0.15s;
}

.sig-component__tab:hover {
  background: hsl(var(--muted));
}

.sig-component__tab--active {
  background: hsl(var(--primary));
  color: hsl(var(--primary-foreground));
  border-color: hsl(var(--primary));
}

.sig-component__content {
  display: flex;
  flex-direction: column;
  gap: 8px;
}

/* Draw */
.sig-component__canvas-wrap {
  border: 2px dashed hsl(var(--border));
  border-radius: var(--radius);
  background: hsl(var(--background));
  padding: 4px;
}

.sig-component__canvas {
  width: 100%;
  max-width: 500px;
  height: 150px;
  cursor: crosshair;
  touch-action: none;
}

.sig-component__actions {
  display: flex;
  justify-content: flex-end;
}

.sig-component__btn {
  padding: 4px 14px;
  font-size: 0.8125rem;
  border: 1px solid hsl(var(--border));
  border-radius: var(--radius);
  background: hsl(var(--background));
  color: hsl(var(--foreground));
  cursor: pointer;
  transition: background 0.15s;
}

.sig-component__btn:hover:not(:disabled) {
  background: hsl(var(--muted));
}

.sig-component__btn:disabled {
  opacity: 0.4;
  cursor: not-allowed;
}

.sig-component__btn--primary {
  background: hsl(var(--primary));
  color: hsl(var(--primary-foreground));
  border-color: hsl(var(--primary));
}

.sig-component__btn--primary:hover:not(:disabled) {
  opacity: 0.9;
}

/* Type */
.sig-component__type-input {
  width: 100%;
  padding: 8px 12px;
  border: 1px solid hsl(var(--input));
  border-radius: var(--radius);
  background: hsl(var(--background));
  font-family: 'Brush Script MT', 'Segoe Script', 'Dancing Script', cursive;
  font-size: 24px;
  font-style: italic;
  color: hsl(var(--foreground));
  outline: none;
}

.sig-component__type-input:focus {
  border-color: hsl(var(--primary));
  box-shadow: 0 0 0 2px hsl(var(--primary) / 0.1);
}

.sig-component__type-preview {
  border: 1px solid hsl(var(--border));
  border-radius: var(--radius);
  background: hsl(var(--background));
  padding: 16px;
  text-align: center;
}

.sig-component__type-preview-text {
  font-family: 'Brush Script MT', 'Segoe Script', 'Dancing Script', cursive;
  font-size: 36px;
  font-style: italic;
  color: hsl(var(--foreground));
  margin: 0;
}

/* BISS */
.sig-component__biss {
  display: flex;
  flex-direction: column;
  gap: 10px;
}

.sig-component__biss-status {
  text-align: center;
  color: hsl(var(--muted-foreground));
  font-size: 0.875rem;
  padding: 20px;
}

.sig-component__biss-unavailable {
  text-align: center;
  padding: 16px;
  display: flex;
  flex-direction: column;
  gap: 8px;
  align-items: center;
}

.sig-component__biss-error {
  color: hsl(var(--destructive));
  font-weight: 600;
  font-size: 0.875rem;
  margin: 0;
}

.sig-component__biss-hint {
  color: hsl(var(--muted-foreground));
  font-size: 0.8125rem;
  margin: 0;
}

.sig-component__biss-ready {
  display: flex;
  flex-direction: column;
  gap: 12px;
}

.sig-component__biss-info {
  display: flex;
  align-items: center;
  gap: 8px;
}

.sig-component__biss-badge {
  background: hsl(143 72% 42% / 0.1);
  color: hsl(143 72% 42%);
  padding: 4px 10px;
  border-radius: 12px;
  font-size: 0.75rem;
  font-weight: 600;
}

.sig-component__biss-port {
  color: hsl(var(--muted-foreground));
  font-size: 0.75rem;
}

.sig-component__biss-step {
  padding: 12px;
  border: 1px solid hsl(var(--border));
  border-radius: var(--radius);
  background: hsl(var(--muted));
}

.sig-component__biss-step p {
  margin: 0 0 8px;
  font-size: 0.8125rem;
  color: hsl(var(--foreground));
}

.sig-component__biss-step p:last-child {
  margin-bottom: 0;
}

.sig-component__biss-error-msg {
  color: hsl(var(--destructive));
  font-size: 0.8125rem;
  padding: 8px;
  background: hsl(var(--destructive) / 0.08);
  border-radius: var(--radius);
}
</style>
