// Package submissions sends documents for signature (US2) and lets their
// sender control them (US5): create from an active template with signers
// chosen among the tenant's active members (their names and e-mail
// addresses come from auth Profiles.Contacts), freeze the template's fields
// and PDF, send (sequential: the first position; parallel: everyone), the
// progress view, the caller's "To sign" inbox, document downloads, and
// cancel, resend, replace-signer and delete.
//
// Access (FR-015, FR-017): the sender or submissions:manage controls a
// submission; the sender, its signers and signing:read may view it; a
// foreign or invisible id is "not found". Mail goes out after the database
// work has committed; a failure is recorded on the signer (mail_error) and
// in the history and never fails the action.
package submissions

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"slices"
	"strings"
	"time"

	"github.com/go-tangra/go-tangra-signing/v4/internal/apperr"
	"github.com/go-tangra/go-tangra-signing/v4/internal/audit"
	"github.com/go-tangra/go-tangra-signing/v4/internal/authz"
	"github.com/go-tangra/go-tangra-signing/v4/internal/blob"
	"github.com/go-tangra/go-tangra-signing/v4/internal/contacts"
	"github.com/go-tangra/go-tangra-signing/v4/internal/events"
	"github.com/go-tangra/go-tangra-signing/v4/internal/fieldvalues"
	"github.com/go-tangra/go-tangra-signing/v4/internal/mail"
	"github.com/go-tangra/go-tangra-signing/v4/internal/repo"
	"github.com/go-tangra/go-tangra-signing/v4/internal/store"
	"github.com/go-tangra/go-tangra-signing/v4/internal/templates"
)

// Limits bound submissions.
type Limits struct {
	MaxSigners    int // default 50
	MaxExpiryDays int // default 365
	MaxPDFBytes   int64
}

// Deps wire the service.
type Deps struct {
	Store    repo.Store
	Blob     blob.Store
	Audit    audit.Recorder
	Checker  authz.Checker
	Contacts contacts.Directory
	Mail     mail.Mailer
	Events   events.Emitter
	Now      func() time.Time
	Limits   Limits
	Values   fieldvalues.Box // seals prefills (SC-005)
}

// Service manages submissions.
type Service struct{ d Deps }

// New builds the service.
func New(d Deps) *Service {
	if d.Now == nil {
		d.Now = func() time.Time { return time.Now().UTC() }
	}
	if d.Limits.MaxSigners <= 0 {
		d.Limits.MaxSigners = 50
	}
	if d.Limits.MaxExpiryDays <= 0 {
		d.Limits.MaxExpiryDays = 365
	}
	if d.Limits.MaxPDFBytes <= 0 {
		d.Limits.MaxPDFBytes = 50 << 20
	}
	return &Service{d: d}
}

// Detail is a submission with its signers and the caller's control right.
type Detail struct {
	Submission store.Submission
	Signers    []store.Signer
	CanControl bool
}

func notFound(err error) error {
	if errors.Is(err, repo.ErrNotFound) {
		return apperr.NotFound
	}
	return err
}

func reasonOf(err error) string {
	if e, ok := apperr.As(err); ok {
		return e.Reason
	}
	return "error"
}

func (s *Service) record(ctx context.Context, subj authz.Subjects, t audit.EventType, kind, id, outcome, reason string, detail map[string]any) {
	audit.Emit(ctx, s.d.Audit, audit.Event{TenantID: subj.TenantID, EventType: t, ActorKind: audit.ActorOf(subj.ActorKind),
		ActorID: subj.ActorID(), SubjectKind: kind, SubjectID: id, Outcome: outcome, Reason: reason, Details: detail})
}

// Event adds a history entry (no field values in meta).
func Event(ctx context.Context, st repo.Store, tenant, submissionID string, signerID *string, actor, typ string, meta map[string]any, at time.Time) error {
	var a *string
	if actor != "" {
		a = &actor
	}
	sid := submissionID
	return st.AddEvent(ctx, store.Event{ID: store.NewID(), TenantID: tenant, SubmissionID: &sid, SignerID: signerID,
		ActorUserID: a, Type: typ, Meta: meta, At: at})
}

func userIDs(signers []store.Signer) []string {
	out := make([]string, 0, len(signers))
	for _, sg := range signers {
		out = append(out, sg.UserID)
	}
	return out
}

// ---------------------------------------------------------------- create

// SignerInput is one signer of a new submission.
type SignerInput struct {
	UserID   string
	Party    string
	Position *int
}

