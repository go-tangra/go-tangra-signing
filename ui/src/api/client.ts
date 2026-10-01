// The signing API through the gateway: the kit client bound to this module's base.
// Mutating calls carry the platform CSRF header (X-CSRF-Token, double-submit
// cookie) — the kit client adds it to every non-GET request.
import { createApi, ApiError, csrfToken, describe, type Method, type RequestOptions } from '@go-tangra/ui/api'
import { registerReasons } from '@go-tangra/ui/forms'
import type { paths } from './schema.d'
import type { ListParams } from '@go-tangra/ui'

export { ApiError, csrfToken, describe }
export type { Method, RequestOptions }

// Path names are checked against the OpenAPI contract at compile time.
export type ApiPath = keyof paths
export const BASE = '/api/signing/v1'

// Signing-specific refusal reasons (closed vocabulary, api/openapi/signing.yaml).
registerReasons({
  invalid_pdf: 'That file is not a valid PDF (or it is encrypted).',
  payload_too_large: 'The file is too large.',
  invalid_field: 'A field is not valid.',
  invalid_rule: 'A condition or formula is not valid.',
  version_conflict: 'Someone else saved this template meanwhile. Reload and try again.',
  template_in_use: 'The template is used by unfinished submissions.',
  template_not_active: 'Only active templates can be sent.',
  folder_not_empty: 'The folder is not empty.',
  name_taken: 'That name is already taken.',
  invalid_signer: 'A signer is not an active member of this tenant.',
  invalid_prefill: 'A prefilled value is not valid.',
  not_draft: 'The submission has already been sent.',
  signer_final: 'That signer has already signed or declined.',
  missing_required: 'Fill in every required field.',
  invalid_value: 'A value is not valid.',
  pin_invalid: 'Wrong PIN.',
  certificate_locked: 'Too many wrong PINs. Your certificate is locked for a while.',
  certificate_missing: 'Set up your signing certificate first.',
  certificate_unusable: 'Your signing certificate is expired or revoked. Request a new one.',
  certificate_exists: 'You already have an active signing certificate.',
  not_your_turn: 'It is not your turn to sign yet.',
  submission_not_open: 'This document can no longer be signed.',
  already_signed: 'You have already signed this document.',
  rate_limited: 'Too many attempts. Wait a minute and try again.',
  qes_signature_invalid: 'The qualified signature does not match the document.',
  preparation_expired: 'The qualified signing step expired. Start it again.',
  document_changed: 'The document changed meanwhile. Start again.',
  tsa_failed: 'The timestamp authority did not answer.',
  contact_missing: 'The user has no e-mail address.',
  backup_too_large: 'The backup is too large.',
  invalid_backup: 'The backup file is not valid.',
})

export const api = createApi({ base: BASE })

/** The field a refusal names (`field` in the body: a field name, "parties", "status"…), if any. */
export function refusalField(err: unknown): string | undefined {
  if (!(err instanceof ApiError)) return undefined
  const f = err.detail?.field
  return typeof f === 'string' && f ? f : undefined
}

/** describe() plus the field the server named ("A field is not valid. (Salary)"). */
export function describeRefusal(err: unknown): string {
  const f = refusalField(err)
  return f ? `${describe(err)} (${f})` : describe(err)
}

/** A value of the refusal's detail object (`attempts_left`, `locked_until`…), if any. */
export function refusalDetail(err: unknown, key: string): unknown {
  return err instanceof ApiError ? err.detail?.[key] : undefined
}

/** The wording registered for a reason code (the session's `reason` when it cannot sign). */
export function describeReason(reason: string): string {
  return describe(new ApiError(0, reason))
}

/** The list parameters of a page request (go-tangra list contract): page and size always, sort and order only when chosen. */
export function pageQuery(q: Partial<ListParams>, defaultSize: number): { page: number; page_size: number; sort?: string; order?: 'asc' | 'desc' } {
  return { page: q.page ?? 1, page_size: q.page_size ?? defaultSize, ...(q.sort ? { sort: q.sort } : {}), ...(q.order ? { order: q.order } : {}) }
}

