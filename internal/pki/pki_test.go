package pki

import (
	"bytes"
	"context"
	"crypto/sha256"
	"crypto/x509"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/go-tangra/go-tangra-signing/v4/internal/memstore"
	"github.com/go-tangra/go-tangra-signing/v4/internal/pincrypto"
	"github.com/go-tangra/go-tangra-signing/v4/internal/repo"
	"github.com/go-tangra/go-tangra-signing/v4/internal/sealed"
	"github.com/go-tangra/go-tangra-signing/v4/internal/store"
)

const tenant = "0190f7c2-6a3e-7c1a-9b2e-2f6f9d1b4c55"

var ctx = context.Background()

type env struct {
	pki *PKI
	mem *memstore.Mem
	now time.Time
}

func newEnv(t *testing.T) *env {
	t.Helper()
	e := &env{mem: memstore.New(), now: time.Now().UTC()}
	sealer, _ := sealed.NewEnvelope(bytes.Repeat([]byte{7}, 32))
	e.pki = New(Deps{Store: e.mem, Sealer: sealer, Now: func() time.Time { return e.now },
		Config: Config{CAValidityYears: 10, CARenewBeforeYears: 2, CertValidityYears: 2, CRLValidityDays: 7, PINIterations: pincrypto.MinIterations}})
	return e
}

func TestEnsureCALazyAndConcurrent(t *testing.T) {
	e := newEnv(t)
	var wg sync.WaitGroup
	ids := make(chan string, 8)
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			ca, err := e.pki.EnsureCA(ctx, tenant)
			if err != nil {
				t.Error(err)
				return
			}
			ids <- ca.ID
		}()
	}
	wg.Wait()
	close(ids)
	seen := map[string]bool{}
	for id := range ids {
		seen[id] = true
	}
	if len(seen) != 1 {
		t.Fatalf("concurrent EnsureCA created %d CAs", len(seen))
	}
	cas, _ := e.mem.ListCAs(ctx, tenant)
	if len(cas) != 1 {
		t.Fatalf("stored CAs = %d", len(cas))
	}
	c, err := Parse(cas[0])
	if err != nil || !c.IsCA || !c.MaxPathLenZero || c.KeyUsage&x509.KeyUsageCertSign == 0 || c.Subject.Organization[0] != Organization {
		t.Fatalf("ca cert = %+v %v", c, err)
	}
	if bytes.Contains(cas[0].KeyBlob, []byte("PRIVATE")) || cas[0].KeyProtection != store.KeySealed {
		t.Fatal("CA key not sealed")
	}
}

func TestCARenewal(t *testing.T) {
	e := newEnv(t)
	old, _ := e.pki.EnsureCA(ctx, tenant)
	e.now = e.now.AddDate(8, 1, 0) // less than two years left
	renewed, err := e.pki.EnsureCA(ctx, tenant)
	if err != nil || renewed.ID == old.ID {
		t.Fatalf("renewal: %v", err)
	}
	cas, _ := e.mem.ListCAs(ctx, tenant)
	if len(cas) != 2 {
		t.Fatalf("CAs = %d (old one must stay for verification)", len(cas))
	}
	prev, _ := e.mem.GetCertificate(ctx, tenant, old.ID)
	if prev.SupersededBy == nil || *prev.SupersededBy != renewed.ID {
		t.Fatal("old CA not superseded")
	}
	again, _ := e.pki.EnsureCA(ctx, tenant)
	if again.ID != renewed.ID {
		t.Fatal("renewed twice")
	}
}

func TestIssueSignerProfileAndPIN(t *testing.T) {
	e := newEnv(t)
	c, err := e.pki.IssueSigner(ctx, SignerInput{Tenant: tenant, UserID: "maria", Name: "Мария Иванова", Email: "maria@example.org", PIN: "123456", CreatedBy: "maria"})
	if err != nil {
		t.Fatal(err)
	}
	x, _ := Parse(c)
	if x.Subject.CommonName != "Mariya Ivanova" || x.EmailAddresses[0] != "maria@example.org" {
		t.Fatalf("subject = %q %v", x.Subject.CommonName, x.EmailAddresses)
	}
	if x.KeyUsage&x509.KeyUsageDigitalSignature == 0 || x.KeyUsage&x509.KeyUsageContentCommitment == 0 || x.IsCA ||
		len(x.UnknownExtKeyUsage) != 1 || !x.UnknownExtKeyUsage[0].Equal(OIDExtKeyUsageDocumentSigning) {
		t.Fatalf("profile = %+v", x)
	}
	if years := x.NotAfter.Sub(x.NotBefore).Hours() / 24 / 365; years < 1.9 || years > 2.1 {
		t.Fatalf("validity %.2f years", years)
	}
	chain, err := e.pki.Chain(ctx, c)
	if err != nil || len(chain) != 2 {
		t.Fatalf("chain: %v", err)
	}
	pool := x509.NewCertPool()
	pool.AddCert(chain[1])
	if _, err := x.Verify(x509.VerifyOptions{Roots: pool, KeyUsages: []x509.ExtKeyUsage{x509.ExtKeyUsageAny}, CurrentTime: e.now}); err != nil {
		t.Fatalf("not chained to the tenant CA: %v", err)
	}
	if c.KeyProtection != store.KeyPIN || bytes.Contains(c.KeyBlob, []byte("123456")) {
		t.Fatal("key not PIN-protected")
	}
	signer, err := e.pki.UnlockSigner(c, "123456")
	if err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256([]byte("doc"))
	sig, err := signer.Sign(e.pki.d.Rand, digest[:], nil)
	if err != nil || x.CheckSignature(x509.ECDSAWithSHA256, []byte("doc"), sig) != nil {
		t.Fatalf("unlocked key does not match the certificate: %v", err)
	}
	if _, err := e.pki.UnlockSigner(c, "000000"); !errors.Is(err, pincrypto.ErrPIN) {
		t.Fatalf("wrong pin: %v", err)
	}
	// A second active signer certificate for the same user is refused.
	if _, err := e.pki.IssueSigner(ctx, SignerInput{Tenant: tenant, UserID: "maria", Name: "M", Email: "m@x", PIN: "123456"}); !errors.Is(err, repo.ErrConflict) {
		t.Fatalf("second certificate: %v", err)
	}
	changed, err := e.pki.ChangePIN(c, "123456", "654321")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.pki.UnlockSigner(changed, "654321"); err != nil {
		t.Fatalf("new pin: %v", err)
	}
	if _, err := e.pki.ChangePIN(c, "wrong", "x"); !errors.Is(err, pincrypto.ErrPIN) {
		t.Fatalf("change with wrong pin: %v", err)
	}
	// Name without letters to transliterate / empty name falls back to the e-mail.
	for in, want := range map[string]string{"Чавдар Шишков": "Chavdar Shishkov", "Щерев": "Shterev", "Жана Юлиева": "Zhana Yulieva",
		"ТАНГРА ООД": "TANGRA OOD", "Ян": "Yan"} {
		if got := Transliterate(in); got != want {
			t.Errorf("Transliterate(%q) = %q, want %q", in, got, want)
		}
	}
	if Transliterate(" Ivan Petrov ") != "Ivan Petrov" || Transliterate("") != "" {
		t.Fatal("transliterate latin")
	}
}

