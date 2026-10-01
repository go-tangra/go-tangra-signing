package submissions

import (
	"bytes"
	"context"
	"errors"
	"io"
	"sync"
	"testing"
	"time"

	"github.com/go-tangra/go-tangra-notification/sdk/v4/pkg/notifyclient"

	"github.com/go-tangra/go-tangra-signing/v4/internal/apperr"
	"github.com/go-tangra/go-tangra-signing/v4/internal/audit"
	"github.com/go-tangra/go-tangra-signing/v4/internal/authz"
	"github.com/go-tangra/go-tangra-signing/v4/internal/blob"
	"github.com/go-tangra/go-tangra-signing/v4/internal/contacts"
	"github.com/go-tangra/go-tangra-signing/v4/internal/events"
	"github.com/go-tangra/go-tangra-signing/v4/internal/fieldvalues"
	"github.com/go-tangra/go-tangra-signing/v4/internal/mail"
	"github.com/go-tangra/go-tangra-signing/v4/internal/memstore"
	"github.com/go-tangra/go-tangra-signing/v4/internal/pdf/pdftest"
	"github.com/go-tangra/go-tangra-signing/v4/internal/repo"
	"github.com/go-tangra/go-tangra-signing/v4/internal/sealed"
	"github.com/go-tangra/go-tangra-signing/v4/internal/store"
)

const (
	tenant = "0190f7c2-6a3e-7c1a-9b2e-2f6f9d1b4c55"
	other  = "0190f7c2-6a3e-7c1a-9b2e-2f6f9d1b4c66"
)

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

func (r *recorder) has(t audit.EventType, outcome string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, e := range r.events {
		if e.EventType == t && e.Outcome == outcome {
			return true
		}
	}
	return false
}

type sent struct {
	key, to string
	vars    map[string]string
}

type sender struct {
	mu    sync.Mutex
	calls []sent
}

func (s *sender) SendKey(_ context.Context, _, key, to string, vars map[string]string, _ string) (notifyclient.Result, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.calls = append(s.calls, sent{key, to, vars})
	if to == "fail@example.org" {
		return notifyclient.Result{}, errors.New("email not configured")
	}
	return notifyclient.Result{Sent: true}, nil
}

func (s *sender) to(key string) []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []string
	for _, c := range s.calls {
		if c.key == key {
			out = append(out, c.to)
		}
	}
	return out
}

type env struct {
	svc    *Service
	mem    *memstore.Mem
	blob   *blob.Fake
	audit  *recorder
	mail   *sender
	dir    *contacts.Fake
	events *events.Recorder
	now    time.Time
	tpl    store.Template
}

var perms = authz.Static{
	"admin":    authz.Permissions,
	"sender":   {authz.SigningSign, authz.SubmissionsCreate},
	"reader":   {authz.SigningSign, authz.SigningRead},
	"alice":    {authz.SigningSign},
	"bob":      {authz.SigningSign},
	"carol":    {authz.SigningSign},
	"outsider": {authz.SigningSign},
}

func user(id string) authz.Subjects { return authz.User(tenant, id, nil) }

func newEnv(t *testing.T) *env {
	t.Helper()
	e := &env{mem: memstore.New(), blob: blob.NewFake(), audit: &recorder{}, mail: &sender{}, events: &events.Recorder{},
		now: time.Date(2026, 9, 29, 10, 0, 0, 0, time.UTC)}
	e.dir = &contacts.Fake{Users: map[string][]contacts.Contact{tenant: {
		{UserID: "sender", DisplayName: "Sam Sender", Email: "sam@example.org"},
		{UserID: "alice", DisplayName: "Alice Иванова", Email: "alice@example.org"},
		{UserID: "bob", DisplayName: "Bob", Email: "fail@example.org"},
		{UserID: "carol", DisplayName: "", Email: "carol@example.org"},
		{UserID: "noemail", DisplayName: "No Mail"},
	}}}
	e.svc = New(Deps{Store: e.mem, Blob: e.blob, Audit: e.audit, Checker: perms, Contacts: e.dir,
		Mail: mail.Mailer{Sender: e.mail, PortalBaseURL: "https://portal.example.org"}, Events: events.Emitter{Pub: e.events},
		Now: func() time.Time { return e.now }})
	e.tpl = e.template(t, tenant, store.TemplateActive)
	return e
}

