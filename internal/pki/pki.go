// Package pki is the per-tenant signing PKI (research D4, FR-030–FR-036):
// the tenant signing CA (created on first need, renewed before it expires,
// its key sealed with the module KEK), personal signer certificates whose key
// is encrypted with the signer's PIN, the audit-trail system certificate and
// administrator certificates (both sealed), CRLs, and unlocking keys for a
// signing operation. Decrypted keys live only in memory for the operation.
package pki

import (
	"context"
	"crypto"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/asn1"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"math/big"
	"strings"
	"time"
	"unicode"

	transliteration "github.com/menta2k/go-transliteration"

	"github.com/go-tangra/go-tangra-signing/v4/internal/pincrypto"
	"github.com/go-tangra/go-tangra-signing/v4/internal/repo"
	"github.com/go-tangra/go-tangra-signing/v4/internal/sealed"
	"github.com/go-tangra/go-tangra-signing/v4/internal/store"
)

// Errors.
var (
	ErrNoSigner = errors.New("pki: certificate cannot sign")
)

// OIDExtKeyUsageDocumentSigning is id-kp-documentSigning (RFC 9336).
var OIDExtKeyUsageDocumentSigning = asn1.ObjectIdentifier{1, 3, 6, 1, 5, 5, 7, 3, 36}

// Organization is the O of every certificate the module issues.
const Organization = "Tangra Signing"

// Config tunes the PKI.
type Config struct {
	CAValidityYears    int
	CARenewBeforeYears int
	CertValidityYears  int
	CRLValidityDays    int
	PINIterations      int
}

// Deps wire the PKI.
type Deps struct {
	Store  repo.Store
	Sealer *sealed.Envelope
	Now    func() time.Time
	Config Config
	Rand   io.Reader
}

// PKI issues and manages signing certificates.
type PKI struct{ d Deps }

// New builds the PKI.
func New(d Deps) *PKI {
	if d.Now == nil {
		d.Now = func() time.Time { return time.Now().UTC() }
	}
	if d.Rand == nil {
		d.Rand = rand.Reader
	}
	return &PKI{d: d}
}

func (p *PKI) serial() (*big.Int, string, error) {
	n, err := rand.Int(p.d.Rand, new(big.Int).Lsh(big.NewInt(1), 127))
	if err != nil {
		return nil, "", err
	}
	n.Add(n, big.NewInt(1)) // never zero
	return n, hex.EncodeToString(n.Bytes()), nil
}

func (p *PKI) newKey() (*ecdsa.PrivateKey, []byte, error) {
	k, err := ecdsa.GenerateKey(elliptic.P256(), p.d.Rand)
	if err != nil {
		return nil, nil, err
	}
	der, err := x509.MarshalPKCS8PrivateKey(k)
	return k, der, err
}

// Transliterate turns a Cyrillic (Bulgarian) name into Latin for the
// certificate subject (FR-032); Latin names are unchanged.
func Transliterate(name string) string {
	name = strings.TrimSpace(name)
	for _, r := range name {
		if r > 127 && unicode.IsLetter(r) {
			if out := strings.TrimSpace(transliteration.ToLatin(name, "bg", false)); out != "" {
				return fixDigraphCase(out)
			}
			return name
		}
	}
	return name
}

// fixDigraphCase turns the upper-case digraphs of a capitalised word into
// title case ("CHoveshki" → "Choveshki", "SHTerev" → "Shterev"); words in
// capitals stay as they are ("OOD").
func fixDigraphCase(s string) string {
	words := strings.Split(s, " ")
	for i, w := range words {
		r := []rune(w)
		hasLower := false
		for _, c := range r {
			if unicode.IsLower(c) {
				hasLower = true
				break
			}
		}
		if !hasLower || len(r) < 3 {
			continue
		}
		k := 0
		for k < len(r) && unicode.IsUpper(r[k]) {
			k++
		}
		for j := 1; j < k; j++ {
			r[j] = unicode.ToLower(r[j])
		}
		words[i] = string(r)
	}
	return strings.Join(words, " ")
}

func shortTenant(t string) string {
	if len(t) >= 8 {
		return t[:8]
	}
	return t
}

