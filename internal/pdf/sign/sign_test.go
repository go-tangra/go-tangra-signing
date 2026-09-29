package sign

import (
	"bytes"
	"crypto"
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/hex"
	"errors"
	"image"
	"image/color"
	"math/big"
	"testing"
	"time"

	"github.com/go-tangra/go-tangra-signing/v4/internal/pdf/incr"
	"github.com/go-tangra/go-tangra-signing/v4/internal/pdf/pdftest"
	"github.com/go-tangra/go-tangra-signing/v4/internal/pdf/render"
	"github.com/go-tangra/go-tangra-signing/v4/internal/pdf/verify"
)

func check(t *testing.T, doc []byte, want int) []verify.Signature {
	t.Helper()
	sigs, err := verify.Verify(doc, verify.Options{})
	if err != nil {
		t.Fatalf("verify: %v", err)
	}
	if len(sigs) != want {
		t.Fatalf("signatures = %d, want %d", len(sigs), want)
	}
	for i, s := range sigs {
		if !s.Intact {
			t.Fatalf("signature %d (%s) broken: %s", i, s.Name, s.Problem)
		}
	}
	if !sigs[len(sigs)-1].CoversWhole {
		t.Fatal("the last signature must cover the whole document")
	}
	return sigs
}

func stampPNG(t *testing.T, name string) []byte {
	t.Helper()
	img, err := render.Stamp{Name: name, Date: "2026-09-29 10:00 UTC", Issuer: "Test CA"}.Render(150, 50)
	if err != nil {
		t.Fatal(err)
	}
	b, err := render.PNG(img)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func value(t *testing.T) image.Image {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, 60, 10))
	for x := 0; x < 60; x++ {
		img.Set(x, 5, color.Black)
	}
	return img
}

