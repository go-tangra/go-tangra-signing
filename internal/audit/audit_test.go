package audit

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/go-tangra/go-tangra-signing/v4/internal/store"
)

const tn = "0190f7c2-6a3e-7c1a-9b2e-2f6f9d1b4c55"

type memStore struct {
	mu   sync.Mutex
	rows []store.AuditRow
	err  error
}

func (m *memStore) AppendAudit(_ context.Context, r store.AuditRow) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.err != nil {
		return m.err
	}
	m.rows = append(m.rows, r)
	return nil
}

func (m *memStore) all() []store.AuditRow {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]store.AuditRow(nil), m.rows...)
}

func ok(t EventType) Event {
	return Event{TenantID: tn, EventType: t, ActorKind: ActorUser, ActorID: "u1", SubjectKind: SubjectTemplate, SubjectID: "t1", Outcome: OutcomeOK}
}

func TestVocabulary(t *testing.T) {
	for _, want := range []string{"folder.create", "template.fields", "submission.send", "submission.signer_replace",
		"signer.sign", "signer.decline", "qes.prepare", "certificate.setup", "certificate.lock", "ca.crl",
		"document.sign", "document.verify", "backup.export", "task.expire", "task.remind", "access.refused"} {
		if !Known(want) {
			t.Errorf("vocabulary lacks %s", want)
		}
	}
	if len(Vocabulary) != 33 || Known("task.explode") {
		t.Fatalf("vocabulary size %d / unknown event accepted", len(Vocabulary))
	}
}

func TestValidate(t *testing.T) {
	if err := Validate(ok(TemplateCreate)); err != nil {
		t.Fatal(err)
	}
	bad := []func(*Event){
		func(e *Event) { e.EventType = "nope" },
		func(e *Event) { e.TenantID = "" },
		func(e *Event) { e.ActorKind = "robot" },
		func(e *Event) { e.SubjectKind = "planet" },
		func(e *Event) { e.Outcome = "maybe" },
	}
	for i, mut := range bad {
		e := ok(TemplateCreate)
		mut(&e)
		if Validate(e) == nil {
			t.Errorf("case %d accepted", i)
		}
	}
	for _, k := range []string{ActorService, ActorSystem} {
		e := ok(TaskExpire)
		e.ActorKind, e.SubjectKind, e.TenantID = k, SubjectSystem, NilTenant
		if err := Validate(e); err != nil {
			t.Errorf("actor %s: %v", k, err)
		}
	}
	for _, sk := range []string{SubjectFolder, SubjectSubmission, SubjectSigner, SubjectCertificate, SubjectDocument, SubjectBackup, SubjectSystem} {
		e := ok(SignerSign)
		e.SubjectKind = sk
		if err := Validate(e); err != nil {
			t.Errorf("subject %s: %v", sk, err)
		}
	}
	for _, o := range []string{OutcomeRefused, OutcomeError} {
		e := ok(AccessRefused)
		e.Outcome = o
		if err := Validate(e); err != nil {
			t.Errorf("outcome %s: %v", o, err)
		}
	}
}

func TestRedaction(t *testing.T) {
	d := Redact(map[string]any{
		"values": map[string]any{"Salary": "5000"}, "field_value": "x", "pin": "123456", "PIN_attempts": 1,
		"private_key": "k", "signature_image": "png", "content": "pdf", "pdf": "x", "decline_reason": "r",
		"body": "b", "email": "a@b", "recipients": []any{"a@b"}, "client_secret": "s", "token": "t",
		"password": "p", "credential": "c",
		"method": "local_certificate", "version": 2, "long": strings.Repeat("x", 400),
		"nested": map[string]any{"token": "t", "keep": "v", "list": []any{map[string]any{"password": "p", "n": 1}, "s"}},
	})
	for _, k := range []string{"values", "field_value", "pin", "PIN_attempts", "private_key", "signature_image", "content",
		"pdf", "decline_reason", "body", "email", "recipients", "client_secret", "token", "password", "credential"} {
		if _, found := d[k]; found {
			t.Errorf("%s leaked", k)
		}
	}
	if d["method"] != "local_certificate" || d["version"] != 2 || len(d["long"].(string)) != 256 {
		t.Fatalf("kept values: %+v", d)
	}
	nested := d["nested"].(map[string]any)
	if _, found := nested["token"]; found || nested["keep"] != "v" {
		t.Fatalf("nested: %+v", nested)
	}
	item := nested["list"].([]any)[0].(map[string]any)
	if _, found := item["password"]; found || item["n"] != 1 {
		t.Fatalf("list item: %+v", item)
	}
}

func TestWriterRecordsAndFlushes(t *testing.T) {
	st := &memStore{}
	w := NewWriter(st, nil)
	e := ok(TemplateUpdate)
	e.Reason = strings.Repeat("r", 300)
	e.Details = map[string]any{"fields": []any{"name"}, "to": "active", "values": "never"}
	if err := w.Record(context.Background(), e); err != nil {
		t.Fatal(err)
	}
	if err := w.Record(context.Background(), Event{EventType: "bogus"}); err == nil {
		t.Fatal("invalid event queued")
	}
	w.Flush(context.Background())
	rows := st.all()
	if len(rows) != 1 {
		t.Fatalf("rows = %d", len(rows))
	}
	r := rows[0]
	if r.Action != "template.update" || r.ID == "" || r.At.IsZero() || len(r.Reason) != 256 || r.Detail["to"] != "active" {
		t.Fatalf("row = %+v", r)
	}
	if _, found := r.Detail["values"]; found {
		t.Fatal("values in audit detail")
	}
	w.Close()
	w.Close() // idempotent
	if err := w.Record(context.Background(), ok(TemplateCreate)); err == nil {
		t.Fatal("record after close")
	}
	w.Flush(context.Background()) // no-op after close
	if w.Dropped() != 1 {
		t.Fatalf("dropped = %d", w.Dropped())
	}
}

func TestWriterErrorsAndBackpressure(t *testing.T) {
	st := &memStore{err: errors.New("db down")}
	var mu sync.Mutex
	var errs []error
	w := newWriter(st, func(err error) { mu.Lock(); errs = append(errs, err); mu.Unlock() }, 1)
	// not started: the second record overflows the queue of 1
	_ = w.Record(context.Background(), ok(TemplateCreate))
	_ = w.Record(context.Background(), ok(TemplateCreate))
	if w.Dropped() != 1 {
		t.Fatalf("dropped = %d", w.Dropped())
	}
	w.tick = time.Millisecond
	w.start()
	w.Flush(context.Background())
	time.Sleep(5 * time.Millisecond)
	w.Close()
	mu.Lock()
	defer mu.Unlock()
	if len(errs) < 2 {
		t.Fatalf("errors = %v", errs)
	}
	nw := newWriter(st, nil, 1)
	nw.onError(errors.New("ignored")) // default handler is a no-op
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	nw.Flush(ctx) // not started + cancelled: returns
}

type recorder struct{ got []Event }

func (r *recorder) Record(_ context.Context, e Event) error { r.got = append(r.got, e); return nil }

func TestEmitAndActorOf(t *testing.T) {
	Emit(context.Background(), nil, ok(TemplateCreate)) // nil recorder: no-op
	r := &recorder{}
	Emit(context.Background(), r, ok(TemplateCreate))
	if len(r.got) != 1 {
		t.Fatal("emit")
	}
	for in, want := range map[string]string{"user": ActorUser, "service": ActorService, "system": ActorSystem, "": ActorUser} {
		if ActorOf(in) != want {
			t.Errorf("ActorOf(%q) = %q", in, ActorOf(in))
		}
	}
}
