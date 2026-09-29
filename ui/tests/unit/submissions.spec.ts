import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { createPinia, setActivePinia } from 'pinia'
import { flushPromises, mount } from '@vue/test-utils'
import { createMemoryHistory, createRouter, RouterView } from 'vue-router'
import { h } from 'vue'
import { useConfirm } from '@go-tangra/ui'
import Submissions from '@/views/submissions/index.vue'
import Detail from '@/views/submissions/detail.vue'
import { useSubmissions } from '@/stores/submissions'
import type { Submission } from '@/api/types'
import { FakeSource, OPERATOR, READER, SENDER, field, fetchMock, pickCombo, setField, signer, submission, template, withAbility, type Reply } from './helpers'

beforeEach(() => {
  setActivePinia(createPinia())
  document.cookie = '__Host-csrf=tok; Secure; Path=/'
  vi.stubGlobal('EventSource', FakeSource)
  FakeSource.instances = []
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

const members = [{ user_id: 'u1', display_name: 'Alice Employee' }, { user_id: 'u2', display_name: 'Bob Employer' }, { user_id: 'u3', display_name: 'Carol Manager' }]
const tpl = template({
  id: 't1', status: 'active',
  fields: [
    field({ id: 'f-a', name: 'Salary', type: 'text', party: 'p1' }),
    field({ id: 'f-n', name: 'Hours', type: 'number', party: 'p2', y: 0.4 }),
    field({ id: 'f-s', name: 'Signature', type: 'signature', party: 'p2', y: 0.8 }),
  ],
})

type Handler = (path: string, method: string, body: unknown) => Reply | undefined
function api(extra: Handler = () => undefined, detail: () => Submission = () => submission()) {
  return (path: string, method: string, body: unknown): Reply => {
    const r = extra(path, method, body)
    if (r) return r
    if (path.startsWith('submissions?')) return { body: { items: [submission()], total: 1 } }
    if (path.startsWith('templates?')) return { body: { items: [tpl], total: 1 } }
    if (path === 'templates/t1') return { body: tpl }
    if (path.startsWith('users')) return { body: { items: members } }
    if (path === 'submissions' && method === 'POST') return { status: 201, body: submission({ id: 'sub9', status: 'draft', name: (body as { name: string }).name }) }
    if (path === 'submissions/sub9/send' && method === 'POST') return { body: submission({ id: 'sub9' }) }
    if (path === 'submissions/sub1' && method === 'GET') return { body: detail() }
    if (path === 'submissions/sub1/events') return { body: { items: [{ id: 'e1', type: 'submission.sent', at: '2026-09-28T10:00:00Z' }, { id: 'e2', type: 'signer.opened', signer_id: 'sg1', at: '2026-09-28T11:00:00Z' }] } }
    if (path === 'submissions/sub1' && method === 'DELETE') return { status: 204 }
    if (/^submissions\/sub1\/(cancel|signers\/\w+(\/resend)?)$/.test(path)) return { body: detail() }
    return { status: 404, body: { reason: 'not_found' } }
  }
}

async function openList(rules = SENDER) {
  const router = createRouter({
    history: createMemoryHistory(),
    routes: [
      { path: '/signing/submissions', name: 'signing-submissions', component: Submissions },
      { path: '/signing/submissions/:id', name: 'signing-submission', component: { render: () => h('p', { 'data-test': 'detail-page' }) } },
    ],
  })
  await router.push('/signing/submissions')
  const w = mount({ render: () => h(RouterView) }, { global: { plugins: [router, withAbility(rules).plugins[0]] as never }, attachTo: document.body })
  await flushPromises()
  return { w, router }
}

async function openDetail(rules = SENDER) {
  const router = createRouter({
    history: createMemoryHistory(),
    routes: [
      { path: '/signing/submissions', name: 'signing-submissions', component: { render: () => h('p', { 'data-test': 'list-page' }) } },
      { path: '/signing/submissions/:id', name: 'signing-submission', component: Detail },
    ],
  })
  await router.push('/signing/submissions/sub1')
  const w = mount({ render: () => h(RouterView) }, { global: { plugins: [router, withAbility(rules).plugins[0]] as never }, attachTo: document.body })
  await flushPromises()
  return { w, router }
}

describe('submissions store', () => {
  it('lists with the filter (blank values and mine=false left out) and hits the action routes', async () => {
    const calls = fetchMock(api())
    const s = useSubmissions()
    await s.list({ q: 'contract', status: 'in_progress', template_id: '', mine: false }, 2)
    expect(calls[0]!.url).toBe('/api/signing/v1/submissions?q=contract&status=in_progress&page=2&page_size=25')
    await s.list({ mine: true })
    expect(calls[1]!.url).toBe('/api/signing/v1/submissions?mine=true&page=1&page_size=25')
    await s.cancel('sub1', ' No longer needed ')
    await s.resend('sub1', 'sg1')
    await s.replaceSigner('sub1', 'sg2', 'u3')
    await s.remove('sub1')
    expect(calls.slice(2).map((c) => [c.method, c.url.replace('/api/signing/v1/', ''), c.body, c.headers['X-CSRF-Token']])).toEqual([
      ['POST', 'submissions/sub1/cancel', { reason: 'No longer needed' }, 'tok'],
      ['POST', 'submissions/sub1/signers/sg1/resend', undefined, 'tok'],
      ['PUT', 'submissions/sub1/signers/sg2', { user_id: 'u3' }, 'tok'],
      ['DELETE', 'submissions/sub1', undefined, 'tok'],
    ])
    expect(s.documentUrl('sub1')).toBe('/api/signing/v1/submissions/sub1/document')
    expect(s.documentUrl('sub1', 3)).toBe('/api/signing/v1/submissions/sub1/document?version=3')
  })
})

describe('submissions list and create drawer', () => {
  it('lists submissions with status and progress; readers without create get no New button', async () => {
    fetchMock(api())
    await openList(READER)
    expect(q('[data-test="submission-row-sub1"]')!.textContent).toContain('Contract Alice')
    expect(q('[data-test="submission-row-sub1"]')!.textContent).toContain('0 of 2 signed')
    expect(q('[data-test="submission-status-sub1"]')!.textContent).toBe('In progress')
    expect(q('[data-test="submission-new"]')).toBeNull()
    expect(q('#submission-filter-mine')).not.toBeNull()
  })

  it('creates with one signer per party in the chosen order, prefill, expiry and reminders, then sends', async () => {
    const calls = fetchMock(api())
    await openList(SENDER)
    await click('[data-test="submission-new"]')
    expect(calls.some((c) => c.url.includes('/templates?status=active'))).toBe(true)
    await setField(q('#submission-template'), 't1')
    expect((q('#submission-name') as HTMLInputElement).value).toBe('Employment contract')
    // One picker per party.
    expect(q('[data-test="signer-row-p1"]')).not.toBeNull()
    expect(q('[data-test="signer-row-p2"]')).not.toBeNull()

    await pickCombo('signer-p1', 'Alice Employee')
    await pickCombo('signer-p2', 'Bob Employer')
    // A person already picked is not offered for the other party.
    q('#signer-p2')!.dispatchEvent(new Event('focus'))
    await flushPromises()
    expect([...document.querySelectorAll('#signer-p2-listbox button')].map((b) => b.textContent?.trim())).not.toContain('Alice Employee')

    // Employer signs first.
    await click('[data-test="signer-down-p1"]')
    expect([...document.querySelectorAll('[data-test^="signer-row-"]')].map((e) => e.getAttribute('data-test'))).toEqual(['signer-row-p2', 'signer-row-p1'])

    // Prefill offers the text-valued fields only.
    expect(q('#prefill-f-a')).not.toBeNull()
    expect(q('#prefill-f-n')).not.toBeNull()
    expect(q('#prefill-f-s')).toBeNull()
    await setField(q('#prefill-f-a'), '5000 EUR')
    await setField(q('#prefill-f-n'), '37,5')
    await setField(q('#submission-expires'), '2099-01-31T17:00')
    q('#submission-remind')!.click()
    await flushPromises()

    await click('[data-test="submission-save"]')
    const post = calls.find((c) => c.url === '/api/signing/v1/submissions' && c.method === 'POST')!
    expect(post.body).toEqual({
      template_id: 't1',
      name: 'Employment contract',
      mode: 'sequential',
      signers: [{ user_id: 'u2', party: 'p2', position: 0 }, { user_id: 'u1', party: 'p1', position: 1 }],
      prefill: { 'f-a': '5000 EUR', 'f-n': '37,5' },
      expires_at: new Date('2099-01-31T17:00').toISOString(),
      reminder: { interval_days: 3, max: 3 },
    })

    expect(q('[data-test="submission-created"]')).not.toBeNull()
    await click('[data-test="submission-send-now"]')
    expect(calls.some((c) => c.url === '/api/signing/v1/submissions/sub9/send' && c.method === 'POST')).toBe(true)
    expect(q('[data-test="submission-drawer"]')).toBeNull()
  })

  it('parallel mode sends no positions; a missing signer or a bad prefill is caught before the request', async () => {
    const calls = fetchMock(api())
    await openList(SENDER)
    await click('[data-test="submission-new"]')
    await setField(q('#submission-template'), 't1')
    await setField(q('#submission-mode'), 'parallel')
    expect(q('[data-test="signer-down-p1"]')).toBeNull()
    await pickCombo('signer-p1', 'Alice Employee')
    await setField(q('#prefill-f-n'), 'many')
    await click('[data-test="submission-save"]')
    expect(document.body.textContent).toContain('Choose who signs for this party.')
    expect(document.body.textContent).toContain('Enter a number.')
    expect(calls.some((c) => c.url === '/api/signing/v1/submissions' && c.method === 'POST')).toBe(false)

    await pickCombo('signer-p2', 'Carol Manager')
    await setField(q('#prefill-f-n'), '')
    await click('[data-test="submission-save"]')
    const post = calls.find((c) => c.url === '/api/signing/v1/submissions' && c.method === 'POST')!
    expect((post.body as { signers: unknown }).signers).toEqual([{ user_id: 'u1', party: 'p1' }, { user_id: 'u3', party: 'p2' }])
    expect(post.body).not.toHaveProperty('prefill')
  })

  it('shows the server refusal (invalid_signer) in the drawer', async () => {
    fetchMock(api((path, method) => (path === 'submissions' && method === 'POST' ? { status: 400, body: { reason: 'invalid_signer', field: 'signers' } } : undefined)))
    await openList(SENDER)
    await click('[data-test="submission-new"]')
    await setField(q('#submission-template'), 't1')
    await pickCombo('signer-p1', 'Alice Employee')
    await pickCombo('signer-p2', 'Bob Employer')
    await click('[data-test="submission-save"]')
    expect(q('[data-test="submission-form-error"]')!.textContent).toContain('A signer is not an active member of this tenant. (signers)')
  })
})

describe('submission detail', () => {
  const full = () => submission({
    signers: [
      signer({ status: 'opened', opened_at: '2026-09-28T11:00:00Z', mail_error: 'mailbox full' }),
      signer({ id: 'sg2', user_id: 'u2', name: 'Bob Employer', party: 'p2', position: 1, status: 'pending', invited_at: null }),
    ],
  })

  it('shows signer states, the mail_error warning, the history and the downloads available', async () => {
    fetchMock(api(undefined, () => ({ ...full(), status: 'completed', final_version: 3, audit_trail: true })))
    await openDetail()
    expect(q('[data-test="signer-status-sg1"]')!.textContent).toBe('Opened')
    expect(q('[data-test="signer-status-sg2"]')!.textContent).toBe('Waiting')
    expect(q('[data-test="signer-mail-error-sg1"]')!.textContent).toContain('mailbox full')
    expect(q('[data-test="signer-mail-error-sg2"]')).toBeNull()
    const events = q('[data-test="submission-events"]')!.textContent!
    expect(events).toContain('Opened the document · Alice Employee')
    expect(events).toContain('Sent')
    expect(q('[data-test="download-current"]')!.getAttribute('href')).toBe('/api/signing/v1/submissions/sub1/document')
    expect(q('[data-test="download-final"]')!.getAttribute('href')).toBe('/api/signing/v1/submissions/sub1/document?version=3')
    expect(q('[data-test="download-audit"]')!.getAttribute('href')).toBe('/api/signing/v1/submissions/sub1/audit-trail')
  })

  it('no final document or audit trail link before they exist', async () => {
    fetchMock(api(undefined, full))
    await openDetail()
    expect(q('[data-test="download-final"]')).toBeNull()
    expect(q('[data-test="download-audit"]')).toBeNull()
  })

  it('without can_control there are no sender actions', async () => {
    fetchMock(api(undefined, () => ({ ...full(), can_control: false })))
    await openDetail(OPERATOR)
    for (const sel of ['submission-cancel', 'submission-delete', 'submission-send', 'signer-resend-sg1', 'signer-replace-sg1', 'signer-replace-sg2']) {
      expect(q(`[data-test="${sel}"]`), sel).toBeNull()
    }
  })

  it('with can_control: cancel asks a reason, resend and replace hit their routes', async () => {
    const calls = fetchMock(api(undefined, full))
    await openDetail()
    expect(q('[data-test="signer-resend-sg1"]')).not.toBeNull()
    // A waiting signer has no invitation to resend yet, but can be replaced.
    expect(q('[data-test="signer-resend-sg2"]')).toBeNull()
    expect(q('[data-test="signer-replace-sg2"]')).not.toBeNull()

    await click('[data-test="submission-cancel"]')
    await click('[data-test="cancel-confirm"]')
    expect(q('[data-test="cancel-error"]')!.textContent).toContain('Enter a reason')
    await setField(q('#cancel-reason'), 'Terms changed')
    await click('[data-test="cancel-confirm"]')
    expect(calls.find((c) => c.url.endsWith('/sub1/cancel'))!.body).toEqual({ reason: 'Terms changed' })

    await click('[data-test="signer-resend-sg1"]')
    expect(calls.some((c) => c.method === 'POST' && c.url === '/api/signing/v1/submissions/sub1/signers/sg1/resend')).toBe(true)

    await click('[data-test="signer-replace-sg2"]')
    await pickCombo('replace-user', 'Carol Manager')
    await click('[data-test="replace-confirm"]')
    const put = calls.find((c) => c.method === 'PUT')!
    expect([put.url, put.body]).toEqual(['/api/signing/v1/submissions/sub1/signers/sg2', { user_id: 'u3' }])
  })

  it('delete asks for confirmation through the kit dialog, then returns to the list', async () => {
    const calls = fetchMock(api(undefined, full))
    const { router } = await openDetail()
    const confirm = useConfirm()
    await click('[data-test="submission-delete"]')
    expect(confirm.state.pending?.title).toBe('Delete Contract Alice?')
    confirm.answer(false)
    await flushPromises()
    expect(calls.some((c) => c.method === 'DELETE')).toBe(false)
    await click('[data-test="submission-delete"]')
    confirm.answer(true)
    await flushPromises()
    expect(calls.some((c) => c.method === 'DELETE' && c.url.endsWith('/submissions/sub1'))).toBe(true)
    expect(router.currentRoute.value.name).toBe('signing-submissions')
  })

  it('a draft offers Send', async () => {
    const calls = fetchMock(api((path, method) => (path === 'submissions/sub1/send' && method === 'POST' ? { body: submission() } : undefined), () => submission({ status: 'draft', sent_at: null })))
    await openDetail()
    expect(q('[data-test="submission-cancel"]')).toBeNull()
    await click('[data-test="submission-send"]')
    expect(calls.some((c) => c.method === 'POST' && c.url.endsWith('/sub1/send'))).toBe(true)
  })

  it('reloads when a live event for this submission arrives', async () => {
    const calls = fetchMock(api(undefined, full))
    await openDetail()
    const before = calls.filter((c) => c.url.endsWith('/submissions/sub1')).length
    FakeSource.instances[0]!.emit('signing.submission.completed', { submission_id: 'sub1' })
    await new Promise((r) => setTimeout(r, 450))
    await flushPromises()
    expect(calls.filter((c) => c.url.endsWith('/submissions/sub1')).length).toBe(before + 1)
  })
})
