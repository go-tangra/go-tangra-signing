package jobs

import (
	"bytes"
	"context"
	"crypto/x509"
	"errors"
	"io"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/go-tangra/go-tangra-notification/sdk/v4/pkg/notifyclient"

	"github.com/go-tangra/go-tangra-signing/v4/internal/authz"
	"github.com/go-tangra/go-tangra-signing/v4/internal/blob"
	"github.com/go-tangra/go-tangra-signing/v4/internal/certs"
	"github.com/go-tangra/go-tangra-signing/v4/internal/contacts"
	"github.com/go-tangra/go-tangra-signing/v4/internal/events"
	"github.com/go-tangra/go-tangra-signing/v4/internal/mail"
	"github.com/go-tangra/go-tangra-signing/v4/internal/memstore"
	"github.com/go-tangra/go-tangra-signing/v4/internal/pdf/pdftest"
	"github.com/go-tangra/go-tangra-signing/v4/internal/pdf/verify"
	"github.com/go-tangra/go-tangra-signing/v4/internal/pincrypto"
	"github.com/go-tangra/go-tangra-signing/v4/internal/pki"
	"github.com/go-tangra/go-tangra-signing/v4/internal/sealed"
	"github.com/go-tangra/go-tangra-signing/v4/internal/signing"
	"github.com/go-tangra/go-tangra-signing/v4/internal/store"
	"github.com/go-tangra/go-tangra-signing/v4/internal/submissions"
)

const tenant = "0190f7c2-6a3e-7c1a-9b2e-2f6f9d1b4c55"

var ctx = context.Background()

type sender struct {
	mu sync.Mutex
	to map[string][]string
}

func (s *sender) SendKey(_ context.Context, _, key, to string, _ map[string]string, _ string) (notifyclient.Result, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.to == nil {
		s.to = map[string][]string{}
	}
	s.to[key] = append(s.to[key], to)
	return notifyclient.Result{Sent: true}, nil
}

// flaky fails Put for audit trails while fail is set.
type flaky struct {
	blob.Store
	fail bool
}

func (f *flaky) Put(ctx context.Context, key string, r io.Reader, size int64, ct string) (string, error) {
	if f.fail && strings.HasSuffix(key, "audit-trail.pdf") {
		return "", errors.New("object store down")
	}
	return f.Store.Put(ctx, key, r, size, ct)
}

type env struct {
	w      *Worker
	mem    *memstore.Mem
	blob   *flaky
	mail   *sender
	events *events.Recorder
	subs   *submissions.Service
	sign   *signing.Service
	me     *certs.Me
	pki    *pki.PKI
	tpl    store.Template
	now    time.Time
}

func newEnv(t *testing.T) *env {
	t.Helper()
	e := &env{mem: memstore.New(), blob: &flaky{Store: blob.NewFake()}, mail: &sender{}, events: &events.Recorder{}, now: time.Now().UTC().Truncate(time.Second)}
	clock := func() time.Time { return e.now }
	dir := &contacts.Fake{Users: map[string][]contacts.Contact{tenant: {
		{UserID: "sender", DisplayName: "Sam Sender", Email: "sam@example.org"},
		{UserID: "alice", DisplayName: "Alice Иванова", Email: "alice@example.org"},
		{UserID: "bob", DisplayName: "Bob", Email: "bob@example.org"},
	}}}
	sealer, _ := sealed.NewEnvelope(bytes.Repeat([]byte{7}, 32))
	e.pki = pki.New(pki.Deps{Store: e.mem, Sealer: sealer, Now: clock,
		Config: pki.Config{CAValidityYears: 10, CARenewBeforeYears: 2, CertValidityYears: 2, CRLValidityDays: 7, PINIterations: pincrypto.MinIterations}})
	e.me = certs.New(certs.Deps{Store: e.mem, PKI: e.pki, Contacts: dir, Now: clock, Rules: pincrypto.Rules{Min: 6, Max: 32},
		Lockout: pincrypto.Lockout{Attempts: 3, Duration: time.Minute}})
	em := events.Emitter{Pub: e.events}
	e.subs = submissions.New(submissions.Deps{Store: e.mem, Blob: e.blob, Checker: authz.Static{"sender": {authz.SubmissionsCreate}},
		Contacts: dir, Mail: mail.Mailer{Sender: e.mail, PortalBaseURL: "https://portal.example.org"}, Events: em, Now: clock})
	e.sign = signing.New(signing.Deps{Store: e.mem, Blob: e.blob, Subs: e.subs, Me: e.me, PKI: e.pki, Events: em, Now: clock})
	e.w = New(Deps{Store: e.mem, Blob: e.blob, PKI: e.pki, Subs: e.subs, Contacts: dir, Events: em, Now: clock, Interval: 10 * time.Millisecond})

	pdf := pdftest.Contract(1)
	id := store.NewID()
	key := blob.TemplatePDF(tenant, id, store.NewID())
	sum, _ := e.blob.Put(ctx, key, bytes.NewReader(pdf), int64(len(pdf)), "application/pdf")
	e.tpl = store.Template{ID: id, TenantID: tenant, Name: "Contract", Status: store.TemplateActive, PDFKey: key, PDFSHA256: sum,
		PDFPages: 1, Parties: []store.Party{{Key: "a", Name: "Employee"}, {Key: "b", Name: "Employer"}},
		Fields: []store.Field{
			{ID: "s1", Name: "S1", Type: "signature", Party: "a", Page: 1, X: 0.1, Y: 0.8, W: 0.3, H: 0.08},
			{ID: "s2", Name: "S2", Type: "signature", Party: "b", Page: 1, X: 0.6, Y: 0.8, W: 0.3, H: 0.08},
		}, Version: 1, CreatedAt: e.now, UpdatedAt: e.now}
	if err := e.mem.CreateTemplate(ctx, e.tpl); err != nil {
		t.Fatal(err)
	}
	for _, u := range []string{"alice", "bob"} {
		if _, err := e.me.Setup(ctx, authz.User(tenant, u, nil), "123456"); err != nil {
			t.Fatal(err)
		}
	}
	return e
}

