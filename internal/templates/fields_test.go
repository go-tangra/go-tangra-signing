package templates

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/go-tangra/go-tangra-signing/v4/internal/apperr"
	"github.com/go-tangra/go-tangra-signing/v4/internal/store"
)

var twoParties = []store.Party{{Key: "p1", Name: "Employee"}, {Key: "p2", Name: "Employer"}}

func field(id, name, typ, party string) store.Field {
	return store.Field{ID: id, Name: name, Type: typ, Party: party, Page: 1, X: 0.1, Y: 0.1, W: 0.2, H: 0.05}
}

func TestValidatePartiesAndAllTypes(t *testing.T) {
	if err := ValidateParties(twoParties, 50); err != nil {
		t.Fatal(err)
	}
	for name, p := range map[string][]store.Party{
		"none":     nil,
		"too many": {{Key: "a", Name: "A"}, {Key: "b", Name: "B"}},
		"bad key":  {{Key: "P 1", Name: "x"}},
		"no name":  {{Key: "p1", Name: " "}},
		"dup key":  {{Key: "p1", Name: "A"}, {Key: "p1", Name: "B"}},
		"dup name": {{Key: "p1", Name: "A"}, {Key: "p2", Name: "a"}},
		"long":     {{Key: "p1", Name: strings.Repeat("x", 81)}},
	} {
		max := 50
		if name == "too many" {
			max = 1
		}
		if err := ValidateParties(p, max); !errors.Is(err, apperr.InvalidField) {
			t.Errorf("%s: %v", name, err)
		}
	}
	var fields []store.Field
	i := 0
	for typ := range FieldTypes {
		i++
		f := field("f"+string(rune('a'+i)), "Field "+typ, typ, "p1")
		if typ == "select" || typ == "radio" {
			f.Options, f.Default = []string{"A", "B"}, "B"
		}
		if typ == "number" {
			f.Formula = "{Qty} * 2"
		}
		fields = append(fields, f)
	}
	if err := ValidateFields(fields, twoParties, 1, 500); err != nil {
		t.Fatal(err)
	}
}

func TestValidateFieldsRefusals(t *testing.T) {
	base := func() store.Field { return field("f1", "Salary", "text", "p1") }
	cases := map[string]func(*store.Field){
		"bad id":           func(f *store.Field) { f.ID = "has space" },
		"no name":          func(f *store.Field) { f.Name = " " },
		"long name":        func(f *store.Field) { f.Name = strings.Repeat("n", 121) },
		"type":             func(f *store.Field) { f.Type = "payment" },
		"party":            func(f *store.Field) { f.Party = "p9" },
		"page 0":           func(f *store.Field) { f.Page = 0 },
		"page beyond":      func(f *store.Field) { f.Page = 3 },
		"negative x":       func(f *store.Field) { f.X = -0.1 },
		"negative y":       func(f *store.Field) { f.Y = -0.1 },
		"zero width":       func(f *store.Field) { f.W = 0 },
		"zero height":      func(f *store.Field) { f.H = 0 },
		"off right":        func(f *store.Field) { f.X, f.W = 0.9, 0.2 },
		"off bottom":       func(f *store.Field) { f.Y, f.H = 0.99, 0.05 },
		"font size":        func(f *store.Field) { f.FontSize = 100 },
		"negative size":    func(f *store.Field) { f.FontSize = -1 },
		"font name":        func(f *store.Field) { f.Font = strings.Repeat("f", 41) },
		"default":          func(f *store.Field) { f.Default = strings.Repeat("d", 2001) },
		"select no opts":   func(f *store.Field) { f.Type = "select" },
		"select dup opt":   func(f *store.Field) { f.Type, f.Options = "select", []string{"A", "A"} },
		"select empty opt": func(f *store.Field) { f.Type, f.Options = "radio", []string{" "} },
		"default not opt":  func(f *store.Field) { f.Type, f.Options, f.Default = "select", []string{"A"}, "B" },
		"opts on text":     func(f *store.Field) { f.Options = []string{"A"} },
		"formula on text":  func(f *store.Field) { f.Formula = "1+1" },
		"long formula":     func(f *store.Field) { f.Type, f.Formula = "number", strings.Repeat("1", 501) },
		"too many opts":    func(f *store.Field) { f.Type, f.Options = "select", make([]string, 51) },
	}
	for name, mut := range cases {
		f := base()
		mut(&f)
		err := ValidateFields([]store.Field{f}, twoParties, 2, 500)
		if !errors.Is(err, apperr.InvalidField) {
			t.Errorf("%s: %v", name, err)
		}
	}
	dup := []store.Field{base(), field("f2", "salary", "text", "p1")}
	if err := ValidateFields(dup, twoParties, 1, 500); !errors.Is(err, apperr.InvalidField) {
		t.Fatalf("duplicate name: %v", err)
	}
	if err := ValidateFields([]store.Field{base(), base()}, twoParties, 1, 1); !errors.Is(err, apperr.InvalidField) {
		t.Fatalf("too many fields: %v", err)
	}
	// Refusals name the offending field.
	f := base()
	f.Page = 9
	if e, _ := apperr.As(ValidateFields([]store.Field{f}, twoParties, 1, 10)); e == nil || e.Field != "Salary" {
		t.Fatalf("field label: %+v", e)
	}
	f = base()
	f.Name, f.ID = "", strings.Repeat("i", 65)
	if e, _ := apperr.As(ValidateFields([]store.Field{f}, twoParties, 1, 10)); e == nil || e.Field != "fields" {
		t.Fatalf("fallback label: %+v", e)
	}
}

