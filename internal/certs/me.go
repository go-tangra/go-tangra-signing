// Package certs manages signing certificates from the signer's and the
// administrator's side: the caller's own personal certificate (US3: setup
// with a PIN, view, PIN change, renewal, self-revocation) and — shared with
// the signing flow — unlocking a signer key with its PIN under the lockout
// policy (FR-034: failures are counted in their own transaction, a lock is
// audited and announced, success resets the counter).
package certs

import (
	"context"
	"crypto"
	"errors"
	"time"

	"github.com/go-tangra/go-tangra-signing/v4/internal/apperr"
	"github.com/go-tangra/go-tangra-signing/v4/internal/audit"
	"github.com/go-tangra/go-tangra-signing/v4/internal/authz"
	"github.com/go-tangra/go-tangra-signing/v4/internal/contacts"
	"github.com/go-tangra/go-tangra-signing/v4/internal/pincrypto"
	"github.com/go-tangra/go-tangra-signing/v4/internal/pki"
	"github.com/go-tangra/go-tangra-signing/v4/internal/repo"
	"github.com/go-tangra/go-tangra-signing/v4/internal/store"
)

// SignedLimit bounds the "documents signed with it" list.
const SignedLimit = 50

// Locked is told when a certificate gets locked (the mail notice).
type Locked func(ctx context.Context, c store.Certificate, until time.Time)

// Deps wire the service.
type Deps struct {
	Store    repo.Store
	PKI      *pki.PKI
	Contacts contacts.Directory
	Audit    audit.Recorder
	Now      func() time.Time
	Rules    pincrypto.Rules
	Lockout  pincrypto.Lockout
	OnLocked Locked
}

// Me serves the caller's own certificate and PIN checks.
type Me struct{ d Deps }

// New builds the service.
func New(d Deps) *Me {
	if d.Now == nil {
		d.Now = func() time.Time { return time.Now().UTC() }
	}
	return &Me{d: d}
}

// MyCertificate is the caller's active certificate (nil when none) and the
// documents signed with it.
type MyCertificate struct {
	Certificate *store.Certificate
	Signed      []repo.SignedDocument
}

func (m *Me) record(ctx context.Context, subj authz.Subjects, t audit.EventType, id, outcome, reason string) {
	audit.Emit(ctx, m.d.Audit, audit.Event{TenantID: subj.TenantID, EventType: t, ActorKind: audit.ActorOf(subj.ActorKind),
		ActorID: subj.ActorID(), SubjectKind: audit.SubjectCertificate, SubjectID: id, Outcome: outcome, Reason: reason})
}

func (m *Me) event(ctx context.Context, subj authz.Subjects, typ, certID string) {
	id, actor := certID, subj.UserID
	_ = m.d.Store.AddEvent(ctx, store.Event{ID: store.NewID(), TenantID: subj.TenantID, CertificateID: &id, ActorUserID: &actor,
		Type: typ, Meta: map[string]any{}, At: m.d.Now()})
}

// Get returns the caller's active certificate and what was signed with it.
func (m *Me) Get(ctx context.Context, subj authz.Subjects) (MyCertificate, error) {
	c, err := m.d.Store.ActiveSignerCertificate(ctx, subj.TenantID, subj.UserID)
	if errors.Is(err, repo.ErrNotFound) {
		return MyCertificate{}, nil
	}
	if err != nil {
		return MyCertificate{}, err
	}
	signed, err := m.d.Store.SignedWithCertificate(ctx, subj.TenantID, c.ID, SignedLimit)
	if err != nil {
		return MyCertificate{}, err
	}
	return MyCertificate{Certificate: &c, Signed: signed}, nil
}

func (m *Me) checkPIN(pin string) error {
	if err := m.d.Rules.Check(pin); err != nil {
		return apperr.Validation.WithField("pin")
	}
	return nil
}

// Setup creates the caller's personal certificate protected by pin (FR-031).
func (m *Me) Setup(ctx context.Context, subj authz.Subjects, pin string) (MyCertificate, error) {
	if err := m.checkPIN(pin); err != nil {
		return MyCertificate{}, err
	}
	if _, err := m.d.Store.ActiveSignerCertificate(ctx, subj.TenantID, subj.UserID); err == nil {
		return MyCertificate{}, apperr.CertificateExists
	} else if !errors.Is(err, repo.ErrNotFound) {
		return MyCertificate{}, err
	}
	c, err := m.issue(ctx, subj, pin)
	if err != nil {
		return MyCertificate{}, err
	}
	m.record(ctx, subj, audit.CertificateSetup, c.ID, audit.OutcomeOK, "")
	m.event(ctx, subj, "certificate.setup", c.ID)
	return MyCertificate{Certificate: &c, Signed: []repo.SignedDocument{}}, nil
}

func (m *Me) issue(ctx context.Context, subj authz.Subjects, pin string) (store.Certificate, error) {
	cs, err := m.d.Contacts.Contacts(ctx, subj.TenantID, []string{subj.UserID})
	if err != nil {
		return store.Certificate{}, apperr.TemporarilyUnavailable
	}
	ct, ok := cs[subj.UserID]
	if !ok || ct.Email == "" {
		return store.Certificate{}, apperr.ContactMissing
	}
	c, err := m.d.PKI.IssueSigner(ctx, pki.SignerInput{Tenant: subj.TenantID, UserID: subj.UserID, Name: ct.DisplayName,
		Email: ct.Email, PIN: pin, CreatedBy: subj.UserID})
	if errors.Is(err, repo.ErrConflict) {
		return store.Certificate{}, apperr.CertificateExists
	}
	return c, err
}

