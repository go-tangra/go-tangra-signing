// Package documents is administrator document signing and signature
// verification (US7).
//
// Signing (FR-038, route permission certificates:manage) applies an
// administrator certificate's sealed key to an uploaded PDF or a stored
// submission version: a certification signature (DocMDP, form filling and
// further signatures allowed) on an unsigned document, an approval signature
// on an already signed one. An optional RFC 3161 time-stamp uses a TSA whose
// credentials are a Warden secret read on behalf of the signed-in user; the
// TSA address must be public (no loopback/private/link-local targets). The
// result is kept for one hour for download.
//
// Verification (FR-039, research D11) checks every signature against the
// tenant's signing CAs (current and previous) and the configured qualified
// roots; revocation of tenant certificates comes from the module's records,
// foreign certificates are "unknown".
package documents

import (
	"bytes"
	"context"
	"crypto/x509"
	"errors"
	"io"
	"net"
	"net/url"
	"strings"
	"time"

	"github.com/go-tangra/go-tangra-signing/v4/internal/apperr"
	"github.com/go-tangra/go-tangra-signing/v4/internal/audit"
	"github.com/go-tangra/go-tangra-signing/v4/internal/authz"
	"github.com/go-tangra/go-tangra-signing/v4/internal/blob"
	"github.com/go-tangra/go-tangra-signing/v4/internal/pdf/limits"
	"github.com/go-tangra/go-tangra-signing/v4/internal/pdf/sign"
	"github.com/go-tangra/go-tangra-signing/v4/internal/pdf/verify"
	"github.com/go-tangra/go-tangra-signing/v4/internal/pki"
	"github.com/go-tangra/go-tangra-signing/v4/internal/repo"
	"github.com/go-tangra/go-tangra-signing/v4/internal/store"
	"github.com/go-tangra/go-tangra-signing/v4/internal/submissions"
	"github.com/go-tangra/go-tangra-signing/v4/internal/warden"
)

// Retention of a signed document for download.
const Retention = time.Hour

// Trust pool names (Signature.Method).
const (
	PoolTenant    = "tenant"
	PoolQualified = "qualified"
)

// Resolver resolves a host name (net.DefaultResolver in production).
type Resolver interface {
	LookupIPAddr(ctx context.Context, host string) ([]net.IPAddr, error)
}

// Deps wire the service.
type Deps struct {
	Store  repo.Store
	Blob   blob.Store
	Audit  audit.Recorder
	PKI    *pki.PKI
	Warden warden.Client
	Subs   *submissions.Service
	Roots  *x509.CertPool // qualified / platform roots (nil = tenant CAs only)
	Limits limits.Limits
	Now    func() time.Time
	// Resolver checks TSA hosts; AllowPrivateTSA lifts the public-address
	// rule (tests with a local TSA only).
	Resolver        Resolver
	AllowPrivateTSA bool
}

// Service signs and verifies documents.
type Service struct{ d Deps }

// New builds the service.
func New(d Deps) *Service {
	if d.Now == nil {
		d.Now = func() time.Time { return time.Now().UTC() }
	}
	if d.Resolver == nil {
		d.Resolver = net.DefaultResolver
	}
	if d.Limits.MaxBytes <= 0 {
		d.Limits.MaxBytes = 50 << 20
	}
	return &Service{d: d}
}

func reasonOf(err error) string {
	if e, ok := apperr.As(err); ok {
		return e.Reason
	}
	return "error"
}

func (s *Service) record(ctx context.Context, subj authz.Subjects, t audit.EventType, kind, id, outcome, reason string, detail map[string]any) {
	audit.Emit(ctx, s.d.Audit, audit.Event{TenantID: subj.TenantID, EventType: t, ActorKind: audit.ActorOf(subj.ActorKind),
		ActorID: subj.ActorID(), SubjectKind: kind, SubjectID: id, Outcome: outcome, Reason: reason, Details: detail})
}

// Source is the document to act on: an upload, or a stored submission
// version (Version < 0 = current/final).
type Source struct {
	PDF          []byte
	SubmissionID string
	Version      int
}

