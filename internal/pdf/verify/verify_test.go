package verify

import (
	"bytes"
	"crypto/x509"
	"errors"
	"testing"
	"time"

	"github.com/pdfcpu/pdfcpu/pkg/api"

	"github.com/go-tangra/go-tangra-signing/v4/internal/pdf/incr"
	"github.com/go-tangra/go-tangra-signing/v4/internal/pdf/limits"
	"github.com/go-tangra/go-tangra-signing/v4/internal/pdf/pdftest"
	"github.com/go-tangra/go-tangra-signing/v4/internal/pdf/render"
	"github.com/go-tangra/go-tangra-signing/v4/internal/pdf/sign"
)

func pool(certs ...*x509.Certificate) *x509.CertPool {
	p := x509.NewCertPool()
	for _, c := range certs {
		p.AddCert(c)
	}
	return p
}

func signWith(t *testing.T, doc []byte, id pdftest.Identity, o sign.Options) []byte {
	t.Helper()
	out, err := sign.Sign(doc, sign.Identity{Signer: id.Key, Chain: id.Chain}, o)
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func TestTrustRevisionsAndRevocation(t *testing.T) {
	tenant := pdftest.CA("Tenant Signing CA")
	other := pdftest.CA("Someone else")
	alice := pdftest.Signer(tenant, "Alice")
	mallory := pdftest.Signer(other, "Mallory")
	signedAt := time.Now().Add(-time.Minute).Truncate(time.Second)

	doc := signWith(t, pdftest.Contract(1), alice, sign.Options{Name: "Alice", Reason: "agree", Location: "Sofia", Contact: "a@x", Time: signedAt})
	img, _ := render.Text("later value", 100, 12, 0)
	doc, err := incr.AddStamps(doc, []incr.Stamp{{Page: 1, X: 0.1, Y: 0.3, W: 0.3, H: 0.03, Image: img, Name: "Later"}})
	if err != nil {
		t.Fatal(err)
	}
	doc = signWith(t, doc, mallory, sign.Options{Name: "Mallory", Certify: false})

	revokedAt := time.Now()
	sigs, err := Verify(doc, Options{
		Pools: map[string]*x509.CertPool{"tenant": pool(tenant.Cert), "qualified": pool()},
		Revoked: func(c *x509.Certificate) (time.Time, bool) {
			return revokedAt, c.Equal(alice.Cert)
		},
	})
	if err != nil || len(sigs) != 2 {
		t.Fatalf("sigs = %d %v", len(sigs), err)
	}
	a, m := sigs[0], sigs[1]
	if a.Name != "Alice" || a.Reason != "agree" || a.Location != "Sofia" || a.Contact != "a@x" || a.Field == "" {
		t.Fatalf("alice dictionary: %+v", a)
	}
	if !a.Intact || a.CoversWhole || a.Trust != "tenant" || len(a.Chain) != 2 {
		t.Fatalf("alice: intact %v whole %v trust %q chain %d", a.Intact, a.CoversWhole, a.Trust, len(a.Chain))
	}
	if a.TimeSource != Claimed || a.SignedAt.Sub(signedAt).Abs() > time.Minute {
		t.Fatalf("alice time %v (%s)", a.SignedAt, a.TimeSource)
	}
	if !a.Revoked || !a.Valid() {
		t.Fatal("revoked after signing: still a valid signature")
	}
	a.RevokedAt = a.SignedAt.Add(-time.Hour)
	if a.Valid() {
		t.Fatal("revoked before signing must be invalid")
	}
	if !m.Intact || !m.CoversWhole || m.Trust != "" || m.TrustProblem == "" || m.Valid() {
		t.Fatalf("mallory: %+v", m)
	}
}

func TestTamperingIsDetected(t *testing.T) {
	ca := pdftest.CA("CA")
	doc := signWith(t, pdftest.Contract(1), pdftest.Signer(ca, "A"), sign.Options{Name: "A"})
	i := bytes.Index(doc, []byte("/Type"))
	bad := append([]byte(nil), doc...)
	bad[i+1] = 'X' // inside the signed range
	sigs, err := Verify(bad, Options{Pools: map[string]*x509.CertPool{"t": pool(ca.Cert)}})
	if err != nil || len(sigs) != 1 || sigs[0].Intact || sigs[0].Problem == "" || sigs[0].Valid() {
		t.Fatalf("tampered: %+v %v", sigs, err)
	}
	if _, err := Verify([]byte("junk"), Options{}); !errors.Is(err, ErrPDF) {
		t.Fatalf("junk: %v", err)
	}
	if sigs, err := Verify(pdftest.Contract(1), Options{}); err != nil || len(sigs) != 0 {
		t.Fatalf("unsigned: %v %v", sigs, err)
	}
}

func TestTimeStampAndXRefStreamDocument(t *testing.T) {
	ca := pdftest.CA("CA")
	at := time.Now().Add(-time.Minute).Truncate(time.Second).UTC()
	tsa := pdftest.TSA(ca, at)
	defer tsa.Close()

	// A document written with an xref stream (PDF 1.5+ style).
	conf := limits.Config()
	conf.WriteXRefStream = true
	conf.WriteObjectStream = true
	var src bytes.Buffer
	if err := api.Optimize(bytes.NewReader(pdftest.Contract(2)), &src, conf); err != nil {
		t.Fatal(err)
	}
	img, _ := render.Text("v", 50, 12, 0)
	doc, err := incr.AddStamps(src.Bytes(), []incr.Stamp{{Page: 2, X: 0.1, Y: 0.1, W: 0.2, H: 0.03, Image: img, Name: "V"}})
	if err != nil {
		t.Fatal(err)
	}
	signer := pdftest.Signer(ca, "Stamped")
	doc = signWith(t, doc, signer, sign.Options{Name: "Stamped", Certify: true, TSA: &sign.TSA{URL: tsa.URL}})
	sigs, err := Verify(doc, Options{Pools: map[string]*x509.CertPool{"t": pool(ca.Cert)}})
	if err != nil || len(sigs) != 1 {
		t.Fatalf("%v %v", sigs, err)
	}
	s := sigs[0]
	if !s.Intact || !s.Certification || s.TimeSource != TimeStamp || !s.SignedAt.Equal(at) || s.Trust != "t" {
		t.Fatalf("timestamped: %+v", s)
	}
}

func TestHelpers(t *testing.T) {
	for _, v := range []string{"D:20260929101500+03'00'", "D:20260929101500Z", "20260929101500"} {
		if _, ok := pdfDate(v); !ok {
			t.Errorf("pdfDate(%q)", v)
		}
	}
	if _, ok := pdfDate("yesterday"); ok {
		t.Error("garbage date parsed")
	}
}
