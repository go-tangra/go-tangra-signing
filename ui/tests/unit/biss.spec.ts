import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { createPinia, setActivePinia } from 'pinia'
import { flushPromises, mount } from '@vue/test-utils'
import { createMemoryHistory, createRouter, RouterView } from 'vue-router'
import { h } from 'vue'
import BissButton from '@/components/BissButton.vue'
import SignaturePad from '@/components/SignaturePad.vue'
import Sign from '@/views/sign/index.vue'
import { BISS_PORTS, useBiss } from '@/composables/useBiss'
import { useSession } from '@/stores/session'
import type { QESPrepared, Session } from '@/api/types'
import { SIGNER, fetchMock, session, setField, withAbility, type Call, type Reply } from './helpers'

// pdf.js is never run in jsdom (the signing page test mounts the whole page).
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

const LEAF = 'MIIBbGVhZg=='
const CA = 'MIIBY2E='
const SIG = 'MEUCIQDsig=='
const prepared = (over: Partial<QESPrepared> = {}): QESPrepared => ({
  preparation_id: '11111111-2222-3333-4444-555555555555', digest_b64: 'ZGlnZXN0', signed_attrs_b64: 'c2lnbmVkYXR0cnM=', hash_algorithm: 'SHA256', expires_at: '2026-09-29T12:10:00Z', ...over,
})
const hang = (init: RequestInit) => new Promise<Reply>((_, reject) => init.signal?.addEventListener('abort', () => reject(new DOMException('Aborted', 'AbortError'))))
const unreachable = () => Promise.reject(new TypeError('Failed to fetch'))

