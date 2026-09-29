package qes

import (
	"crypto"
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/asn1"
	"encoding/base64"
	"encoding/pem"
	"errors"
	"math/big"
	"strings"
	"testing"
	"time"

	"github.com/go-tangra/go-tangra-signing/v4/internal/pdf/pdftest"
)

func b64(c *x509.Certificate) string { return base64.StdEncoding.EncodeToString(c.Raw) }

func TestParseChain(t *testing.T) {
	card := pdftest.Card("Иван")
	chain, err := ParseChain([]string{b64(card.Chain[0]), b64(card.Chain[1])})
	if err != nil || len(chain) != 2 || !Equal(chain, card.Chain) {
		t.Fatalf("chain: %v", err)
	}
	armoured := string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: card.Cert.Raw}))
	if c, err := ParseChain([]string{armoured}); err != nil || len(c) != 1 {
		t.Fatalf("pem: %v", err)
	}
	other := pdftest.Card("Other")
	for name, in := range map[string][]string{
		"empty":        nil,
		"too long":     {b64(card.Cert), b64(card.Cert), b64(card.Cert), b64(card.Cert), b64(card.Cert), b64(card.Cert)},
		"not base64":   {"!!!"},
		"not der":      {base64.StdEncoding.EncodeToString([]byte("junk"))},
		"wrong issuer": {b64(card.Chain[0]), b64(other.Chain[1])},
		"huge":         {strings.Repeat("A", MaxCertBytes*2)},
	} {
		if _, err := ParseChain(in); !errors.Is(err, ErrChain) {
			t.Errorf("%s: %v", name, err)
		}
	}
	der, err := ParseDER(ChainDER(card.Chain))
	if err != nil || !Equal(der, card.Chain) {
		t.Fatalf("der: %v", err)
	}
	if _, err := ParseDER(nil); !errors.Is(err, ErrChain) {
		t.Fatal("empty der")
	}
	if _, err := ParseDER([][]byte{[]byte("x")}); !errors.Is(err, ErrChain) {
		t.Fatal("junk der")
	}
	if Equal(nil, card.Chain) || Equal(card.Chain, other.Chain) {
		t.Fatal("Equal")
	}
}

func selfSigned(t *testing.T, pub, priv any, ku x509.KeyUsage, nb, na time.Time) *x509.Certificate {
	t.Helper()
	tpl := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "x"}, NotBefore: nb, NotAfter: na, KeyUsage: ku}
	der, err := x509.CreateCertificate(rand.Reader, tpl, tpl, pub, priv)
	if err != nil {
		t.Fatal(err)
	}
	c, _ := x509.ParseCertificate(der)
	return c
}

func TestCheckLeaf(t *testing.T) {
	now := time.Now()
	card := pdftest.Card("A")
	if err := CheckLeaf(card.Cert, now); err != nil {
		t.Fatal(err)
	}
	if CheckLeaf(nil, now) == nil || CheckLeaf(card.Cert, now.AddDate(5, 0, 0)) == nil || CheckLeaf(card.Cert, now.AddDate(-1, 0, 0)) == nil {
		t.Fatal("validity")
	}
	k, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err := CheckLeaf(selfSigned(t, &k.PublicKey, k, x509.KeyUsageKeyEncipherment, now.Add(-time.Hour), now.Add(time.Hour)), now); !errors.Is(err, ErrLeaf) {
		t.Fatalf("wrong usage: %v", err)
	}
	if err := CheckLeaf(selfSigned(t, &k.PublicKey, k, 0, now.Add(-time.Hour), now.Add(time.Hour)), now); err != nil {
		t.Fatalf("no key usage extension: %v", err)
	}
	ep, es, _ := ed25519.GenerateKey(rand.Reader)
	if err := CheckLeaf(selfSigned(t, ep, es, x509.KeyUsageDigitalSignature, now.Add(-time.Hour), now.Add(time.Hour)), now); !errors.Is(err, ErrKey) {
		t.Fatalf("ed25519: %v", err)
	}
}

