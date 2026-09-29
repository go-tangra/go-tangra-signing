import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { createPinia, setActivePinia } from 'pinia'
import { flushPromises, mount } from '@vue/test-utils'
import { createMemoryHistory, createRouter, RouterView } from 'vue-router'
import { h } from 'vue'
import { useConfirm } from '@go-tangra/ui'
import Builder from '@/views/builder/index.vue'
import { useBuilder } from '@/stores/builder'
import { OPERATOR, READER, field, fetchMock, template, withAbility, type Reply } from './helpers'

// pdf.js is never run in jsdom: a two-page 600 × 800 pt document stands in.
const pdf = vi.hoisted(() => ({ GlobalWorkerOptions: { workerSrc: '' }, getDocument: vi.fn() }))
vi.mock('pdfjs-dist', () => pdf)
const render = vi.fn(() => ({ promise: Promise.resolve(), cancel: () => {} }))
const fakeDoc = {
  numPages: 2,
  getPage: async () => ({ getViewport: ({ scale }: { scale: number }) => ({ width: 600 * scale, height: 800 * scale }), render }),
  destroy: async () => {},
}

beforeEach(() => {
  setActivePinia(createPinia())
  document.cookie = '__Host-csrf=tok; Secure; Path=/'
  pdf.getDocument.mockReset()
  pdf.getDocument.mockImplementation(() => ({ promise: Promise.resolve(fakeDoc) }))
  render.mockClear()
  vi.spyOn(HTMLCanvasElement.prototype, 'getContext').mockReturnValue({} as never)
})
afterEach(() => {
  vi.unstubAllGlobals()
  vi.restoreAllMocks()
  document.body.innerHTML = ''
})

const q = (sel: string) => document.body.querySelector<HTMLElement>(sel)
const PDF_BYTES = new TextEncoder().encode('%PDF-1.7\n').buffer as ArrayBuffer

function api(extra: (path: string, method: string, body: unknown) => Reply | undefined = () => undefined) {
  return (path: string, method: string, body: unknown): Reply => {
    const r = extra(path, method, body)
    if (r) return r
    if (path === 'templates/t1' && method === 'GET') return { body: template() }
    if (path === 'templates/t1/pdf') return { raw: PDF_BYTES, type: 'application/pdf' }
    if (path === 'templates/t1/fields' && method === 'PUT') {
      const b = body as { version: number; parties: never; fields: never }
      return { body: template({ version: b.version + 1, parties: b.parties, fields: b.fields }) }
    }
    return { status: 404, body: { reason: 'not_found' } }
  }
}

async function open(rules = OPERATOR) {
  const router = createRouter({
    history: createMemoryHistory(),
    routes: [
      { path: '/signing/templates', name: 'signing-templates', component: { render: () => h('p', { 'data-test': 'list-page' }, 'list') } },
      { path: '/signing/templates/:id/builder', name: 'signing-builder', component: Builder },
    ],
  })
  await router.push('/signing/templates/t1/builder')
  const w = mount({ render: () => h(RouterView) }, { global: { plugins: [router, withAbility(rules).plugins[0]] as never }, attachTo: document.body })
  await flushPromises()
  return { w, router }
}

/** Gives the page overlays a 600 × 800 px box at the origin. */
function sizeOverlays(): void {
  vi.spyOn(HTMLElement.prototype, 'getBoundingClientRect').mockReturnValue({ left: 0, top: 0, width: 600, height: 800, right: 600, bottom: 800, x: 0, y: 0, toJSON: () => ({}) } as DOMRect)
}
function pointer(el: HTMLElement, type: string, x: number, y: number): void {
  el.dispatchEvent(new MouseEvent(type, { bubbles: true, cancelable: true, clientX: x, clientY: y, button: 0 }))
}

