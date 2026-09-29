import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { createPinia, setActivePinia } from 'pinia'
import { flushPromises, mount } from '@vue/test-utils'
import { createMemoryHistory, createRouter, RouterView } from 'vue-router'
import { h } from 'vue'
import { useConfirm } from '@go-tangra/ui'
import CertificatePage from '@/views/certificate/index.vue'
import type { Certificate } from '@/api/types'
import { SIGNER, certificate, fetchMock, setField, withAbility, type Reply } from './helpers'

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

const signed = [{ submission_id: 'sub1', submission_name: 'Contract Alice', signed_at: '2026-09-20T10:00:00Z' }]
type Handler = (path: string, method: string, body: unknown) => Reply | undefined
function api(state: { cert: Certificate | null }, extra: Handler = () => undefined) {
  return (path: string, method: string, body: unknown): Reply => {
    const r = extra(path, method, body)
    if (r) return r
    if (path === 'me/certificate' && method === 'GET') return { body: { certificate: state.cert, signed: state.cert ? signed : [] } }
    if (path === 'me/certificate' && method === 'POST') {
      state.cert = certificate()
      return { status: 201, body: { certificate: state.cert, signed: [] } }
    }
    if (path === 'me/certificate/pin') return { status: 204 }
    if (path === 'me/certificate/renew') {
      state.cert = certificate({ id: 'c2', serial: 'FFEE' })
      return { status: 201, body: { certificate: state.cert, signed: [] } }
    }
    if (path === 'me/certificate/revoke') {
      state.cert = certificate({ status: 'revoked', revoked_at: '2026-09-29T08:00:00Z' })
      return { status: 204 }
    }
    return { status: 404, body: { reason: 'not_found' } }
  }
}

async function open(path = '/signing/certificate') {
  const router = createRouter({
    history: createMemoryHistory(),
    routes: [
      { path: '/signing/certificate', name: 'signing-certificate', component: CertificatePage },
      { path: '/signing/sign/:signerId', name: 'signing-sign', component: { render: () => h('p', { 'data-test': 'sign-page' }) } },
    ],
  })
  await router.push(path)
  const w = mount({ render: () => h(RouterView) }, { global: { plugins: [router, withAbility(SIGNER).plugins[0]] as never }, attachTo: document.body })
  await flushPromises()
  return { w, router }
}

