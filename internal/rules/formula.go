package rules

import (
	"math"
	"strconv"
	"strings"
	"unicode"
)

// Formula bounds.
const (
	maxTokens = 300
	maxDepth  = 32
	maxArgs   = 50
)

type tokKind int

const (
	tNum tokKind = iota
	tRef
	tIdent
	tOp // + - * / ( ) ,
	tEOF
)

type token struct {
	kind tokKind
	text string
	num  float64
}

func lex(src string) ([]token, error) {
	var out []token
	r := []rune(src)
	for i := 0; i < len(r); {
		c := r[i]
		switch {
		case unicode.IsSpace(c):
			i++
		case c == '{':
			j := i + 1
			for j < len(r) && r[j] != '}' && r[j] != '{' {
				j++
			}
			if j >= len(r) || r[j] != '}' {
				return nil, errSyntax("unclosed field reference")
			}
			name := strings.TrimSpace(string(r[i+1 : j]))
			if name == "" {
				return nil, errSyntax("empty field reference")
			}
			out = append(out, token{kind: tRef, text: name})
			i = j + 1
		case c >= '0' && c <= '9' || c == '.':
			j := i
			for j < len(r) && (r[j] >= '0' && r[j] <= '9' || r[j] == '.') {
				j++
			}
			n, err := strconv.ParseFloat(string(r[i:j]), 64)
			if err != nil {
				return nil, errSyntax("bad number")
			}
			out = append(out, token{kind: tNum, num: n})
			i = j
		case c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z':
			j := i
			for j < len(r) && (r[j] >= 'a' && r[j] <= 'z' || r[j] >= 'A' && r[j] <= 'Z') {
				j++
			}
			out = append(out, token{kind: tIdent, text: strings.ToLower(string(r[i:j]))})
			i = j
		case strings.ContainsRune("+-*/(),", c):
			out = append(out, token{kind: tOp, text: string(c)})
			i++
		default:
			return nil, errSyntax("unexpected character")
		}
		if len(out) > maxTokens {
			return nil, errSyntax("formula too long")
		}
	}
	return append(out, token{kind: tEOF}), nil
}

// node is a parsed formula.
type node struct {
	op   string // "num", "ref", "neg", "+", "-", "*", "/", "call"
	num  float64
	ref  string // field name as written
	fn   string
	args []*node
}

type parser struct {
	toks  []token
	pos   int
	depth int
}

func (p *parser) peek() token { return p.toks[p.pos] }
func (p *parser) next() token  { t := p.toks[p.pos]; p.pos++; return t }

func bp(op string) int {
	switch op {
	case "+", "-":
		return 10
	case "*", "/":
		return 20
	}
	return 0
}

// Parse parses a formula.
func Parse(src string) (*node, error) {
	toks, err := lex(src)
	if err != nil {
		return nil, err
	}
	p := &parser{toks: toks}
	n, err := p.expr(0)
	if err != nil {
		return nil, err
	}
	if p.peek().kind != tEOF {
		return nil, errSyntax("unexpected input after the formula")
	}
	return n, nil
}

func (p *parser) expr(minBP int) (*node, error) {
	p.depth++
	defer func() { p.depth-- }()
	if p.depth > maxDepth {
		return nil, errSyntax("formula nested too deeply")
	}
	left, err := p.prefix()
	if err != nil {
		return nil, err
	}
	for {
		t := p.peek()
		if t.kind != tOp || bp(t.text) == 0 || bp(t.text) <= minBP {
			return left, nil
		}
		p.next()
		right, err := p.expr(bp(t.text))
		if err != nil {
			return nil, err
		}
		left = &node{op: t.text, args: []*node{left, right}}
	}
}

