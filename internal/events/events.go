// Package events publishes signing state changes to the shared platform event
// bus (platform:events:<tenant>, contracts/cross-module.md): completed,
// cancelled and expired submissions for other modules and the UI, and a
// per-user "signing.inbox" event that refreshes a signer's To-sign list.
// Payloads carry ids and status codes only — never field values, names,
// e-mail addresses, reasons or document content (SR-007).
package events

import (
	"context"
	"sync"

	"github.com/go-tangra/go-tangra-signing/v4/internal/stream"
)

// Event types published to platform:events:<tenant>.
const (
	SubmissionCompleted = "signing.submission.completed"
	SubmissionCancelled = "signing.submission.cancelled"
	SubmissionExpired   = "signing.submission.expired"
	Inbox               = "signing.inbox"
)

// Types lists every event type the module publishes.
var Types = []string{SubmissionCompleted, SubmissionCancelled, SubmissionExpired, Inbox}

// Cancel reason codes of signing.submission.cancelled.
const (
	ReasonCancelled = "cancelled"
	ReasonDeclined  = "declined"
)

// CompletedPayload is the body of signing.submission.completed.
type CompletedPayload struct {
	SubmissionID string `json:"submission_id"`
	TemplateID   string `json:"template_id"`
	FinalVersion int    `json:"final_version"`
	AuditTrail   bool   `json:"audit_trail"`
}

// CancelledPayload is the body of signing.submission.cancelled.
type CancelledPayload struct {
	SubmissionID string `json:"submission_id"`
	TemplateID   string `json:"template_id"`
	ReasonCode   string `json:"reason_code"`
}

// ExpiredPayload is the body of signing.submission.expired.
type ExpiredPayload struct {
	SubmissionID string `json:"submission_id"`
	TemplateID   string `json:"template_id"`
}

// InboxPayload is the body of signing.inbox (sent to the signer only).
type InboxPayload struct {
	SignerID     string `json:"signer_id"`
	SubmissionID string `json:"submission_id"`
	State        string `json:"state"`
}

// Publisher emits an event to a tenant's stream, to every subscriber (users
// nil) or to the listed user ids only.
type Publisher interface {
	Publish(ctx context.Context, tenantID string, users []string, eventType string, payload any)
}

// HubPublisher publishes through the stream hub (nil hub is a no-op).
type HubPublisher struct{ Hub *stream.Hub }

// Publish is best effort and never fails the caller's operation.
func (p HubPublisher) Publish(ctx context.Context, tenantID string, users []string, eventType string, payload any) {
	if p.Hub == nil || tenantID == "" {
		return
	}
	_, _ = p.Hub.PublishID(ctx, tenantID, users, len(users) == 0, eventType, payload, true)
}

// Emitter maps signing changes to events. The zero value (nil Pub) is a no-op.
type Emitter struct{ Pub Publisher }

// Completed publishes signing.submission.completed.
func (e Emitter) Completed(ctx context.Context, tenantID string, p CompletedPayload) {
	if e.Pub != nil {
		e.Pub.Publish(ctx, tenantID, nil, SubmissionCompleted, p)
	}
}

// Cancelled publishes signing.submission.cancelled.
func (e Emitter) Cancelled(ctx context.Context, tenantID string, p CancelledPayload) {
	if e.Pub != nil {
		e.Pub.Publish(ctx, tenantID, nil, SubmissionCancelled, p)
	}
}

// Expired publishes signing.submission.expired.
func (e Emitter) Expired(ctx context.Context, tenantID string, p ExpiredPayload) {
	if e.Pub != nil {
		e.Pub.Publish(ctx, tenantID, nil, SubmissionExpired, p)
	}
}

// InboxChanged tells one signer that their To-sign list changed.
func (e Emitter) InboxChanged(ctx context.Context, tenantID, userID string, p InboxPayload) {
	if e.Pub != nil && userID != "" {
		e.Pub.Publish(ctx, tenantID, []string{userID}, Inbox, p)
	}
}

// Recorded is one captured event (Recorder).
type Recorded struct {
	TenantID string
	Users    []string
	Type     string
	Payload  any
}

// Recorder is an in-memory Publisher for tests.
type Recorder struct {
	mu     sync.Mutex
	events []Recorded
}

// Publish implements Publisher.
func (r *Recorder) Publish(_ context.Context, tenantID string, users []string, eventType string, p any) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.events = append(r.events, Recorded{TenantID: tenantID, Users: append([]string(nil), users...), Type: eventType, Payload: p})
}

// Events returns a copy of the captured events.
func (r *Recorder) Events() []Recorded {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]Recorded(nil), r.events...)
}

// OfType returns the captured events of one type.
func (r *Recorder) OfType(t string) []Recorded {
	var out []Recorded
	for _, e := range r.Events() {
		if e.Type == t {
			out = append(out, e)
		}
	}
	return out
}

var (
	_ Publisher = HubPublisher{}
	_ Publisher = (*Recorder)(nil)
)
