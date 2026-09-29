// Qualified electronic signature with the signer's card through B-Trust BISS
// (Browser Independent Signing Service): a local application answering HTTPS
// on https://localhost:53952..53955. The flow (FR-022/FR-023):
//
//   1. detect   GET  localhost/version on every port (short timeouts), the
//               first port in order that answers wins;
//   2. choose   POST localhost/getsigner — the user picks the certificate in
//               BISS; the answer is the chain (base64 DER, leaf first);
//   3. prepare  POST /signing/{id}/qes/prepare {values, chain, signature_image}
//               — the module fixes the document and returns the signed
//               attributes to sign (plus, when configured, the origin proof);
//   4. sign     POST localhost/sign — BISS asks for the card PIN and signs;
//   5. complete POST /signing/{id}/qes/complete {preparation_id, signature_b64}.
//
// Nothing goes to localhost with credentials (credentials 'omit', no
// referrer), and nothing BISS answers goes anywhere but this module's API.
import { ref } from 'vue'
import { ApiError, api, describe, refusalField } from '@/api/client'
import type { QESPrepare, QESPrepared, SignResult } from '@/api/types'

export const BISS_PORTS = [53952, 53953, 53954, 53955] as const

export interface BissTimeouts {
  /** Per port while looking for BISS. */
  detect: number
  /** While the user chooses a certificate in BISS. */
  choose: number
  /** While BISS asks for the card PIN and signs. */
  sign: number
}
export const DEFAULT_TIMEOUTS: BissTimeouts = { detect: 1500, choose: 120_000, sign: 180_000 }

/** Where a run is (drives the button's wording). */
export type BissPhase = 'idle' | 'detecting' | 'choosing' | 'preparing' | 'signing' | 'completing'

/** How a run ended. */
export type BissOutcome =
  | { kind: 'signed'; result: SignResult }
  | { kind: 'not_installed' }
  /** BISS answered but did not sign (the user cancelled, the card refused…). */
  | { kind: 'refused'; reasonCode: string; reasonText: string }
  | { kind: 'timeout' }
  /** The module refused (reason of the closed vocabulary, the field it names). */
  | { kind: 'server'; reason: string; message: string; field?: string | undefined }
  | { kind: 'error'; message: string }

/** A BISS answer (every property optional: it is a foreign program). */
interface BissAnswer {
  status?: unknown
  reasonCode?: unknown
  reasonText?: unknown
  version?: unknown
  chain?: unknown
  signatures?: unknown
}

/** The /sign request of the BISS protocol. */
export interface BissSignRequest {
  version: '1.0'
  contents: string[]
  contentType: 'data'
  hashAlgorithm: 'SHA256'
  signatureType: 'signature'
  signerCertificateB64: string
  confirmText: string[]
  signedContents?: string[]
  signedContentsCert?: string[]
}

class BissTimeout extends Error {}
class BissUnreachable extends Error {}

const MAX_CHAIN = 5
const MAX_CERT_CHARS = 16384
const MAX_SIGNATURE_CHARS = 32768
const B64 = /^[A-Za-z0-9+/=\r\n]+$/

/** One call to the local BISS: no credentials, no referrer, aborted after timeoutMs. */
async function local(port: number, path: string, timeoutMs: number, body?: unknown): Promise<BissAnswer> {
  const ctl = new AbortController()
  let timedOut = false
  const timer = setTimeout(() => {
    timedOut = true
    ctl.abort()
  }, timeoutMs)
  try {
    const init: RequestInit = {
      method: body === undefined ? 'GET' : 'POST',
      mode: 'cors',
      credentials: 'omit',
      cache: 'no-store',
      referrerPolicy: 'no-referrer',
      signal: ctl.signal,
    }
    if (body !== undefined) {
      init.headers = { 'Content-Type': 'application/json' }
      init.body = JSON.stringify(body)
    }
    const res = await fetch(`https://localhost:${port}/${path}`, init)
    const data = (await res.json().catch(() => null)) as BissAnswer | null
    if (!data || typeof data !== 'object') throw new BissUnreachable(`BISS answered ${res.status} without JSON`)
    return data
  } catch (e) {
    if (timedOut) throw new BissTimeout()
    if (e instanceof BissUnreachable) throw e
    throw new BissUnreachable(e instanceof Error ? e.message : String(e))
  } finally {
    clearTimeout(timer)
  }
}

function refused(a: BissAnswer): BissOutcome {
  const reasonCode = a.reasonCode === undefined || a.reasonCode === null ? '' : String(a.reasonCode)
  const text = typeof a.reasonText === 'string' ? a.reasonText.trim().slice(0, 500) : ''
  return { kind: 'refused', reasonCode, reasonText: text || (reasonCode ? `BISS refused (code ${reasonCode}).` : 'BISS did not sign.') }
}