func (e *env) completed(t *testing.T) string {
	t.Helper()
	u := func(id string) authz.Subjects { return authz.User(tenant, id, nil) }
	d, err := e.subs.Create(ctx, u("sender"), submissions.CreateInput{TemplateID: e.tpl.ID, Mode: store.ModeParallel,
		Signers: []submissions.SignerInput{{UserID: "alice", Party: "a"}, {UserID: "bob", Party: "b"}}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.subs.Send(ctx, u("sender"), d.Submission.ID); err != nil {
		t.Fatal(err)
	}
	for _, sg := range d.Signers {
		if _, err := e.sign.Sign(ctx, u(sg.UserID), sg.ID, signing.Input{PIN: "123456", IP: "10.1.1.1", UserAgent: "UA"}); err != nil {
			t.Fatal(err)
		}
	}
	return d.Submission.ID
}

func TestAuditTrailJob(t *testing.T) {
	e := newEnv(t)
	id := e.completed(t)
	if len(e.events.OfType(events.SubmissionCompleted)) != 0 || len(e.mail.to[mail.Completed]) != 0 {
		t.Fatal("completion is announced only after the audit trail exists")
	}
	done, failed, err := e.w.Drain(ctx)
	if err != nil || done != 1 || failed != 0 {
		t.Fatalf("drain: %d %d %v", done, failed, err)
	}
	sub, _, _ := e.mem.GetSubmission(ctx, tenant, id)
	if sub.AuditTrailKey == "" || sub.AuditTrailSHA256 == "" || !blob.InTenant(sub.AuditTrailKey, tenant) {
		t.Fatalf("audit trail not recorded: %+v", sub)
	}
	rc, err := e.blob.Get(ctx, sub.AuditTrailKey)
	if err != nil {
		t.Fatal(err)
	}
	trail, _ := io.ReadAll(rc)
	_ = rc.Close()
	cas, _ := e.mem.ListCAs(ctx, tenant)
	pool := x509.NewCertPool()
	for _, c := range cas {
		x, _ := pki.Parse(c)
		pool.AddCert(x)
	}
	sigs, err := verify.Verify(trail, verify.Options{Pools: map[string]*x509.CertPool{"tenant": pool}})
	if err != nil || len(sigs) != 1 || !sigs[0].Valid() || !sigs[0].Certification {
		t.Fatalf("audit trail signature: %+v %v", sigs, err)
	}
	// Tampering is detected.
	bad := append([]byte(nil), trail...)
	i := bytes.Index(bad, []byte("/Producer"))
	bad[i+2] = 'X'
	if s, _ := verify.Verify(bad, verify.Options{Pools: map[string]*x509.CertPool{"tenant": pool}}); len(s) == 1 && s[0].Intact {
		t.Fatal("tampered audit trail still verifies")
	}
	if got := e.mail.to[mail.Completed]; len(got) != 3 {
		t.Fatalf("completion mails to signers and sender: %v", got)
	}
	c := e.events.OfType(events.SubmissionCompleted)
	if len(c) != 1 || c[0].Payload.(events.CompletedPayload).FinalVersion != 2 || !c[0].Payload.(events.CompletedPayload).AuditTrail {
		t.Fatalf("completed event %+v", c)
	}
	evs, _ := e.mem.ListEvents(ctx, tenant, id)
	if evs[len(evs)-1].Type != "audit_trail.created" {
		t.Fatalf("history ends with %s", evs[len(evs)-1].Type)
	}
	// Nothing left to do; the job is done.
	if done, _, _ := e.w.Drain(ctx); done != 0 {
		t.Fatal("job ran twice")
	}
	if j := e.mem.Jobs(); j[0].DoneAt == nil {
		t.Fatal("job not completed")
	}
}

func TestRetryAndIdempotence(t *testing.T) {
	e := newEnv(t)
	id := e.completed(t)
	e.blob.fail = true
	if done, failed, err := e.w.Drain(ctx); err != nil || done != 0 || failed != 1 {
		t.Fatalf("failing drain: %d %d %v", done, failed, err)
	}
	j := e.mem.Jobs()[0]
	if j.LastError == "" || !j.NextAttemptAt.Equal(e.now.Add(time.Minute)) || j.DoneAt != nil {
		t.Fatalf("retry scheduled: %+v", j)
	}
	if done, _, _ := e.w.Drain(ctx); done != 0 {
		t.Fatal("not due before the backoff")
	}
	e.blob.fail = false
	e.now = e.now.Add(2 * time.Minute)
	if done, _, err := e.w.Drain(ctx); err != nil || done != 1 {
		t.Fatalf("retry: %d %v", done, err)
	}
	sub, _, _ := e.mem.GetSubmission(ctx, tenant, id)
	first := sub.AuditTrailKey

	// A re-queued job (crash after storing) never builds a second trail.
	again := e.mem.Jobs()[0]
	again.DoneAt, again.NextAttemptAt = nil, e.now
	e.mem.PutJob(again)
	if done, _, _ := e.w.Drain(ctx); done != 1 {
		t.Fatal("re-run")
	}
	if sub, _, _ := e.mem.GetSubmission(ctx, tenant, id); sub.AuditTrailKey != first {
		t.Fatal("audit trail rebuilt")
	}

	// Unknown kinds, deleted and not-completed submissions just finish.
	for _, j := range []store.Job{
		{ID: store.NewID(), TenantID: tenant, Kind: "other", SubmissionID: id},
		{ID: store.NewID(), TenantID: tenant, Kind: KindAuditTrail, SubmissionID: store.NewID()},
	} {
		j.NextAttemptAt, j.CreatedAt = e.now, e.now
		e.mem.PutJob(j)
	}
	if done, failed, err := e.w.Drain(ctx); err != nil || done != 2 || failed != 0 {
		t.Fatalf("odd jobs: %d %d %v", done, failed, err)
	}
	e.mem.Fail("ClaimJobsSystem", errors.New("db"))
	if _, _, err := e.w.Drain(ctx); err == nil {
		t.Fatal("claim error")
	}
}

func TestBackoffAndRun(t *testing.T) {
	for n, want := range map[int]time.Duration{0: time.Minute, 1: time.Minute, 2: 2 * time.Minute, 7: 64 * time.Minute, 8: time.Hour, 50: time.Hour} {
		if got := Backoff(n); got != want {
			t.Errorf("Backoff(%d) = %v", n, got)
		}
	}
	if len(clip(strings.Repeat("x", 500))) != 300 {
		t.Fatal("clip")
	}
	e := newEnv(t)
	id := e.completed(t)
	run, cancel := context.WithCancel(ctx)
	stopped := make(chan struct{})
	go func() { e.w.Run(run); close(stopped) }()
	e.w.Kick()
	e.w.Kick() // coalesced
	deadline := time.Now().Add(5 * time.Second)
	for {
		sub, _, _ := e.mem.GetSubmission(ctx, tenant, id)
		if sub.AuditTrailKey != "" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("worker never built the audit trail")
		}
		time.Sleep(5 * time.Millisecond)
	}
	cancel()
	<-stopped
}
