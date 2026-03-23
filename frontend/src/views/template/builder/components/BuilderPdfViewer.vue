<script setup lang="ts">
import { ref, watch, onMounted, onUnmounted, nextTick } from 'vue';
import * as pdfjsLib from 'pdfjs-dist';
import type { BuilderField } from '../composables/useFieldBuilder';
import BuilderFieldOverlay from './BuilderFieldOverlay.vue';

pdfjsLib.GlobalWorkerOptions.workerSrc =
  'https://cdnjs.cloudflare.com/ajax/libs/pdf.js/4.10.38/pdf.worker.min.mjs';

const RENDER_SCALE = 1.5;

interface Props {
  pdfUrl: string;
  fields: readonly BuilderField[];
  selectedFieldId?: string;
}

const props = defineProps<Props>();

const emit = defineEmits<{
  'drop-field': [type: string, pageNumber: number, xPercent: number, yPercent: number, submitterIndex: number];
  'select-field': [id: string];
  'deselect': [];
  'move-field': [id: string, xPercent: number, yPercent: number];
  'resize-field': [id: string, widthPercent: number, heightPercent: number];
}>();

interface PageInfo {
  readonly pageNumber: number;
  readonly width: number;
  readonly height: number;
}

const pagesInfo = ref<readonly PageInfo[]>([]);
const pageContainerRefs = ref<HTMLDivElement[]>([]);
const canvasRefs = ref<HTMLCanvasElement[]>([]);
const loading = ref(false);
const errorMessage = ref('');
const totalPages = ref(0);

let pdfDocument: any = null;

function fieldsForPage(pageNumber: number): readonly BuilderField[] {
  return props.fields.filter((f) => f.pageNumber === pageNumber);
}

function getPageContainerSize(pageIndex: number): { width: number; height: number } {
  const container = pageContainerRefs.value[pageIndex];
  if (!container) return { width: 0, height: 0 };
  return { width: container.clientWidth, height: container.clientHeight };
}

async function loadPdf(url: string): Promise<void> {
  if (!url) return;

  loading.value = true;
  errorMessage.value = '';

  try {
    if (pdfDocument) {
      pdfDocument.destroy();
      pdfDocument = null;
    }

    const loadingTask = pdfjsLib.getDocument({ url });
    pdfDocument = await loadingTask.promise;
    totalPages.value = pdfDocument.numPages;

    const pages: PageInfo[] = [];
    for (let i = 1; i <= pdfDocument.numPages; i++) {
      const page = await pdfDocument.getPage(i);
      const viewport = page.getViewport({ scale: RENDER_SCALE });
      pages.push({
        pageNumber: i,
        width: viewport.width,
        height: viewport.height,
      });
    }
    pagesInfo.value = pages;

    await nextTick();
    await renderAllPages();
  } catch (err: any) {
    errorMessage.value = err?.message ?? 'Failed to load PDF';
  } finally {
    loading.value = false;
  }
}

async function renderAllPages(): Promise<void> {
  if (!pdfDocument) return;

  for (let i = 0; i < pagesInfo.value.length; i++) {
    const pageInfo = pagesInfo.value[i]!;
    const canvas = canvasRefs.value[i];
    if (!canvas) continue;

    const page = await pdfDocument.getPage(pageInfo.pageNumber);
    const viewport = page.getViewport({ scale: RENDER_SCALE });

    canvas.width = viewport.width;
    canvas.height = viewport.height;

    const ctx = canvas.getContext('2d');
    if (!ctx) continue;

    await page.render({
      canvasContext: ctx,
      viewport,
    }).promise;
  }
}

function handleDrop(event: DragEvent, pageNumber: number, pageIndex: number): void {
  event.preventDefault();
  const fieldType = event.dataTransfer?.getData('field-type');
  if (!fieldType) return;

  const submitterIndexStr = event.dataTransfer?.getData('submitter-index') ?? '0';
  const submitterIndex = parseInt(submitterIndexStr, 10) || 0;

  const container = pageContainerRefs.value[pageIndex];
  if (!container) return;

  const rect = container.getBoundingClientRect();
  const xPercent = ((event.clientX - rect.left) / rect.width) * 100;
  const yPercent = ((event.clientY - rect.top) / rect.height) * 100;

  emit('drop-field', fieldType, pageNumber, xPercent, yPercent, submitterIndex);
}