func (s *Service) load(ctx context.Context, subj authz.Subjects, src Source) ([]byte, string, error) {
	if src.SubmissionID == "" {
		if len(src.PDF) == 0 {
			return nil, "", apperr.Validation.WithField("file")
		}
		if int64(len(src.PDF)) > s.d.Limits.MaxBytes {
			return nil, "", apperr.PayloadTooLarge.WithField("file")
		}
		return src.PDF, "upload", nil
	}
	rc, _, _, err := s.d.Subs.Document(ctx, subj, src.SubmissionID, src.Version)
	if err != nil {
		return nil, "", err
	}
	defer func() { _ = rc.Close() }()
	data, err := io.ReadAll(io.LimitReader(rc, s.d.Limits.MaxBytes+1))
	if err != nil {
		return nil, "", err
	}
	if int64(len(data)) > s.d.Limits.MaxBytes {
		return nil, "", apperr.PayloadTooLarge
	}
	return data, "submission", nil
}

// ---------------------------------------------------------------- sign

// SignInput is an administrator signing request.
type SignInput struct {
	Source
	CertificateID string
	Reason        string
	Location      string
	Contact       string
	TSAURL        string
	TSASecretRef  string // Warden secret id (username/password, optional host URL)
}

func clean(v string, max int, field string) (string, error) {
	v = strings.TrimSpace(v)
	if len([]rune(v)) > max || strings.ContainsAny(v, "\r\n") {
		return "", apperr.Validation.WithField(field)
	}
	return v, nil
}

// Sign signs a document and returns the id to download it within Retention.
func (s *Service) Sign(ctx context.Context, subj authz.Subjects, in SignInput) (string, time.Time, error) {
	id, exp, err := s.sign(ctx, subj, in)
	if err != nil {
		s.record(ctx, subj, audit.DocumentSign, audit.SubjectCertificate, in.CertificateID, audit.OutcomeRefused, reasonOf(err), nil)
		return "", time.Time{}, err
	}
	s.record(ctx, subj, audit.DocumentSign, audit.SubjectDocument, id, audit.OutcomeOK, "",
		map[string]any{"certificate_id": in.CertificateID, "tsa": in.TSAURL != "" || in.TSASecretRef != "", "source": sourceName(in.Source)})
	return id, exp, nil
}

func sourceName(src Source) string {
	if src.SubmissionID != "" {
		return "submission"
	}
	return "upload"
}

func (s *Service) sign(ctx context.Context, subj authz.Subjects, in SignInput) (string, time.Time, error) {
	var err error
	if in.Reason, err = clean(in.Reason, 200, "reason"); err != nil {
		return "", time.Time{}, err
	}
	if in.Location, err = clean(in.Location, 200, "location"); err != nil {
		return "", time.Time{}, err
	}
	if in.Contact, err = clean(in.Contact, 200, "contact"); err != nil {
		return "", time.Time{}, err
	}
	cert, err := s.d.Store.GetCertificate(ctx, subj.TenantID, in.CertificateID)
	if errors.Is(err, repo.ErrNotFound) {
		return "", time.Time{}, apperr.NotFound.WithField("certificate_id")
	} else if err != nil {
		return "", time.Time{}, err
	}
	now := s.d.Now()
	if cert.Kind != store.KindAdmin || cert.KeyProtection != store.KeySealed {
		return "", time.Time{}, apperr.CertificateUnusable.WithField("certificate_id")
	}
	if !cert.Usable(now) {
		return "", time.Time{}, apperr.CertificateUnusable.WithField("certificate_id")
	}
	doc, _, err := s.load(ctx, subj, in.Source)
	if err != nil {
		return "", time.Time{}, err
	}
	info, err := limits.Open(ctx, doc, s.d.Limits)
	if err != nil {
		return "", time.Time{}, apperr.InvalidPDF
	}
	tsa, err := s.tsa(ctx, in)
	if err != nil {
		return "", time.Time{}, err
	}
	key, err := s.d.PKI.SealedSigner(cert)
	if err != nil {
		return "", time.Time{}, err
	}
	chain, err := s.d.PKI.Chain(ctx, cert)
	if err != nil {
		return "", time.Time{}, err
	}
	out, err := sign.Sign(doc, sign.Identity{Signer: key, Chain: chain}, sign.Options{
		Name: cert.SubjectCN, Reason: in.Reason, Location: in.Location, Contact: in.Contact, Time: now,
		Certify: !info.Signed, TSA: tsa,
	})
	switch {
	case errors.Is(err, sign.ErrTSA):
		return "", time.Time{}, apperr.TSAFailed
	case errors.Is(err, sign.ErrPDF):
		return "", time.Time{}, apperr.InvalidPDF
	case err != nil:
		return "", time.Time{}, err
	}
	id := store.NewID()
	if _, err := s.d.Blob.Put(ctx, blob.Upload(subj.TenantID, id), bytes.NewReader(out), int64(len(out)), "application/pdf"); err != nil {
		return "", time.Time{}, err
	}
	certID, actor := cert.ID, subj.ActorID()
	_ = s.d.Store.AddEvent(ctx, store.Event{ID: store.NewID(), TenantID: subj.TenantID, CertificateID: &certID, ActorUserID: &actor,
		Type: "document.signed", Meta: map[string]any{"certify": !info.Signed, "tsa": tsa != nil}, At: now})
	return id, now.Add(Retention), nil
}

