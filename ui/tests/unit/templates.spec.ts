import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { createPinia, setActivePinia } from 'pinia'
import { flushPromises, mount } from '@vue/test-utils'
import { useConfirm } from '@go-tangra/ui'
import Templates from '@/views/templates/index.vue'
import TemplateDrawer from '@/views/templates/drawer.vue'
import { useTemplates } from '@/stores/templates'
import { useFolders } from '@/stores/folders'
import type { Template } from '@/api/types'
import { OPERATOR, READER, fetchMock, folder, template, withAbility, type Reply } from './helpers'

beforeEach(() => {
  setActivePinia(createPinia())
  document.cookie = '__Host-csrf=tok; Secure; Path=/'
})
afterEach(() => {
  vi.unstubAllGlobals()
  document.body.innerHTML = ''
})

const q = (sel: string) => document.body.querySelector<HTMLElement>(sel)
async function setValue(sel: string, value: string): Promise<void> {
  const el = q(sel) as HTMLInputElement | HTMLSelectElement
  el.value = value
  el.dispatchEvent(new Event('input'))
  if (el instanceof HTMLSelectElement) el.dispatchEvent(new Event('change'))
  await flushPromises()
}
async function click(sel: string): Promise<void> {
  const el = q(sel)
  if (!el) throw new Error('missing ' + sel)
  el.click()
  await flushPromises()
}
const pdf = () => new File(['%PDF-1.7\n'], 'Employment contract.pdf', { type: 'application/pdf' })

const rows: Template[] = [
  template(),
  template({ id: 't2', name: 'NDA', status: 'active', tags: ['legal', 'nda'], folder_id: 'fo2', pdf_pages: 1 }),
  template({ id: 't3', name: 'Old offer', status: 'archived', tags: [] }),
]
const folders = [folder({ id: 'fo1', name: 'HR', path: '/HR' }), folder({ id: 'fo2', parent_id: 'fo1', name: 'Contracts', path: '/HR/Contracts' })]

function api(extra: (path: string, method: string, body: unknown) => Reply | undefined = () => undefined) {
  return (path: string, method: string, body: unknown): Reply => {
    const r = extra(path, method, body)
    if (r) return r
    if (path.startsWith('templates?')) return { body: { items: rows, total: rows.length } }
    if (path === 'folders' && method === 'GET') return { body: { items: folders } }
    if (path === 'folders' && method === 'POST') return { status: 201, body: folder({ id: 'fo3', name: (body as { name: string }).name, path: '/' + (body as { name: string }).name }) }
    if (path === 'templates' && method === 'POST') return { status: 201, body: template({ id: 't9', name: 'Employment contract' }) }
    if (/^templates\/\w+\/clone$/.test(path)) return { status: 201, body: template({ id: 't8', name: (body as { name: string }).name }) }
    if (/^templates\/\w+$/.test(path) && method === 'PATCH') return { body: { ...rows.find((t) => path.endsWith(t.id)), ...(body as object) } }
    if (/^(templates|folders)\/\w+$/.test(path) && method === 'DELETE') return { status: 204 }
    return { status: 404, body: { reason: 'not_found' } }
  }
}