// CreateInput creates a submission.
type CreateInput struct {
	TemplateID string
	Name       string
	Mode       string
	Signers    []SignerInput
	Prefill    map[string]string
	ExpiresAt  *time.Time
	Reminder   *store.Reminder
}

// Create freezes the template into a draft submission.
func (s *Service) Create(ctx context.Context, subj authz.Subjects, in CreateInput) (Detail, error) {
	d, err := s.create(ctx, subj, in)
	if err != nil {
		s.record(ctx, subj, audit.SubmissionCreate, audit.SubjectSubmission, "", audit.OutcomeRefused, reasonOf(err), nil)
		return Detail{}, err
	}
	s.record(ctx, subj, audit.SubmissionCreate, audit.SubjectSubmission, d.Submission.ID, audit.OutcomeOK, "",
		map[string]any{"template_id": in.TemplateID, "signers": len(d.Signers), "mode": in.Mode})
	return d, nil
}

func (s *Service) create(ctx context.Context, subj authz.Subjects, in CreateInput) (Detail, error) {
	if err := authz.Require(ctx, s.d.Checker, subj, authz.SubmissionsCreate); err != nil {
		return Detail{}, apperr.Forbidden
	}
	if in.Mode != store.ModeSequential && in.Mode != store.ModeParallel {
		return Detail{}, apperr.Validation.WithField("mode")
	}
	tpl, err := s.d.Store.GetTemplate(ctx, subj.TenantID, in.TemplateID)
	if err != nil {
		return Detail{}, notFound(err)
	}
	if tpl.Status != store.TemplateActive {
		return Detail{}, apperr.TemplateNotActive
	}
	name := strings.TrimSpace(in.Name)
	if name == "" {
		name = tpl.Name
	}
	if len([]rune(name)) > 200 || strings.ContainsAny(name, "\r\n") {
		return Detail{}, apperr.Validation.WithField("name")
	}
	now := s.d.Now()
	fields, err := prefill(tpl.Fields, in.Prefill)
	if err != nil {
		return Detail{}, err
	}
	expires, reminder, err := s.schedule(tpl, in, now)
	if err != nil {
		return Detail{}, err
	}
	id := store.NewID()
	for i, f := range fields {
		if fields[i].Prefill, err = s.d.Values.SealString(f.Prefill, fieldvalues.PrefillAD(id, f.ID)); err != nil {
			return Detail{}, err
		}
	}
	sub := store.Submission{
		ID: id, TenantID: subj.TenantID, TemplateID: tpl.ID, Name: name, PDFSHA256: tpl.PDFSHA256,
		Fields: fields, Parties: tpl.Parties, Mode: in.Mode, Status: store.SubmissionDraft, ExpiresAt: expires,
		Reminder: reminder, CreatedAt: now, CreatedBy: subj.UserID, UpdatedAt: now,
	}
	signers, err := s.signers(ctx, subj.TenantID, sub, tpl.Parties, in.Signers)
	if err != nil {
		return Detail{}, err
	}
	// Version 0 is a copy of the template PDF, so the submission never
	// depends on the template object again (FR-008).
	v0, err := s.copyPDF(ctx, tpl, sub, now)
	if err != nil {
		return Detail{}, err
	}
	sub.PDFKey = v0.ObjectKey
	err = s.d.Store.Tx(ctx, subj.TenantID, func(tx repo.Store) error {
		if err := tx.CreateSubmission(ctx, sub, signers, v0); err != nil {
			return err
		}
		return Event(ctx, tx, subj.TenantID, sub.ID, nil, subj.UserID, "submission.created", map[string]any{"signers": len(signers)}, now)
	})
	if err != nil {
		_ = s.d.Blob.Delete(ctx, v0.ObjectKey)
		return Detail{}, err
	}
	return Detail{Submission: sub, Signers: signers, CanControl: true}, nil
}

// prefill validates prefill values and returns the frozen fields carrying them.
func prefill(tplFields []store.Field, values map[string]string) ([]store.Field, error) {
	fields := slices.Clone(tplFields)
	byID := map[string]int{}
	for i, f := range fields {
		fields[i].Prefill = ""
		byID[f.ID] = i
	}
	for id, v := range values {
		i, ok := byID[id]
		if !ok || !TextValued(fields[i].Type) || !ValidValue(fields[i], v) {
			return nil, apperr.InvalidPrefill.WithField(id)
		}
		fields[i].Prefill = v
	}
	return fields, nil
}

