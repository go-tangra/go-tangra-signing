package documents

import (
	"bytes"
	"context"
	"crypto/x509"
	"errors"
	"io"
	"net"
	"sync"
	"testing"
	"time"

	"github.com/go-tangra/go-tangra-signing/v4/internal/apperr"
	"github.com/go-tangra/go-tangra-signing/v4/internal/audit"
	"github.com/go-tangra/go-tangra-signing/v4/internal/authz"
	"github.com/go-tangra/go-tangra-signing/v4/internal/blob"
	"github.com/go-tangra/go-tangra-signing/v4/internal/certs"
	"github.com/go-tangra/go-tangra-signing/v4/internal/contacts"
	"github.com/go-tangra/go-tangra-signing/v4/internal/mail"
	"github.com/go-tangra/go-tangra-signing/v4/internal/memstore"
	"github.com/go-tangra/go-tangra-signing/v4/internal/pdf/incr"
	"github.com/go-tangra/go-tangra-signing/v4/internal/pdf/pdftest"
	"github.com/go-tangra/go-tangra-signing/v4/internal/pdf/render"
	"github.com/go-tangra/go-tangra-signing/v4/internal/pdf/sign"
	"github.com/go-tangra/go-tangra-signing/v4/internal/pincrypto"
	"github.com/go-tangra/go-tangra-signing/v4/internal/pki"
	"github.com/go-tangra/go-tangra-signing/v4/internal/sealed"
	"github.com/go-tangra/go-tangra-signing/v4/internal/store"
	"github.com/go-tangra/go-tangra-signing/v4/internal/submissions"
	"github.com/go-tangra/go-tangra-signing/v4/internal/warden"
)

const (
	tenant  = "0190f7c2-6a3e-7c1a-9b2e-2f6f9d1b4c55"
	other   = "0190f7c2-6a3e-7c1a-9b2e-2f6f9d1b4c66"
	secret  = "01928f7e-3c1a-7b44-9d2e-5a6b7c8d9e0f"
	userTok = "user-token"
)

var ctx = context.Background()

type recorder struct {
	mu     sync.Mutex
	events []audit.Event
}

