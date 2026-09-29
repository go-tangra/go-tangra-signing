package httpapi

import (
	"strings"
	"testing"
	"time"

	"github.com/go-tangra/go-tangra-signing/v4/internal/blob"
	"github.com/go-tangra/go-tangra-signing/v4/internal/memstore"
	"github.com/go-tangra/go-tangra-signing/v4/internal/pdf/pdftest"
	"github.com/go-tangra/go-tangra-signing/v4/internal/templates"
)

func templatesAPI(t *testing.T) *Server {
	t.Helper()
	svc := templates.New(templates.Deps{Store: memstore.New(), Blob: blob.NewFake(),
		Limits: templates.Limits{MaxPDFBytes: 5 << 20, MaxPages: 50, MaxFields: 500, MaxParties: 50, ParseTimeout: 10 * time.Second}})
	return newAPI(t, Deps{Templates: svc, MaxPDFBytes: 5 << 20})
}

func upload(t *testing.T, s *Server, token, name string, pdf []byte, extra map[string]string) resp {
	t.Helper()
	fields := map[string]string{"name": name, "tags": "hr, legal"}
	for k, v := range extra {
		fields[k] = v
	}
	body, ct := multipartReq(fields, map[string][]byte{"file": pdf})
	return call(s, "POST", Prefix+"/templates", token, body, ct)
}

func TestTemplateRoutes(t *testing.T) {
	s := templatesAPI(t)
	r := upload(t, s, "admin", "Contract", pdftest.Contract(2), nil)
	expect(t, r, 201, "upload")
	tpl := r.json(t)
	id := tpl["id"].(string)
	if tpl["pdf_pages"].(float64) != 2 || tpl["file_name"] != "Договор.pdf" || tpl["status"] != "draft" || len(tpl["tags"].([]any)) != 2 {
		t.Fatalf("template = %v", tpl)
	}
	if strings.Contains(r.Body.String(), "tenants/") || strings.Contains(r.Body.String(), "sha256") {
		t.Fatal("object key or hash leaked in the response")
	}
	// The reader may read, not manage; the member may not even read.
	expect(t, call(s, "GET", Prefix+"/templates/"+id, "reader", nil, ""), 200, "reader get")
	expect(t, upload(t, s, "reader", "X", pdftest.Contract(1), nil), 403, "reader upload")
	expect(t, call(s, "GET", Prefix+"/templates", "member", nil, ""), 403, "member list")
	// Another tenant sees nothing.
	expect(t, call(s, "GET", Prefix+"/templates/"+id, "outsider", nil, ""), 404, "cross-tenant get")
	expect(t, call(s, "GET", Prefix+"/templates/"+id+"/pdf", "outsider", nil, ""), 404, "cross-tenant pdf")

	list := call(s, "GET", Prefix+"/templates?folder_id=root&tag=hr&status=draft&q=contr&page=1&page_size=10", "admin", nil, "")
	expect(t, list, 200, "list")
	if list.json(t)["total"].(float64) != 1 {
		t.Fatalf("list = %s", list.Body)
	}
	pdf := call(s, "GET", Prefix+"/templates/"+id+"/pdf", "reader", nil, "")
	expect(t, pdf, 200, "pdf")
	if pdf.Header().Get("Content-Type") != "application/pdf" || !strings.HasPrefix(pdf.Body.String(), "%PDF-") {
		t.Fatalf("pdf headers %v", pdf.Header())
	}
	det := call(s, "POST", Prefix+"/templates/"+id+"/detect-fields", "admin", nil, "")
	expect(t, det, 200, "detect")
	if len(det.json(t)["fields"].([]any)) < 2 {
		t.Fatalf("detect = %s", det.Body)
	}

	// Save fields, then patch (activate) and clone.
	fields := map[string]any{"version": tpl["version"], "parties": []map[string]any{{"key": "p1", "name": "Employee"}},
		"fields": []map[string]any{{"id": "f1", "name": "Signature", "type": "signature", "party": "p1", "page": 1, "x": 0.1, "y": 0.8, "w": 0.3, "h": 0.05}}}
	saved := callJSON(s, "PUT", Prefix+"/templates/"+id+"/fields", "admin", fields)
	expect(t, saved, 200, "save fields")
	expect(t, callJSON(s, "PUT", Prefix+"/templates/"+id+"/fields", "admin", fields), 409, "stale version")
	fields["version"] = saved.json(t)["version"]
	fields["fields"] = []map[string]any{{"id": "f1", "name": "Sig", "type": "payment", "party": "p1", "page": 1, "x": 0.1, "y": 0.1, "w": 0.1, "h": 0.1}}
	expect(t, callJSON(s, "PUT", Prefix+"/templates/"+id+"/fields", "admin", fields), 422, "unknown field type")
	act := callJSON(s, "PATCH", Prefix+"/templates/"+id, "admin", map[string]any{"status": "active", "default_expiry_days": 30,
		"default_reminder": map[string]any{"interval_days": 3, "max": 5}, "tags": []string{"hr"}})
	expect(t, act, 200, "activate")
	if a := act.json(t); a["status"] != "active" || a["default_expiry_days"].(float64) != 30 {
		t.Fatalf("activate = %v", a)
	}
	expect(t, callJSON(s, "PATCH", Prefix+"/templates/"+id, "admin", map[string]any{"default_expiry_days": nil, "default_reminder": nil,
		"folder_id": nil, "description": "d", "name": "Contract"}), 200, "clear defaults")
	expect(t, callJSON(s, "PATCH", Prefix+"/templates/"+id, "admin", map[string]any{"bogus": 1}), 422, "unknown key")
	cl := callJSON(s, "POST", Prefix+"/templates/"+id+"/clone", "admin", map[string]any{"name": "Copy"})
	expect(t, cl, 201, "clone")
	expect(t, call(s, "DELETE", Prefix+"/templates/"+cl.json(t)["id"].(string), "admin", nil, ""), 204, "delete clone")
	expect(t, call(s, "DELETE", Prefix+"/templates/"+id, "outsider", nil, ""), 404, "cross-tenant delete")

	// Refusals at upload.
	if r := upload(t, s, "admin", "Bad", []byte("hello"), nil); r.Code != 400 || r.reason() != "invalid_pdf" {
		t.Fatalf("not a pdf: %d %s", r.Code, r.Body)
	}
	if r := upload(t, s, "admin", "Big", make([]byte, 6<<20), nil); r.Code != 413 {
		t.Fatalf("too big: %d %s", r.Code, r.Body)
	}
	body, ct := multipartReq(map[string]string{"name": "No file"}, nil)
	if r := call(s, "POST", Prefix+"/templates", "admin", body, ct); r.reason() != "invalid_pdf" {
		t.Fatalf("no file: %d %s", r.Code, r.Body)
	}
	if r := upload(t, s, "admin", "contract", pdftest.Contract(1), nil); r.Code != 409 || r.reason() != "name_taken" {
		t.Fatalf("name taken: %d %s", r.Code, r.Body)
	}
	if r := upload(t, s, "admin", "Foldered", pdftest.Contract(1), map[string]string{"folder_id": "0190f7c2-6a3e-7c1a-9b2e-000000000000"}); r.Code != 404 {
		t.Fatalf("unknown folder: %d %s", r.Code, r.Body)
	}
}

