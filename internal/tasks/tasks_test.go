package tasks

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/go-tangra/go-tangra-notification/sdk/v4/pkg/notifyclient"
	schedulerv1 "github.com/go-tangra/go-tangra-scheduler/sdk/v4/api/proto/scheduler/v1"
	sdk "github.com/go-tangra/go-tangra-scheduler/sdk/v4/pkg/taskexec"

	"github.com/go-tangra/go-tangra-signing/v4/internal/audit"
	"github.com/go-tangra/go-tangra-signing/v4/internal/authz"
	"github.com/go-tangra/go-tangra-signing/v4/internal/blob"
	"github.com/go-tangra/go-tangra-signing/v4/internal/contacts"
	"github.com/go-tangra/go-tangra-signing/v4/internal/events"
	"github.com/go-tangra/go-tangra-signing/v4/internal/mail"
	"github.com/go-tangra/go-tangra-signing/v4/internal/memstore"
	"github.com/go-tangra/go-tangra-signing/v4/internal/pdf/pdftest"
	"github.com/go-tangra/go-tangra-signing/v4/internal/pincrypto"
	"github.com/go-tangra/go-tangra-signing/v4/internal/pki"
	"github.com/go-tangra/go-tangra-signing/v4/internal/sealed"
	"github.com/go-tangra/go-tangra-signing/v4/internal/store"
	"github.com/go-tangra/go-tangra-signing/v4/internal/submissions"
)

const tenant = "0190f7c2-6a3e-7c1a-9b2e-2f6f9d1b4c55"

var ctx = context.Background()

type sender struct {
	mu    sync.Mutex
	calls []map[string]string
	keys  []string
}

func (s *sender) SendKey(_ context.Context, _, key, to string, vars map[string]string, _ string) (notifyclient.Result, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.keys = append(s.keys, key)
	v := map[string]string{"to": to}
	for k, x := range vars {
		v[k] = x
	}
	s.calls = append(s.calls, v)
	return notifyclient.Result{Sent: true}, nil
}

func (s *sender) count(key string) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	n := 0
	for _, k := range s.keys {
		if k == key {
			n++
		}
	}
	return n
}

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

type env struct {
	r      *Runner
	mem    *memstore.Mem
	blob   *blob.Fake
	mail   *sender
	events *events.Recorder
	audit  *recorder
	subs   *submissions.Service
	pki    *pki.PKI
	tpl    store.Template
	now    time.Time
}

func newEnv(t *testing.T) *env {
	t.Helper()
	e := &env{mem: memstore.New(), blob: blob.NewFake(), mail: &sender{}, events: &events.Recorder{}, audit: &recorder{},
		now: time.Now().UTC().Truncate(time.Second)}
	clock := func() time.Time { return e.now }
	dir := &contacts.Fake{Users: map[string][]contacts.Contact{tenant: {
		{UserID: "sender", DisplayName: "Sam Sender", Email: "sam@example.org"},
		{UserID: "alice", DisplayName: "Alice", Email: "alice@example.org"},
		{UserID: "bob", DisplayName: "Bob", Email: "bob@example.org"},
	}}}
	sealer, _ := sealed.NewEnvelope(bytes.Repeat([]byte{7}, 32))
	e.pki = pki.New(pki.Deps{Store: e.mem, Sealer: sealer, Now: clock,
		Config: pki.Config{CAValidityYears: 10, CARenewBeforeYears: 2, CertValidityYears: 2, CRLValidityDays: 7, PINIterations: pincrypto.MinIterations}})
	m := mail.Mailer{Sender: e.mail, PortalBaseURL: "https://portal.example.org"}
	em := events.Emitter{Pub: e.events}
	e.subs = submissions.New(submissions.Deps{Store: e.mem, Blob: e.blob, Checker: authz.Static{"sender": {authz.SubmissionsCreate}},
		Contacts: dir, Mail: m, Events: em, Now: clock})
	e.r = &Runner{Store: e.mem, Blob: e.blob, PKI: e.pki, Subs: e.subs, Mail: m, Events: em, Audit: e.audit, Now: clock}
	pdf := pdftest.Contract(1)
	id := store.NewID()
	key := blob.TemplatePDF(tenant, id, store.NewID())
	sum, _ := e.blob.Put(ctx, key, bytes.NewReader(pdf), int64(len(pdf)), "application/pdf")
	e.tpl = store.Template{ID: id, TenantID: tenant, Name: "Contract", Status: store.TemplateActive, PDFKey: key, PDFSHA256: sum,
		PDFPages: 1, Parties: []store.Party{{Key: "a", Name: "A"}, {Key: "b", Name: "B"}},
		Fields:  []store.Field{{ID: "s", Name: "S", Type: "signature", Party: "a", Page: 1, X: 0.1, Y: 0.8, W: 0.3, H: 0.08}},
		Version: 1, CreatedAt: e.now, UpdatedAt: e.now}
	if err := e.mem.CreateTemplate(ctx, e.tpl); err != nil {
		t.Fatal(err)
	}
	return e
}