// Two signers in sequence, each adding value stamps before signing: both
// signatures stay valid (the core promise of research D2).
func TestSequentialSignersWithStampsStayValid(t *testing.T) {
	ca := pdftest.CA("Tenant Signing CA")
	alice, bob := pdftest.Signer(ca, "Alice Иванова"), pdftest.Signer(ca, "Bob")
	doc := pdftest.Contract(2)
	pages, err := incr.Pages(doc)
	if err != nil {
		t.Fatal(err)
	}

	doc, err = incr.AddStamps(doc, []incr.Stamp{{Page: 1, X: 0.2, Y: 0.15, W: 0.3, H: 0.03, Image: value(t), Name: "Name"}})
	if err != nil {
		t.Fatal(err)
	}
	first, err := Sign(doc, Identity{Signer: alice.Key, Chain: alice.Chain}, Options{
		Name: "Alice Иванова", Reason: "Employee", Location: "Sofia", Time: time.Now(),
		Visible: &Visible{Page: 1, Rect: pages[0].Rect(0.1, 0.8, 0.3, 0.08), Image: stampPNG(t, "Alice")},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.HasPrefix(first, doc) {
		t.Fatal("signature must be an incremental update")
	}
	check(t, first, 1)

	stamped, err := incr.AddStamps(first, []incr.Stamp{{Page: 2, X: 0.5, Y: 0.5, W: 0.2, H: 0.03, Image: value(t), Name: "Employer"}})
	if err != nil {
		t.Fatal(err)
	}
	second, err := Sign(stamped, Identity{Signer: bob.Key, Chain: bob.Chain}, Options{
		Name: "Bob", CRLs: [][]byte{crl(t, ca)},
		Visible: &Visible{Page: 2, Rect: pages[1].Rect(0.6, 0.8, 0.3, 0.08), Image: stampPNG(t, "Bob")},
	})
	if err != nil {
		t.Fatal(err)
	}
	signers := check(t, second, 2)
	if signers[0].Name != "Alice Иванова" && signers[1].Name != "Alice Иванова" {
		t.Fatalf("names: %q %q", signers[0].Name, signers[1].Name)
	}
}

func crl(t *testing.T, ca pdftest.Identity) []byte {
	t.Helper()
	der, err := x509.CreateRevocationList(rand.Reader, &x509.RevocationList{
		Number: bigOne(), ThisUpdate: time.Now(), NextUpdate: time.Now().Add(24 * time.Hour),
	}, ca.Cert, ca.Key)
	if err != nil {
		t.Fatal(err)
	}
	return der
}

func TestCertificationAndRefusals(t *testing.T) {
	ca := pdftest.CA("CA")
	admin := pdftest.Signer(ca, "Admin")
	out, err := Sign(pdftest.Contract(1), Identity{Signer: admin.Key, Chain: admin.Chain}, Options{
		Name: "Admin", Certify: true, Visible: &Visible{Page: 1, Rect: [4]float64{0, 0, 10, 10}},
	})
	if err != nil {
		t.Fatal(err)
	}
	check(t, out, 1)
	if !bytes.Contains(out, []byte("/DocMDP")) {
		t.Fatal("certification signature must carry DocMDP")
	}

	if _, err := Sign(out, Identity{}, Options{}); !errors.Is(err, ErrIdentity) {
		t.Fatalf("no identity: %v", err)
	}
	if _, err := Sign([]byte("not a pdf"), Identity{Signer: admin.Key, Chain: admin.Chain}, Options{}); !errors.Is(err, ErrPDF) {
		t.Fatalf("garbage: %v", err)
	}
	if _, err := Sign(pdftest.Contract(1), Identity{Signer: admin.Key, Chain: admin.Chain}, Options{CRLs: [][]byte{{}}}); err == nil {
		// an empty CRL is accepted by AddCRL; signing still succeeds
		t.Log("empty CRL accepted")
	}
	if _, err := Sign(pdftest.Contract(1), Identity{Signer: admin.Key, Chain: admin.Chain}, Options{
		TSA: &TSA{URL: "http://127.0.0.1:1/tsa"},
	}); !errors.Is(err, ErrTSA) {
		t.Fatal("unreachable TSA must fail the signature")
	}
}

// The external (card) flow: Prepare, sign the digest elsewhere, Complete.
func TestExternalSignature(t *testing.T) {
	card := pdftest.Card("Иван Петров")
	base := pdftest.Contract(1)
	prep, err := Prepare(base, card.Chain, Options{Name: "Иван Петров", Reason: "QES",
		Visible: &Visible{Page: 1, Rect: [4]float64{72, 72, 272, 122}, Image: stampPNG(t, "Иван")}})
	if err != nil {
		t.Fatal(err)
	}
	if sum := sha256.Sum256(prep.SignedAttrs); len(prep.Digest) != 32 || !bytes.Equal(sum[:], prep.Digest) || !bytes.HasPrefix(prep.PDF, base) {
		t.Fatalf("prepared: digest %d", len(prep.Digest))
	}
	sig, err := rsa.SignPKCS1v15(rand.Reader, card.Key, crypto.SHA256, prep.Digest)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Complete(prep.PDF, card.Cert, bytes.Repeat([]byte{1}, len(sig))); !errors.Is(err, ErrSignature) {
		t.Fatalf("forged signature: %v", err)
	}
	other := pdftest.Card("Other")
	if _, err := Complete(prep.PDF, other.Cert, sig); !errors.Is(err, ErrSignature) {
		t.Fatalf("wrong certificate: %v", err)
	}
	done, err := Complete(prep.PDF, card.Cert, sig)
	if err != nil {
		t.Fatal(err)
	}
	if len(done) != len(prep.PDF) {
		t.Fatal("completion must not move any byte")
	}
	check(t, done, 1)

	// An ECDSA key works the same way, and a second signature on top stays valid.
	ca := pdftest.CA("CA")
	ec := pdftest.Signer(ca, "EC")
	prep2, err := Prepare(done, ec.Chain, Options{Name: "EC", TSA: &TSA{URL: "http://ignored"}, Certify: true})
	if err != nil {
		t.Fatal(err)
	}
	esig, err := ecdsa.SignASN1(rand.Reader, ec.Key, prep2.Digest)
	if err != nil {
		t.Fatal(err)
	}
	done2, err := Complete(prep2.PDF, ec.Cert, esig)
	if err != nil {
		t.Fatal(err)
	}
	check(t, done2, 2)
}

func TestExternalRefusals(t *testing.T) {
	if _, err := Prepare(pdftest.Contract(1), nil, Options{}); !errors.Is(err, ErrIdentity) {
		t.Fatalf("no chain: %v", err)
	}
	pub, priv, _ := ed25519.GenerateKey(rand.Reader)
	tpl := &x509.Certificate{SerialNumber: bigOne(), NotBefore: time.Now(), NotAfter: time.Now().Add(time.Hour)}
	der, _ := x509.CreateCertificate(rand.Reader, tpl, tpl, pub, priv)
	edCert, _ := x509.ParseCertificate(der)
	if _, err := Prepare(pdftest.Contract(1), []*x509.Certificate{edCert}, Options{}); !errors.Is(err, ErrKey) {
		t.Fatalf("ed25519: %v", err)
	}
	if verify25519 := verifyAny(edCert.PublicKey); verify25519 {
		t.Fatal("unsupported keys never verify")
	}
	card := pdftest.Card("X")
	if _, err := Prepare([]byte("junk"), card.Chain, Options{}); !errors.Is(err, ErrPDF) {
		t.Fatalf("junk: %v", err)
	}
	if _, err := Complete(pdftest.Contract(1), card.Cert, []byte{1}); !errors.Is(err, ErrPrepared) {
		t.Fatalf("unsigned document: %v", err)
	}
	if _, err := Complete(nil, nil, nil); !errors.Is(err, ErrIdentity) {
		t.Fatalf("nil leaf: %v", err)
	}
	prep, err := Prepare(pdftest.Contract(1), card.Chain, Options{})
	if err != nil {
		t.Fatal(err)
	}
	// Tamper with the reserved contents: no longer hex / no longer CMS.
	start, end, err := contents(prep.PDF)
	if err != nil {
		t.Fatal(err)
	}
	bad := append([]byte(nil), prep.PDF...)
	bad[start] = 'z'
	if _, err := Complete(bad, card.Cert, []byte{1}); !errors.Is(err, ErrPrepared) {
		t.Fatalf("non-hex: %v", err)
	}
	zero := append([]byte(nil), prep.PDF...)
	for i := start; i < end; i++ {
		zero[i] = '0'
	}
	if _, err := Complete(zero, card.Cert, []byte{1}); !errors.Is(err, ErrPrepared) {
		t.Fatalf("empty contents: %v", err)
	}
	// A truncated document fails the ByteRange sanity check.
	if _, err := Complete(prep.PDF[:len(prep.PDF)-5], card.Cert, []byte{1}); !errors.Is(err, ErrPrepared) {
		t.Fatalf("truncated: %v", err)
	}
	// A signature larger than the reserved space.
	sig, _ := rsa.SignPKCS1v15(rand.Reader, card.Key, crypto.SHA256, prep.Digest)
	huge := append(sig, bytes.Repeat([]byte{0}, end-start)...)
	if _, err := completeUnchecked(prep.PDF, huge); !errors.Is(err, ErrTooLarge) {
		t.Fatalf("too large: %v", err)
	}
}

func TestPreparedAttrsRefusals(t *testing.T) {
	if _, err := preparedAttrs(pdftest.Contract(1)); !errors.Is(err, ErrPrepared) {
		t.Fatalf("unsigned: %v", err)
	}
	card := pdftest.Card("X")
	prep, err := Prepare(pdftest.Contract(1), card.Chain, Options{})
	if err != nil {
		t.Fatal(err)
	}
	start, end, _ := contents(prep.PDF)
	bad := append([]byte(nil), prep.PDF...)
	bad[start] = 'z'
	if _, err := preparedAttrs(bad); !errors.Is(err, ErrPrepared) {
		t.Fatalf("non-hex: %v", err)
	}
	for i := start; i < end; i++ {
		bad[i] = '0'
	}
	if _, err := preparedAttrs(bad); !errors.Is(err, ErrPrepared) {
		t.Fatalf("zeros: %v", err)
	}
	// A CMS without signed attributes (index 3 is not [0]).
	noAttrs := []byte{0x30, 0x0f, 0x06, 0x09, 0x2a, 0x86, 0x48, 0x86, 0xf7, 0x0d, 0x01, 0x07, 0x02, 0xa0, 0x02, 0x30, 0x00}
	enc := make([]byte, hex.EncodedLen(len(noAttrs)))
	hex.Encode(enc, noAttrs)
	copy(bad[start:], enc)
	if _, err := preparedAttrs(bad); !errors.Is(err, ErrPrepared) {
		t.Fatalf("no attrs: %v", err)
	}
}

func verifyAny(pub crypto.PublicKey) bool { return verifyKey(pub, make([]byte, 32), []byte{1}) }

func completeUnchecked(prepared, sig []byte) ([]byte, error) {
	return complete(prepared, sig, func([]byte) bool { return true })
}

func bigOne() *big.Int { return big.NewInt(1) }