// Parse parses a stored certificate.
func Parse(c store.Certificate) (*x509.Certificate, error) { return x509.ParseCertificate(c.CertDER) }

// Fingerprint is the SHA-256 of the DER certificate (hex).
func Fingerprint(c store.Certificate) string {
	sum := sha256.Sum256(c.CertDER)
	return hex.EncodeToString(sum[:])
}

// ---------------------------------------------------------------- CA

// EnsureCA returns the tenant's current CA, creating it on first need and
// renewing it when less than CARenewBeforeYears remain. Concurrent creation
// loses to the unique index and re-reads.
func (p *PKI) EnsureCA(ctx context.Context, tenant string) (store.Certificate, error) {
	now := p.d.Now()
	cur, err := p.d.Store.CurrentCA(ctx, tenant)
	if err == nil && now.AddDate(p.d.Config.CARenewBeforeYears, 0, 0).Before(cur.NotAfter) {
		return cur, nil
	}
	if err != nil && !errors.Is(err, repo.ErrNotFound) {
		return store.Certificate{}, err
	}
	var prev *store.Certificate
	if err == nil {
		prev = &cur
	}
	created, cerr := p.createCA(ctx, tenant, prev)
	if cerr == nil {
		return created, nil
	}
	if errors.Is(cerr, repo.ErrConflict) { // another request created or renewed it meanwhile
		return p.d.Store.CurrentCA(ctx, tenant)
	}
	return store.Certificate{}, cerr
}

func (p *PKI) createCA(ctx context.Context, tenant string, prev *store.Certificate) (store.Certificate, error) {
	now := p.d.Now()
	key, der, err := p.newKey()
	if err != nil {
		return store.Certificate{}, err
	}
	sn, serial, err := p.serial()
	if err != nil {
		return store.Certificate{}, err
	}
	cn := "Tangra Tenant Signing CA " + shortTenant(tenant)
	tpl := &x509.Certificate{
		SerialNumber: sn, Subject: pkix.Name{CommonName: cn, Organization: []string{Organization}},
		NotBefore: now.Add(-time.Minute), NotAfter: now.AddDate(p.d.Config.CAValidityYears, 0, 0),
		IsCA: true, BasicConstraintsValid: true, MaxPathLenZero: true,
		KeyUsage: x509.KeyUsageCertSign | x509.KeyUsageCRLSign | x509.KeyUsageDigitalSignature,
	}
	certDER, err := x509.CreateCertificate(p.d.Rand, tpl, tpl, &key.PublicKey, key)
	if err != nil {
		return store.Certificate{}, err
	}
	c := store.Certificate{ID: store.NewID(), TenantID: tenant, Kind: store.KindCA, SubjectCN: cn, Serial: serial,
		NotBefore: tpl.NotBefore, NotAfter: tpl.NotAfter, CertDER: certDER, KeyProtection: store.KeySealed,
		Status: store.CertActive, CreatedAt: now, CreatedBy: "system"}
	if c.KeyBlob, err = p.d.Sealer.Seal(der, sealed.ADCertKey(c.ID)); err != nil {
		return store.Certificate{}, err
	}
	err = p.d.Store.Tx(ctx, tenant, func(r repo.Store) error {
		if prev != nil {
			old := *prev
			old.SupersededBy = &c.ID
			if err := r.UpdateCertificate(ctx, old); err != nil {
				return err
			}
		}
		return r.CreateCertificate(ctx, c)
	})
	return c, err
}

// caKey unseals a CA (or any sealed certificate's) key.
func (p *PKI) sealedKey(c store.Certificate) (crypto.Signer, error) {
	if c.KeyProtection != store.KeySealed {
		return nil, ErrNoSigner
	}
	der, err := p.d.Sealer.Open(c.KeyBlob, sealed.ADCertKey(c.ID))
	if err != nil {
		return nil, err
	}
	return parseKey(der)
}

func parseKey(der []byte) (crypto.Signer, error) {
	k, err := x509.ParsePKCS8PrivateKey(der)
	if err != nil {
		return nil, err
	}
	s, ok := k.(crypto.Signer)
	if !ok {
		return nil, ErrNoSigner
	}
	return s, nil
}

