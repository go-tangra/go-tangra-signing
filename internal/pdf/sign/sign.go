// Package sign applies PAdES-style CMS signatures to PDFs with the upstream
// digitorus/pdfsign engine (research D2): approval signatures (optionally
// with a visible appearance) for signers, a certification signature for
// administrator document signing, and an external two-step flow for
// qualified signatures made on a card (BISS): Prepare returns the PDF with
// a reserved signature and the digest the card must sign, Complete splices
// the card's signature in without changing any signed byte.
//
// Every signature is an incremental update, so earlier signatures stay
// valid. Revocation data is only embedded when the caller passes it — the
// engine never fetches anything over the network except the configured TSA.
package sign

import (
	"bytes"
	"crypto"
	"crypto/ecdsa"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/digitorus/pdf"
	pdfsign "github.com/digitorus/pdfsign/sign"
)

// Errors.
var (
	ErrIdentity  = errors.New("sign: a signer key and certificate are required")
	ErrKey       = errors.New("sign: unsupported key type")
	ErrPrepared  = errors.New("sign: not a prepared document")
	ErrSignature = errors.New("sign: the signature does not match the prepared digest")
	ErrTooLarge  = errors.New("sign: the signature does not fit the reserved space")
	ErrPDF       = errors.New("sign: the PDF cannot be read")
	ErrTSA       = errors.New("sign: the time-stamp authority failed")
)

// Identity is the signing key with its certificate chain (leaf first).
type Identity struct {
	Signer crypto.Signer
	Chain  []*x509.Certificate
}

// Visible is a signature appearance: a user-space rectangle on a page and a
// PNG already rotated for the page.
type Visible struct {
	Page  int
	Rect  [4]float64 // llx lly urx ury
	Image []byte
}

// TSA is an RFC 3161 time-stamp authority.
type TSA struct {
	URL, Username, Password string
}

// Options describe the signature dictionary.
type Options struct {
	Name, Reason, Location, Contact string
	Time                            time.Time
	// Certify makes a certification (DocMDP) signature that still allows
	// form filling and further signatures; it cannot be visible.
	Certify bool
	Visible *Visible
	TSA     *TSA
	CRLs    [][]byte // DER CRLs to embed (adbe-revocationInfoArchival)
}

func (o Options) data(id Identity) (pdfsign.SignData, error) {
	if id.Signer == nil || len(id.Chain) == 0 || id.Chain[0] == nil {
		return pdfsign.SignData{}, ErrIdentity
	}
	t := o.Time
	if t.IsZero() {
		t = time.Now()
	}
	d := pdfsign.SignData{
		Signature: pdfsign.SignDataSignature{
			CertType: pdfsign.ApprovalSignature,
			Info: pdfsign.SignDataSignatureInfo{
				Name: o.Name, Reason: o.Reason, Location: o.Location, ContactInfo: o.Contact, Date: t.UTC(),
			},
		},
		Signer:            id.Signer,
		DigestAlgorithm:   crypto.SHA256,
		Certificate:       id.Chain[0],
		CertificateChains: [][]*x509.Certificate{id.Chain},
	}
	if o.Certify {
		d.Signature.CertType = pdfsign.CertificationSignature
		d.Signature.DocMDPPerm = pdfsign.AllowFillingExistingFormFieldsAndSignaturesPerms
	}
	if v := o.Visible; v != nil && !o.Certify {
		d.Appearance = pdfsign.Appearance{
			Visible: true, Page: uint32(max(v.Page, 1)),
			LowerLeftX: v.Rect[0], LowerLeftY: v.Rect[1], UpperRightX: v.Rect[2], UpperRightY: v.Rect[3],
			Image: v.Image,
		}
	}
	if o.TSA != nil && o.TSA.URL != "" {
		d.TSA = pdfsign.TSA{URL: o.TSA.URL, Username: o.TSA.Username, Password: o.TSA.Password}
	}
	for _, c := range o.CRLs {
		if err := d.RevocationData.AddCRL(c); err != nil {
			return pdfsign.SignData{}, err
		}
	}
	return d, nil
}

// Sign returns pdf with one more signature appended.
func Sign(doc []byte, id Identity, o Options) ([]byte, error) {
	d, err := o.data(id)
	if err != nil {
		return nil, err
	}
	return run(doc, d)
}

func run(doc []byte, d pdfsign.SignData) (out []byte, err error) {
	// The engine and its reader panic on some malformed input.
	defer func() {
		if r := recover(); r != nil {
			out, err = nil, fmt.Errorf("%w: %v", ErrPDF, r)
		}
	}()
	rs := bytes.NewReader(doc)
	rdr, err := pdf.NewReader(rs, int64(len(doc)))
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrPDF, err)
	}
	var buf bytes.Buffer
	buf.Grow(len(doc) + 32<<10)
	if err := pdfsign.Sign(rs, &buf, rdr, int64(len(doc)), d); err != nil {
		if d.TSA.URL != "" && strings.Contains(err.Error(), "timestamp") {
			return nil, fmt.Errorf("%w: %v", ErrTSA, err)
		}
		return nil, err
	}
	return buf.Bytes(), nil
}

// Prepared is the first half of an external signature.
type Prepared struct {
	PDF         []byte // the document with the reserved (placeholder) signature
	Digest      []byte // SHA-256 of the CMS signed attributes: what the card signs
	SignedAttrs []byte // the DER signed attributes (BISS signs SHA-256 of them)
}

// capture stands in for the card: it records the digest and returns a
// placeholder of the final signature's maximum length.
type capture struct {
	pub    crypto.PublicKey
	size   int
	digest []byte
}

