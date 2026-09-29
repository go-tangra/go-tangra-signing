import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { createPinia, setActivePinia } from 'pinia'
import { flushPromises, mount } from '@vue/test-utils'
import { createMemoryHistory, createRouter, RouterView } from 'vue-router'
import { h } from 'vue'
import Sign from '@/views/sign/index.vue'
import SignaturePad from '@/components/SignaturePad.vue'
import { useSession } from '@/stores/session'
import { evaluate } from '@/rules/evaluate'
import { valueError } from '@/utils/values'
import type { Field, FieldType, Session } from '@/api/types'
import { SIGNER, fetchMock, field, session, setField, withAbility, type Reply } from './helpers'

// pdf.js is never run in jsdom: a two-page document stands in.
const pdf = vi.hoisted(() => ({ GlobalWorkerOptions: { workerSrc: '' }, getDocument: vi.fn() }))
vi.mock('pdfjs-dist', () => pdf)
const fakeDoc = {
  numPages: 2,
  getPage: async () => ({ getViewport: ({ scale }: { scale: number }) => ({ width: 600 * scale, height: 800 * scale }), render: () => ({ promise: Promise.resolve(), cancel: () => {} }) }),
  destroy: async () => {},
}
const PDF_BYTES = new TextEncoder().encode('%PDF-1.7\n').buffer as ArrayBuffer

beforeEach(() => {
  setActivePinia(createPinia())
  document.cookie = '__Host-csrf=tok; Secure; Path=/'
  pdf.getDocument.mockReset()
  pdf.getDocument.mockImplementation(() => ({ promise: Promise.resolve(fakeDoc) }))
  vi.spyOn(HTMLCanvasElement.prototype, 'getContext').mockReturnValue({ clearRect() {}, beginPath() {}, moveTo() {}, lineTo() {}, stroke() {}, fillText() {}, measureText: () => ({ width: 10 }) } as never)
})
afterEach(() => {
  vi.unstubAllGlobals()
  vi.restoreAllMocks()
  document.body.innerHTML = ''
})

const q = (sel: string) => document.body.querySelector<HTMLElement>(sel)
async function click(sel: string): Promise<void> {
  const el = q(sel)
  if (!el) throw new Error('missing ' + sel)
  el.click()
  await flushPromises()
}

type Handler = (path: string, method: string, body: unknown) => Reply | undefined
function api(s: () => Session, extra: Handler = () => undefined) {
  return (path: string, method: string, body: unknown): Reply => {
    const r = extra(path, method, body)
    if (r) return r
    if (path === 'signing/s1' && method === 'GET') return { body: s() }
    if (path === 'signing/s1/document') return { raw: PDF_BYTES, type: 'application/pdf' }
    if (path === 'signing/s1/open' && method === 'POST') return { status: 204 }
    return { status: 404, body: { reason: 'not_found' } }
  }
}

async function open() {
  const router = createRouter({
    history: createMemoryHistory(),
    routes: [
      { path: '/signing', name: 'signing-inbox', component: { render: () => h('p', 'inbox') } },
      { path: '/signing/certificate', name: 'signing-certificate', component: { render: () => h('p', { 'data-test': 'cert-page' }, 'cert') } },
      { path: '/signing/sign/:signerId', name: 'signing-sign', component: Sign },
    ],
  })
  await router.push('/signing/sign/s1')
  const w = mount({ render: () => h(RouterView) }, { global: { plugins: [router, withAbility(SIGNER).plugins[0]] as never }, attachTo: document.body })
  await flushPromises()
  return { w, router }
}

const png = () => new Blob([new Uint8Array([0x89, 0x50, 0x4e, 0x47])], { type: 'image/png' })
function setFile(el: HTMLElement | null, file: File): void {
  if (!el) throw new Error('missing file input')
  Object.defineProperty(el, 'files', { value: [file], configurable: true })
  el.dispatchEvent(new Event('change'))
}
/** Fills every required field and draws a signature. */
async function fill(w: ReturnType<typeof mount>): Promise<void> {
  await setField(q('[data-test="sign-input-f-name"]'), 'Alice Employee')
  await setField(q('[data-test="sign-input-f-amount"]'), '1250,50')
  setFile(q('[data-test="sign-input-f-photo"]'), new File([new Uint8Array([0xff, 0xd8, 0xff])], 'me.jpg', { type: 'image/jpeg' }))
  w.findComponent(SignaturePad).vm.$emit('change', png())
  await flushPromises()
}
async function enterPin(pin: string): Promise<void> {
  await setField(q('#pin-input'), pin)
  await click('[data-test="pin-submit"]')
}