describe('templates store', () => {
  it('lists with the filter and paging (blank values left out)', async () => {
    const calls = fetchMock(api())
    const s = useTemplates()
    await s.list({ folder_id: 'root', status: 'active', q: '', tag: 'hr' }, { page: 2 })
    expect(calls[0]!.url).toBe('/api/signing/v1/templates?folder_id=root&status=active&tag=hr&page=2&page_size=25')
    expect(s.items).toHaveLength(3)
    expect(s.page).toBe(2)
  })

  it('uploads multipart/form-data with the file, name, description, folder and comma-separated tags', async () => {
    const calls = fetchMock(api())
    const s = useTemplates()
    const file = pdf()
    const t = await s.create(file, { name: ' Employment contract ', description: 'Standard', folder_id: 'fo2', tags: ['hr', ' contracts ', ''] })
    expect(t.id).toBe('t9')
    const c = calls[0]!
    expect([c.method, c.url, c.headers['X-CSRF-Token']]).toEqual(['POST', '/api/signing/v1/templates', 'tok'])
    const form = c.body as FormData
    expect(form).toBeInstanceOf(FormData)
    expect((form.get('file') as File).name).toBe('Employment contract.pdf')
    expect(Object.fromEntries([...form.entries()].filter(([k]) => k !== 'file'))).toEqual({ name: 'Employment contract', description: 'Standard', folder_id: 'fo2', tags: 'hr,contracts' })
    expect(s.items[0]!.id).toBe('t9')
  })

  it('invalid or oversized PDFs are refused with a reason', async () => {
    fetchMock(api((path, method) => (path === 'templates' && method === 'POST' ? { status: 400, body: { reason: 'invalid_pdf' } } : undefined)))
    await expect(useTemplates().create(pdf(), { name: 'x' })).rejects.toMatchObject({ status: 400, reason: 'invalid_pdf' })
  })

  it('clone, patch and delete hit their routes', async () => {
    const calls = fetchMock(api())
    const s = useTemplates()
    await s.list()
    await s.clone('t1', 'Copy', null)
    await s.patch('t2', { status: 'archived' })
    await s.remove('t3')
    expect(calls.slice(1).map((c) => [c.method, c.url, c.body])).toEqual([
      ['POST', '/api/signing/v1/templates/t1/clone', { name: 'Copy', folder_id: null }],
      ['PATCH', '/api/signing/v1/templates/t2', { status: 'archived' }],
      ['DELETE', '/api/signing/v1/templates/t3', undefined],
    ])
    expect(s.items.find((t) => t.id === 't2')!.status).toBe('archived')
    expect(s.items.some((t) => t.id === 't3')).toBe(false)
  })

  it('folders: create, move to the top (parent_id null) reloads, delete', async () => {
    const calls = fetchMock(api((path, method) => (path === 'folders/fo2' && method === 'PATCH' ? { body: folder({ id: 'fo2', name: 'Contracts', path: '/Contracts' }) } : undefined)))
    const f = useFolders()
    await f.list()
    expect(f.tree.map((n) => [n.label, n.children.map((c) => c.label)])).toEqual([['HR', ['Contracts']]])
    await f.create(' Legal ', null)
    await f.move('fo2', null)
    await f.remove('fo1')
    expect(calls.map((c) => [c.method, c.url.replace('/api/signing/v1/', ''), c.body])).toEqual([
      ['GET', 'folders', undefined], ['POST', 'folders', { name: 'Legal' }], ['PATCH', 'folders/fo2', { parent_id: null }], ['GET', 'folders', undefined], ['DELETE', 'folders/fo1', undefined],
    ])
  })
})