func (e *env) sent(t *testing.T, mode string, expires *time.Time, rem *store.Reminder) submissions.Detail {
	t.Helper()
	u := authz.User(tenant, "sender", nil)
	d, err := e.subs.Create(ctx, u, submissions.CreateInput{TemplateID: e.tpl.ID, Mode: mode, ExpiresAt: expires, Reminder: rem,
		Signers: []submissions.SignerInput{{UserID: "alice", Party: "a"}, {UserID: "bob", Party: "b"}}})
	if err != nil {
		t.Fatal(err)
	}
	if d, err = e.subs.Send(ctx, u, d.Submission.ID); err != nil {
		t.Fatal(err)
	}
	return d
}

func req(typ, tenantID, payload string) sdk.Request {
	return sdk.Request{Type: typ, TenantID: tenantID, Payload: json.RawMessage(payload)}
}

func TestExpireExactlyOnce(t *testing.T) {
	e := newEnv(t)
	soon := e.now.Add(time.Hour)
	d := e.sent(t, store.ModeSequential, &soon, nil)
	later := e.now.Add(48 * time.Hour)
	keep := e.sent(t, store.ModeParallel, &later, nil)

	if res := e.r.Expire(ctx, req(TypeExpire, "", "{}")); !res.Success || res.Message != "expired 0" {
		t.Fatalf("nothing due: %+v", res)
	}
	e.now = e.now.Add(2 * time.Hour)
	if res := e.r.Expire(ctx, req(TypeExpire, "", "{}")); !res.Success || res.Message != "expired 1" {
		t.Fatalf("expire: %+v", res)
	}
	if res := e.r.Expire(ctx, req(TypeExpire, "", "{}")); res.Message != "expired 0" {
		t.Fatalf("second run must not expire again: %+v", res)
	}
	sub, _, _ := e.mem.GetSubmission(ctx, tenant, d.Submission.ID)
	if sub.Status != store.SubmissionExpired {
		t.Fatalf("status %s", sub.Status)
	}
	if k, _, _ := e.mem.GetSubmission(ctx, tenant, keep.Submission.ID); k.Status != store.SubmissionInProgress {
		t.Fatal("a submission not yet due was expired")
	}
	// Sequential: only alice was invited; she and the sender are told.
	if n := e.mail.count(mail.Expired); n != 2 {
		t.Fatalf("expired mails %d", n)
	}
	if len(e.events.OfType(events.SubmissionExpired)) != 1 {
		t.Fatal("expired event")
	}
	evs, _ := e.mem.ListEvents(ctx, tenant, d.Submission.ID)
	if evs[len(evs)-1].Type != "submission.expired" {
		t.Fatalf("history %s", evs[len(evs)-1].Type)
	}
	if res := e.r.Expire(ctx, req(TypeExpire, tenant, "{}")); !res.Permanent {
		t.Fatal("tenant request must be refused")
	}
	if res := e.r.Expire(ctx, req(TypeExpire, "", `{"x":1}`)); !res.Permanent {
		t.Fatal("unknown payload key")
	}
	e.mem.Fail("ExpireDueSystem", errors.New("db"))
	if res := e.r.Expire(ctx, req(TypeExpire, "", "{}")); res.Success || res.Permanent {
		t.Fatalf("storage outage must retry: %+v", res)
	}
}

func TestRemindersExactlyOnceAndBounded(t *testing.T) {
	e := newEnv(t)
	d := e.sent(t, store.ModeParallel, nil, &store.Reminder{IntervalDays: 1, Max: 2})
	if res := e.r.Reminders(ctx, req(TypeReminders, "", "{}")); !res.Success || !strings.HasPrefix(res.Message, "reminded 0,") {
		t.Fatalf("nothing due: %+v", res)
	}
	e.now = e.now.Add(25 * time.Hour)
	if res := e.r.Reminders(ctx, req(TypeReminders, "", `{"batch":1}`)); !res.Success || !strings.HasPrefix(res.Message, "reminded 2,") {
		t.Fatalf("reminders: %+v", res)
	}
	if res := e.r.Reminders(ctx, req(TypeReminders, "", "{}")); !strings.HasPrefix(res.Message, "reminded 0,") {
		t.Fatalf("again within the interval: %+v", res)
	}
	var reminder map[string]string
	for _, c := range e.mail.calls {
		if c["reminder_no"] != "" {
			reminder = c
		}
	}
	if reminder["reminder_no"] != "1" || reminder["sender"] != "Sam Sender" || !strings.Contains(reminder["link"], "/signing/sign/") || reminder["document"] != "Contract" {
		t.Fatalf("reminder vars %v", reminder)
	}
	e.now = e.now.Add(25 * time.Hour)
	e.r.Reminders(ctx, req(TypeReminders, "", "{}"))
	e.now = e.now.Add(25 * time.Hour)
	e.r.Reminders(ctx, req(TypeReminders, "", "{}"))
	if n := e.mail.count(mail.Reminder); n != 4 {
		t.Fatalf("max 2 reminders per signer: %d", n)
	}
	for _, sg := range d.Signers {
		s, _ := e.mem.GetSigner(ctx, tenant, sg.ID)
		if s.RemindersSent != 2 || s.NextReminderAt != nil {
			t.Fatalf("signer %+v", s)
		}
	}
	for _, bad := range []string{`{"batch":0.5}`, `{"batch":5000}`, `{"batch":-1}`, `{"other":1}`} {
		if res := e.r.Reminders(ctx, req(TypeReminders, "", bad)); !res.Permanent {
			t.Errorf("payload %s accepted", bad)
		}
	}
	if res := e.r.Reminders(ctx, req(TypeReminders, tenant, "{}")); !res.Permanent {
		t.Fatal("tenant request")
	}
	e.mem.Fail("ClaimRemindersSystem", errors.New("db"))
	if res := e.r.Reminders(ctx, req(TypeReminders, "", "{}")); res.Success || res.Permanent {
		t.Fatalf("outage: %+v", res)
	}
}

