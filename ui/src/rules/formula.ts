// Formulas of number fields: a step-by-step port of the module's evaluator
// (internal/rules/formula.go) so the signer sees the same value the server
// stores. The grammar is restricted on purpose: numbers, field references
// {Field name} (case-insensitive, trimmed), + - * /, unary minus (binding
// tighter than * and /), parentheses and round(x[, digits]) / min / max / sum.
// Arithmetic is IEEE double in the same operation order as Go; a division by
// zero or an invalid round() makes the result undefined. Both evaluators are
// checked against the same vectors (internal/rules/testdata/vectors.json).

// Formula bounds (same as Go).
export const MAX_TOKENS = 300
export const MAX_DEPTH = 32
export const MAX_ARGS = 50

/** A formula syntax error (the message the Go parser gives). */
export class FormulaSyntaxError extends Error {
  constructor(message: string) {
    super(message)
    this.name = 'FormulaSyntaxError'
  }
}

// --- Go string helpers -------------------------------------------------------

/** unicode.IsSpace: Latin-1 spaces plus the Unicode White_Space property (no U+FEFF, unlike String.trim). */
export function isGoSpace(cp: number): boolean {
  if (cp <= 0xff) return cp === 0x20 || (cp >= 0x09 && cp <= 0x0d) || cp === 0x85 || cp === 0xa0
  return cp === 0x1680 || (cp >= 0x2000 && cp <= 0x200a) || cp === 0x2028 || cp === 0x2029 || cp === 0x202f || cp === 0x205f || cp === 0x3000
}

/** strings.TrimSpace. */
export function goTrim(s: string): string {
  const r = Array.from(s)
  let i = 0
  let j = r.length
  while (i < j && isGoSpace(r[i]!.codePointAt(0)!)) i++
  while (j > i && isGoSpace(r[j - 1]!.codePointAt(0)!)) j--
  return r.slice(i, j).join('')
}

/**
 * strings.ToLower: rune by rune (no context-dependent final sigma; U+0130
 * becomes a plain "i" as Go's unicode.ToLower gives).
 */
export function goLower(s: string): string {
  let out = ''
  for (const ch of s) {
    const lower = ch.toLowerCase()
    out += Array.from(lower).length === 1 ? lower : String.fromCodePoint(lower.codePointAt(0)!)
  }
  return out
}

// --- strconv.ParseFloat ------------------------------------------------------

/** strconv's underscoreOK: underscores only between digits (or after a base prefix). */
function underscoreOK(str: string): boolean {
  let s = str
  let saw = '^'
  let i = 0
  if (s.length >= 1 && (s[0] === '-' || s[0] === '+')) s = s.slice(1)
  let hex = false
  if (s.length >= 2 && s[0] === '0' && 'box'.includes(s[1]!.toLowerCase())) {
    i = 2
    saw = '0'
    hex = s[1]!.toLowerCase() === 'x'
  }
  for (; i < s.length; i++) {
    const c = s[i]!
    const l = c.toLowerCase()
    if ((c >= '0' && c <= '9') || (hex && l >= 'a' && l <= 'f')) {
      saw = '0'
      continue
    }
    if (c === '_') {
      if (saw !== '0') return false
      saw = '_'
      continue
    }
    if (saw === '_') return false
    saw = '!'
  }
  return saw !== '_'
}

const DECIMAL = /^[+-]?(\d+\.?\d*|\.\d+)([eE][+-]?\d+)?$/
const HEX = /^([+-]?)0[xX]([0-9a-fA-F]*)(?:\.([0-9a-fA-F]*))?[pP]([+-]?\d+)$/

