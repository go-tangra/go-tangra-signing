package signing

import (
	"crypto"
	"crypto/ecdsa"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/asn1"
	"encoding/base64"
	"errors"
	"math/big"
	"testing"
	"time"

	"github.com/go-tangra/go-tangra-signing/v4/internal/apperr"
	"github.com/go-tangra/go-tangra-signing/v4/internal/audit"
	"github.com/go-tangra/go-tangra-signing/v4/internal/pdf/pdftest"
	"github.com/go-tangra/go-tangra-signing/v4/internal/pdf/verify"
	"github.com/go-tangra/go-tangra-signing/v4/internal/store"
)

func chainB64(chain []*x509.Certificate) []string {
	out := make([]string, 0, len(chain))
	for _, c := range chain {
		out = append(out, base64.StdEncoding.EncodeToString(c.Raw))
	}
	return out
}

func cardSign(t *testing.T, card pdftest.RSACard, digest []byte) []byte {
	t.Helper()
	sig, err := rsa.SignPKCS1v15(rand.Reader, card.Key, crypto.SHA256, digest)
	if err != nil {
		t.Fatal(err)
	}
	return sig
}

func TestQESAfterLocalSignature(t *testing.T) {
	origin := pdftest.Card("portal.example.org")
	e := newEnv(t, func(d *Deps) { d.Origin = &Origin{Cert: origin.Cert, Signer: origin.Key} })
	e.setup(t, "alice")
	d := e.submission(t, store.ModeSequential)
	a, b := slot(d, "alice"), slot(d, "bob")
	if _, err := e.svc.Sign(ctx, user("alice"), a, aliceInput()); err != nil {
		t.Fatal(err)
	}
	card := pdftest.Card("Боб Строителя")
	prep, err := e.svc.PrepareQES(ctx, user("bob"), b, QESInput{Values: map[string]string{"salary": "6000"}, Chain: chainB64(card.Chain),
		Signature: pngBytes(100, 30)})
	if err != nil {
		t.Fatal(err)
	}
	if sum := sha256.Sum256(prep.SignedAttrs); string(sum[:]) != string(prep.Digest) || !prep.ExpiresAt.Equal(e.now.Add(10*time.Minute)) {
		t.Fatalf("prepared %+v", prep)
	}
	// The origin proof is the portal certificate's signature of H(H(c)||c).
	h := sha256.Sum256(prep.SignedAttrs)
	final := sha256.Sum256(append(h[:], prep.SignedAttrs...))
	if rsa.VerifyPKCS1v15(&origin.Key.PublicKey, crypto.SHA256, final[:], prep.OriginProof) != nil || string(prep.OriginCertificate) != string(origin.Cert.Raw) {
		t.Fatal("origin proof")
	}
	// A new service instance over the same storage completes it.
	other := New(e.svc.d)
	res, err := other.CompleteQES(ctx, user("bob"), b, prep.ID, cardSign(t, card, prep.Digest), "10.9.9.9", "UA")
	if err != nil || res.SubmissionStatus != store.SubmissionCompleted {
		t.Fatalf("complete: %+v %v", res, err)
	}
	sg := e.signer(t, b)
	if sg.Method != store.MethodQES || sg.CertSubject != "Боб Строителя" || sg.CertIssuer != "Test Qualified CA" || sg.CertSerial == "" ||
		sg.Values["salary"] != "6000" || sg.IP != "10.9.9.9" || sg.CertificateID != nil {
		t.Fatalf("qes signer %+v", sg)
	}
	roots := x509.NewCertPool()
	roots.AddCert(card.Chain[1])
	sigs, err := verify.Verify(e.finalPDF(t, d.Submission.ID), verify.Options{Pools: map[string]*x509.CertPool{"tenant": e.caPool(t), "qualified": roots}})
	if err != nil || len(sigs) != 2 || !sigs[0].Valid() || !sigs[1].Valid() || sigs[0].Trust != "tenant" || sigs[1].Trust != "qualified" {
		t.Fatalf("both signatures valid: %+v %v", sigs, err)
	}
	if q, _ := e.mem.GetQES(ctx, tenant, prep.ID); q.UsedAt == nil || q.Values["salary"] != "" {
		t.Fatal("preparation not marked used")
	}
	if e.blob.Has(blobKey(prep.ID)) {
		t.Fatal("prepared object kept")
	}
	if _, err := e.svc.CompleteQES(ctx, user("bob"), b, prep.ID, cardSign(t, card, prep.Digest), "", ""); !errors.Is(err, apperr.PreparationExpired) {
		t.Fatalf("used twice: %v", err)
	}
	if !e.audit.has(audit.QESPrepare, audit.OutcomeOK) {
		t.Fatal("prepare audited")
	}
}

func blobKey(id string) string { return "tenants/" + tenant + "/qes/" + id + ".pdf" }