// tsa resolves the time-stamp authority of a request (nil = none).
func (s *Service) tsa(ctx context.Context, in SignInput) (*sign.TSA, error) {
	if in.TSAURL == "" && in.TSASecretRef == "" {
		return nil, nil
	}
	t := &sign.TSA{URL: strings.TrimSpace(in.TSAURL)}
	if in.TSASecretRef != "" {
		creds, err := s.d.Warden.Credentials(ctx, in.TSASecretRef)
		switch {
		case errors.Is(err, warden.ErrNotFound), errors.Is(err, warden.ErrEmptyRef):
			return nil, apperr.Validation.WithField("tsa_secret_ref")
		case errors.Is(err, warden.ErrForbidden):
			return nil, apperr.Forbidden.WithField("tsa_secret_ref")
		case err != nil:
			return nil, apperr.TemporarilyUnavailable.WithField("tsa_secret_ref")
		}
		t.Username, t.Password = creds.Username, creds.Password
		if t.URL == "" {
			t.URL = creds.HostURL
		}
	}
	if err := s.checkTSA(ctx, t.URL); err != nil {
		return nil, err
	}
	return t, nil
}

// checkTSA accepts an http(s) URL without credentials whose host resolves
// only to public addresses (no SSRF into the platform network).
func (s *Service) checkTSA(ctx context.Context, raw string) error {
	bad := apperr.Validation.WithField("tsa_url")
	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Host == "" || u.User != nil || len(raw) > 500 {
		return bad
	}
	if s.d.AllowPrivateTSA {
		return nil
	}
	host := u.Hostname()
	var ips []net.IP
	if ip := net.ParseIP(host); ip != nil {
		ips = []net.IP{ip}
	} else {
		addrs, err := s.d.Resolver.LookupIPAddr(ctx, host)
		if err != nil || len(addrs) == 0 {
			return bad
		}
		for _, a := range addrs {
			ips = append(ips, a.IP)
		}
	}
	for _, ip := range ips {
		if ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() || ip.IsUnspecified() ||
			ip.IsMulticast() || ip.IsInterfaceLocalMulticast() {
			return bad
		}
	}
	return nil
}

// Download opens a signed document of the caller's tenant within Retention.
func (s *Service) Download(ctx context.Context, subj authz.Subjects, id string) (io.ReadCloser, error) {
	created, ok := store.IDTime(id)
	if !ok || s.d.Now().Sub(created) > Retention {
		return nil, apperr.NotFound
	}
	rc, err := s.d.Blob.Get(ctx, blob.Upload(subj.TenantID, id))
	if err != nil {
		return nil, apperr.NotFound
	}
	return rc, nil
}

