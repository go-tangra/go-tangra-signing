// Package signing is the signer's side of a submission (US2, US5): the
// signing session (own fields only, FR-010), opening, the signing pipeline
// and declining.
//
// Signing (research D3) checks the request, unlocks the signer's key with
// the PIN (a wrong PIN is counted in its own transaction and nothing is
// signed), then in one transaction holding the submission's row lock:
// re-checks the state (FR-011), stamps the signer's values onto the current
// document version as an incremental update, applies the PAdES approval
// signature (the first signature field carries the visible appearance),
// stores version n+1, marks the signer signed, invites the next signers
// (sequential) or completes the submission and queues the audit-trail job
// (research D9). Any failure rolls back and removes the stored objects.
package signing

import (
	"bytes"
	"context"
	"crypto"
	"crypto/x509"
	"errors"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/go-tangra/go-tangra-signing/v4/internal/apperr"
	"github.com/go-tangra/go-tangra-signing/v4/internal/audit"
	"github.com/go-tangra/go-tangra-signing/v4/internal/authz"
	"github.com/go-tangra/go-tangra-signing/v4/internal/blob"
	"github.com/go-tangra/go-tangra-signing/v4/internal/certs"
	"github.com/go-tangra/go-tangra-signing/v4/internal/events"
	"github.com/go-tangra/go-tangra-signing/v4/internal/mail"
	"github.com/go-tangra/go-tangra-signing/v4/internal/metrics"
	"github.com/go-tangra/go-tangra-signing/v4/internal/pdf/incr"
	"github.com/go-tangra/go-tangra-signing/v4/internal/pdf/overlay"
	"github.com/go-tangra/go-tangra-signing/v4/internal/pdf/render"
	"github.com/go-tangra/go-tangra-signing/v4/internal/pdf/sign"
	"github.com/go-tangra/go-tangra-signing/v4/internal/pki"
	"github.com/go-tangra/go-tangra-signing/v4/internal/repo"
	"github.com/go-tangra/go-tangra-signing/v4/internal/store"
	"github.com/go-tangra/go-tangra-signing/v4/internal/submissions"
)

// Evaluation is the outcome of the conditions and formulas over the
// submission's values (package rules, US6).
type Evaluation struct {
	Hidden   map[string]bool   // field id → not shown (ignored)
	Required map[string]bool   // field id → required now
	Computed map[string]string // field id → server-computed value
}

// Evaluator evaluates the rules; nil means no rules (every field visible,
// required as configured, no formulas).
type Evaluator func(fields []store.Field, values map[string]string) (Evaluation, error)

func plain(fields []store.Field, _ map[string]string) (Evaluation, error) {
	ev := Evaluation{Hidden: map[string]bool{}, Required: map[string]bool{}, Computed: map[string]string{}}
	for _, f := range fields {
		ev.Required[f.ID] = f.Required
	}
	return ev, nil
}

// Limits bound signing input.
type Limits struct {
	MaxPDFBytes   int64 // default 50 MiB
	MaxImageBytes int   // default 1 MiB per image
	MaxFileBytes  int64 // file-field uploads, default MaxPDFBytes
}

// Deps wire the service.
type Deps struct {
	Store   repo.Store
	Blob    blob.Store
	Audit   audit.Recorder
	Subs    *submissions.Service
	Me      *certs.Me
	PKI     *pki.PKI
	Events  events.Emitter
	Metrics *metrics.Metrics
	Rules   Evaluator
	Now     func() time.Time
	Limits  Limits
	// Location is the /Location of signatures (optional).
	Location string
	// Limited reports whether the user exceeded the signing rate (nil = no
	// limit); an error lets the request through.
	Limited func(ctx context.Context, tenantID, userID string) (bool, error)
	// OnCompleted is told after a signature completed a submission (the
	// audit-trail worker's kick).
	OnCompleted func()
}

// Service signs.
type Service struct{ d Deps }

