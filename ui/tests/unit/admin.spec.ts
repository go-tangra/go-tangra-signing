import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { createPinia, setActivePinia } from 'pinia'
import { flushPromises, mount } from '@vue/test-utils'
import { useConfirm } from '@go-tangra/ui'
import Admin from '@/views/admin/index.vue'
import { useCertificates } from '@/stores/admin'
import type { Certificate } from '@/api/types'
import { ADMIN, OPERATOR, certificate, fetchMock, setField, submission, withAbility, type Reply } from './helpers'

beforeEach(() => {
  setActivePinia(createPinia())
  document.cookie = '__Host-csrf=tok; Secure; Path=/'
})
afterEach(() => {
  vi.unstubAllGlobals()
  document.body.innerHTML = ''
})

const q = (sel: string) => document.body.querySelector<HTMLElement>(sel)
async function click(sel: string): Promise<void> {
  const el = q(sel)
  if (!el) throw new Error('missing ' + sel)
  el.click()
  await flushPromises()
}
async function setFile(sel: string, file: File): Promise<void> {
  const input = q(sel) as HTMLInputElement
  Object.defineProperty(input, 'files', { value: [file], configurable: true })
  input.dispatchEvent(new Event('change'))
  await flushPromises()
}
const pdf = () => new File(['%PDF-1.7\n'], 'contract.pdf', { type: 'application/pdf' })
const PEM = '-----BEGIN CERTIFICATE-----\nMIIB\n-----END CERTIFICATE-----\n'

const rows: Certificate[] = [
  certificate({ id: 'ca1', kind: 'ca', subject_cn: 'Tenant Signing CA', email: '', serial: '01' }),
  certificate({ id: 'c1' }),
  certificate({ id: 'a1', kind: 'admin', subject_cn: 'Legal Admin', email: 'legal@example.com', serial: 'AD01' }),
]
const admins = [rows[2]!, certificate({ id: 'a2', kind: 'admin', subject_cn: 'Old Admin', status: 'revoked' })]

type Handler = (path: string, method: string, body: unknown) => Reply | undefined
function api(extra: Handler = () => undefined) {
  return (path: string, method: string, body: unknown): Reply => {
    const r = extra(path, method, body)
    if (r) return r
    if (path.startsWith('certificates?kind=admin&status=active')) return { body: { items: admins, total: admins.length } }
    if (path.startsWith('certificates?')) return { body: { items: rows, total: rows.length } }
    if (/^certificates\/\w+$/.test(path) && method === 'GET') return { body: { ...rows.find((c) => path.endsWith('/' + c.id)), pem: PEM } }
    if (/^certificates\/\w+\/revoke$/.test(path)) return { body: { ...rows.find((c) => path.includes('/' + c.id + '/')), status: 'revoked', revoked_at: '2026-09-29T08:00:00Z', revocation_reason: (body as { reason: string }).reason } }
    if (path === 'certificates' && method === 'POST') return { status: 201, body: certificate({ id: 'a9', kind: 'admin', subject_cn: (body as { subject_cn: string }).subject_cn }) }
    if (path.startsWith('submissions?')) return { body: { items: [submission({ current_version: 3 }), submission({ id: 'sub2', name: 'Draft only', current_version: 0 })], total: 2 } }
    if (path === 'documents/sign') return { status: 201, body: { id: 'd1', expires_at: '2026-09-29T09:00:00Z' } }
    return { status: 404, body: { reason: 'not_found' } }
  }
}
const mountAdmin = async (rules = ADMIN) => {
  const w = mount(Admin, { global: withAbility(rules), attachTo: document.body })
  await flushPromises()
  return w
}

describe('certificates store', () => {
  it('lists with the filter (blank values left out) and signs multipart', async () => {
    const calls = fetchMock(api())
    const s = useCertificates()
    await s.list({ q: ' legal ', kind: 'admin', status: undefined }, { page: 2 })
    expect(calls[0]!.url).toBe('/api/signing/v1/certificates?q=legal&kind=admin&page=2&page_size=25')
    expect(s.items).toHaveLength(3)
    expect(s.crlUrl()).toBe('/api/signing/v1/ca/crl')
    expect(s.documentUrl('d1')).toBe('/api/signing/v1/documents/d1')
  })
})

