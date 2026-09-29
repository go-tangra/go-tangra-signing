// Package tasks holds the scheduled task types signing executes for the
// scheduler module (research D12 — no hidden timers in the service):
//
//   - signing:expire-submissions expires in-progress submissions past their
//     expiry, exactly once each, and tells the participants;
//   - signing:send-reminders e-mails due reminders (each claimed exactly once)
//     and runs the housekeeping sweeps: used or expired QES preparations,
//     objects of deleted submissions, and CRLs due for re-publication.
//
// Both are platform-scoped: they run without a tenant and work across tenants
// under the system scope. The SDK executor server verifies that the caller is
// the scheduler; the handlers refuse a tenant-scoped request, never log
// payloads or addresses, and answer Retry on storage outages.
package tasks

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/go-tangra/go-tangra-scheduler/sdk/v4/pkg/schedulerclient"
	sdk "github.com/go-tangra/go-tangra-scheduler/sdk/v4/pkg/taskexec"

	"github.com/go-tangra/go-tangra-signing/v4/internal/audit"
	"github.com/go-tangra/go-tangra-signing/v4/internal/blob"
	"github.com/go-tangra/go-tangra-signing/v4/internal/events"
	"github.com/go-tangra/go-tangra-signing/v4/internal/mail"
	"github.com/go-tangra/go-tangra-signing/v4/internal/pki"
	"github.com/go-tangra/go-tangra-signing/v4/internal/repo"
	"github.com/go-tangra/go-tangra-signing/v4/internal/store"
	"github.com/go-tangra/go-tangra-signing/v4/internal/submissions"
)

// Task types.
const (
	TypeExpire    = "signing:expire-submissions"
	TypeReminders = "signing:send-reminders"
)

// Bounds.
const (
	DefaultBatch = 200
	MaxBatch     = 1000
	maxRounds    = 20 // batches per run (bounded work per execution)
	sweepKeys    = 1000
	// UploadRetention matches documents.Retention (signed documents).
	UploadRetention = time.Hour
)

const (
	expireSchema    = `{"type":"object","properties":{},"additionalProperties":false}`
	remindersSchema = `{"type":"object","properties":{"batch":{"type":"integer","minimum":1,"maximum":1000,"default":200,` +
		`"description":"Reminders sent per batch (at most 20 batches per run)."}},"additionalProperties":false}`
)

// Descriptors are the task types signing registers with the scheduler.
func Descriptors() []schedulerclient.Descriptor {
	return []schedulerclient.Descriptor{
		{Type: TypeExpire, DisplayName: "Expire signing submissions",
			Description:   "Marks in-progress submissions past their expiry date as expired and informs the sender and invited signers.",
			PayloadSchema: expireSchema, DefaultCron: "*/15 * * * *", DefaultMaxRetry: 1, Platform: true},
		{Type: TypeReminders, DisplayName: "Send signing reminders",
			Description: "E-mails due signing reminders and runs the signing housekeeping (expired card-signature preparations, " +
				"objects of deleted submissions, CRL re-publication).",
			PayloadSchema: remindersSchema, DefaultCron: "0 * * * *", DefaultMaxRetry: 1, Platform: true},
	}
}

// Platform lists the platform-scoped types for the SDK server.
func Platform() map[string]bool { return map[string]bool{TypeExpire: true, TypeReminders: true} }

// Runner executes the task types.
type Runner struct {
	Store  repo.Store
	Blob   blob.Store
	PKI    *pki.PKI
	Subs   *submissions.Service
	Mail   mail.Mailer
	Events events.Emitter
	Audit  audit.Recorder
	Log    *slog.Logger
	Now    func() time.Time
}

func (r *Runner) now() time.Time {
	if r.Now != nil {
		return r.Now()
	}
	return time.Now().UTC()
}

func (r *Runner) warn(ctx context.Context, msg string, args ...any) {
	if r.Log != nil {
		r.Log.WarnContext(ctx, msg, args...)
	}
}

// Handlers maps the task types to their handlers for sdk.NewServer.
func (r *Runner) Handlers() map[string]sdk.Handler {
	return map[string]sdk.Handler{TypeExpire: r.Expire, TypeReminders: r.Reminders}
}

