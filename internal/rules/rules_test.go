package rules

import (
	"encoding/json"
	"errors"
	"os"
	"sort"
	"strings"
	"testing"

	"github.com/go-tangra/go-tangra-signing/v4/internal/store"
)

type vectorCase struct {
	Name    string            `json:"name"`
	Fields  []store.Field     `json:"fields"`
	Values  map[string]string `json:"values"`
	Invalid string            `json:"invalid"`
	Want    struct {
		Hidden   []string          `json:"hidden"`
		Required []string          `json:"required"`
		Computed map[string]string `json:"computed"`
	} `json:"want"`
}

func loadVectors(t *testing.T) []vectorCase {
	t.Helper()
	raw, err := os.ReadFile("testdata/vectors.json")
	if err != nil {
		t.Fatal(err)
	}
	var v struct {
		Cases []vectorCase `json:"cases"`
	}
	if err := json.Unmarshal(raw, &v); err != nil {
		t.Fatal(err)
	}
	return v.Cases
}

func keys(m map[string]bool) []string {
	out := []string{}
	for k, v := range m {
		if v {
			out = append(out, k)
		}
	}
	sort.Strings(out)
	return out
}

func sorted(s []string) []string {
	out := append([]string{}, s...)
	sort.Strings(out)
	return out
}

func TestVectors(t *testing.T) {
	for _, c := range loadVectors(t) {
		t.Run(c.Name, func(t *testing.T) {
			if c.Invalid != "" {
				err := Validate(c.Fields)
				var re *Error
				if !errors.As(err, &re) || re.Field != c.Invalid || re.Error() == "" {
					t.Fatalf("Validate = %v, want refusal of %q", err, c.Invalid)
				}
				if _, err := Evaluate(c.Fields, c.Values); err == nil {
					t.Fatal("Evaluate accepted invalid rules")
				}
				return
			}
			if err := Validate(c.Fields); err != nil {
				t.Fatalf("Validate: %v", err)
			}
			res, err := Evaluate(c.Fields, c.Values)
			if err != nil {
				t.Fatal(err)
			}
			if got, want := keys(res.Hidden), sorted(c.Want.Hidden); strings.Join(got, ",") != strings.Join(want, ",") {
				t.Errorf("hidden %v, want %v", got, want)
			}
			if got, want := keys(res.Required), sorted(c.Want.Required); strings.Join(got, ",") != strings.Join(want, ",") {
				t.Errorf("required %v, want %v", got, want)
			}
			if len(res.Computed) != len(c.Want.Computed) {
				t.Errorf("computed %v, want %v", res.Computed, c.Want.Computed)
			}
			for id, want := range c.Want.Computed {
				if got, ok := res.Computed[id]; !ok || got != want {
					t.Errorf("computed[%s] = %q, want %q", id, got, want)
				}
			}
		})
	}
}

func TestDeepNestingAndLimits(t *testing.T) {
	deep := strings.Repeat("(", 40) + "1" + strings.Repeat(")", 40)
	if _, err := Parse(deep); err == nil {
		t.Fatal("deep nesting accepted")
	}
	if _, err := Parse(strings.Repeat("1+", 200) + "1"); err == nil {
		t.Fatal("too many tokens accepted")
	}
	args := "sum(" + strings.Repeat("1,", 60) + "1)"
	if _, err := Parse(args); err == nil {
		t.Fatal("too many arguments accepted")
	}
	for _, bad := range []string{"", "{}", "sum", "sum(1", "sum(1;2)", "1 2", ")", "*1", "{A}{", "-", "- *"} {
		if _, err := Parse(bad); err == nil {
			t.Errorf("Parse(%q) accepted", bad)
		}
	}
}

func TestHelpers(t *testing.T) {
	if Number(" 1,5 ") != 1.5 || Number("x") != 0 || Number("NaN") != 0 || Number("Inf") != 0 {
		t.Fatal("Number")
	}
	if Format(0, false) != "" || Format(-0.001, true) != "0" || Format(1234.567, true) != "1234.57" {
		t.Fatal("Format")
	}
	if Round(-1.235, 2) != -1.24 || Round(0.125, 2) != 0.13 {
		t.Fatal("Round")
	}
	// Unknown operators and effects never match (structure is validated elsewhere).
	if holds(store.Rule{Op: "matches"}, "x") {
		t.Fatal("unknown op")
	}
	if !matches(&store.Conditions{Mode: "all"}, nil) || matches(&store.Conditions{Mode: "any"}, nil) {
		t.Fatal("empty rule lists")
	}
}
