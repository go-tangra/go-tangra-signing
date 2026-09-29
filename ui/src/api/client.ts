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