describe('builder view', () => {
  it('renders the PDF pages with pdf.js (local worker, no eval) and lays the fields over them', async () => {
    const calls = fetchMock(api())
    // jsdom lays nothing out: the pages get a 600 px wide container.
    vi.spyOn(HTMLElement.prototype, 'clientWidth', 'get').mockReturnValue(600)
    const { w } = await open()
    expect(calls.map((c) => c.url)).toContain('/api/signing/v1/templates/t1/pdf')
    expect(pdf.getDocument).toHaveBeenCalledWith(expect.objectContaining({ isEvalSupported: false }))
    expect(pdf.GlobalWorkerOptions.workerSrc).toMatch(/pdf\.worker\.min\.mjs/)
    expect(pdf.GlobalWorkerOptions.workerSrc).not.toMatch(/^https?:/)
    expect(document.querySelectorAll('[data-page]')).toHaveLength(2)
    // Without an IntersectionObserver (jsdom) every page is drawn, at the container width.
    expect(render).toHaveBeenCalledTimes(2)
    expect(document.querySelector<HTMLCanvasElement>('[data-page="1"] canvas')!.width).toBe(600 * (window.devicePixelRatio || 1))
    expect(document.querySelector<HTMLElement>('[data-page="1"]')!.style.aspectRatio).toBe('600 / 800')
    const box = q('[data-test="field-f-a"]')!
    expect(box.getAttribute('aria-label')).toBe('Text field Salary for Employee, page 1')
    expect([box.style.left, box.style.top, box.style.width, box.style.height]).toEqual(['10%', '20%', '25%', '3%'])
    expect(q('[data-test="builder-status"]')!.textContent).toBe('Draft')
    w.unmount()
  })

  it('palette: keyboard adds in the middle of the page; click arms, then a click on the page places at that point', async () => {
    fetchMock(api())
    const { w } = await open()
    const b = useBuilder()
    q('[data-test="palette-signature"]')!.click() // detail 0 = keyboard activation
    await flushPromises()
    expect(b.fields.at(-1)).toMatchObject({ type: 'signature', page: 1, x: 0.375, y: 0.465, party: 'p1' })
    expect(q('[data-test="builder-dirty"]')).not.toBeNull()

    q('[data-test="palette-date"]')!.dispatchEvent(new MouseEvent('click', { bubbles: true, detail: 1 }))
    await flushPromises()
    expect(q('[data-test="palette-date"]')!.getAttribute('aria-pressed')).toBe('true')
    sizeOverlays()
    pointer(q('[data-test="overlay-2"]')!, 'pointerdown', 300, 200)
    await flushPromises()
    expect(b.fields.at(-1)).toMatchObject({ type: 'date', page: 2, x: 0.425, y: 0.235 })
    expect(q('[data-test="palette-date"]')!.getAttribute('aria-pressed')).toBe('false')
    w.unmount()
  })

  it('dropping a dragged palette type places the field at the drop point', async () => {
    fetchMock(api())
    const { w } = await open()
    sizeOverlays()
    const drop = new MouseEvent('drop', { bubbles: true, cancelable: true, clientX: 60, clientY: 400 }) as DragEvent
    Object.defineProperty(drop, 'dataTransfer', { value: { getData: (t: string) => (t === 'application/x-signing-field' ? 'checkbox' : '') } })
    q('[data-test="overlay-1"]')!.dispatchEvent(drop)
    await flushPromises()
    expect(useBuilder().fields.at(-1)).toMatchObject({ type: 'checkbox', page: 1, x: 0.085, y: 0.489 })
    w.unmount()
  })

  it('fields move with the arrows, resize with Alt+arrows and by dragging, and Delete removes them', async () => {
    fetchMock(api())
    const { w } = await open()
    const b = useBuilder()
    const box = q('[data-test="field-f-a"]')!
    box.dispatchEvent(new KeyboardEvent('keydown', { key: 'ArrowRight', bubbles: true }))
    box.dispatchEvent(new KeyboardEvent('keydown', { key: 'ArrowDown', shiftKey: true, bubbles: true }))
    await flushPromises()
    expect(b.fields[0]).toMatchObject({ x: 0.105, y: 0.25 })
    box.dispatchEvent(new KeyboardEvent('keydown', { key: 'ArrowRight', altKey: true, bubbles: true }))
    await flushPromises()
    expect(b.fields[0]!.w).toBe(0.255)

    sizeOverlays()
    pointer(box, 'pointerdown', 100, 100)
    pointer(box, 'pointermove', 700, 100) // far past the right edge
    pointer(box, 'pointerup', 700, 100)
    await flushPromises()
    expect(b.selectedId).toBe('f-a')
    expect(b.fields[0]).toMatchObject({ x: 0.745, y: 0.25 })

    const handle = q('[data-test="resize-f-a"]')!
    pointer(handle, 'pointerdown', 0, 0)
    pointer(handle, 'pointermove', -600, -800)
    pointer(handle, 'pointerup', -600, -800)
    await flushPromises()
    expect(b.fields[0]).toMatchObject({ w: 0.02, h: 0.01 })

    q('[data-test="field-f-a"]')!.dispatchEvent(new KeyboardEvent('keydown', { key: 'Delete', bubbles: true }))
    await flushPromises()
    expect(b.fields).toHaveLength(0)
    w.unmount()
  })

  it('the properties panel edits the selected field', async () => {
    fetchMock(api())
    const { w } = await open()
    const b = useBuilder()
    q('[data-test="field-item-f-a"]')!.click()
    await flushPromises()
    const name = q('#field-prop-name') as HTMLInputElement
    name.value = 'Monthly salary'
    name.dispatchEvent(new Event('input'))
    const req = q('#field-prop-required') as HTMLInputElement
    req.click()
    const party = q('#field-prop-party') as HTMLSelectElement
    party.value = 'p2'
    party.dispatchEvent(new Event('change'))
    const type = q('#field-prop-type') as HTMLSelectElement
    type.value = 'select'
    type.dispatchEvent(new Event('change'))
    await flushPromises()
    expect(b.fields[0]).toMatchObject({ name: 'Monthly salary', required: true, party: 'p2', type: 'select', options: ['Option 1', 'Option 2'] })
    const opts = q('#field-prop-options') as HTMLTextAreaElement
    opts.value = 'Monthly\nYearly'
    opts.dispatchEvent(new Event('input'))
    await flushPromises()
    expect(b.fields[0]!.options).toEqual(['Monthly', 'Yearly'])
    expect(q('[data-test="field-rules"]')).not.toBeNull()
    w.unmount()
  })

  it('save stores the fields with the version; a newer save elsewhere asks to reload', async () => {
    let conflict = false
    const calls = fetchMock(api((path, method) => (conflict && method === 'PUT' ? { status: 409, body: { reason: 'version_conflict' } } : undefined)))
    const { w } = await open()
    const save = () => q('[data-test="builder-save"]') as HTMLButtonElement
    expect(save().disabled).toBe(true)
    q('[data-test="palette-text"]')!.click()
    await flushPromises()
    save().click()
    await flushPromises()
    expect((calls.find((c) => c.method === 'PUT')!.body as { version: number }).version).toBe(3)
    expect(q('[data-test="builder-dirty"]')).toBeNull()

    conflict = true
    q('[data-test="palette-text"]')!.click()
    await flushPromises()
    save().click()
    await flushPromises()
    expect(q('[data-test="builder-conflict"]')!.textContent).toContain('Reload')
    const gets = calls.filter((c) => c.url.endsWith('/templates/t1')).length
    q('[data-test="builder-reload"]')!.click()
    await flushPromises()
    expect(calls.filter((c) => c.url.endsWith('/templates/t1')).length).toBe(gets + 1)
    expect(q('[data-test="builder-conflict"]')).toBeNull()
    w.unmount()
  })

  it('auto-detect adds the proposals, marked until saved', async () => {
    fetchMock(api((path) => (path === 'templates/t1/detect-fields' ? { body: { fields: [field({ id: 'detected-1', name: 'Text 1', y: 0.7 })] } } : undefined)))
    const { w } = await open()
    q('[data-test="builder-detect"]')!.click()
    await flushPromises()
    const added = useBuilder().fields[1]!
    expect(q(`[data-test="field-${added.id}"]`)!.getAttribute('aria-label')).toContain('proposed')
    expect(q('[data-test="builder-field-list"]')!.textContent).toContain('proposed')
    w.unmount()
  })

  it('removing a party with fields asks to move or delete them', async () => {
    fetchMock(api())
    const { w } = await open()
    const b = useBuilder()
    q('[data-test="party-remove-p1"]')!.click()
    await flushPromises()
    expect(q('[data-test="party-remove-dialog"]')!.textContent).toContain('1 field(s)')
    q('[data-test="party-remove-move"]')!.click()
    await flushPromises()
    expect(b.parties.map((p) => p.key)).toEqual(['p2'])
    expect(b.fields[0]!.party).toBe('p2')
    expect(q('[data-test="party-remove-p2"]')).toBeNull() // the last party stays
    q('[data-test="party-add"]')!.click()
    await flushPromises()
    expect(q('[data-test="party-p1"]')).not.toBeNull()
    w.unmount()
  })

  it('leaving with unsaved changes asks first', async () => {
    fetchMock(api())
    const confirm = useConfirm()
    const { w, router } = await open()
    q('[data-test="palette-text"]')!.click()
    await flushPromises()
    const nav = router.push('/signing/templates')
    await flushPromises()
    expect(confirm.state.pending?.title).toBe('Discard unsaved changes?')
    confirm.answer(false)
    await nav
    expect(router.currentRoute.value.name).toBe('signing-builder')
    q('[data-test="builder-back"]')!.click()
    await flushPromises()
    confirm.answer(true)
    await flushPromises()
    expect(router.currentRoute.value.name).toBe('signing-templates')
    w.unmount()
  })

  it('without template management the builder is read-only', async () => {
    fetchMock(api())
    const { w, router } = await open(READER)
    for (const sel of ['field-palette', 'builder-save', 'builder-detect', 'builder-activate', 'party-add', 'party-remove-p1']) expect(q(`[data-test="${sel}"]`), sel).toBeNull()
    const box = q('[data-test="field-f-a"]')!
    box.dispatchEvent(new KeyboardEvent('keydown', { key: 'Delete', bubbles: true }))
    box.dispatchEvent(new KeyboardEvent('keydown', { key: 'ArrowRight', bubbles: true }))
    pointer(box, 'pointerdown', 10, 10)
    await flushPromises()
    expect(useBuilder().fields[0]).toMatchObject({ x: 0.1 })
    expect((q('#field-prop-name') as HTMLInputElement).disabled).toBe(true)
    await router.push('/signing/templates')
    expect(router.currentRoute.value.name).toBe('signing-templates')
    w.unmount()
  })
})