describe('signing page', () => {
  it('overlays only the signer\'s own fields and marks an invited slot opened once', async () => {
    const calls = fetchMock(api(() => session()))
    await open()
    for (const id of ['f-name', 'f-amount', 'f-photo', 'f-sig']) expect(q(`[data-test="sign-input-${id}"]`)).not.toBeNull()
    expect(q('[data-test="sign-input-f-other"]')).toBeNull()
    expect(document.body.textContent).not.toContain('Employer note')
    expect(q('[data-test="sign-field-list"]')!.querySelectorAll('li')).toHaveLength(4)
    expect(calls.filter((c) => c.url.endsWith('/signing/s1/open')).map((c) => [c.method, c.headers['X-CSRF-Token']])).toEqual([['POST', 'tok']])
    // Page 2 carries the image and the signature box.
    expect(q('[data-test="sign-overlay-2"] [data-test="sign-input-f-sig"]')).not.toBeNull()
  })

  it('refuses to sign with a required field empty or without a signature (no request, errors shown)', async () => {
    const calls = fetchMock(api(() => session()))
    await open()
    await click('[data-test="sign-submit"]')
    expect(q('[data-test="sign-error"]')!.textContent).toContain('Check the highlighted fields')
    expect(q('[data-test="sign-item-status-f-name"]')!.textContent).toBe('Fill in this field.')
    expect(q('[data-test="sign-pad-error"]')!.textContent).toContain('Draw or type your signature')
    expect(q('[data-test="pin-dialog"]')).toBeNull()
    await setField(q('[data-test="sign-input-f-amount"]'), '12abc')
    await click('[data-test="sign-submit"]')
    expect(q('[data-test="sign-item-status-f-amount"]')!.textContent).toBe('Enter a number.')
    expect(calls.some((c) => c.url.endsWith('/sign'))).toBe(false)
  })

  it('posts multipart values JSON, pin, signature and upload.<field id>, then shows the signed state', async () => {
    let signed = false
    const calls = fetchMock(api(() => (signed ? session({ status: 'signed', can_sign: false, reason: 'already_signed' }) : session()), (path, method) => {
      if (path === 'signing/s1/sign' && method === 'POST') {
        signed = true
        return { body: { status: 'signed', submission_status: 'in_progress' } }
      }
      return undefined
    }))
    const { w } = await open()
    await fill(w)
    await click('[data-test="sign-submit"]')
    expect(q('[data-test="pin-dialog"]')).not.toBeNull()
    await enterPin('123456')

    const c = calls.find((x) => x.url === '/api/signing/v1/signing/s1/sign')!
    expect([c.method, c.headers['X-CSRF-Token']]).toEqual(['POST', 'tok'])
    const form = c.body as FormData
    expect(form).toBeInstanceOf(FormData)
    expect(JSON.parse(String(form.get('values')))).toEqual({ 'f-name': 'Alice Employee', 'f-amount': '1250,50' })
    expect(form.get('pin')).toBe('123456')
    const sig = form.get('signature') as File
    expect([sig.type, sig.name, sig.size > 0]).toEqual(['image/png', 'signature.png', true])
    expect((form.get('upload.f-photo') as File).name).toBe('me.jpg')
    expect([...form.keys()].sort()).toEqual(['pin', 'signature', 'upload.f-photo', 'values'])

    expect(q('[data-test="pin-dialog"]')).toBeNull()
    expect(q('[data-test="sign-done"]')).not.toBeNull()
    expect(q('[data-test="sign-submit"]')).toBeNull()
  })

  it('a wrong PIN shows the attempts left and keeps the dialog; a locked certificate shows until when', async () => {
    let n = 0
    fetchMock(api(() => session(), (path, method) => {
      if (path !== 'signing/s1/sign' || method !== 'POST') return undefined
      n += 1
      return n === 1 ? { status: 403, body: { reason: 'pin_invalid', detail: { attempts_left: 2 } } } : { status: 423, body: { reason: 'certificate_locked', detail: { locked_until: '2026-09-29T12:00:00Z' } } }
    }))
    const { w } = await open()
    await fill(w)
    await click('[data-test="sign-submit"]')
    await enterPin('000000')
    expect(q('[data-test="pin-error"]')!.textContent).toContain('Wrong PIN.')
    expect(q('[data-test="pin-error"]')!.textContent).toContain('2 attempts left')
    expect((q('#pin-input') as HTMLInputElement).value).toBe('')

    await enterPin('111111')
    expect(q('[data-test="pin-locked"]')!.textContent).toContain(new Date('2026-09-29T12:00:00Z').toLocaleString())
    expect(q('#pin-input')).toBeNull()
    expect(q('[data-test="pin-submit"]')).toBeNull()
    // The page banner follows the certificate state.
    expect(q('[data-test="sign-cert-locked"]')).not.toBeNull()
  })

  it('not your turn: a banner explains it, no inputs and no Sign button', async () => {
    const calls = fetchMock(api(() => session({ status: 'pending', can_sign: false, reason: 'not_your_turn' })))
    await open()
    expect(q('[data-test="sign-blocked"]')!.textContent).toContain('It is not your turn to sign yet.')
    expect(q('[data-test="sign-input-f-name"]')).toBeNull()
    expect(q('[data-test="sign-submit"]')).toBeNull()
    expect(q('[data-test="sign-decline"]')).toBeNull()
    // Only an invited slot is marked opened.
    expect(calls.some((c) => c.url.endsWith('/open'))).toBe(false)
  })

  it('certificate_missing offers the set-up page with a way back to this document', async () => {
    fetchMock(api(() => session(), (path, method) => (path === 'signing/s1/sign' && method === 'POST' ? { status: 409, body: { reason: 'certificate_missing' } } : undefined)))
    const { w, router } = await open()
    await fill(w)
    await click('[data-test="sign-submit"]')
    await enterPin('123456')
    expect(q('[data-test="pin-dialog"]')).toBeNull()
    expect(q('[data-test="sign-cert-missing"]')).not.toBeNull()
    await click('[data-test="sign-cert-setup"]')
    expect(router.currentRoute.value.name).toBe('signing-certificate')
    expect(router.currentRoute.value.query.return).toBe('/signing/sign/s1')
  })

  it('without a certificate the banner shows before signing and Sign leads there instead of asking a PIN', async () => {
    fetchMock(api(() => session({ certificate: { state: 'none', locked_until: null } })))
    const { w } = await open()
    expect(q('[data-test="sign-cert-missing"]')).not.toBeNull()
    await fill(w)
    await click('[data-test="sign-submit"]')
    expect(q('[data-test="pin-dialog"]')).toBeNull()
  })

  it('a server refusal naming a field lands on that field', async () => {
    fetchMock(api(() => session(), (path, method) => (path === 'signing/s1/sign' && method === 'POST' ? { status: 400, body: { reason: 'missing_required', field: 'f-amount' } } : undefined)))
    const { w } = await open()
    await fill(w)
    await click('[data-test="sign-submit"]')
    await enterPin('123456')
    expect(q('[data-test="sign-error"]')!.textContent).toContain('Fill in every required field. (Amount)')
    expect(q('[data-test="sign-item-status-f-amount"]')!.textContent).toBe('Fill in every required field.')
  })

  it('decline posts the reason and reloads the session', async () => {
    let declined = false
    const calls = fetchMock(api(() => (declined ? session({ status: 'declined', can_sign: false, reason: 'submission_not_open' }) : session()), (path, method) => {
      if (path === 'signing/s1/decline' && method === 'POST') {
        declined = true
        return { body: { status: 'declined', submission_status: 'cancelled' } }
      }
      return undefined
    }))
    await open()
    await click('[data-test="sign-decline"]')
    await click('[data-test="decline-confirm"]')
    expect(q('[data-test="decline-error"]')!.textContent).toContain('why')
    await setField(q('#decline-reason'), '  Wrong salary  ')
    await click('[data-test="decline-confirm"]')
    expect(calls.find((c) => c.url.endsWith('/decline'))!.body).toEqual({ reason: 'Wrong salary' })
    expect(q('[data-test="sign-declined"]')).not.toBeNull()
  })
})

