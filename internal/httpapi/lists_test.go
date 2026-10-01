package httpapi

import (
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-tangra/go-tangra/v4/listquery"

	"github.com/go-tangra/go-tangra-signing/v4/internal/pdf/pdftest"
	"github.com/go-tangra/go-tangra-signing/v4/internal/store"
)

// listNegatives are the paging refusals of contracts/http-list.md.
var listNegatives = map[string]string{
	"sort=bogus_col":      "sort",
	"order=up":            "order",
	"page_size=201":       "page_size",
	"page_size=0":         "page_size",
	"page_size=abc":       "page_size",
	"page=0":              "page",
	"page=-1":             "page",
	"page=abc":            "page",
	"page=1&sort=pdf_key": "sort",
}

func expectParam(t *testing.T, r resp, param, what string) {
	t.Helper()
	if r.Code != 422 || r.reason() != "validation_failed" {
		t.Fatalf("%s: %d %s", what, r.Code, r.Body)
	}
	d, _ := r.json(t)["detail"].(map[string]any)
	if d["param"] != param {
		t.Fatalf("%s: detail %s", what, r.Body)
	}
	for _, leak := range []string{"bogus_col", "pdf_key", "abc", "up\""} {
		if strings.Contains(r.Body.String(), leak) {
			t.Fatalf("%s: submitted value echoed: %s", what, r.Body)
		}
	}
}

func expectPage(t *testing.T, r resp, page, size int, sort, order string, what string) map[string]any {
	t.Helper()
	expect(t, r, 200, what)
	m := r.json(t)
	if _, ok := m["items"].([]any); !ok {
		t.Fatalf("%s: items %s", what, r.Body)
	}
	if m["page"] != float64(page) || m["page_size"] != float64(size) || m["sort"] != sort || m["order"] != order {
		t.Fatalf("%s: page shape %s", what, r.Body)
	}
	if _, ok := m["total"].(float64); !ok {
		t.Fatalf("%s: total %s", what, r.Body)
	}
	return m
}

func names(m map[string]any, key string) []string {
	var out []string
	for _, it := range m["items"].([]any) {
		out = append(out, it.(map[string]any)[key].(string))
	}
	return out
}

func TestListNegatives(t *testing.T) {
	tpl := templatesAPI(t)
	subs := subsAPI(t)
	adm := adminAPI(t)
	for _, c := range []struct {
		s    *Server
		path string
		tok  string
	}{
		{tpl, "/templates", "admin"},
		{subs.s, "/submissions", "admin"},
		{subs.s, "/inbox", "member"},
		{adm, "/certificates", "admin"},
	} {
		for q, param := range listNegatives {
			expectParam(t, call(c.s, "GET", Prefix+c.path+"?"+q, c.tok, nil, ""), param, c.path+"?"+q)
		}
	}
}

func TestTemplateListSortAndPage(t *testing.T) {
	s := templatesAPI(t)
	for _, n := range []string{"beta", "Alpha", "gamma"} {
		expect(t, upload(t, s, "admin", n, pdftest.Contract(1), nil), 201, "upload "+n)
	}
	// Default: updated_at desc (newest first).
	m := expectPage(t, call(s, "GET", Prefix+"/templates", "admin", nil, ""), 1, 25, "updated_at", "desc", "default")
	if got := names(m, "name"); len(got) != 3 || got[0] != "gamma" {
		t.Fatalf("default order %v", got)
	}
	// Text fields compare case-insensitively; the field's default direction applies.
	m = expectPage(t, call(s, "GET", Prefix+"/templates?sort=name", "admin", nil, ""), 1, 25, "name", "asc", "by name")
	if got := strings.Join(names(m, "name"), ","); got != "Alpha,beta,gamma" {
		t.Fatalf("name asc %s", got)
	}
	m = expectPage(t, call(s, "GET", Prefix+"/templates?sort=name&order=desc&page=2&page_size=2", "admin", nil, ""), 2, 2, "name", "desc", "page 2")
	if got := strings.Join(names(m, "name"), ","); got != "Alpha" || m["total"] != float64(3) {
		t.Fatalf("name desc page 2 %s", got)
	}
	// Beyond the last page answers the last page.
	m = expectPage(t, call(s, "GET", Prefix+"/templates?sort=name&page=99&page_size=2", "admin", nil, ""), 2, 2, "name", "asc", "clamp")
	if got := strings.Join(names(m, "name"), ","); got != "gamma" {
		t.Fatalf("clamped page %s", got)
	}
	// No match: page 1, no items.
	m = expectPage(t, call(s, "GET", Prefix+"/templates?q=zzz&page=5", "admin", nil, ""), 1, 25, "updated_at", "desc", "empty")
	if len(m["items"].([]any)) != 0 || m["total"] != float64(0) {
		t.Fatalf("empty %v", m)
	}
}