func (s *Service) schedule(tpl store.Template, in CreateInput, now time.Time) (*time.Time, *store.Reminder, error) {
	expires := in.ExpiresAt
	if expires == nil && tpl.DefaultExpiryDays != nil && *tpl.DefaultExpiryDays > 0 {
		t := now.AddDate(0, 0, *tpl.DefaultExpiryDays)
		expires = &t
	}
	if expires != nil {
		t := expires.UTC()
		if !t.After(now) || t.After(now.AddDate(0, 0, s.d.Limits.MaxExpiryDays)) {
			return nil, nil, apperr.Validation.WithField("expires_at")
		}
		expires = &t
	}
	reminder := in.Reminder
	if reminder == nil && tpl.DefaultReminder != nil {
		r := *tpl.DefaultReminder
		reminder = &r
	}
	if reminder != nil && !templates.ValidReminder(*reminder) {
		return nil, nil, apperr.Validation.WithField("reminder")
	}
	return expires, reminder, nil
}

// signers checks one signer per party, all active members with an e-mail.
func (s *Service) signers(ctx context.Context, tenant string, sub store.Submission, parties []store.Party, in []SignerInput) ([]store.Signer, error) {
	if len(in) == 0 || len(in) > s.d.Limits.MaxSigners || len(in) != len(parties) {
		return nil, apperr.InvalidSigner.WithField("signers")
	}
	known := map[string]bool{}
	for _, p := range parties {
		known[p.Key] = true
	}
	seenParty, seenUser, ids := map[string]bool{}, map[string]bool{}, make([]string, 0, len(in))
	for _, sg := range in {
		if !known[sg.Party] || seenParty[sg.Party] || sg.UserID == "" || seenUser[sg.UserID] ||
			(sg.Position != nil && (*sg.Position < 0 || *sg.Position > 1000)) {
			return nil, apperr.InvalidSigner.WithField("signers")
		}
		seenParty[sg.Party], seenUser[sg.UserID] = true, true
		ids = append(ids, sg.UserID)
	}
	found, err := s.d.Contacts.Contacts(ctx, tenant, ids)
	if err != nil {
		return nil, apperr.TemporarilyUnavailable
	}
	out := make([]store.Signer, 0, len(in))
	for i, sg := range in {
		c, ok := found[sg.UserID]
		if !ok {
			return nil, apperr.InvalidSigner.WithField("signers").WithDetail(map[string]any{"user_id": sg.UserID})
		}
		pos := i
		if sg.Position != nil {
			pos = *sg.Position
		}
		if sub.Mode == store.ModeParallel {
			pos = 0
		}
		name := c.DisplayName
		if name == "" {
			name = c.Email
		}
		out = append(out, store.Signer{ID: store.NewID(), TenantID: tenant, SubmissionID: sub.ID, UserID: sg.UserID,
			Name: name, Email: c.Email, Party: sg.Party, Position: pos, Status: store.SignerPending})
	}
	return out, nil
}

func (s *Service) copyPDF(ctx context.Context, tpl store.Template, sub store.Submission, now time.Time) (store.DocumentVersion, error) {
	if !blob.InTenant(tpl.PDFKey, sub.TenantID) {
		return store.DocumentVersion{}, apperr.NotFound
	}
	rc, err := s.d.Blob.Get(ctx, tpl.PDFKey)
	if err != nil {
		return store.DocumentVersion{}, err
	}
	data, err := io.ReadAll(io.LimitReader(rc, s.d.Limits.MaxPDFBytes+1))
	_ = rc.Close()
	if err != nil {
		return store.DocumentVersion{}, err
	}
	if int64(len(data)) > s.d.Limits.MaxPDFBytes {
		return store.DocumentVersion{}, apperr.PayloadTooLarge
	}
	key := blob.DocumentVersion(sub.TenantID, sub.ID, 0)
	sum, err := s.d.Blob.Put(ctx, key, bytes.NewReader(data), int64(len(data)), "application/pdf")
	if err != nil {
		return store.DocumentVersion{}, err
	}
	return store.DocumentVersion{SubmissionID: sub.ID, Version: 0, TenantID: sub.TenantID, ObjectKey: key,
		SHA256: sum, Size: int64(len(data)), CreatedAt: now}, nil
}

// ---------------------------------------------------------------- turns

// Due returns the signers to invite now: in parallel mode every pending
// signer; in sequential mode the pending signers of the lowest position not
// yet signed, once every lower position has signed.
func Due(sub store.Submission, signers []store.Signer) []store.Signer {
	var out []store.Signer
	if sub.Mode == store.ModeParallel {
		for _, sg := range signers {
			if sg.Status == store.SignerPending {
				out = append(out, sg)
			}
		}
		return out
	}
	next := -1
	for _, sg := range signers {
		if sg.Status != store.SignerSigned && (next < 0 || sg.Position < next) {
			next = sg.Position
		}
	}
	for _, sg := range signers {
		if sg.Position == next && sg.Status == store.SignerPending {
			out = append(out, sg)
		}
	}
	return out
}

