import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { createPinia, setActivePinia } from 'pinia'
import { flushPromises, mount } from '@vue/test-utils'
import { createMemoryHistory, createRouter, RouterView } from 'vue-router'
import { h } from 'vue'
import Inbox from '@/views/inbox/index.vue'
import { useInbox } from '@/stores/inbox'
import { EVENTS, STREAM_URL, useLive } from '@/stores/live'
import { FakeSource, SIGNER, fetchMock, inboxItem, withAbility, type Reply } from './helpers'

beforeEach(() => {
  setActivePinia(createPinia())
  vi.stubGlobal('EventSource', FakeSource)
  FakeSource.instances = []
})
afterEach(() => {
  vi.unstubAllGlobals()
  document.body.innerHTML = ''
})

const q = (sel: string) => document.body.querySelector<HTMLElement>(sel)
const toSign = [inboxItem(), inboxItem({ signer_id: 'sg7', submission_id: 'sub7', submission_name: 'NDA', status: 'opened', expires_at: '2026-10-10T10:00:00Z' })]
const signed = [inboxItem({ signer_id: 'sg3', submission_id: 'sub3', submission_name: 'Old offer', status: 'signed', submission_status: 'completed', signed_at: '2026-09-20T10:00:00Z' })]

function api(counter: { to_sign: number; signed: number }) {
  return (path: string): Reply => {
    if (path.startsWith('inbox?state=to_sign')) {
      counter.to_sign += 1
      return { body: { items: toSign, total: toSign.length } }
    }
    if (path.startsWith('inbox?state=signed')) {
      counter.signed += 1
      return { body: { items: signed, total: 1 } }
    }
    return { status: 404, body: { reason: 'not_found' } }
  }
}

async function open() {
  const router = createRouter({
    history: createMemoryHistory(),
    routes: [
      { path: '/signing', name: 'signing-inbox', component: Inbox },
      { path: '/signing/sign/:signerId', name: 'signing-sign', component: { render: () => h('p', { 'data-test': 'sign-page' }) } },
    ],
  })
  await router.push('/signing')
  const w = mount({ render: () => h(RouterView) }, { global: { plugins: [router, withAbility(SIGNER).plugins[0]] as never }, attachTo: document.body })
  await flushPromises()
  return { w, router }
}

describe('inbox store', () => {
  it('asks for the state and pages', async () => {
    const calls = fetchMock(api({ to_sign: 0, signed: 0 }))
    const s = useInbox()
    await s.list('signed', { page: 2 })
    expect(calls[0]!.url).toBe('/api/signing/v1/inbox?state=signed&page=2&page_size=50')
    expect(s.items.map((i) => i.signer_id)).toEqual(['sg3'])
  })
})

describe('inbox view', () => {
  it('shows the documents to sign with their status, and opens the signing page', async () => {
    fetchMock(api({ to_sign: 0, signed: 0 }))
    const { router } = await open()
    expect(q('[data-test="inbox-row-sg1"]')!.textContent).toContain('Contract Alice')
    expect(q('[data-test="inbox-row-sg1"]')!.textContent).toContain('Sam Sender')
    expect(q('[data-test="inbox-status-sg7"]')!.textContent).toBe('Opened')
    expect(q('[data-test="inbox-tabs"]')!.textContent).toContain('2')
    q('[data-test="inbox-sign-sg7"]')!.click()
    await flushPromises()
    expect(router.currentRoute.value.fullPath).toBe('/signing/sign/sg7')
  })

  it('"Signed by me" lists the signed documents with the document status', async () => {
    const counter = { to_sign: 0, signed: 0 }
    fetchMock(api(counter))
    await open()
    const tab = [...document.querySelectorAll<HTMLButtonElement>('[role="tab"]')].find((b) => b.textContent?.includes('Signed by me'))!
    tab.click()
    await flushPromises()
    expect(counter.signed).toBe(1)
    expect(q('[data-test="inbox-row-sg3"]')!.textContent).toContain('Old offer')
    expect(q('[data-test="inbox-status-sg3"]')!.textContent).toBe('Completed')
    expect(q('[data-test="inbox-row-sg1"]')).toBeNull()
  })

  it('refreshes live on signing.inbox (coalesced) and ignores other events and garbage', async () => {
    const counter = { to_sign: 0, signed: 0 }
    fetchMock(api(counter))
    const { w } = await open()
    expect(FakeSource.instances).toHaveLength(1)
    const src = FakeSource.instances[0]!
    expect(src.url).toBe(STREAM_URL)
    expect([...src.listeners.keys()].sort()).toEqual([...EVENTS].sort())
    expect(counter.to_sign).toBe(1)

    src.emit('signing.submission.completed', { submission_id: 'sub1' })
    src.emit('signing.inbox', 'not json')
    src.emit('signing.inbox', { signer_id: 'sg9', submission_id: 'sub9', state: 'invited' })
    src.emit('signing.inbox', { signer_id: 'sg9', submission_id: 'sub9', state: 'invited' })
    await new Promise((r) => setTimeout(r, 350))
    await flushPromises()
    expect(counter.to_sign).toBe(2)

    src.onopen?.()
    expect(useLive().connected).toBe(true)
    w.unmount()
    expect(src.closed).toBe(true)
  })
})