/** A module URL with the query (blank values left out), like the kit client builds it. */
function url(path: string, query?: Record<string, string | undefined>): string {
  const q = new URLSearchParams()
  for (const [k, v] of Object.entries(query ?? {})) if (v !== undefined && v !== '') q.set(k, v)
  const qs = q.toString()
  return `${BASE}/${path}${qs ? '?' + qs : ''}`
}

/** fetch with the kit client's conventions: session cookie, the CSRF header on non-GET requests; a failed fetch reads as ApiError "network". */
async function send(method: Method, target: string, headers: Record<string, string>, body?: BodyInit): Promise<Response> {
  try {
    return await fetch(target, {
      method,
      headers: { ...(method !== 'GET' ? { 'X-CSRF-Token': csrfToken() } : {}), ...headers },
      credentials: 'same-origin',
      ...(body !== undefined ? { body } : {}),
    })
  } catch (err) {
    if (err instanceof DOMException && err.name === 'AbortError') throw err
    throw new ApiError(0, 'network')
  }
}

/**
 * A refusal as ApiError: reason plus detail, with the field the refusal names
 * kept readable next to a detail object (the kit client drops `field` when
 * the body also has `detail`, e.g. invalid_rule's message).
 */
async function refusal(res: Response): Promise<ApiError> {
  const data = (await res.json().catch(() => ({}))) as Record<string, unknown>
  const reason = typeof data.reason === 'string' ? data.reason : 'error'
  const rest = Object.fromEntries(Object.entries(data).filter(([k]) => k !== 'reason' && k !== 'detail'))
  const detail = typeof data.detail === 'object' && data.detail !== null ? { ...rest, ...(data.detail as Record<string, unknown>) } : rest
  return new ApiError(res.status, reason, Object.keys(detail).length ? detail : undefined)
}

async function json<T>(res: Response): Promise<T> {
  if (!res.ok) throw await refusal(res)
  if (res.status === 204) return undefined as T
  return (await res.json().catch(() => ({}))) as T
}

/**
 * POSTs a multipart/form-data body with several parts (the kit's upload()
 * sends one file). Same conventions as the kit client: session cookie, the
 * CSRF header, JSON answers, refusals as ApiError with reason and detail.
 */
export async function postForm<T = unknown>(path: string, form: FormData): Promise<T> {
  return json<T>(await send('POST', url(path), { Accept: 'application/json' }, form))
}

/** A JSON request whose refusal keeps both `field` and `detail` (see refusal()). */
export async function sendJSON<T = unknown>(method: Method, path: string, body: unknown): Promise<T> {
  return json<T>(await send(method, url(path), { Accept: 'application/json', 'Content-Type': 'application/json' }, JSON.stringify(body)))
}

/** POSTs raw bytes (e.g. a backup archive) with their content type; the answer is JSON. */
export async function postBytes<T = unknown>(path: string, body: Blob, contentType: string, query?: Record<string, string | undefined>): Promise<T> {
  return json<T>(await send('POST', url(path, query), { Accept: 'application/json', 'Content-Type': contentType }, body))
}

/** POSTs without a body and takes a file back (the blob and the name from Content-Disposition, if any). */
export async function postDownload(path: string, query?: Record<string, string | undefined>): Promise<{ blob: Blob; filename: string }> {
  const res = await send('POST', url(path, query), { Accept: 'application/gzip, application/json' })
  if (!res.ok) throw await refusal(res)
  const cd = res.headers.get('Content-Disposition') ?? ''
  const star = /filename\*=UTF-8''([^;]+)/i.exec(cd)?.[1]?.trim()
  let filename = /filename="?([^";]+)"?/i.exec(cd)?.[1]?.trim() ?? ''
  if (star) {
    try {
      filename = decodeURIComponent(star)
    } catch {
      // Keep the plain name.
    }
  }
  return { blob: await res.blob(), filename }
}