/** A hexadecimal float (mantissa × 2^exp), rounded to nearest even like Go's atofHex. */
function hexFloat(neg: boolean, intDigits: string, fracDigits: string, exp: number): number {
  let m = BigInt('0x' + (intDigits + fracDigits || '0'))
  let e = exp - 4 * fracDigits.length
  const sign = neg ? -1 : 1
  if (m === 0n) return sign * 0
  const bits = m.toString(2).length
  const top = bits - 1 + e // exponent of the leading bit
  if (top > 1023) return sign * Infinity
  // Significant bits available: 53 for normal numbers, fewer when subnormal.
  const prec = top >= -1022 ? 53 : 53 - (-1022 - top)
  if (prec < 0) return sign * 0
  const shift = bits - prec
  if (shift > 0) {
    const s = BigInt(shift)
    let q = m >> s
    const rem = m & ((1n << s) - 1n)
    const half = 1n << (s - 1n)
    if (rem > half || (rem === half && (q & 1n) === 1n)) q += 1n
    m = q
    e += shift
  }
  // m has at most 54 bits here (a carry): exact as a double; scale in two steps
  // so intermediate powers of two stay in range.
  const h = Math.trunc(e / 2)
  const v = Number(m) * 2 ** h * 2 ** (e - h)
  if (!Number.isFinite(v)) return sign * Infinity
  return sign * v
}

/**
 * strconv.ParseFloat(s, 64): the whole string or nothing (no partial parses),
 * underscores between digits, hexadecimal floats with a p exponent, and the
 * inf / infinity / nan words. Returns null for a syntax or range error (Go's
 * err != nil); values that underflow become 0 without an error, as in Go.
 */
export function parseGoFloat(s: string): number | null {
  const lower = s.toLowerCase()
  const word = lower.replace(/^[+-]/, '')
  if (word === 'inf' || word === 'infinity') return lower.startsWith('-') ? -Infinity : Infinity
  if (word === 'nan') return NaN
  if (s.includes('_')) {
    if (!underscoreOK(s)) return null
    s = s.replaceAll('_', '')
  }
  const hex = HEX.exec(s)
  if (hex) {
    const [, sign, int = '', frac = '', exp = '0'] = hex
    if (!int && !frac) return null
    const v = hexFloat(sign === '-', int, frac, Number(exp))
    return Number.isFinite(v) ? v : null
  }
  if (!DECIMAL.test(s)) return null
  const v = Number(s)
  return Number.isFinite(v) ? v : null // out of range: ±Inf with ErrRange in Go
}

// --- lexer -------------------------------------------------------------------

type Token =
  | { kind: 'num'; num: number }
  | { kind: 'ref'; text: string }
  | { kind: 'ident'; text: string }
  | { kind: 'op'; text: string } // + - * / ( ) ,
  | { kind: 'eof' }

