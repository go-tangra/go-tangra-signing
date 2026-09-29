// Package verify checks the CMS signatures of a PDF (US7): for every
// signature field it recomputes the ByteRange digest, verifies the CMS
// signature and its RFC 3161 time-stamp, reports whether later revisions
// were appended after it, and tells which trust store (the tenant signing
// CA, the qualified roots, …) its certificate chains to at signing time.
// Nothing is fetched over the network; revocation is answered by the
// caller (the module's own CRL state).
package verify

import (
	"bytes"
	"crypto/x509"
	"encoding/asn1"
	"errors"
	"fmt"
	"slices"
	"sort"
	"time"

	"github.com/digitorus/pdf"
	"github.com/digitorus/pkcs7"
	"github.com/digitorus/timestamp"
)

// MaxSignatures bounds the signature fields examined in one document.
const MaxSignatures = 100

const maxFieldDepth = 16

// ErrPDF is returned for documents that cannot be read.
var ErrPDF = errors.New("verify: the PDF cannot be read")

var (
	oidSigningTime = asn1.ObjectIdentifier{1, 2, 840, 113549, 1, 9, 5}
	oidTimeStamp   = asn1.ObjectIdentifier{1, 2, 840, 113549, 1, 9, 16, 2, 14}
)

// Time sources of Signature.SignedAt.
const (
	TimeStamp = "timestamp" // a valid RFC 3161 token over the signature
	Claimed   = "claimed"   // the signer's own clock (signingTime / M)
	Now       = "now"       // no time at all: checked at verification time
)

// Options control trust decisions.
type Options struct {
	// Pools are named trust stores; Signature.Trust is the name of the
	// first (in name order) whose roots the signer chains to.
	Pools map[string]*x509.CertPool
	// Revoked reports whether (and since when) a certificate is revoked.
	Revoked func(*x509.Certificate) (time.Time, bool)
	// Now overrides the clock (tests).
	Now time.Time
}

// Signature is the outcome for one signature field.
type Signature struct {
	Field                           string
	Name, Reason, Location, Contact string
	Certification                   bool // DocMDP certification signature

	SignedAt   time.Time
	TimeSource string

	Intact  bool   // digest and CMS signature verify
	Problem string // why Intact is false

	CoversWhole bool // no revision was appended after this signature
	Revision    int  // byte length of the revision it signs

	Signer *x509.Certificate
	Chain  []*x509.Certificate // as validated (leaf first), or as embedded

	Trust        string // name of the pool it chains to ("" = untrusted)
	TrustProblem string

	Revoked   bool
	RevokedAt time.Time
}

// Valid is the verdict shown to users: intact, trusted and not revoked
// before it was made.
func (s Signature) Valid() bool {
	return s.Intact && s.Trust != "" && (!s.Revoked || s.RevokedAt.After(s.SignedAt))
}

// Verify returns the document's signatures in signing order.
func Verify(doc []byte, o Options) (sigs []Signature, err error) {
	defer func() {
		if r := recover(); r != nil {
			sigs, err = nil, fmt.Errorf("%w: %v", ErrPDF, r)
		}
	}()
	rdr, err := pdf.NewReader(bytes.NewReader(doc), int64(len(doc)))
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrPDF, err)
	}
	fields := rdr.Trailer().Key("Root").Key("AcroForm").Key("Fields")
	var found []field
	collect(fields, "", "", 0, &found)
	now := o.Now
	if now.IsZero() {
		now = time.Now()
	}
	for _, f := range found {
		sigs = append(sigs, check(doc, f, o, now))
	}
	sort.SliceStable(sigs, func(i, j int) bool { return sigs[i].Revision < sigs[j].Revision })
	return sigs, nil
}

type field struct {
	name string
	v    pdf.Value
}

func collect(arr pdf.Value, prefix, inheritedFT string, depth int, out *[]field) {
	if depth > maxFieldDepth || arr.Kind() != pdf.Array {
		return
	}
	for i := 0; i < arr.Len() && len(*out) < MaxSignatures; i++ {
		f := arr.Index(i)
		if f.Kind() != pdf.Dict {
			continue
		}
		name := f.Key("T").Text()
		if prefix != "" && name != "" {
			name = prefix + "." + name
		} else if name == "" {
			name = prefix
		}
		ft := inheritedFT
		if n := f.Key("FT").Name(); n != "" {
			ft = n
		}
		if ft == "Sig" {
			if v := f.Key("V"); v.Kind() == pdf.Dict {
				*out = append(*out, field{name: name, v: v})
			}
		}
		collect(f.Key("Kids"), name, ft, depth+1, out)
	}
}