describe('templates view', () => {
  it('shows name, folder, status, tags and pages with every manage action for an operator', async () => {
    const calls = fetchMock(api())
    const w = mount(Templates, { global: withAbility(OPERATOR), attachTo: document.body })
    await flushPromises()
    expect(calls[0]!.url).toBe('/api/signing/v1/templates?page=1&page_size=25&sort=updated_at&order=desc')
    const r2 = w.find('[data-test="template-row-t2"]').text()
    expect(r2).toContain('NDA')
    expect(r2).toContain('HR / Contracts')
    expect(r2).toContain('legal')
    expect(w.find('[data-test="template-status-t2"]').text()).toBe('Active')
    expect(w.find('[data-test="template-status-t1"]').text()).toBe('Draft')
    const has = (sel: string) => w.find(`[data-test="${sel}"]`).exists()
    expect([has('template-upload'), has('folder-new'), has('template-edit-t1'), has('template-clone-t1'), has('template-delete-t1'), has('folder-edit-fo1')]).toEqual([true, true, true, true, true, true])
    expect([has('template-activate-t1'), has('template-archive-t1'), has('template-archive-t2'), has('template-activate-t3')]).toEqual([true, false, true, true])
    w.unmount()
  })

  it('readers see the list and can open the builder, but get no manage actions', async () => {
    fetchMock(api())
    const w = mount(Templates, { global: withAbility(READER), attachTo: document.body })
    await flushPromises()
    for (const sel of ['template-upload', 'folder-new', 'folder-edit-fo1', 'folder-delete-fo1', 'folder-add-fo1', 'template-edit-t1', 'template-clone-t1', 'template-activate-t1', 'template-archive-t2', 'template-delete-t1']) {
      expect(w.find(`[data-test="${sel}"]`).exists(), sel).toBe(false)
    }
    expect(w.find('[data-test="template-open-t1"]').attributes('aria-label')).toBe('View fields')
    w.unmount()
  })

  it('filters and the folder tree reload the first page', async () => {
    const calls = fetchMock(api())
    const w = mount(Templates, { global: withAbility(READER), attachTo: document.body })
    await flushPromises()
    await setValue('#template-filter-status', 'active')
    expect(calls.at(-1)!.url).toBe('/api/signing/v1/templates?status=active&page=1&page_size=25&sort=updated_at&order=desc')
    await setValue('#template-filter-q', ' nda ')
    await setValue('#template-filter-tag', 'legal')
    q('#template-filter-tag')!.dispatchEvent(new KeyboardEvent('keyup', { key: 'Enter' }))
    await flushPromises()
    expect(calls.at(-1)!.url).toBe('/api/signing/v1/templates?q=nda&status=active&tag=legal&page=1&page_size=25&sort=updated_at&order=desc')
    const items = () => Array.from(document.querySelectorAll<HTMLElement>('[role=treeitem]'))
    expect(items().map((i) => i.textContent?.trim())).toEqual(['All templates', 'Not in a folder', 'HR', 'Contracts'])
    items()[3]!.click()
    await flushPromises()
    expect(calls.at(-1)!.url).toContain('folder_id=fo2')
    items()[1]!.click()
    await flushPromises()
    expect(calls.at(-1)!.url).toContain('folder_id=root')
    w.unmount()
  })

  it('upload drawer posts the PDF with its details, preselecting the open folder', async () => {
    const calls = fetchMock(api())
    const w = mount(Templates, { global: withAbility(OPERATOR), attachTo: document.body })
    await flushPromises()
    document.querySelectorAll<HTMLElement>('[role=treeitem]')[2]!.click()
    await flushPromises()
    await click('[data-test="template-upload"]')
    await click('[data-test="template-save"]')
    expect(q('[data-test="template-drawer"]')!.textContent).toContain('Choose a PDF file.')
    const input = q('#template-file') as HTMLInputElement
    Object.defineProperty(input, 'files', { value: [pdf()] })
    input.dispatchEvent(new Event('change'))
    await flushPromises()
    expect((q('#template-name') as HTMLInputElement).value).toBe('Employment contract')
    expect((q('#template-folder') as HTMLSelectElement).value).toBe('fo1')
    await setValue('#template-tags', 'hr, contracts')
    await click('[data-test="template-save"]')
    const post = calls.find((c) => c.method === 'POST')!
    const form = post.body as FormData
    expect(form.get('file')).toBeInstanceOf(File)
    expect([form.get('name'), form.get('folder_id'), form.get('tags')]).toEqual(['Employment contract', 'fo1', 'hr,contracts'])
    expect(q('[data-test="template-drawer"]')).toBeNull()
    w.unmount()
  })

  it('upload refusals are shown on the file', async () => {
    fetchMock(api((path, method) => (path === 'templates' && method === 'POST' ? { status: 413, body: { reason: 'payload_too_large' } } : undefined)))
    const w = mount(Templates, { global: withAbility(OPERATOR), attachTo: document.body })
    await flushPromises()
    await click('[data-test="template-upload"]')
    const input = q('#template-file') as HTMLInputElement
    Object.defineProperty(input, 'files', { value: [pdf()] })
    input.dispatchEvent(new Event('change'))
    await flushPromises()
    await click('[data-test="template-save"]')
    expect(q('[data-test="template-drawer"]')!.textContent).toContain('The file is too large.')
    w.unmount()
  })

  it('clone asks for a name; archive and delete confirm; refusals are explained', async () => {
    const calls = fetchMock(api((path, method) => (path === 'templates/t1' && method === 'DELETE' ? { status: 409, body: { reason: 'template_in_use' } } : undefined)))
    const confirm = useConfirm()
    const w = mount(Templates, { global: withAbility(OPERATOR), attachTo: document.body })
    await flushPromises()

    await click('[data-test="template-clone-t1"]')
    expect((q('#clone-name') as HTMLInputElement).value).toBe('Copy of Employment contract')
    await setValue('#clone-name', 'Contract 2027')
    await click('[data-test="clone-save"]')
    expect(calls.find((c) => c.url.endsWith('/clone'))!.body).toEqual({ name: 'Contract 2027', folder_id: null })

    await click('[data-test="template-archive-t2"]')
    confirm.answer(true)
    await flushPromises()
    expect(calls.find((c) => c.method === 'PATCH')!.body).toEqual({ status: 'archived' })

    await click('[data-test="template-delete-t1"]')
    confirm.answer(true)
    await flushPromises()
    expect(w.find('[data-test="template-error"]').text()).toBe('The template is used by unfinished submissions.')
    w.unmount()
  })

  it('folder dialog creates folders; deleting a non-empty folder is refused with a reason', async () => {
    const calls = fetchMock(api((path, method) => (path === 'folders/fo1' && method === 'DELETE' ? { status: 409, body: { reason: 'folder_not_empty' } } : undefined)))
    const confirm = useConfirm()
    const w = mount(Templates, { global: withAbility(OPERATOR), attachTo: document.body })
    await flushPromises()
    await click('[data-test="folder-add-fo1"]')
    expect((q('#folder-parent') as HTMLSelectElement).value).toBe('fo1')
    await setValue('#folder-name', 'Offers')
    await click('[data-test="folder-save"]')
    expect(calls.find((c) => c.url.endsWith('/folders') && c.method === 'POST')!.body).toEqual({ name: 'Offers', parent_id: 'fo1' })

    await click('[data-test="folder-edit-fo2"]')
    const opts = Array.from((q('#folder-parent') as HTMLSelectElement).options).map((o) => o.value)
    expect(opts).not.toContain('fo2')
    await click('[data-test="folder-dialog"] button[aria-label="Close"]')

    await click('[data-test="folder-delete-fo1"]')
    confirm.answer(true)
    await flushPromises()
    expect(w.find('[data-test="template-error"]').text()).toBe('The folder is not empty.')
    w.unmount()
  })
})