// Turn reports whether signer may sign now (FR-011): parallel always;
// sequential once every signer at a lower position has signed.
func Turn(sub store.Submission, signers []store.Signer, signer store.Signer) bool {
	if sub.Mode == store.ModeParallel {
		return true
	}
	for _, sg := range signers {
		if sg.Position < signer.Position && sg.Status != store.SignerSigned {
			return false
		}
	}
	return true
}

// Complete reports whether every signer has signed.
func Complete(signers []store.Signer) bool {
	for _, sg := range signers {
		if sg.Status != store.SignerSigned {
			return false
		}
	}
	return len(signers) > 0
}

// MarkInvited sets the invited state (and the first reminder time) on the
// due signers inside a transaction and returns them.
func MarkInvited(ctx context.Context, tx repo.Store, sub store.Submission, due []store.Signer, now time.Time) ([]store.Signer, error) {
	out := make([]store.Signer, 0, len(due))
	for _, sg := range due {
		sg.Status = store.SignerInvited
		t := now
		sg.InvitedAt = &t
		sg.NextReminderAt = nil
		if sub.Reminder != nil && sub.Reminder.Max > 0 {
			next := now.AddDate(0, 0, sub.Reminder.IntervalDays)
			sg.NextReminderAt = &next
		}
		if err := tx.UpdateSigner(ctx, sg); err != nil {
			return nil, err
		}
		id := sg.ID
		if err := Event(ctx, tx, sub.TenantID, sub.ID, &id, "", "signer.invited", nil, now); err != nil {
			return nil, err
		}
		out = append(out, sg)
	}
	return out, nil
}

// ---------------------------------------------------------------- mail

// SenderName is the display name of the submission's sender.
func (s *Service) SenderName(ctx context.Context, sub store.Submission) string {
	if s.d.Contacts == nil || sub.CreatedBy == "" {
		return ""
	}
	found, err := s.d.Contacts.Contacts(ctx, sub.TenantID, []string{sub.CreatedBy})
	if err != nil {
		return ""
	}
	return found[sub.CreatedBy].DisplayName
}

// Invite e-mails the given signers (key: invitation or next_signer) and
// refreshes their inboxes. Called after the transaction committed.
func (s *Service) Invite(ctx context.Context, sub store.Submission, signers []store.Signer, key string) {
	sender := s.SenderName(ctx, sub)
	for _, sg := range signers {
		vars := map[string]string{"document": sub.Name, "sender": sender, "signer": sg.Name, "link": s.d.Mail.SignLink(sg.ID)}
		s.Mailed(ctx, sub, sg, key, s.d.Mail.Send(ctx, sub.TenantID, key, sg.Email, vars, sub.ID))
		s.d.Events.InboxChanged(ctx, sub.TenantID, sg.UserID, events.InboxPayload{SignerID: sg.ID, SubmissionID: sub.ID, State: "to_sign"})
	}
}

// Mailed records the outcome of one mail to a signer: a failure sets
// mail_error and adds a history entry; a success clears an earlier error.
func (s *Service) Mailed(ctx context.Context, sub store.Submission, sg store.Signer, key, reason string) {
	if reason == "" && sg.MailError == "" {
		return
	}
	now := s.d.Now()
	_ = s.d.Store.Tx(ctx, sub.TenantID, func(tx repo.Store) error {
		cur, err := tx.GetSigner(ctx, sub.TenantID, sg.ID)
		if err != nil {
			return err
		}
		cur.MailError = reason
		if err := tx.UpdateSigner(ctx, cur); err != nil {
			return err
		}
		if reason == "" {
			return nil
		}
		id := sg.ID
		return Event(ctx, tx, sub.TenantID, sub.ID, &id, "", "mail.failed", map[string]any{"template": key}, now)
	})
}

