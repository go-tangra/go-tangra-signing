import { vi } from 'vitest'
import { flushPromises } from '@vue/test-utils'
import { abilitiesPlugin } from '@casl/vue'
import { createMongoAbility } from '@casl/ability'
import type { Certificate, Field, Folder, InboxItem, Session, SignerView, Submission, Template } from '@/api/types'

export type Call = { url: string; method: string; body: unknown; headers: Record<string, string>; init: RequestInit }
/** `raw` answers with bytes (binary routes such as the template PDF) instead of JSON. */
export type Reply = { status?: number; body?: unknown; raw?: ArrayBuffer; type?: string }

/** Stubs fetch; the handler sees the path below /api/signing/v1/ (query included; other URLs whole). Multipart bodies arrive as the FormData itself. */
export function fetchMock(handler: (path: string, method: string, body: unknown, init: RequestInit) => Reply | Promise<Reply>) {
  const calls: Call[] = []
  vi.stubGlobal('fetch', vi.fn(async (url: string, init: RequestInit = {}) => {
    const method = init.method ?? 'GET'
    const body = init.body instanceof FormData ? init.body : init.body ? JSON.parse(String(init.body)) : undefined
    calls.push({ url, method, body, headers: (init.headers ?? {}) as Record<string, string>, init })
    const res = await handler(url.replace(/^\/api\/signing\/v1\//, ''), method, body, init)
    const status = res.status ?? 200
    if (res.raw) return new Response(res.raw, { status, headers: { 'Content-Type': res.type ?? 'application/octet-stream' } })
    return new Response(status === 204 ? null : JSON.stringify(res.body ?? {}), { status, headers: { 'Content-Type': 'application/json' } })
  }))
  return calls
}

export const withAbility = (rules: { action: string; subject: string }[]) => ({ plugins: [[abilitiesPlugin, createMongoAbility(rules), { useGlobalProperties: true }]] as never })

// CASL rule sets of the module roles (pkg/signingmanifest Abilities).
export const SIGNER = [
  { action: 'sign', subject: 'SigningDocument' },
  { action: 'sign', subject: 'SigningCertificate' },
]
export const READER = [
  ...SIGNER,
  { action: 'read', subject: 'SigningTemplate' },
  { action: 'read', subject: 'SigningSubmission' },
  { action: 'read', subject: 'SigningVerification' },
]
export const SENDER = [...READER, { action: 'create', subject: 'SigningSubmission' }]
export const OPERATOR = [
  ...SENDER,
  { action: 'create', subject: 'SigningTemplate' },
  { action: 'update', subject: 'SigningTemplate' },
  { action: 'delete', subject: 'SigningTemplate' },
  { action: 'manage', subject: 'SigningSubmission' },
]
export const ADMIN = [...OPERATOR, { action: 'manage', subject: 'SigningCertificate' }, { action: 'manage', subject: 'SigningBackup' }]

export class FakeSource {
  static instances: FakeSource[] = []
  url: string
  closed = false
  onopen: (() => void) | null = null
  onerror: (() => void) | null = null
  listeners = new Map<string, (e: MessageEvent) => void>()
  constructor(url: string) {
    this.url = url
    FakeSource.instances.push(this)
  }
  addEventListener(t: string, fn: (e: MessageEvent) => void) { this.listeners.set(t, fn) }
  close() { this.closed = true }
  emit(t: string, data: unknown) { this.listeners.get(t)?.({ data: typeof data === 'string' ? data : JSON.stringify(data) } as MessageEvent) }
}

// --- fixtures ---
export function folder(over: Partial<Folder> = {}): Folder {
  return { id: 'fo1', parent_id: null, name: 'HR', path: '/HR', sort_order: 0, ...over }
}

export function field(over: Partial<Field> = {}): Field {
  return { id: 'f-a', name: 'Salary', type: 'text', party: 'p1', page: 1, x: 0.1, y: 0.2, w: 0.25, h: 0.03, ...over }
}

export function template(over: Partial<Template> = {}): Template {
  return {
    id: 't1', folder_id: null, name: 'Employment contract', description: '', tags: ['hr'], status: 'draft', file_name: 'contract.pdf', pdf_size: 1024, pdf_pages: 2,
    parties: [{ key: 'p1', name: 'Employee' }, { key: 'p2', name: 'Employer' }], fields: [field()], version: 3, updated_at: '2026-09-28T10:00:00Z', ...over,
  }
}

export function signer(over: Partial<SignerView> = {}): SignerView {
  return { id: 'sg1', user_id: 'u1', name: 'Alice Employee', party: 'p1', position: 0, status: 'invited', invited_at: '2026-09-28T10:00:00Z', reminders_sent: 0, ...over }
}

export function submission(over: Partial<Submission> = {}): Submission {
  return {
    id: 'sub1', template_id: 't1', name: 'Contract Alice', mode: 'sequential', status: 'in_progress', expires_at: null, reminder: null, current_version: 1, final_version: null,
    audit_trail: false, sent_at: '2026-09-28T10:00:00Z', created_at: '2026-09-28T09:00:00Z', created_by: 'sender', can_control: true,
    signers: [signer(), signer({ id: 'sg2', user_id: 'u2', name: 'Bob Employer', party: 'p2', position: 1, status: 'pending', invited_at: null })],
    ...over,
  }
}

export function inboxItem(over: Partial<InboxItem> = {}): InboxItem {
  return { signer_id: 'sg1', submission_id: 'sub1', submission_name: 'Contract Alice', party: 'p1', status: 'invited', submission_status: 'in_progress', sender: 'Sam Sender', expires_at: null, signed_at: null, created_at: '2026-09-28T10:00:00Z', ...over }
}

export function session(over: Partial<Session> = {}): Session {
  const own = [
    field({ id: 'f-name', name: 'Full name', type: 'text', party: 'p1', page: 1, required: true }),
    field({ id: 'f-amount', name: 'Amount', type: 'number', party: 'p1', page: 1, y: 0.3 }),
    field({ id: 'f-photo', name: 'Photo', type: 'image', party: 'p1', page: 2, y: 0.1 }),
    field({ id: 'f-sig', name: 'Signature', type: 'signature', party: 'p1', page: 2, y: 0.5, required: true }),
  ]
  return {
    signer_id: 's1', submission_id: 'sub1', submission_name: 'Contract Alice', party: 'p1', status: 'invited', fields: own,
    all_fields: [...own, field({ id: 'f-other', name: 'Employer note', type: 'text', party: 'p2', page: 1, y: 0.6 })],
    values: { 'f-other': 'set by someone' }, document_version: 1, certificate: { state: 'active', locked_until: null }, can_sign: true, ...over,
  }
}

export function certificate(over: Partial<Certificate> = {}): Certificate {
  return {
    id: 'c1', kind: 'signer', subject_cn: 'Alice Employee', email: 'alice@example.com', serial: '0A1B2C', fingerprint_sha256: 'AB:CD:EF', issuer_cn: 'Tenant Signing CA',
    not_before: '2026-09-01T00:00:00Z', not_after: '2028-09-01T00:00:00Z', status: 'active', locked_until: null, ...over,
  }
}

/** Sets an input / select value the way a user would (input + change events). */
export async function setField(el: Element | null, value: string): Promise<void> {
  if (!el) throw new Error('missing element')
  const input = el as HTMLInputElement | HTMLSelectElement | HTMLTextAreaElement
  input.value = value
  input.dispatchEvent(new Event('input'))
  input.dispatchEvent(new Event('change'))
  await flushPromises()
}

/** Picks an option of a kit combobox by its visible title. */
export async function pickCombo(inputId: string, title: string): Promise<void> {
  const input = document.getElementById(inputId) as HTMLInputElement | null
  if (!input) throw new Error('missing combobox ' + inputId)
  input.dispatchEvent(new Event('focus'))
  await flushPromises()
  const btn = [...document.querySelectorAll<HTMLButtonElement>(`#${inputId}-listbox button`)].find((b) => b.textContent?.trim() === title)
  if (!btn) throw new Error(`no option ${title} in ${inputId}`)
  btn.dispatchEvent(new MouseEvent('mousedown', { bubbles: true, cancelable: true }))
  await flushPromises()
}
