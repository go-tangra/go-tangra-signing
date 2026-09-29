// Package qes is the verification core of qualified signatures made with a
// card through B-Trust BISS (research D6): it parses the certificate chain
// the browser reports, checks that the leaf may sign now, normalises the
// signature BISS returns (raw RSA, raw or DER ECDSA, or a CMS SignedData
// carrying one) and verifies it over the prepared digest with the leaf's key
// before anything is embedded. It holds no state and never trusts the
// browser beyond these checks.
package qes

import (
	"bytes"
	"crypto"
	"crypto/ecdsa"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/asn1"
	"encoding/base64"
	"errors"
	"math/big"
	"strings"
	"time"
)

// Bounds.
const (
	MaxChain     = 5
	MaxCertBytes = 16 << 10
	MaxSigBytes  = 24 << 10
)

// Errors.
var (
	ErrChain     = errors.New("qes: invalid certificate chain")
	ErrLeaf      = errors.New("qes: the certificate cannot sign now")
	ErrSignature = errors.New("qes: the signature does not match the prepared digest")
	ErrKey       = errors.New("qes: unsupported key type")
)

// ParseChain decodes a leaf-first chain of base64 DER (or PEM-armoured)
// certificates and checks that each one is issued by the next.
func ParseChain(encoded []string) ([]*x509.Certificate, error) {
	if len(encoded) == 0 || len(encoded) > MaxChain {
		return nil, ErrChain
	}
	chain := make([]*x509.Certificate, 0, len(encoded))
	for _, e := range encoded {
		der, err := decodeCert(e)
		if err != nil {
			return nil, ErrChain
		}
		c, err := x509.ParseCertificate(der)
		if err != nil {
			return nil, ErrChain
		}
		chain = append(chain, c)
	}
	return chain, Consistent(chain)
}

func decodeCert(s string) ([]byte, error) {
	s = strings.TrimSpace(s)
	s = strings.TrimPrefix(s, "-----BEGIN CERTIFICATE-----")
	s = strings.TrimSuffix(s, "-----END CERTIFICATE-----")
	s = strings.Join(strings.Fields(s), "")
	if len(s) > base64.StdEncoding.EncodedLen(MaxCertBytes) {
		return nil, ErrChain
	}
	return base64.StdEncoding.DecodeString(s)
}

// ParseDER parses a stored chain.
func ParseDER(ders [][]byte) ([]*x509.Certificate, error) {
	if len(ders) == 0 || len(ders) > MaxChain {
		return nil, ErrChain
	}
	chain := make([]*x509.Certificate, 0, len(ders))
	for _, d := range ders {
		c, err := x509.ParseCertificate(d)
		if err != nil {
			return nil, ErrChain
		}
		chain = append(chain, c)
	}
	return chain, Consistent(chain)
}

// Consistent checks that every certificate is signed by the next one.
func Consistent(chain []*x509.Certificate) error {
	for i := 0; i+1 < len(chain); i++ {
		if err := chain[i].CheckSignatureFrom(chain[i+1]); err != nil {
			return ErrChain
		}
	}
	return nil
}

// CheckLeaf reports whether the leaf may sign at now: within validity and
// with the nonRepudiation (contentCommitment) or digitalSignature usage.
func CheckLeaf(leaf *x509.Certificate, now time.Time) error {
	if leaf == nil || now.Before(leaf.NotBefore) || now.After(leaf.NotAfter) {
		return ErrLeaf
	}
	if leaf.KeyUsage != 0 && leaf.KeyUsage&(x509.KeyUsageContentCommitment|x509.KeyUsageDigitalSignature) == 0 {
		return ErrLeaf
	}
	switch leaf.PublicKey.(type) {
	case *rsa.PublicKey, *ecdsa.PublicKey:
		return nil
	}
	return ErrKey
}

// ChainDER returns the chain's raw certificates (storage).
func ChainDER(chain []*x509.Certificate) [][]byte {
	out := make([][]byte, 0, len(chain))
	for _, c := range chain {
		out = append(out, c.Raw)
	}
	return out
}