describe('template defaults (drawer)', () => {
  const mountDrawer = async (t: Template) => {
    const w = mount(TemplateDrawer, { props: { open: true, template: t, folders }, attachTo: document.body })
    await flushPromises()
    return w
  }
  const patchBody = (calls: { method: string; body: unknown }[]) => calls.filter((c) => c.method === 'PATCH').at(-1)!.body as Record<string, unknown>

  it('shows the current defaults and clears one with null', async () => {
    const calls = fetchMock(api())
    const w = await mountDrawer(template({ id: 't2', default_expiry_days: 14, default_reminder: { interval_days: 3, max: 5 } }))
    expect((q('#template-expire') as HTMLInputElement).checked).toBe(true)
    expect((q('#template-expiry-days') as HTMLInputElement).value).toBe('14')
    expect([(q('#template-interval') as HTMLInputElement).value, (q('#template-max') as HTMLInputElement).value]).toEqual(['3', '5'])

    const remind = q('#template-remind') as HTMLInputElement
    remind.click()
    await flushPromises()
    expect(q('#template-interval')).toBeNull()
    await click('[data-test="template-save"]')
    const body = patchBody(calls)
    expect([body.default_expiry_days, body.default_reminder]).toEqual([14, null])
    expect(w.emitted('saved')).toHaveLength(1)
    w.unmount()
  })

  it('sets an expiry and reminders within the limits (checked before saving)', async () => {
    const calls = fetchMock(api())
    const w = await mountDrawer(template({ id: 't2', default_expiry_days: null, default_reminder: null }))
    expect(q('#template-expiry-days')).toBeNull()
    ;(q('#template-expire') as HTMLInputElement).click()
    ;(q('#template-remind') as HTMLInputElement).click()
    await flushPromises()
    await setValue('#template-expiry-days', '400')
    await setValue('#template-interval', '0')
    await setValue('#template-max', '21')
    await click('[data-test="template-save"]')
    const txt = q('[data-test="template-defaults"]')!.textContent!
    for (const s of ['Between 1 and 365 days.', 'Between 1 and 30 days.', 'Between 1 and 20.']) expect(txt).toContain(s)
    expect(calls.some((c) => c.method === 'PATCH')).toBe(false)

    await setValue('#template-expiry-days', '60')
    await setValue('#template-interval', '7')
    await setValue('#template-max', '4')
    await click('[data-test="template-save"]')
    expect(patchBody(calls)).toEqual({ name: 'Employment contract', description: '', folder_id: null, tags: ['hr'], default_expiry_days: 60, default_reminder: { interval_days: 7, max: 4 } })
    w.unmount()
  })

  it('without defaults both are sent as null; a refusal on them lands on the input', async () => {
    const calls = fetchMock(api((path, method) => (path === 'templates/t2' && method === 'PATCH' && calls.length > 1 ? { status: 400, body: { reason: 'validation_failed', field: 'default_expiry_days' } } : undefined)))
    const w = await mountDrawer(template({ id: 't2' }))
    await click('[data-test="template-save"]')
    expect([patchBody(calls).default_expiry_days, patchBody(calls).default_reminder]).toEqual([null, null])
    ;(q('#template-expire') as HTMLInputElement).click()
    await flushPromises()
    await click('[data-test="template-save"]')
    expect(q('[data-test="template-defaults"]')!.textContent).toContain('Please check the highlighted fields.')
    w.unmount()
  })
})