func (e *env) template(t *testing.T, tn, status string) store.Template {
	t.Helper()
	pdf := pdftest.Contract(1)
	id := store.NewID()
	key := blob.TemplatePDF(tn, id, store.NewID())
	sum, err := e.blob.Put(ctx, key, bytes.NewReader(pdf), int64(len(pdf)), "application/pdf")
	if err != nil {
		t.Fatal(err)
	}
	days := 14
	name := "Employment contract"
	if e.tpl.ID != "" {
		name += " " + id
	}
	tpl := store.Template{ID: id, TenantID: tn, Name: name, Status: status, PDFKey: key, PDFSHA256: sum,
		PDFSize: int64(len(pdf)), PDFPages: 1, FileName: "c.pdf",
		Parties: []store.Party{{Key: "employee", Name: "Employee"}, {Key: "employer", Name: "Employer"}},
		Fields: []store.Field{
			{ID: "name", Name: "Name", Type: "text", Party: "employee", Page: 1, X: 0.1, Y: 0.1, W: 0.3, H: 0.03, Required: true},
			{ID: "salary", Name: "Salary", Type: "number", Party: "employer", Page: 1, X: 0.1, Y: 0.2, W: 0.3, H: 0.03},
			{ID: "city", Name: "City", Type: "select", Party: "employee", Page: 1, X: 0.1, Y: 0.3, W: 0.3, H: 0.03, Options: []string{"Sofia", "Plovdiv"}},
			{ID: "sig", Name: "Signature", Type: "signature", Party: "employee", Page: 1, X: 0.1, Y: 0.8, W: 0.3, H: 0.08},
		},
		DefaultExpiryDays: &days, DefaultReminder: &store.Reminder{IntervalDays: 3, Max: 2},
		Version: 1, CreatedAt: e.now, CreatedBy: "admin", UpdatedAt: e.now, UpdatedBy: "admin"}
	if err := e.mem.CreateTemplate(ctx, tpl); err != nil {
		t.Fatal(err)
	}
	return tpl
}

func pos(n int) *int { return &n }

func (e *env) input(mode string) CreateInput {
	return CreateInput{TemplateID: e.tpl.ID, Mode: mode,
		Signers: []SignerInput{{UserID: "alice", Party: "employee", Position: pos(0)}, {UserID: "bob", Party: "employer", Position: pos(1)}},
		Prefill: map[string]string{"salary": "5000,50", "city": "Sofia"}}
}

func (e *env) create(t *testing.T, mode string) Detail {
	t.Helper()
	d, err := e.svc.Create(ctx, user("sender"), e.input(mode))
	if err != nil {
		t.Fatal(err)
	}
	return d
}

func signerOf(d Detail, userID string) store.Signer {
	for _, sg := range d.Signers {
		if sg.UserID == userID {
			return sg
		}
	}
	return store.Signer{}
}

