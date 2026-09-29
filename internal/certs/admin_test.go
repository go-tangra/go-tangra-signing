package certs

import (
	"crypto/x509"
	"errors"
	"testing"
	"time"

	"github.com/go-tangra/go-tangra-signing/v4/internal/apperr"
	"github.com/go-tangra/go-tangra-signing/v4/internal/audit"
	"github.com/go-tangra/go-tangra-signing/v4/internal/authz"
	"github.com/go-tangra/go-tangra-signing/v4/internal/pki"
	"github.com/go-tangra/go-tangra-signing/v4/internal/repo"
	"github.com/go-tangra/go-tangra-signing/v4/internal/store"
)

func TestAdminCertificates(t *testing.T) {
	e := newEnv(t)
	admin := NewAdmin(e.me.d)
	boss := authz.User(tenant, "boss", nil)

	mc, err := e.me.Setup(ctx, e.maria, "123456")
	if err != nil {
		t.Fatal(err)
	}
	for _, bad := range []struct {
		cn    string
		years int
	}{{"", 2}, {"a\nb", 2}, {"Admin", 9}, {"Admin", -1}} {
		if _, err := admin.CreateAdmin(ctx, boss, bad.cn, "", bad.years); !errors.Is(err, apperr.Validation) {
			t.Errorf("%+v: %v", bad, err)
		}
	}
	ac, err := admin.CreateAdmin(ctx, boss, " Отдел Човешки ресурси ", "hr@example.org", 0)
	if err != nil {
		t.Fatal(err)
	}
	if ac.Kind != store.KindAdmin || ac.KeyProtection != store.KeySealed || ac.SubjectCN != "Otdel Choveshki resursi" ||
		ac.NotAfter.Sub(ac.NotBefore) < 2*365*24*time.Hour-time.Hour || ac.Email != "hr@example.org" {
		t.Fatalf("admin certificate %+v", ac)
	}
	if !e.audit.has(audit.CertificateCreateAdmin) {
		t.Fatal("create audited")
	}
	certs, total, err := admin.List(ctx, boss, repo.CertificateFilter{Kind: store.KindAdmin})
	if err != nil || total != 1 || certs[0].ID != ac.ID {
		t.Fatalf("list %d %v", total, err)
	}
	if got, err := admin.Get(ctx, boss, ac.ID); err != nil || got.ID != ac.ID || admin.Issuer(ctx, got) == "" {
		t.Fatalf("get %v", err)
	}
	if _, err := admin.Get(ctx, authz.User("0190f7c2-6a3e-7c1a-9b2e-2f6f9d1b4c66", "x", nil), ac.ID); !errors.Is(err, apperr.NotFound) {
		t.Fatalf("other tenant: %v", err)
	}

	// Revoke the signer certificate: the CRL lists it and signing is refused.
	if _, err := admin.Revoke(ctx, boss, mc.Certificate.ID, "because"); !errors.Is(err, apperr.Validation) {
		t.Fatalf("bad reason: %v", err)
	}
	if _, err := admin.Revoke(ctx, boss, "missing", "unspecified"); !errors.Is(err, apperr.NotFound) {
		t.Fatalf("missing: %v", err)
	}
	ca, _ := e.mem.CurrentCA(ctx, tenant)
	if _, err := admin.Revoke(ctx, boss, ca.ID, "unspecified"); !errors.Is(err, apperr.Conflict) {
		t.Fatalf("ca: %v", err)
	}
	rc, err := admin.Revoke(ctx, boss, mc.Certificate.ID, "key_compromise")
	if err != nil || rc.Status != store.CertRevoked || rc.RevokedAt == nil {
		t.Fatalf("revoke: %+v %v", rc, err)
	}
	der, err := admin.CRL(ctx, boss)
	if err != nil {
		t.Fatal(err)
	}
	crl, err := x509.ParseRevocationList(der)
	if err != nil || len(crl.RevokedCertificateEntries) != 1 || crl.RevokedCertificateEntries[0].SerialNumber.Text(16) != rc.Serial {
		t.Fatalf("crl: %v %+v", err, crl)
	}
	caCert, _ := pki.Parse(ca)
	if err := crl.CheckSignatureFrom(caCert); err != nil {
		t.Fatalf("crl signature: %v", err)
	}
	if _, err := e.me.Unlock(ctx, e.maria, rc, "123456"); !errors.Is(err, apperr.CertificateUnusable) {
		t.Fatalf("revoked certificate still signs: %v", err)
	}
	// A CRL past its next update is re-published on download.
	e.now = e.now.AddDate(0, 0, 8)
	again, err := admin.CRL(ctx, boss)
	if err != nil || string(again) == string(der) {
		t.Fatalf("stale CRL not refreshed: %v", err)
	}
	// A tenant without a CA gets one (lazily) with an empty CRL.
	other := authz.User("0190f7c2-6a3e-7c1a-9b2e-2f6f9d1b4c77", "y", nil)
	if der, err := admin.CRL(ctx, other); err != nil || len(der) == 0 {
		t.Fatalf("fresh tenant CRL: %v", err)
	}
	evs, _ := e.mem.ListEvents(ctx, tenant, "")
	_ = evs
}