func (r *Runner) record(ctx context.Context, t audit.EventType, tenant, kind, id string, detail map[string]any) {
	audit.Emit(ctx, r.Audit, audit.Event{TenantID: tenant, EventType: t, ActorKind: audit.ActorSystem, ActorID: "scheduler",
		SubjectKind: kind, SubjectID: id, Outcome: audit.OutcomeOK, Details: detail})
}

// Expire runs signing:expire-submissions.
func (r *Runner) Expire(ctx context.Context, req sdk.Request) sdk.Result {
	if req.TenantID != "" {
		return sdk.Permanent("signing:expire-submissions is platform-scoped")
	}
	var p struct{}
	if err := sdk.DecodeStrict(req.Payload, &p); err != nil {
		return sdk.Permanent("invalid payload: " + err.Error())
	}
	total := 0
	for round := 0; round < maxRounds; round++ {
		now := r.now()
		subs, err := r.Store.ExpireDueSystem(ctx, now, DefaultBatch)
		if err != nil {
			return sdk.Retry(fmt.Sprintf("expired %d; storage unavailable", total))
		}
		for _, sub := range subs {
			r.expired(ctx, sub, now)
		}
		total += len(subs)
		if len(subs) < DefaultBatch {
			break
		}
	}
	return sdk.OK(fmt.Sprintf("expired %d", total))
}

// expired records and announces one expired submission (best effort: the
// state change is already committed).
func (r *Runner) expired(ctx context.Context, sub store.Submission, now time.Time) {
	var signers []store.Signer
	err := r.Store.Tx(ctx, sub.TenantID, func(tx repo.Store) error {
		var err error
		if _, signers, err = tx.GetSubmission(ctx, sub.TenantID, sub.ID); err != nil {
			return err
		}
		return submissions.Event(ctx, tx, sub.TenantID, sub.ID, nil, "", "submission.expired", nil, now)
	})
	if err != nil {
		r.warn(ctx, "expired submission history", "submission", sub.ID, "err", err)
	}
	r.record(ctx, audit.TaskExpire, sub.TenantID, audit.SubjectSubmission, sub.ID, nil)
	r.Subs.Ended(ctx, sub, signers, mail.Expired, map[string]string{"document": sub.Name}, "")
	r.Events.Expired(ctx, sub.TenantID, events.ExpiredPayload{SubmissionID: sub.ID, TemplateID: sub.TemplateID})
}

// Reminders runs signing:send-reminders.
func (r *Runner) Reminders(ctx context.Context, req sdk.Request) sdk.Result {
	if req.TenantID != "" {
		return sdk.Permanent("signing:send-reminders is platform-scoped")
	}
	var p struct {
		Batch int `json:"batch"`
	}
	if err := sdk.DecodeStrict(req.Payload, &p); err != nil {
		return sdk.Permanent("invalid payload: " + err.Error())
	}
	if p.Batch == 0 {
		p.Batch = DefaultBatch
	}
	if p.Batch < 1 || p.Batch > MaxBatch {
		return sdk.Permanent(fmt.Sprintf("batch must be within 1..%d", MaxBatch))
	}
	reminded := 0
	for round := 0; round < maxRounds; round++ {
		due, err := r.Store.ClaimRemindersSystem(ctx, r.now(), p.Batch)
		if err != nil {
			return sdk.Retry(fmt.Sprintf("reminded %d; storage unavailable", reminded))
		}
		for _, d := range due {
			r.remind(ctx, d)
		}
		reminded += len(due)
		if len(due) < p.Batch {
			break
		}
	}
	crl, swept, err := r.sweep(ctx)
	msg := fmt.Sprintf("reminded %d, crl %d, swept %d", reminded, crl, swept)
	if err != nil {
		return sdk.Retry(msg + "; sweep incomplete")
	}
	return sdk.OK(msg)
}