func TestNormalizeAndVerify(t *testing.T) {
	digest := Digest([]byte("signed attributes"))
	if len(digest) != 32 {
		t.Fatal("digest")
	}
	card := pdftest.Card("R")
	rsig, _ := rsa.SignPKCS1v15(rand.Reader, card.Key, crypto.SHA256, digest)
	sig, err := Normalize(rsig, card.Cert.PublicKey)
	if err != nil || Verify(card.Cert.PublicKey, digest, sig) != nil {
		t.Fatalf("rsa: %v", err)
	}
	if _, err := Normalize(rsig[:10], card.Cert.PublicKey); !errors.Is(err, ErrSignature) {
		t.Fatal("short rsa")
	}
	if err := Verify(card.Cert.PublicKey, Digest([]byte("other")), sig); !errors.Is(err, ErrSignature) {
		t.Fatal("rsa over another digest")
	}
	// The same signature wrapped in a CMS SignedData.
	if s, err := Normalize(cms(t, rsig), card.Cert.PublicKey); err != nil || string(s) != string(rsig) {
		t.Fatalf("cms: %v", err)
	}

	k, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	der, _ := ecdsa.SignASN1(rand.Reader, k, digest)
	if s, err := Normalize(der, &k.PublicKey); err != nil || Verify(&k.PublicKey, digest, s) != nil {
		t.Fatalf("ecdsa der: %v", err)
	}
	var rs struct{ R, S *big.Int }
	_, _ = asn1.Unmarshal(der, &rs)
	raw := append(rs.R.FillBytes(make([]byte, 32)), rs.S.FillBytes(make([]byte, 32))...)
	if s, err := Normalize(raw, &k.PublicKey); err != nil || Verify(&k.PublicKey, digest, s) != nil {
		t.Fatalf("ecdsa raw: %v", err)
	}
	if err := Verify(&k.PublicKey, digest, []byte{1, 2}); !errors.Is(err, ErrSignature) {
		t.Fatal("junk ecdsa")
	}
	for _, bad := range [][]byte{nil, make([]byte, MaxSigBytes+1)} {
		if _, err := Normalize(bad, &k.PublicKey); !errors.Is(err, ErrSignature) {
			t.Error("bounds")
		}
	}
	ep, _, _ := ed25519.GenerateKey(rand.Reader)
	if _, err := Normalize([]byte{1}, ep); !errors.Is(err, ErrKey) {
		t.Fatal("ed25519 normalize")
	}
	if err := Verify(ep, digest, []byte{1}); !errors.Is(err, ErrKey) {
		t.Fatal("ed25519 verify")
	}
	if _, ok := fromCMS([]byte{0x30, 0x03, 0x06, 0x01, 0x00}); ok {
		t.Fatal("not a cms")
	}
	if _, ok := fromCMS(append(cms(t, rsig)[:len(cms(t, rsig))], 0)); ok {
		t.Fatal("trailing data")
	}
	// A SignedData whose content is not a SignedData body.
	junk, _ := asn1.Marshal(struct {
		Type    asn1.ObjectIdentifier
		Content asn1.RawValue
	}{oidSignedData, asn1.RawValue{Class: asn1.ClassContextSpecific, Tag: 0, IsCompound: true, Bytes: []byte{0x05, 0x00}}})
	if _, ok := fromCMS(junk); ok {
		t.Fatal("empty signed data")
	}
}

// cms wraps sig in a minimal ContentInfo(SignedData) with one SignerInfo.
func cms(t *testing.T, sig []byte) []byte {
	t.Helper()
	null := asn1.RawValue{FullBytes: []byte{0x05, 0x00}}
	alg := asn1.RawValue{FullBytes: []byte{0x30, 0x02, 0x05, 0x00}}
	type signerInfo struct {
		Version            int
		SID                asn1.RawValue
		DigestAlgorithm    asn1.RawValue
		SignatureAlgorithm asn1.RawValue
		Signature          []byte
	}
	body, err := asn1.Marshal(struct {
		Version          int
		DigestAlgorithms asn1.RawValue
		ContentInfo      asn1.RawValue
		SignerInfos      []signerInfo `asn1:"set"`
	}{1, asn1.RawValue{FullBytes: []byte{0x31, 0x00}}, asn1.RawValue{FullBytes: []byte{0x30, 0x0b, 0x06, 0x09, 0x2a, 0x86, 0x48, 0x86, 0xf7, 0x0d, 0x01, 0x07, 0x01}},
		[]signerInfo{{1, null, alg, alg, sig}}})
	if err != nil {
		t.Fatal(err)
	}
	out, err := asn1.Marshal(struct {
		Type    asn1.ObjectIdentifier
		Content asn1.RawValue
	}{oidSignedData, asn1.RawValue{Class: asn1.ClassContextSpecific, Tag: 0, IsCompound: true, Bytes: body}})
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func TestOriginProof(t *testing.T) {
	k, _ := rsa.GenerateKey(rand.Reader, 2048)
	content := []byte("attrs")
	sig, err := OriginProof(k, content)
	if err != nil {
		t.Fatal(err)
	}
	h := sha256.Sum256(content)
	final := sha256.Sum256(append(h[:], content...))
	if rsa.VerifyPKCS1v15(&k.PublicKey, crypto.SHA256, final[:], sig) != nil {
		t.Fatal("origin proof")
	}
}