/** The chain BISS returned, when it is plausible (strings of base64, at most 5). */
function chainOf(a: BissAnswer): string[] | null {
  if (!Array.isArray(a.chain) || a.chain.length < 1 || a.chain.length > MAX_CHAIN) return null
  const out = a.chain.filter((c): c is string => typeof c === 'string' && c.length > 0 && c.length <= MAX_CERT_CHARS && B64.test(c))
  return out.length === a.chain.length ? out : null
}

function signatureOf(a: BissAnswer): string | null {
  const s = Array.isArray(a.signatures) ? a.signatures[0] : undefined
  return typeof s === 'string' && s.length > 0 && s.length <= MAX_SIGNATURE_CHARS && B64.test(s) ? s : null
}

/** The BISS /sign body for a preparation; the origin proof only when the module returned it. */
export function signRequest(prep: QESPrepared, leaf: string): BissSignRequest {
  const req: BissSignRequest = {
    version: '1.0',
    contents: [prep.signed_attrs_b64],
    contentType: 'data',
    hashAlgorithm: 'SHA256',
    signatureType: 'signature',
    signerCertificateB64: leaf,
    confirmText: ['hash'],
  }
  if (prep.signed_contents_b64 && prep.signed_contents_cert_b64) {
    req.signedContents = [prep.signed_contents_b64]
    req.signedContentsCert = [prep.signed_contents_cert_b64]
  }
  return req
}

/** A signature image as a data URL (for QESPrepare.signature_image). */
export function toDataUrl(blob: Blob): Promise<string> {
  return new Promise((resolve, reject) => {
    const r = new FileReader()
    r.onload = () => resolve(typeof r.result === 'string' ? r.result : '')
    r.onerror = () => reject(r.error ?? new Error('unreadable image'))
    r.readAsDataURL(blob)
  })
}

export function useBiss(opts: { timeouts?: Partial<BissTimeouts> } = {}) {
  const timeouts: BissTimeouts = { ...DEFAULT_TIMEOUTS, ...opts.timeouts }
  const phase = ref<BissPhase>('idle')
  const port = ref<number | null>(null)
  const version = ref('')

  /** Probes every port at once; the first port (in order) that answers /version wins. */
  async function detect(): Promise<number | null> {
    const probes = await Promise.all(BISS_PORTS.map(async (p) => {
      try {
        const a = await local(p, 'version', timeouts.detect)
        return typeof a.version === 'string' && a.version ? { port: p, version: a.version } : null
      } catch {
        return null
      }
    }))
    const found = probes.find((x) => x !== null) ?? null
    port.value = found?.port ?? null
    version.value = found?.version ?? ''
    return port.value
  }

  /** The whole qualified signing of one signer slot. */
  async function run(signerId: string, input: { values: Record<string, string>; signatureImage?: string | undefined }): Promise<BissOutcome> {
    try {
      phase.value = 'detecting'
      const p = await detect()
      if (p === null) return { kind: 'not_installed' }

      phase.value = 'choosing'
      const signer = await local(p, 'getsigner', timeouts.choose, { selector: { keyUsages: ['nonRepudiation'] }, showValidCerts: true })
      if (signer.status !== 'ok') return refused(signer)
      const chain = chainOf(signer)
      if (!chain) return { kind: 'error', message: 'BISS returned no usable certificate. Check the card and try again.' }

      phase.value = 'preparing'
      const body: QESPrepare = { values: input.values, chain }
      if (input.signatureImage) body.signature_image = input.signatureImage
      const prep = await api<QESPrepared>('POST', `signing/${signerId}/qes/prepare`, body)

      phase.value = 'signing'
      const signed = await local(p, 'sign', timeouts.sign, signRequest(prep, chain[0]!))
      if (signed.status !== 'ok') return refused(signed)
      const signature = signatureOf(signed)
      if (!signature) return { kind: 'error', message: 'BISS returned no usable signature. Try again.' }

      phase.value = 'completing'
      const result = await api<SignResult>('POST', `signing/${signerId}/qes/complete`, { preparation_id: prep.preparation_id, signature_b64: signature })
      return { kind: 'signed', result }
    } catch (e) {
      if (e instanceof BissTimeout) return { kind: 'timeout' }
      if (e instanceof BissUnreachable) return { kind: 'error', message: 'B-Trust BISS stopped answering. Check that it is running and try again.' }
      if (e instanceof ApiError) return { kind: 'server', reason: e.reason, message: describe(e), field: refusalField(e) }
      return { kind: 'error', message: describe(e) }
    } finally {
      phase.value = 'idle'
    }
  }

  return { phase, port, version, timeouts, detect, run }
}