func TestValidateConditions(t *testing.T) {
	married := field("married", "Married", "checkbox", "p1")
	spouse := field("spouse", "Spouse name", "text", "p1")
	spouse.Conditions = &store.Conditions{Mode: "all", Effect: "visible", Rules: []store.Rule{{Field: "married", Op: "checked"}}}
	if err := ValidateFields([]store.Field{married, spouse}, twoParties, 1, 10); err != nil {
		t.Fatal(err)
	}
	cases := map[string]store.Conditions{
		"mode":     {Mode: "some", Effect: "visible", Rules: spouse.Conditions.Rules},
		"effect":   {Mode: "all", Effect: "hidden", Rules: spouse.Conditions.Rules},
		"no rules": {Mode: "all", Effect: "visible"},
		"op":       {Mode: "all", Effect: "visible", Rules: []store.Rule{{Field: "married", Op: "like"}}},
		"unknown":  {Mode: "all", Effect: "visible", Rules: []store.Rule{{Field: "ghost", Op: "checked"}}},
		"self":     {Mode: "all", Effect: "visible", Rules: []store.Rule{{Field: "spouse", Op: "empty"}}},
		"long val": {Mode: "any", Effect: "required", Rules: []store.Rule{{Field: "married", Op: "eq", Value: strings.Repeat("v", 501)}}},
		"too many": {Mode: "any", Effect: "required", Rules: make([]store.Rule, 21)},
	}
	for name, c := range cases {
		s := spouse
		cc := c
		s.Conditions = &cc
		if err := ValidateFields([]store.Field{married, s}, twoParties, 1, 10); !errors.Is(err, apperr.InvalidRule) {
			t.Errorf("%s: %v", name, err)
		}
	}
}

func TestReadyToActivateAndTags(t *testing.T) {
	tpl := store.Template{Parties: twoParties}
	if err := ReadyToActivate(tpl); !errors.Is(err, apperr.InvalidField) {
		t.Fatal("no fields activated")
	}
	tpl.Fields = []store.Field{field("f1", "A", "signature", "p1")}
	if e, _ := apperr.As(ReadyToActivate(tpl)); e == nil || e.Field != "Employer" {
		t.Fatalf("party without field: %+v", e)
	}
	tpl.Fields = append(tpl.Fields, field("f2", "B", "signature", "p2"))
	if err := ReadyToActivate(tpl); err != nil {
		t.Fatal(err)
	}
	tags, err := NormalizeTags([]string{" hr ", "HR", "", "legal"})
	if err != nil || len(tags) != 2 || tags[0] != "hr" || tags[1] != "legal" {
		t.Fatalf("tags = %v %v", tags, err)
	}
	for _, bad := range [][]string{{strings.Repeat("t", 41)}, {"a,b"}, make21()} {
		if _, err := NormalizeTags(bad); !errors.Is(err, apperr.Validation) {
			t.Errorf("tags %v accepted", bad)
		}
	}
}

func make21() []string {
	out := make([]string, 21)
	for i := range out {
		out[i] = "t" + string(rune('a'+i))
	}
	return out
}

// FuzzValidateFields: arbitrary builder JSON never panics, and anything
// accepted stays inside the page and refers to existing parties and fields.
func FuzzValidateFields(f *testing.F) {
	f.Add([]byte(`[{"id":"f1","name":"A","type":"text","party":"p1","page":1,"x":0.1,"y":0.1,"w":0.2,"h":0.1}]`))
	f.Add([]byte(`[{"id":"f1","name":"A","type":"select","party":"p2","page":2,"x":0,"y":0,"w":1,"h":1,"options":["x"]}]`))
	f.Fuzz(func(t *testing.T, data []byte) {
		var fields []store.Field
		if json.Unmarshal(data, &fields) != nil {
			return
		}
		if ValidateFields(fields, twoParties, 2, 50) != nil {
			return
		}
		for _, fl := range fields {
			if fl.X < 0 || fl.Y < 0 || fl.X+fl.W > 1+eps || fl.Y+fl.H > 1+eps || fl.Page < 1 || fl.Page > 2 || (fl.Party != "p1" && fl.Party != "p2") {
				t.Fatalf("accepted invalid field %+v", fl)
			}
		}
	})
}
