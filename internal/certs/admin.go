package certs

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/go-tangra/go-tangra-signing/v4/internal/apperr"
	"github.com/go-tangra/go-tangra-signing/v4/internal/audit"
	"github.com/go-tangra/go-tangra-signing/v4/internal/authz"
	"github.com/go-tangra/go-tangra-signing/v4/internal/pki"
	"github.com/go-tangra/go-tangra-signing/v4/internal/repo"
	"github.com/go-tangra/go-tangra-signing/v4/internal/store"
)

// Admin is the certificate administration of a tenant (US7, route
// permission certificates:manage): list, view, revoke, create administrator
// certificates, and the tenant CA's CRL. Key material never leaves it.
type Admin struct{ d Deps }

// NewAdmin builds the administration service.
func NewAdmin(d Deps) *Admin {
	if d.Now == nil {
		d.Now = func() time.Time { return time.Now().UTC() }
	}
	return &Admin{d: d}
}

func (a *Admin) record(ctx context.Context, subj authz.Subjects, t audit.EventType, id, outcome, reason string, detail map[string]any) {
	audit.Emit(ctx, a.d.Audit, audit.Event{TenantID: subj.TenantID, EventType: t, ActorKind: audit.ActorOf(subj.ActorKind),
		ActorID: subj.ActorID(), SubjectKind: audit.SubjectCertificate, SubjectID: id, Outcome: outcome, Reason: reason, Details: detail})
}

func (a *Admin) event(ctx context.Context, subj authz.Subjects, typ, certID string, meta map[string]any) {
	actor := subj.ActorID()
	_ = a.d.Store.AddEvent(ctx, store.Event{ID: store.NewID(), TenantID: subj.TenantID, CertificateID: &certID, ActorUserID: &actor,
		Type: typ, Meta: meta, At: a.d.Now()})
}

func notFound(err error) error {
	if errors.Is(err, repo.ErrNotFound) {
		return apperr.NotFound
	}
	return err
}

// List pages the tenant's certificates.
func (a *Admin) List(ctx context.Context, subj authz.Subjects, f repo.CertificateFilter) ([]store.Certificate, int, error) {
	return a.d.Store.ListCertificates(ctx, subj.TenantID, f)
}

// Get returns one certificate of the tenant.
func (a *Admin) Get(ctx context.Context, subj authz.Subjects, id string) (store.Certificate, error) {
	c, err := a.d.Store.GetCertificate(ctx, subj.TenantID, id)
	return c, notFound(err)
}

// Issuer returns the issuing CA's subject of c ("" for a root).
func (a *Admin) Issuer(ctx context.Context, c store.Certificate) string {
	if c.IssuerID == nil {
		return ""
	}
	ca, err := a.d.Store.GetCertificate(ctx, c.TenantID, *c.IssuerID)
	if err != nil {
		return ""
	}
	return ca.SubjectCN
}

// Revoke revokes a signer, admin or system certificate (a CA is renewed,
// not revoked) and republishes the CRL; later signing with it is refused.
func (a *Admin) Revoke(ctx context.Context, subj authz.Subjects, id, reason string) (store.Certificate, error) {
	if _, ok := pki.ReasonCodes[reason]; !ok {
		return store.Certificate{}, apperr.Validation.WithField("reason")
	}
	c, err := a.d.Store.GetCertificate(ctx, subj.TenantID, id)
	if err != nil {
		return store.Certificate{}, notFound(err)
	}
	if c.Kind == store.KindCA {
		a.record(ctx, subj, audit.CertificateRevoke, id, audit.OutcomeRefused, "ca", nil)
		return store.Certificate{}, apperr.Conflict.WithField("kind")
	}
	c, err = a.d.PKI.Revoke(ctx, c, reason)
	if err != nil {
		return store.Certificate{}, err
	}
	a.record(ctx, subj, audit.CertificateRevoke, id, audit.OutcomeOK, "", map[string]any{"revocation_reason": reason, "kind": c.Kind})
	a.event(ctx, subj, "certificate.revoked", id, map[string]any{"revocation_reason": reason})
	return c, nil
}

// CreateAdmin issues an administrator certificate with a sealed key (FR-038).
func (a *Admin) CreateAdmin(ctx context.Context, subj authz.Subjects, cn, email string, years int) (store.Certificate, error) {
	cn = strings.TrimSpace(cn)
	if cn == "" || len([]rune(cn)) > 120 || strings.ContainsAny(cn, "\r\n") {
		return store.Certificate{}, apperr.Validation.WithField("subject_cn")
	}
	if years == 0 {
		years = 2
	}
	if years < 1 || years > 5 {
		return store.Certificate{}, apperr.Validation.WithField("validity_years")
	}
	c, err := a.d.PKI.IssueAdmin(ctx, subj.TenantID, pki.Transliterate(cn), strings.TrimSpace(email), years, subj.ActorID())
	if err != nil {
		return store.Certificate{}, err
	}
	a.record(ctx, subj, audit.CertificateCreateAdmin, c.ID, audit.OutcomeOK, "", map[string]any{"validity_years": years})
	a.event(ctx, subj, "certificate.created", c.ID, map[string]any{"kind": c.Kind})
	return c, nil
}

// CRL returns the current tenant CA's DER CRL, publishing one when due.
func (a *Admin) CRL(ctx context.Context, subj authz.Subjects) ([]byte, error) {
	ca, err := a.d.PKI.EnsureCA(ctx, subj.TenantID)
	if err != nil {
		return nil, err
	}
	now := a.d.Now()
	if len(ca.CRLDER) == 0 || ca.CRLNextUpdate == nil || !ca.CRLNextUpdate.After(now) {
		if err := a.d.PKI.PublishCRL(ctx, subj.TenantID, ca.ID); err != nil {
			return nil, err
		}
		if ca, err = a.d.Store.GetCertificate(ctx, subj.TenantID, ca.ID); err != nil {
			return nil, err
		}
	}
	return ca.CRLDER, nil
}