func TestCreateFreezesTemplate(t *testing.T) {
	e := newEnv(t)
	d := e.create(t, store.ModeSequential)
	sub := d.Submission
	if sub.Status != store.SubmissionDraft || sub.Name != "Employment contract" || sub.CreatedBy != "sender" || !d.CanControl {
		t.Fatalf("submission %+v", sub)
	}
	if sub.ExpiresAt == nil || !sub.ExpiresAt.Equal(e.now.AddDate(0, 0, 14)) || sub.Reminder == nil || sub.Reminder.Max != 2 {
		t.Fatalf("template defaults not applied: %v %v", sub.ExpiresAt, sub.Reminder)
	}
	if len(sub.Fields) != 4 || sub.Fields[1].Prefill != "5000,50" || sub.Fields[2].Prefill != "Sofia" || len(sub.Parties) != 2 {
		t.Fatalf("frozen fields %+v", sub.Fields)
	}
	if sub.PDFKey == e.tpl.PDFKey || !e.blob.Has(sub.PDFKey) || !blob.InTenant(sub.PDFKey, tenant) {
		t.Fatal("version 0 must be a copy under the submission")
	}
	a, b := signerOf(d, "alice"), signerOf(d, "bob")
	if a.Name != "Alice Иванова" || a.Email != "alice@example.org" || a.Position != 0 || b.Position != 1 || a.Status != store.SignerPending {
		t.Fatalf("signers %+v %+v", a, b)
	}
	v0, err := e.mem.GetVersion(ctx, tenant, sub.ID, 0)
	if err != nil || v0.SHA256 != e.tpl.PDFSHA256 {
		t.Fatalf("v0 %+v %v", v0, err)
	}
	if !e.audit.has(audit.SubmissionCreate, audit.OutcomeOK) {
		t.Fatal("audit")
	}
	// Explicit expiry and reminder, parallel mode (positions collapse), and a
	// display name falling back to the e-mail.
	exp := e.now.Add(48 * time.Hour)
	in := e.input(store.ModeParallel)
	in.Signers[1].UserID = "carol"
	in.ExpiresAt, in.Reminder, in.Name = &exp, &store.Reminder{IntervalDays: 1, Max: 1}, "  Custom  "
	d2, err := e.svc.Create(ctx, user("sender"), in)
	if err != nil {
		t.Fatal(err)
	}
	if d2.Submission.Name != "Custom" || !d2.Submission.ExpiresAt.Equal(exp) || signerOf(d2, "carol").Position != 0 || signerOf(d2, "carol").Name != "carol@example.org" {
		t.Fatalf("parallel %+v", d2)
	}
}

func TestCreateRefusals(t *testing.T) {
	e := newEnv(t)
	draft := e.template(t, tenant, store.TemplateDraft)
	foreign := e.template(t, other, store.TemplateActive)
	past := e.now.Add(-time.Hour)
	far := e.now.AddDate(2, 0, 0)
	cases := []struct {
		name string
		subj authz.Subjects
		mod  func(*CreateInput)
		want error
	}{
		{"no permission", user("alice"), nil, apperr.Forbidden},
		{"mode", user("sender"), func(in *CreateInput) { in.Mode = "random" }, apperr.Validation},
		{"draft template", user("sender"), func(in *CreateInput) { in.TemplateID = draft.ID }, apperr.TemplateNotActive},
		{"foreign template", user("sender"), func(in *CreateInput) { in.TemplateID = foreign.ID }, apperr.NotFound},
		{"name", user("sender"), func(in *CreateInput) { in.Name = "a\nb" }, apperr.Validation},
		{"too few signers", user("sender"), func(in *CreateInput) { in.Signers = in.Signers[:1] }, apperr.InvalidSigner},
		{"unknown party", user("sender"), func(in *CreateInput) { in.Signers[1].Party = "ghost" }, apperr.InvalidSigner},
		{"same party", user("sender"), func(in *CreateInput) { in.Signers[1].Party = "employee" }, apperr.InvalidSigner},
		{"same user", user("sender"), func(in *CreateInput) { in.Signers[1].UserID = "alice" }, apperr.InvalidSigner},
		{"bad position", user("sender"), func(in *CreateInput) { in.Signers[1].Position = pos(5000) }, apperr.InvalidSigner},
		{"no e-mail", user("sender"), func(in *CreateInput) { in.Signers[1].UserID = "noemail" }, apperr.InvalidSigner},
		{"not a member", user("sender"), func(in *CreateInput) { in.Signers[1].UserID = "stranger" }, apperr.InvalidSigner},
		{"prefill unknown", user("sender"), func(in *CreateInput) { in.Prefill = map[string]string{"ghost": "x"} }, apperr.InvalidPrefill},
		{"prefill number", user("sender"), func(in *CreateInput) { in.Prefill = map[string]string{"salary": "lots"} }, apperr.InvalidPrefill},
		{"prefill option", user("sender"), func(in *CreateInput) { in.Prefill = map[string]string{"city": "Varna"} }, apperr.InvalidPrefill},
		{"prefill signature", user("sender"), func(in *CreateInput) { in.Prefill = map[string]string{"sig": "x"} }, apperr.InvalidPrefill},
		{"expiry past", user("sender"), func(in *CreateInput) { in.ExpiresAt = &past }, apperr.Validation},
		{"expiry far", user("sender"), func(in *CreateInput) { in.ExpiresAt = &far }, apperr.Validation},
		{"reminder", user("sender"), func(in *CreateInput) { in.Reminder = &store.Reminder{IntervalDays: 0, Max: 99} }, apperr.Validation},
	}
	for _, c := range cases {
		in := e.input(store.ModeSequential)
		if c.mod != nil {
			c.mod(&in)
		}
		if _, err := e.svc.Create(ctx, c.subj, in); !errors.Is(err, c.want) {
			t.Errorf("%s: %v, want %v", c.name, err, c.want)
		}
	}
	e.dir.Err = errors.New("auth down")
	if _, err := e.svc.Create(ctx, user("sender"), e.input(store.ModeSequential)); !errors.Is(err, apperr.TemporarilyUnavailable) {
		t.Fatalf("contacts down: %v", err)
	}
	e.dir.Err = nil
	e.mem.Fail("CreateSubmission", errors.New("db"))
	before, _ := e.blob.List(ctx, "", 100000)
	if _, err := e.svc.Create(ctx, user("sender"), e.input(store.ModeSequential)); err == nil || countKeys(e) != len(before) {
		t.Fatalf("failed create must remove the copied PDF: %v", err)
	}
	if !e.audit.has(audit.SubmissionCreate, audit.OutcomeRefused) {
		t.Fatal("refusals audited")
	}
}

