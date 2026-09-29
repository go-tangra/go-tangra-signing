package certs

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/go-tangra/go-tangra-signing/v4/internal/apperr"
	"github.com/go-tangra/go-tangra-signing/v4/internal/audit"
	"github.com/go-tangra/go-tangra-signing/v4/internal/authz"
	"github.com/go-tangra/go-tangra-signing/v4/internal/contacts"
	"github.com/go-tangra/go-tangra-signing/v4/internal/memstore"
	"github.com/go-tangra/go-tangra-signing/v4/internal/pincrypto"
	"github.com/go-tangra/go-tangra-signing/v4/internal/pki"
	"github.com/go-tangra/go-tangra-signing/v4/internal/sealed"
	"github.com/go-tangra/go-tangra-signing/v4/internal/store"
)

const tenant = "0190f7c2-6a3e-7c1a-9b2e-2f6f9d1b4c55"

var ctx = context.Background()

type recorder struct {
	mu     sync.Mutex
	events []audit.Event
}

func (r *recorder) Record(_ context.Context, e audit.Event) error {
	if err := audit.Validate(e); err != nil {
		return err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.events = append(r.events, e)
	return nil
}

func (r *recorder) has(t audit.EventType) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, e := range r.events {
		if e.EventType == t {
			return true
		}
	}
	return false
}

type env struct {
	me     *Me
	mem    *memstore.Mem
	dir    *contacts.Fake
	audit  *recorder
	now    time.Time
	locked []time.Time
	maria  authz.Subjects
}

func newEnv(t *testing.T) *env {
	t.Helper()
	e := &env{mem: memstore.New(), audit: &recorder{}, now: time.Now().UTC(), maria: authz.User(tenant, "maria", nil)}
	e.dir = &contacts.Fake{Users: map[string][]contacts.Contact{tenant: {
		{UserID: "maria", DisplayName: "Мария Иванова", Email: "maria@example.org"},
		{UserID: "nomail", DisplayName: "No Mail"},
	}}}
	sealer, _ := sealed.NewEnvelope(bytes.Repeat([]byte{7}, 32))
	clock := func() time.Time { return e.now }
	p := pki.New(pki.Deps{Store: e.mem, Sealer: sealer, Now: clock,
		Config: pki.Config{CAValidityYears: 10, CARenewBeforeYears: 2, CertValidityYears: 2, CRLValidityDays: 7, PINIterations: pincrypto.MinIterations}})
	e.me = New(Deps{Store: e.mem, PKI: p, Contacts: e.dir, Audit: e.audit, Now: clock, Rules: pincrypto.Rules{Min: 6, Max: 32},
		Lockout:  pincrypto.Lockout{Attempts: 3, Duration: 15 * time.Minute},
		OnLocked: func(_ context.Context, _ store.Certificate, until time.Time) { e.locked = append(e.locked, until) }})
	return e
}

func TestSetupAndGet(t *testing.T) {
	e := newEnv(t)
	if got, err := e.me.Get(ctx, e.maria); err != nil || got.Certificate != nil {
		t.Fatalf("no certificate yet: %+v %v", got, err)
	}
	if _, err := e.me.Setup(ctx, e.maria, "123"); !errors.Is(err, apperr.Validation) {
		t.Fatalf("short pin: %v", err)
	}
	mc, err := e.me.Setup(ctx, e.maria, "123456")
	if err != nil || mc.Certificate == nil || mc.Certificate.SubjectCN != "Mariya Ivanova" || mc.Certificate.Email != "maria@example.org" {
		t.Fatalf("setup = %+v %v", mc, err)
	}
	if _, err := e.me.Setup(ctx, e.maria, "123456"); !errors.Is(err, apperr.CertificateExists) {
		t.Fatalf("second setup: %v", err)
	}
	if _, err := e.me.Setup(ctx, authz.User(tenant, "nomail", nil), "123456"); !errors.Is(err, apperr.ContactMissing) {
		t.Fatalf("no e-mail: %v", err)
	}
	e.dir.Err = errors.New("auth down")
	if _, err := e.me.Setup(ctx, authz.User(tenant, "other", nil), "123456"); !errors.Is(err, apperr.TemporarilyUnavailable) {
		t.Fatalf("auth down: %v", err)
	}
	e.dir.Err = nil
	got, err := e.me.Get(ctx, e.maria)
	if err != nil || got.Certificate.ID != mc.Certificate.ID || got.Signed == nil && len(got.Signed) != 0 {
		t.Fatalf("get = %+v %v", got, err)
	}
	if !e.audit.has(audit.CertificateSetup) {
		t.Fatal("audit")
	}
	ev := e.mem.Events()
	if len(ev) != 1 || ev[0].Type != "certificate.setup" || *ev[0].CertificateID != mc.Certificate.ID {
		t.Fatalf("events = %+v", ev)
	}
}