describe('certificates page', () => {
  it('without certificates:manage it explains and loads nothing', async () => {
    const calls = fetchMock(api())
    const w = await mountAdmin(OPERATOR)
    expect(q('[data-test="cert-forbidden"]')).not.toBeNull()
    expect(q('[data-test="certs-table"]')).toBeNull()
    expect(calls).toHaveLength(0)
    w.unmount()
  })

  it('lists certificates, filters by kind, status and search, and links the CRL', async () => {
    const calls = fetchMock(api())
    const w = await mountAdmin()
    expect(calls.map((c) => c.url)).toContain('/api/signing/v1/certificates?page=1&page_size=25&sort=created_at&order=desc')
    expect(q('[data-test="cert-row-a1"]')!.textContent).toContain('Legal Admin')
    expect(q('[data-test="cert-row-a1"]')!.textContent).toContain('Administrator')
    expect(q('[data-test="cert-status-c1"]')!.textContent).toBe('Active')
    await setField(q('#cert-filter-kind'), 'signer')
    expect(calls.at(-1)!.url).toBe('/api/signing/v1/certificates?kind=signer&page=1&page_size=25&sort=created_at&order=desc')
    await setField(q('#cert-filter-status'), 'revoked')
    await setField(q('#cert-filter-q'), ' alice ')
    q('#cert-filter-q')!.dispatchEvent(new KeyboardEvent('keyup', { key: 'Enter' }))
    await flushPromises()
    expect(calls.at(-1)!.url).toBe('/api/signing/v1/certificates?q=alice&kind=signer&status=revoked&page=1&page_size=25&sort=created_at&order=desc')
    expect(q('[data-test="cert-crl"]')!.getAttribute('href')).toBe('/api/signing/v1/ca/crl')
    w.unmount()
  })

  it('details show the PEM; revoke takes a reason and the kit confirmation', async () => {
    const calls = fetchMock(api())
    const confirm = useConfirm()
    const w = await mountAdmin()
    await click('[data-test="cert-open-c1"]')
    expect(calls.at(-1)!.url).toBe('/api/signing/v1/certificates/c1')
    const d = q('[data-test="cert-drawer-details"]')!.textContent!
    for (const s of ['Alice Employee', 'alice@example.com', '0A1B2C', 'AB:CD:EF', 'Tenant Signing CA', new Date('2028-09-01T00:00:00Z').toLocaleString()]) expect(d).toContain(s)
    expect(q('[data-test="cert-pem"]')!.textContent).toBe(PEM)
    expect(q('[data-test="cert-pem-copy"]')).not.toBeNull()

    await setField(q('#cert-revoke-reason'), 'key_compromise')
    await click('[data-test="cert-revoke"]')
    expect(confirm.state.pending?.title).toBe('Revoke this certificate?')
    expect(confirm.state.pending?.text).toContain('Key compromise')
    expect(confirm.state.pending?.danger).toBe(true)
    confirm.answer(false)
    await flushPromises()
    expect(calls.some((c) => c.url.endsWith('/revoke'))).toBe(false)

    await click('[data-test="cert-revoke"]')
    confirm.answer(true)
    await flushPromises()
    const post = calls.find((c) => c.url.endsWith('/certificates/c1/revoke'))!
    expect([post.method, post.body, post.headers['X-CSRF-Token']]).toEqual(['POST', { reason: 'key_compromise' }, 'tok'])
    expect(q('[data-test="cert-drawer-status"]')!.textContent).toBe('Revoked')
    expect(q('[data-test="cert-drawer-details"]')!.textContent).toContain('Key compromise')
    expect(q('[data-test="cert-revoke-section"]')).toBeNull()
    w.unmount()
  })

  it('the CA cannot be revoked here', async () => {
    fetchMock(api())
    const w = await mountAdmin()
    await click('[data-test="cert-open-ca1"]')
    expect(q('[data-test="cert-revoke-section"]')).toBeNull()
    expect(q('[data-test="cert-ca-note"]')).not.toBeNull()
    w.unmount()
  })

  it('creates an administrator certificate (checked locally first)', async () => {
    const calls = fetchMock(api())
    const w = await mountAdmin()
    await click('[data-test="cert-new"]')
    await setField(q('#cert-create-email'), 'not-an-email')
    await click('[data-test="cert-create-submit"]')
    const dlg = q('[data-test="cert-create-dialog"]')!.textContent!
    expect(dlg).toContain('Enter a name.')
    expect(dlg).toContain('Enter a valid e-mail address.')
    expect(calls.some((c) => c.method === 'POST')).toBe(false)

    await setField(q('#cert-create-cn'), ' Legal Admin 2 ')
    await setField(q('#cert-create-email'), 'legal2@example.com')
    await setField(q('#cert-create-years'), '3')
    await click('[data-test="cert-create-submit"]')
    expect(calls.find((c) => c.method === 'POST' && c.url.endsWith('/certificates'))!.body).toEqual({ subject_cn: 'Legal Admin 2', email: 'legal2@example.com', validity_years: 3 })
    expect(q('[data-test="cert-create-dialog"]')).toBeNull()
    w.unmount()
  })
})