func (r *recorder) Record(_ context.Context, e audit.Event) error {
	if err := audit.Validate(e); err != nil {
		return err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.events = append(r.events, e)
	return nil
}

func (r *recorder) has(t audit.EventType, outcome string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, e := range r.events {
		if e.EventType == t && e.Outcome == outcome {
			return true
		}
	}
	return false
}

type env struct {
	svc    *Service
	mem    *memstore.Mem
	blob   *blob.Fake
	admin  *certs.Admin
	me     *certs.Me
	pki    *pki.PKI
	warden *warden.Fake
	audit  *recorder
	subs   *submissions.Service
	card   pdftest.RSACard
	now    time.Time
	boss   authz.Subjects
}

func newEnv(t *testing.T, opts ...func(*Deps)) *env {
	t.Helper()
	e := &env{mem: memstore.New(), blob: blob.NewFake(), warden: warden.NewFake(), audit: &recorder{}, now: time.Now().UTC(),
		boss: authz.User(tenant, "boss", nil), card: pdftest.Card("Иван Петров")}
	clock := func() time.Time { return e.now }
	sealer, _ := sealed.NewEnvelope(bytes.Repeat([]byte{7}, 32))
	e.pki = pki.New(pki.Deps{Store: e.mem, Sealer: sealer, Now: clock,
		Config: pki.Config{CAValidityYears: 10, CARenewBeforeYears: 2, CertValidityYears: 2, CRLValidityDays: 7, PINIterations: pincrypto.MinIterations}})
	dir := &contacts.Fake{Users: map[string][]contacts.Contact{tenant: {
		{UserID: "boss", DisplayName: "Boss", Email: "boss@example.org"},
		{UserID: "maria", DisplayName: "Мария", Email: "maria@example.org"},
	}}}
	cd := certs.Deps{Store: e.mem, PKI: e.pki, Contacts: dir, Now: clock, Rules: pincrypto.Rules{Min: 6, Max: 32},
		Lockout: pincrypto.Lockout{Attempts: 3, Duration: time.Minute}}
	e.admin, e.me = certs.NewAdmin(cd), certs.New(cd)
	e.subs = submissions.New(submissions.Deps{Store: e.mem, Blob: e.blob, Contacts: dir, Now: clock,
		Checker: authz.Static{"boss": authz.Permissions}, Mail: mail.Mailer{}})
	roots := x509.NewCertPool()
	roots.AddCert(e.card.Chain[1])
	d := Deps{Store: e.mem, Blob: e.blob, Audit: e.audit, PKI: e.pki, Warden: e.warden, Subs: e.subs, Roots: roots, Now: clock}
	for _, o := range opts {
		o(&d)
	}
	e.svc = New(d)
	return e
}

func (e *env) adminCert(t *testing.T) store.Certificate {
	t.Helper()
	c, err := e.admin.CreateAdmin(ctx, e.boss, "HR Department", "hr@example.org", 2)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func (e *env) download(t *testing.T, id string) []byte {
	t.Helper()
	rc, err := e.svc.Download(ctx, e.boss, id)
	if err != nil {
		t.Fatal(err)
	}
	defer rc.Close()
	b, _ := io.ReadAll(rc)
	return b
}

func TestSignAndVerifyUpload(t *testing.T) {
	e := newEnv(t)
	c := e.adminCert(t)
	id, exp, err := e.svc.Sign(ctx, e.boss, SignInput{Source: Source{PDF: pdftest.Contract(1)}, CertificateID: c.ID,
		Reason: "Approved", Location: "Sofia", Contact: "hr@example.org"})
	if err != nil {
		t.Fatal(err)
	}
	if !exp.Equal(e.now.Add(Retention)) {
		t.Fatalf("expires %v", exp)
	}
	signed := e.download(t, id)
	res, err := e.svc.Verify(ctx, e.boss, Source{PDF: signed})
	if err != nil || len(res.Signatures) != 1 || res.ModifiedAfterLast {
		t.Fatalf("verify: %+v %v", res, err)
	}
	s := res.Signatures[0]
	if s.Signer != "HR Department" || s.Reason != "Approved" || s.Location != "Sofia" || s.Integrity != "valid" || s.Trust != "trusted" ||
		s.Method != PoolTenant || s.Revocation != "good" || serialKey(s.Serial) != serialKey(c.Serial) || s.Issuer == "" || s.Time == nil {
		t.Fatalf("signature %+v", s)
	}
	if !bytes.Contains(signed, []byte("/DocMDP")) {
		t.Fatal("unsigned input gets a certification signature")
	}

	// A second admin signature on the signed document is an approval.
	id2, _, err := e.svc.Sign(ctx, e.boss, SignInput{Source: Source{PDF: signed}, CertificateID: c.ID})
	if err != nil {
		t.Fatal(err)
	}
	res, _ = e.svc.Verify(ctx, e.boss, Source{PDF: e.download(t, id2)})
	if len(res.Signatures) != 2 || res.Signatures[0].Integrity != "valid" || res.Signatures[1].Integrity != "valid" {
		t.Fatalf("two signatures %+v", res)
	}

	// Revoked after signing: still good; appended changes are reported.
	e.now = e.now.Add(time.Minute)
	if _, err := e.admin.Revoke(ctx, e.boss, c.ID, "superseded"); err != nil {
		t.Fatal(err)
	}
	img, _ := render.Text("later", 80, 12, 0)
	changed, err := incr.AddStamps(signed, []incr.Stamp{{Page: 1, X: 0.1, Y: 0.1, W: 0.2, H: 0.03, Image: img}})
	if err != nil {
		t.Fatal(err)
	}
	res, _ = e.svc.Verify(ctx, e.boss, Source{PDF: changed})
	if !res.ModifiedAfterLast || res.Signatures[0].Revocation != "good" {
		t.Fatalf("after revoke/change %+v", res)
	}
	if _, _, err := e.svc.Sign(ctx, e.boss, SignInput{Source: Source{PDF: signed}, CertificateID: c.ID}); !errors.Is(err, apperr.CertificateUnusable) {
		t.Fatalf("revoked certificate signs: %v", err)
	}

	// Downloads expire and stay in the tenant.
	if _, err := e.svc.Download(ctx, authz.User(other, "x", nil), id); !errors.Is(err, apperr.NotFound) {
		t.Fatalf("other tenant: %v", err)
	}
	if _, err := e.svc.Download(ctx, e.boss, "nope"); !errors.Is(err, apperr.NotFound) {
		t.Fatalf("bad id: %v", err)
	}
	e.now = e.now.Add(2 * time.Hour)
	if _, err := e.svc.Download(ctx, e.boss, id); !errors.Is(err, apperr.NotFound) {
		t.Fatalf("expired: %v", err)
	}
	if !e.audit.has(audit.DocumentSign, audit.OutcomeOK) || !e.audit.has(audit.DocumentSign, audit.OutcomeRefused) || !e.audit.has(audit.DocumentVerify, audit.OutcomeOK) {
		t.Fatal("audit")
	}
}

func TestVerifyForeignAndTampered(t *testing.T) {
	e := newEnv(t)
	// A qualified card signature chains to the configured roots.
	card, err := sign.Sign(pdftest.Contract(1), sign.Identity{Signer: e.card.Key, Chain: e.card.Chain}, sign.Options{Name: "Иван Петров"})
	if err != nil {
		t.Fatal(err)
	}
	res, err := e.svc.Verify(ctx, e.boss, Source{PDF: card})
	if err != nil || res.Signatures[0].Trust != "trusted" || res.Signatures[0].Method != PoolQualified || res.Signatures[0].Revocation != "unknown" {
		t.Fatalf("card: %+v %v", res, err)
	}
	// An unknown issuer is untrusted.
	ca := pdftest.CA("Nobody")
	id := pdftest.Signer(ca, "Stranger")
	foreign, _ := sign.Sign(pdftest.Contract(1), sign.Identity{Signer: id.Key, Chain: id.Chain}, sign.Options{})
	res, _ = e.svc.Verify(ctx, e.boss, Source{PDF: foreign})
	if s := res.Signatures[0]; s.Trust != "untrusted" || s.Method != "" || s.Signer != "Stranger" || s.Integrity != "valid" {
		t.Fatalf("foreign %+v", s)
	}
	// Tampering inside the signed range.
	bad := append([]byte(nil), foreign...)
	i := bytes.Index(bad, []byte("/Type"))
	bad[i+1] = 'X'
	res, err = e.svc.Verify(ctx, e.boss, Source{PDF: bad})
	if err == nil && (res.Signatures[0].Integrity != "modified" || res.Signatures[0].Trust != "unknown") {
		t.Fatalf("tampered %+v", res)
	}
	// Unsigned documents have no signatures; garbage is refused.
	if res, err := e.svc.Verify(ctx, e.boss, Source{PDF: pdftest.Contract(1)}); err != nil || len(res.Signatures) != 0 {
		t.Fatalf("unsigned %+v %v", res, err)
	}
	if _, err := e.svc.Verify(ctx, e.boss, Source{PDF: []byte("junk")}); !errors.Is(err, apperr.InvalidPDF) {
		t.Fatalf("junk: %v", err)
	}
	if _, err := e.svc.Verify(ctx, e.boss, Source{}); !errors.Is(err, apperr.Validation) {
		t.Fatalf("no source: %v", err)
	}
	if !e.audit.has(audit.DocumentVerify, audit.OutcomeRefused) {
		t.Fatal("refusal audited")
	}
}

func TestSignRefusals(t *testing.T) {
	e := newEnv(t)
	c := e.adminCert(t)
	mc, err := e.me.Setup(ctx, authz.User(tenant, "maria", nil), "123456")
	if err != nil {
		t.Fatal(err)
	}
	pdf := pdftest.Contract(1)
	cases := []struct {
		name string
		in   SignInput
		want error
	}{
		{"unknown certificate", SignInput{Source: Source{PDF: pdf}, CertificateID: "x"}, apperr.NotFound},
		{"signer certificate", SignInput{Source: Source{PDF: pdf}, CertificateID: mc.Certificate.ID}, apperr.CertificateUnusable},
		{"reason newline", SignInput{Source: Source{PDF: pdf}, CertificateID: c.ID, Reason: "a\nb"}, apperr.Validation},
		{"location long", SignInput{Source: Source{PDF: pdf}, CertificateID: c.ID, Location: string(bytes.Repeat([]byte("x"), 201))}, apperr.Validation},
		{"contact newline", SignInput{Source: Source{PDF: pdf}, CertificateID: c.ID, Contact: "a\rb"}, apperr.Validation},
		{"no file", SignInput{CertificateID: c.ID}, apperr.Validation},
		{"garbage", SignInput{Source: Source{PDF: []byte("%PDF-junk")}, CertificateID: c.ID}, apperr.InvalidPDF},
		{"bad tsa url", SignInput{Source: Source{PDF: pdf}, CertificateID: c.ID, TSAURL: "ftp://tsa"}, apperr.Validation},
		{"private tsa", SignInput{Source: Source{PDF: pdf}, CertificateID: c.ID, TSAURL: "http://127.0.0.1:9/tsr"}, apperr.Validation},
		{"secret without token", SignInput{Source: Source{PDF: pdf}, CertificateID: c.ID, TSASecretRef: secret}, apperr.Forbidden},
	}
	for _, tc := range cases {
		if _, _, err := e.svc.Sign(ctx, e.boss, tc.in); !errors.Is(err, tc.want) {
			t.Errorf("%s: %v", tc.name, err)
		}
	}
	big := newEnv(t, func(d *Deps) { d.Limits.MaxBytes = 100 })
	bc := big.adminCert(t)
	if _, _, err := big.svc.Sign(ctx, big.boss, SignInput{Source: Source{PDF: pdf}, CertificateID: bc.ID}); !errors.Is(err, apperr.PayloadTooLarge) {
		t.Fatalf("too large: %v", err)
	}
	uctx := warden.WithUserToken(ctx, userTok)
	if _, _, err := e.svc.Sign(uctx, e.boss, SignInput{Source: Source{PDF: pdf}, CertificateID: c.ID, TSASecretRef: secret}); !errors.Is(err, apperr.Validation) {
		t.Fatalf("unknown secret: %v", err)
	}
	e.warden.Put(secret, warden.SecretMeta{Username: "u", HostURL: "http://10.0.0.5/tsr"}, "p")
	e.warden.Deny(userTok, secret)
	if _, _, err := e.svc.Sign(uctx, e.boss, SignInput{Source: Source{PDF: pdf}, CertificateID: c.ID, TSASecretRef: secret}); !errors.Is(err, apperr.Forbidden) {
		t.Fatalf("denied secret: %v", err)
	}
	e.warden.SetUnavailable(true)
	if _, _, err := e.svc.Sign(uctx, e.boss, SignInput{Source: Source{PDF: pdf}, CertificateID: c.ID, TSASecretRef: secret}); !errors.Is(err, apperr.TemporarilyUnavailable) {
		t.Fatalf("warden down: %v", err)
	}
	if e.warden.CredentialCalls() != 3 {
		t.Fatalf("warden calls %d", e.warden.CredentialCalls())
	}
}

func TestTimeStampedWithWardenSecret(t *testing.T) {
	e := newEnv(t, func(d *Deps) { d.AllowPrivateTSA = true })
	c := e.adminCert(t)
	ca := pdftest.CA("TSA CA")
	at := time.Now().UTC().Add(-time.Minute).Truncate(time.Second)
	tsa := pdftest.TSA(ca, at)
	defer tsa.Close()
	e.warden.Put(secret, warden.SecretMeta{Username: "tsa-user", HostURL: tsa.URL}, "tsa-pass")
	uctx := warden.WithUserToken(ctx, userTok)
	id, _, err := e.svc.Sign(uctx, e.boss, SignInput{Source: Source{PDF: pdftest.Contract(1)}, CertificateID: c.ID, TSASecretRef: secret})
	if err != nil {
		t.Fatal(err)
	}
	res, _ := e.svc.Verify(ctx, e.boss, Source{PDF: e.download(t, id)})
	if s := res.Signatures[0]; s.Time == nil || !s.Time.Equal(at) {
		t.Fatalf("time-stamp time %+v", s.Time)
	}
	if calls := e.warden.Calls(); len(calls) != 1 || calls[0].Token != userTok {
		t.Fatalf("warden on behalf of the user: %+v", calls)
	}
	// An unreachable TSA fails the signature.
	if _, _, err := e.svc.Sign(ctx, e.boss, SignInput{Source: Source{PDF: pdftest.Contract(1)}, CertificateID: c.ID, TSAURL: "http://127.0.0.1:1/tsr"}); !errors.Is(err, apperr.TSAFailed) {
		t.Fatalf("tsa down: %v", err)
	}
}

type resolver map[string][]string

func (r resolver) LookupIPAddr(_ context.Context, host string) ([]net.IPAddr, error) {
	ips, ok := r[host]
	if !ok {
		return nil, errors.New("no such host")
	}
	var out []net.IPAddr
	for _, s := range ips {
		out = append(out, net.IPAddr{IP: net.ParseIP(s)})
	}
	return out, nil
}

func TestTSAAddressRules(t *testing.T) {
	e := newEnv(t, func(d *Deps) {
		d.Resolver = resolver{"tsa.example.org": {"93.184.216.34"}, "rebind.example.org": {"93.184.216.34", "10.1.2.3"}, "meta.example.org": {"169.254.169.254"}}
	})
	for raw, ok := range map[string]bool{
		"https://tsa.example.org/tsr": true, "http://93.184.216.34/tsr": true,
		"https://rebind.example.org/": false, "http://meta.example.org/": false, "https://unknown.example.org/": false,
		"https://user:pw@tsa.example.org/": false, "file:///etc/passwd": false, "http:///x": false, "http://[::1]/": false,
		"http://0.0.0.0/": false, "::": false,
	} {
		if err := e.svc.checkTSA(ctx, raw); (err == nil) != ok {
			t.Errorf("%s: %v", raw, err)
		}
	}
}

func TestSignStoredSubmissionVersion(t *testing.T) {
	e := newEnv(t)
	c := e.adminCert(t)
	pdf := pdftest.Contract(1)
	tid := store.NewID()
	key := blob.TemplatePDF(tenant, tid, store.NewID())
	sum, _ := e.blob.Put(ctx, key, bytes.NewReader(pdf), int64(len(pdf)), "application/pdf")
	if err := e.mem.CreateTemplate(ctx, store.Template{ID: tid, TenantID: tenant, Name: "T", Status: store.TemplateActive, PDFKey: key, PDFSHA256: sum,
		PDFPages: 1, Parties: []store.Party{{Key: "a", Name: "A"}}, Version: 1, CreatedAt: e.now, UpdatedAt: e.now}); err != nil {
		t.Fatal(err)
	}
	d, err := e.subs.Create(ctx, e.boss, submissions.CreateInput{TemplateID: tid, Mode: store.ModeParallel,
		Signers: []submissions.SignerInput{{UserID: "maria", Party: "a"}}})
	if err != nil {
		t.Fatal(err)
	}
	id, _, err := e.svc.Sign(ctx, e.boss, SignInput{Source: Source{SubmissionID: d.Submission.ID, Version: 0}, CertificateID: c.ID})
	if err != nil {
		t.Fatal(err)
	}
	if res, err := e.svc.Verify(ctx, e.boss, Source{PDF: e.download(t, id)}); err != nil || len(res.Signatures) != 1 {
		t.Fatalf("verify %v", err)
	}
	if res, err := e.svc.Verify(ctx, e.boss, Source{SubmissionID: d.Submission.ID, Version: -1}); err != nil || len(res.Signatures) != 0 {
		t.Fatalf("verify stored version %+v %v", res, err)
	}
	if _, err := e.svc.Verify(ctx, authz.User(tenant, "maria", nil), Source{SubmissionID: d.Submission.ID, Version: 0}); !errors.Is(err, apperr.Forbidden) {
		t.Fatalf("a signer who has not signed cannot read it: %v", err)
	}
}