// Broadcast mails a key to the sender and every signer (except skip) with
// the same variables plus the recipient's name. Failures are recorded on
// signers only.
func (s *Service) Broadcast(ctx context.Context, sub store.Submission, signers []store.Signer, key string, vars map[string]string, skipUserID string) {
	for _, sg := range signers {
		if sg.UserID == skipUserID {
			continue
		}
		v := clone(vars)
		v["recipient"], v["link"] = sg.Name, s.d.Mail.SubmissionLink(sub.ID)
		s.Mailed(ctx, sub, sg, key, s.d.Mail.Send(ctx, sub.TenantID, key, sg.Email, v, sub.ID))
	}
	if sub.CreatedBy == "" || sub.CreatedBy == skipUserID || slices.Contains(userIDs(signers), sub.CreatedBy) {
		return
	}
	found, err := s.d.Contacts.Contacts(ctx, sub.TenantID, []string{sub.CreatedBy})
	if err != nil {
		return
	}
	if c, ok := found[sub.CreatedBy]; ok {
		v := clone(vars)
		v["recipient"], v["link"] = c.DisplayName, s.d.Mail.SubmissionLink(sub.ID)
		_ = s.d.Mail.Send(ctx, sub.TenantID, key, c.Email, v, sub.ID)
	}
}

func clone(m map[string]string) map[string]string {
	out := make(map[string]string, len(m)+2)
	for k, v := range m {
		out[k] = v
	}
	return out
}

// ---------------------------------------------------------------- send

// Send starts a draft submission and invites the first signers.
func (s *Service) Send(ctx context.Context, subj authz.Subjects, id string) (Detail, error) {
	var sub store.Submission
	var all, invited []store.Signer
	now := s.d.Now()
	err := s.d.Store.Tx(ctx, subj.TenantID, func(tx repo.Store) error {
		var err error
		sub, all, err = tx.LockSubmission(ctx, subj.TenantID, id)
		if err != nil {
			return notFound(err)
		}
		if !s.visible(ctx, subj, sub, all) {
			return apperr.NotFound
		}
		if !authz.CanControlSubmission(ctx, s.d.Checker, subj, sub.CreatedBy) {
			return apperr.Forbidden
		}
		if sub.Status != store.SubmissionDraft {
			return apperr.NotDraft
		}
		if sub.ExpiresAt != nil && !now.Before(*sub.ExpiresAt) {
			return apperr.Validation.WithField("expires_at")
		}
		sub.Status, sub.SentAt, sub.UpdatedAt = store.SubmissionInProgress, &now, now
		if err := tx.UpdateSubmission(ctx, sub); err != nil {
			return err
		}
		if invited, err = MarkInvited(ctx, tx, sub, Due(sub, all), now); err != nil {
			return err
		}
		return Event(ctx, tx, subj.TenantID, sub.ID, nil, subj.UserID, "submission.sent", nil, now)
	})
	if err != nil {
		s.record(ctx, subj, audit.SubmissionSend, audit.SubjectSubmission, id, audit.OutcomeRefused, reasonOf(err), nil)
		return Detail{}, err
	}
	s.record(ctx, subj, audit.SubmissionSend, audit.SubjectSubmission, id, audit.OutcomeOK, "", map[string]any{"invited": len(invited)})
	s.Invite(ctx, sub, invited, mail.Invitation)
	return s.Get(ctx, subj, id)
}

// ---------------------------------------------------------------- views

func (s *Service) visible(ctx context.Context, subj authz.Subjects, sub store.Submission, signers []store.Signer) bool {
	return sub.TenantID == subj.TenantID && authz.CanViewSubmission(ctx, s.d.Checker, subj, sub.CreatedBy, userIDs(signers))
}

// Get returns a submission the caller may view.
func (s *Service) Get(ctx context.Context, subj authz.Subjects, id string) (Detail, error) {
	sub, signers, err := s.d.Store.GetSubmission(ctx, subj.TenantID, id)
	if err != nil {
		return Detail{}, notFound(err)
	}
	if !s.visible(ctx, subj, sub, signers) {
		return Detail{}, apperr.NotFound
	}
	return Detail{Submission: sub, Signers: signers, CanControl: authz.CanControlSubmission(ctx, s.d.Checker, subj, sub.CreatedBy)}, nil
}

// List pages submissions: signing:read or submissions:manage see every one,
// everybody else the ones they sent (mine forces that for everyone).
func (s *Service) List(ctx context.Context, subj authz.Subjects, f repo.SubmissionFilter, mine bool) ([]Detail, int, error) {
	if mine || !(authz.Allowed(ctx, s.d.Checker, subj, authz.SigningRead) || authz.Allowed(ctx, s.d.Checker, subj, authz.SubmissionsManage)) {
		f.CreatedBy = subj.UserID
	}
	subs, total, err := s.d.Store.ListSubmissions(ctx, subj.TenantID, f)
	if err != nil {
		return nil, 0, err
	}
	out := make([]Detail, 0, len(subs))
	for _, sub := range subs {
		_, signers, err := s.d.Store.GetSubmission(ctx, subj.TenantID, sub.ID)
		if err != nil {
			return nil, 0, err
		}
		out = append(out, Detail{Submission: sub, Signers: signers, CanControl: authz.CanControlSubmission(ctx, s.d.Checker, subj, sub.CreatedBy)})
	}
	return out, total, nil
}