// issue signs tpl with the tenant's current CA.
func (p *PKI) issue(ctx context.Context, tenant string, tpl *x509.Certificate, pub crypto.PublicKey) (store.Certificate, []byte, error) {
	ca, err := p.EnsureCA(ctx, tenant)
	if err != nil {
		return store.Certificate{}, nil, err
	}
	caCert, err := Parse(ca)
	if err != nil {
		return store.Certificate{}, nil, err
	}
	caKey, err := p.sealedKey(ca)
	if err != nil {
		return store.Certificate{}, nil, err
	}
	if tpl.NotAfter.After(caCert.NotAfter) {
		tpl.NotAfter = caCert.NotAfter // never outlive the issuer
	}
	der, err := x509.CreateCertificate(p.d.Rand, tpl, caCert, pub, caKey)
	return ca, der, err
}

// ---------------------------------------------------------------- signer certificates

// SignerInput is a personal certificate request.
type SignerInput struct {
	Tenant, UserID, Name, Email, PIN, CreatedBy string
}

// IssueSigner creates the user's personal certificate with a PIN-protected key.
func (p *PKI) IssueSigner(ctx context.Context, in SignerInput) (store.Certificate, error) {
	now := p.d.Now()
	key, der, err := p.newKey()
	if err != nil {
		return store.Certificate{}, err
	}
	sn, serial, err := p.serial()
	if err != nil {
		return store.Certificate{}, err
	}
	cn := Transliterate(in.Name)
	if cn == "" {
		cn = in.Email
	}
	tpl := &x509.Certificate{
		SerialNumber: sn, Subject: pkix.Name{CommonName: cn, Organization: []string{Organization}},
		EmailAddresses: []string{in.Email},
		NotBefore:      now.Add(-time.Minute), NotAfter: now.AddDate(p.d.Config.CertValidityYears, 0, 0),
		KeyUsage:           x509.KeyUsageDigitalSignature | x509.KeyUsageContentCommitment,
		ExtKeyUsage:        []x509.ExtKeyUsage{x509.ExtKeyUsageEmailProtection},
		UnknownExtKeyUsage: []asn1.ObjectIdentifier{OIDExtKeyUsageDocumentSigning},
	}
	ca, certDER, err := p.issue(ctx, in.Tenant, tpl, &key.PublicKey)
	if err != nil {
		return store.Certificate{}, err
	}
	owner := in.UserID
	c := store.Certificate{ID: store.NewID(), TenantID: in.Tenant, Kind: store.KindSigner, IssuerID: &ca.ID, OwnerUserID: &owner,
		SubjectCN: cn, Email: in.Email, Serial: serial, NotBefore: tpl.NotBefore, NotAfter: tpl.NotAfter, CertDER: certDER,
		KeyProtection: store.KeyPIN, Status: store.CertActive, CreatedAt: now, CreatedBy: in.CreatedBy}
	if c.KeyBlob, err = pincrypto.Seal(der, in.PIN, sealed.ADCertKey(c.ID), p.d.Config.PINIterations); err != nil {
		return store.Certificate{}, err
	}
	return c, p.d.Store.CreateCertificate(ctx, c)
}

// UnlockSigner decrypts a signer certificate's key with the PIN
// (pincrypto.ErrPIN on a wrong PIN).
func (p *PKI) UnlockSigner(c store.Certificate, pin string) (crypto.Signer, error) {
	if c.KeyProtection != store.KeyPIN {
		return nil, ErrNoSigner
	}
	der, err := pincrypto.Open(c.KeyBlob, pin, sealed.ADCertKey(c.ID))
	if err != nil {
		return nil, err
	}
	return parseKey(der)
}

// ChangePIN re-encrypts a signer key under a new PIN (the old one is required).
func (p *PKI) ChangePIN(c store.Certificate, oldPIN, newPIN string) (store.Certificate, error) {
	if c.KeyProtection != store.KeyPIN {
		return store.Certificate{}, ErrNoSigner
	}
	der, err := pincrypto.Open(c.KeyBlob, oldPIN, sealed.ADCertKey(c.ID))
	if err != nil {
		return store.Certificate{}, err
	}
	if c.KeyBlob, err = pincrypto.Seal(der, newPIN, sealed.ADCertKey(c.ID), p.d.Config.PINIterations); err != nil {
		return store.Certificate{}, err
	}
	return c, nil
}