func TestFolderRoutes(t *testing.T) {
	s := templatesAPI(t)
	r := callJSON(s, "POST", Prefix+"/folders", "admin", map[string]any{"name": "HR"})
	expect(t, r, 201, "create root")
	hr := r.json(t)["id"].(string)
	r = callJSON(s, "POST", Prefix+"/folders", "admin", map[string]any{"name": "Contracts", "parent_id": hr, "sort_order": 1})
	expect(t, r, 201, "create child")
	child := r.json(t)["id"].(string)
	expect(t, callJSON(s, "POST", Prefix+"/folders", "reader", map[string]any{"name": "X"}), 403, "reader create")
	list := call(s, "GET", Prefix+"/folders", "reader", nil, "")
	expect(t, list, 200, "list")
	if items := list.json(t)["items"].([]any); len(items) != 2 || items[1].(map[string]any)["path"] != "/HR/Contracts" {
		t.Fatalf("folders = %s", list.Body)
	}
	expect(t, callJSON(s, "PATCH", Prefix+"/folders/"+child, "admin", map[string]any{"parent_id": nil}), 200, "move to root")
	expect(t, callJSON(s, "PATCH", Prefix+"/folders/"+hr, "admin", map[string]any{"name": "People"}), 200, "rename")
	expect(t, callJSON(s, "PATCH", Prefix+"/folders/"+hr, "outsider", map[string]any{"name": "x"}), 404, "cross-tenant")
	up := upload(t, s, "admin", "In folder", pdftest.Contract(1), map[string]string{"folder_id": hr})
	expect(t, up, 201, "upload into folder")
	if r := call(s, "DELETE", Prefix+"/folders/"+hr, "admin", nil, ""); r.Code != 409 || r.reason() != "folder_not_empty" {
		t.Fatalf("delete non-empty: %d %s", r.Code, r.Body)
	}
	expect(t, call(s, "DELETE", Prefix+"/folders/"+child, "admin", nil, ""), 204, "delete empty")
	expect(t, call(s, "GET", Prefix+"/folders", "", nil, ""), 401, "anonymous")
}