// Inbox pages the caller's signer slots (to sign, or signed/declined).
func (s *Service) Inbox(ctx context.Context, subj authz.Subjects, signed bool, page, size int) ([]repo.InboxItem, int, error) {
	if !subj.IsMember() {
		return nil, 0, apperr.Forbidden
	}
	return s.d.Store.Inbox(ctx, subj.TenantID, subj.UserID, signed, page, size)
}

// Events returns the history of a submission the caller may view.
func (s *Service) Events(ctx context.Context, subj authz.Subjects, id string) ([]store.Event, error) {
	if _, err := s.Get(ctx, subj, id); err != nil {
		return nil, err
	}
	return s.d.Store.ListEvents(ctx, subj.TenantID, id)
}

// Members lists active tenant members for the signer picker (names only).
func (s *Service) Members(ctx context.Context, subj authz.Subjects, q string) ([]contacts.Member, error) {
	if err := authz.Require(ctx, s.d.Checker, subj, authz.SubmissionsCreate); err != nil {
		return nil, apperr.Forbidden
	}
	m, err := s.d.Contacts.Members(ctx, subj.TenantID, strings.TrimSpace(q), 50)
	if err != nil {
		return nil, apperr.TemporarilyUnavailable
	}
	return m, nil
}

// Document opens a version of the submission's PDF (FR-017): the final
// version of a completed submission for every viewer; otherwise the sender
// and signing:read may open any version, and a signer only after signing.
// version < 0 means the current (or final) one.
func (s *Service) Document(ctx context.Context, subj authz.Subjects, id string, version int) (io.ReadCloser, store.DocumentVersion, store.Submission, error) {
	d, err := s.Get(ctx, subj, id)
	if err != nil {
		return nil, store.DocumentVersion{}, store.Submission{}, err
	}
	sub := d.Submission
	privileged := authz.IsSender(subj, sub.CreatedBy) || authz.Allowed(ctx, s.d.Checker, subj, authz.SigningRead) ||
		authz.Allowed(ctx, s.d.Checker, subj, authz.SubmissionsManage)
	want := sub.CurrentVersion
	if sub.FinalVersion != nil {
		want = *sub.FinalVersion
	}
	if version >= 0 {
		if !privileged && version != want {
			return nil, store.DocumentVersion{}, sub, apperr.NotFound
		}
		want = version
	}
	if !privileged && sub.FinalVersion == nil && !signedBy(d.Signers, subj.UserID) {
		return nil, store.DocumentVersion{}, sub, apperr.Forbidden
	}
	v, err := s.d.Store.GetVersion(ctx, subj.TenantID, sub.ID, want)
	if err != nil {
		return nil, store.DocumentVersion{}, sub, notFound(err)
	}
	if !blob.InTenant(v.ObjectKey, subj.TenantID) {
		return nil, store.DocumentVersion{}, sub, apperr.NotFound
	}
	rc, err := s.d.Blob.Get(ctx, v.ObjectKey)
	if err != nil {
		return nil, store.DocumentVersion{}, sub, err
	}
	return rc, v, sub, nil
}

func signedBy(signers []store.Signer, userID string) bool {
	for _, sg := range signers {
		if sg.UserID == userID && sg.Status == store.SignerSigned {
			return true
		}
	}
	return false
}

// AuditTrail opens the audit-trail PDF of a completed submission.
func (s *Service) AuditTrail(ctx context.Context, subj authz.Subjects, id string) (io.ReadCloser, store.Submission, error) {
	d, err := s.Get(ctx, subj, id)
	if err != nil {
		return nil, store.Submission{}, err
	}
	sub := d.Submission
	if sub.AuditTrailKey == "" || !blob.InTenant(sub.AuditTrailKey, subj.TenantID) {
		return nil, sub, apperr.NotFound
	}
	rc, err := s.d.Blob.Get(ctx, sub.AuditTrailKey)
	if err != nil {
		return nil, sub, err
	}
	return rc, sub, nil
}

// ---------------------------------------------------------------- control (US5)

// control locks the submission and checks the control right.
func (s *Service) control(ctx context.Context, tx repo.Store, subj authz.Subjects, id string) (store.Submission, []store.Signer, error) {
	sub, signers, err := tx.LockSubmission(ctx, subj.TenantID, id)
	if err != nil {
		return sub, nil, notFound(err)
	}
	if !s.visible(ctx, subj, sub, signers) {
		return sub, nil, apperr.NotFound
	}
	if !authz.CanControlSubmission(ctx, s.d.Checker, subj, sub.CreatedBy) {
		return sub, nil, apperr.Forbidden
	}
	return sub, signers, nil
}

