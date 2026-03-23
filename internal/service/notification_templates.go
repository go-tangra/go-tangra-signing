package service

import (
	"context"
	"fmt"
	"sync"

	"github.com/go-kratos/kratos/v2/log"

	"github.com/go-tangra/go-tangra-signing/internal/client"

	notificationv1 "buf.build/gen/go/go-tangra/notification/protocolbuffers/go/notification/service/v1"
)

// Template names registered in the notification service.
const (
	templateNameInvitation = "signing-invitation-template"
	templateNameCompleted  = "signing-completed-template"
	templateNameDeclined   = "signing-declined-template"
	templateNameNextSigner = "signing-next-signer-template"
	channelName            = "Default SMTP"
)

// --- Invitation template ---

var invitationSubject = `{{.SenderName}} has requested your signature on "{{.TemplateName}}"`

var invitationBody = `<!DOCTYPE html>
<html>
<body style="font-family: Arial, sans-serif; max-width: 600px; margin: 0 auto;">
  <h2>Document Signing Request</h2>
  <p>Hello {{.SignerName}},</p>
  <p><strong>{{.SenderName}}</strong> has requested your signature on the document <strong>"{{.TemplateName}}"</strong>.</p>
  {{if .Message}}<p>Message: {{.Message}}</p>{{end}}
  <p>Your role: <strong>{{.Role}}</strong></p>
  <p style="margin: 24px 0;">
    <a href="{{.SigningLink}}" style="background: #1677ff; color: white; padding: 12px 24px; text-decoration: none; border-radius: 6px; display: inline-block;">
      Review & Sign Document
    </a>
  </p>
  <p style="color: #666; font-size: 12px;">
    If the button doesn't work, copy and paste this link: {{.SigningLink}}
  </p>
  <hr style="border: none; border-top: 1px solid #eee; margin: 24px 0;">
  <p style="color: #999; font-size: 11px;">This is an automated message from GoTangra Document Signing.</p>
</body>
</html>`

// --- Completed template (all signers done) ---

var completedSubject = `"{{.TemplateName}}" has been fully signed`

var completedBody = `<!DOCTYPE html>
<html>
<body style="font-family: Arial, sans-serif; max-width: 600px; margin: 0 auto;">
  <h2>Document Fully Signed</h2>
  <p>Hello {{.RecipientName}},</p>
  <p>All participants have completed signing the document <strong>"{{.TemplateName}}"</strong>.</p>
  <p>The signed document is now available for download in your dashboard.</p>
  <p style="margin: 24px 0;">
    <a href="{{.DashboardLink}}" style="background: #52c41a; color: white; padding: 12px 24px; text-decoration: none; border-radius: 6px; display: inline-block;">
      View Signed Document
    </a>
  </p>
  <hr style="border: none; border-top: 1px solid #eee; margin: 24px 0;">
  <p style="color: #999; font-size: 11px;">This is an automated message from GoTangra Document Signing.</p>
</body>
</html>`

// --- Declined template ---

var declinedSubject = `Signing declined on "{{.TemplateName}}"`

var declinedBody = `<!DOCTYPE html>
<html>
<body style="font-family: Arial, sans-serif; max-width: 600px; margin: 0 auto;">
  <h2>Signing Declined</h2>
  <p>Hello {{.RecipientName}},</p>
  <p><strong>{{.DeclinerName}}</strong> has declined to sign the document <strong>"{{.TemplateName}}"</strong>.</p>
  {{if .Reason}}<p>Reason: {{.Reason}}</p>{{end}}
  <p>The submission has been cancelled. You may create a new submission if needed.</p>
  <hr style="border: none; border-top: 1px solid #eee; margin: 24px 0;">
  <p style="color: #999; font-size: 11px;">This is an automated message from GoTangra Document Signing.</p>
</body>
</html>`

// --- Next signer template (sequential workflow advancement) ---

var nextSignerSubject = `It's your turn to sign "{{.TemplateName}}"`

var nextSignerBody = `<!DOCTYPE html>
<html>
<body style="font-family: Arial, sans-serif; max-width: 600px; margin: 0 auto;">
  <h2>Your Turn to Sign</h2>
  <p>Hello {{.SignerName}},</p>
  <p>The previous signer has completed their part. It's now your turn to sign the document <strong>"{{.TemplateName}}"</strong>.</p>
  <p>Your role: <strong>{{.Role}}</strong></p>
  <p style="margin: 24px 0;">
    <a href="{{.SigningLink}}" style="background: #1677ff; color: white; padding: 12px 24px; text-decoration: none; border-radius: 6px; display: inline-block;">
      Review & Sign Document
    </a>
  </p>
  <p style="color: #666; font-size: 12px;">
    If the button doesn't work, copy and paste this link: {{.SigningLink}}
  </p>
  <hr style="border: none; border-top: 1px solid #eee; margin: 24px 0;">
  <p style="color: #999; font-size: 11px;">This is an automated message from GoTangra Document Signing.</p>
</body>
</html>`