// Equal reports whether two chains have the same leaf.
func Equal(a, b []*x509.Certificate) bool {
	return len(a) > 0 && len(b) > 0 && bytes.Equal(a[0].Raw, b[0].Raw)
}

var oidSignedData = asn1.ObjectIdentifier{1, 2, 840, 113549, 1, 7, 2}

// Normalize turns what BISS returned into the signature value a CMS
// SignerInfo carries: raw RSA bytes, ECDSA as DER (a raw r||s is converted),
// or the encryptedDigest of a CMS SignedData.
func Normalize(sig []byte, pub crypto.PublicKey) ([]byte, error) {
	if len(sig) == 0 || len(sig) > MaxSigBytes {
		return nil, ErrSignature
	}
	if inner, ok := fromCMS(sig); ok {
		sig = inner
	}
	switch k := pub.(type) {
	case *rsa.PublicKey:
		if len(sig) != k.Size() {
			return nil, ErrSignature
		}
		return sig, nil
	case *ecdsa.PublicKey:
		n := (k.Curve.Params().BitSize + 7) / 8
		if len(sig) == 2*n && !validDER(sig) { // raw r||s (IEEE P1363)
			r, s := new(big.Int).SetBytes(sig[:n]), new(big.Int).SetBytes(sig[n:])
			der, _ := asn1.Marshal(struct{ R, S *big.Int }{r, s}) // two INTEGERs always marshal
			return der, nil
		}
		return sig, nil
	default:
		return nil, ErrKey
	}
}

func validDER(sig []byte) bool {
	var v struct{ R, S *big.Int }
	rest, err := asn1.Unmarshal(sig, &v)
	return err == nil && len(rest) == 0
}

// fromCMS extracts the first SignerInfo's encryptedDigest of a CMS
// ContentInfo(SignedData).
func fromCMS(b []byte) ([]byte, bool) {
	var ci struct {
		Type    asn1.ObjectIdentifier
		Content asn1.RawValue `asn1:"explicit,tag:0"`
	}
	if rest, err := asn1.Unmarshal(b, &ci); err != nil || len(rest) != 0 || !ci.Type.Equal(oidSignedData) {
		return nil, false
	}
	var sd struct {
		Version          int
		DigestAlgorithms asn1.RawValue
		ContentInfo      asn1.RawValue
		Certificates     asn1.RawValue `asn1:"optional,tag:0"`
		CRLs             asn1.RawValue `asn1:"optional,tag:1"`
		SignerInfos      []struct {
			Version            int
			SID                asn1.RawValue
			DigestAlgorithm    asn1.RawValue
			SignedAttrs        asn1.RawValue `asn1:"optional,tag:0"`
			SignatureAlgorithm asn1.RawValue
			Signature          []byte
			UnsignedAttrs      asn1.RawValue `asn1:"optional,tag:1"`
		} `asn1:"set"`
	}
	if _, err := asn1.Unmarshal(ci.Content.Bytes, &sd); err != nil || len(sd.SignerInfos) == 0 {
		return nil, false
	}
	return sd.SignerInfos[0].Signature, true
}

// Verify checks a normalised signature over digest (SHA-256) with pub.
func Verify(pub crypto.PublicKey, digest, sig []byte) error {
	switch k := pub.(type) {
	case *rsa.PublicKey:
		if rsa.VerifyPKCS1v15(k, crypto.SHA256, digest, sig) == nil {
			return nil
		}
	case *ecdsa.PublicKey:
		if ecdsa.VerifyASN1(k, digest, sig) {
			return nil
		}
	default:
		return ErrKey
	}
	return ErrSignature
}

// Digest is SHA-256 of the signed attributes (what the card signs).
func Digest(signedAttrs []byte) []byte {
	d := sha256.Sum256(signedAttrs)
	return d[:]
}

// OriginProof is BISS's "signedContents": the origin server's signature of
// SHA-256(SHA-256(content) || content), made with the origin certificate so
// BISS can check who asked for the signature.
func OriginProof(signer crypto.Signer, content []byte) ([]byte, error) {
	h := sha256.Sum256(content)
	final := sha256.Sum256(append(h[:], content...))
	return signer.Sign(nil, final[:], crypto.SHA256)
}