func cleanReason(r string) (string, error) {
	r = strings.TrimSpace(r)
	if r == "" || len([]rune(r)) > 500 {
		return "", apperr.Validation.WithField("reason")
	}
	return r, nil
}

// Cancel stops an unfinished submission with a reason.
func (s *Service) Cancel(ctx context.Context, subj authz.Subjects, id, reason string) (Detail, error) {
	reason, err := cleanReason(reason)
	if err != nil {
		return Detail{}, err
	}
	var sub store.Submission
	var signers []store.Signer
	now := s.d.Now()
	err = s.d.Store.Tx(ctx, subj.TenantID, func(tx repo.Store) error {
		var err error
		if sub, signers, err = s.control(ctx, tx, subj, id); err != nil {
			return err
		}
		if sub.Status != store.SubmissionDraft && sub.Status != store.SubmissionInProgress {
			return apperr.SubmissionNotOpen
		}
		sub.Status, sub.CancelledAt, sub.CancelReason, sub.UpdatedAt = store.SubmissionCancelled, &now, reason, now
		if err := tx.UpdateSubmission(ctx, sub); err != nil {
			return err
		}
		return Event(ctx, tx, subj.TenantID, id, nil, subj.UserID, "submission.cancelled", nil, now)
	})
	if err != nil {
		s.record(ctx, subj, audit.SubmissionCancel, audit.SubjectSubmission, id, audit.OutcomeRefused, reasonOf(err), nil)
		return Detail{}, err
	}
	s.record(ctx, subj, audit.SubmissionCancel, audit.SubjectSubmission, id, audit.OutcomeOK, "", nil)
	s.Ended(ctx, sub, signers, mail.Cancelled, map[string]string{"document": sub.Name, "reason": reason}, subj.UserID)
	s.d.Events.Cancelled(ctx, sub.TenantID, events.CancelledPayload{SubmissionID: sub.ID, TemplateID: sub.TemplateID, ReasonCode: events.ReasonCancelled})
	return s.Get(ctx, subj, id)
}

// Ended informs everyone of a cancelled/declined/expired submission (only
// signers who were invited, plus the sender) and refreshes inboxes.
func (s *Service) Ended(ctx context.Context, sub store.Submission, signers []store.Signer, key string, vars map[string]string, skip string) {
	if sub.SentAt == nil {
		return
	}
	var invited []store.Signer
	for _, sg := range signers {
		if sg.Status != store.SignerPending {
			invited = append(invited, sg)
			s.d.Events.InboxChanged(ctx, sub.TenantID, sg.UserID, events.InboxPayload{SignerID: sg.ID, SubmissionID: sub.ID, State: "closed"})
		}
	}
	s.Broadcast(ctx, sub, invited, key, vars, skip)
}

// Resend sends the invitation of an invited or opened signer again.
func (s *Service) Resend(ctx context.Context, subj authz.Subjects, id, signerID string) (Detail, error) {
	var sub store.Submission
	var target store.Signer
	now := s.d.Now()
	err := s.d.Store.Tx(ctx, subj.TenantID, func(tx repo.Store) error {
		var signers []store.Signer
		var err error
		if sub, signers, err = s.control(ctx, tx, subj, id); err != nil {
			return err
		}
		if !sub.Open(now) {
			return apperr.SubmissionNotOpen
		}
		i := slices.IndexFunc(signers, func(sg store.Signer) bool { return sg.ID == signerID })
		if i < 0 {
			return apperr.NotFound
		}
		target = signers[i]
		if target.Status != store.SignerInvited && target.Status != store.SignerOpened {
			return apperr.SignerFinal
		}
		sid := target.ID
		return Event(ctx, tx, subj.TenantID, id, &sid, subj.UserID, "invitation.resent", nil, now)
	})
	if err != nil {
		s.record(ctx, subj, audit.SubmissionResend, audit.SubjectSigner, signerID, audit.OutcomeRefused, reasonOf(err), nil)
		return Detail{}, err
	}
	s.record(ctx, subj, audit.SubmissionResend, audit.SubjectSigner, signerID, audit.OutcomeOK, "", nil)
	s.Invite(ctx, sub, []store.Signer{target}, mail.Invitation)
	return s.Get(ctx, subj, id)
}

