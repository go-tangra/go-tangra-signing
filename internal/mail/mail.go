// Package mail sends the signing e-mails through the notification module's
// system templates (research D13): one SendKey per recipient over the
// tenant's e-mail channel, variables limited to names, links and reasons —
// never field values. Mail is sent after the database work has committed; a
// failure is returned as a short reason the caller records on the signer
// (mail_error) and in the history, and never fails the signing action.
package mail

import (
	"context"
	"log/slog"
	"strings"

	"github.com/go-tangra/go-tangra-notification/sdk/v4/pkg/notifyclient"
)

// System template keys (contracts/cross-module.md).
const (
	Invitation         = "signing.invitation"
	NextSigner         = "signing.next_signer"
	CertificateSetup   = "signing.certificate_setup"
	Reminder           = "signing.reminder"
	Completed          = "signing.completed"
	Declined           = "signing.declined"
	Cancelled          = "signing.cancelled"
	Expired            = "signing.expired"
	CertificateLocked  = "signing.certificate_locked"
	maxReasonLength    = 200
	unreachableMessage = "notification unreachable"
)

// Sender is the notification client (notifyclient.Client or a lazy dialer).
type Sender interface {
	SendKey(ctx context.Context, tenantID, key, recipient string, vars map[string]string, correlationID string) (notifyclient.Result, error)
}

// Mailer builds links and sends.
type Mailer struct {
	Sender        Sender
	PortalBaseURL string // https://portal.example.org:8443
	Log           *slog.Logger
}

// Links into the portal (the signing remote's routes).
func (m Mailer) base() string { return strings.TrimRight(m.PortalBaseURL, "/") }

// SignLink opens a signer's page.
func (m Mailer) SignLink(signerID string) string { return m.base() + "/signing/sign/" + signerID }

// SubmissionLink opens a submission's detail page.
func (m Mailer) SubmissionLink(id string) string { return m.base() + "/signing/submissions/" + id }

// InboxLink opens the "To sign" list.
func (m Mailer) InboxLink() string { return m.base() + "/signing" }

// CertificateLink opens "My signing certificate".
func (m Mailer) CertificateLink() string { return m.base() + "/signing/certificate" }

// Send delivers one e-mail. It returns "" when sent, otherwise a short
// reason to record (the caller decides whether to retry). A recipient
// without an address is a failure too ("no e-mail address").
func (m Mailer) Send(ctx context.Context, tenantID, key, to string, vars map[string]string, correlationID string) string {
	if strings.TrimSpace(to) == "" {
		return "no e-mail address"
	}
	if m.Sender == nil {
		return unreachableMessage
	}
	res, err := m.Sender.SendKey(ctx, tenantID, key, to, vars, correlationID)
	switch {
	case err != nil:
		m.warn(ctx, key, err.Error())
		return clip(err.Error())
	case res.Sent:
		return ""
	default:
		reason := res.Reason
		if reason == "" {
			reason = "not delivered"
		}
		m.warn(ctx, key, reason)
		return clip(reason)
	}
}

func (m Mailer) warn(ctx context.Context, key, reason string) {
	if m.Log != nil {
		m.Log.WarnContext(ctx, "signing mail not sent", "key", key, "reason", clip(reason))
	}
}

func clip(s string) string {
	s = strings.TrimSpace(s)
	if r := []rune(s); len(r) > maxReasonLength {
		return string(r[:maxReasonLength])
	}
	return s
}
