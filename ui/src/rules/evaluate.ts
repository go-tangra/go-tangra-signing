// Conditions and formulas on the signing page: every caller (visibility,
// requiredness and computed values) goes through evaluate(). The rules are
// evaluated exactly as the module does (./rules.ts is a port of
// internal/rules, checked against the same vectors); the server recomputes
// everything on submit and stays the authority.
import type { Field } from '@/api/types'
import { RuleError, evaluateRules } from './rules'

export interface Evaluation {
  /** Field ids hidden by their conditions (hidden fields are neither shown nor checked). */
  hidden: Set<string>
  /** Field ids that must be filled. */
  required: Set<string>
  /** Calculated values (formulas) by field id ("" when undefined). */
  computed: Record<string, string>
}

/**
 * Evaluates the fields' conditions and formulas over the current values
 * (every field of the submission, field id → value). Rules the module would
 * refuse (it names the field on submit) leave every field visible, required
 * as configured and nothing computed.
 */
export function evaluate(fields: readonly Field[], values: Readonly<Record<string, string>>): Evaluation {
  try {
    return evaluateRules(fields, values)
  } catch (e) {
    if (!(e instanceof RuleError)) throw e
    return { hidden: new Set(), required: new Set(fields.filter((f) => f.required).map((f) => f.id)), computed: {} }
  }
}
