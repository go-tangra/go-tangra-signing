// Server-side paging and sorting of the four signing tables (go-tangra
// specs/032-server-side-tables): sortable headers send sort/order, page /
// size / sort live in the URL under a per-table key, filters return to page 1,
// the server's clamped page is adopted, and the inbox has a pager.
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { createPinia, setActivePinia } from 'pinia'
import { flushPromises, mount } from '@vue/test-utils'
import { createMemoryHistory, createRouter, RouterView, type Router } from 'vue-router'
import { h, type Component } from 'vue'
import Templates from '@/views/templates/index.vue'
import Submissions from '@/views/submissions/index.vue'
import Admin from '@/views/admin/index.vue'
import Inbox from '@/views/inbox/index.vue'
import { ADMIN, FakeSource, OPERATOR, SIGNER, certificate, fetchMock, inboxItem, submission, template, withAbility, type Reply } from './helpers'

beforeEach(() => {
  setActivePinia(createPinia())
  vi.stubGlobal('EventSource', FakeSource)
  FakeSource.instances = []
  document.cookie = '__Host-csrf=tok; Secure; Path=/'
})
afterEach(() => {
  vi.unstubAllGlobals()
  document.body.innerHTML = ''
})

const q = (sel: string) => document.body.querySelector<HTMLElement>(sel)
const header = (label: string) =>
  Array.from(document.body.querySelectorAll<HTMLElement>('th')).find((th) => th.textContent?.trim().startsWith(label))
const sortButton = (label: string) => header(label)?.querySelector<HTMLButtonElement>('button') ?? null
const params = (url: string) => Object.fromEntries(new URL(url, 'https://x').searchParams)

/** Answers every list with `total` records (the page is what the request asked for, clamped like the server). */
function lists(total: number, perPath: Record<string, unknown[]> = {}) {
  return (path: string): Reply => {
    const [p, qs = ''] = path.split('?')
    const sp = new URLSearchParams(qs)
    const size = Number(sp.get('page_size') ?? 25)
    const last = Math.max(1, Math.ceil(total / size))
    const page = Math.min(Number(sp.get('page') ?? 1), last)
    const items = perPath[p!] ?? []
    if (p === 'folders') return { body: { items: [] } }
    if (perPath[p!]) return { body: { items, total, page, page_size: size, sort: sp.get('sort') ?? '', order: sp.get('order') ?? '' } }
    return { body: { items: [], total: 0 } }
  }
}

async function mountAt(component: Component, path: string, rules: { action: string; subject: string }[]): Promise<Router> {
  const router = createRouter({
    history: createMemoryHistory(),
    routes: [
      { path: '/t', component },
      { path: '/:rest(.*)*', component: { render: () => h('p') } },
    ],
  })
  await router.push(path)
  mount({ render: () => h(RouterView) }, { global: { plugins: [router, withAbility(rules).plugins[0]] as never }, attachTo: document.body })
  await flushPromises()
  return router
}

describe('templates table', () => {
  it('sorts by the sortable headers only, first click in the field direction, then reversed', async () => {
    const calls = fetchMock(lists(3, { templates: [template()] }))
    const router = await mountAt(Templates, '/t', OPERATOR)
    expect(params(calls.find((c) => c.url.includes('/templates?'))!.url)).toMatchObject({ page: '1', page_size: '25', sort: 'updated_at', order: 'desc' })
    expect(sortButton('Tags')).toBeNull()
    expect(sortButton('Pages')).toBeNull()
    sortButton('Name')!.click()
    await flushPromises()
    expect(params(calls.at(-1)!.url)).toMatchObject({ sort: 'name', order: 'asc', page: '1' })
    expect(router.currentRoute.value.query).toMatchObject({ 'templates.sort': 'name', 'templates.order': 'asc' })
    sortButton('Name')!.click()
    await flushPromises()
    expect(params(calls.at(-1)!.url)).toMatchObject({ sort: 'name', order: 'desc' })
    sortButton('Updated')!.click()
    await flushPromises()
    expect(params(calls.at(-1)!.url)).toMatchObject({ sort: 'updated_at', order: 'desc' })
    expect(header('Status')!.querySelector('button')).not.toBeNull()
  })

  it('restores page, size and sort from the URL and adopts the clamped page', async () => {
    const calls = fetchMock(lists(30, { templates: [template()] }))
    const router = await mountAt(Templates, '/t?templates.page=9&templates.size=10&templates.sort=status&templates.order=asc', OPERATOR)
    const first = calls.find((c) => c.url.includes('/templates?'))!
    expect(params(first.url)).toMatchObject({ page: '9', page_size: '10', sort: 'status', order: 'asc' })
    expect(router.currentRoute.value.query['templates.page']).toBe('3')
    expect(header('Status')!.getAttribute('aria-sort')).toBe('ascending')
  })

  it('ignores invalid URL values', async () => {
    const calls = fetchMock(lists(3, { templates: [template()] }))
    await mountAt(Templates, '/t?templates.sort=pdf_key&templates.order=up&templates.page=-4', OPERATOR)
    expect(params(calls.find((c) => c.url.includes('/templates?'))!.url)).toMatchObject({ page: '1', sort: 'updated_at', order: 'desc' })
  })
})