func (r *Runner) remind(ctx context.Context, d repo.ReminderDue) {
	sub, sg := d.Submission, d.Signer
	vars := map[string]string{"document": sub.Name, "sender": r.Subs.SenderName(ctx, sub), "signer": sg.Name,
		"link": r.Mail.SignLink(sg.ID), "reminder_no": fmt.Sprintf("%d", d.Number)}
	r.Subs.Mailed(ctx, sub, sg, mail.Reminder, r.Mail.Send(ctx, sub.TenantID, mail.Reminder, sg.Email, vars, sub.ID))
	id := sg.ID
	if err := r.Store.Tx(ctx, sub.TenantID, func(tx repo.Store) error {
		return submissions.Event(ctx, tx, sub.TenantID, sub.ID, &id, "", "signer.reminded", map[string]any{"number": d.Number}, r.now())
	}); err != nil {
		r.warn(ctx, "reminder history", "submission", sub.ID, "err", err)
	}
	r.record(ctx, audit.TaskRemind, sub.TenantID, audit.SubjectSigner, sg.ID, map[string]any{"number": d.Number})
}

// sweep runs the housekeeping: QES preparations, objects of deleted
// submissions, due CRLs.
func (r *Runner) sweep(ctx context.Context) (crl, swept int, err error) {
	now := r.now()
	var errs []error
	preps, qerr := r.Store.ExpiredQESSystem(ctx, now, DefaultBatch)
	if qerr != nil {
		errs = append(errs, qerr)
	}
	for _, q := range preps {
		if q.PreparedKey != "" {
			if err := r.Blob.Delete(ctx, q.PreparedKey); err != nil {
				errs = append(errs, err)
				continue
			}
		}
		if err := r.Store.DeleteQESSystem(ctx, q.ID); err != nil {
			errs = append(errs, err)
			continue
		}
		swept++
	}
	n, oerr := r.orphans(ctx, now)
	swept += n
	if oerr != nil {
		errs = append(errs, oerr)
	}
	cas, cerr := r.Store.DueCRLsSystem(ctx, now, DefaultBatch)
	if cerr != nil {
		errs = append(errs, cerr)
	}
	for _, ca := range cas {
		if err := r.PKI.PublishCRL(ctx, ca.TenantID, ca.ID); err != nil {
			errs = append(errs, err)
			continue
		}
		crl++
	}
	return crl, swept, errors.Join(errs...)
}

// orphans deletes expired administrator-signed documents and objects under
// submissions that no longer exist (a delete whose object cleanup was
// interrupted). Objects of live submissions are
// never touched: an in-flight signature may be writing one right now.
func (r *Runner) orphans(ctx context.Context, now time.Time) (int, error) {
	tenants, err := r.Store.TenantsSystem(ctx)
	if err != nil {
		return 0, err
	}
	n := 0
	var errs []error
	for _, t := range tenants {
		// Signed documents of administrator signing are kept one hour.
		uploads := blob.TenantPrefix(t) + "uploads/"
		if keys, err := r.Blob.List(ctx, uploads, sweepKeys); err != nil {
			errs = append(errs, err)
		} else {
			for _, k := range keys {
				id := strings.TrimSuffix(strings.TrimPrefix(k, uploads), ".pdf")
				if created, ok := store.IDTime(id); ok && now.Sub(created) <= UploadRetention {
					continue
				}
				if err := r.Blob.Delete(ctx, k); err != nil {
					errs = append(errs, err)
					continue
				}
				n++
			}
		}
		prefix := blob.TenantPrefix(t) + "submissions/"
		keys, err := r.Blob.List(ctx, prefix, sweepKeys)
		if err != nil {
			errs = append(errs, err)
			continue
		}
		gone := map[string]bool{}
		for _, k := range keys {
			id, _, ok := strings.Cut(strings.TrimPrefix(k, prefix), "/")
			if !ok || id == "" {
				continue
			}
			missing, seen := gone[id]
			if !seen {
				_, _, err := r.Store.GetSubmission(ctx, t, id)
				missing = errors.Is(err, repo.ErrNotFound)
				if err != nil && !missing {
					errs = append(errs, err)
					continue
				}
				gone[id] = missing
			}
			if missing {
				if err := r.Blob.Delete(ctx, k); err != nil {
					errs = append(errs, err)
					continue
				}
				n++
			}
		}
	}
	return n, errors.Join(errs...)
}