func (c *capture) Public() crypto.PublicKey { return c.pub }

func (c *capture) Sign(_ io.Reader, digest []byte, _ crypto.SignerOpts) ([]byte, error) {
	c.digest = append([]byte(nil), digest...)
	return bytes.Repeat([]byte{0xff}, c.size), nil
}

func sigSize(pub crypto.PublicKey) (int, error) {
	switch k := pub.(type) {
	case *rsa.PublicKey:
		return k.Size(), nil
	case *ecdsa.PublicKey:
		n := (k.Curve.Params().BitSize + 7) / 8
		return 2*(n+3) + 3, nil // SEQUENCE of two INTEGERs, worst case
	default:
		return 0, ErrKey
	}
}

// Prepare reserves an approval signature for a certificate whose key lives
// elsewhere. The TSA option is not supported (the time-stamp would cover
// the placeholder); CRLs are.
func Prepare(doc []byte, chain []*x509.Certificate, o Options) (*Prepared, error) {
	if len(chain) == 0 || chain[0] == nil {
		return nil, ErrIdentity
	}
	size, err := sigSize(chain[0].PublicKey)
	if err != nil {
		return nil, err
	}
	c := &capture{pub: chain[0].PublicKey, size: size}
	o.Certify, o.TSA = false, nil
	d, err := o.data(Identity{Signer: c, Chain: chain})
	if err != nil {
		return nil, err
	}
	out, err := run(doc, d)
	if err != nil {
		return nil, err
	}
	attrs, err := preparedAttrs(out)
	if err != nil {
		return nil, err
	}
	if sum := sha256.Sum256(attrs); !bytes.Equal(sum[:], c.digest) {
		return nil, ErrPrepared // the engine signed something else than its attributes
	}
	return &Prepared{PDF: out, Digest: c.digest, SignedAttrs: attrs}, nil
}

// preparedAttrs reads the signed attributes of the last signature's CMS.
func preparedAttrs(doc []byte) ([]byte, error) {
	start, end, err := contents(doc)
	if err != nil {
		return nil, err
	}
	raw := make([]byte, hex.DecodedLen(end-start))
	if _, err := hex.Decode(raw, doc[start:end]); err != nil {
		return nil, ErrPrepared
	}
	root, _, err := parseTLV(raw, 0)
	if err != nil {
		return nil, ErrPrepared
	}
	attrs, _, err := inspect(root.encode())
	if err != nil {
		return nil, ErrPrepared
	}
	return attrs, nil
}

var byteRangeRE = regexp.MustCompile(`/ByteRange\s*\[\s*(\d+)\s+(\d+)\s+(\d+)\s+(\d+)\s*\]`)

// contents locates the hex /Contents of the last signature: the gap of its
// ByteRange, "<hex>".
func contents(doc []byte) (start, end int, err error) {
	m := byteRangeRE.FindAllSubmatch(doc, -1)
	if len(m) == 0 {
		return 0, 0, ErrPrepared
	}
	last := m[len(m)-1]
	var br [4]int
	for i := range br {
		if br[i], err = strconv.Atoi(string(last[i+1])); err != nil {
			return 0, 0, ErrPrepared
		}
	}
	start, end = br[0]+br[1], br[2]
	if br[0] != 0 || start < 0 || end > len(doc) || end-start < 2 || br[2]+br[3] != len(doc) ||
		doc[start] != '<' || doc[end-1] != '>' {
		return 0, 0, ErrPrepared
	}
	return start + 1, end - 1, nil
}

// Complete splices the external signature into a prepared document after
// checking it against the certificate and the prepared signed attributes.
func Complete(prepared []byte, leaf *x509.Certificate, signature []byte) ([]byte, error) {
	if leaf == nil {
		return nil, ErrIdentity
	}
	return complete(prepared, signature, func(digest []byte) bool { return verifyKey(leaf.PublicKey, digest, signature) })
}

func complete(prepared, signature []byte, valid func(digest []byte) bool) ([]byte, error) {
	start, end, err := contents(prepared)
	if err != nil {
		return nil, err
	}
	raw := make([]byte, hex.DecodedLen(end-start))
	if _, err := hex.Decode(raw, prepared[start:end]); err != nil {
		return nil, ErrPrepared
	}
	root, _, err := parseTLV(raw, 0) // the rest is zero padding
	if err != nil {
		return nil, ErrPrepared
	}
	cms := root.encode()
	attrs, _, err := inspect(cms)
	if err != nil {
		return nil, ErrPrepared
	}
	digest := sha256.Sum256(attrs)
	if !valid(digest[:]) {
		return nil, ErrSignature
	}
	cms, err = replaceSignature(cms, signature)
	if err != nil {
		return nil, ErrPrepared
	}
	enc := make([]byte, hex.EncodedLen(len(cms)))
	hex.Encode(enc, cms)
	if len(enc) > end-start {
		return nil, ErrTooLarge
	}
	out := append([]byte(nil), prepared...)
	copy(out[start:], enc)
	for i := start + len(enc); i < end; i++ {
		out[i] = '0'
	}
	return out, nil
}

func verifyKey(pub crypto.PublicKey, digest, sig []byte) bool {
	switch k := pub.(type) {
	case *rsa.PublicKey:
		return rsa.VerifyPKCS1v15(k, crypto.SHA256, digest, sig) == nil
	case *ecdsa.PublicKey:
		return ecdsa.VerifyASN1(k, digest, sig)
	default:
		return false
	}
}
