<script setup lang="ts">
/**
 * PDF viewer for signing sessions.
 *
 * Reuses the same pdfjs-dist approach as BuilderPdfViewer but renders
 * interactive input overlays instead of draggable field placement controls.
 */

import { ref, watch, onMounted, onUnmounted, nextTick } from 'vue';
import * as pdfjsLib from 'pdfjs-dist';
import type { SigningFieldState } from '../composables/useSigningFlow';
import SessionFieldOverlay from './SessionFieldOverlay.vue';

pdfjsLib.GlobalWorkerOptions.workerSrc =
  'https://cdnjs.cloudflare.com/ajax/libs/pdf.js/4.10.38/pdf.worker.min.mjs';

const RENDER_SCALE = 1.5;

interface Props {
  pdfUrl: string;
  fields: readonly SigningFieldState[];
  activeFieldId?: string;
  isFieldFilled: (field: SigningFieldState) => boolean;
}

const props = defineProps<Props>();

const emit = defineEmits<{
  'field-click': [fieldId: string];
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

function fieldsForPage(pageNumber: number): readonly SigningFieldState[] {
  return props.fields.filter((f) => f.pageNumber === pageNumber);
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

function handleFieldClick(fieldId: string): void {
  emit('field-click', fieldId);
}

/**
 * Scroll a specific field into view by its page number.
 */
function scrollToPage(pageNumber: number): void {
  const pageIndex = pageNumber - 1;
  const container = pageContainerRefs.value[pageIndex];
  if (container) {
    container.scrollIntoView({ behavior: 'smooth', block: 'center' });
  }
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

defineExpose({ scrollToPage });
</script>

<template>
  <div class="session-pdf-viewer">
    <!-- Loading state -->
    <div v-if="loading" class="session-pdf-viewer__status">
      Loading PDF...
    </div>

    <!-- Error state -->
    <div
      v-else-if="errorMessage"
      class="session-pdf-viewer__status session-pdf-viewer__status--error"
    >
      {{ errorMessage }}
    </div>

    <!-- Empty state -->
    <div v-else-if="!pdfUrl" class="session-pdf-viewer__status">
      No document loaded
    </div>

    <!-- Pages -->
    <div
      v-for="(pageInfo, pageIndex) in pagesInfo"
      :key="pageInfo.pageNumber"
      class="session-pdf-viewer__page-wrapper"
    >
      <div class="session-pdf-viewer__page-label">
        Page {{ pageInfo.pageNumber }} of {{ totalPages }}
      </div>
      <div
        :ref="(el) => { if (el) pageContainerRefs[pageIndex] = el as HTMLDivElement }"
        class="session-pdf-viewer__page-container"
        :style="{
          aspectRatio: `${pageInfo.width} / ${pageInfo.height}`,
        }"
      >
        <canvas
          :ref="(el) => { if (el) canvasRefs[pageIndex] = el as HTMLCanvasElement }"
          class="session-pdf-viewer__canvas"
        />

        <!-- Field overlays for this page -->
        <SessionFieldOverlay
          v-for="field in fieldsForPage(pageInfo.pageNumber)"
          :key="field.fieldId"
          :field="field"
          :active="field.fieldId === activeFieldId"
          :filled="isFieldFilled(field)"
          @click="handleFieldClick"
        />
      </div>
    </div>
  </div>
</template>

<style scoped>
.session-pdf-viewer {
  display: flex;
  flex-direction: column;
  gap: 16px;
  overflow-y: auto;
  padding: 16px;
  background: hsl(var(--muted));
  border-radius: 8px;
  height: 100%;
}

.session-pdf-viewer__status {
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

.session-pdf-viewer__status--error {
  color: hsl(0, 84%, 60%);
  border-color: hsl(0, 84%, 60%);
}

.session-pdf-viewer__page-wrapper {
  display: flex;
  flex-direction: column;
  align-items: center;
  gap: 4px;
}

.session-pdf-viewer__page-label {
  font-size: 0.75rem;
  color: hsl(var(--muted-foreground));
  padding: 2px 8px;
}

.session-pdf-viewer__page-container {
  position: relative;
  width: 100%;
  max-width: 100%;
  background: hsl(var(--card));
  box-shadow: 0 2px 8px hsl(var(--foreground) / 0.1);
  border-radius: 2px;
  overflow: hidden;
}

.session-pdf-viewer__canvas {
  display: block;
  width: 100%;
  height: 100%;
  pointer-events: none;
}
</style>