// templateDef defines a notification template to be lazily registered.
type templateDef struct {
	name      string
	subject   string
	body      string
	variables string
}

var templateDefs = map[string]templateDef{
	templateNameInvitation: {
		name:      templateNameInvitation,
		subject:   invitationSubject,
		body:      invitationBody,
		variables: "SenderName,SignerName,TemplateName,Message,Role,SigningLink",
	},
	templateNameCompleted: {
		name:      templateNameCompleted,
		subject:   completedSubject,
		body:      completedBody,
		variables: "RecipientName,TemplateName,DashboardLink",
	},
	templateNameDeclined: {
		name:      templateNameDeclined,
		subject:   declinedSubject,
		body:      declinedBody,
		variables: "RecipientName,DeclinerName,TemplateName,Reason",
	},
	templateNameNextSigner: {
		name:      templateNameNextSigner,
		subject:   nextSignerSubject,
		body:      nextSignerBody,
		variables: "SignerName,TemplateName,Role,SigningLink",
	},
}

// NotificationHelper manages lazy registration and sending of notification templates.
// It resolves emails for local users via the AdminClient when a submitter has a name
// but no email address.
type NotificationHelper struct {
	log                *log.Helper
	notificationClient *client.NotificationClient
	adminClient        *client.AdminClient
	appHost            string

	mu          sync.Mutex
	templateIDs map[string]string // template name -> resolved ID

	// Cached user email map (username -> email) from admin service
	userEmailMu    sync.RWMutex
	userEmailCache map[string]string
}

// NewNotificationHelper creates a NotificationHelper.
func NewNotificationHelper(log *log.Helper, notificationClient *client.NotificationClient, adminClient *client.AdminClient, appHost string) *NotificationHelper {
	return &NotificationHelper{
		log:                log,
		notificationClient: notificationClient,
		adminClient:        adminClient,
		appHost:            appHost,
		templateIDs:        make(map[string]string),
		userEmailCache:     make(map[string]string),
	}
}

// resolveEmail returns the email for a submitter. If the email is empty but the name
// matches a local user, it resolves the email from the admin service.
func (h *NotificationHelper) resolveEmail(ctx context.Context, name, email string) string {
	if email != "" {
		return email
	}

	// Try to resolve from cached user list
	if resolved := h.lookupCachedEmail(name); resolved != "" {
		return resolved
	}

	// Refresh cache from admin service and retry
	h.refreshUserEmailCache(ctx)
	return h.lookupCachedEmail(name)
}

// lookupCachedEmail checks the local cache for a user email by name/username.
func (h *NotificationHelper) lookupCachedEmail(name string) string {
	h.userEmailMu.RLock()
	defer h.userEmailMu.RUnlock()
	if email, ok := h.userEmailCache[name]; ok {
		return email
	}
	return ""
}

// refreshUserEmailCache fetches users from admin-service and caches name→email mappings.
func (h *NotificationHelper) refreshUserEmailCache(ctx context.Context) {
	if h.adminClient == nil {
		return
	}

	platformCtx := client.DetachedMetadataContext(ctx, 0)
	resp, err := h.adminClient.ListUsers(platformCtx)
	if err != nil {
		h.log.Warnf("Failed to refresh user email cache: %v", err)
		return
	}

	h.userEmailMu.Lock()
	defer h.userEmailMu.Unlock()
	for _, user := range resp.GetItems() {
		if user.GetEmail() != "" {
			h.userEmailCache[user.GetUsername()] = user.GetEmail()
			// Also cache by display name for fuzzy matching
			if user.GetUsername() != "" {
				h.userEmailCache[user.GetUsername()] = user.GetEmail()
			}
		}
	}
	h.log.Infof("Refreshed user email cache: %d entries", len(h.userEmailCache))
}

// EnsureTemplate resolves (or creates) a notification template by name.
// Uses platform admin context (tenant 0) because templates are platform-level resources.
func (h *NotificationHelper) EnsureTemplate(ctx context.Context, templateName string) (string, error) {
	h.mu.Lock()
	defer h.mu.Unlock()

	// Return cached ID if already resolved
	if id, ok := h.templateIDs[templateName]; ok {
		return id, nil
	}

	def, ok := templateDefs[templateName]
	if !ok {
		return "", fmt.Errorf("unknown template name: %s", templateName)
	}

	h.log.Infof("Resolving notification template %q...", templateName)

	platformCtx := client.DetachedMetadataContext(ctx, 0)

	// Search for existing template
	tmpl, err := h.notificationClient.FindTemplateByName(platformCtx, templateName)
	if err != nil {
		return "", fmt.Errorf("search notification template: %w", err)
	}
	if tmpl != nil {
		h.templateIDs[templateName] = tmpl.GetId()
		h.log.Infof("Found existing notification template %q: %s", templateName, tmpl.GetId())
		return tmpl.GetId(), nil
	}

	// Template not found — find the channel and create
	channelID, err := h.notificationClient.FindChannelByName(platformCtx, channelName)
	if err != nil {
		return "", fmt.Errorf("find channel %q: %w", channelName, err)
	}

	created, err := h.notificationClient.CreateTemplate(platformCtx, &notificationv1.CreateTemplateRequest{
		Name:      def.name,
		ChannelId: channelID,
		Subject:   def.subject,
		Body:      def.body,
		Variables: def.variables,
		IsDefault: false,
	})
	if err != nil {
		return "", fmt.Errorf("create notification template: %w", err)
	}

	h.templateIDs[templateName] = created.GetId()
	h.log.Infof("Created notification template %q: %s", templateName, created.GetId())
	return created.GetId(), nil
}

