import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, mount } from '@vue/test-utils'
import SignaturePad from '@/components/SignaturePad.vue'

// jsdom has no canvas: a recording 2D context and a PNG-producing toBlob stand in.
function fakeCtx() {
  return {
    clearRect: vi.fn(), beginPath: vi.fn(), moveTo: vi.fn(), lineTo: vi.fn(), stroke: vi.fn(), fillText: vi.fn(),
    measureText: vi.fn((t: string) => ({ width: t.length * 20 })),
    strokeStyle: '', fillStyle: '', lineWidth: 1, lineCap: '', lineJoin: '', font: '', textAlign: '', textBaseline: '',
  }
}
let ctx: ReturnType<typeof fakeCtx>

beforeEach(() => {
  ctx = fakeCtx()
  vi.spyOn(HTMLCanvasElement.prototype, 'getContext').mockReturnValue(ctx as never)
  vi.spyOn(HTMLCanvasElement.prototype, 'toBlob').mockImplementation(function (cb: BlobCallback, type?: string) {
    cb(new Blob([new Uint8Array([0x89, 0x50, 0x4e, 0x47, 1, 2, 3])], { type: type ?? 'image/png' }))
  })
  vi.spyOn(HTMLElement.prototype, 'getBoundingClientRect').mockReturnValue({ left: 0, top: 0, width: 300, height: 100, right: 300, bottom: 100, x: 0, y: 0, toJSON: () => ({}) } as DOMRect)
})
afterEach(() => {
  vi.restoreAllMocks()
  document.body.innerHTML = ''
})

function pointer(el: Element, type: string, x: number, y: number): void {
  el.dispatchEvent(new MouseEvent(type, { bubbles: true, cancelable: true, clientX: x, clientY: y, button: 0 }))
}
async function stroke(el: Element, from: [number, number], to: [number, number]): Promise<void> {
  pointer(el, 'pointerdown', ...from)
  pointer(el, 'pointermove', (from[0] + to[0]) / 2, (from[1] + to[1]) / 2)
  pointer(el, 'pointermove', ...to)
  pointer(el, 'pointerup', ...to)
  await flushPromises()
}
const last = (w: ReturnType<typeof mount>) => (w.emitted('change') ?? []).at(-1)?.[0] as Blob | null | undefined

describe('SignaturePad', () => {
  it('drawing emits a non-empty PNG and the strokes are scaled to the pad', async () => {
    const w = mount(SignaturePad, { attachTo: document.body })
    const canvas = w.find('[data-test="signature-canvas"]').element
    await stroke(canvas, [30, 50], [150, 60])
    const png = last(w)
    expect(png).toBeInstanceOf(Blob)
    expect(png!.type).toBe('image/png')
    expect(png!.size).toBeGreaterThan(0)
    // 300 px wide on screen → 600 px canvas: x doubles.
    expect(ctx.moveTo).toHaveBeenCalledWith(60, 100)
    expect(ctx.lineTo).toHaveBeenCalledWith(300, 120)
    expect((w.vm as unknown as { strokes: unknown[] }).strokes).toHaveLength(1)
    w.unmount()
  })

  it('undo removes the last stroke and clear empties the pad (null)', async () => {
    const w = mount(SignaturePad, { attachTo: document.body })
    const canvas = w.find('[data-test="signature-canvas"]').element
    await stroke(canvas, [10, 10], [50, 50])
    await stroke(canvas, [60, 60], [90, 90])
    expect((w.vm as unknown as { strokes: unknown[] }).strokes).toHaveLength(2)
    await w.find('[data-test="signature-undo"]').trigger('click')
    await flushPromises()
    expect((w.vm as unknown as { strokes: unknown[] }).strokes).toHaveLength(1)
    expect(last(w)).toBeInstanceOf(Blob)
    await w.find('[data-test="signature-clear"]').trigger('click')
    await flushPromises()
    expect((w.vm as unknown as { strokes: unknown[] }).strokes).toHaveLength(0)
    expect(last(w)).toBeNull()
    expect(w.find('[data-test="signature-undo"]').attributes('disabled')).toBeDefined()
    w.unmount()
  })

  it('Ctrl+Z inside the pad undoes a stroke', async () => {
    const w = mount(SignaturePad, { attachTo: document.body })
    await stroke(w.find('[data-test="signature-canvas"]').element, [10, 10], [50, 50])
    await w.find('[data-test="signature-pad"]').trigger('keydown', { key: 'z', ctrlKey: true })
    await flushPromises()
    expect(last(w)).toBeNull()
    w.unmount()
  })

  it('typed mode renders the name in a script font and emits a PNG; clearing the name empties it', async () => {
    const w = mount(SignaturePad, { props: { name: 'Alice Employee' }, attachTo: document.body })
    await w.find('[data-test="signature-mode-type"]').trigger('click')
    await flushPromises()
    expect(w.find('[data-test="signature-mode-type"]').attributes('aria-pressed')).toBe('true')
    const input = w.find('#signature-typed')
    expect((input.element as HTMLInputElement).value).toBe('Alice Employee')
    expect(ctx.fillText).toHaveBeenLastCalledWith('Alice Employee', 300, 100)
    expect(ctx.font).toMatch(/cursive/)
    expect(last(w)?.type).toBe('image/png')

    await input.setValue('A. Employee')
    await flushPromises()
    expect(ctx.fillText).toHaveBeenLastCalledWith('A. Employee', 300, 100)
    expect(w.find('canvas').attributes('aria-label')).toBe('Your typed signature: A. Employee')

    await w.find('[data-test="signature-clear"]').trigger('click')
    await flushPromises()
    expect(last(w)).toBeNull()
    w.unmount()
  })

  it('does not draw while disabled', async () => {
    const w = mount(SignaturePad, { props: { disabled: true }, attachTo: document.body })
    await stroke(w.find('[data-test="signature-canvas"]').element, [10, 10], [50, 50])
    expect((w.vm as unknown as { strokes: unknown[] }).strokes).toHaveLength(0)
    expect(w.emitted('change')).toBeUndefined()
    w.unmount()
  })
})
