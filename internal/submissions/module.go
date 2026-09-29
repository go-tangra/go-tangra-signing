package submissions

import (
	"context"
	"errors"
	"io"
	"strings"

	"github.com/go-tangra/go-tangra-signing/v4/internal/apperr"
	"github.com/go-tangra/go-tangra-signing/v4/internal/audit"
	"github.com/go-tangra/go-tangra-signing/v4/internal/authz"
	"github.com/go-tangra/go-tangra-signing/v4/internal/blob"
	"github.com/go-tangra/go-tangra-signing/v4/internal/events"
	"github.com/go-tangra/go-tangra-signing/v4/internal/mail"
	"github.com/go-tangra/go-tangra-signing/v4/internal/repo"
	"github.com/go-tangra/go-tangra-signing/v4/internal/store"
)

// Module API (feature 028): another module (the hr module) creates, follows,
// cancels and deletes submissions of a tenant over the mesh. The caller is a
// service subject; it only sees submissions whose source is its own service
// name, and its signers and sender must be active members of the tenant.

// ModuleTemplate is an active template as the module API shows it.
type ModuleTemplate struct {
	ID, Name string
	Parties  []store.Party
	Fields   []store.Field
}

// ModuleSigner is one signer of a module submission, in signing order.
type ModuleSigner struct{ UserID, Party string }

// ModuleInput creates and sends a submission for a module.
type ModuleInput struct {
	TemplateID     string
	SenderUserID   string
	Name           string
	Signers        []ModuleSigner
	Prefill        map[string]string
	SourceRef      string
	IdempotencyKey string
}

// ModuleState is a submission's state for a module.
type ModuleState struct {
	ID               string
	Status           string
	CancelReasonCode string // events.ReasonCancelled | events.ReasonDeclined
	FinalVersion     int
}

// Module sources and their callers: the service name of the caller is the
// submission source.
const maxModuleSigners = 10

func moduleSubject(tenant, spiffeID string) authz.Subjects {
	subj := authz.Service(spiffeID)
	subj.TenantID = tenant
	return subj
}

// ModuleTemplates lists the tenant's active templates (no PDF, no values).
func (s *Service) ModuleTemplates(ctx context.Context, tenant string) ([]ModuleTemplate, error) {
	var out []ModuleTemplate
	for page := 1; page <= 10; page++ {
		list, total, err := s.d.Store.ListTemplates(ctx, tenant, repo.TemplateFilter{Status: store.TemplateActive, Page: page, PageSize: 100})
		if err != nil {
			return nil, err
		}
		for _, t := range list {
			out = append(out, ModuleTemplate{ID: t.ID, Name: t.Name, Parties: t.Parties, Fields: t.Fields})
		}
		if page*100 >= total {
			break
		}
	}
	return out, nil
}

// ModuleCreateAndSend creates a sequential submission for source and sends it
// (idempotent by in.IdempotencyKey). The sender must be an active member.
func (s *Service) ModuleCreateAndSend(ctx context.Context, tenant, spiffeID, source string, in ModuleInput) (sub store.Submission, err error) {
	subj := moduleSubject(tenant, spiffeID)
	defer func() {
		outcome, reason := audit.OutcomeOK, ""
		if err != nil {
			outcome, reason = audit.OutcomeRefused, reasonOf(err)
		}
		s.record(ctx, subj, audit.SubmissionCreate, audit.SubjectSubmission, sub.ID, outcome, reason,
			map[string]any{"source": source, "template_id": in.TemplateID, "signers": len(in.Signers), "sender": in.SenderUserID})
	}()
	key := strings.TrimSpace(in.IdempotencyKey)
	if key == "" || len(key) > 128 || len(in.SourceRef) > 64 || source == "" {
		return sub, apperr.Validation.WithField("idempotency_key")
	}
	if prev, _, err := s.d.Store.SubmissionByIdempotency(ctx, tenant, source, key); err == nil {
		return prev, nil // replay
	} else if !errors.Is(err, repo.ErrNotFound) {
		return sub, err
	}
	if len(in.Signers) == 0 || len(in.Signers) > maxModuleSigners {
		return sub, apperr.InvalidSigner.WithField("signers")
	}
	found, err := s.d.Contacts.Contacts(ctx, tenant, []string{in.SenderUserID})
	if err != nil {
		return sub, apperr.TemporarilyUnavailable
	}
	if _, ok := found[in.SenderUserID]; !ok || in.SenderUserID == "" {
		return sub, apperr.InvalidSigner.WithField("sender_user_id")
	}
	signers := make([]SignerInput, len(in.Signers))
	for i, sg := range in.Signers {
		pos := i
		signers[i] = SignerInput{UserID: sg.UserID, Party: sg.Party, Position: &pos}
	}
	d, err := s.createFor(ctx, tenant, in.SenderUserID, CreateInput{TemplateID: in.TemplateID, Name: in.Name, Mode: store.ModeSequential,
		Signers: signers, Prefill: in.Prefill}, origin{Source: source, Ref: in.SourceRef, Key: key})
	if errors.Is(err, repo.ErrConflict) {
		// A concurrent replay won the race: return its submission.
		prev, _, err := s.d.Store.SubmissionByIdempotency(ctx, tenant, source, key)
		return prev, err
	}
	if err != nil {
		return sub, err
	}
	if err := s.moduleSend(ctx, subj, in.SenderUserID, d.Submission.ID); err != nil {
		_ = s.moduleDelete(ctx, tenant, d.Submission.ID)
		return sub, err
	}
	sub, _, err = s.d.Store.GetSubmission(ctx, tenant, d.Submission.ID)
	return sub, err
}

