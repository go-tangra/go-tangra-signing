package httpapi

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"github.com/go-tangra/go-tangra-signing/v4/internal/certs"
	"github.com/go-tangra/go-tangra-signing/v4/internal/contacts"
	"github.com/go-tangra/go-tangra-signing/v4/internal/memstore"
	"github.com/go-tangra/go-tangra-signing/v4/internal/pincrypto"
	"github.com/go-tangra/go-tangra-signing/v4/internal/pki"
	"github.com/go-tangra/go-tangra-signing/v4/internal/sealed"
)

func meAPI(t *testing.T) *Server {
	t.Helper()
	mem := memstore.New()
	sealer, _ := sealed.NewEnvelope(bytes.Repeat([]byte{7}, 32))
	p := pki.New(pki.Deps{Store: mem, Sealer: sealer,
		Config: pki.Config{CAValidityYears: 10, CARenewBeforeYears: 2, CertValidityYears: 2, CRLValidityDays: 7, PINIterations: pincrypto.MinIterations}})
	dir := &contacts.Fake{Users: map[string][]contacts.Contact{tenantA: {{UserID: "member", DisplayName: "Ivan Petrov", Email: "ivan@example.org"}}}}
	me := certs.New(certs.Deps{Store: mem, PKI: p, Contacts: dir, Rules: pincrypto.Rules{Min: 6, Max: 32},
		Lockout: pincrypto.Lockout{Attempts: 5, Duration: 15 * time.Minute}})
	return newAPI(t, Deps{Me: me})
}

func TestMyCertificateRoutes(t *testing.T) {
	s := meAPI(t)
	r := call(s, "GET", Prefix+"/me/certificate", "member", nil, "")
	expect(t, r, 200, "no certificate")
	if r.json(t)["certificate"] != nil {
		t.Fatalf("certificate = %s", r.Body)
	}
	expect(t, callJSON(s, "POST", Prefix+"/me/certificate", "member", map[string]any{"pin": "12"}), 422, "weak pin")
	r = callJSON(s, "POST", Prefix+"/me/certificate", "member", map[string]any{"pin": "123456"})
	expect(t, r, 201, "setup")
	c := r.json(t)["certificate"].(map[string]any)
	if c["subject_cn"] != "Ivan Petrov" || c["kind"] != "signer" || !strings.Contains(c["pem"].(string), "BEGIN CERTIFICATE") ||
		len(c["fingerprint_sha256"].(string)) != 64 || c["issuer_cn"] == "" {
		t.Fatalf("certificate = %v", c)
	}
	if strings.Contains(r.Body.String(), "PRIVATE") || strings.Contains(r.Body.String(), "key_blob") {
		t.Fatal("key material in the response")
	}
	if r := callJSON(s, "POST", Prefix+"/me/certificate", "member", map[string]any{"pin": "123456"}); r.Code != 409 || r.reason() != "certificate_exists" {
		t.Fatalf("second setup: %d %s", r.Code, r.Body)
	}
	if r := callJSON(s, "POST", Prefix+"/me/certificate/pin", "member", map[string]any{"old_pin": "000000", "new_pin": "654321"}); r.Code != 403 || r.reason() != "pin_invalid" ||
		r.json(t)["detail"].(map[string]any)["attempts_left"].(float64) != 4 {
		t.Fatalf("wrong old pin: %d %s", r.Code, r.Body)
	}
	expect(t, callJSON(s, "POST", Prefix+"/me/certificate/pin", "member", map[string]any{"old_pin": "123456", "new_pin": "654321"}), 204, "change pin")
	expect(t, callJSON(s, "POST", Prefix+"/me/certificate/renew", "member", map[string]any{"pin": "111111"}), 201, "renew")
	expect(t, call(s, "POST", Prefix+"/me/certificate/revoke", "member", nil, ""), 204, "revoke")
	if r := call(s, "POST", Prefix+"/me/certificate/revoke", "member", nil, ""); r.reason() != "certificate_missing" {
		t.Fatalf("revoke twice: %d %s", r.Code, r.Body)
	}
	// Another tenant's user without a contact cannot set one up.
	if r := callJSON(s, "POST", Prefix+"/me/certificate", "outsider", map[string]any{"pin": "123456"}); r.reason() != "contact_missing" {
		t.Fatalf("outsider: %d %s", r.Code, r.Body)
	}
	expect(t, call(s, "GET", Prefix+"/me/certificate", "", nil, ""), 401, "anonymous")
}
