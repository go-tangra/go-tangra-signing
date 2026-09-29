package submissions

import (
	"errors"
	"io"
	"testing"

	"github.com/go-tangra/go-tangra-signing/v4/internal/apperr"
	"github.com/go-tangra/go-tangra-signing/v4/internal/audit"
	"github.com/go-tangra/go-tangra-signing/v4/internal/events"
	"github.com/go-tangra/go-tangra-signing/v4/internal/mail"
	"github.com/go-tangra/go-tangra-signing/v4/internal/store"
)

const hrSVID = "spiffe://example.org/svc/hr"

func (e *env) moduleInput(key string) ModuleInput {
	return ModuleInput{TemplateID: e.tpl.ID, SenderUserID: "sender", Name: "Annual leave",
		Signers: []ModuleSigner{{UserID: "alice", Party: "employee"}, {UserID: "carol", Party: "employer"}},
		Prefill: map[string]string{"name": "Alice"}, SourceRef: "req-1", IdempotencyKey: key}
}

func TestModuleTemplates(t *testing.T) {
	e := newEnv(t)
	_ = e.template(t, tenant, store.TemplateDraft)
	_ = e.template(t, other, store.TemplateActive)
	ts, err := e.svc.ModuleTemplates(ctx, tenant)
	if err != nil || len(ts) != 1 || ts[0].ID != e.tpl.ID || len(ts[0].Parties) != 2 || len(ts[0].Fields) != 4 {
		t.Fatalf("templates: %+v %v", ts, err)
	}
	e.mem.Fail("ListTemplates", errors.New("db"))
	if _, err := e.svc.ModuleTemplates(ctx, tenant); err == nil {
		t.Fatal("store error")
	}
}

func TestModuleCreateAndSend(t *testing.T) {
	e := newEnv(t)
	sub, err := e.svc.ModuleCreateAndSend(ctx, tenant, hrSVID, store.SourceHR, e.moduleInput("req-1:1"))
	if err != nil || sub.Status != store.SubmissionInProgress || sub.Source != store.SourceHR || sub.SourceRef != "req-1" ||
		sub.CreatedBy != "sender" || sub.Mode != store.ModeSequential {
		t.Fatalf("create: %+v %v", sub, err)
	}
	if to := e.mail.to(mail.Invitation); len(to) != 1 || to[0] != "alice@example.org" {
		t.Fatalf("first signer invited: %v", to)
	}
	again, err := e.svc.ModuleCreateAndSend(ctx, tenant, hrSVID, store.SourceHR, e.moduleInput("req-1:1"))
	if err != nil || again.ID != sub.ID || len(e.mail.to(mail.Invitation)) != 1 {
		t.Fatalf("replay: %v", err)
	}
	if !e.audit.has(audit.SubmissionCreate, audit.OutcomeOK) || !e.audit.has(audit.SubmissionSend, audit.OutcomeOK) {
		t.Fatal("audit")
	}
	for name, in := range map[string]ModuleInput{
		"no key":      func() ModuleInput { i := e.moduleInput(""); return i }(),
		"long ref":    func() ModuleInput { i := e.moduleInput("k2"); i.SourceRef = string(make([]byte, 65)); return i }(),
		"no signers":  func() ModuleInput { i := e.moduleInput("k3"); i.Signers = nil; return i }(),
		"bad sender":  func() ModuleInput { i := e.moduleInput("k4"); i.SenderUserID = "stranger"; return i }(),
		"bad signer":  func() ModuleInput { i := e.moduleInput("k5"); i.Signers[1].UserID = "stranger"; return i }(),
		"bad prefill": func() ModuleInput { i := e.moduleInput("k6"); i.Prefill = map[string]string{"sig": "x"}; return i }(),
		"inactive tpl": func() ModuleInput {
			i := e.moduleInput("k7")
			i.TemplateID = e.template(t, tenant, store.TemplateDraft).ID
			return i
		}(),
	} {
		if _, err := e.svc.ModuleCreateAndSend(ctx, tenant, hrSVID, store.SourceHR, in); err == nil {
			t.Errorf("%s accepted", name)
		}
	}
	if _, err := e.svc.ModuleCreateAndSend(ctx, tenant, hrSVID, "", e.moduleInput("k8")); !errors.Is(err, apperr.Validation) {
		t.Fatal("empty source")
	}
	e.dir.Err = errors.New("auth down")
	if _, err := e.svc.ModuleCreateAndSend(ctx, tenant, hrSVID, store.SourceHR, e.moduleInput("k9")); !errors.Is(err, apperr.TemporarilyUnavailable) {
		t.Fatal("contacts down")
	}
	e.dir.Err = nil
	e.mem.Fail("SubmissionByIdempotency", errors.New("db"))
	if _, err := e.svc.ModuleCreateAndSend(ctx, tenant, hrSVID, store.SourceHR, e.moduleInput("k10")); err == nil {
		t.Fatal("lookup error")
	}
	e.mem.Fail("UpdateSubmission", errors.New("db"))
	if _, err := e.svc.ModuleCreateAndSend(ctx, tenant, hrSVID, store.SourceHR, e.moduleInput("k11")); err == nil {
		t.Fatal("send failure")
	}
	e.mem.Fail("", nil)
	if _, _, err := e.mem.SubmissionByIdempotency(ctx, tenant, store.SourceHR, "k11"); err == nil {
		t.Fatal("draft of a failed send is deleted")
	}
}