// ---------------------------------------------------------------- sealed certificates

// IssueAdmin creates an administrator signing certificate (sealed key, FR-038).
func (p *PKI) IssueAdmin(ctx context.Context, tenant, cn, email string, years int, createdBy string) (store.Certificate, error) {
	now := p.d.Now()
	key, der, err := p.newKey()
	if err != nil {
		return store.Certificate{}, err
	}
	sn, serial, err := p.serial()
	if err != nil {
		return store.Certificate{}, err
	}
	tpl := &x509.Certificate{
		SerialNumber: sn, Subject: pkix.Name{CommonName: cn, Organization: []string{Organization}},
		NotBefore: now.Add(-time.Minute), NotAfter: now.AddDate(years, 0, 0),
		KeyUsage:           x509.KeyUsageDigitalSignature | x509.KeyUsageContentCommitment,
		UnknownExtKeyUsage: []asn1.ObjectIdentifier{OIDExtKeyUsageDocumentSigning},
	}
	if email != "" {
		tpl.EmailAddresses = []string{email}
	}
	ca, certDER, err := p.issue(ctx, tenant, tpl, &key.PublicKey)
	if err != nil {
		return store.Certificate{}, err
	}
	owner := createdBy
	c := store.Certificate{ID: store.NewID(), TenantID: tenant, Kind: store.KindAdmin, IssuerID: &ca.ID, OwnerUserID: &owner,
		SubjectCN: cn, Email: email, Serial: serial, NotBefore: tpl.NotBefore, NotAfter: tpl.NotAfter, CertDER: certDER,
		KeyProtection: store.KeySealed, Status: store.CertActive, CreatedAt: now, CreatedBy: createdBy}
	if c.KeyBlob, err = p.d.Sealer.Seal(der, sealed.ADCertKey(c.ID)); err != nil {
		return store.Certificate{}, err
	}
	return c, p.d.Store.CreateCertificate(ctx, c)
}

// SystemCertificate returns the current CA's audit-trail certificate and its
// key, creating it on first need.
func (p *PKI) SystemCertificate(ctx context.Context, tenant string) (store.Certificate, crypto.Signer, error) {
	ca, err := p.EnsureCA(ctx, tenant)
	if err != nil {
		return store.Certificate{}, nil, err
	}
	c, err := p.d.Store.SystemCertificate(ctx, tenant, ca.ID)
	if errors.Is(err, repo.ErrNotFound) {
		if c, err = p.createSystem(ctx, tenant); errors.Is(err, repo.ErrConflict) {
			c, err = p.d.Store.SystemCertificate(ctx, tenant, ca.ID)
		}
	}
	if err != nil {
		return store.Certificate{}, nil, err
	}
	k, err := p.sealedKey(c)
	return c, k, err
}

func (p *PKI) createSystem(ctx context.Context, tenant string) (store.Certificate, error) {
	now := p.d.Now()
	key, der, err := p.newKey()
	if err != nil {
		return store.Certificate{}, err
	}
	sn, serial, err := p.serial()
	if err != nil {
		return store.Certificate{}, err
	}
	cn := "Tangra Signing Audit Trail " + shortTenant(tenant)
	tpl := &x509.Certificate{
		SerialNumber: sn, Subject: pkix.Name{CommonName: cn, Organization: []string{Organization}},
		NotBefore: now.Add(-time.Minute), NotAfter: now.AddDate(p.d.Config.CAValidityYears, 0, 0),
		KeyUsage:           x509.KeyUsageDigitalSignature | x509.KeyUsageContentCommitment,
		UnknownExtKeyUsage: []asn1.ObjectIdentifier{OIDExtKeyUsageDocumentSigning},
	}
	ca, certDER, err := p.issue(ctx, tenant, tpl, &key.PublicKey)
	if err != nil {
		return store.Certificate{}, err
	}
	c := store.Certificate{ID: store.NewID(), TenantID: tenant, Kind: store.KindSystem, IssuerID: &ca.ID, SubjectCN: cn, Serial: serial,
		NotBefore: tpl.NotBefore, NotAfter: tpl.NotAfter, CertDER: certDER, KeyProtection: store.KeySealed, Status: store.CertActive,
		CreatedAt: now, CreatedBy: "system"}
	if c.KeyBlob, err = p.d.Sealer.Seal(der, sealed.ADCertKey(c.ID)); err != nil {
		return store.Certificate{}, err
	}
	return c, p.d.Store.CreateCertificate(ctx, c)
}