func (s *Service) moduleSend(ctx context.Context, subj authz.Subjects, sender, id string) error {
	var sub store.Submission
	var invited []store.Signer
	now := s.d.Now()
	err := s.d.Store.Tx(ctx, subj.TenantID, func(tx repo.Store) error {
		var all []store.Signer
		var err error
		if sub, all, err = tx.LockSubmission(ctx, subj.TenantID, id); err != nil {
			return notFound(err)
		}
		sub.Status, sub.SentAt, sub.UpdatedAt = store.SubmissionInProgress, &now, now
		if err := tx.UpdateSubmission(ctx, sub); err != nil {
			return err
		}
		if invited, err = MarkInvited(ctx, tx, sub, Due(sub, all), now); err != nil {
			return err
		}
		return Event(ctx, tx, subj.TenantID, sub.ID, nil, sender, "submission.sent", map[string]any{"source": sub.Source}, now)
	})
	if err != nil {
		return err
	}
	s.record(ctx, subj, audit.SubmissionSend, audit.SubjectSubmission, id, audit.OutcomeOK, "", map[string]any{"invited": len(invited)})
	s.Invite(ctx, sub, invited, mail.Invitation)
	return nil
}

// owned returns a submission of tenant created by source (404 otherwise).
func owned(sub store.Submission, err error, source string) (store.Submission, error) {
	if err != nil {
		return sub, notFound(err)
	}
	if sub.Source == "" || sub.Source != source {
		return store.Submission{}, apperr.NotFound
	}
	return sub, nil
}

// ModuleState returns the state of a submission created by source.
func (s *Service) ModuleState(ctx context.Context, tenant, source, id string) (ModuleState, error) {
	sub, signers, err := s.d.Store.GetSubmission(ctx, tenant, id)
	if sub, err = owned(sub, err, source); err != nil {
		return ModuleState{}, err
	}
	st := ModuleState{ID: sub.ID, Status: sub.Status}
	if sub.FinalVersion != nil {
		st.FinalVersion = *sub.FinalVersion
	}
	if sub.Status == store.SubmissionCancelled {
		st.CancelReasonCode = events.ReasonCancelled
		for _, sg := range signers {
			if sg.Status == store.SignerDeclined {
				st.CancelReasonCode = events.ReasonDeclined
			}
		}
	}
	return st, nil
}

// Module cancel reason codes and their wording in the cancellation e-mail.
var moduleCancelReasons = map[string]string{
	"hr_cancelled": "The leave request was cancelled.",
	"hr_rejected":  "The leave request was rejected.",
	"member_left":  "The employee left the organisation.",
}