// ReplaceSigner assigns an unsigned slot to another active member.
func (s *Service) ReplaceSigner(ctx context.Context, subj authz.Subjects, id, signerID, userID string) (Detail, error) {
	var sub store.Submission
	var repl store.Signer
	var reinvite bool
	now := s.d.Now()
	err := s.d.Store.Tx(ctx, subj.TenantID, func(tx repo.Store) error {
		var signers []store.Signer
		var err error
		if sub, signers, err = s.control(ctx, tx, subj, id); err != nil {
			return err
		}
		if sub.Status != store.SubmissionDraft && !sub.Open(now) {
			return apperr.SubmissionNotOpen
		}
		i := slices.IndexFunc(signers, func(sg store.Signer) bool { return sg.ID == signerID })
		if i < 0 {
			return apperr.NotFound
		}
		repl = signers[i]
		if repl.Final() {
			return apperr.SignerFinal
		}
		if repl.UserID == userID || slices.Contains(userIDs(signers), userID) {
			return apperr.InvalidSigner.WithField("user_id")
		}
		found, err := s.d.Contacts.Contacts(ctx, subj.TenantID, []string{userID})
		if err != nil {
			return apperr.TemporarilyUnavailable
		}
		c, ok := found[userID]
		if !ok {
			return apperr.InvalidSigner.WithField("user_id")
		}
		previous := repl.UserID
		repl.UserID, repl.Name, repl.Email, repl.MailError, repl.OpenedAt = userID, c.DisplayName, c.Email, "", nil
		if repl.Name == "" {
			repl.Name = c.Email
		}
		reinvite = repl.Status == store.SignerInvited || repl.Status == store.SignerOpened
		if reinvite {
			repl.Status, repl.InvitedAt = store.SignerInvited, &now
		}
		if err := tx.UpdateSigner(ctx, repl); err != nil {
			return err
		}
		sid := repl.ID
		if err := Event(ctx, tx, subj.TenantID, id, &sid, subj.UserID, "signer.replaced", nil, now); err != nil {
			return err
		}
		if reinvite {
			s.d.Events.InboxChanged(ctx, sub.TenantID, previous, events.InboxPayload{SignerID: repl.ID, SubmissionID: sub.ID, State: "closed"})
		}
		return nil
	})
	if err != nil {
		s.record(ctx, subj, audit.SubmissionSignerReplace, audit.SubjectSigner, signerID, audit.OutcomeRefused, reasonOf(err), nil)
		return Detail{}, err
	}
	s.record(ctx, subj, audit.SubmissionSignerReplace, audit.SubjectSigner, signerID, audit.OutcomeOK, "", nil)
	if reinvite {
		s.Invite(ctx, sub, []store.Signer{repl}, mail.Invitation)
	}
	return s.Get(ctx, subj, id)
}

// Delete removes a submission and its stored documents.
func (s *Service) Delete(ctx context.Context, subj authz.Subjects, id string) error {
	var sub store.Submission
	var versions []store.DocumentVersion
	var signers []store.Signer
	err := s.d.Store.Tx(ctx, subj.TenantID, func(tx repo.Store) error {
		var err error
		if sub, signers, err = s.control(ctx, tx, subj, id); err != nil {
			return err
		}
		if versions, err = tx.ListVersions(ctx, subj.TenantID, id); err != nil {
			return err
		}
		return tx.DeleteSubmission(ctx, subj.TenantID, id)
	})
	if err != nil {
		s.record(ctx, subj, audit.SubmissionDelete, audit.SubjectSubmission, id, audit.OutcomeRefused, reasonOf(err), nil)
		return err
	}
	// Remove every object under the submission prefix (versions, uploads,
	// the audit trail); the retention sweep catches anything left behind.
	for _, v := range versions {
		_ = s.d.Blob.Delete(ctx, v.ObjectKey)
	}
	if keys, err := s.d.Blob.List(ctx, blob.SubmissionPrefix(subj.TenantID, id), 10000); err == nil {
		for _, k := range keys {
			_ = s.d.Blob.Delete(ctx, k)
		}
	}
	if sub.Status == store.SubmissionInProgress {
		for _, sg := range signers {
			s.d.Events.InboxChanged(ctx, sub.TenantID, sg.UserID, events.InboxPayload{SignerID: sg.ID, SubmissionID: sub.ID, State: "closed"})
		}
	}
	s.record(ctx, subj, audit.SubmissionDelete, audit.SubjectSubmission, id, audit.OutcomeOK, "", map[string]any{"objects": len(versions)})
	return nil
}

// String is a short description for logs (no values).
func (d Detail) String() string {
	return fmt.Sprintf("submission %s (%s, %d signers)", d.Submission.ID, d.Submission.Status, len(d.Signers))
}
