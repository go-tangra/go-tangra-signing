import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { createPinia, setActivePinia } from 'pinia'
import { flushPromises, mount } from '@vue/test-utils'
import Verify from '@/views/verify/index.vue'
import type { VerifyResult, VerifySignature } from '@/api/types'
import { READER, SIGNER, fetchMock, setField, submission, withAbility, type Reply } from './helpers'

beforeEach(() => {
  setActivePinia(createPinia())
  document.cookie = '__Host-csrf=tok; Secure; Path=/'
})
afterEach(() => {
  vi.unstubAllGlobals()
  document.body.innerHTML = ''
})

const q = (sel: string) => document.body.querySelector<HTMLElement>(sel)
const text = (sel: string) => q(sel)?.textContent?.trim() ?? ''
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
const pdf = () => new File(['%PDF-1.7\n'], 'signed.pdf', { type: 'application/pdf' })

const sig = (over: Partial<VerifySignature> = {}): VerifySignature => ({
  signer: 'Alice Employee', time: '2026-09-28T10:00:00Z', reason: 'Signed by Alice Employee', location: 'Sofia', integrity: 'valid', trust: 'trusted', revocation: 'good', method: 'tenant', issuer: 'Tenant Signing CA', serial: '0A1B', ...over,
})

function api(result: VerifyResult | Reply) {
  return (path: string): Reply => {
    if (path.startsWith('submissions?')) return { body: { items: [submission({ current_version: 2 })], total: 1 } }
    if (path === 'verify') return 'signatures' in result ? { body: result } : result
    return { status: 404, body: { reason: 'not_found' } }
  }
}
const mountVerify = async (rules = READER) => {
  const w = mount(Verify, { global: withAbility(rules), attachTo: document.body })
  await flushPromises()
  return w
}

describe('verify page', () => {
  it('needs signing:read', async () => {
    fetchMock(api({ signatures: [] }))
    const w = await mountVerify(SIGNER)
    expect(q('[data-test="verify-forbidden"]')).not.toBeNull()
    expect(q('[data-test="verify-submit"]')).toBeNull()
    w.unmount()
  })

  it('uploads a PDF (multipart file) and shows every signature with an overall valid verdict', async () => {
    const calls = fetchMock(api({ signatures: [sig(), sig({ signer: 'Bob Employer', method: 'qualified', issuer: 'B-Trust Qualified CA', serial: 'FF01' })], modified_after_last_signature: false }))
    const w = await mountVerify()
    await click('[data-test="verify-submit"]')
    expect(document.body.textContent).toContain('Choose a PDF or a submission.')
    expect(calls.some((c) => c.url.endsWith('/verify'))).toBe(false)

    await setFile('#verify-file', pdf())
    await click('[data-test="verify-submit"]')
    const c = calls.find((x) => x.url === '/api/signing/v1/verify')!
    expect([c.method, c.headers['X-CSRF-Token']]).toEqual(['POST', 'tok'])
    const form = c.body as FormData
    expect([...form.keys()]).toEqual(['file'])
    expect((form.get('file') as File).name).toBe('signed.pdf')

    expect(text('[data-test="verify-verdict"]')).toContain('All 2 signatures are valid')
    expect(q('[data-test="verify-modified-after"]')).toBeNull()
    expect(text('[data-test="verify-row-1"]')).toContain('Alice Employee')
    expect(text('[data-test="verify-row-1"]')).toContain('Tenant certificate')
    expect(text('[data-test="verify-row-2"]')).toContain('Qualified card')
    expect(text('[data-test="verify-row-2"]')).toContain('B-Trust Qualified CA')
    expect([text('[data-test="verify-integrity-1"]'), text('[data-test="verify-trust-1"]'), text('[data-test="verify-revocation-1"]')]).toEqual(['Intact', 'Trusted', 'Not revoked'])
    w.unmount()
  })

  it('verifies a submission version', async () => {
    const calls = fetchMock(api({ signatures: [sig()] }))
    const w = await mountVerify()
    await click('#tab-submission')
    await setField(q('#verify-submission'), 'sub1')
    expect((q('#verify-version') as HTMLSelectElement).value).toBe('2')
    await setField(q('#verify-version'), '1')
    await click('[data-test="verify-submit"]')
    const form = calls.find((x) => x.url.endsWith('/verify'))!.body as FormData
    expect(Object.fromEntries(form.entries())).toEqual({ submission_id: 'sub1', version: '1' })
    expect(text('[data-test="verify-verdict"]')).toContain('The signature is valid')
    w.unmount()
  })

  it('renders every status and explains a document modified after its last signature', async () => {
    fetchMock(api({
      signatures: [sig({ trust: 'untrusted', revocation: 'unknown', method: '' }), sig({ signer: '', trust: 'unknown', revocation: 'unknown' })],
      modified_after_last_signature: true,
    }))
    const w = await mountVerify()
    await setFile('#verify-file', pdf())
    await click('[data-test="verify-submit"]')
    expect(text('[data-test="verify-verdict"]')).toContain('the document changed afterwards')
    expect(text('[data-test="verify-modified-after"]')).toContain('Content was added to the PDF after its last signature')
    expect([text('[data-test="verify-trust-1"]'), text('[data-test="verify-revocation-1"]')]).toEqual(['Not trusted', 'Not checked'])
    expect(text('[data-test="verify-trust-2"]')).toBe('Unknown')
    expect(text('[data-test="verify-row-1"]')).toContain('Other')
    expect(text('[data-test="verify-row-2"]')).toContain('Unknown signer')
    w.unmount()
  })

  it('a modified or revoked signature makes the verdict "not valid"', async () => {
    let result: VerifyResult = { signatures: [sig(), sig({ integrity: 'modified' })] }
    fetchMock((path) => api(result)(path))
    const w = await mountVerify()
    await setFile('#verify-file', pdf())
    await click('[data-test="verify-submit"]')
    expect(text('[data-test="verify-verdict"]')).toContain('Not valid: a signed part was modified')
    expect(text('[data-test="verify-integrity-2"]')).toBe('Modified')

    result = { signatures: [sig({ revocation: 'revoked' })] }
    await click('[data-test="verify-submit"]')
    expect(text('[data-test="verify-verdict"]')).toContain('Not valid: a certificate is revoked')
    expect(text('[data-test="verify-revocation-1"]')).toBe('Revoked')

    result = { signatures: [], modified_after_last_signature: false }
    await click('[data-test="verify-submit"]')
    expect(text('[data-test="verify-verdict"]')).toContain('No signatures found')
    expect(q('[data-test="verify-table"]')).toBeNull()
    w.unmount()
  })

  it('shows a refusal', async () => {
    fetchMock(api({ status: 400, body: { reason: 'invalid_pdf' } }))
    const w = await mountVerify()
    await setFile('#verify-file', pdf())
    await click('[data-test="verify-submit"]')
    expect(text('[data-test="verify-error"]')).toBe('That file is not a valid PDF (or it is encrypted).')
    expect(q('[data-test="verify-verdict"]')).toBeNull()
    w.unmount()
  })
})
