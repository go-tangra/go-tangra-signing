import { defineStore } from 'pinia'
import { ref } from 'vue'

// One shared EventSource relays the caller's signing events (GET /stream,
// signing:sign) through the gateway: signing.inbox for the caller's own signer
// slots and the tenant's submission completed / cancelled / expired events.
// Payloads carry ids and status only. It is reference-counted so the inbox and
// a submission view share one connection. The module closes the stream at
// 290 s; EventSource reconnects on its own (sending Last-Event-ID).
export const STREAM_URL = '/api/signing/v1/stream'
export const EVENTS = ['signing.inbox', 'signing.submission.completed', 'signing.submission.cancelled', 'signing.submission.expired'] as const
export type SigningEventType = (typeof EVENTS)[number]
export interface SigningEvent {
  type: SigningEventType
  data: { submission_id?: string; signer_id?: string; state?: string } & Record<string, unknown>
}
export type Listener = (e: SigningEvent) => void

export const useLive = defineStore('signing-live', () => {
  const connected = ref(false)
  let source: EventSource | null = null
  let refs = 0
  const listeners = new Set<Listener>()

  function handle(type: SigningEventType, raw: string): void {
    let data: unknown
    try {
      data = JSON.parse(raw)
    } catch {
      return // non-JSON frames are ignored
    }
    if (!data || typeof data !== 'object') return
    for (const l of listeners) l({ type, data: data as SigningEvent['data'] })
  }

  function open(): void {
    if (source || typeof EventSource === 'undefined') return
    source = new EventSource(STREAM_URL, { withCredentials: true })
    source.onopen = () => (connected.value = true)
    source.onerror = () => (connected.value = false)
    for (const t of EVENTS) source.addEventListener(t, (e) => handle(t, (e as MessageEvent).data))
  }

  function close(): void {
    refs = 0
    source?.close()
    source = null
    connected.value = false
  }

  /** Opens the stream (first caller) and returns a release function. */
  function connect(): () => void {
    refs += 1
    open()
    let released = false
    return () => {
      if (released) return
      released = true
      refs -= 1
      if (refs <= 0) close()
    }
  }

  /** Subscribes to every event; returns the unsubscribe function. */
  function on(l: Listener): () => void {
    listeners.add(l)
    return () => listeners.delete(l)
  }

  return { connected, connect, close, on }
})

/** Calls fn at most once per `wait` ms for a burst of events; cancel stops a pending call. */
export function coalesce(fn: () => void, wait = 400): { trigger: () => void; cancel: () => void } {
  let timer: ReturnType<typeof setTimeout> | null = null
  return {
    trigger: () => {
      if (timer) return
      timer = setTimeout(() => {
        timer = null
        fn()
      }, wait)
    },
    cancel: () => {
      if (timer) clearTimeout(timer)
      timer = null
    },
  }
}
