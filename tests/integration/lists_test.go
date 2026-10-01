//go:build integration

package integration

import (
	"fmt"
	"strings"
	"testing"

	"github.com/go-tangra/go-tangra-signing/v4/internal/repo/repodb"
	"github.com/go-tangra/go-tangra-signing/v4/internal/repo/repotest"
)

// T060: every list, every sort field × direction, pages exactly once over
// TimescaleDB under RLS; inbox join stable; backup export walk pins id order.
func TestListsSortAndPage(t *testing.T) {
	e := newEnv(t)
	repotest.Lists(t, repodb.New(e.st))
}

// T060/T061 over HTTP: the app answers the list contract (Page shape, 422
// naming the parameter, totals per tenant, clamp) through the OpenAPI
// validator and the handlers.
func TestListsHTTPContract(t *testing.T) {
	e := newEnv(t)
	db := repodb.New(e.st)
	for i, n := range []string{"beta", "Alpha", "gamma", "delta", "Epsilon"} {
		tp := repotest.Template(tenantA, fmt.Sprintf("%s%d", n, i))
		if err := db.CreateTemplate(ctx, tp); err != nil {
			t.Fatal(err)
		}
	}
	if err := db.CreateTemplate(ctx, repotest.Template(tenantB, "other")); err != nil {
		t.Fatal(err)
	}
	r := e.call("GET", "/api/signing/v1/templates?sort=name&order=asc&page=2&page_size=2", "adminA", nil, "")
	expect(t, r, 200, "templates by name")
	m := r.json(t)
	if m["total"] != float64(5) || m["page"] != float64(2) || m["page_size"] != float64(2) || m["sort"] != "name" || m["order"] != "asc" {
		t.Fatalf("page shape %s", r.Body)
	}
	var got []string
	for _, it := range m["items"].([]any) {
		got = append(got, it.(map[string]any)["name"].(string))
	}
	if strings.Join(got, ",") != "delta3,Epsilon4" {
		t.Fatalf("page 2 by name: %v", got)
	}
	r = e.call("GET", "/api/signing/v1/templates?sort=name&page=9&page_size=2", "adminA", nil, "")
	expect(t, r, 200, "clamped")
	if m := r.json(t); m["page"] != float64(3) || len(m["items"].([]any)) != 1 {
		t.Fatalf("clamp %s", r.Body)
	}
	r = e.call("GET", "/api/signing/v1/templates", "adminB", nil, "")
	expect(t, r, 200, "tenant B")
	if m := r.json(t); m["total"] != float64(1) {
		t.Fatalf("tenant B total %s", r.Body)
	}
	for _, path := range []string{"/templates", "/submissions", "/certificates", "/inbox"} {
		for q, param := range map[string]string{"sort=pdf_key": "sort", "order=up": "order", "page_size=201": "page_size", "page=0": "page"} {
			r := e.call("GET", "/api/signing/v1"+path+"?"+q, "adminA", nil, "")
			if r.Code != 422 || !strings.Contains(r.Body.String(), `"param":"`+param+`"`) || strings.Contains(r.Body.String(), "pdf_key") {
				t.Fatalf("%s?%s: %d %s", path, q, r.Code, r.Body)
			}
		}
	}
	for path, sorts := range map[string][]string{
		"/submissions":  {"title", "status", "created_at", "completed_at"},
		"/certificates": {"subject", "kind", "status", "not_after", "created_at"},
		"/inbox":        {"created_at", "title", "status"},
	} {
		for _, s := range sorts {
			expect(t, e.call("GET", "/api/signing/v1"+path+"?sort="+s+"&order=desc", "adminA", nil, ""), 200, path+" "+s)
		}
	}
}