func TestSendSequentialAndParallel(t *testing.T) {
	e := newEnv(t)
	d := e.create(t, store.ModeSequential)
	id := d.Submission.ID
	if _, err := e.svc.Send(ctx, user("alice"), id); !errors.Is(err, apperr.Forbidden) {
		t.Fatalf("a signer cannot send: %v", err)
	}
	if _, err := e.svc.Send(ctx, user("outsider"), id); !errors.Is(err, apperr.NotFound) {
		t.Fatalf("outsider: %v", err)
	}
	got, err := e.svc.Send(ctx, user("sender"), id)
	if err != nil {
		t.Fatal(err)
	}
	a, b := signerOf(got, "alice"), signerOf(got, "bob")
	if got.Submission.Status != store.SubmissionInProgress || got.Submission.SentAt == nil || a.Status != store.SignerInvited ||
		b.Status != store.SignerPending || a.NextReminderAt == nil || !a.NextReminderAt.Equal(e.now.AddDate(0, 0, 3)) {
		t.Fatalf("after send: %+v / %+v / %+v", got.Submission, a, b)
	}
	if to := e.mail.to(mail.Invitation); len(to) != 1 || to[0] != "alice@example.org" {
		t.Fatalf("invitations %v", to)
	}
	c := e.mail.calls[0]
	if c.vars["document"] != "Employment contract" || c.vars["sender"] != "Sam Sender" || c.vars["signer"] != "Alice Иванова" ||
		c.vars["link"] != "https://portal.example.org/signing/sign/"+a.ID {
		t.Fatalf("vars %v", c.vars)
	}
	if len(e.events.OfType(events.Inbox)) != 1 {
		t.Fatal("inbox refresh for the invited signer")
	}
	if _, err := e.svc.Send(ctx, user("sender"), id); !errors.Is(err, apperr.NotDraft) {
		t.Fatalf("second send: %v", err)
	}

	// Parallel: everyone invited; a failed mail is recorded on the signer.
	p := e.create(t, store.ModeParallel)
	got, err = e.svc.Send(ctx, user("admin"), p.Submission.ID)
	if err != nil {
		t.Fatal(err)
	}
	if signerOf(got, "bob").Status != store.SignerInvited || signerOf(got, "bob").MailError != "email not configured" {
		t.Fatalf("bob %+v", signerOf(got, "bob"))
	}
	evs, _ := e.svc.Events(ctx, user("sender"), p.Submission.ID)
	if !hasEvent(evs, "mail.failed") || !hasEvent(evs, "submission.sent") || !hasEvent(evs, "signer.invited") {
		t.Fatalf("history %+v", evs)
	}
	// A later successful mail clears the error (resend after fixing the channel).
	e.dir.Users[tenant][2].Email = "bob@example.org"
	bob := signerOf(got, "bob")
	bob.Email = "bob@example.org"
	e.svc.Mailed(ctx, got.Submission, bob, mail.Invitation, "")
	if s, _ := e.mem.GetSigner(ctx, tenant, bob.ID); s.MailError != "" {
		t.Fatal("mail error not cleared")
	}

	// An expired draft cannot be sent.
	x := e.create(t, store.ModeParallel)
	e.now = e.now.AddDate(0, 1, 0)
	if _, err := e.svc.Send(ctx, user("sender"), x.Submission.ID); !errors.Is(err, apperr.Validation) {
		t.Fatalf("expired draft: %v", err)
	}
	if !e.audit.has(audit.SubmissionSend, audit.OutcomeRefused) || !e.audit.has(audit.SubmissionSend, audit.OutcomeOK) {
		t.Fatal("send audit")
	}
}

