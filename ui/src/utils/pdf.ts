// pdf.js loader. The library is imported on first use so it stays out of every
// view but the builder, and its worker is served from this remote itself
// (never a CDN). Scripted PDFs are not evaluated (isEvalSupported: false).
import workerUrl from 'pdfjs-dist/build/pdf.worker.min.mjs?url'
import type { PDFDocumentProxy } from 'pdfjs-dist'
import { ApiError } from '@/api/client'

type PdfJs = typeof import('pdfjs-dist')
let lib: Promise<PdfJs> | null = null

function pdfjs(): Promise<PdfJs> {
  lib ??= import('pdfjs-dist').then((m) => {
    m.GlobalWorkerOptions.workerSrc = workerUrl
    return m
  })
  return lib
}

/** GETs a PDF through the gateway with the session cookie; refusals become ApiError like the JSON client's. */
export async function fetchPdf(url: string, signal?: AbortSignal): Promise<ArrayBuffer> {
  let res: Response
  try {
    res = await fetch(url, { method: 'GET', headers: { Accept: 'application/pdf' }, credentials: 'same-origin', ...(signal ? { signal } : {}) })
  } catch (err) {
    if (err instanceof DOMException && err.name === 'AbortError') throw err
    throw new ApiError(0, 'network')
  }
  if (!res.ok) {
    const data = (await res.json().catch(() => ({}))) as { reason?: unknown }
    throw new ApiError(res.status, typeof data.reason === 'string' ? data.reason : 'error')
  }
  return res.arrayBuffer()
}

/** Opens PDF bytes with pdf.js. */
export async function openPdf(data: ArrayBuffer): Promise<PDFDocumentProxy> {
  const m = await pdfjs()
  return m.getDocument({ data: new Uint8Array(data), isEvalSupported: false }).promise
}
