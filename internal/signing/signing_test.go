package signing

import (
	"bytes"
	"context"
	"crypto/x509"
	"errors"
	"image"
	"image/color"
	"image/png"
	"io"
	"sync"
	"testing"
	"time"

	"github.com/go-tangra/go-tangra-notification/sdk/v4/pkg/notifyclient"

	"github.com/go-tangra/go-tangra-signing/v4/internal/apperr"
	"github.com/go-tangra/go-tangra-signing/v4/internal/audit"
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
	"github.com/go-tangra/go-tangra-signing/v4/internal/store"
	"github.com/go-tangra/go-tangra-signing/v4/internal/submissions"
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

type sender struct {
	mu   sync.Mutex
	keys []string
	to   []string
}

func (s *sender) SendKey(_ context.Context, _, key, to string, vars map[string]string, _ string) (notifyclient.Result, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for k, v := range vars {
		if v == "5000" || v == "Мария" {
			panic("field value in mail variable " + k)
		}
	}
	s.keys, s.to = append(s.keys, key), append(s.to, to)
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

type env struct {
	svc    *Service
	subs   *submissions.Service
	me     *certs.Me
	pki    *pki.PKI
	mem    *memstore.Mem
	blob   *blob.Fake
	audit  *recorder
	mail   *sender
	events *events.Recorder
	now    time.Time
	tpl    store.Template
}

var perms = authz.Static{
	"sender": {authz.SigningSign, authz.SubmissionsCreate},
	"alice":  {authz.SigningSign},
	"bob":    {authz.SigningSign},
	"carol":  {authz.SigningSign},
}

func user(id string) authz.Subjects { return authz.User(tenant, id, nil) }

func newEnv(t *testing.T, opts ...func(*Deps)) *env {
	t.Helper()
	e := &env{mem: memstore.New(), blob: blob.NewFake(), audit: &recorder{}, mail: &sender{}, events: &events.Recorder{},
		now: time.Now().UTC().Truncate(time.Second)}
	clock := func() time.Time { return e.now }
	dir := &contacts.Fake{Users: map[string][]contacts.Contact{tenant: {
		{UserID: "sender", DisplayName: "Sam Sender", Email: "sam@example.org"},
		{UserID: "alice", DisplayName: "Alice Иванова", Email: "alice@example.org"},
		{UserID: "bob", DisplayName: "Bob Builder", Email: "bob@example.org"},
		{UserID: "carol", DisplayName: "Carol", Email: "carol@example.org"},
	}}}
	sealer, _ := sealed.NewEnvelope(bytes.Repeat([]byte{7}, 32))
	e.pki = pki.New(pki.Deps{Store: e.mem, Sealer: sealer, Now: clock,
		Config: pki.Config{CAValidityYears: 10, CARenewBeforeYears: 2, CertValidityYears: 2, CRLValidityDays: 7, PINIterations: pincrypto.MinIterations}})
	e.me = certs.New(certs.Deps{Store: e.mem, PKI: e.pki, Contacts: dir, Audit: e.audit, Now: clock,
		Rules: pincrypto.Rules{Min: 6, Max: 32}, Lockout: pincrypto.Lockout{Attempts: 3, Duration: 15 * time.Minute}})
	emitter := events.Emitter{Pub: e.events}
	e.subs = submissions.New(submissions.Deps{Store: e.mem, Blob: e.blob, Audit: e.audit, Checker: perms, Contacts: dir,
		Mail: mail.Mailer{Sender: e.mail, PortalBaseURL: "https://portal.example.org"}, Events: emitter, Now: clock})
	d := Deps{Store: e.mem, Blob: e.blob, Audit: e.audit, Subs: e.subs, Me: e.me, PKI: e.pki, Events: emitter, Now: clock, Location: "Sofia"}
	for _, o := range opts {
		o(&d)
	}
	e.svc = New(d)
	e.tpl = e.template(t)
	return e
}

func (e *env) template(t *testing.T) store.Template {
	t.Helper()
	pdf := pdftest.Contract(2)
	id := store.NewID()
	key := blob.TemplatePDF(tenant, id, store.NewID())
	sum, _ := e.blob.Put(ctx, key, bytes.NewReader(pdf), int64(len(pdf)), "application/pdf")
	tpl := store.Template{ID: id, TenantID: tenant, Name: "Contract " + id, Status: store.TemplateActive, PDFKey: key, PDFSHA256: sum,
		PDFSize: int64(len(pdf)), PDFPages: 2, FileName: "c.pdf",
		Parties: []store.Party{{Key: "employee", Name: "Employee"}, {Key: "employer", Name: "Employer"}},
		Fields: []store.Field{
			{ID: "name", Name: "Name", Type: "text", Party: "employee", Page: 1, X: 0.2, Y: 0.15, W: 0.4, H: 0.03, Required: true},
			{ID: "agree", Name: "Agree", Type: "checkbox", Party: "employee", Page: 1, X: 0.1, Y: 0.3, W: 0.03, H: 0.02},
			{ID: "photo", Name: "Photo", Type: "image", Party: "employee", Page: 1, X: 0.6, Y: 0.3, W: 0.2, H: 0.1},
			{ID: "cv", Name: "CV", Type: "file", Party: "employee", Page: 1, X: 0.6, Y: 0.5, W: 0.1, H: 0.03},
			{ID: "esig", Name: "Employee signature", Type: "signature", Party: "employee", Page: 1, X: 0.1, Y: 0.8, W: 0.3, H: 0.08},
			{ID: "salary", Name: "Salary", Type: "number", Party: "employer", Page: 1, X: 0.2, Y: 0.22, W: 0.3, H: 0.03},
			{ID: "rsig", Name: "Employer signature", Type: "signature", Party: "employer", Page: 2, X: 0.6, Y: 0.8, W: 0.3, H: 0.08},
		},
		Version: 1, CreatedAt: e.now, CreatedBy: "sender", UpdatedAt: e.now, UpdatedBy: "sender"}
	if err := e.mem.CreateTemplate(ctx, tpl); err != nil {
		t.Fatal(err)
	}
	return tpl
}

// submission creates and sends a submission (alice = employee, bob = employer).
func (e *env) submission(t *testing.T, mode string) submissions.Detail {
	t.Helper()
	d, err := e.subs.Create(ctx, user("sender"), submissions.CreateInput{TemplateID: e.tpl.ID, Mode: mode,
		Signers: []submissions.SignerInput{{UserID: "alice", Party: "employee"}, {UserID: "bob", Party: "employer"}},
		Prefill: map[string]string{"salary": "5000"}})
	if err != nil {
		t.Fatal(err)
	}
	if d, err = e.subs.Send(ctx, user("sender"), d.Submission.ID); err != nil {
		t.Fatal(err)
	}
	return d
}

func slot(d submissions.Detail, userID string) string {
	for _, sg := range d.Signers {
		if sg.UserID == userID {
			return sg.ID
		}
	}
	return ""
}

func (e *env) setup(t *testing.T, users ...string) {
	t.Helper()
	for _, u := range users {
		if _, err := e.me.Setup(ctx, user(u), "123456"); err != nil {
			t.Fatal(err)
		}
	}
}

func pngBytes(w, h int) []byte {
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for x := 0; x < w; x++ {
		img.Set(x, h/2, color.Black)
	}
	var b bytes.Buffer
	_ = png.Encode(&b, img)
	return b.Bytes()
}

func aliceInput() Input {
	return Input{Values: map[string]string{"name": "Мария", "agree": "true"}, Uploads: map[string][]byte{"photo": pngBytes(40, 40), "cv": []byte("%PDF-cv")},
		Signature: pngBytes(120, 40), PIN: "123456", IP: "10.0.0.1", UserAgent: "test"}
}

func (e *env) finalPDF(t *testing.T, id string) []byte {
	t.Helper()
	rc, _, _, err := e.subs.Document(ctx, user("sender"), id, -1)
	if err != nil {
		t.Fatal(err)
	}
	defer rc.Close()
	b, _ := io.ReadAll(rc)
	return b
}

func (e *env) caPool(t *testing.T) *x509.CertPool {
	t.Helper()
	cas, err := e.mem.ListCAs(ctx, tenant)
	if err != nil || len(cas) == 0 {
		t.Fatalf("CAs: %v", err)
	}
	p := x509.NewCertPool()
	for _, c := range cas {
		x, _ := pki.Parse(c)
		p.AddCert(x)
	}
	return p
}

func TestSequentialFlowCompletes(t *testing.T) {
	e := newEnv(t)
	e.setup(t, "alice", "bob")
	d := e.submission(t, store.ModeSequential)
	id := d.Submission.ID
	a, b := slot(d, "alice"), slot(d, "bob")

	s, err := e.svc.Session(ctx, user("alice"), a)
	if err != nil || !s.CanSign || s.CertState != "active" || len(s.Fields) != 5 || len(s.AllFields) != 7 || s.Values["salary"] != "5000" {
		t.Fatalf("alice session: %+v %v", s, err)
	}
	if s, _ := e.svc.Session(ctx, user("bob"), b); s.CanSign || s.Reason != "not_your_turn" {
		t.Fatalf("bob before alice: %+v", s)
	}
	if _, err := e.svc.Session(ctx, user("bob"), a); !errors.Is(err, apperr.NotFound) {
		t.Fatalf("someone else's slot: %v", err)
	}
	if err := e.svc.Open(ctx, user("alice"), a); err != nil {
		t.Fatal(err)
	}
	if sg, _ := e.mem.GetSigner(ctx, tenant, a); sg.Status != store.SignerOpened || sg.OpenedAt == nil {
		t.Fatalf("opened: %+v", sg)
	}
	if err := e.svc.Open(ctx, user("alice"), a); err != nil {
		t.Fatal("open twice is a no-op")
	}
	rc, v, _, err := e.svc.Document(ctx, user("alice"), a)
	if err != nil || v.Version != 0 {
		t.Fatalf("session document: %v", err)
	}
	_ = rc.Close()

	if _, err := e.svc.Sign(ctx, user("bob"), b, Input{PIN: "123456"}); !errors.Is(err, apperr.NotYourTurn) {
		t.Fatalf("bob first: %v", err)
	}
	res, err := e.svc.Sign(ctx, user("alice"), a, aliceInput())
	if err != nil || res.SignerStatus != store.SignerSigned || res.SubmissionStatus != store.SubmissionInProgress {
		t.Fatalf("alice sign: %+v %v", res, err)
	}
	sg, _ := e.mem.GetSigner(ctx, tenant, a)
	if sg.Values["name"] != "Мария" || sg.Values["agree"] != "true" || !blob.InTenant(sg.Values["photo"], tenant) || !e.blob.Has(sg.Values["cv"]) ||
		sg.Method != store.MethodLocal || sg.CertSerial == "" || sg.CertSubject != "Alice Ivanova" || sg.IP != "10.0.0.1" || sg.SignedAt == nil {
		t.Fatalf("alice signer row: %+v", sg)
	}
	if e.mail.count(mail.NextSigner) != 1 {
		t.Fatal("bob must be invited as next signer")
	}
	if s, _ := e.svc.Session(ctx, user("bob"), b); !s.CanSign || s.Values["name"] != "Мария" || s.Values["photo"] != "" {
		t.Fatalf("bob sees alice's values (not object keys): %+v", s.Values)
	}
	if _, err := e.svc.Sign(ctx, user("alice"), a, aliceInput()); !errors.Is(err, apperr.AlreadySigned) {
		t.Fatalf("sign twice: %v", err)
	}

	res, err = e.svc.Sign(ctx, user("bob"), b, Input{Values: map[string]string{"salary": "5200"}, PIN: "123456"})
	if err != nil || res.SubmissionStatus != store.SubmissionCompleted {
		t.Fatalf("bob sign: %+v %v", res, err)
	}
	sub, _, _ := e.mem.GetSubmission(ctx, tenant, id)
	if sub.FinalVersion == nil || *sub.FinalVersion != 2 || sub.CurrentVersion != 2 || sub.CompletedAt == nil {
		t.Fatalf("completed: %+v", sub)
	}
	if jobs := e.mem.Jobs(); len(jobs) != 1 || jobs[0].SubmissionID != id || jobs[0].Kind != "audit_trail" {
		t.Fatalf("jobs %+v", jobs)
	}
	sigs, err := verify.Verify(e.finalPDF(t, id), verify.Options{Pools: map[string]*x509.CertPool{"tenant": e.caPool(t)}})
	if err != nil || len(sigs) != 2 {
		t.Fatalf("final pdf: %v %v", sigs, err)
	}
	for _, sig := range sigs {
		if !sig.Valid() || sig.Trust != "tenant" {
			t.Fatalf("signature %s: intact %v trust %q %s", sig.Name, sig.Intact, sig.Trust, sig.Problem)
		}
	}
	if sigs[0].Name != "Alice Иванова" || sigs[1].Name != "Bob Builder" || !sigs[1].CoversWhole || sigs[0].CoversWhole {
		t.Fatalf("order/coverage: %s %s", sigs[0].Name, sigs[1].Name)
	}
	if !e.audit.has(audit.SignerSign, audit.OutcomeOK) || len(e.events.OfType(events.Inbox)) < 3 {
		t.Fatal("audit/inbox events")
	}
	evs, _ := e.mem.ListEvents(ctx, tenant, id)
	var types []string
	for _, ev := range evs {
		types = append(types, ev.Type)
	}
	if !contains(types, "signer.signed") || !contains(types, "submission.completed") || !contains(types, "signer.opened") {
		t.Fatalf("history %v", types)
	}
}

func contains(xs []string, x string) bool {
	for _, v := range xs {
		if v == x {
			return true
		}
	}
	return false
}

func TestRefusalsSignNothing(t *testing.T) {
	e := newEnv(t)
	d := e.submission(t, store.ModeParallel)
	a := slot(d, "alice")
	if _, err := e.svc.Sign(ctx, user("alice"), a, aliceInput()); !errors.Is(err, apperr.CertificateMissing) {
		t.Fatalf("no certificate: %v", err)
	}
	e.setup(t, "alice")
	cases := []struct {
		name string
		mod  func(*Input)
		want error
	}{
		{"missing required", func(in *Input) { delete(in.Values, "name") }, apperr.MissingRequired},
		{"foreign field", func(in *Input) { in.Values["salary"] = "1" }, apperr.InvalidValue},
		{"bad value", func(in *Input) { in.Values["agree"] = "yes" }, apperr.InvalidValue},
		{"upload to text", func(in *Input) { in.Uploads["name"] = pngBytes(2, 2) }, apperr.InvalidValue},
		{"upload to signature", func(in *Input) { in.Uploads["esig"] = pngBytes(2, 2) }, apperr.InvalidValue},
		{"not an image", func(in *Input) { in.Uploads["photo"] = []byte("GIF89a....") }, apperr.InvalidValue},
		{"huge image", func(in *Input) { in.Uploads["photo"] = append(pngBytes(2, 2), make([]byte, 2<<20)...) }, apperr.InvalidValue},
		{"huge file", func(in *Input) { in.Uploads["cv"] = make([]byte, 51<<20) }, apperr.PayloadTooLarge},
		{"bad signature image", func(in *Input) { in.Signature = []byte("junk") }, apperr.InvalidValue},
	}
	for _, c := range cases {
		in := aliceInput()
		c.mod(&in)
		if _, err := e.svc.Sign(ctx, user("alice"), a, in); !errors.Is(err, c.want) {
			t.Errorf("%s: %v", c.name, err)
		}
	}
	if _, err := e.svc.Sign(ctx, user("bob"), a, aliceInput()); !errors.Is(err, apperr.NotFound) {
		t.Fatalf("another user's slot: %v", err)
	}
	if _, err := e.svc.Sign(ctx, authz.System(), a, aliceInput()); !errors.Is(err, apperr.NotFound) {
		t.Fatalf("system subject: %v", err)
	}

	// Wrong PIN: counted, attempts left, then locked; nothing signed.
	in := aliceInput()
	in.PIN = "999999"
	_, err := e.svc.Sign(ctx, user("alice"), a, in)
	var ae *apperr.Error
	if !errors.As(err, &ae) || ae.Reason != "pin_invalid" || ae.Detail["attempts_left"] != 2 {
		t.Fatalf("wrong pin: %v %+v", err, ae)
	}
	_, _ = e.svc.Sign(ctx, user("alice"), a, in)
	if _, err := e.svc.Sign(ctx, user("alice"), a, in); !errors.Is(err, apperr.CertificateLocked) {
		t.Fatalf("third failure locks: %v", err)
	}
	if _, err := e.svc.Sign(ctx, user("alice"), a, aliceInput()); !errors.Is(err, apperr.CertificateLocked) {
		t.Fatalf("locked even with the right PIN: %v", err)
	}
	if s, _ := e.svc.Session(ctx, user("alice"), a); s.CertState != "locked" || s.LockedTill == nil {
		t.Fatalf("session shows the lock: %+v", s)
	}
	sub, signers, _ := e.mem.GetSubmission(ctx, tenant, d.Submission.ID)
	if sub.CurrentVersion != 0 || signers[0].Status == store.SignerSigned || signers[1].Status == store.SignerSigned {
		t.Fatal("nothing may be signed after refusals")
	}
	if !e.audit.has(audit.SignerSign, audit.OutcomeRefused) {
		t.Fatal("refusals audited")
	}

	// After the lock expires, a closed submission refuses before the PIN.
	e.now = e.now.Add(time.Hour)
	if _, err := e.subs.Cancel(ctx, user("sender"), d.Submission.ID, "stop"); err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc.Sign(ctx, user("alice"), a, aliceInput()); !errors.Is(err, apperr.SubmissionNotOpen) {
		t.Fatalf("cancelled: %v", err)
	}
	// Drafts are invisible to signers.
	draft, _ := e.subs.Create(ctx, user("sender"), submissions.CreateInput{TemplateID: e.tpl.ID, Mode: store.ModeParallel,
		Signers: []submissions.SignerInput{{UserID: "alice", Party: "employee"}, {UserID: "bob", Party: "employer"}}})
	if _, err := e.svc.Session(ctx, user("alice"), slot(draft, "alice")); !errors.Is(err, apperr.NotFound) {
		t.Fatalf("draft session: %v", err)
	}
}

func TestFailureRollsBackAndRemovesObjects(t *testing.T) {
	e := newEnv(t)
	e.setup(t, "alice")
	d := e.submission(t, store.ModeParallel)
	a := slot(d, "alice")
	before, _ := e.blob.List(ctx, "", 1000)
	e.mem.Fail("UpdateSubmission", errors.New("db down"))
	if _, err := e.svc.Sign(ctx, user("alice"), a, aliceInput()); err == nil {
		t.Fatal("expected failure")
	}
	after, _ := e.blob.List(ctx, "", 1000)
	if len(after) != len(before) {
		t.Fatalf("objects left behind: %d → %d", len(before), len(after))
	}
	if sg, _ := e.mem.GetSigner(ctx, tenant, a); sg.Status == store.SignerSigned {
		t.Fatal("signer marked signed after rollback")
	}
	e.mem.Fail("UpdateSubmission", nil)
	if _, err := e.svc.Sign(ctx, user("alice"), a, aliceInput()); err != nil {
		t.Fatalf("retry after the outage: %v", err)
	}
}

func TestParallelSignersSerialise(t *testing.T) {
	e := newEnv(t)
	e.setup(t, "alice", "bob")
	d := e.submission(t, store.ModeParallel)
	var wg sync.WaitGroup
	errs := make([]error, 2)
	for i, u := range []string{"alice", "bob"} {
		wg.Add(1)
		go func() {
			defer wg.Done()
			in := Input{PIN: "123456", Values: map[string]string{"salary": "6000"}}
			if u == "alice" {
				in = aliceInput()
			}
			_, errs[i] = e.svc.Sign(ctx, user(u), slot(d, u), in)
		}()
	}
	wg.Wait()
	if errs[0] != nil || errs[1] != nil {
		t.Fatalf("errors: %v", errs)
	}
	sigs, err := verify.Verify(e.finalPDF(t, d.Submission.ID), verify.Options{Pools: map[string]*x509.CertPool{"tenant": e.caPool(t)}})
	if err != nil || len(sigs) != 2 || !sigs[0].Valid() || !sigs[1].Valid() {
		t.Fatalf("both signatures must survive: %v %v", sigs, err)
	}
	versions, _ := e.mem.ListVersions(ctx, tenant, d.Submission.ID)
	if len(versions) != 3 {
		t.Fatalf("versions %d", len(versions))
	}
}

func TestRateLimit(t *testing.T) {
	calls := 0
	e := newEnv(t, func(d *Deps) {
		d.Limited = func(_ context.Context, tn, u string) (bool, error) {
			calls++
			if tn != tenant || u != "alice" {
				t.Errorf("limit key %s/%s", tn, u)
			}
			if calls == 2 {
				return false, errors.New("valkey down") // fail open
			}
			return calls > 2, nil
		}
		d.Limits.MaxFileBytes = 3
	})
	e.setup(t, "alice")
	d := e.submission(t, store.ModeParallel)
	a := slot(d, "alice")
	if _, err := e.svc.Sign(ctx, user("alice"), a, aliceInput()); !errors.Is(err, apperr.PayloadTooLarge) {
		t.Fatalf("file bound: %v", err)
	}
	in := aliceInput()
	delete(in.Uploads, "cv")
	in.PIN = "000000"
	if _, err := e.svc.Sign(ctx, user("alice"), a, in); !errors.Is(err, apperr.PINInvalid) {
		t.Fatalf("limiter error lets the request through: %v", err)
	}
	if _, err := e.svc.Sign(ctx, user("alice"), a, in); !errors.Is(err, apperr.RateLimited) {
		t.Fatalf("rate limited: %v", err)
	}
}

func TestDecline(t *testing.T) {
	e := newEnv(t)
	d := e.submission(t, store.ModeParallel)
	a, b := slot(d, "alice"), slot(d, "bob")
	if _, err := e.svc.Decline(ctx, user("alice"), a, " ", ""); !errors.Is(err, apperr.Validation) {
		t.Fatalf("empty reason: %v", err)
	}
	if _, err := e.svc.Decline(ctx, user("bob"), a, "no", ""); !errors.Is(err, apperr.NotFound) {
		t.Fatalf("someone else's slot: %v", err)
	}
	res, err := e.svc.Decline(ctx, user("alice"), a, "salary too low", "10.0.0.2")
	if err != nil || res.SignerStatus != store.SignerDeclined || res.SubmissionStatus != store.SubmissionCancelled {
		t.Fatalf("decline: %+v %v", res, err)
	}
	sub, _, _ := e.mem.GetSubmission(ctx, tenant, d.Submission.ID)
	if sub.CancelReason != "salary too low" || sub.CancelledAt == nil {
		t.Fatalf("submission %+v", sub)
	}
	if e.mail.count(mail.Declined) != 2 { // bob and the sender, not alice
		t.Fatalf("declined mails %d (%v)", e.mail.count(mail.Declined), e.mail.to)
	}
	if c := e.events.OfType(events.SubmissionCancelled); len(c) != 1 || c[0].Payload.(events.CancelledPayload).ReasonCode != events.ReasonDeclined {
		t.Fatalf("cancelled event %+v", c)
	}
	if _, err := e.svc.Decline(ctx, user("alice"), a, "again", ""); !errors.Is(err, apperr.SignerFinal) {
		t.Fatalf("decline twice: %v", err)
	}
	if _, err := e.svc.Decline(ctx, user("bob"), b, "me too", ""); !errors.Is(err, apperr.SubmissionNotOpen) {
		t.Fatalf("decline a cancelled submission: %v", err)
	}
	if !e.audit.has(audit.SignerDecline, audit.OutcomeOK) || !e.audit.has(audit.SignerDecline, audit.OutcomeRefused) {
		t.Fatal("decline audit")
	}
}

func TestRulesHook(t *testing.T) {
	e := newEnv(t, func(d *Deps) {
		d.Rules = func(fields []store.Field, values map[string]string) (Evaluation, error) {
			ev, _ := plain(fields, values)
			ev.Hidden["photo"] = values["agree"] != "true"
			ev.Required["agree"] = true
			ev.Computed["name"] = "COMPUTED"
			return ev, nil
		}
	})
	e.setup(t, "alice")
	d := e.submission(t, store.ModeParallel)
	a := slot(d, "alice")
	in := aliceInput()
	in.Values["agree"] = "false"
	if _, err := e.svc.Sign(ctx, user("alice"), a, in); !errors.Is(err, apperr.MissingRequired) {
		t.Fatalf("conditionally required: %v", err)
	}
	in.Values["agree"], in.Values["name"] = "true", "tampered"
	if _, err := e.svc.Sign(ctx, user("alice"), a, in); err != nil {
		t.Fatal(err)
	}
	if sg, _ := e.mem.GetSigner(ctx, tenant, a); sg.Values["name"] != "COMPUTED" {
		t.Fatalf("computed value must win: %v", sg.Values)
	}

	failing := newEnv(t, func(d *Deps) {
		d.Rules = func([]store.Field, map[string]string) (Evaluation, error) { return Evaluation{}, apperr.InvalidRule }
	})
	failing.setup(t, "alice")
	fd := failing.submission(t, store.ModeParallel)
	if _, err := failing.svc.Sign(ctx, user("alice"), slot(fd, "alice"), aliceInput()); !errors.Is(err, apperr.InvalidRule) {
		t.Fatalf("rule error: %v", err)
	}
}