func hasEvent(evs []store.Event, typ string) bool {
	for _, ev := range evs {
		if ev.Type == typ {
			return true
		}
	}
	return false
}

func TestTurns(t *testing.T) {
	seq := store.Submission{Mode: store.ModeSequential}
	sg := []store.Signer{
		{ID: "a", Position: 0, Status: store.SignerSigned},
		{ID: "b", Position: 1, Status: store.SignerPending},
		{ID: "c", Position: 1, Status: store.SignerPending},
		{ID: "d", Position: 2, Status: store.SignerPending},
	}
	due := Due(seq, sg)
	if len(due) != 2 || due[0].ID != "b" || due[1].ID != "c" {
		t.Fatalf("due %+v", due)
	}
	if !Turn(seq, sg, sg[1]) || Turn(seq, sg, sg[3]) {
		t.Fatal("turn")
	}
	par := store.Submission{Mode: store.ModeParallel}
	if len(Due(par, sg)) != 3 || !Turn(par, sg, sg[3]) {
		t.Fatal("parallel")
	}
	if Complete(sg) || Complete(nil) || !Complete(sg[:1]) {
		t.Fatal("complete")
	}
	sg[1].Status, sg[2].Status = store.SignerInvited, store.SignerOpened
	if len(Due(seq, sg)) != 0 {
		t.Fatal("already invited signers are not due again")
	}
}