// SealedSigner unseals an administrator or system certificate's key.
func (p *PKI) SealedSigner(c store.Certificate) (crypto.Signer, error) { return p.sealedKey(c) }

// Chain returns [certificate, issuer] as parsed certificates.
func (p *PKI) Chain(ctx context.Context, c store.Certificate) ([]*x509.Certificate, error) {
	leaf, err := Parse(c)
	if err != nil {
		return nil, err
	}
	if c.IssuerID == nil {
		return []*x509.Certificate{leaf}, nil
	}
	issuer, err := p.d.Store.GetCertificate(ctx, c.TenantID, *c.IssuerID)
	if err != nil {
		return nil, err
	}
	ic, err := Parse(issuer)
	if err != nil {
		return nil, err
	}
	return []*x509.Certificate{leaf, ic}, nil
}

// ---------------------------------------------------------------- revocation

// Revocation reasons (RFC 5280) accepted by the API.
var ReasonCodes = map[string]int{"unspecified": 0, "key_compromise": 1, "affiliation_changed": 3, "superseded": 4,
	"cessation_of_operation": 5}

// Revoke marks a certificate revoked and republishes its issuer's CRL.
// Revoking an already revoked certificate is a no-op.
func (p *PKI) Revoke(ctx context.Context, c store.Certificate, reason string) (store.Certificate, error) {
	if _, ok := ReasonCodes[reason]; !ok {
		return store.Certificate{}, fmt.Errorf("pki: unknown revocation reason %q", reason)
	}
	if c.Kind == store.KindCA {
		return store.Certificate{}, fmt.Errorf("pki: a CA is renewed, not revoked")
	}
	if c.Status == store.CertRevoked {
		return c, nil
	}
	now := p.d.Now()
	c.Status, c.RevokedAt, c.RevocationReason = store.CertRevoked, &now, reason
	if err := p.d.Store.UpdateCertificate(ctx, c); err != nil {
		return store.Certificate{}, err
	}
	if c.IssuerID != nil {
		if err := p.PublishCRL(ctx, c.TenantID, *c.IssuerID); err != nil {
			return c, err
		}
	}
	return c, nil
}

// PublishCRL regenerates and stores the CRL of a CA.
func (p *PKI) PublishCRL(ctx context.Context, tenant, caID string) error {
	ca, err := p.d.Store.GetCertificate(ctx, tenant, caID)
	if err != nil {
		return err
	}
	caCert, err := Parse(ca)
	if err != nil {
		return err
	}
	key, err := p.sealedKey(ca)
	if err != nil {
		return err
	}
	issued, err := p.d.Store.IssuedBy(ctx, tenant, caID)
	if err != nil {
		return err
	}
	now := p.d.Now()
	var entries []x509.RevocationListEntry
	for _, c := range issued {
		if c.Status != store.CertRevoked || c.RevokedAt == nil {
			continue
		}
		sn, ok := new(big.Int).SetString(c.Serial, 16)
		if !ok {
			continue
		}
		entries = append(entries, x509.RevocationListEntry{SerialNumber: sn, RevocationTime: *c.RevokedAt, ReasonCode: ReasonCodes[c.RevocationReason]})
	}
	next := now.AddDate(0, 0, p.d.Config.CRLValidityDays)
	der, err := x509.CreateRevocationList(p.d.Rand, &x509.RevocationList{Number: big.NewInt(now.UnixNano()), ThisUpdate: now,
		NextUpdate: next, RevokedCertificateEntries: entries}, caCert, key)
	if err != nil {
		return err
	}
	ca.CRLDER, ca.CRLNextUpdate = der, &next
	return p.d.Store.UpdateCertificate(ctx, ca)
}
