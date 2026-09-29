// Package rules evaluates field conditions and formulas (US6, research D8):
// a small restricted grammar instead of a general expression engine.
//
// Conditions make a field visible or required when all/any of their rules
// hold (eq, neq, contains, empty, not_empty, checked, unchecked) over other
// fields' values. Formulas compute number fields from numbers, field
// references {Field name}, + - * /, parentheses and round/min/max/sum.
//
// Everything is evaluated in one dependency order: a hidden field counts as
// empty for the fields that depend on it, a formula sees the values of the
// formulas it references. Invalid rules, unknown fields and cycles are
// refused at save time (Validate). The TypeScript evaluator in the UI
// implements the same steps and is checked against the same test vectors
// (testdata/vectors.json); the server's results are authoritative.
package rules

import (
	"sort"
	"strings"

	"github.com/go-tangra/go-tangra-signing/v4/internal/store"
)

// Error is a refused rule, naming the field it belongs to.
type Error struct {
	Field string // field name
	Msg   string
}

func (e *Error) Error() string { return "rules: " + e.Field + ": " + e.Msg }

type syntaxError string

func (e syntaxError) Error() string { return string(e) }

func errSyntax(msg string) error { return syntaxError(msg) }

// Result is the evaluation of a submission's fields.
type Result struct {
	Hidden   map[string]bool   // field id → not shown
	Required map[string]bool   // field id → required now
	Computed map[string]string // field id → formula value ("" when undefined)
}

type graph struct {
	fields  []store.Field
	byName  map[string]int // lower-case trimmed name → index
	byID    map[string]int
	formula map[int]*node
	deps    map[int][]int
}

func build(fields []store.Field) (*graph, error) {
	g := &graph{fields: fields, byName: map[string]int{}, byID: map[string]int{}, formula: map[int]*node{}, deps: map[int][]int{}}
	for i, f := range fields {
		g.byName[strings.ToLower(strings.TrimSpace(f.Name))] = i
		g.byID[f.ID] = i
	}
	for i, f := range fields {
		seen := map[int]bool{}
		add := func(j int) {
			if !seen[j] {
				seen[j] = true
				g.deps[i] = append(g.deps[i], j)
			}
		}
		if f.Formula != "" {
			if f.Type != "number" {
				return nil, &Error{Field: f.Name, Msg: "only number fields take a formula"}
			}
			n, err := Parse(f.Formula)
			if err != nil {
				return nil, &Error{Field: f.Name, Msg: err.Error()}
			}
			refs := map[string]bool{}
			n.refs(refs)
			for name := range refs {
				j, ok := g.byName[name]
				if !ok {
					return nil, &Error{Field: f.Name, Msg: "unknown field {" + name + "}"}
				}
				if j == i {
					return nil, &Error{Field: f.Name, Msg: "a formula cannot use its own field"}
				}
				add(j)
			}
			g.formula[i] = n
		}
		if c := f.Conditions; c != nil {
			for _, r := range c.Rules {
				j, ok := g.byID[r.Field]
				if !ok || j == i {
					return nil, &Error{Field: f.Name, Msg: "condition on an unknown field"}
				}
				add(j)
			}
		}
	}
	return g, nil
}

// order returns the fields in dependency order, or the name of a field on a
// cycle.
func (g *graph) order() ([]int, string) {
	state := make([]int, len(g.fields)) // 0 new, 1 visiting, 2 done
	var out []int
	var cyc string
	var visit func(i int) bool
	visit = func(i int) bool {
		switch state[i] {
		case 1:
			cyc = g.fields[i].Name
			return false
		case 2:
			return true
		}
		state[i] = 1
		deps := append([]int(nil), g.deps[i]...)
		sort.Ints(deps)
		for _, j := range deps {
			if !visit(j) {
				return false
			}
		}
		state[i] = 2
		out = append(out, i)
		return true
	}
	for i := range g.fields {
		if !visit(i) {
			return nil, cyc
		}
	}
	return out, ""
}

// Validate refuses syntax errors, unknown fields and cycles.
func Validate(fields []store.Field) error {
	g, err := build(fields)
	if err != nil {
		return err
	}
	if _, cyc := g.order(); cyc != "" {
		return &Error{Field: cyc, Msg: "rules form a cycle"}
	}
	return nil
}

// Evaluate computes visibility, requiredness and formula values over values
// (field id → value). Values of hidden fields are ignored.
func Evaluate(fields []store.Field, values map[string]string) (Result, error) {
	g, err := build(fields)
	if err != nil {
		return Result{}, err
	}
	ord, cyc := g.order()
	if cyc != "" {
		return Result{}, &Error{Field: cyc, Msg: "rules form a cycle"}
	}
	res := Result{Hidden: map[string]bool{}, Required: map[string]bool{}, Computed: map[string]string{}}
	cur := map[string]string{} // effective values (hidden → "")
	for _, i := range ord {
		f := fields[i]
		visible, required := true, f.Required
		if c := f.Conditions; c != nil && len(c.Rules) > 0 {
			match := matches(c, cur)
			if c.Effect == "visible" {
				visible = match
			} else {
				required = required || match
			}
		}
		if !visible {
			res.Hidden[f.ID] = true
			cur[f.ID] = ""
			continue
		}
		res.Required[f.ID] = required
		v := values[f.ID]
		if n, ok := g.formula[i]; ok {
			v = Format(n.eval(func(name string) float64 { return Number(cur[fields[g.byName[name]].ID]) }))
			res.Computed[f.ID] = v
		}
		cur[f.ID] = v
	}
	return res, nil
}

func matches(c *store.Conditions, cur map[string]string) bool {
	all := c.Mode != "any"
	for _, r := range c.Rules {
		ok := holds(r, cur[r.Field])
		if all && !ok {
			return false
		}
		if !all && ok {
			return true
		}
	}
	return all
}

func holds(r store.Rule, v string) bool {
	v = strings.TrimSpace(v)
	switch r.Op {
	case "eq":
		return v == strings.TrimSpace(r.Value)
	case "neq":
		return v != strings.TrimSpace(r.Value)
	case "contains":
		return strings.Contains(strings.ToLower(v), strings.ToLower(strings.TrimSpace(r.Value)))
	case "empty":
		return v == ""
	case "not_empty":
		return v != ""
	case "checked":
		return v == "true"
	case "unchecked":
		return v != "true"
	}
	return false
}