describe('submissions table', () => {
  it('sorts by title (the name column) and completion; a filter returns to page 1', async () => {
    const calls = fetchMock(lists(80, { submissions: [submission()], templates: [] }))
    const router = await mountAt(Submissions, '/t?submissions.page=2', OPERATOR)
    expect(params(calls.find((c) => c.url.includes('/submissions?'))!.url)).toMatchObject({ page: '2', sort: 'created_at', order: 'desc' })
    sortButton('Name')!.click()
    await flushPromises()
    expect(params(calls.at(-1)!.url)).toMatchObject({ sort: 'title', order: 'asc', page: '1' })
    sortButton('Completed')!.click()
    await flushPromises()
    expect(params(calls.at(-1)!.url)).toMatchObject({ sort: 'completed_at', order: 'desc' })
    expect(sortButton('Signatures')).toBeNull()
    // Go to page 2 with the pager, then filter: back to page 1.
    Array.from(document.querySelectorAll<HTMLButtonElement>('button')).find((b) => b.getAttribute('aria-label') === 'Next page')!.click()
    await flushPromises()
    expect(params(calls.at(-1)!.url).page).toBe('2')
    const status = q('#submission-filter-status') as HTMLSelectElement
    status.value = 'completed'
    status.dispatchEvent(new Event('change'))
    await flushPromises()
    expect(params(calls.at(-1)!.url)).toMatchObject({ status: 'completed', page: '1', sort: 'completed_at' })
    expect(router.currentRoute.value.query['submissions.page'] ?? '1').toBe('1')
  })
})

describe('certificates table', () => {
  it('sorts by subject, kind, status, validity and issue date', async () => {
    const calls = fetchMock(lists(2, { certificates: [certificate()], submissions: [] }))
    await mountAt(Admin, '/t', ADMIN)
    expect(params(calls.find((c) => /\/certificates\?(?!kind=admin&status=active)/.test(c.url))!.url)).toMatchObject({ sort: 'created_at', order: 'desc' })
    for (const [label, sort, order] of [['Subject', 'subject', 'asc'], ['Kind', 'kind', 'asc'], ['Status', 'status', 'asc'], ['Valid until', 'not_after', 'desc'], ['Issued', 'created_at', 'desc']] as const) {
      sortButton(label)!.click()
      await flushPromises()
      const list = calls.filter((c) => c.url.includes('/certificates?') && !c.url.includes('kind=admin&status=active')).at(-1)!
      expect(params(list.url), label).toMatchObject({ sort, page: '1' })
      if (label !== 'Issued') expect(params(list.url).order, label).toBe(order)
    }
    expect(sortButton('Serial')).toBeNull()
  })
})

describe('inbox table', () => {
  it('gains a pager; paging and sorting are sent, switching tabs returns to page 1', async () => {
    const items = Array.from({ length: 50 }, (_, i) => inboxItem({ signer_id: 'sg' + i, submission_name: 'Doc ' + i }))
    const calls = fetchMock(lists(120, { inbox: items }))
    const router = await mountAt(Inbox, '/t', SIGNER)
    expect(params(calls[0]!.url)).toMatchObject({ state: 'to_sign', page: '1', page_size: '50', sort: 'created_at', order: 'desc' })
    expect(document.body.textContent).toContain('Showing 1–50 of 120')
    Array.from(document.querySelectorAll<HTMLButtonElement>('button')).find((b) => b.getAttribute('aria-label') === 'Next page')!.click()
    await flushPromises()
    expect(params(calls.at(-1)!.url)).toMatchObject({ state: 'to_sign', page: '2' })
    expect(router.currentRoute.value.query['inbox.page']).toBe('2')
    sortButton('Document')!.click()
    await flushPromises()
    expect(params(calls.at(-1)!.url)).toMatchObject({ sort: 'title', order: 'asc', page: '1' })
    sortButton('Your status')!.click()
    await flushPromises()
    expect(params(calls.at(-1)!.url)).toMatchObject({ sort: 'status' })
    Array.from(document.querySelectorAll<HTMLButtonElement>('button')).find((b) => b.getAttribute('aria-label') === 'Next page')!.click()
    await flushPromises()
    Array.from(document.querySelectorAll<HTMLElement>('[role="tab"]')).find((t) => t.textContent?.includes('Signed by me'))!.click()
    await flushPromises()
    expect(params(calls.at(-1)!.url)).toMatchObject({ state: 'signed', page: '1' })
    // The signed tab shows the document's status: not sortable.
    expect(sortButton('Document status')).toBeNull()
  })
})
