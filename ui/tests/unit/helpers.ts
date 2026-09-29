import { vi } from 'vitest'
import { abilitiesPlugin } from '@casl/vue'
import { createMongoAbility } from '@casl/ability'

export type Call = { url: string; method: string; body: unknown; headers: Record<string, string> }
export type Reply = { status?: number; body?: unknown }

/** Stubs fetch; the handler sees the path below /api/signing/v1/ (query included). */
export function fetchMock(handler: (path: string, method: string, body: unknown) => Reply) {
  const calls: Call[] = []
  vi.stubGlobal('fetch', vi.fn(async (url: string, init: RequestInit = {}) => {
    const method = init.method ?? 'GET'
    const body = init.body ? JSON.parse(String(init.body)) : undefined
    calls.push({ url, method, body, headers: (init.headers ?? {}) as Record<string, string> })
    const res = handler(url.replace(/^\/api\/signing\/v1\//, ''), method, body)
    const status = res.status ?? 200
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