// New builds the service.
func New(d Deps) *Service {
	if d.Now == nil {
		d.Now = func() time.Time { return time.Now().UTC() }
	}
	if d.Rules == nil {
		d.Rules = plain
	}
	if d.Limits.MaxPDFBytes <= 0 {
		d.Limits.MaxPDFBytes = 50 << 20
	}
	if d.Limits.MaxImageBytes <= 0 {
		d.Limits.MaxImageBytes = 1 << 20
	}
	if d.Limits.MaxFileBytes <= 0 {
		d.Limits.MaxFileBytes = d.Limits.MaxPDFBytes
	}
	return &Service{d: d}
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

func (s *Service) record(ctx context.Context, subj authz.Subjects, t audit.EventType, id, outcome, reason string, detail map[string]any) {
	audit.Emit(ctx, s.d.Audit, audit.Event{TenantID: subj.TenantID, EventType: t, ActorKind: audit.ActorOf(subj.ActorKind),
		ActorID: subj.ActorID(), SubjectKind: audit.SubjectSigner, SubjectID: id, Outcome: outcome, Reason: reason, Details: detail})
}

// own loads the caller's signer slot; anybody else's is "not found".
func (s *Service) own(ctx context.Context, st repo.Store, subj authz.Subjects, signerID string, lock bool) (store.Submission, []store.Signer, store.Signer, error) {
	if !subj.IsMember() {
		return store.Submission{}, nil, store.Signer{}, apperr.NotFound
	}
	sg, err := st.GetSigner(ctx, subj.TenantID, signerID)
	if err != nil {
		return store.Submission{}, nil, store.Signer{}, notFound(err)
	}
	if sg.UserID != subj.UserID {
		return store.Submission{}, nil, store.Signer{}, apperr.NotFound
	}
	var sub store.Submission
	var all []store.Signer
	if lock {
		sub, all, err = st.LockSubmission(ctx, subj.TenantID, sg.SubmissionID)
	} else {
		sub, all, err = st.GetSubmission(ctx, subj.TenantID, sg.SubmissionID)
	}
	if err != nil {
		return store.Submission{}, nil, store.Signer{}, notFound(err)
	}
	for _, x := range all {
		if x.ID == sg.ID {
			sg = x
		}
	}
	if sub.Status == store.SubmissionDraft {
		return store.Submission{}, nil, store.Signer{}, apperr.NotFound
	}
	return sub, all, sg, nil
}

// refusal returns why the signer cannot act now (FR-011), or nil.
func refusal(sub store.Submission, all []store.Signer, sg store.Signer, now time.Time) error {
	switch {
	case sg.Status == store.SignerSigned:
		return apperr.AlreadySigned
	case sg.Status == store.SignerDeclined:
		return apperr.SignerFinal
	case !sub.Open(now):
		return apperr.SubmissionNotOpen
	case !submissions.Turn(sub, all, sg):
		return apperr.NotYourTurn
	}
	return nil
}

// ---------------------------------------------------------------- session

// Session is what the signer's page needs.
type Session struct {
	Submission store.Submission
	Signer     store.Signer
	Fields     []store.Field     // the signer's own fields (with prefills)
	AllFields  []store.Field     // every field, without values (rules)
	Values     map[string]string // prefills and the values of signers who signed
	CertState  string
	LockedTill *time.Time
	CanSign    bool
	Reason     string // why not, when CanSign is false
}

// Session returns the caller's signing session.
func (s *Service) Session(ctx context.Context, subj authz.Subjects, signerID string) (Session, error) {
	sub, all, sg, err := s.own(ctx, s.d.Store, subj, signerID, false)
	if err != nil {
		return Session{}, err
	}
	now := s.d.Now()
	out := Session{Submission: sub, Signer: sg, Values: map[string]string{}}
	for _, f := range sub.Fields {
		if f.Prefill != "" {
			out.Values[f.ID] = f.Prefill
		}
		bare := f
		bare.Prefill = ""
		out.AllFields = append(out.AllFields, bare)
		if f.Party == sg.Party {
			out.Fields = append(out.Fields, f)
		}
	}
	for _, x := range all {
		if x.Status != store.SignerSigned {
			continue
		}
		for id, v := range x.Values {
			if !strings.HasPrefix(v, blob.TenantPrefix(sub.TenantID)) { // object keys stay private
				out.Values[id] = v
			}
		}
	}
	var cert *store.Certificate
	if c, err := s.d.Store.ActiveSignerCertificate(ctx, subj.TenantID, subj.UserID); err == nil {
		cert = &c
	} else if !errors.Is(err, repo.ErrNotFound) {
		return Session{}, err
	}
	out.CertState, out.LockedTill = certs.State(cert, now)
	if r := refusal(sub, all, sg, now); r != nil {
		out.Reason = reasonOf(r)
	} else {
		out.CanSign = true
	}
	return out, nil
}

// Document opens the current document version for the caller's slot.
func (s *Service) Document(ctx context.Context, subj authz.Subjects, signerID string) (io.ReadCloser, store.DocumentVersion, store.Submission, error) {
	sub, _, _, err := s.own(ctx, s.d.Store, subj, signerID, false)
	if err != nil {
		return nil, store.DocumentVersion{}, store.Submission{}, err
	}
	v, err := s.d.Store.GetVersion(ctx, subj.TenantID, sub.ID, sub.CurrentVersion)
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

// Open marks an invited signer as having opened the document.
func (s *Service) Open(ctx context.Context, subj authz.Subjects, signerID string) error {
	now := s.d.Now()
	return s.d.Store.Tx(ctx, subj.TenantID, func(tx repo.Store) error {
		sub, _, sg, err := s.own(ctx, tx, subj, signerID, true)
		if err != nil {
			return err
		}
		if sg.Status != store.SignerInvited {
			return nil
		}
		sg.Status, sg.OpenedAt = store.SignerOpened, &now
		if err := tx.UpdateSigner(ctx, sg); err != nil {
			return err
		}
		id := sg.ID
		return submissions.Event(ctx, tx, sub.TenantID, sub.ID, &id, subj.UserID, "signer.opened", nil, now)
	})
}

// ---------------------------------------------------------------- sign

// Input is one signing request.
type Input struct {
	Values    map[string]string // field id → value (own text fields)
	Uploads   map[string][]byte // field id → PNG/JPEG or file bytes
	Signature []byte            // drawn/typed signature image (optional)
	PIN       string
	IP        string
	UserAgent string
}

// Result is the outcome of a signing.
type Result struct {
	SignerStatus     string
	SubmissionStatus string
}

// prepared is the validated request against the frozen fields.
type prepared struct {
	fields  []store.Field     // own visible fields
	values  map[string]string // own text values (incl. computed)
	uploads map[string][]byte
}

// check validates values and uploads against the signer's fields and the
// rules (FR-011 last point); hidden fields are ignored.
func (s *Service) check(sub store.Submission, all []store.Signer, sg store.Signer, in Input) (prepared, error) {
	merged := map[string]string{}
	for _, f := range sub.Fields {
		if f.Prefill != "" {
			merged[f.ID] = f.Prefill
		}
	}
	for _, x := range all {
		if x.Status == store.SignerSigned {
			for k, v := range x.Values {
				merged[k] = v
			}
		}
	}
	own := map[string]store.Field{}
	for _, f := range sub.Fields {
		if f.Party == sg.Party {
			own[f.ID] = f
		}
	}
	for id, v := range in.Values {
		f, ok := own[id]
		if !ok || !submissions.TextValued(f.Type) || !submissions.ValidValue(f, v) {
			return prepared{}, apperr.InvalidValue.WithField(id)
		}
		merged[id] = v
	}
	for id, b := range in.Uploads {
		f, ok := own[id]
		if !ok || submissions.TextValued(f.Type) || f.Type == "signature" {
			return prepared{}, apperr.InvalidValue.WithField(id)
		}
		if f.Type != "file" {
			if len(b) > s.d.Limits.MaxImageBytes || !isImage(b) {
				return prepared{}, apperr.InvalidValue.WithField(id)
			}
		} else if int64(len(b)) > s.d.Limits.MaxFileBytes {
			return prepared{}, apperr.PayloadTooLarge.WithField(id)
		}
	}
	if len(in.Signature) > 0 && (len(in.Signature) > s.d.Limits.MaxImageBytes || !isImage(in.Signature)) {
		return prepared{}, apperr.InvalidValue.WithField("signature")
	}
	ev, err := s.d.Rules(sub.Fields, merged)
	if err != nil {
		return prepared{}, err
	}
	out := prepared{values: map[string]string{}, uploads: map[string][]byte{}}
	for _, f := range sub.Fields {
		if f.Party != sg.Party || ev.Hidden[f.ID] {
			continue
		}
		v := merged[f.ID]
		if c, ok := ev.Computed[f.ID]; ok {
			v = c // a tampered calculated value is replaced (US6)
		}
		if ev.Required[f.ID] && !submissions.Filled(f, v, len(in.Uploads[f.ID]) > 0) {
			return prepared{}, apperr.MissingRequired.WithField(f.ID)
		}
		out.fields = append(out.fields, f)
		if submissions.TextValued(f.Type) && v != "" {
			out.values[f.ID] = v
		}
		if b := in.Uploads[f.ID]; len(b) > 0 {
			out.uploads[f.ID] = b
		}
	}
	return out, nil
}

func isImage(b []byte) bool {
	ct := http.DetectContentType(b)
	return ct == "image/png" || ct == "image/jpeg"
}

func ext(b []byte) string {
	switch http.DetectContentType(b) {
	case "image/png":
		return "png"
	case "image/jpeg":
		return "jpg"
	default:
		return "bin"
	}
}

// Sign signs the caller's slot with their personal certificate.
func (s *Service) Sign(ctx context.Context, subj authz.Subjects, signerID string, in Input) (Result, error) {
	res, err := s.sign(ctx, subj, signerID, in)
	outcome := "ok"
	if err != nil {
		outcome = reasonOf(err)
		s.record(ctx, subj, audit.SignerSign, signerID, audit.OutcomeRefused, outcome, map[string]any{"method": store.MethodLocal})
	}
	s.d.Metrics.Signing(store.MethodLocal, outcome)
	return res, err
}

func (s *Service) sign(ctx context.Context, subj authz.Subjects, signerID string, in Input) (Result, error) {
	now := s.d.Now()
	if s.d.Limited != nil {
		if limited, err := s.d.Limited(ctx, subj.TenantID, subj.UserID); err == nil && limited {
			return Result{}, apperr.RateLimited
		}
	}
	// Fast checks without the lock, so a doomed request never costs a PIN try.
	sub, all, sg, err := s.own(ctx, s.d.Store, subj, signerID, false)
	if err != nil {
		return Result{}, err
	}
	if err := refusal(sub, all, sg, now); err != nil {
		return Result{}, err
	}
	if _, err := s.check(sub, all, sg, in); err != nil {
		return Result{}, err
	}
	cert, err := s.d.Store.ActiveSignerCertificate(ctx, subj.TenantID, subj.UserID)
	if errors.Is(err, repo.ErrNotFound) {
		return Result{}, apperr.CertificateMissing
	} else if err != nil {
		return Result{}, err
	}
	key, err := s.d.Me.Unlock(ctx, subj, cert, in.PIN)
	if err != nil {
		if errors.Is(err, apperr.PINInvalid) {
			s.d.Metrics.PINFailure()
		}
		return Result{}, err
	}
	chain, err := s.d.PKI.Chain(ctx, cert)
	if err != nil {
		return Result{}, err
	}

	var objects []string
	var invited []store.Signer
	var completed bool
	err = s.d.Store.Tx(ctx, subj.TenantID, func(tx repo.Store) error {
		sub, all, sg, err = s.own(ctx, tx, subj, signerID, true)
		if err != nil {
			return err
		}
		if err := refusal(sub, all, sg, now); err != nil {
			return err
		}
		p, err := s.check(sub, all, sg, in)
		if err != nil {
			return err
		}
		doc, err := s.current(ctx, tx, sub)
		if err != nil {
			return err
		}
		signed, err := s.apply(doc, p, in.Signature, sg, chain, key, now)
		if err != nil {
			return err
		}
		v := sub.CurrentVersion + 1
		vkey := blob.DocumentVersion(sub.TenantID, sub.ID, v)
		sum, err := s.d.Blob.Put(ctx, vkey, bytes.NewReader(signed), int64(len(signed)), "application/pdf")
		if err != nil {
			return err
		}
		objects = append(objects, vkey)
		values := p.values
		for id, b := range p.uploads {
			k := blob.SignerUpload(sub.TenantID, sub.ID, sg.ID, id, ext(b))
			if _, err := s.d.Blob.Put(ctx, k, bytes.NewReader(b), int64(len(b)), "application/octet-stream"); err != nil {
				return err
			}
			objects = append(objects, k)
			values[id] = k
		}
		if len(in.Signature) > 0 {
			k := blob.SignerUpload(sub.TenantID, sub.ID, sg.ID, "signature", ext(in.Signature))
			if _, err := s.d.Blob.Put(ctx, k, bytes.NewReader(in.Signature), int64(len(in.Signature)), "application/octet-stream"); err != nil {
				return err
			}
			objects = append(objects, k)
		}
		sid := sg.ID
		if err := tx.AddVersion(ctx, store.DocumentVersion{SubmissionID: sub.ID, Version: v, TenantID: sub.TenantID,
			ObjectKey: vkey, SHA256: sum, Size: int64(len(signed)), SignerID: &sid, CreatedAt: now}); err != nil {
			return err
		}
		certID := cert.ID
		sg.Status, sg.Values, sg.Method, sg.CertificateID = store.SignerSigned, values, store.MethodLocal, &certID
		sg.CertSubject, sg.CertSerial, sg.CertIssuer = chain[0].Subject.CommonName, cert.Serial, chain[0].Issuer.CommonName
		sg.IP, sg.UserAgent, sg.SignedAt, sg.NextReminderAt = in.IP, clip(in.UserAgent, 300), &now, nil
		if err := tx.UpdateSigner(ctx, sg); err != nil {
			return err
		}
		if err := submissions.Event(ctx, tx, sub.TenantID, sub.ID, &sid, subj.UserID, "signer.signed",
			map[string]any{"method": store.MethodLocal, "version": v}, now); err != nil {
			return err
		}
		for i := range all {
			if all[i].ID == sg.ID {
				all[i] = sg
			}
		}
		sub.CurrentVersion, sub.UpdatedAt = v, now
		if completed = submissions.Complete(all); completed {
			sub.Status, sub.CompletedAt, sub.FinalVersion = store.SubmissionCompleted, &now, &v
			if err := tx.EnqueueJob(ctx, store.Job{ID: store.NewID(), TenantID: sub.TenantID, Kind: "audit_trail",
				SubmissionID: sub.ID, NextAttemptAt: now, CreatedAt: now}); err != nil {
				return err
			}
			if err := submissions.Event(ctx, tx, sub.TenantID, sub.ID, nil, "", "submission.completed", map[string]any{"final_version": v}, now); err != nil {
				return err
			}
		} else if invited, err = submissions.MarkInvited(ctx, tx, sub, submissions.Due(sub, all), now); err != nil {
			return err
		}
		return tx.UpdateSubmission(ctx, sub)
	})
	if err != nil {
		for _, k := range objects {
			_ = s.d.Blob.Delete(context.WithoutCancel(ctx), k)
		}
		return Result{}, err
	}
	s.record(ctx, subj, audit.SignerSign, signerID, audit.OutcomeOK, "", map[string]any{"method": store.MethodLocal, "version": sub.CurrentVersion})
	s.d.Events.InboxChanged(ctx, sub.TenantID, sg.UserID, events.InboxPayload{SignerID: sg.ID, SubmissionID: sub.ID, State: "signed"})
	if len(invited) > 0 {
		s.d.Subs.Invite(ctx, sub, invited, mail.NextSigner)
	}
	if completed && s.d.OnCompleted != nil {
		s.d.OnCompleted()
	}
	return Result{SignerStatus: sg.Status, SubmissionStatus: sub.Status}, nil
}

func clip(s string, n int) string {
	if r := []rune(s); len(r) > n {
		return string(r[:n])
	}
	return s
}

// current reads the submission's current document version.
func (s *Service) current(ctx context.Context, st repo.Store, sub store.Submission) ([]byte, error) {
	v, err := st.GetVersion(ctx, sub.TenantID, sub.ID, sub.CurrentVersion)
	if err != nil {
		return nil, err
	}
	rc, err := s.d.Blob.Get(ctx, v.ObjectKey)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rc.Close() }()
	data, err := io.ReadAll(io.LimitReader(rc, s.d.Limits.MaxPDFBytes+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > s.d.Limits.MaxPDFBytes {
		return nil, apperr.PayloadTooLarge
	}
	return data, nil
}

// Stamped places the signer's values on doc and returns the stamped bytes
// and the visible signature appearance (shared with the QES flow).
func Stamped(doc []byte, fields []store.Field, values map[string]string, uploads map[string][]byte, signature []byte,
	name, issuer string, at time.Time) ([]byte, *sign.Visible, error) {
	pages, err := incr.Pages(doc)
	if err != nil {
		return nil, nil, err
	}
	res, err := overlay.Build(overlay.Input{Pages: pages, Fields: fields, Values: values, Uploads: uploads,
		Signature: signature, Name: name, Date: at.UTC().Format("2006-01-02 15:04 MST"), Issuer: issuer})
	if err != nil {
		return nil, nil, err
	}
	stamped, err := incr.AddStamps(doc, res.Stamps)
	if err != nil {
		return nil, nil, err
	}
	return stamped, res.Appearance, nil
}

// apply stamps and signs.
func (s *Service) apply(doc []byte, p prepared, signature []byte, sg store.Signer, chain []*x509.Certificate, key crypto.Signer, at time.Time) ([]byte, error) {
	stamped, appearance, err := Stamped(doc, p.fields, p.values, p.uploads, signature, sg.Name, chain[0].Issuer.CommonName, at)
	if err != nil {
		return nil, pdfErr(err)
	}
	out, err := sign.Sign(stamped, sign.Identity{Signer: key, Chain: chain}, sign.Options{
		Name: sg.Name, Reason: "Signed by " + sg.Name, Location: s.d.Location, Time: at, Visible: appearance,
	})
	if err != nil {
		return nil, pdfErr(err)
	}
	return out, nil
}

// pdfErr maps render refusals of user input to the API; the rest stays internal.
func pdfErr(err error) error {
	switch {
	case errors.Is(err, render.ErrImage):
		return apperr.InvalidValue.WithField("image")
	case errors.Is(err, render.ErrBox):
		return apperr.InvalidField
	}
	return err
}

// ---------------------------------------------------------------- decline (US5)

// Decline refuses to sign: the submission is cancelled (FR-014).
func (s *Service) Decline(ctx context.Context, subj authz.Subjects, signerID, reason, ip string) (Result, error) {
	reason = strings.TrimSpace(reason)
	if reason == "" || len([]rune(reason)) > 500 {
		return Result{}, apperr.Validation.WithField("reason")
	}
	now := s.d.Now()
	var sub store.Submission
	var all []store.Signer
	var sg store.Signer
	err := s.d.Store.Tx(ctx, subj.TenantID, func(tx repo.Store) error {
		var err error
		if sub, all, sg, err = s.own(ctx, tx, subj, signerID, true); err != nil {
			return err
		}
		if sg.Final() {
			return apperr.SignerFinal
		}
		if !sub.Open(now) {
			return apperr.SubmissionNotOpen
		}
		sg.Status, sg.DeclinedAt, sg.DeclineReason, sg.IP, sg.NextReminderAt = store.SignerDeclined, &now, reason, ip, nil
		if err := tx.UpdateSigner(ctx, sg); err != nil {
			return err
		}
		sub.Status, sub.CancelledAt, sub.CancelReason, sub.UpdatedAt = store.SubmissionCancelled, &now, reason, now
		if err := tx.UpdateSubmission(ctx, sub); err != nil {
			return err
		}
		sid := sg.ID
		return submissions.Event(ctx, tx, sub.TenantID, sub.ID, &sid, subj.UserID, "signer.declined", nil, now)
	})
	if err != nil {
		s.record(ctx, subj, audit.SignerDecline, signerID, audit.OutcomeRefused, reasonOf(err), nil)
		return Result{}, err
	}
	s.record(ctx, subj, audit.SignerDecline, signerID, audit.OutcomeOK, "", nil)
	for i := range all {
		if all[i].ID == sg.ID {
			all[i] = sg
		}
	}
	s.d.Subs.Ended(ctx, sub, all, mail.Declined, map[string]string{"document": sub.Name, "signer": sg.Name, "reason": reason}, subj.UserID)
	s.d.Events.Cancelled(ctx, sub.TenantID, events.CancelledPayload{SubmissionID: sub.ID, TemplateID: sub.TemplateID, ReasonCode: events.ReasonDeclined})
	return Result{SignerStatus: sg.Status, SubmissionStatus: sub.Status}, nil
}
