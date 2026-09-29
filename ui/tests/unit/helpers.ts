import { vi } from 'vitest'
import { abilitiesPlugin } from '@casl/vue'
import { createMongoAbility } from '@casl/ability'
import type { Field, Folder, Template } from '@/api/types'

export type Call = { url: string; method: string; body: unknown; headers: Record<string, string> }
/** `raw` answers with bytes (binary routes such as the template PDF) instead of JSON. */
export type Reply = { status?: number; body?: unknown; raw?: ArrayBuffer; type?: string }

/** Stubs fetch; the handler sees the path below /api/signing/v1/ (query included). Multipart bodies arrive as the FormData itself. */
export function fetchMock(handler: (path: string, method: string, body: unknown) => Reply) {
  const calls: Call[] = []
  vi.stubGlobal('fetch', vi.fn(async (url: string, init: RequestInit = {}) => {
    const method = init.method ?? 'GET'
    const body = init.body instanceof FormData ? init.body : init.body ? JSON.parse(String(init.body)) : undefined
    calls.push({ url, method, body, headers: (init.headers ?? {}) as Record<string, string> })
    const res = handler(url.replace(/^\/api\/signing\/v1\//, ''), method, body)
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
