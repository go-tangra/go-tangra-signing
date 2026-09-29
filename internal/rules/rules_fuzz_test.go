package rules

import (
	"testing"

	"github.com/go-tangra/go-tangra-signing/v4/internal/store"
)

// Template authors' formulas and signers' values never panic.
func FuzzFormula(f *testing.F) {
	for _, s := range []string{"{A} + {B} * 2", "round({A}/3, 2)", "sum(1,2,3)", "-(-{A})", "{A", "max(", "1.2.3"} {
		f.Add(s, "5", "0")
	}
	f.Fuzz(func(t *testing.T, formula, a, b string) {
		fields := []store.Field{
			{ID: "a", Name: "A", Type: "number"}, {ID: "b", Name: "B", Type: "number"},
			{ID: "t", Name: "T", Type: "number", Formula: formula},
		}
		if Validate(fields) != nil {
			return
		}
		res, err := Evaluate(fields, map[string]string{"a": a, "b": b})
		if err != nil {
			t.Fatalf("valid rules failed to evaluate: %v", err)
		}
		_ = res.Computed["t"]
	})
}