const isDigit = (c: string) => c >= '0' && c <= '9'
const isLetter = (c: string) => (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z')

function lex(src: string): Token[] {
  const out: Token[] = []
  const r = Array.from(src)
  for (let i = 0; i < r.length; ) {
    const c = r[i]!
    if (isGoSpace(c.codePointAt(0)!)) {
      i++
    } else if (c === '{') {
      let j = i + 1
      while (j < r.length && r[j] !== '}' && r[j] !== '{') j++
      if (j >= r.length || r[j] !== '}') throw new FormulaSyntaxError('unclosed field reference')
      const name = goTrim(r.slice(i + 1, j).join(''))
      if (name === '') throw new FormulaSyntaxError('empty field reference')
      out.push({ kind: 'ref', text: name })
      i = j + 1
    } else if (isDigit(c) || c === '.') {
      let j = i
      while (j < r.length && (isDigit(r[j]!) || r[j] === '.')) j++
      const text = r.slice(i, j).join('')
      const n = /^(\d+\.?\d*|\.\d+)$/.test(text) ? Number(text) : NaN
      if (!Number.isFinite(n)) throw new FormulaSyntaxError('bad number')
      out.push({ kind: 'num', num: n })
      i = j
    } else if (isLetter(c)) {
      let j = i
      while (j < r.length && isLetter(r[j]!)) j++
      out.push({ kind: 'ident', text: r.slice(i, j).join('').toLowerCase() })
      i = j
    } else if ('+-*/(),'.includes(c)) {
      out.push({ kind: 'op', text: c })
      i++
    } else {
      throw new FormulaSyntaxError('unexpected character')
    }
    if (out.length > MAX_TOKENS) throw new FormulaSyntaxError('formula too long')
  }
  out.push({ kind: 'eof' })
  return out
}

// --- parser ------------------------------------------------------------------

/** A parsed formula. */
export type Node =
  | { op: 'num'; num: number }
  | { op: 'ref'; ref: string }
  | { op: 'neg'; arg: Node }
  | { op: 'call'; fn: 'round' | 'min' | 'max' | 'sum'; args: Node[] }
  | { op: '+' | '-' | '*' | '/'; left: Node; right: Node }

function bp(t: Token): number {
  if (t.kind !== 'op') return 0
  if (t.text === '+' || t.text === '-') return 10
  if (t.text === '*' || t.text === '/') return 20
  return 0
}

const EOF: Token = { kind: 'eof' }

class Parser {
  pos = 0
  depth = 0
  constructor(private readonly toks: Token[]) {}
  peek(): Token { return this.toks[this.pos] ?? EOF }
  next(): Token { return this.toks[this.pos++] ?? EOF }

  expr(minBP: number): Node {
    this.depth++
    try {
      if (this.depth > MAX_DEPTH) throw new FormulaSyntaxError('formula nested too deeply')
      let left = this.prefix()
      for (;;) {
        const t = this.peek()
        const b = bp(t)
        if (t.kind !== 'op' || b === 0 || b <= minBP) return left
        this.next()
        const right = this.expr(b)
        left = { op: t.text as '+' | '-' | '*' | '/', left, right }
      }
    } finally {
      this.depth--
    }
  }

  prefix(): Node {
    const t = this.next()
    switch (t.kind) {
      case 'num':
        return { op: 'num', num: t.num }
      case 'ref':
        return { op: 'ref', ref: t.text }
      case 'ident': {
        const fn = t.text
        if (fn !== 'round' && fn !== 'min' && fn !== 'max' && fn !== 'sum') throw new FormulaSyntaxError('unknown function ' + fn)
        const o = this.next()
        if (o.kind !== 'op' || o.text !== '(') throw new FormulaSyntaxError('expected ( after ' + fn)
        const args: Node[] = []
        for (;;) {
          args.push(this.expr(0))
          if (args.length > MAX_ARGS) throw new FormulaSyntaxError('too many arguments')
          const c = this.next()
          if (c.kind === 'op' && c.text === ')') break
          if (c.kind !== 'op' || c.text !== ',') throw new FormulaSyntaxError('expected , or )')
        }
        if (fn === 'round' && args.length > 2) throw new FormulaSyntaxError('round takes one or two arguments')
        return { op: 'call', fn, args }
      }
      case 'op':
        if (t.text === '-') return { op: 'neg', arg: this.expr(30) }
        if (t.text === '(') {
          const n = this.expr(0)
          const c = this.next()
          if (c.kind !== 'op' || c.text !== ')') throw new FormulaSyntaxError('missing )')
          return n
        }
    }
    throw new FormulaSyntaxError('unexpected token')
  }
}

/** Parses a formula; throws FormulaSyntaxError. */
export function parse(src: string): Node {
  const p = new Parser(lex(src))
  const n = p.expr(0)
  if (p.peek().kind !== 'eof') throw new FormulaSyntaxError('unexpected input after the formula')
  return n
}

/** The lower-case field names a formula uses, in order of first use. */
export function refs(n: Node, out: string[] = []): string[] {
  switch (n.op) {
    case 'ref': {
      const name = goLower(n.ref)
      if (!out.includes(name)) out.push(name)
      break
    }
    case 'neg':
      refs(n.arg, out)
      break
    case 'call':
      for (const a of n.args) refs(a, out)
      break
    case 'num':
      break
    default:
      refs(n.left, out)
      refs(n.right, out)
  }
  return out
}

/** Computes the formula; undefined for an undefined result (division by zero, invalid round digits). */
export function evalNode(n: Node, val: (name: string) => number): number | undefined {
  switch (n.op) {
    case 'num':
      return n.num
    case 'ref':
      return val(goLower(n.ref))
    case 'neg': {
      const v = evalNode(n.arg, val)
      return v === undefined ? undefined : -v
    }
    case 'call': {
      const vals: number[] = []
      for (const a of n.args) {
        const v = evalNode(a, val)
        if (v === undefined) return undefined
        vals.push(v)
      }
      if (n.fn === 'round') {
        const digits = vals.length === 2 ? vals[1]! : 0
        if (digits < 0 || digits > 6 || digits !== Math.trunc(digits)) return undefined
        return round(vals[0]!, Math.trunc(digits))
      }
      if (n.fn === 'min' || n.fn === 'max') {
        let r = vals[0]!
        for (const v of vals.slice(1)) if ((n.fn === 'min') === (v < r)) r = v
        return r
      }
      let s = 0
      for (const v of vals) s += v
      return s
    }
  }
  const a = evalNode(n.left, val)
  const b = evalNode(n.right, val)
  if (a === undefined || b === undefined) return undefined
  switch (n.op) {
    case '+':
      return a + b
    case '-':
      return a - b
    case '*':
      return a * b
    default:
      return b === 0 ? undefined : a / b
  }
}

/** Rounds half away from zero to `digits` decimals, in the same steps as Go's Round. */
export function round(v: number, digits: number): number {
  const p = Math.pow(10, digits)
  const r = Math.floor(Math.abs(v) * p + 0.5 + 1e-9) / p
  return v < 0 ? -r : r
}

/** Beyond this magnitude a formula result is empty. */
export const MAX_MAGNITUDE = 1e15

/** Expands exponent notation ("1.5e-7", "1e+21") to plain decimal digits. */
export function plainDecimal(s: string): string {
  const m = /^(-?)(\d+)(?:\.(\d+))?e([+-]?\d+)$/i.exec(s)
  if (!m) return s
  const [, sign = '', int = '', frac = '', expText = '0'] = m
  const exp = Number(expText)
  const digits = int + frac
  const point = int.length + exp // position of the decimal point in digits
  let out: string
  if (point <= 0) out = '0.' + '0'.repeat(-point) + digits
  else if (point >= digits.length) out = digits + '0'.repeat(point - digits.length)
  else out = digits.slice(0, point) + '.' + digits.slice(point)
  out = out.replace(/^0+(?=\d)/, '')
  if (out.includes('.')) out = out.replace(/\.?0+$/, '')
  return sign + out
}

/**
 * Renders a computed number like Go's Format: "" for undefined, NaN, ±Inf or
 * |v| ≥ 1e15; otherwise rounded to two decimals, "0" for zero (never "-0"),
 * else the shortest decimal form without an exponent (strconv 'f', -1).
 */
export function format(v: number | undefined): string {
  if (v === undefined || Number.isNaN(v) || !Number.isFinite(v) || Math.abs(v) >= MAX_MAGNITUDE) return ''
  const r = round(v, 2)
  if (r === 0) return '0'
  // JS and Go both print the shortest round-tripping digits; JS switches to
  // an exponent outside [1e-6, 1e21), which plainDecimal undoes.
  return plainDecimal(String(r))
}

/**
 * Reads a field value as a number like Go's Number: trimmed, every decimal
 * comma read as a dot, parsed strictly (no partial parses); empty, invalid,
 * NaN and infinite values count as 0.
 */
export function toNumber(s: string): number {
  const n = parseGoFloat(goTrim(s).replaceAll(',', '.'))
  return n === null || Number.isNaN(n) || !Number.isFinite(n) ? 0 : n
}