func TestViewsAndDocuments(t *testing.T) {
	e := newEnv(t)
	d := e.create(t, store.ModeSequential)
	id := d.Submission.ID
	e.create(t, store.ModeParallel) // a second one by the same sender
	if _, err := e.svc.Send(ctx, user("sender"), id); err != nil {
		t.Fatal(err)
	}
	for _, u := range []string{"sender", "alice", "bob", "reader", "admin"} {
		if got, err := e.svc.Get(ctx, user(u), id); err != nil || got.CanControl != (u == "sender" || u == "admin") {
			t.Errorf("%s: %v control=%v", u, err, got.CanControl)
		}
	}
	if _, err := e.svc.Get(ctx, user("outsider"), id); !errors.Is(err, apperr.NotFound) {
		t.Fatalf("outsider: %v", err)
	}
	if _, err := e.svc.Get(ctx, authz.User(other, "sender", nil), id); !errors.Is(err, apperr.NotFound) {
		t.Fatalf("other tenant: %v", err)
	}
	if list, total, err := e.svc.List(ctx, user("reader"), repo.SubmissionFilter{}, false); err != nil || total != 2 || len(list) != 2 {
		t.Fatalf("reader list %d %v", total, err)
	}
	if _, total, _ := e.svc.List(ctx, user("alice"), repo.SubmissionFilter{}, false); total != 0 {
		t.Fatalf("a signer's list shows only what they sent: %d", total)
	}
	if _, total, _ := e.svc.List(ctx, user("admin"), repo.SubmissionFilter{}, true); total != 0 {
		t.Fatalf("mine: %d", total)
	}
	if items, total, err := e.svc.Inbox(ctx, user("alice"), repo.InboxFilter{Page: 1, PageSize: 20}); err != nil || total != 1 || items[0].Submission.ID != id {
		t.Fatalf("inbox %d %v", total, err)
	}
	if _, _, err := e.svc.Inbox(ctx, authz.System(), repo.InboxFilter{Page: 1, PageSize: 20}); !errors.Is(err, apperr.Forbidden) {
		t.Fatal("inbox needs a user")
	}
	if m, err := e.svc.Members(ctx, user("sender"), " ali "); err != nil || len(m) != 1 || m[0].UserID != "alice" {
		t.Fatalf("members %v %v", m, err)
	}
	if _, err := e.svc.Members(ctx, user("alice"), ""); !errors.Is(err, apperr.Forbidden) {
		t.Fatal("members needs submissions:create")
	}
	if _, err := e.svc.Events(ctx, user("outsider"), id); !errors.Is(err, apperr.NotFound) {
		t.Fatal("events visibility")
	}

	read := func(u string, v int) error {
		rc, _, _, err := e.svc.Document(ctx, user(u), id, v)
		if err == nil {
			b, _ := io.ReadAll(rc)
			_ = rc.Close()
			if !bytes.HasPrefix(b, []byte("%PDF")) {
				t.Fatalf("not a pdf for %s", u)
			}
		}
		return err
	}
	if err := read("sender", 0); err != nil {
		t.Fatalf("sender v0: %v", err)
	}
	if err := read("alice", -1); !errors.Is(err, apperr.Forbidden) {
		t.Fatalf("unsigned signer: %v", err)
	}
	if err := read("alice", 3); !errors.Is(err, apperr.NotFound) {
		t.Fatalf("other version: %v", err)
	}
	if err := read("sender", 7); !errors.Is(err, apperr.NotFound) {
		t.Fatalf("missing version: %v", err)
	}
	// After Alice signed she may download the current version; once
	// completed everyone gets the final one.
	alice := signerOf(d, "alice")
	alice.Status = store.SignerSigned
	e.mem.PutSigner(alice)
	if err := read("alice", -1); err != nil {
		t.Fatalf("signed signer: %v", err)
	}
	sub, _, _ := e.mem.GetSubmission(ctx, tenant, id)
	zero := 0
	sub.Status, sub.FinalVersion = store.SubmissionCompleted, &zero
	e.mem.PutSubmission(sub)
	if err := read("bob", -1); err != nil {
		t.Fatalf("participant final: %v", err)
	}
	if _, _, err := e.svc.AuditTrail(ctx, user("bob"), id); !errors.Is(err, apperr.NotFound) {
		t.Fatalf("no audit trail yet: %v", err)
	}
	key := blob.AuditTrail(tenant, id)
	_, _ = e.blob.Put(ctx, key, bytes.NewReader([]byte("%PDF-trail")), 10, "application/pdf")
	sub.AuditTrailKey = key
	e.mem.PutSubmission(sub)
	if rc, _, err := e.svc.AuditTrail(ctx, user("bob"), id); err != nil {
		t.Fatalf("audit trail: %v", err)
	} else {
		_ = rc.Close()
	}
	if _, _, err := e.svc.AuditTrail(ctx, user("outsider"), id); !errors.Is(err, apperr.NotFound) {
		t.Fatal("audit trail visibility")
	}
	if d.String() == "" {
		t.Fatal("string")
	}
}

