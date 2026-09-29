// Signing field values, mirroring the module's rules (internal/submissions
// values.go) so the signer sees a problem before the round-trip: numbers accept
// a decimal comma, dates are YYYY-MM-DD, checkboxes "true"/"false", select and
// radio one of the options, text at most 2000 characters and cells 200.
// Signature, initials and stamp fields take no text (they are drawn from the
// signature image, the typed name or the caption); image and file fields are
// uploads. The server re-checks everything.
import type { Field, FieldType } from '@/api/types'

export const MAX_TEXT = 2000
export const MAX_CELLS = 200
/** Server limit on the signature image and on an image field upload. */
export const MAX_IMAGE_BYTES = 1024 * 1024

const TEXT_VALUED: ReadonlySet<FieldType> = new Set(['text', 'number', 'date', 'checkbox', 'select', 'radio', 'cells'])

/** The field takes a text value (sent in the values JSON). */
export const isTextValued = (t: FieldType): boolean => TEXT_VALUED.has(t)
/** The field takes an uploaded file (sent as upload.<field id>). */
export const isUpload = (t: FieldType): boolean => t === 'image' || t === 'file'
/** The field is drawn from the signature image or the typed name. */
export const isSignatureLike = (t: FieldType): boolean => t === 'signature' || t === 'initials' || t === 'stamp'

const DATE = /^\d{4}-\d{2}-\d{2}$/

function validDate(v: string): boolean {
  if (!DATE.test(v)) return false
  const d = new Date(v + 'T00:00:00Z')
  return !Number.isNaN(d.getTime()) && d.toISOString().slice(0, 10) === v
}

/** Why a text value is not acceptable for the field ('' when it is; an empty value is always acceptable here). */
export function valueError(f: Field, v: string): string {
  if (v === '') return ''
  // Control characters are refused by the server except in multi-line text.
  // eslint-disable-next-line no-control-regex
  if (/[\u0000-\u0008\u000b\u000c\u000e-\u001f\u007f]/.test(v)) return 'Remove the control characters.'
  if ([...v].length > MAX_TEXT) return `At most ${MAX_TEXT} characters.`
  switch (f.type) {
    case 'text':
      return ''
    case 'cells':
      return [...v].length > MAX_CELLS ? `At most ${MAX_CELLS} characters.` : ''
    case 'number': {
      const s = v.trim().replace(',', '.')
      return s !== '' && /^[+-]?(\d+\.?\d*|\.\d+)([eE][+-]?\d+)?$/.test(s) && Number.isFinite(Number(s)) ? '' : 'Enter a number.'
    }
    case 'date':
      return validDate(v) ? '' : 'Enter a date (YYYY-MM-DD).'
    case 'checkbox':
      return v === 'true' || v === 'false' ? '' : 'Not a valid choice.'
    case 'select':
    case 'radio':
      return (f.options ?? []).includes(v) ? '' : 'Choose one of the options.'
    default:
      return 'This field takes no typed value.'
  }
}

/** The field counts as filled (requiredness), like the server's Filled(). */
export function isFilled(f: Field, v: string, hasUpload: boolean): boolean {
  if (f.type === 'checkbox') return v === 'true'
  if (isUpload(f.type)) return hasUpload
  if (isSignatureLike(f.type)) return true
  return v.trim() !== ''
}
