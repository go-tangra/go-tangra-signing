// Package jobs drains the signing_jobs queue (research D9). The only job
// kind is audit_trail, queued in the same transaction that completes a
// submission: the worker renders the audit-trail PDF, signs it with the
// tenant's system certificate, stores it, and only then tells everyone the
// submission is complete (completion e-mails, signing.submission.completed,
// inbox refresh). Failures are retried with a capped exponential backoff; a
// re-run after a crash never builds a second audit trail.
package jobs

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"time"

	"github.com/go-tangra/go-tangra-signing/v4/internal/blob"
	"github.com/go-tangra/go-tangra-signing/v4/internal/contacts"
	"github.com/go-tangra/go-tangra-signing/v4/internal/events"
	"github.com/go-tangra/go-tangra-signing/v4/internal/mail"
	"github.com/go-tangra/go-tangra-signing/v4/internal/pdf/audittrail"
	"github.com/go-tangra/go-tangra-signing/v4/internal/pdf/sign"
	"github.com/go-tangra/go-tangra-signing/v4/internal/pki"
	"github.com/go-tangra/go-tangra-signing/v4/internal/repo"
	"github.com/go-tangra/go-tangra-signing/v4/internal/store"
	"github.com/go-tangra/go-tangra-signing/v4/internal/submissions"
)

// KindAuditTrail is the audit-trail job.
const KindAuditTrail = "audit_trail"

// Deps wire the worker.
type Deps struct {
	Store    repo.Store
	Blob     blob.Store
	PKI      *pki.PKI
	Subs     *submissions.Service
	Contacts contacts.Directory
	Events   events.Emitter
	Log      *slog.Logger
	Now      func() time.Time
	Batch    int           // jobs per pass, default 10
	Lease    time.Duration // claim lease, default 5 min
	Interval time.Duration // idle poll interval, default 30 s
	MaxBytes int64         // bound on a document read, default 50 MiB
}

// Worker drains the queue.
type Worker struct {
	d    Deps
	kick chan struct{}
}

// New builds the worker.
func New(d Deps) *Worker {
	if d.Now == nil {
		d.Now = func() time.Time { return time.Now().UTC() }
	}
	if d.Batch <= 0 {
		d.Batch = 10
	}
	if d.Lease <= 0 {
		d.Lease = 5 * time.Minute
	}
	if d.Interval <= 0 {
		d.Interval = 30 * time.Second
	}
	if d.MaxBytes <= 0 {
		d.MaxBytes = 50 << 20
	}
	if d.Log == nil {
		d.Log = slog.New(slog.DiscardHandler)
	}
	return &Worker{d: d, kick: make(chan struct{}, 1)}
}

// Kick asks for a pass now (a submission just completed).
func (w *Worker) Kick() {
	select {
	case w.kick <- struct{}{}:
	default:
	}
}