describe('my certificate', () => {
  it('without a certificate: set up with the PIN twice (mismatch caught locally), server refusals shown', async () => {
    let refuse = true
    const state = { cert: null as Certificate | null }
    const calls = fetchMock(api(state, (path, method) => {
      if (path === 'me/certificate' && method === 'POST' && refuse) {
        refuse = false
        return { status: 400, body: { reason: 'validation_failed', field: 'pin' } }
      }
      return undefined
    }))
    await open()
    expect(q('[data-test="cert-setup"]')).not.toBeNull()
    expect(q('[data-test="cert-details"]')).toBeNull()
    await setField(q('#cert-setup-pin'), '123456')
    await setField(q('#cert-setup-pin2'), '654321')
    await click('[data-test="cert-setup-submit"]')
    expect(document.body.textContent).toContain('The PINs do not match.')
    expect(calls.some((c) => c.method === 'POST')).toBe(false)

    await setField(q('#cert-setup-pin2'), '123456')
    await click('[data-test="cert-setup-submit"]')
    expect(q('[data-test="cert-setup-error"]')!.textContent).toContain('(pin)')

    await click('[data-test="cert-setup-submit"]')
    const posts = calls.filter((c) => c.method === 'POST')
    expect(posts.map((c) => [c.url, c.body, c.headers['X-CSRF-Token']])).toEqual([
      ['/api/signing/v1/me/certificate', { pin: '123456' }, 'tok'],
      ['/api/signing/v1/me/certificate', { pin: '123456' }, 'tok'],
    ])
    expect(q('[data-test="cert-setup"]')).toBeNull()
    expect(q('[data-test="cert-details"]')!.textContent).toContain('Alice Employee')
  })

  it('shows the details and the documents signed with it', async () => {
    fetchMock(api({ cert: certificate() }))
    await open()
    const d = q('[data-test="cert-details"]')!.textContent!
    for (const s of ['Alice Employee', '0A1B2C', 'Tenant Signing CA', 'AB:CD:EF', new Date('2028-09-01T00:00:00Z').toLocaleString()]) expect(d).toContain(s)
    expect(q('[data-test="cert-status"]')!.textContent).toBe('Active')
    expect(q('[data-test="cert-signed"]')!.textContent).toContain('Contract Alice')
  })

  it('changes the PIN and shows a wrong current PIN with the attempts left', async () => {
    let n = 0
    const calls = fetchMock(api({ cert: certificate() }, (path) => {
      if (path !== 'me/certificate/pin') return undefined
      n += 1
      return n === 1 ? { status: 403, body: { reason: 'pin_invalid', detail: { attempts_left: 1 } } } : undefined
    }))
    await open()
    await setField(q('#cert-old-pin'), '000000')
    await setField(q('#cert-new-pin'), '222222')
    await setField(q('#cert-new-pin2'), '222222')
    await click('[data-test="cert-change-submit"]')
    expect(q('[data-test="cert-change-error"]')!.textContent).toContain('Wrong PIN. 1 attempt left')
    await setField(q('#cert-old-pin'), '123456')
    await click('[data-test="cert-change-submit"]')
    expect(calls.filter((c) => c.url.endsWith('/me/certificate/pin')).map((c) => c.body)).toEqual([
      { old_pin: '000000', new_pin: '222222' }, { old_pin: '123456', new_pin: '222222' },
    ])
    expect(q('[data-test="cert-change-error"]')).toBeNull()
    expect((q('#cert-old-pin') as HTMLInputElement).value).toBe('')
  })

  it('renews through the PIN dialog (locked shown there) and revokes after the kit confirmation', async () => {
    let lock = true
    const calls = fetchMock(api({ cert: certificate() }, (path) => {
      if (path === 'me/certificate/renew' && lock) {
        lock = false
        return { status: 423, body: { reason: 'certificate_locked', detail: { locked_until: '2026-09-29T12:00:00Z' } } }
      }
      return undefined
    }))
    await open()
    await click('[data-test="cert-renew"]')
    await setField(q('#pin-input'), '123456')
    await click('[data-test="pin-submit"]')
    expect(q('[data-test="pin-locked"]')).not.toBeNull()
    await click('[data-test="pin-cancel"]')
    await click('[data-test="cert-renew"]')
    expect(q('[data-test="pin-locked"]')).toBeNull()
    await setField(q('#pin-input'), '123456')
    await click('[data-test="pin-submit"]')
    expect(calls.filter((c) => c.url.endsWith('/renew')).map((c) => c.body)).toEqual([{ pin: '123456' }, { pin: '123456' }])
    expect(q('[data-test="pin-dialog"]')).toBeNull()
    expect(q('[data-test="cert-details"]')!.textContent).toContain('FFEE')

    const confirm = useConfirm()
    await click('[data-test="cert-revoke"]')
    expect(confirm.state.pending?.title).toBe('Revoke your signing certificate?')
    confirm.answer(true)
    await flushPromises()
    expect(calls.some((c) => c.method === 'POST' && c.url.endsWith('/me/certificate/revoke'))).toBe(true)
    expect(q('[data-test="cert-status"]')!.textContent).toBe('Revoked')
    // A revoked certificate is replaced by setting up a new one.
    expect(q('[data-test="cert-setup"]')).not.toBeNull()
  })

  it('leads back to the document it was opened from once the certificate is ready (module paths only)', async () => {
    fetchMock(api({ cert: certificate() }))
    const { router } = await open('/signing/certificate?return=/signing/sign/s1')
    await click('[data-test="cert-return-button"]')
    expect(router.currentRoute.value.fullPath).toBe('/signing/sign/s1')
  })

  it('ignores a foreign return target', async () => {
    fetchMock(api({ cert: certificate() }))
    await open('/signing/certificate?return=https://evil.example/x')
    expect(q('[data-test="cert-return"]')).toBeNull()
  })
})
