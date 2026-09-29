package signing

import (
	"errors"
	"testing"

	"github.com/go-tangra/go-tangra-signing/v4/internal/apperr"
	"github.com/go-tangra/go-tangra-signing/v4/internal/store"
)

// Real conditions and formulas (package rules) in the signing pipeline.
func TestRulesInSigning(t *testing.T) {
	e := newEnv(t)
	tpl := e.tpl
	tpl.Fields = append(tpl.Fields,
		store.Field{ID: "city", Name: "City", Type: "text", Party: "employee", Page: 1, X: 0.1, Y: 0.4, W: 0.3, H: 0.03, Required: true,
			Conditions: &store.Conditions{Mode: "all", Effect: "visible", Rules: []store.Rule{{Field: "agree", Op: "checked"}}}},
		store.Field{ID: "zip", Name: "Zip", Type: "text", Party: "employee", Page: 1, X: 0.1, Y: 0.45, W: 0.3, H: 0.03,
			Conditions: &store.Conditions{Mode: "all", Effect: "required", Rules: []store.Rule{{Field: "city", Op: "eq", Value: "Varna"}}}},
		store.Field{ID: "bonus", Name: "Bonus", Type: "number", Party: "employer", Page: 1, X: 0.5, Y: 0.22, W: 0.2, H: 0.03,
			Formula: "round({Salary} * 0.1, 2)"},
	)
	if _, err := e.mem.UpdateTemplate(ctx, tpl, tpl.Version); err != nil {
		t.Fatal(err)
	}
	e.setup(t, "alice", "bob")
	d := e.submission(t, store.ModeParallel)
	a, b := slot(d, "alice"), slot(d, "bob")

	// City is required only when visible: unchecked "agree" hides it.
	in := aliceInput()
	in.Values["agree"] = "false"
	in.Uploads = map[string][]byte{}
	if _, err := e.svc.Sign(ctx, user("alice"), a, in); err != nil {
		t.Fatalf("hidden required field enforced: %v", err)
	}
	sg, _ := e.mem.GetSigner(ctx, tenant, a)
	if _, ok := sg.Values["city"]; ok {
		t.Fatal("a hidden field's value was kept")
	}

	// Bob: the bonus is computed by the server whatever the browser sends.
	res, err := e.svc.Sign(ctx, user("bob"), b, Input{PIN: "123456", Values: map[string]string{"salary": "5000", "bonus": "999999"}})
	if err != nil || res.SubmissionStatus != store.SubmissionCompleted {
		t.Fatalf("bob: %+v %v", res, err)
	}
	if sg, _ := e.mem.GetSigner(ctx, tenant, b); sg.Values["bonus"] != "500" {
		t.Fatalf("computed value %q", sg.Values["bonus"])
	}
}

func TestConditionallyRequired(t *testing.T) {
	e := newEnv(t)
	tpl := e.tpl
	tpl.Fields = append(tpl.Fields,
		store.Field{ID: "zip", Name: "Zip", Type: "text", Party: "employee", Page: 1, X: 0.1, Y: 0.45, W: 0.3, H: 0.03,
			Conditions: &store.Conditions{Mode: "any", Effect: "required", Rules: []store.Rule{{Field: "name", Op: "contains", Value: "varna"}}}})
	if _, err := e.mem.UpdateTemplate(ctx, tpl, tpl.Version); err != nil {
		t.Fatal(err)
	}
	e.setup(t, "alice")
	d := e.submission(t, store.ModeParallel)
	in := aliceInput()
	in.Values["name"] = "Мария from Varna"
	_, err := e.svc.Sign(ctx, user("alice"), slot(d, "alice"), in)
	var ae *apperr.Error
	if !errors.As(err, &ae) || ae.Reason != "missing_required" || ae.Field != "zip" {
		t.Fatalf("conditionally required: %v", err)
	}
	in.Values["zip"] = "9000"
	if _, err := e.svc.Sign(ctx, user("alice"), slot(d, "alice"), in); err != nil {
		t.Fatal(err)
	}

	// A frozen rule that became invalid reads as invalid_rule.
	if _, err := Rules([]store.Field{{ID: "x", Name: "X", Type: "number", Formula: "{Y}"}}, nil); !errors.Is(err, apperr.InvalidRule) {
		t.Fatalf("invalid frozen rule: %v", err)
	}
}
