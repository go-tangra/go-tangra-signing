// The signing API through the gateway: the kit client bound to this module's base.
// Mutating calls carry the platform CSRF header (X-CSRF-Token, double-submit
// cookie) — the kit client adds it to every non-GET request.
import { createApi, ApiError, csrfToken, describe, type Method, type RequestOptions } from '@go-tangra/ui/api'
import { registerReasons } from '@go-tangra/ui/forms'
import type { paths } from './schema.d'

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

/**
 * POSTs a multipart/form-data body with several parts (the kit's upload()
 * sends one file). Same conventions as the kit client: session cookie, the
 * CSRF header, JSON answers, refusals as ApiError with reason and detail.
 */
export async function postForm<T = unknown>(path: string, form: FormData): Promise<T> {
  let res: Response
  try {
    res = await fetch(`${BASE}/${path}`, {
      method: 'POST',
      headers: { Accept: 'application/json', 'X-CSRF-Token': csrfToken() },
      credentials: 'same-origin',
      body: form,
    })
  } catch (err) {
    if (err instanceof DOMException && err.name === 'AbortError') throw err
    throw new ApiError(0, 'network')
  }
  if (res.status === 204) return undefined as T
  const data = (await res.json().catch(() => ({}))) as Record<string, unknown>
  if (!res.ok) {
    const reason = typeof data.reason === 'string' ? data.reason : 'error'
    const rest = Object.fromEntries(Object.entries(data).filter(([k]) => k !== 'reason' && k !== 'detail'))
    // The field a refusal names stays readable next to a detail object.
    const detail = typeof data.detail === 'object' && data.detail !== null ? { ...rest, ...(data.detail as Record<string, unknown>) } : rest
    throw new ApiError(res.status, reason, Object.keys(detail).length ? detail : undefined)
  }
  return data as T
}