func TestQESRefusals(t *testing.T) {
	e := newEnv(t)
	d := e.submission(t, store.ModeParallel)
	a, b := slot(d, "alice"), slot(d, "bob")
	card := pdftest.Card("Card")
	in := func() QESInput {
		return QESInput{Values: map[string]string{"name": "Мария"}, Chain: chainB64(card.Chain)}
	}

	if _, err := e.svc.PrepareQES(ctx, user("alice"), a, QESInput{Chain: []string{"junk"}}); !errors.Is(err, apperr.Validation) {
		t.Fatalf("bad chain: %v", err)
	}
	if _, err := e.svc.PrepareQES(ctx, user("bob"), a, in()); !errors.Is(err, apperr.NotFound) {
		t.Fatalf("someone else's slot: %v", err)
	}
	if _, err := e.svc.PrepareQES(ctx, user("alice"), a, QESInput{Chain: chainB64(card.Chain)}); !errors.Is(err, apperr.MissingRequired) {
		t.Fatalf("missing required: %v", err)
	}
	prep, err := e.svc.PrepareQES(ctx, user("alice"), a, in())
	if err != nil {
		t.Fatal(err)
	}
	if prep.OriginProof != nil {
		t.Fatal("no origin configured, no proof")
	}
	if _, err := e.svc.CompleteQES(ctx, user("alice"), a, prep.ID, make([]byte, 256), "", ""); !errors.Is(err, apperr.QESSignatureInvalid) {
		t.Fatalf("forged: %v", err)
	}
	other := pdftest.Card("Other")
	if _, err := e.svc.CompleteQES(ctx, user("alice"), a, prep.ID, cardSign(t, other, prep.Digest), "", ""); !errors.Is(err, apperr.QESSignatureInvalid) {
		t.Fatalf("other card: %v", err)
	}
	if _, err := e.svc.CompleteQES(ctx, user("alice"), a, prep.ID, []byte{1}, "", ""); !errors.Is(err, apperr.QESSignatureInvalid) {
		t.Fatalf("short: %v", err)
	}
	if _, err := e.svc.CompleteQES(ctx, user("alice"), b, prep.ID, cardSign(t, card, prep.Digest), "", ""); !errors.Is(err, apperr.NotFound) {
		t.Fatalf("someone else's slot: %v", err)
	}
	if _, err := e.svc.CompleteQES(ctx, user("bob"), b, prep.ID, cardSign(t, card, prep.Digest), "", ""); !errors.Is(err, apperr.PreparationExpired) {
		t.Fatalf("another slot's preparation: %v", err)
	}
	if _, err := e.svc.CompleteQES(ctx, user("alice"), a, "missing", nil, "", ""); !errors.Is(err, apperr.PreparationExpired) {
		t.Fatalf("unknown preparation: %v", err)
	}
	// The document moved on (bob signed locally meanwhile).
	e.setup(t, "bob")
	if _, err := e.svc.Sign(ctx, user("bob"), b, Input{PIN: "123456"}); err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc.CompleteQES(ctx, user("alice"), a, prep.ID, cardSign(t, card, prep.Digest), "", ""); !errors.Is(err, apperr.DocumentChanged) {
		t.Fatalf("document changed: %v", err)
	}
	// Expired preparations and certificates.
	prep2, err := e.svc.PrepareQES(ctx, user("alice"), a, in())
	if err != nil {
		t.Fatal(err)
	}
	e.now = e.now.Add(11 * time.Minute)
	if _, err := e.svc.CompleteQES(ctx, user("alice"), a, prep2.ID, cardSign(t, card, prep2.Digest), "", ""); !errors.Is(err, apperr.PreparationExpired) {
		t.Fatalf("expired: %v", err)
	}
	e.now = e.now.AddDate(2, 0, 0)
	if _, err := e.svc.PrepareQES(ctx, user("alice"), a, in()); !errors.Is(err, apperr.CertificateUnusable) {
		t.Fatalf("expired card: %v", err)
	}
	if !e.audit.has(audit.QESPrepare, audit.OutcomeRefused) || !e.audit.has(audit.SignerSign, audit.OutcomeRefused) {
		t.Fatal("refusals audited")
	}
}

func TestQESCardSignsAfterExpiry(t *testing.T) {
	e := newEnv(t)
	d := e.submission(t, store.ModeParallel)
	a := slot(d, "alice")
	card := pdftest.Card("Card")
	prep, err := e.svc.PrepareQES(ctx, user("alice"), a, QESInput{Values: map[string]string{"name": "M"}, Chain: chainB64(card.Chain)})
	if err != nil {
		t.Fatal(err)
	}
	// The certificate expired between prepare and complete.
	e.svc.d.QESTTL = 0
	e.now = card.Cert.NotAfter.Add(time.Minute)
	q, _ := e.mem.GetQES(ctx, tenant, prep.ID)
	q.ExpiresAt = e.now.Add(time.Minute)
	e.mem.PutQES(q)
	if _, err := e.svc.CompleteQES(ctx, user("alice"), a, prep.ID, cardSign(t, card, prep.Digest), "", ""); !errors.Is(err, apperr.CertificateUnusable) {
		t.Fatalf("leaf expired at completion: %v", err)
	}
}

func TestQESWithRawECDSA(t *testing.T) {
	e := newEnv(t)
	d := e.submission(t, store.ModeParallel)
	a := slot(d, "alice")
	ca := pdftest.CA("EC Qualified CA")
	id := pdftest.Signer(ca, "EC Card")
	prep, err := e.svc.PrepareQES(ctx, user("alice"), a, QESInput{Values: map[string]string{"name": "M"}, Chain: chainB64(id.Chain)})
	if err != nil {
		t.Fatal(err)
	}
	der, _ := ecdsa.SignASN1(rand.Reader, id.Key, prep.Digest)
	var rs struct{ R, S *big.Int }
	_, _ = asn1.Unmarshal(der, &rs)
	raw := append(rs.R.FillBytes(make([]byte, 32)), rs.S.FillBytes(make([]byte, 32))...)
	if res, err := e.svc.CompleteQES(ctx, user("alice"), a, prep.ID, raw, "", ""); err != nil || res.SignerStatus != store.SignerSigned {
		t.Fatalf("raw ecdsa: %+v %v", res, err)
	}
}