// ModuleCancel cancels an unfinished submission created by source.
func (s *Service) ModuleCancel(ctx context.Context, tenant, spiffeID, source, id, reasonCode string) (st ModuleState, err error) {
	subj := moduleSubject(tenant, spiffeID)
	reason, ok := moduleCancelReasons[reasonCode]
	if !ok {
		return st, apperr.Validation.WithField("reason_code")
	}
	var sub store.Submission
	var signers []store.Signer
	now := s.d.Now()
	err = s.d.Store.Tx(ctx, tenant, func(tx repo.Store) error {
		var err error
		sub, signers, err = tx.LockSubmission(ctx, tenant, id)
		if sub, err = owned(sub, err, source); err != nil {
			return err
		}
		if sub.Status != store.SubmissionDraft && sub.Status != store.SubmissionInProgress {
			return apperr.SubmissionNotOpen
		}
		sub.Status, sub.CancelledAt, sub.CancelReason, sub.UpdatedAt = store.SubmissionCancelled, &now, reason, now
		if err := tx.UpdateSubmission(ctx, sub); err != nil {
			return err
		}
		return Event(ctx, tx, tenant, id, nil, sub.CreatedBy, "submission.cancelled", map[string]any{"source": source, "reason_code": reasonCode}, now)
	})
	if err != nil {
		s.record(ctx, subj, audit.SubmissionCancel, audit.SubjectSubmission, id, audit.OutcomeRefused, reasonOf(err), nil)
		return st, err
	}
	s.record(ctx, subj, audit.SubmissionCancel, audit.SubjectSubmission, id, audit.OutcomeOK, "", map[string]any{"source": source})
	s.Ended(ctx, sub, signers, mail.Cancelled, map[string]string{"document": sub.Name, "reason": reason}, "")
	s.d.Events.Cancelled(ctx, tenant, events.CancelledPayload{SubmissionID: sub.ID, TemplateID: sub.TemplateID, ReasonCode: events.ReasonCancelled})
	return ModuleState{ID: sub.ID, Status: sub.Status, CancelReasonCode: events.ReasonCancelled}, nil
}

// ModuleDelete deletes a submission created by source with its documents.
func (s *Service) ModuleDelete(ctx context.Context, tenant, spiffeID, source, id string) (err error) {
	subj := moduleSubject(tenant, spiffeID)
	sub, _, gerr := s.d.Store.GetSubmission(ctx, tenant, id)
	if _, err = owned(sub, gerr, source); err != nil {
		s.record(ctx, subj, audit.SubmissionDelete, audit.SubjectSubmission, id, audit.OutcomeRefused, reasonOf(err), nil)
		return err
	}
	if err = s.moduleDelete(ctx, tenant, id); err != nil {
		s.record(ctx, subj, audit.SubmissionDelete, audit.SubjectSubmission, id, audit.OutcomeRefused, reasonOf(err), nil)
		return err
	}
	s.record(ctx, subj, audit.SubmissionDelete, audit.SubjectSubmission, id, audit.OutcomeOK, "", map[string]any{"source": source})
	return nil
}

func (s *Service) moduleDelete(ctx context.Context, tenant, id string) error {
	var sub store.Submission
	var signers []store.Signer
	var versions []store.DocumentVersion
	err := s.d.Store.Tx(ctx, tenant, func(tx repo.Store) error {
		var err error
		if sub, signers, err = tx.LockSubmission(ctx, tenant, id); err != nil {
			return notFound(err)
		}
		if versions, err = tx.ListVersions(ctx, tenant, id); err != nil {
			return err
		}
		return tx.DeleteSubmission(ctx, tenant, id)
	})
	if err != nil {
		return err
	}
	for _, v := range versions {
		_ = s.d.Blob.Delete(ctx, v.ObjectKey)
	}
	if keys, err := s.d.Blob.List(ctx, blob.SubmissionPrefix(tenant, id), 10000); err == nil {
		for _, k := range keys {
			_ = s.d.Blob.Delete(ctx, k)
		}
	}
	if sub.Status == store.SubmissionInProgress {
		for _, sg := range signers {
			s.d.Events.InboxChanged(ctx, tenant, sg.UserID, events.InboxPayload{SignerID: sg.ID, SubmissionID: sub.ID, State: "closed"})
		}
	}
	return nil
}

// ModuleDocument opens the final signed PDF of a completed submission
// created by source.
func (s *Service) ModuleDocument(ctx context.Context, tenant, source, id string) (io.ReadCloser, store.DocumentVersion, store.Submission, error) {
	sub, _, err := s.d.Store.GetSubmission(ctx, tenant, id)
	if sub, err = owned(sub, err, source); err != nil {
		return nil, store.DocumentVersion{}, sub, err
	}
	if sub.Status != store.SubmissionCompleted || sub.FinalVersion == nil {
		return nil, store.DocumentVersion{}, sub, apperr.SubmissionNotOpen
	}
	v, err := s.d.Store.GetVersion(ctx, tenant, sub.ID, *sub.FinalVersion)
	if err != nil {
		return nil, store.DocumentVersion{}, sub, notFound(err)
	}
	if !blob.InTenant(v.ObjectKey, tenant) {
		return nil, store.DocumentVersion{}, sub, apperr.NotFound
	}
	rc, err := s.d.Blob.Get(ctx, v.ObjectKey)
	if err != nil {
		return nil, store.DocumentVersion{}, sub, err
	}
	return rc, v, sub, nil
}
