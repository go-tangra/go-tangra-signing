// Conditions and formulas (US6) — HOOK. The rules evaluator of the
// conditional-fields story replaces the body of evaluate(); every caller (the
// signing page's visibility, requiredness and computed values) already goes
// through it. Until then every field is visible, required as configured in the
// builder, and nothing is computed. The server evaluates the same rules and
// stays the authority.
import type { Field } from '@/api/types'

export interface Evaluation {
  /** Field ids hidden by their conditions (hidden fields are neither shown nor checked). */
  hidden: Set<string>
  /** Field ids that must be filled. */
  required: Set<string>
  /** Calculated values (formulas) by field id. */
  computed: Record<string, string>
}

/**
 * Evaluates the fields' conditions and formulas over the current values
 * (every field of the submission, field id → value).
 * TODO(US6): conditions (visible / required when …) and number formulas.
 */
export function evaluate(fields: readonly Field[], values: Readonly<Record<string, string>>): Evaluation {
  void values
  return { hidden: new Set(), required: new Set(fields.filter((f) => f.required).map((f) => f.id)), computed: {} }
}