describe('signing values and rules hook', () => {
  it('mirrors the server value rules', () => {
    const f = (type: FieldType, over: Partial<Field> = {}) => field({ type, ...over })
    expect(valueError(f('number'), '1,5')).toBe('')
    expect(valueError(f('number'), 'x')).not.toBe('')
    expect(valueError(f('date'), '2026-02-30')).not.toBe('')
    expect(valueError(f('date'), '2026-02-28')).toBe('')
    expect(valueError(f('checkbox'), 'yes')).not.toBe('')
    expect(valueError(f('select', { options: ['A', 'B'] }), 'B')).toBe('')
    expect(valueError(f('radio', { options: ['A', 'B'] }), 'C')).not.toBe('')
    expect(valueError(f('text'), 'x'.repeat(2001))).not.toBe('')
    expect(valueError(f('cells'), 'x'.repeat(201))).not.toBe('')
  })

  it('evaluate() (US6 hook) keeps every field visible and required as configured', () => {
    const ev = evaluate([field({ id: 'a', required: true }), field({ id: 'b' })], {})
    expect([...ev.hidden]).toEqual([])
    expect([...ev.required]).toEqual(['a'])
    expect(ev.computed).toEqual({})
  })

  it('the session store starts own values from known values, then defaults', async () => {
    fetchMock(api(() => session({ values: { 'f-name': 'Prefilled' }, fields: [field({ id: 'f-name', party: 'p1' }), field({ id: 'f-cb', type: 'checkbox', party: 'p1' }), field({ id: 'f-d', party: 'p1', default: 'dflt' })] })))
    const s = useSession()
    await s.load('s1')
    expect(s.values).toEqual({ 'f-name': 'Prefilled', 'f-cb': 'false', 'f-d': 'dflt' })
  })
})
