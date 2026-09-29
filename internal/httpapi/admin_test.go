package httpapi

import (
	"bytes"
	"crypto/x509"
	"strings"
	"testing"
	"time"

	"github.com/go-tangra/go-tangra-signing/v4/internal/blob"
	"github.com/go-tangra/go-tangra-signing/v4/internal/certs"
	"github.com/go-tangra/go-tangra-signing/v4/internal/contacts"
	"github.com/go-tangra/go-tangra-signing/v4/internal/documents"
	"github.com/go-tangra/go-tangra-signing/v4/internal/memstore"
	"github.com/go-tangra/go-tangra-signing/v4/internal/pdf/pdftest"
	"github.com/go-tangra/go-tangra-signing/v4/internal/pincrypto"
	"github.com/go-tangra/go-tangra-signing/v4/internal/pki"
	"github.com/go-tangra/go-tangra-signing/v4/internal/sealed"
	"github.com/go-tangra/go-tangra-signing/v4/internal/submissions"
	"github.com/go-tangra/go-tangra-signing/v4/internal/warden"
)

func adminAPI(t *testing.T) *Server {
	t.Helper()
	mem, bl := memstore.New(), blob.NewFake()
	sealer, _ := sealed.NewEnvelope(bytes.Repeat([]byte{7}, 32))
	p := pki.New(pki.Deps{Store: mem, Sealer: sealer,
		Config: pki.Config{CAValidityYears: 10, CARenewBeforeYears: 2, CertValidityYears: 2, CRLValidityDays: 7, PINIterations: pincrypto.MinIterations}})
	cd := certs.Deps{Store: mem, PKI: p, Contacts: &contacts.Fake{}, Rules: pincrypto.Rules{Min: 6, Max: 32},
		Lockout: pincrypto.Lockout{Attempts: 5, Duration: 15 * time.Minute}}
	subs := submissions.New(submissions.Deps{Store: mem, Blob: bl, Checker: testChecker, Contacts: &contacts.Fake{}})
	docs := documents.New(documents.Deps{Store: mem, Blob: bl, PKI: p, Warden: warden.NewFake(), Subs: subs, Roots: x509.NewCertPool()})
	return newAPI(t, Deps{Admin: certs.NewAdmin(cd), Documents: docs, MaxPDFBytes: 5 << 20})
}

func TestAdminRoutes(t *testing.T) {
	s := adminAPI(t)
	expect(t, call(s, "GET", Prefix+"/certificates", "member", nil, ""), 403, "members cannot list")
	r := callJSON(s, "POST", Prefix+"/certificates", "admin", map[string]any{"subject_cn": "HR", "validity_years": 3})
	expect(t, r, 201, "create admin certificate")
	c := r.json(t)
	if c["kind"] != "admin" || !strings.Contains(c["pem"].(string), "CERTIFICATE") || strings.Contains(r.Body.String(), "key") {
		t.Fatalf("created %s", r.Body)
	}
	id := c["id"].(string)
	expect(t, callJSON(s, "POST", Prefix+"/certificates", "admin", map[string]any{"subject_cn": "HR", "validity_years": 9}), 422, "validity bound (schema)")
	r = call(s, "GET", Prefix+"/certificates?kind=admin", "admin", nil, "")
	expect(t, r, 200, "list")
	if r.json(t)["total"].(float64) != 1 {
		t.Fatalf("list %s", r.Body)
	}
	expect(t, call(s, "GET", Prefix+"/certificates/"+id, "admin", nil, ""), 200, "get")
	expect(t, call(s, "GET", Prefix+"/certificates/"+id, "outsider", nil, ""), 404, "other tenant")

	// Sign an upload, download it, verify it.
	body, ct := multipartReq(map[string]string{"certificate_id": id, "reason": "Approved"}, map[string][]byte{"file": pdftest.Contract(1)})
	r = call(s, "POST", Prefix+"/documents/sign", "admin", body, ct)
	expect(t, r, 201, "sign document")
	docID := r.json(t)["id"].(string)
	if r.json(t)["expires_at"] == "" {
		t.Fatal("expiry missing")
	}
	r = call(s, "GET", Prefix+"/documents/"+docID, "admin", nil, "")
	expect(t, r, 200, "download")
	signed := r.Body.Bytes()
	body, ct = multipartReq(nil, map[string][]byte{"file": signed})
	r = call(s, "POST", Prefix+"/verify", "reader", body, ct)
	expect(t, r, 200, "verify")
	sigs := r.json(t)["signatures"].([]any)
	if len(sigs) != 1 || sigs[0].(map[string]any)["trust"] != "trusted" || sigs[0].(map[string]any)["integrity"] != "valid" {
		t.Fatalf("verify %s", r.Body)
	}
	body, ct = multipartReq(map[string]string{"submission_id": "x"}, map[string][]byte{"file": signed})
	expect(t, call(s, "POST", Prefix+"/verify", "reader", body, ct), 422, "file and submission together")
	body, ct = multipartReq(map[string]string{"submission_id": "x", "version": "-3"}, nil)
	expect(t, call(s, "POST", Prefix+"/verify", "reader", body, ct), 422, "bad version")
	body, ct = multipartReq(map[string]string{"evil": "1"}, nil)
	expect(t, call(s, "POST", Prefix+"/verify", "reader", body, ct), 400, "unknown part")
	body, ct = multipartReq(map[string]string{"certificate_id": id}, map[string][]byte{"file": bytes.Repeat([]byte("x"), 6<<20)})
	expect(t, call(s, "POST", Prefix+"/documents/sign", "admin", body, ct), 413, "oversized file")

	// Revoke, then the CRL lists it and signing is refused.
	expect(t, callJSON(s, "POST", Prefix+"/certificates/"+id+"/revoke", "admin", map[string]any{"reason": "because"}), 422, "reason enum (schema)")
	r = callJSON(s, "POST", Prefix+"/certificates/"+id+"/revoke", "admin", map[string]any{"reason": "superseded"})
	expect(t, r, 200, "revoke")
	if r.json(t)["status"] != "revoked" {
		t.Fatalf("revoke %s", r.Body)
	}
	r = call(s, "GET", Prefix+"/ca/crl", "reader", nil, "")
	expect(t, r, 200, "crl")
	crl, err := x509.ParseRevocationList(r.Body.Bytes())
	if err != nil || len(crl.RevokedCertificateEntries) != 1 || r.Header().Get("Content-Type") != "application/pkix-crl" {
		t.Fatalf("crl %v", err)
	}
	body, ct = multipartReq(map[string]string{"certificate_id": id}, map[string][]byte{"file": pdftest.Contract(1)})
	if r := call(s, "POST", Prefix+"/documents/sign", "admin", body, ct); r.Code != 409 || r.reason() != "certificate_unusable" {
		t.Fatalf("revoked sign: %d %s", r.Code, r.Body)
	}
}