// active returns the caller's active certificate or certificate_missing.
func (m *Me) active(ctx context.Context, subj authz.Subjects) (store.Certificate, error) {
	c, err := m.d.Store.ActiveSignerCertificate(ctx, subj.TenantID, subj.UserID)
	if errors.Is(err, repo.ErrNotFound) {
		return store.Certificate{}, apperr.CertificateMissing
	}
	return c, err
}

// ChangePIN re-encrypts the caller's key under a new PIN; a wrong old PIN
// counts as a failed attempt (the lockout applies here too).
func (m *Me) ChangePIN(ctx context.Context, subj authz.Subjects, oldPIN, newPIN string) error {
	if err := m.checkPIN(newPIN); err != nil {
		return err
	}
	c, err := m.active(ctx, subj)
	if err != nil {
		return err
	}
	if _, err := m.Unlock(ctx, subj, c, oldPIN); err != nil {
		return err
	}
	changed, err := m.d.PKI.ChangePIN(c, oldPIN, newPIN)
	if err != nil {
		return err
	}
	if err := m.d.Store.UpdateCertificate(ctx, changed); err != nil {
		return err
	}
	m.record(ctx, subj, audit.CertificatePINChange, c.ID, audit.OutcomeOK, "")
	m.event(ctx, subj, "certificate.pin_changed", c.ID)
	return nil
}

// Renew replaces the caller's certificate with a new one under pin; the old
// one is revoked as superseded.
func (m *Me) Renew(ctx context.Context, subj authz.Subjects, pin string) (MyCertificate, error) {
	if err := m.checkPIN(pin); err != nil {
		return MyCertificate{}, err
	}
	old, err := m.active(ctx, subj)
	if err != nil {
		return MyCertificate{}, err
	}
	revoked, err := m.d.PKI.Revoke(ctx, old, "superseded")
	if err != nil {
		return MyCertificate{}, err
	}
	c, err := m.issue(ctx, subj, pin)
	if err != nil {
		return MyCertificate{}, err
	}
	revoked.SupersededBy = &c.ID
	_ = m.d.Store.UpdateCertificate(ctx, revoked) // link only; the revocation above is what counts
	m.record(ctx, subj, audit.CertificateRenew, c.ID, audit.OutcomeOK, "")
	m.event(ctx, subj, "certificate.setup", c.ID)
	return MyCertificate{Certificate: &c, Signed: []repo.SignedDocument{}}, nil
}

// Revoke revokes the caller's certificate (a forgotten PIN: set up a new one).
func (m *Me) Revoke(ctx context.Context, subj authz.Subjects) error {
	c, err := m.active(ctx, subj)
	if err != nil {
		return err
	}
	if _, err := m.d.PKI.Revoke(ctx, c, "cessation_of_operation"); err != nil {
		return err
	}
	m.record(ctx, subj, audit.CertificateRevoke, c.ID, audit.OutcomeOK, "")
	m.event(ctx, subj, "certificate.revoked", c.ID)
	return nil
}

// State describes a certificate's usability for the signing page.
func State(c *store.Certificate, now time.Time) (state string, lockedUntil *time.Time) {
	switch {
	case c == nil:
		return "none", nil
	case c.Status == store.CertRevoked:
		return "revoked", nil
	case c.Status != store.CertActive || !now.Before(c.NotAfter):
		return "expired", nil
	case pincrypto.Locked(c.LockedUntil, now):
		return "locked", c.LockedUntil
	}
	return "active", nil
}

// Usable checks that c may sign at now (FR-035).
func Usable(c store.Certificate, now time.Time) error {
	switch st, until := State(&c, now); st {
	case "active":
		if now.Before(c.NotBefore) {
			return apperr.CertificateUnusable
		}
		return nil
	case "locked":
		return apperr.CertificateLocked.WithDetail(map[string]any{"locked_until": until.UTC().Format(time.RFC3339)})
	default:
		return apperr.CertificateUnusable
	}
}

// Unlock decrypts the signer key of c with pin under the lockout policy: a
// locked or unusable certificate is refused before any PIN check; a wrong PIN
// is counted (own transaction) and answers pin_invalid with the attempts left,
// or certificate_locked when this failure locks it; success resets the count.
func (m *Me) Unlock(ctx context.Context, subj authz.Subjects, c store.Certificate, pin string) (crypto.Signer, error) {
	now := m.d.Now()
	if err := Usable(c, now); err != nil {
		return nil, err
	}
	signer, err := m.d.PKI.UnlockSigner(c, pin)
	if err == nil {
		if c.FailedPINCount > 0 {
			_ = m.d.Store.ResetPINFailures(ctx, c.TenantID, c.ID)
		}
		return signer, nil
	}
	if !errors.Is(err, pincrypto.ErrPIN) {
		return nil, err
	}
	until := m.d.Lockout.Until(now)
	failures, locked, rerr := m.d.Store.RecordPINFailure(ctx, c.TenantID, c.ID, m.d.Lockout.Attempts, until)
	if rerr != nil {
		return nil, rerr
	}
	if locked {
		m.record(ctx, subj, audit.CertificateLock, c.ID, audit.OutcomeOK, "pin_invalid")
		m.event(ctx, subj, "certificate.locked", c.ID)
		if m.d.OnLocked != nil {
			m.d.OnLocked(ctx, c, until)
		}
		return nil, apperr.CertificateLocked.WithDetail(map[string]any{"locked_until": until.UTC().Format(time.RFC3339)})
	}
	return nil, apperr.PINInvalid.WithDetail(map[string]any{"attempts_left": m.d.Lockout.AttemptsLeft(failures)})
}