func TestModuleStateCancelDeleteDocument(t *testing.T) {
	e := newEnv(t)
	sub, _ := e.svc.ModuleCreateAndSend(ctx, tenant, hrSVID, store.SourceHR, e.moduleInput("req-1:1"))
	st, err := e.svc.ModuleState(ctx, tenant, store.SourceHR, sub.ID)
	if err != nil || st.Status != store.SubmissionInProgress || st.CancelReasonCode != "" {
		t.Fatalf("state: %+v %v", st, err)
	}
	// A user's own submission is invisible to the module; so is another tenant.
	own := e.create(t, store.ModeSequential)
	if _, err := e.svc.ModuleState(ctx, tenant, store.SourceHR, own.Submission.ID); !errors.Is(err, apperr.NotFound) {
		t.Fatal("user submission visible")
	}
	if _, err := e.svc.ModuleState(ctx, other, store.SourceHR, sub.ID); !errors.Is(err, apperr.NotFound) {
		t.Fatal("foreign tenant")
	}
	if _, err := e.svc.ModuleCancel(ctx, tenant, hrSVID, store.SourceHR, own.Submission.ID, "hr_cancelled"); !errors.Is(err, apperr.NotFound) {
		t.Fatal("cancel user submission")
	}
	if err := e.svc.ModuleDelete(ctx, tenant, hrSVID, store.SourceHR, own.Submission.ID); !errors.Is(err, apperr.NotFound) {
		t.Fatal("delete user submission")
	}
	if _, _, _, err := e.svc.ModuleDocument(ctx, tenant, store.SourceHR, sub.ID); !errors.Is(err, apperr.SubmissionNotOpen) {
		t.Fatal("document before completion")
	}
	if _, err := e.svc.ModuleCancel(ctx, tenant, hrSVID, store.SourceHR, sub.ID, "bogus"); !errors.Is(err, apperr.Validation) {
		t.Fatal("reason code")
	}
	cst, err := e.svc.ModuleCancel(ctx, tenant, hrSVID, store.SourceHR, sub.ID, "hr_rejected")
	if err != nil || cst.Status != store.SubmissionCancelled || cst.CancelReasonCode != events.ReasonCancelled {
		t.Fatalf("cancel: %+v %v", cst, err)
	}
	if len(e.events.OfType(events.SubmissionCancelled)) != 1 {
		t.Fatal("cancel event")
	}
	if _, err := e.svc.ModuleCancel(ctx, tenant, hrSVID, store.SourceHR, sub.ID, "hr_rejected"); !errors.Is(err, apperr.SubmissionNotOpen) {
		t.Fatal("cancel twice")
	}
	if st, _ := e.svc.ModuleState(ctx, tenant, store.SourceHR, sub.ID); st.CancelReasonCode != events.ReasonCancelled {
		t.Fatal("cancel reason code")
	}
	// Declined by a signer reports "declined".
	sub2, _ := e.svc.ModuleCreateAndSend(ctx, tenant, hrSVID, store.SourceHR, e.moduleInput("req-2:1"))
	s2, signers, _ := e.mem.GetSubmission(ctx, tenant, sub2.ID)
	signers[0].Status = store.SignerDeclined
	_ = e.mem.UpdateSigner(ctx, signers[0])
	s2.Status = store.SubmissionCancelled
	_ = e.mem.UpdateSubmission(ctx, s2)
	if st, _ := e.svc.ModuleState(ctx, tenant, store.SourceHR, sub2.ID); st.CancelReasonCode != events.ReasonDeclined {
		t.Fatalf("declined: %+v", st)
	}
	// Completed: document streams.
	sub3, _ := e.svc.ModuleCreateAndSend(ctx, tenant, hrSVID, store.SourceHR, e.moduleInput("req-3:1"))
	s3, _, _ := e.mem.GetSubmission(ctx, tenant, sub3.ID)
	zero := 0
	s3.Status, s3.FinalVersion = store.SubmissionCompleted, &zero
	_ = e.mem.UpdateSubmission(ctx, s3)
	rc, v, _, err := e.svc.ModuleDocument(ctx, tenant, store.SourceHR, sub3.ID)
	if err != nil || v.Version != 0 {
		t.Fatal(err)
	}
	b, _ := io.ReadAll(rc)
	_ = rc.Close()
	if len(b) == 0 {
		t.Fatal("empty document")
	}
	if st, _ := e.svc.ModuleState(ctx, tenant, store.SourceHR, sub3.ID); st.FinalVersion != 0 || st.Status != store.SubmissionCompleted {
		t.Fatal("completed state")
	}
	// Delete removes rows and objects.
	if err := e.svc.ModuleDelete(ctx, tenant, hrSVID, store.SourceHR, sub3.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc.ModuleState(ctx, tenant, store.SourceHR, sub3.ID); !errors.Is(err, apperr.NotFound) {
		t.Fatal("deleted")
	}
	e.mem.Fail("DeleteSubmission", errors.New("db"))
	if err := e.svc.ModuleDelete(ctx, tenant, hrSVID, store.SourceHR, sub2.ID); err == nil {
		t.Fatal("delete failure")
	}
	e.mem.Fail("GetVersion", errors.New("db"))
	sub4, _ := e.svc.ModuleCreateAndSend(ctx, tenant, hrSVID, store.SourceHR, e.moduleInput("req-4:1"))
	s4, _, _ := e.mem.GetSubmission(ctx, tenant, sub4.ID)
	s4.Status, s4.FinalVersion = store.SubmissionCompleted, &zero
	_ = e.mem.UpdateSubmission(ctx, s4)
	if _, _, _, err := e.svc.ModuleDocument(ctx, tenant, store.SourceHR, sub4.ID); err == nil {
		t.Fatal("version error")
	}
	e.mem.Fail("UpdateSubmission", errors.New("db"))
	sub5, _ := e.svc.ModuleCreateAndSend(ctx, tenant, hrSVID, store.SourceHR, e.moduleInput("req-5:1"))
	_ = sub5
	e.mem.Fail("", nil)
}