func TestSweeps(t *testing.T) {
	e := newEnv(t)
	d := e.sent(t, store.ModeParallel, nil, nil)
	// A due CRL: the tenant CA exists and has never published one.
	if _, err := e.pki.EnsureCA(ctx, tenant); err != nil {
		t.Fatal(err)
	}
	// An expired QES preparation with its object.
	pkey := blob.QESPrepared(tenant, "prep1")
	_, _ = e.blob.Put(ctx, pkey, bytes.NewReader([]byte("%PDF")), 4, "application/pdf")
	if err := e.mem.CreateQES(ctx, store.QESPreparation{ID: store.NewID(), TenantID: tenant, SignerID: d.Signers[0].ID,
		PreparedKey: pkey, ExpiresAt: e.now.Add(-time.Minute), CreatedAt: e.now.Add(-time.Hour)}); err != nil {
		t.Fatal(err)
	}
	// Objects left behind by a deleted submission; live ones stay.
	ghost := blob.DocumentVersion(tenant, store.NewID(), 1)
	_, _ = e.blob.Put(ctx, ghost, bytes.NewReader([]byte("x")), 1, "application/pdf")
	stray := blob.TenantPrefix(tenant) + "submissions/orphan-without-slash"
	_, _ = e.blob.Put(ctx, stray, bytes.NewReader([]byte("x")), 1, "application/pdf")
	res := e.r.Reminders(ctx, req(TypeReminders, "", "{}"))
	if !res.Success || res.Message != "reminded 0, crl 1, swept 2" {
		t.Fatalf("sweep: %+v", res)
	}
	if e.blob.Has(pkey) || e.blob.Has(ghost) || !e.blob.Has(d.Submission.PDFKey) || !e.blob.Has(stray) {
		t.Fatal("sweep touched the wrong objects")
	}
	if res := e.r.Reminders(ctx, req(TypeReminders, "", "{}")); res.Message != "reminded 0, crl 0, swept 0" {
		t.Fatalf("idempotent: %+v", res)
	}
	e.mem.Fail("TenantsSystem", errors.New("db"))
	if res := e.r.Reminders(ctx, req(TypeReminders, "", "{}")); res.Success || !strings.Contains(res.Message, "sweep incomplete") {
		t.Fatalf("sweep error: %+v", res)
	}
}

func TestExecutorServer(t *testing.T) {
	e := newEnv(t)
	caller := "scheduler"
	srv := sdk.NewServer(e.r.Handlers(), sdk.Options{Platform: Platform(),
		Caller: func(context.Context) (string, bool) { return caller, true }})
	resp, err := srv.ExecuteTask(ctx, &schedulerv1.ExecuteTaskRequest{TaskType: TypeExpire, Attempt: 1})
	if err != nil || !resp.GetSuccess() || resp.GetMessage() != "expired 0" {
		t.Fatalf("platform run: %v %+v", err, resp)
	}
	if resp, _ := srv.ExecuteTask(ctx, &schedulerv1.ExecuteTaskRequest{TaskType: TypeReminders, TenantId: tenant}); !resp.GetPermanentFailure() {
		t.Fatalf("tenant-scoped call refused: %+v", resp)
	}
	caller = "gateway"
	if _, err := srv.ExecuteTask(ctx, &schedulerv1.ExecuteTaskRequest{TaskType: TypeExpire}); err == nil {
		t.Fatal("only the scheduler may call")
	}
	ds := Descriptors()
	if len(ds) != 2 || !ds[0].Platform || !ds[1].Platform || ds[0].DefaultCron != "*/15 * * * *" || ds[1].DefaultCron != "0 * * * *" {
		t.Fatalf("descriptors %+v", ds)
	}
	for _, d := range ds {
		var schema map[string]any
		if err := json.Unmarshal([]byte(d.PayloadSchema), &schema); err != nil {
			t.Fatalf("%s schema: %v", d.Type, err)
		}
	}
	if len(e.audit.events) != 0 {
		t.Fatal("nothing to audit")
	}
}