func check(doc []byte, f field, o Options, now time.Time) Signature {
	v := f.v
	s := Signature{
		Field: f.name, Name: v.Key("Name").Text(), Reason: v.Key("Reason").Text(),
		Location: v.Key("Location").Text(), Contact: v.Key("ContactInfo").Text(),
		TimeSource: Now, SignedAt: now,
	}
	refs := v.Key("Reference")
	for i := 0; i < refs.Len(); i++ {
		if refs.Index(i).Key("TransformMethod").Name() == "DocMDP" {
			s.Certification = true
		}
	}
	br, ok := byteRange(v.Key("ByteRange"), len(doc))
	if !ok {
		s.Problem = "malformed byte range"
		return s
	}
	s.Revision = br[2] + br[3]
	s.CoversWhole = len(bytes.TrimSpace(doc[s.Revision:])) == 0

	p7, err := pkcs7.Parse([]byte(v.Key("Contents").RawString()))
	if err != nil || len(p7.Signers) != 1 {
		s.Problem = "not a CMS signature"
		return s
	}
	s.Signer = p7.GetOnlySigner()
	s.Chain = p7.Certificates
	if s.Signer == nil {
		s.Problem = "the signer certificate is missing"
		return s
	}
	signed := make([]byte, 0, br[1]+br[3])
	signed = append(signed, doc[br[0]:br[0]+br[1]]...)
	signed = append(signed, doc[br[2]:br[2]+br[3]]...)
	p7.Content = signed

	// The signing time: a valid time-stamp token wins over the signer's clock.
	var claimed time.Time
	if err := p7.UnmarshalSignedAttribute(oidSigningTime, &claimed); err == nil && !claimed.IsZero() {
		s.SignedAt, s.TimeSource = claimed, Claimed
	} else if m := v.Key("M").Text(); m != "" {
		if t, ok := pdfDate(m); ok {
			s.SignedAt, s.TimeSource = t, Claimed
		}
	}
	if t, ok := tokenTime(p7); ok {
		s.SignedAt, s.TimeSource = t, TimeStamp
	}

	if err := p7.Verify(); err != nil {
		s.Problem = "the document was changed after signing or the signature is corrupt"
		return s
	}
	s.Intact = true

	trust(&s, p7, o)
	if o.Revoked != nil {
		s.RevokedAt, s.Revoked = o.Revoked(s.Signer)
	}
	return s
}

func byteRange(v pdf.Value, size int) ([4]int, bool) {
	var br [4]int
	if v.Kind() != pdf.Array || v.Len() != 4 {
		return br, false
	}
	for i := range br {
		n := v.Index(i)
		if n.Kind() != pdf.Integer {
			return br, false
		}
		br[i] = int(n.Int64())
	}
	ok := br[0] == 0 && br[1] > 0 && br[2] > br[1] && br[3] >= 0 && br[2]+br[3] <= size
	return br, ok
}

// tokenTime returns the time of a valid RFC 3161 token over the signature.
func tokenTime(p7 *pkcs7.PKCS7) (time.Time, bool) {
	si := p7.Signers[0]
	for _, a := range si.UnauthenticatedAttributes {
		if !a.Type.Equal(oidTimeStamp) {
			continue
		}
		ts, err := timestamp.Parse(a.Value.Bytes)
		if err != nil {
			return time.Time{}, false
		}
		if !ts.HashAlgorithm.Available() {
			return time.Time{}, false
		}
		h := ts.HashAlgorithm.New()
		h.Write(si.EncryptedDigest)
		if !bytes.Equal(h.Sum(nil), ts.HashedMessage) {
			return time.Time{}, false
		}
		return ts.Time, true
	}
	return time.Time{}, false
}

func trust(s *Signature, p7 *pkcs7.PKCS7, o Options) {
	names := make([]string, 0, len(o.Pools))
	for n := range o.Pools {
		names = append(names, n)
	}
	slices.Sort(names)
	inter := x509.NewCertPool()
	for _, c := range p7.Certificates {
		if !c.Equal(s.Signer) {
			inter.AddCert(c)
		}
	}
	s.TrustProblem = "no trusted issuer"
	for _, n := range names {
		chains, err := s.Signer.Verify(x509.VerifyOptions{
			Roots: o.Pools[n], Intermediates: inter, CurrentTime: s.SignedAt,
			KeyUsages: []x509.ExtKeyUsage{x509.ExtKeyUsageAny},
		})
		if err != nil {
			s.TrustProblem = err.Error()
			continue
		}
		s.Trust, s.TrustProblem, s.Chain = n, "", chains[0]
		return
	}
}

// pdfDate parses a PDF date string (D:YYYYMMDDHHmmSS+HH'mm').
func pdfDate(v string) (time.Time, bool) {
	if len(v) >= 2 && v[:2] == "D:" {
		v = v[2:]
	}
	for _, layout := range []string{"20060102150405-07'00'", "20060102150405Z07'00'", "20060102150405Z", "20060102150405"} {
		if t, err := time.Parse(layout, v); err == nil {
			return t, true
		}
	}
	return time.Time{}, false
}