func TestControl(t *testing.T) {
	e := newEnv(t)
	d := e.create(t, store.ModeSequential)
	id := d.Submission.ID
	if _, err := e.svc.Send(ctx, user("sender"), id); err != nil {
		t.Fatal(err)
	}
	alice, bob := signerOf(d, "alice"), signerOf(d, "bob")

	// Resend: only to invited/opened signers, by the sender.
	if _, err := e.svc.Resend(ctx, user("alice"), id, alice.ID); !errors.Is(err, apperr.Forbidden) {
		t.Fatalf("resend by signer: %v", err)
	}
	if _, err := e.svc.Resend(ctx, user("sender"), id, bob.ID); !errors.Is(err, apperr.SignerFinal) {
		t.Fatalf("resend to pending: %v", err)
	}
	if _, err := e.svc.Resend(ctx, user("sender"), id, "nobody"); !errors.Is(err, apperr.NotFound) {
		t.Fatalf("resend unknown: %v", err)
	}
	if _, err := e.svc.Resend(ctx, user("sender"), id, alice.ID); err != nil || len(e.mail.to(mail.Invitation)) != 2 {
		t.Fatalf("resend: %v", err)
	}

	// Replace: an unsigned slot to another member; invited slots are re-invited.
	if _, err := e.svc.ReplaceSigner(ctx, user("sender"), id, alice.ID, "bob"); !errors.Is(err, apperr.InvalidSigner) {
		t.Fatalf("replace with an existing signer: %v", err)
	}
	if _, err := e.svc.ReplaceSigner(ctx, user("sender"), id, alice.ID, "stranger"); !errors.Is(err, apperr.InvalidSigner) {
		t.Fatalf("replace with a stranger: %v", err)
	}
	got, err := e.svc.ReplaceSigner(ctx, user("sender"), id, alice.ID, "carol")
	if err != nil {
		t.Fatal(err)
	}
	c := signerOf(got, "carol")
	if c.ID != alice.ID || c.Status != store.SignerInvited || c.Name != "carol@example.org" || signerOf(got, "alice").ID != "" {
		t.Fatalf("replaced %+v", c)
	}
	if to := e.mail.to(mail.Invitation); to[len(to)-1] != "carol@example.org" {
		t.Fatalf("new signer invited: %v", to)
	}
	if _, err := e.svc.ReplaceSigner(ctx, user("sender"), id, "nobody", "alice"); !errors.Is(err, apperr.NotFound) {
		t.Fatalf("replace unknown slot: %v", err)
	}
	signed := c
	signed.Status = store.SignerSigned
	e.mem.PutSigner(signed)
	if _, err := e.svc.ReplaceSigner(ctx, user("sender"), id, c.ID, "alice"); !errors.Is(err, apperr.SignerFinal) {
		t.Fatalf("replace signed: %v", err)
	}
	e.dir.Err = errors.New("down")
	if _, err := e.svc.ReplaceSigner(ctx, user("sender"), id, bob.ID, "alice"); !errors.Is(err, apperr.TemporarilyUnavailable) {
		t.Fatalf("contacts down: %v", err)
	}
	e.dir.Err = nil
	if got, err := e.svc.ReplaceSigner(ctx, user("sender"), id, bob.ID, "alice"); err != nil || signerOf(got, "alice").Status != store.SignerPending {
		t.Fatalf("replace pending: %v", err)
	}

	// Cancel: a reason is required; everyone invited and the sender are told.
	if _, err := e.svc.Cancel(ctx, user("sender"), id, "  "); !errors.Is(err, apperr.Validation) {
		t.Fatalf("empty reason: %v", err)
	}
	got, err = e.svc.Cancel(ctx, user("sender"), id, "wrong salary")
	if err != nil || got.Submission.Status != store.SubmissionCancelled || got.Submission.CancelReason != "wrong salary" {
		t.Fatalf("cancel: %v %+v", err, got.Submission)
	}
	if to := e.mail.to(mail.Cancelled); len(to) != 1 || to[0] != "carol@example.org" {
		t.Fatalf("cancel mails %v", to)
	}
	if len(e.events.OfType(events.SubmissionCancelled)) != 1 {
		t.Fatal("cancelled event")
	}
	if _, err := e.svc.Cancel(ctx, user("sender"), id, "again"); !errors.Is(err, apperr.SubmissionNotOpen) {
		t.Fatalf("cancel twice: %v", err)
	}
	if _, err := e.svc.Resend(ctx, user("sender"), id, c.ID); !errors.Is(err, apperr.SubmissionNotOpen) {
		t.Fatalf("resend after cancel: %v", err)
	}
	if _, err := e.svc.ReplaceSigner(ctx, user("sender"), id, bob.ID, "sender"); !errors.Is(err, apperr.SubmissionNotOpen) {
		t.Fatalf("replace after cancel: %v", err)
	}

	// Delete removes the rows and every object.
	if err := e.svc.Delete(ctx, user("alice"), id); !errors.Is(err, apperr.Forbidden) {
		t.Fatalf("delete by signer: %v", err)
	}
	upload := blob.SignerUpload(tenant, id, c.ID, "photo", "png")
	_, _ = e.blob.Put(ctx, upload, bytes.NewReader([]byte("x")), 1, "image/png")
	if err := e.svc.Delete(ctx, user("sender"), id); err != nil {
		t.Fatal(err)
	}
	if e.blob.Has(d.Submission.PDFKey) || e.blob.Has(upload) {
		t.Fatal("objects left behind")
	}
	if _, err := e.svc.Get(ctx, user("sender"), id); !errors.Is(err, apperr.NotFound) {
		t.Fatal("deleted submission still visible")
	}
	for _, typ := range []audit.EventType{audit.SubmissionResend, audit.SubmissionSignerReplace, audit.SubmissionCancel, audit.SubmissionDelete} {
		if !e.audit.has(typ, audit.OutcomeOK) || !e.audit.has(typ, audit.OutcomeRefused) {
			t.Errorf("audit %s", typ)
		}
	}

	// Deleting an in-progress submission closes the signers' inbox entries.
	x := e.create(t, store.ModeParallel)
	if _, err := e.svc.Send(ctx, user("sender"), x.Submission.ID); err != nil {
		t.Fatal(err)
	}
	before := len(e.events.OfType(events.Inbox))
	if err := e.svc.Delete(ctx, user("admin"), x.Submission.ID); err != nil || len(e.events.OfType(events.Inbox)) != before+2 {
		t.Fatalf("delete in progress: %v", err)
	}
}