func TestSealedCertificatesAndRevocation(t *testing.T) {
	e := newEnv(t)
	adm, err := e.pki.IssueAdmin(ctx, tenant, "Legal department", "legal@example.org", 3, "admin")
	if err != nil {
		t.Fatal(err)
	}
	if adm.Kind != store.KindAdmin || adm.KeyProtection != store.KeySealed {
		t.Fatalf("admin cert = %+v", adm)
	}
	if _, err := e.pki.SealedSigner(adm); err != nil {
		t.Fatal(err)
	}
	if _, err := e.pki.UnlockSigner(adm, "x"); !errors.Is(err, ErrNoSigner) {
		t.Fatal("PIN unlock of a sealed key")
	}
	sys1, k1, err := e.pki.SystemCertificate(ctx, tenant)
	if err != nil || k1 == nil || sys1.Kind != store.KindSystem {
		t.Fatalf("system cert: %v", err)
	}
	sys2, _, _ := e.pki.SystemCertificate(ctx, tenant)
	if sys2.ID != sys1.ID {
		t.Fatal("system certificate created twice")
	}
	signer, _ := e.pki.IssueSigner(ctx, SignerInput{Tenant: tenant, UserID: "ivan", Name: "Ivan", Email: "i@x", PIN: "123456"})
	if _, err := e.pki.SealedSigner(signer); !errors.Is(err, ErrNoSigner) {
		t.Fatal("sealed unlock of a PIN key")
	}
	if _, err := e.pki.Revoke(ctx, signer, "bogus"); err == nil {
		t.Fatal("unknown reason accepted")
	}
	ca, _ := e.pki.EnsureCA(ctx, tenant)
	if _, err := e.pki.Revoke(ctx, ca, "superseded"); err == nil {
		t.Fatal("CA revoked")
	}
	rev, err := e.pki.Revoke(ctx, signer, "key_compromise")
	if err != nil || rev.Status != store.CertRevoked || rev.RevokedAt == nil {
		t.Fatalf("revoke: %+v %v", rev, err)
	}
	if again, err := e.pki.Revoke(ctx, rev, "superseded"); err != nil || again.RevocationReason != "key_compromise" {
		t.Fatal("second revoke must be a no-op")
	}
	ca, _ = e.mem.GetCertificate(ctx, tenant, ca.ID)
	crl, err := x509.ParseRevocationList(ca.CRLDER)
	if err != nil || ca.CRLNextUpdate == nil {
		t.Fatalf("crl: %v", err)
	}
	caCert, _ := Parse(ca)
	if err := crl.CheckSignatureFrom(caCert); err != nil {
		t.Fatalf("crl signature: %v", err)
	}
	x, _ := Parse(signer)
	if len(crl.RevokedCertificateEntries) != 1 || crl.RevokedCertificateEntries[0].SerialNumber.Cmp(x.SerialNumber) != 0 ||
		crl.RevokedCertificateEntries[0].ReasonCode != 1 {
		t.Fatalf("crl entries = %+v", crl.RevokedCertificateEntries)
	}
	if Fingerprint(signer) == "" || len(Fingerprint(signer)) != 64 {
		t.Fatal("fingerprint")
	}
	if err := e.pki.PublishCRL(ctx, tenant, store.NewID()); !errors.Is(err, repo.ErrNotFound) {
		t.Fatalf("crl of unknown CA: %v", err)
	}
}

func TestStoreFailures(t *testing.T) {
	e := newEnv(t)
	e.mem.Fail("CurrentCA", errors.New("db down"))
	if _, err := e.pki.EnsureCA(ctx, tenant); err == nil {
		t.Fatal("db failure hidden")
	}
	e.mem.Fail("", nil)
	if _, err := e.pki.IssueSigner(ctx, SignerInput{Tenant: tenant, UserID: "u", Name: "U", Email: "u@x", PIN: "123456"}); err != nil {
		t.Fatal(err)
	}
	e.mem.Fail("SystemCertificate", errors.New("db down"))
	if _, _, err := e.pki.SystemCertificate(ctx, tenant); err == nil {
		t.Fatal("system cert failure hidden")
	}
}