// ---------------------------------------------------------------- verify

// Signature is one verified signature as the API shows it.
type Signature struct {
	Signer     string
	Time       *time.Time
	Reason     string
	Location   string
	Integrity  string // valid | modified
	Trust      string // trusted | untrusted | unknown
	Revocation string // good | revoked | unknown
	Method     string // tenant | qualified | "" (untrusted)
	Issuer     string
	Serial     string
}

// Result is a verification report.
type Result struct {
	Signatures        []Signature
	ModifiedAfterLast bool
}

// Verify checks the signatures of an uploaded or stored document.
func (s *Service) Verify(ctx context.Context, subj authz.Subjects, src Source) (Result, error) {
	res, err := s.verify(ctx, subj, src)
	if err != nil {
		s.record(ctx, subj, audit.DocumentVerify, audit.SubjectDocument, src.SubmissionID, audit.OutcomeRefused, reasonOf(err), nil)
		return Result{}, err
	}
	s.record(ctx, subj, audit.DocumentVerify, audit.SubjectDocument, src.SubmissionID, audit.OutcomeOK, "",
		map[string]any{"signatures": len(res.Signatures), "source": sourceName(src)})
	return res, nil
}

func (s *Service) verify(ctx context.Context, subj authz.Subjects, src Source) (Result, error) {
	doc, _, err := s.load(ctx, subj, src)
	if err != nil {
		return Result{}, err
	}
	if _, err := limits.Open(ctx, doc, s.d.Limits); err != nil {
		return Result{}, apperr.InvalidPDF
	}
	cas, err := s.d.Store.ListCAs(ctx, subj.TenantID)
	if err != nil {
		return Result{}, err
	}
	tenantPool := x509.NewCertPool()
	issued := map[string]store.Certificate{} // serial → tenant certificate
	for _, ca := range cas {
		if c, err := pki.Parse(ca); err == nil {
			tenantPool.AddCert(c)
		}
		certs, err := s.d.Store.IssuedBy(ctx, subj.TenantID, ca.ID)
		if err != nil {
			return Result{}, err
		}
		for _, c := range certs {
			issued[strings.ToLower(c.Serial)] = c
		}
	}
	pools := map[string]*x509.CertPool{PoolTenant: tenantPool}
	if s.d.Roots != nil {
		pools[PoolQualified] = s.d.Roots
	}
	sigs, err := verify.Verify(doc, verify.Options{Pools: pools, Now: s.d.Now()})
	if err != nil {
		return Result{}, apperr.InvalidPDF
	}
	out := Result{Signatures: []Signature{}}
	for i, v := range sigs {
		sg := Signature{Signer: v.Name, Reason: v.Reason, Location: v.Location, Integrity: "modified", Trust: "unknown", Revocation: "unknown"}
		if v.TimeSource != verify.Now {
			t := v.SignedAt
			sg.Time = &t
		}
		if v.Signer != nil {
			if sg.Signer == "" {
				sg.Signer = v.Signer.Subject.CommonName
			}
			sg.Issuer, sg.Serial = v.Signer.Issuer.CommonName, strings.ToLower(v.Signer.SerialNumber.Text(16))
		}
		if v.Intact {
			sg.Integrity, sg.Trust = "valid", "untrusted"
			if v.Trust != "" {
				sg.Trust, sg.Method = "trusted", v.Trust
			}
		}
		if v.Trust == PoolTenant {
			sg.Revocation = "good"
			if c, ok := issued[sg.Serial]; ok && c.Status == store.CertRevoked && c.RevokedAt != nil && !c.RevokedAt.After(v.SignedAt) {
				sg.Revocation = "revoked"
			}
		}
		out.Signatures = append(out.Signatures, sg)
		if i == len(sigs)-1 {
			out.ModifiedAfterLast = !v.CoversWhole
		}
	}
	return out, nil
}