// Run drains until ctx ends: a pass on every kick and every interval.
func (w *Worker) Run(ctx context.Context) {
	t := time.NewTicker(w.d.Interval)
	defer t.Stop()
	for {
		if _, _, err := w.Drain(ctx); err != nil && ctx.Err() == nil {
			w.d.Log.WarnContext(ctx, "signing jobs", "err", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		case <-w.kick:
		}
	}
}

// Drain runs one pass and reports the jobs completed and failed.
func (w *Worker) Drain(ctx context.Context) (done, failed int, err error) {
	jobs, err := w.d.Store.ClaimJobsSystem(ctx, w.d.Now(), w.d.Batch, w.d.Lease)
	if err != nil {
		return 0, 0, err
	}
	for _, j := range jobs {
		if err := w.process(ctx, j); err != nil {
			failed++
			w.d.Log.WarnContext(ctx, "signing job failed", "job", j.ID, "submission", j.SubmissionID, "attempt", j.Attempts, "err", err)
			if ferr := w.d.Store.FailJobSystem(ctx, j.ID, clip(err.Error()), w.d.Now().Add(Backoff(j.Attempts))); ferr != nil {
				return done, failed, ferr
			}
			continue
		}
		done++
	}
	return done, failed, nil
}

// Backoff is the wait before attempt n+1: 1, 2, 4 … minutes, at most 1 hour.
func Backoff(attempts int) time.Duration {
	if attempts < 1 {
		attempts = 1
	}
	if attempts > 7 {
		return time.Hour
	}
	return time.Duration(1<<(attempts-1)) * time.Minute
}

func clip(s string) string {
	if len(s) > 300 {
		return s[:300]
	}
	return s
}

func (w *Worker) process(ctx context.Context, j store.Job) error {
	if j.Kind != KindAuditTrail {
		return w.d.Store.CompleteJobSystem(ctx, j.ID, w.d.Now())
	}
	sub, signers, err := w.d.Store.GetSubmission(ctx, j.TenantID, j.SubmissionID)
	if errors.Is(err, repo.ErrNotFound) {
		return w.d.Store.CompleteJobSystem(ctx, j.ID, w.d.Now()) // deleted meanwhile
	}
	if err != nil {
		return err
	}
	if sub.Status != store.SubmissionCompleted || sub.FinalVersion == nil {
		return w.d.Store.CompleteJobSystem(ctx, j.ID, w.d.Now())
	}
	if sub.AuditTrailKey == "" {
		if sub, err = w.build(ctx, sub, signers); err != nil {
			return err
		}
	}
	if err := w.d.Store.CompleteJobSystem(ctx, j.ID, w.d.Now()); err != nil {
		return err
	}
	w.announce(ctx, sub, signers)
	return nil
}

// build renders, signs and stores the audit trail and records it.
func (w *Worker) build(ctx context.Context, sub store.Submission, signers []store.Signer) (store.Submission, error) {
	now := w.d.Now()
	in, err := w.input(ctx, sub, signers, now)
	if err != nil {
		return sub, err
	}
	doc, err := audittrail.Build(in)
	if err != nil {
		return sub, err
	}
	cert, key, err := w.d.PKI.SystemCertificate(ctx, sub.TenantID)
	if err != nil {
		return sub, err
	}
	chain, err := w.d.PKI.Chain(ctx, cert)
	if err != nil {
		return sub, err
	}
	signed, err := sign.Sign(doc, sign.Identity{Signer: key, Chain: chain}, sign.Options{
		Name: chain[0].Subject.CommonName, Reason: "Audit trail of " + sub.Name, Certify: true, Time: now})
	if err != nil {
		return sub, err
	}
	k := blob.AuditTrail(sub.TenantID, sub.ID)
	sum, err := w.d.Blob.Put(ctx, k, bytes.NewReader(signed), int64(len(signed)), "application/pdf")
	if err != nil {
		return sub, err
	}
	err = w.d.Store.Tx(ctx, sub.TenantID, func(tx repo.Store) error {
		cur, _, err := tx.LockSubmission(ctx, sub.TenantID, sub.ID)
		if err != nil {
			return err
		}
		cur.AuditTrailKey, cur.AuditTrailSHA256, cur.UpdatedAt = k, sum, now
		if err := tx.UpdateSubmission(ctx, cur); err != nil {
			return err
		}
		sub = cur
		return submissions.Event(ctx, tx, sub.TenantID, sub.ID, nil, "", "audit_trail.created", nil, now)
	})
	if err != nil {
		_ = w.d.Blob.Delete(context.WithoutCancel(ctx), k)
		return sub, err
	}
	return sub, nil
}

func (w *Worker) input(ctx context.Context, sub store.Submission, signers []store.Signer, now time.Time) (audittrail.Input, error) {
	v0, err := w.d.Store.GetVersion(ctx, sub.TenantID, sub.ID, 0)
	if err != nil {
		return audittrail.Input{}, err
	}
	final, err := w.d.Store.GetVersion(ctx, sub.TenantID, sub.ID, *sub.FinalVersion)
	if err != nil {
		return audittrail.Input{}, err
	}
	evs, err := w.d.Store.ListEvents(ctx, sub.TenantID, sub.ID)
	if err != nil {
		return audittrail.Input{}, err
	}
	names, bySigner := map[string]string{}, map[string]string{}
	for _, sg := range signers {
		names[sg.UserID], bySigner[sg.ID] = sg.Name, sg.Name
	}
	sender := sub.CreatedBy
	if w.d.Contacts != nil {
		if found, err := w.d.Contacts.Contacts(ctx, sub.TenantID, []string{sub.CreatedBy}); err == nil {
			if c, ok := found[sub.CreatedBy]; ok && c.DisplayName != "" {
				sender = c.DisplayName
			}
		}
	}
	names[sub.CreatedBy] = sender
	tplName := ""
	if t, err := w.d.Store.GetTemplate(ctx, sub.TenantID, sub.TemplateID); err == nil {
		tplName = t.Name
	}
	in := audittrail.Input{SubmissionID: sub.ID, Name: sub.Name, TemplateName: tplName, Sender: sender, CreatedAt: sub.CreatedAt,
		OriginalSHA256: v0.SHA256, FinalSHA256: final.SHA256, FinalVersion: final.Version, GeneratedAt: now}
	if sub.CompletedAt != nil {
		in.CompletedAt = *sub.CompletedAt
	}
	for _, sg := range signers {
		in.Signers = append(in.Signers, audittrail.Signer{Name: sg.Name, Email: sg.Email, Party: partyName(sub, sg.Party),
			Status: sg.Status, Method: sg.Method, CertSubject: sg.CertSubject, CertSerial: sg.CertSerial, CertIssuer: sg.CertIssuer,
			IP: sg.IP, UserAgent: sg.UserAgent, SignedAt: sg.SignedAt, DeclinedAt: sg.DeclinedAt, DeclineReason: sg.DeclineReason})
	}
	for _, e := range evs {
		ev := audittrail.Event{At: e.At, Type: e.Type}
		if e.ActorUserID != nil {
			ev.Actor = *e.ActorUserID
			if n, ok := names[*e.ActorUserID]; ok {
				ev.Actor = n
			}
		}
		if e.SignerID != nil {
			ev.Signer = bySigner[*e.SignerID]
		}
		in.Events = append(in.Events, ev)
	}
	return in, nil
}

func partyName(sub store.Submission, key string) string {
	for _, p := range sub.Parties {
		if p.Key == key {
			return p.Name
		}
	}
	return key
}

// announce sends the completion notices (after the audit trail exists).
func (w *Worker) announce(ctx context.Context, sub store.Submission, signers []store.Signer) {
	w.d.Subs.Broadcast(ctx, sub, signers, mail.Completed, map[string]string{"document": sub.Name}, "")
	for _, sg := range signers {
		w.d.Events.InboxChanged(ctx, sub.TenantID, sg.UserID, events.InboxPayload{SignerID: sg.ID, SubmissionID: sub.ID, State: "completed"})
	}
	w.d.Events.Completed(ctx, sub.TenantID, events.CompletedPayload{SubmissionID: sub.ID, TemplateID: sub.TemplateID,
		FinalVersion: *sub.FinalVersion, AuditTrail: sub.AuditTrailKey != ""})
}