// SendInvitation sends a signing invitation email.
func (h *NotificationHelper) SendInvitation(ctx context.Context, signerName, signerEmail, slug, role, templateName, senderName, message string) error {
	recipient := h.resolveEmail(ctx, signerName, signerEmail)
	if recipient == "" {
		h.log.Warnf("Cannot send invitation to %q: no email address", signerName)
		return nil
	}

	templateID, err := h.EnsureTemplate(ctx, templateNameInvitation)
	if err != nil {
		return err
	}

	signingLink := fmt.Sprintf("%s/#/signing/session/%s", h.appHost, slug)

	// Use platform admin context (tenant 0) because signing templates are
	// platform-level resources and the authz engine requires matching tenant.
	platformCtx := client.DetachedMetadataContext(ctx, 0)
	_, err = h.notificationClient.SendNotification(platformCtx, templateID, recipient, map[string]string{
		"SenderName":   senderName,
		"SignerName":   signerName,
		"TemplateName": templateName,
		"Message":      message,
		"Role":         role,
		"SigningLink":  signingLink,
	})
	return err
}

// SendNextSignerNotification sends a "your turn" email to the next signer in a sequential workflow.
func (h *NotificationHelper) SendNextSignerNotification(ctx context.Context, signerName, signerEmail, slug, role, templateName string) error {
	recipient := h.resolveEmail(ctx, signerName, signerEmail)
	if recipient == "" {
		h.log.Warnf("Cannot send next-signer notification to %q: no email address", signerName)
		return nil
	}

	templateID, err := h.EnsureTemplate(ctx, templateNameNextSigner)
	if err != nil {
		return err
	}

	signingLink := fmt.Sprintf("%s/#/signing/session/%s", h.appHost, slug)

	// Use platform admin context (tenant 0) because signing templates are
	// platform-level resources and the authz engine requires matching tenant.
	platformCtx := client.DetachedMetadataContext(ctx, 0)
	_, err = h.notificationClient.SendNotification(platformCtx, templateID, recipient, map[string]string{
		"SignerName":   signerName,
		"TemplateName": templateName,
		"Role":         role,
		"SigningLink":  signingLink,
	})
	return err
}

// SendCompletionNotification sends a "fully signed" email to a participant.
func (h *NotificationHelper) SendCompletionNotification(ctx context.Context, recipientName, recipientEmail, templateName, submissionID string) error {
	recipient := h.resolveEmail(ctx, recipientName, recipientEmail)
	if recipient == "" {
		h.log.Warnf("Cannot send completion notification to %q: no email address", recipientName)
		return nil
	}

	templateID, err := h.EnsureTemplate(ctx, templateNameCompleted)
	if err != nil {
		return err
	}

	dashboardLink := fmt.Sprintf("%s/#/signing/submissions/%s", h.appHost, submissionID)

	// Use platform admin context (tenant 0) because signing templates are
	// platform-level resources and the authz engine requires matching tenant.
	platformCtx := client.DetachedMetadataContext(ctx, 0)
	_, err = h.notificationClient.SendNotification(platformCtx, templateID, recipient, map[string]string{
		"RecipientName": recipientName,
		"TemplateName":  templateName,
		"DashboardLink": dashboardLink,
	})
	return err
}

// SendDeclineNotification sends a "signing declined" email to a participant.
func (h *NotificationHelper) SendDeclineNotification(ctx context.Context, recipientName, recipientEmail, declinerName, templateName, reason string) error {
	recipient := h.resolveEmail(ctx, recipientName, recipientEmail)
	if recipient == "" {
		h.log.Warnf("Cannot send decline notification to %q: no email address", recipientName)
		return nil
	}

	templateID, err := h.EnsureTemplate(ctx, templateNameDeclined)
	if err != nil {
		return err
	}

	// Use platform admin context (tenant 0) because signing templates are
	// platform-level resources and the authz engine requires matching tenant.
	platformCtx := client.DetachedMetadataContext(ctx, 0)
	_, err = h.notificationClient.SendNotification(platformCtx, templateID, recipient, map[string]string{
		"RecipientName": recipientName,
		"DeclinerName":  declinerName,
		"TemplateName":  templateName,
		"Reason":        reason,
	})
	return err
}