function handleDragOver(event: DragEvent): void {
  event.preventDefault();
  if (event.dataTransfer) {
    event.dataTransfer.dropEffect = 'copy';
  }
}

function handlePageClick(): void {
  emit('deselect');
}

function handleFieldSelect(id: string): void {
  emit('select-field', id);
}

function handleFieldMove(id: string, x: number, y: number): void {
  emit('move-field', id, x, y);
}

function handleFieldResize(id: string, w: number, h: number): void {
  emit('resize-field', id, w, h);
}

watch(
  () => props.pdfUrl,
  (url) => {
    if (url) {
      loadPdf(url);
    }
  },
);

onMounted(() => {
  if (props.pdfUrl) {
    loadPdf(props.pdfUrl);
  }
});

onUnmounted(() => {
  if (pdfDocument) {
    pdfDocument.destroy();
    pdfDocument = null;
  }
});
</script>

<template>
  <div class="pdf-viewer">
    <!-- Loading state -->
    <div v-if="loading" class="pdf-viewer__status">
      Loading PDF...
    </div>

    <!-- Error state -->
    <div v-else-if="errorMessage" class="pdf-viewer__status pdf-viewer__status--error">
      {{ errorMessage }}
    </div>

    <!-- Empty state -->
    <div v-else-if="!pdfUrl" class="pdf-viewer__status">
      No PDF loaded
    </div>

    <!-- Pages -->
    <div
      v-for="(pageInfo, pageIndex) in pagesInfo"
      :key="pageInfo.pageNumber"
      class="pdf-viewer__page-wrapper"
    >
      <div class="pdf-viewer__page-label">
        Page {{ pageInfo.pageNumber }} of {{ totalPages }}
      </div>
      <div
        :ref="(el) => { if (el) pageContainerRefs[pageIndex] = el as HTMLDivElement }"
        class="pdf-viewer__page-container"
        :style="{
          aspectRatio: `${pageInfo.width} / ${pageInfo.height}`,
        }"
        @dragover="handleDragOver"
        @drop="(e) => handleDrop(e, pageInfo.pageNumber, pageIndex)"
        @click.self="handlePageClick"
      >
        <canvas
          :ref="(el) => { if (el) canvasRefs[pageIndex] = el as HTMLCanvasElement }"
          class="pdf-viewer__canvas"
        />

        <!-- Field overlays for this page -->
        <BuilderFieldOverlay
          v-for="field in fieldsForPage(pageInfo.pageNumber)"
          :key="field.id"
          :field="field"
          :selected="field.id === selectedFieldId"
          :page-width="getPageContainerSize(pageIndex).width"
          :page-height="getPageContainerSize(pageIndex).height"
          @select="handleFieldSelect"
          @move="handleFieldMove"
          @resize="handleFieldResize"
        />
      </div>
    </div>
  </div>
</template>

<style scoped>
.pdf-viewer {
  display: flex;
  flex-direction: column;
  gap: 16px;
  overflow-y: auto;
  padding: 16px;
  background: hsl(var(--muted));
  border-radius: 8px;
  height: 100%;
}

.pdf-viewer__status {
  display: flex;
  align-items: center;
  justify-content: center;
  height: 400px;
  font-size: 0.875rem;
  color: hsl(var(--muted-foreground));
  border: 2px dashed hsl(var(--border));
  border-radius: 8px;
  background: hsl(var(--card));
}

.pdf-viewer__status--error {
  color: hsl(0, 84%, 60%);
  border-color: hsl(0, 84%, 60%);
}

.pdf-viewer__page-wrapper {
  display: flex;
  flex-direction: column;
  align-items: center;
  gap: 4px;
}

.pdf-viewer__page-label {
  font-size: 0.75rem;
  color: hsl(var(--muted-foreground));
  padding: 2px 8px;
}

.pdf-viewer__page-container {
  position: relative;
  width: 100%;
  max-width: 100%;
  background: hsl(var(--card));
  box-shadow: 0 2px 8px hsl(var(--foreground) / 0.1);
  border-radius: 2px;
  overflow: hidden;
}

.pdf-viewer__canvas {
  display: block;
  width: 100%;
  height: 100%;
  pointer-events: none;
}
</style>
