import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { createPinia, setActivePinia } from 'pinia'
import { flushPromises, mount } from '@vue/test-utils'
import { useConfirm } from '@go-tangra/ui'
import Admin from '@/views/admin/index.vue'
import { useBackup } from '@/stores/backup'
import type { BackupResult } from '@/api/types'
import { ADMIN, OPERATOR, certificate, fetchMock, setField, withAbility, type Reply } from './helpers'

const GZIP = new Uint8Array([0x1f, 0x8b, 0x08, 0x00, 1, 2, 3]).buffer as ArrayBuffer
const counts = (n: number) => ({ folders: n, templates: n, submissions: n, certificates: n, objects: n })
const SUMMARY: BackupResult = {
  created: { folders: 2, templates: 3, submissions: 4, certificates: 1, objects: 9 },
  updated: counts(0),
  skipped: { ...counts(0), templates: 1 },
  needs_reissue: 1,
  errors: ['submission s9: missing document version 2'],
}
const BACKUP_ONLY = [{ action: 'manage', subject: 'SigningBackup' }]

let clicked: { href: string; download: string }[] = []
beforeEach(() => {
  setActivePinia(createPinia())
  document.cookie = '__Host-csrf=tok; Secure; Path=/'
  clicked = []
  // jsdom has no object URLs.
  Object.assign(URL, { createObjectURL: vi.fn(() => 'blob:backup'), revokeObjectURL: vi.fn() })
  vi.spyOn(HTMLAnchorElement.prototype, 'click').mockImplementation(function (this: HTMLAnchorElement) {
    clicked.push({ href: this.getAttribute('href') ?? '', download: this.download })
  })
})
afterEach(() => {
  Reflect.deleteProperty(URL, 'createObjectURL')
  Reflect.deleteProperty(URL, 'revokeObjectURL')
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
async function setFile(sel: string, file: File): Promise<void> {
  const input = q(sel) as HTMLInputElement
  Object.defineProperty(input, 'files', { value: [file], configurable: true })
  input.dispatchEvent(new Event('change'))
  await flushPromises()
}
const archive = () => new File([GZIP], 'signing-t1.tar.gz', { type: 'application/gzip' })

type Handler = (path: string, method: string, body: unknown) => Reply | undefined
function api(extra: Handler = () => undefined) {
  return (path: string, method: string, body: unknown): Reply => {
    const r = extra(path, method, body)
    if (r) return r
    if (path.startsWith('certificates?')) return { body: { items: [certificate()], total: 1 } }
    if (path === 'backup/export' && method === 'POST') return { raw: GZIP, type: 'application/gzip', headers: { 'Content-Disposition': 'attachment; filename="signing-t1-20260929-080000.tar.gz"' } }
    if (path.startsWith('backup/import?') && method === 'POST') return { body: SUMMARY }
    return { status: 404, body: { reason: 'not_found' } }
  }
}
const mountAdmin = async (rules = ADMIN) => {
  const w = mount(Admin, { global: withAbility(rules), attachTo: document.body })
  await flushPromises()
  return w
}

describe('backup store', () => {
  it('export POSTs with the CSRF header and saves the gzip under the server name', async () => {
    const calls = fetchMock(api())
    const name = await useBackup().exportBackup()
    const c = calls.find((x) => x.url === '/api/signing/v1/backup/export')!
    expect([c.method, c.headers['X-CSRF-Token'], c.init.body]).toEqual(['POST', 'tok', undefined])
    expect(name).toBe('signing-t1-20260929-080000.tar.gz')
    expect(clicked).toEqual([{ href: 'blob:backup', download: 'signing-t1-20260929-080000.tar.gz' }])
    const blob = (URL.createObjectURL as ReturnType<typeof vi.fn>).mock.calls[0]![0] as Blob
    expect(new Uint8Array(await blob.arrayBuffer())).toEqual(new Uint8Array(GZIP))
  })

  it('import sends the raw archive as application/gzip with the mode', async () => {
    const calls = fetchMock(api())
    const file = archive()
    const res = await useBackup().importBackup(file, 'overwrite')
    const c = calls.find((x) => x.url.startsWith('/api/signing/v1/backup/import'))!
    expect(c.url).toBe('/api/signing/v1/backup/import?mode=overwrite')
    expect([c.method, c.headers['X-CSRF-Token'], c.headers['Content-Type']]).toEqual(['POST', 'tok', 'application/gzip'])
    expect(c.body).toBe(file)
    expect(res).toEqual(SUMMARY)
  })
})

describe('backup panel', () => {
  it('shows only with backup:manage (also without certificates:manage)', async () => {
    fetchMock(api())
    let w = await mountAdmin(OPERATOR)
    expect(q('[data-test="backup"]')).toBeNull()
    expect(q('[data-test="cert-forbidden"]')).not.toBeNull()
    w.unmount()

    const calls = fetchMock(api())
    w = await mountAdmin(BACKUP_ONLY)
    expect(q('[data-test="backup"]')).not.toBeNull()
    expect(q('[data-test="cert-forbidden"]')).toBeNull()
    expect(q('[data-test="certs-table"]')).toBeNull()
    expect(calls.some((c) => c.url.includes('/certificates'))).toBe(false)
    w.unmount()

    fetchMock(api())
    w = await mountAdmin(ADMIN)
    expect(q('[data-test="backup"]')).not.toBeNull()
    expect(q('[data-test="certs-table"]')).not.toBeNull()
    w.unmount()
  })

  it('exports: downloads the archive; a refusal is shown', async () => {
    let reply: Reply | undefined = undefined
    fetchMock(api((path) => (path === 'backup/export' ? reply : undefined)))
    const w = await mountAdmin(BACKUP_ONLY)
    await click('[data-test="backup-export"]')
    expect(clicked.map((c) => c.download)).toEqual(['signing-t1-20260929-080000.tar.gz'])
    reply = { status: 403, body: { reason: 'forbidden' } }
    await click('[data-test="backup-export"]')
    expect(q('[data-test="backup-export-error"]')!.textContent).not.toBe('')
    expect(clicked).toHaveLength(1)
    w.unmount()
  })

  it('imports: needs a file, confirms an overwrite, shows the summary', async () => {
    const calls = fetchMock(api())
    const w = await mountAdmin(BACKUP_ONLY)
    await click('[data-test="backup-import"]')
    expect(q('[data-test="backup"]')!.textContent).toContain('Choose a backup archive (.tar.gz).')
    expect(calls.some((c) => c.url.includes('backup/import'))).toBe(false)

    await setFile('#backup-file', archive())
    await setField(q('#backup-mode'), 'overwrite')
    const confirm = useConfirm()
    await click('[data-test="backup-import"]')
    expect(confirm.state.pending?.title).toBe('Overwrite existing records?')
    confirm.answer(false)
    await flushPromises()
    expect(calls.some((c) => c.url.includes('backup/import'))).toBe(false)

    await click('[data-test="backup-import"]')
    confirm.answer(true)
    await flushPromises()
    const c = calls.find((x) => x.url.includes('backup/import'))!
    expect([c.url, c.headers['Content-Type'], (c.body as File).name]).toEqual(['/api/signing/v1/backup/import?mode=overwrite', 'application/gzip', 'signing-t1.tar.gz'])
    const cells = (k: string) => Array.from(q(`[data-test="backup-row-${k}"]`)!.querySelectorAll('td')).map((td) => td.textContent)
    expect(cells('templates')).toEqual(['3', '0', '1'])
    expect(cells('objects')).toEqual(['9', '0', '0'])
    expect(q('[data-test="backup-reissue"]')!.textContent).toContain('1 certificate could not be restored')
    expect(q('[data-test="backup-errors"]')!.textContent).toContain('submission s9: missing document version 2')
    w.unmount()
  })

  it('an import refusal is shown (invalid_backup)', async () => {
    fetchMock(api((path) => (path.startsWith('backup/import') ? { status: 400, body: { reason: 'invalid_backup' } } : undefined)))
    const w = await mountAdmin(BACKUP_ONLY)
    await setFile('#backup-file', archive())
    await click('[data-test="backup-import"]')
    expect(q('[data-test="backup-import-error"]')!.textContent).toBe('The backup file is not valid.')
    expect(q('[data-test="backup-result"]')).toBeNull()
    w.unmount()
  })
})