func (p *parser) prefix() (*node, error) {
	t := p.next()
	switch t.kind {
	case tNum:
		return &node{op: "num", num: t.num}, nil
	case tRef:
		return &node{op: "ref", ref: t.text}, nil
	case tIdent:
		switch t.text {
		case "round", "min", "max", "sum":
		default:
			return nil, errSyntax("unknown function " + t.text)
		}
		if o := p.next(); o.kind != tOp || o.text != "(" {
			return nil, errSyntax("expected ( after " + t.text)
		}
		var args []*node
		for {
			a, err := p.expr(0)
			if err != nil {
				return nil, err
			}
			args = append(args, a)
			if len(args) > maxArgs {
				return nil, errSyntax("too many arguments")
			}
			o := p.next()
			if o.kind == tOp && o.text == ")" {
				break
			}
			if o.kind != tOp || o.text != "," {
				return nil, errSyntax("expected , or )")
			}
		}
		if t.text == "round" && len(args) > 2 {
			return nil, errSyntax("round takes one or two arguments")
		}
		return &node{op: "call", fn: t.text, args: args}, nil
	case tOp:
		switch t.text {
		case "-":
			a, err := p.expr(30)
			if err != nil {
				return nil, err
			}
			return &node{op: "neg", args: []*node{a}}, nil
		case "(":
			n, err := p.expr(0)
			if err != nil {
				return nil, err
			}
			if c := p.next(); c.kind != tOp || c.text != ")" {
				return nil, errSyntax("missing )")
			}
			return n, nil
		}
	}
	return nil, errSyntax("unexpected token")
}

// refs lists the field names a formula uses.
func (n *node) refs(out map[string]bool) {
	if n.op == "ref" {
		out[strings.ToLower(n.ref)] = true
	}
	for _, a := range n.args {
		a.refs(out)
	}
}

// eval computes the formula; ok is false for an undefined result (division
// by zero, overflow).
func (n *node) eval(val func(name string) float64) (float64, bool) {
	switch n.op {
	case "num":
		return n.num, true
	case "ref":
		return val(strings.ToLower(n.ref)), true
	case "neg":
		v, ok := n.args[0].eval(val)
		return -v, ok
	case "call":
		vals := make([]float64, 0, len(n.args))
		for _, a := range n.args {
			v, ok := a.eval(val)
			if !ok {
				return 0, false
			}
			vals = append(vals, v)
		}
		switch n.fn {
		case "round":
			digits := 0.0
			if len(vals) == 2 {
				digits = vals[1]
			}
			if digits < 0 || digits > 6 || digits != math.Trunc(digits) {
				return 0, false
			}
			return Round(vals[0], int(digits)), true
		case "min", "max":
			r := vals[0]
			for _, v := range vals[1:] {
				if (n.fn == "min") == (v < r) {
					r = v
				}
			}
			return r, true
		default: // sum
			s := 0.0
			for _, v := range vals {
				s += v
			}
			return s, true
		}
	}
	a, ok1 := n.args[0].eval(val)
	b, ok2 := n.args[1].eval(val)
	if !ok1 || !ok2 {
		return 0, false
	}
	switch n.op {
	case "+":
		return a + b, true
	case "-":
		return a - b, true
	case "*":
		return a * b, true
	default: // "/"
		if b == 0 {
			return 0, false
		}
		return a / b, true
	}
}

// Round rounds half away from zero to digits decimals (the TypeScript
// evaluator uses the same steps, so both give identical results).
func Round(v float64, digits int) float64 {
	p := math.Pow(10, float64(digits))
	r := math.Floor(math.Abs(v)*p+0.5+1e-9) / p
	if v < 0 {
		return -r
	}
	return r
}

// maxMagnitude bounds formula results (beyond it the output is empty).
const maxMagnitude = 1e15

// Format renders a computed number: rounded to two decimals, shortest form,
// no negative zero; "" for undefined or out-of-range results.
func Format(v float64, ok bool) string {
	if !ok || math.IsNaN(v) || math.IsInf(v, 0) || math.Abs(v) >= maxMagnitude {
		return ""
	}
	v = Round(v, 2)
	if v == 0 {
		return "0"
	}
	return strconv.FormatFloat(v, 'f', -1, 64)
}

// Number reads a field value as a number (decimal comma accepted); empty or
// non-numeric values count as 0.
func Number(s string) float64 {
	n, err := strconv.ParseFloat(strings.ReplaceAll(strings.TrimSpace(s), ",", "."), 64)
	if err != nil || math.IsNaN(n) || math.IsInf(n, 0) {
		return 0
	}
	return n
}
