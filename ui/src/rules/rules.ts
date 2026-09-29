// Conditions and formulas of a template's fields: a step-by-step port of the
// module's evaluator (internal/rules/rules.go). Conditions make a field
// visible or required when all / any of their rules hold over other fields'
// values; formulas compute number fields. Everything is evaluated in ONE
// dependency order (a depth-first walk over formula and condition references,
// dependencies visited in ascending field index, exactly like Go's order()):
// a hidden field counts as empty downstream and is not computed. Invalid
// rules, unknown fields and cycles are refused, naming the field. The server
// evaluates the same rules and stays the authority.
import type { Conditions, Field, Rule } from '@/api/types'
import { FormulaSyntaxError, evalNode, format, goLower, goTrim, parse, refs, toNumber, type Node } from './formula'

/** A refused rule, naming the field (its name) it belongs to. */
export class RuleError extends Error {
  constructor(readonly field: string, readonly msg: string) {
    super(`rules: ${field}: ${msg}`)
    this.name = 'RuleError'
  }
}

/** The evaluation of a submission's fields (Go's Result). */
export interface RulesResult {
  /** Field id → not shown. */
  hidden: Set<string>
  /** Field ids required now (visible fields only). */
  required: Set<string>
  /** Field id → formula value ("" when undefined), visible formula fields only. */
  computed: Record<string, string>
}

interface Graph {
  fields: readonly Field[]
  byName: Map<string, number>
  byID: Map<string, number>
  formula: Map<number, Node>
  deps: Map<number, number[]>
}

function build(fields: readonly Field[]): Graph {
  const g: Graph = { fields, byName: new Map(), byID: new Map(), formula: new Map(), deps: new Map() }
  fields.forEach((f, i) => {
    g.byName.set(goLower(goTrim(f.name)), i)
    g.byID.set(f.id, i)
  })
  fields.forEach((f, i) => {
    const seen = new Set<number>()
    const add = (j: number) => {
      if (seen.has(j)) return
      seen.add(j)
      g.deps.set(i, [...(g.deps.get(i) ?? []), j])
    }
    if (f.formula) {
      if (f.type !== 'number') throw new RuleError(f.name, 'only number fields take a formula')
      let n: Node
      try {
        n = parse(f.formula)
      } catch (e) {
        if (e instanceof FormulaSyntaxError) throw new RuleError(f.name, e.message)
        throw e
      }
      for (const name of refs(n)) {
        const j = g.byName.get(name)
        if (j === undefined) throw new RuleError(f.name, `unknown field {${name}}`)
        if (j === i) throw new RuleError(f.name, 'a formula cannot use its own field')
        add(j)
      }
      g.formula.set(i, n)
    }
    for (const r of f.conditions?.rules ?? []) {
      const j = g.byID.get(r.field)
      if (j === undefined || j === i) throw new RuleError(f.name, 'condition on an unknown field')
      add(j)
    }
  })
  return g
}

/** The fields in dependency order, or the name of a field on a cycle. */
function order(g: Graph): { ord: number[]; cycle: string | null } {
  const state = new Array<number>(g.fields.length).fill(0) // 0 new, 1 visiting, 2 done
  const out: number[] = []
  let cycle: string | null = null
  const visit = (i: number): boolean => {
    if (state[i] === 1) {
      cycle = g.fields[i]!.name
      return false
    }
    if (state[i] === 2) return true
    state[i] = 1
    const deps = [...(g.deps.get(i) ?? [])].sort((a, b) => a - b)
    for (const j of deps) if (!visit(j)) return false
    state[i] = 2
    out.push(i)
    return true
  }
  for (let i = 0; i < g.fields.length; i++) if (!visit(i)) return { ord: [], cycle }
  return { ord: out, cycle: null }
}

/** Refuses syntax errors, unknown fields and cycles: the RuleError naming the field, or null when the rules are valid. */
export function validateRules(fields: readonly Field[]): RuleError | null {
  try {
    const { cycle } = order(build(fields))
    return cycle !== null ? new RuleError(cycle, 'rules form a cycle') : null
  } catch (e) {
    if (e instanceof RuleError) return e
    throw e
  }
}

/**
 * Computes visibility, requiredness and formula values over values (field id
 * → value; values of hidden fields are ignored). Throws RuleError for invalid
 * rules, like Go's Evaluate returns an error.
 */
export function evaluateRules(fields: readonly Field[], values: Readonly<Record<string, string>>): RulesResult {
  const g = build(fields)
  const { ord, cycle } = order(g)
  if (cycle !== null) throw new RuleError(cycle, 'rules form a cycle')
  const res: RulesResult = { hidden: new Set(), required: new Set(), computed: {} }
  const cur = new Map<string, string>() // effective values (hidden → "")
  for (const i of ord) {
    const f = fields[i]!
    let visible = true
    let required = !!f.required
    const c = f.conditions
    if (c && (c.rules ?? []).length > 0) {
      const match = matches(c, cur)
      if (c.effect === 'visible') visible = match
      else required = required || match
    }
    if (!visible) {
      res.hidden.add(f.id)
      cur.set(f.id, '')
      continue
    }
    if (required) res.required.add(f.id)
    let v = values[f.id] ?? ''
    const n = g.formula.get(i)
    if (n) {
      v = format(evalNode(n, (name) => toNumber(cur.get(fields[g.byName.get(name)!]!.id) ?? '')))
      res.computed[f.id] = v
    }
    cur.set(f.id, v)
  }
  return res
}

/** The rules hold (all of them, or any for mode "any"). */
export function matches(c: Pick<Conditions, 'mode' | 'rules'>, cur: ReadonlyMap<string, string>): boolean {
  const all = c.mode !== 'any'
  for (const r of c.rules ?? []) {
    const ok = holds(r, cur.get(r.field) ?? '')
    if (all && !ok) return false
    if (!all && ok) return true
  }
  return all
}

/** One rule over a field's effective value. */
export function holds(r: Pick<Rule, 'op' | 'value'>, value: string): boolean {
  const v = goTrim(value)
  const want = goTrim(r.value ?? '')
  switch (r.op) {
    case 'eq':
      return v === want
    case 'neq':
      return v !== want
    case 'contains':
      return goLower(v).includes(goLower(want))
    case 'empty':
      return v === ''
    case 'not_empty':
      return v !== ''
    case 'checked':
      return v === 'true'
    case 'unchecked':
      return v !== 'true'
  }
  return false
}