describe('sign a document', () => {
  it('offers only active administrator certificates and posts an uploaded PDF with the options', async () => {
    const calls = fetchMock(api())
    const w = await mountAdmin()
    const opts = Array.from((q('#doc-sign-cert') as HTMLSelectElement).options).map((o) => o.value).filter(Boolean)
    expect(opts).toEqual(['a1'])

    await click('[data-test="doc-sign-submit"]')
    expect(q('[data-test="doc-sign"]')!.textContent).toContain('Choose a PDF or a submission.')
    expect(calls.some((c) => c.url.endsWith('/documents/sign'))).toBe(false)

    await setFile('#doc-sign-file', pdf())
    await setField(q('#doc-sign-cert'), 'a1')
    await setField(q('#doc-sign-reason'), ' Approved ')
    await setField(q('#doc-sign-location'), 'Sofia')
    await setField(q('#doc-sign-tsa-url'), 'https://tsa.example.com/tsr')
    await setField(q('#doc-sign-tsa-secret'), 'sec-123')
    await click('[data-test="doc-sign-submit"]')
    const c = calls.find((x) => x.url === '/api/signing/v1/documents/sign')!
    expect([c.method, c.headers['X-CSRF-Token']]).toEqual(['POST', 'tok'])
    const form = c.body as FormData
    expect((form.get('file') as File).name).toBe('contract.pdf')
    expect(Object.fromEntries([...form.entries()].filter(([k]) => k !== 'file'))).toEqual({
      certificate_id: 'a1', reason: 'Approved', location: 'Sofia', tsa_url: 'https://tsa.example.com/tsr', tsa_secret_ref: 'sec-123',
    })
    expect(q('[data-test="doc-sign-download"]')!.getAttribute('href')).toBe('/api/signing/v1/documents/d1')
    expect(q('[data-test="doc-sign-done"]')!.textContent).toContain(new Date('2026-09-29T09:00:00Z').toLocaleString())
    w.unmount()
  })

  it('posts a submission version instead of a file', async () => {
    const calls = fetchMock(api())
    const w = await mountAdmin()
    await click('#tab-submission')
    expect(calls.at(-1)!.url).toBe('/api/signing/v1/submissions?page=1&page_size=100')
    const subs = Array.from((q('#doc-sign-submission') as HTMLSelectElement).options).map((o) => o.value).filter(Boolean)
    expect(subs).toEqual(['sub1'])
    await setField(q('#doc-sign-submission'), 'sub1')
    expect((q('#doc-sign-version') as HTMLSelectElement).value).toBe('3')
    await setField(q('#doc-sign-version'), '2')
    await setField(q('#doc-sign-cert'), 'a1')
    await click('[data-test="doc-sign-submit"]')
    const form = calls.find((x) => x.url.endsWith('/documents/sign'))!.body as FormData
    expect(Object.fromEntries(form.entries())).toEqual({ submission_id: 'sub1', version: '2', certificate_id: 'a1' })
    w.unmount()
  })

  it('shows refusals where they belong', async () => {
    let reply: Reply = { status: 502, body: { reason: 'tsa_failed' } }
    fetchMock(api((path) => (path === 'documents/sign' ? reply : undefined)))
    const w = await mountAdmin()
    await setFile('#doc-sign-file', pdf())
    await setField(q('#doc-sign-cert'), 'a1')
    await click('[data-test="doc-sign-submit"]')
    expect(q('[data-test="doc-sign-error"]')!.textContent).toBe('The timestamp authority did not answer.')

    reply = { status: 403, body: { reason: 'forbidden', field: 'tsa_secret_ref' } }
    await click('[data-test="doc-sign-submit"]')
    expect(q('[data-test="doc-sign"]')!.textContent).toContain('You may not use that Warden secret')
    expect(q('[data-test="doc-sign-error"]')).toBeNull()

    reply = { status: 400, body: { reason: 'validation_failed', field: 'tsa_url' } }
    await click('[data-test="doc-sign-submit"]')
    expect(q('[data-test="doc-sign"]')!.textContent).toContain('The timestamp authority URL is not accepted.')

    reply = { status: 409, body: { reason: 'certificate_unusable' } }
    await click('[data-test="doc-sign-submit"]')
    expect(q('[data-test="doc-sign"]')!.textContent).toContain('That administrator certificate is expired or revoked.')

    reply = { status: 400, body: { reason: 'invalid_pdf', field: 'file' } }
    await click('[data-test="doc-sign-submit"]')
    expect(q('[data-test="doc-sign-source"]')!.textContent).toContain('That file is not a valid PDF')
    w.unmount()
  })
})
