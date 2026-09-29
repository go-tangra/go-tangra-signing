import { describe, expect, it } from 'vitest'
import type { Field } from '@/api/types'
import { evaluateRules, holds, matches, validateRules, RuleError } from '@/rules/rules'
import { evaluate } from '@/rules/evaluate'
import { format, goLower, goTrim, parse, plainDecimal, round, toNumber } from '@/rules/formula'
// The SHARED vectors of the Go and TypeScript evaluators, read straight from
// the Go tree (never copied): both must give identical results for every case.
import vectorsText from '../../../internal/rules/testdata/vectors.json?raw'
import { field } from './helpers'

interface VectorCase {
  name: string
  fields: Field[]
  values?: Record<string, string>
  invalid?: string
  want?: { hidden: string[]; required: string[]; computed: Record<string, string> }
}
const cases = (JSON.parse(vectorsText) as { cases: VectorCase[] }).cases
const sorted = (s: Iterable<string>) => [...s].sort()

describe('shared rule vectors (internal/rules/testdata/vectors.json)', () => {
  it('has cases, valid and invalid', () => {
    expect(cases.length).toBeGreaterThan(20)
    expect(cases.some((c) => c.invalid)).toBe(true)
    expect(cases.some((c) => !c.invalid)).toBe(true)
  })

  it.each(cases.map((c) => [c.name, c] as const))('%s', (_name, c) => {
    if (c.invalid) {
      const err = validateRules(c.fields)
      expect(err).toBeInstanceOf(RuleError)
      expect(err!.field).toBe(c.invalid)
      expect(err!.message).not.toBe('')
      expect(() => evaluateRules(c.fields, c.values ?? {})).toThrow(RuleError)
      return
    }
    expect(validateRules(c.fields)).toBeNull()
    const res = evaluateRules(c.fields, c.values ?? {})
    expect(sorted(res.hidden)).toEqual(sorted(c.want!.hidden))
    expect(sorted(res.required)).toEqual(sorted(c.want!.required))
    expect(res.computed).toEqual(c.want!.computed)
  })
})

describe('formula parsing limits (as Go TestDeepNestingAndLimits)', () => {
  it('refuses deep nesting, too many tokens, too many arguments and broken input', () => {
    expect(() => parse('('.repeat(40) + '1' + ')'.repeat(40))).toThrow('nested too deeply')
    expect(() => parse('1+'.repeat(200) + '1')).toThrow('formula too long')
    expect(() => parse('sum(' + '1,'.repeat(60) + '1)')).toThrow('too many arguments')
    for (const bad of ['', '{}', 'sum', 'sum(1', 'sum(1;2)', '1 2', ')', '*1', '{A}{', '-', '- *', '1.2.3', 'foo(1)', 'round(1,2,3)']) {
      expect(() => parse(bad), bad).toThrow()
    }
    expect(() => parse('('.repeat(31) + '1' + ')'.repeat(31))).not.toThrow()
  })
})

describe('number helpers (values checked against Go strconv)', () => {
  it('format: two decimals, shortest form, never an exponent or negative zero', () => {
    expect(format(0.1 + 0.2)).toBe('0.3')
    expect(format(0.1 * 3)).toBe('0.3')
    expect(format(1e14)).toBe('100000000000000')
    expect(format(1e14 + 0.1)).toBe('100000000000000.1')
    expect(format(999999999999999.9)).toBe('999999999999999.9')
    expect(format(Number('99999999999999.99'))).toBe('99999999999999.98') // the literal reads as 99999999999999.98
    expect(format(1e15)).toBe('')
    expect(format(-1e15)).toBe('')
    expect(format(-0)).toBe('0')
    expect(format(-0.001)).toBe('0')
    expect(format(-0.005)).toBe('-0.01')
    expect(format(0.005)).toBe('0.01')
    expect(format(1.005)).toBe('1.01')
    expect(format(2.675)).toBe('2.68')
    expect(format(123456789.125)).toBe('123456789.13')
    expect(format(1e-7)).toBe('0')
    expect(format(5e-324)).toBe('0')
    expect(format(-1234.567)).toBe('-1234.57')
    expect(format(100)).toBe('100')
    expect(format(NaN)).toBe('')
    expect(format(Infinity)).toBe('')
    expect(format(undefined)).toBe('')
  })

  it('plainDecimal expands exponent notation', () => {
    expect(plainDecimal('1e+21')).toBe('1000000000000000000000')
    expect(plainDecimal('1.5e-7')).toBe('0.00000015')
    expect(plainDecimal('-2.5e3')).toBe('-2500')
    expect(plainDecimal('12.34')).toBe('12.34')
  })

  it('round: half away from zero, like Go Round', () => {
    expect(round(-1.235, 2)).toBe(-1.24)
    expect(round(0.125, 2)).toBe(0.13)
    expect(round(2.5, 0)).toBe(3)
    expect(round(-2.5, 0)).toBe(-3)
  })

  it('toNumber parses strictly like strconv.ParseFloat (decimal comma accepted)', () => {
    const want: [string, number][] = [
      ['', 0], ['  ', 0], [' 1,5 ', 1.5], ['12abc', 0], ['Inf', 0], ['-inf', 0], ['NaN', 0], ['1e400', 0], ['1e-400', 0],
      ['0x1p4', 16], ['0x10', 0], ['1_000', 1000], ['1__0', 0], ['_1', 0], ['1,2,3', 0], ['+.5', 0.5], ['5.', 5], ['.', 0],
      ['1e5', 100000], [' 12 ', 12], ['﻿12', 0], ['\u008512', 12], ['１２', 0], ['1e1_0', 1e10], ['0x_1.8p1', 3], ['1.5e-3', 0.0015],
    ]
    for (const [s, n] of want) expect(toNumber(s), JSON.stringify(s)).toBe(n)
    expect(Object.is(toNumber('-0'), -0)).toBe(true)
  })

  it('Go string semantics: TrimSpace and rune-wise ToLower', () => {
    expect(goTrim('\u0085 x 　')).toBe('x')
    expect(goTrim('﻿x')).toBe('﻿x')
    expect(goLower('ΑΣ')).toBe('ασ')
    expect(goLower('İ')).toBe('i')
  })
})

describe('conditions', () => {
  it('unknown operators never match; empty rule lists are all → true, any → false', () => {
    expect(holds({ op: 'matches' as never }, 'x')).toBe(false)
    expect(matches({ mode: 'all', rules: [] }, new Map())).toBe(true)
    expect(matches({ mode: 'any', rules: [] }, new Map())).toBe(false)
  })

  it('evaluate() falls back to the configured requiredness when the rules are invalid', () => {
    const ev = evaluate([field({ id: 'a', required: true, type: 'number', formula: '{Nope}' }), field({ id: 'b', name: 'B' })], {})
    expect([...ev.required]).toEqual(['a'])
    expect([...ev.hidden]).toEqual([])
    expect(ev.computed).toEqual({})
  })
})