type Handler = (path: string, method: string, body: unknown, init: RequestInit) => Reply | Promise<Reply> | undefined
/** BISS on port 53953 (53952 refuses the connection), the module's QES routes, the session. */
function api(opts: { prep?: QESPrepared; sess?: () => Session; extra?: Handler } = {}) {
  let signed = false
  return (path: string, method: string, body: unknown, init: RequestInit): Reply | Promise<Reply> => {
    const r = opts.extra?.(path, method, body, init)
    if (r) return r
    if (/^https:\/\/localhost:5395[245]\//.test(path)) return unreachable()
    if (path === 'https://localhost:53953/version') return { body: { version: '2.3.1' } }
    if (path === 'https://localhost:53953/getsigner') return { body: { status: 'ok', chain: [LEAF, CA] } }
    if (path === 'https://localhost:53953/sign') return { body: { status: 'ok', signatures: [SIG] } }
    if (path === 'signing/s1/qes/prepare') return { body: opts.prep ?? prepared() }
    if (path === 'signing/s1/qes/complete') {
      signed = true
      return { body: { status: 'signed', submission_status: 'in_progress' } }
    }
    if (path === 'signing/s1' && method === 'GET') return { body: signed ? session({ status: 'signed', can_sign: false, reason: 'already_signed' }) : (opts.sess?.() ?? session()) }
    if (path === 'signing/s1/document') return { raw: PDF_BYTES, type: 'application/pdf' }
    if (path === 'signing/s1/open') return { status: 204 }
    return { status: 404, body: { reason: 'not_found' } }
  }
}
const isLocal = (c: Call) => c.url.startsWith('https://localhost:')
const png = () => new Blob([new Uint8Array([0x89, 0x50, 0x4e, 0x47])], { type: 'image/png' })

describe('useBiss', () => {
  it('probes 53952–53955 without credentials and takes the first port that answers', async () => {
    const calls = fetchMock(api({ extra: (path) => (path === 'https://localhost:53954/version' ? { body: { version: '9.9' } } : undefined) }))
    const b = useBiss()
    expect(await b.detect()).toBe(53953)
    expect(b.version.value).toBe('2.3.1')
    expect(calls.map((c) => c.url).sort()).toEqual(BISS_PORTS.map((p) => `https://localhost:${p}/version`))
    for (const c of calls) expect([c.method, c.init.credentials, c.init.mode, c.init.referrerPolicy]).toEqual(['GET', 'omit', 'cors', 'no-referrer'])
  })

  it('happy path: getsigner → prepare → sign → complete with the exact bodies; the origin proof is left out when absent', async () => {
    const calls = fetchMock(api())
    const out = await useBiss().run('s1', { values: { 'f-name': 'Alice' }, signatureImage: 'data:image/png;base64,iVBO' })
    expect(out).toEqual({ kind: 'signed', result: { status: 'signed', submission_status: 'in_progress' } })

    const flow = calls.filter((c) => !c.url.endsWith('/version')).map((c) => [c.method, c.url])
    expect(flow).toEqual([
      ['POST', 'https://localhost:53953/getsigner'],
      ['POST', '/api/signing/v1/signing/s1/qes/prepare'],
      ['POST', 'https://localhost:53953/sign'],
      ['POST', '/api/signing/v1/signing/s1/qes/complete'],
    ])
    const by = (u: string) => calls.find((c) => c.url.endsWith(u))!
    expect(by('/qes/prepare').body).toEqual({ values: { 'f-name': 'Alice' }, chain: [LEAF, CA], signature_image: 'data:image/png;base64,iVBO' })
    expect(by('/qes/prepare').headers['X-CSRF-Token']).toBe('tok')
    expect(by(':53953/sign').body).toEqual({
      version: '1.0', contents: ['c2lnbmVkYXR0cnM='], contentType: 'data', hashAlgorithm: 'SHA256', signatureType: 'signature', signerCertificateB64: LEAF, confirmText: ['hash'],
    })
    expect(by('/qes/complete').body).toEqual({ preparation_id: '11111111-2222-3333-4444-555555555555', signature_b64: SIG })
    // Nothing goes to BISS with credentials or a referrer, nor carries the CSRF token.
    for (const c of calls.filter(isLocal)) {
      expect([c.init.credentials, c.init.mode, c.init.referrerPolicy]).toEqual(['omit', 'cors', 'no-referrer'])
      expect(c.headers['X-CSRF-Token']).toBeUndefined()
    }
  })

  it('passes signedContents / signedContentsCert when the module returned them', async () => {
    const calls = fetchMock(api({ prep: prepared({ signed_contents_b64: 'b3JpZ2lu', signed_contents_cert_b64: 'Y2VydA==' }) }))
    const out = await useBiss().run('s1', { values: {} })
    expect(out.kind).toBe('signed')
    const body = calls.find((c) => c.url === 'https://localhost:53953/sign')!.body as Record<string, unknown>
    expect([body.signedContents, body.signedContentsCert]).toEqual([['b3JpZ2lu'], ['Y2VydA==']])
    expect(calls.find((c) => c.url.endsWith('/qes/prepare'))!.body).toEqual({ values: {}, chain: [LEAF, CA] })
  })

  it('not installed: no port answers and nothing reaches the module', async () => {
    const calls = fetchMock(api({ extra: (path) => (path.startsWith('https://localhost:') ? unreachable() : undefined) }))
    expect(await useBiss().run('s1', { values: {} })).toEqual({ kind: 'not_installed' })
    expect(calls.every(isLocal)).toBe(true)
  })

  it('a BISS refusal carries its reason and stops before completing', async () => {
    const calls = fetchMock(api({ extra: (path) => (path === 'https://localhost:53953/sign' ? { body: { status: 'failed', reasonCode: 202, reasonText: 'User cancelled the operation' } } : undefined) }))
    expect(await useBiss().run('s1', { values: {} })).toEqual({ kind: 'refused', reasonCode: '202', reasonText: 'User cancelled the operation' })
    expect(calls.some((c) => c.url.endsWith('/qes/complete'))).toBe(false)
  })

  it('times out when BISS does not answer while choosing the certificate', async () => {
    const calls = fetchMock(api({ extra: (path, _m, _b, init) => (path === 'https://localhost:53953/getsigner' ? hang(init) : undefined) }))
    expect(await useBiss({ timeouts: { choose: 20 } }).run('s1', { values: {} })).toEqual({ kind: 'timeout' })
    expect(calls.some((c) => c.url.includes('/qes/'))).toBe(false)
  })

  it('module refusals come back with their reason and field', async () => {
    fetchMock(api({ extra: (path) => (path === 'signing/s1/qes/complete' ? { status: 409, body: { reason: 'preparation_expired' } } : undefined) }))
    expect(await useBiss().run('s1', { values: {} })).toEqual({ kind: 'server', reason: 'preparation_expired', message: 'The qualified signing step expired. Start it again.', field: undefined })
    fetchMock(api({ extra: (path) => (path === 'signing/s1/qes/prepare' ? { status: 400, body: { reason: 'missing_required', field: 'f-name' } } : undefined) }))
    expect(await useBiss().run('s1', { values: {} })).toMatchObject({ kind: 'server', reason: 'missing_required', field: 'f-name' })
  })
})

describe('BissButton', () => {
  function mountButton(check: () => boolean = () => true, timeouts?: Record<string, number>) {
    const s = useSession()
    s.reset(session({ signer_id: 's1' }))
    const w = mount(BissButton, { props: { signerId: 's1', check, ...(timeouts ? { timeouts } : {}) }, attachTo: document.body })
    return { s, w }
  }
  const text = (sel: string) => document.body.querySelector(sel)?.textContent ?? ''
  async function start(w: ReturnType<typeof mount>): Promise<void> {
    await w.find('[data-test="biss-start"]').trigger('click')
    await flushPromises()
  }

  it('checks the fields first: nothing is contacted when the check fails', async () => {
    const calls = fetchMock(api())
    const { w } = mountButton(() => false)
    await start(w)
    expect(calls).toHaveLength(0)
    w.unmount()
  })

  it('signs with the own values and the pad image, reloads the session and emits signed', async () => {
    const calls = fetchMock(api())
    const { s, w } = mountButton()
    s.setValue('f-name', 'Alice Employee')
    s.signature = png()
    await start(w)
    await vi.waitFor(() => expect(w.emitted('signed')).toBeTruthy())
    const prep = calls.find((c) => c.url.endsWith('/qes/prepare'))!.body as { values: unknown; signature_image: string }
    expect(prep.values).toEqual({ 'f-name': 'Alice Employee' })
    expect(prep.signature_image.startsWith('data:image/png;base64,')).toBe(true)
    expect(w.emitted('signed')![0]).toEqual([{ status: 'signed', submission_status: 'in_progress' }])
    expect(s.session!.status).toBe('signed')
    expect(w.emitted('busy')!.map((e) => e[0])).toEqual([true, false])
    w.unmount()
  })

  it('explains a missing BISS installation', async () => {
    fetchMock(api({ extra: (path) => (path.startsWith('https://localhost:') ? unreachable() : undefined) }))
    const { w } = mountButton()
    await start(w)
    expect(text('[data-test="biss-not-installed"]')).toContain('B-Trust BISS application installed and running')
    w.unmount()
  })

  it('shows the reason BISS gives when it refuses', async () => {
    fetchMock(api({ extra: (path) => (path === 'https://localhost:53953/getsigner' ? { body: { status: 'failed', reasonCode: 202, reasonText: 'User cancelled the operation' } } : undefined) }))
    const { w } = mountButton()
    await start(w)
    expect(text('[data-test="biss-refused"]')).toContain('User cancelled the operation')
    w.unmount()
  })

  it('reports a timeout', async () => {
    fetchMock(api({ extra: (path, _m, _b, init) => (path === 'https://localhost:53953/sign' ? hang(init) : undefined) }))
    const { w } = mountButton(() => true, { sign: 20 })
    await start(w)
    await vi.waitFor(() => expect(document.body.querySelector('[data-test="biss-timeout"]')).not.toBeNull())
    w.unmount()
  })

  it('preparation_expired offers to start again, which runs the whole flow anew', async () => {
    let n = 0
    const calls = fetchMock(api({ extra: (path) => (path === 'signing/s1/qes/complete' && ++n === 1 ? { status: 409, body: { reason: 'preparation_expired' } } : undefined) }))
    const { w } = mountButton()
    await start(w)
    expect(text('[data-test="biss-expired"]')).toContain('The qualified signing step expired.')
    await w.find('[data-test="biss-retry"]').trigger('click')
    await flushPromises()
    expect(calls.filter((c) => c.url.endsWith('/qes/prepare'))).toHaveLength(2)
    expect(w.emitted('signed')).toHaveLength(1)
    w.unmount()
  })

  it('document_changed asks the page to reload; certificate_unusable and signature mismatches are explained', async () => {
    let reason = 'document_changed'
    fetchMock(api({ extra: (path) => (path === 'signing/s1/qes/complete' ? { status: 409, body: { reason } } : undefined) }))
    const { w } = mountButton()
    await start(w)
    expect(w.emitted('changed')).toHaveLength(1)
    expect(document.body.querySelector('[data-test="biss-changed"]')).not.toBeNull()

    reason = 'certificate_unusable'
    await start(w)
    expect(text('[data-test="biss-error"]')).toContain('The certificate on the card is expired, revoked or not accepted')

    reason = 'qes_signature_invalid'
    await start(w)
    expect(text('[data-test="biss-error"]')).toBe('The qualified signature does not match the document.')
    w.unmount()
  })

  it('missing_required names the field and marks it', async () => {
    fetchMock(api({ extra: (path) => (path === 'signing/s1/qes/prepare' ? { status: 400, body: { reason: 'missing_required', field: 'f-name' } } : undefined) }))
    const { s, w } = mountButton()
    await start(w)
    expect(text('[data-test="biss-error"]')).toBe('Fill in every required field. (Full name)')
    expect(s.errors['f-name']).toBe('Fill in every required field.')
    expect(w.emitted('field')![0]).toEqual(['f-name'])
    w.unmount()
  })

  it('is unavailable when a required image or file field needs an upload', async () => {
    const calls = fetchMock(api())
    const s = useSession()
    const sess = session()
    const req = <T extends { id: string }>(f: T): T => (f.id === 'f-photo' ? { ...f, required: true } : f)
    s.reset({ ...sess, fields: sess.fields.map(req), all_fields: sess.all_fields!.map(req) })
    const w = mount(BissButton, { props: { signerId: 's1', check: () => true }, attachTo: document.body })
    expect(w.find('[data-test="biss-start"]').attributes('disabled')).toBeDefined()
    expect(text('[data-test="biss-phase"]')).toContain('Image and file fields cannot be sent')
    await start(w)
    expect(calls).toHaveLength(0)
    w.unmount()
  })
})

describe('signing page with a qualified card', () => {
  async function open() {
    const router = createRouter({
      history: createMemoryHistory(),
      routes: [
        { path: '/signing', name: 'signing-inbox', component: { render: () => h('p', 'inbox') } },
        { path: '/signing/certificate', name: 'signing-certificate', component: { render: () => h('p', 'cert') } },
        { path: '/signing/sign/:signerId', name: 'signing-sign', component: Sign },
      ],
    })
    await router.push('/signing/sign/s1')
    const w = mount({ render: () => h(RouterView) }, { global: { plugins: [router, withAbility(SIGNER).plugins[0]] as never }, attachTo: document.body })
    await flushPromises()
    return w
  }
  const q = (sel: string) => document.body.querySelector<HTMLElement>(sel)
  // Only text fields and the signature: the qualified flow carries no uploads.
  const textOnly = () => {
    const s = session({ signer_id: 's1' })
    return { ...s, fields: s.fields.filter((f) => f.type !== 'image') }
  }

  it('uses the page validation (nothing contacted with a required field empty)', async () => {
    const calls = fetchMock(api({ sess: textOnly }))
    await open()
    const before = calls.length
    q('[data-test="biss-start"]')!.click()
    await flushPromises()
    expect(calls.length).toBe(before)
    expect(q('[data-test="sign-error"]')!.textContent).toContain('Check the highlighted fields')
  })

  it('signs through BISS and shows the signed state', async () => {
    const calls = fetchMock(api({ sess: textOnly }))
    const w = await open()
    await setField(q('[data-test="sign-input-f-name"]'), 'Alice Employee')
    w.findComponent(SignaturePad).vm.$emit('change', png())
    await flushPromises()
    q('[data-test="biss-start"]')!.click()
    await flushPromises()
    await vi.waitFor(() => expect(q('[data-test="sign-done"]')).not.toBeNull())
    expect(calls.filter((c) => c.url === '/api/signing/v1/signing/s1' && c.method === 'GET')).toHaveLength(2)
    expect((calls.find((c) => c.url.endsWith('/qes/prepare'))!.body as { values: unknown }).values).toEqual({ 'f-name': 'Alice Employee' })
  })

  it('document_changed reloads the session and the document', async () => {
    const calls = fetchMock(api({ sess: textOnly, extra: (path) => (path === 'signing/s1/qes/complete' ? { status: 409, body: { reason: 'document_changed' } } : undefined) }))
    const w = await open()
    await setField(q('[data-test="sign-input-f-name"]'), 'Alice Employee')
    w.findComponent(SignaturePad).vm.$emit('change', png())
    await flushPromises()
    const docs = () => calls.filter((c) => c.url.endsWith('/signing/s1/document')).length
    const before = docs()
    q('[data-test="biss-start"]')!.click()
    await flushPromises()
    await vi.waitFor(() => expect(docs()).toBe(before + 1))
    expect(calls.filter((c) => c.url === '/api/signing/v1/signing/s1' && c.method === 'GET')).toHaveLength(2)
    expect(q('[data-test="biss-changed"]')).not.toBeNull()
  })
})
