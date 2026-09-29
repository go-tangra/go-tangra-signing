//go:build integration

package integration

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/go-tangra/go-tangra-signing/v4/internal/httpapi"
	"github.com/go-tangra/go-tangra-signing/v4/internal/pdf/pdftest"
)

// T093 / SC-004: tenant B (with every permission) gets "not found" on every
// route that names a tenant-A object, and its listings show none of them.
func TestTenantIsolation(t *testing.T) {
	e := newEnv(t)
	f := e.setupFlow(t, "sequential", map[string]string{"salary": "5000"})
	e.setupCert(t, "aliceA", "123456")
	expect(t, e.sign("aliceA", f.aliceSlot, "123456", map[string]string{"name": "Alice"}), 200, "alice signs")
	r := e.json("POST", "/api/signing/v1/folders", "adminA", map[string]any{"name": "Secret folder"})
	expect(t, r, 201, "folder")
	folder := r.json(t)["id"].(string)
	r = e.json("POST", "/api/signing/v1/certificates", "adminA", map[string]any{"subject_cn": "A admin"})
	expect(t, r, 201, "admin certificate")
	cert := r.json(t)["id"].(string)
	body, ct := multipartBody(map[string]string{"certificate_id": cert}, map[string][]byte{"file": pdftest.Contract(1)})
	r = e.call("POST", "/api/signing/v1/documents/sign", "adminA", body, ct)
	expect(t, r, 201, "document")
	docID := r.json(t)["id"].(string)
	aIDs := []string{f.template, f.submission, f.aliceSlot, f.bobSlot, folder, cert, docID}

	ids := map[string]string{"/folders/{id}": folder, "/templates/{id}": f.template, "/submissions/{id}": f.submission,
		"/certificates/{id}": cert, "/documents/{id}": docID}
	bodies := map[string]any{
		"updateFolder": map[string]any{"name": "x"}, "updateTemplate": map[string]any{"name": "x"},
		"saveTemplateFields": map[string]any{"version": 1, "parties": []map[string]any{{"key": "p", "name": "P"}}, "fields": []any{}},
		"cloneTemplate":      map[string]any{"name": "x"}, "cancelSubmission": map[string]any{"reason": "x"},
		"replaceSigner": map[string]any{"user_id": "memberB"}, "decline": map[string]any{"reason": "x"},
		"completeQES":       map[string]any{"preparation_id": f.aliceSlot, "signature_b64": "AAAA"},
		"prepareQES":        map[string]any{"values": map[string]string{}, "chain": []string{"AAAA"}},
		"revokeCertificate": map[string]any{"reason": "unspecified"},
	}
	// Validation that runs before any lookup may answer 422; it never reveals data.
	validationFirst := map[string]bool{"prepareQES": true}

	doc, err := httpapi.LoadDocument()
	if err != nil {
		t.Fatal(err)
	}
	checked := 0
	for p, item := range doc.Paths.Map() {
		if !strings.Contains(p, "{") {
			continue
		}
		for method, op := range item.Operations() {
			path := strings.TrimPrefix(p, "/api/signing/v1")
			for prefix, id := range ids {
				path = strings.Replace(path, prefix, strings.Replace(prefix, "{id}", id, 1), 1)
			}
			path = strings.ReplaceAll(path, "{sid}", f.aliceSlot)
			path = strings.ReplaceAll(path, "{signer_id}", f.aliceSlot)
			if strings.Contains(path, "{") {
				t.Errorf("%s %s: unmapped parameter", method, p)
				continue
			}
			var res resp
			switch {
			case op.OperationID == "sign":
				b, ct := multipartBody(map[string]string{"values": "{}", "pin": "123456"}, nil)
				res = e.call(method, "/api/signing/v1"+path, "adminB", b, ct)
			case bodies[op.OperationID] != nil:
				res = e.json(method, "/api/signing/v1"+path, "adminB", bodies[op.OperationID])
			default:
				res = e.call(method, "/api/signing/v1"+path, "adminB", nil, "")
			}
			checked++
			if res.Code == 404 || (res.Code == 422 && validationFirst[op.OperationID]) {
				continue
			}
			t.Errorf("%s %s (%s): tenant B got %d: %s", method, path, op.OperationID, res.Code, res.Body)
		}
	}
	if checked < 25 {
		t.Fatalf("only %d parameterised routes checked", checked)
	}

	// Listings and exports of tenant B show nothing of tenant A.
	for _, path := range []string{"/templates", "/folders", "/submissions", "/inbox", "/certificates", "/users"} {
		r := e.call("GET", "/api/signing/v1"+path, "adminB", nil, "")
		expect(t, r, 200, "list "+path)
		for _, id := range aIDs {
			if strings.Contains(r.Body.String(), id) {
				t.Errorf("%s of tenant B lists tenant-A id %s", path, id)
			}
		}
	}
	vb, vct := multipartBody(map[string]string{"submission_id": f.submission}, nil)
	expect(t, e.call("POST", "/api/signing/v1/verify", "adminB", vb, vct), 404, "verify a tenant-A submission")
	expect(t, e.call("POST", "/api/signing/v1/backup/export?tenant_id="+tenantA, "adminB", nil, ""), 403, "export tenant A")
	r = e.call("POST", "/api/signing/v1/backup/export", "adminB", nil, "")
	expect(t, r, 200, "export tenant B")
	for _, id := range aIDs {
		if bytes.Contains(r.Body.Bytes(), []byte(id)) {
			t.Errorf("tenant B's backup contains %s", id)
		}
	}
	// A tenant-A member who is not a signer cannot open the other's slot either.
	expect(t, e.call("GET", "/api/signing/v1/signing/"+f.bobSlot, "aliceA", nil, ""), 404, "another signer's slot")
	// Out-of-order and post-cancel signings are refused.
	e.setupCert(t, "bobA", "654321")
	f2 := e.setupFlow(t, "sequential", nil)
	if r := e.sign("bobA", f2.bobSlot, "654321", map[string]string{}); r.Code != 409 || !strings.Contains(r.Body.String(), "not_your_turn") {
		t.Fatalf("out of order: %d %s", r.Code, r.Body)
	}
	expect(t, e.json("POST", "/api/signing/v1/submissions/"+f2.submission+"/cancel", "adminA", map[string]any{"reason": "stop"}), 200, "cancel")
	if r := e.sign("aliceA", f2.aliceSlot, "123456", map[string]string{"name": "A"}); r.Code != 409 {
		t.Fatalf("post-cancel signing: %d %s", r.Code, r.Body)
	}
	// A wrong PIN never signs.
	f3 := e.setupFlow(t, "parallel", nil)
	if r := e.sign("aliceA", f3.aliceSlot, "000000", map[string]string{"name": "A"}); r.Code != 403 {
		t.Fatalf("wrong pin: %d", r.Code)
	}
	var s map[string]any
	_ = json.Unmarshal(e.call("GET", "/api/signing/v1/submissions/"+f3.submission, "adminA", nil, "").Body.Bytes(), &s)
	if s["current_version"].(float64) != 0 {
		t.Fatal("a wrong PIN produced a version")
	}
}