func TestUnlockLockout(t *testing.T) {
	e := newEnv(t)
	mc, _ := e.me.Setup(ctx, e.maria, "123456")
	c := *mc.Certificate
	for i, left := range []int{2, 1} {
		_, err := e.me.Unlock(ctx, e.maria, c, "000000")
		ae, ok := apperr.As(err)
		if !ok || ae.Reason != "pin_invalid" || ae.Detail["attempts_left"] != left {
			t.Fatalf("failure %d: %v %+v", i, err, ae)
		}
	}
	_, err := e.me.Unlock(ctx, e.maria, c, "000000")
	if !errors.Is(err, apperr.CertificateLocked) || len(e.locked) != 1 {
		t.Fatalf("third failure must lock: %v", err)
	}
	c, _ = e.mem.GetCertificate(ctx, tenant, c.ID)
	// Even the right PIN is refused while locked.
	if _, err := e.me.Unlock(ctx, e.maria, c, "123456"); !errors.Is(err, apperr.CertificateLocked) {
		t.Fatalf("locked: %v", err)
	}
	if st, until := State(&c, e.now); st != "locked" || until == nil {
		t.Fatalf("state = %s", st)
	}
	e.now = e.now.Add(16 * time.Minute)
	signer, err := e.me.Unlock(ctx, e.maria, c, "123456")
	if err != nil || signer == nil {
		t.Fatalf("after the lock: %v", err)
	}
	// A failure then a success resets the counter.
	_, _ = e.me.Unlock(ctx, e.maria, c, "bad-pin")
	c, _ = e.mem.GetCertificate(ctx, tenant, c.ID)
	if c.FailedPINCount != 1 {
		t.Fatalf("count = %d", c.FailedPINCount)
	}
	if _, err := e.me.Unlock(ctx, e.maria, c, "123456"); err != nil {
		t.Fatal(err)
	}
	c, _ = e.mem.GetCertificate(ctx, tenant, c.ID)
	if c.FailedPINCount != 0 || !e.audit.has(audit.CertificateLock) {
		t.Fatalf("reset/audit: %d", c.FailedPINCount)
	}
	e.mem.Fail("RecordPINFailure", errors.New("db down"))
	if _, err := e.me.Unlock(ctx, e.maria, c, "bad-pin"); err == nil || errors.Is(err, apperr.PINInvalid) {
		t.Fatalf("counter failure hidden: %v", err)
	}
}

func TestUsableStates(t *testing.T) {
	now := time.Now()
	c := store.Certificate{Status: store.CertActive, NotBefore: now.Add(-time.Hour), NotAfter: now.Add(time.Hour)}
	if err := Usable(c, now); err != nil {
		t.Fatal(err)
	}
	early := c
	early.NotBefore = now.Add(time.Hour)
	expired := c
	expired.NotAfter = now.Add(-time.Minute)
	revoked := c
	revoked.Status = store.CertRevoked
	reissue := c
	reissue.Status = store.CertNeedsReissue
	for name, x := range map[string]store.Certificate{"early": early, "expired": expired, "revoked": revoked, "reissue": reissue} {
		if err := Usable(x, now); !errors.Is(err, apperr.CertificateUnusable) {
			t.Errorf("%s: %v", name, err)
		}
	}
	for want, x := range map[string]*store.Certificate{"none": nil, "revoked": &revoked, "expired": &expired, "active": &c} {
		if st, _ := State(x, now); st != want {
			t.Errorf("state %s = %s", want, st)
		}
	}
}

func TestChangePINRenewRevoke(t *testing.T) {
	e := newEnv(t)
	if err := e.me.ChangePIN(ctx, e.maria, "123456", "654321"); !errors.Is(err, apperr.CertificateMissing) {
		t.Fatalf("no certificate: %v", err)
	}
	mc, _ := e.me.Setup(ctx, e.maria, "123456")
	if err := e.me.ChangePIN(ctx, e.maria, "123456", "12"); !errors.Is(err, apperr.Validation) {
		t.Fatalf("weak new pin: %v", err)
	}
	if err := e.me.ChangePIN(ctx, e.maria, "wrong-old", "654321"); !errors.Is(err, apperr.PINInvalid) {
		t.Fatalf("wrong old pin: %v", err)
	}
	if err := e.me.ChangePIN(ctx, e.maria, "123456", "654321"); err != nil {
		t.Fatal(err)
	}
	c, _ := e.mem.GetCertificate(ctx, tenant, mc.Certificate.ID)
	if _, err := e.me.Unlock(ctx, e.maria, c, "654321"); err != nil {
		t.Fatalf("new pin: %v", err)
	}
	renewed, err := e.me.Renew(ctx, e.maria, "111111")
	if err != nil || renewed.Certificate.ID == c.ID {
		t.Fatalf("renew: %v", err)
	}
	old, _ := e.mem.GetCertificate(ctx, tenant, c.ID)
	if old.Status != store.CertRevoked || old.RevokedAt == nil || old.RevocationReason != "superseded" || old.SupersededBy == nil {
		t.Fatalf("old = %+v", old)
	}
	if _, err := e.me.Renew(ctx, e.maria, "1"); !errors.Is(err, apperr.Validation) {
		t.Fatal("weak renew pin")
	}
	if err := e.me.Revoke(ctx, e.maria); err != nil {
		t.Fatal(err)
	}
	if got, _ := e.me.Get(ctx, e.maria); got.Certificate != nil {
		t.Fatal("revoked certificate still active")
	}
	if err := e.me.Revoke(ctx, e.maria); !errors.Is(err, apperr.CertificateMissing) {
		t.Fatalf("revoke twice: %v", err)
	}
	if _, err := e.me.Renew(ctx, e.maria, "123456"); !errors.Is(err, apperr.CertificateMissing) {
		t.Fatalf("renew without a certificate: %v", err)
	}
	for _, typ := range []audit.EventType{audit.CertificatePINChange, audit.CertificateRenew, audit.CertificateRevoke} {
		if !e.audit.has(typ) {
			t.Errorf("audit lacks %s", typ)
		}
	}
	e.mem.Fail("ActiveSignerCertificate", errors.New("db down"))
	if _, err := e.me.Get(ctx, e.maria); err == nil || strings.Contains(err.Error(), "not found") {
		t.Fatalf("db failure hidden: %v", err)
	}
}
