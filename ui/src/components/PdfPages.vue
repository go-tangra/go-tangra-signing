<script setup lang="ts">
// Renders a PDF (fetched from `url`) page by page into canvases as wide as the
// container. Every page gets a placeholder of the right aspect ratio at once;
// a page is drawn only when it scrolls near the viewport (IntersectionObserver)
// and redrawn when the container width changes. The `page` slot is laid over
// each page (absolutely positioned overlays such as the builder's fields).
import { nextTick, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import type { PDFDocumentProxy, RenderTask } from 'pdfjs-dist'
import { UiAlert, UiSkeleton } from '@go-tangra/ui'
import { describe } from '@/api/client'
import { fetchPdf, openPdf } from '@/utils/pdf'
import { vCssom } from '@/utils/cssom'

export interface PageSize { w: number; h: number }

const props = defineProps<{ url: string }>()
const emit = defineEmits<{ (e: 'loaded', sizes: PageSize[]): void }>()

const root = ref<HTMLElement | null>(null)
const sizes = ref<PageSize[]>([])
const loading = ref(false)
const error = ref('')

let doc: PDFDocumentProxy | null = null
let abort: AbortController | null = null
let io: IntersectionObserver | null = null
let ro: ResizeObserver | null = null
let width = 0
const canvases = new Map<number, HTMLCanvasElement>()
const visible = new Set<number>()
/** Page → the CSS width it was drawn at. */
const drawn = new Map<number, number>()
const tasks = new Map<number, RenderTask>()

function setCanvas(n: number, el: unknown): void {
  if (el instanceof HTMLCanvasElement) canvases.set(n, el)
  else canvases.delete(n)
}

async function draw(n: number): Promise<void> {
  const canvas = canvases.get(n)
  const cssWidth = canvas?.parentElement?.clientWidth || width
  if (!doc || !canvas || !cssWidth || drawn.get(n) === cssWidth) return
  drawn.set(n, cssWidth)
  tasks.get(n)?.cancel()
  const page = await doc.getPage(n)
  const base = page.getViewport({ scale: 1 })
  const ratio = window.devicePixelRatio || 1
  const viewport = page.getViewport({ scale: (cssWidth / base.width) * ratio })
  const ctx = canvas.getContext('2d')
  if (!ctx) return
  canvas.width = Math.floor(viewport.width)
  canvas.height = Math.floor(viewport.height)
  const task = page.render({ canvasContext: ctx, viewport })
  tasks.set(n, task)
  try {
    await task.promise
  } catch {
    // Cancelled by a newer draw of the same page (or unmount): nothing to show.
  } finally {
    if (tasks.get(n) === task) tasks.delete(n)
  }
}

function observe(): void {
  io?.disconnect()
  const pages = root.value?.querySelectorAll<HTMLElement>('[data-page]') ?? []
  if (typeof IntersectionObserver === 'undefined') {
    // No observer (old browsers, tests): draw every page.
    pages.forEach((el) => visible.add(Number(el.dataset.page)))
    visible.forEach((n) => void draw(n))
    return
  }
  io = new IntersectionObserver((entries) => {
    for (const e of entries) {
      const n = Number((e.target as HTMLElement).dataset.page)
      if (e.isIntersecting) {
        visible.add(n)
        void draw(n)
      } else visible.delete(n)
    }
  }, { rootMargin: '400px 0px' })
  pages.forEach((el) => io!.observe(el))
}

async function load(): Promise<void> {
  abort?.abort()
  const ctl = new AbortController()
  abort = ctl
  await doc?.destroy()
  doc = null
  drawn.clear()
  visible.clear()
  sizes.value = []
  error.value = ''
  loading.value = true
  try {
    const d = await openPdf(await fetchPdf(props.url, ctl.signal))
    if (ctl.signal.aborted) {
      await d.destroy()
      return
    }
    doc = d
    const out: PageSize[] = []
    for (let n = 1; n <= d.numPages; n++) {
      const vp = (await d.getPage(n)).getViewport({ scale: 1 })
      out.push({ w: vp.width, h: vp.height })
    }
    sizes.value = out
    emit('loaded', out)
    await nextTick()
    observe()
  } catch (e) {
    if (!ctl.signal.aborted) error.value = describe(e)
  } finally {
    if (abort === ctl) loading.value = false
  }
}

/** Scrolls page n (1-based) into view. */
function scrollToPage(n: number): void {
  root.value?.querySelector<HTMLElement>(`[data-page="${n}"]`)?.scrollIntoView({ block: 'start', behavior: 'smooth' })
}
defineExpose({ sizes, scrollToPage })

onMounted(() => {
  width = root.value?.clientWidth ?? 0
  ro = new ResizeObserver(() => {
    const w = root.value?.clientWidth ?? 0
    if (Math.abs(w - width) < 2) return
    width = w
    visible.forEach((n) => void draw(n))
  })
  if (root.value) ro.observe(root.value)
  void load()
})
watch(() => props.url, () => void load())
onBeforeUnmount(() => {
  abort?.abort()
  io?.disconnect()
  ro?.disconnect()
  tasks.forEach((t) => t.cancel())
  void doc?.destroy()
})
</script>

<template>
  <div ref="root" class="mx-auto flex w-full max-w-4xl flex-col gap-4" data-test="pdf-pages">
    <UiAlert v-if="error" kind="error" data-test="pdf-error">{{ error }}</UiAlert>
    <UiSkeleton v-else-if="loading && !sizes.length" kind="card" :lines="6" />
    <div
      v-for="(s, i) in sizes"
      :key="i"
      v-cssom="{ 'aspect-ratio': `${s.w} / ${s.h}` }"
      :data-page="i + 1"
      role="group"
      :aria-label="`Page ${i + 1} of ${sizes.length}`"
      class="relative w-full overflow-hidden bg-white shadow-md"
    >
      <canvas :ref="(el) => setCanvas(i + 1, el)" class="absolute inset-0 h-full w-full" aria-hidden="true" />
      <slot name="page" :page="i + 1" :size="s" />
    </div>
  </div>
</template>
