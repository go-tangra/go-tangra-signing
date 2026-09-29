// Builder helpers of the conditions and formula editors, kept out of the
// components so they are tested without a DOM: the operators each field type
// offers, which ones take a value, and the rule JSON the module expects
// (api/openapi/signing.yaml Conditions / Rule).
import type { Conditions, Field, FieldType, Rule } from '@/api/types'
import { isTextValued } from '@/utils/values'
import { hasOptions } from '@/utils/fields'

export type RuleOp = Rule['op']

/** At most this many rules per field (the module's limit). */
export const MAX_RULES = 20
/** Longest rule value and formula the module accepts. */
export const MAX_RULE_VALUE = 500
export const MAX_FORMULA = 500

export const OP_LABELS: Record<RuleOp, string> = {
  eq: 'is', neq: 'is not', contains: 'contains', empty: 'is empty', not_empty: 'is filled', checked: 'is checked', unchecked: 'is not checked',
}

/** Operators that compare with a value. */
export const needsValue = (op: RuleOp): boolean => op === 'eq' || op === 'neq' || op === 'contains'

/** The operators valid for a field type: checked / unchecked only for checkboxes; select and radio compare with an option. */
export function opsFor(t: FieldType): RuleOp[] {
  if (t === 'checkbox') return ['checked', 'unchecked']
  if (hasOptions(t)) return ['eq', 'neq', 'empty', 'not_empty']
  return ['eq', 'neq', 'contains', 'empty', 'not_empty']
}

/** Fields a condition can look at: the other fields that take a typed value. */
export const ruleTargets = (fields: readonly Field[], self: string): Field[] => fields.filter((f) => f.id !== self && isTextValued(f.type))

/** A rule on the target field with its first valid operator (and the first option as the value of select / radio). */
export function newRule(target: Field): Rule {
  return fitRule({ field: target.id, op: opsFor(target.type)[0]! }, target)
}

/** The rule adjusted to its target: a valid operator, a value only when the operator takes one (an option for select / radio). */
export function fitRule(r: Rule, target: Field | undefined): Rule {
  const ops = target ? opsFor(target.type) : []
  const op = target && !ops.includes(r.op) ? ops[0]! : r.op
  if (!needsValue(op)) return { field: r.field, op }
  let value = r.value ?? ''
  if (target && hasOptions(target.type)) {
    const opts = (target.options ?? []).filter(Boolean)
    if (!opts.includes(value)) value = opts[0] ?? ''
  }
  return { field: r.field, op, value }
}

/** Default conditions when the editor is switched on (null when no field can be a target). */
export function newConditions(fields: readonly Field[], self: string): Conditions | null {
  const target = ruleTargets(fields, self)[0]
  if (!target) return null
  const list: Rule[] = [newRule(target)]
  return { mode: 'all', effect: 'visible', rules: list }
}