func TestSubmissionAndInboxListShape(t *testing.T) {
	e := subsAPI(t)
	e.create(t)
	e.create(t)
	m := expectPage(t, call(e.s, "GET", Prefix+"/submissions?sort=title&order=asc&page_size=1", "admin", nil, ""), 1, 1, "title", "asc", "submissions")
	if m["total"] != float64(2) || len(m["items"].([]any)) != 1 {
		t.Fatalf("submissions %v", m)
	}
	expectPage(t, call(e.s, "GET", Prefix+"/submissions?sort=completed_at", "admin", nil, ""), 1, 25, "completed_at", "desc", "completed_at")
	expectPage(t, call(e.s, "GET", Prefix+"/inbox?state=signed&sort=status", "member", nil, ""), 1, 25, "status", "asc", "inbox")
	expectPage(t, call(e.s, "GET", Prefix+"/inbox", "member", nil, ""), 1, 25, "created_at", "desc", "inbox default")
}

func TestCertificateListShape(t *testing.T) {
	s := adminAPI(t)
	for _, cn := range []string{"Zeta", "alpha"} {
		expect(t, callJSON(s, "POST", Prefix+"/certificates", "admin", map[string]any{"subject_cn": cn, "validity_years": 3}), 201, "create "+cn)
	}
	m := expectPage(t, call(s, "GET", Prefix+"/certificates?kind=admin&sort=subject", "admin", nil, ""), 1, 25, "subject", "asc", "by subject")
	if got := strings.Join(names(m, "subject_cn"), ","); got != "alpha,Zeta" {
		t.Fatalf("subject asc %s", got)
	}
	for _, f := range []string{"kind", "status", "not_after", "created_at"} {
		expectPage(t, call(s, "GET", Prefix+"/certificates?sort="+f+"&order=asc", "admin", nil, ""), 1, 25, f, "asc", f)
	}
}

// parseList is the handler-level guard for callers that bypass the OpenAPI
// validator (defence in depth).
func TestParseListDirect(t *testing.T) {
	for q, param := range listNegatives {
		w := httptest.NewRecorder()
		if _, ok := parseList(w, httptest.NewRequest("GET", "/x?"+q, nil), store.TemplateList); ok {
			t.Fatalf("%s accepted", q)
		}
		expectParam(t, resp{w}, param, "direct "+q)
	}
	w := httptest.NewRecorder()
	req, ok := parseList(w, httptest.NewRequest("GET", "/x?sort=status", nil), store.TemplateList)
	if !ok || req.Sort != "status" || req.Order != listquery.Asc || req.PageSize != listquery.DefaultPageSize {
		t.Fatalf("valid request %+v", req)
	}
	p := listPage([]int(nil), 0, listquery.Request{Page: 3, PageSize: 10, Sort: "name", Order: listquery.Asc})
	if p.Items == nil || p.Page != 1 {
		t.Fatalf("empty page %+v", p)
	}
}