func TestValues(t *testing.T) {
	f := func(typ string, opts ...string) store.Field { return store.Field{Type: typ, Options: opts} }
	ok := []struct {
		f store.Field
		v string
	}{
		{f("text"), "Мария\tИванова"}, {f("number"), " 1234,5 "}, {f("number"), "-3e2"}, {f("date"), "2026-09-29"},
		{f("checkbox"), "true"}, {f("checkbox"), "false"}, {f("select", "a", "b"), "b"}, {f("radio", "x"), "x"},
		{f("cells"), "BG80BNBG"}, {f("signature"), ""},
	}
	for _, c := range ok {
		if !ValidValue(c.f, c.v) {
			t.Errorf("%s %q refused", c.f.Type, c.v)
		}
	}
	bad := []struct {
		f store.Field
		v string
	}{
		{f("text"), "a\x00b"}, {f("text"), string(bytes.Repeat([]byte("x"), MaxValueLength+1))}, {f("number"), "NaN"},
		{f("number"), "1e999"}, {f("date"), "29.09.2026"}, {f("checkbox"), "yes"}, {f("select", "a"), "c"},
		{f("cells"), string(bytes.Repeat([]byte("x"), 201))}, {f("signature"), "x"}, {f("text"), "\xff"},
	}
	for _, c := range bad {
		if ValidValue(c.f, c.v) {
			t.Errorf("%s %q accepted", c.f.Type, c.v)
		}
	}
	for typ, want := range map[string]bool{"text": true, "cells": true, "signature": false, "file": false, "image": false} {
		if TextValued(typ) != want {
			t.Errorf("TextValued(%s)", typ)
		}
	}
	if Filled(f("checkbox"), "false", false) || !Filled(f("checkbox"), "true", false) || Filled(f("image"), "", false) ||
		!Filled(f("file"), "", true) || !Filled(f("signature"), "", false) || Filled(f("text"), "  ", false) || !Filled(f("text"), "x", false) {
		t.Fatal("Filled")
	}
}

func countKeys(e *env) int {
	keys, _ := e.blob.List(ctx, "", 100000)
	return len(keys)
}

func TestPrefillsAreSealed(t *testing.T) {
	e := newEnv(t)
	env, _ := sealed.NewEnvelope(bytes.Repeat([]byte{5}, 32))
	e.svc.d.Values = fieldvalues.Box{E: env}
	d := e.create(t, store.ModeParallel)
	sub, _, _ := e.mem.GetSubmission(ctx, tenant, d.Submission.ID)
	for _, f := range sub.Fields {
		if f.ID == "salary" {
			if bytes.Contains([]byte(f.Prefill), []byte("5000")) || f.Prefill == "" {
				t.Fatalf("prefill in clear: %q", f.Prefill)
			}
			if v, err := e.svc.d.Values.OpenString(f.Prefill, fieldvalues.PrefillAD(sub.ID, f.ID)); err != nil || v != "5000,50" {
				t.Fatalf("open prefill %q %v", v, err)
			}
		}
	}
}
