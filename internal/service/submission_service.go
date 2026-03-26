package service

import (
	"context"
	"encoding/json"
	"os"
	"time"

	"github.com/go-kratos/kratos/v2/log"
	"github.com/tx7do/kratos-bootstrap/bootstrap"

	"github.com/go-tangra/go-tangra-signing/internal/client"
	"github.com/go-tangra/go-tangra-signing/internal/data"
	"github.com/go-tangra/go-tangra-signing/internal/event"

	signingV1 "github.com/go-tangra/go-tangra-signing/gen/go/signing/service/v1"
)

type SubmissionService struct {
	signingV1.UnimplementedSigningSubmissionServiceServer

	log            *log.Helper
	submissionRepo *data.SubmissionRepo
	submitterRepo  *data.SubmitterRepo
	templateRepo   *data.TemplateRepo
	eventRepo      *data.EventRepo
	certRepo       *data.CertificateRepo
	storage        *data.StorageClient
	notifHelper    *NotificationHelper
	publisher      *event.Publisher
}

func NewSubmissionService(
	ctx *bootstrap.Context,
	submissionRepo *data.SubmissionRepo,
	submitterRepo *data.SubmitterRepo,
	templateRepo *data.TemplateRepo,
	eventRepo *data.EventRepo,
	certRepo *data.CertificateRepo,
	storage *data.StorageClient,
	notificationClient *client.NotificationClient,
	adminClient *client.AdminClient,
	publisher *event.Publisher,
) *SubmissionService {
	l := ctx.NewLoggerHelper("signing/service/submission")

	appHost := os.Getenv("APP_HOST")
	if appHost == "" {
		appHost = "http://localhost:8080"
	}

	var notifHelper *NotificationHelper
	if notificationClient != nil {
		notifHelper = NewNotificationHelper(l, notificationClient, adminClient, appHost)
	}

	return &SubmissionService{
		log:            l,
		submissionRepo: submissionRepo,
		submitterRepo:  submitterRepo,
		templateRepo:   templateRepo,
		eventRepo:      eventRepo,
		certRepo:       certRepo,
		storage:        storage,
		notifHelper:    notifHelper,
		publisher:      publisher,
	}
}

// CreateSubmission creates a new submission from a template.
func (s *SubmissionService) CreateSubmission(ctx context.Context, req *signingV1.CreateSubmissionRequest) (*signingV1.CreateSubmissionResponse, error) {
	tenantID := getTenantIDFromContext(ctx)
	createdBy := getUserIDAsUint32(ctx)

	// Get template and snapshot its current state
	template, err := s.templateRepo.GetByID(ctx, req.TemplateId)
	if err != nil {
		return nil, err
	}
	if template == nil {
		return nil, signingV1.ErrorTemplateNotFound("template not found")
	}

	// Convert preferences from map[string]string to map[string]interface{}
	preferences := make(map[string]interface{}, len(req.Preferences))
	for k, v := range req.Preferences {
		preferences[k] = v
	}

	// Create submission with template snapshot + prefill values
	submissionID := generateUUID()
	submission, err := s.submissionRepo.Create(ctx, tenantID, submissionID, req.TemplateId,
		req.SigningMode, req.Source, preferences, template, req.PrefillValues, createdBy)
	if err != nil {
		return nil, err
	}

	// Create submitters from request
	for i, sub := range req.Submitters {
		_, err := s.submitterRepo.Create(ctx, tenantID, submissionID, sub.Name, sub.Email, sub.Phone, sub.Role, i)
		if err != nil {
			s.log.Errorf("failed to create submitter: %v", err)
			return nil, err
		}
	}

	// Log event
	_ = s.eventRepo.Create(ctx, tenantID, "submission.created", getUserIDFromContext(ctx),
		"submission", submissionID, nil, "")

	return &signingV1.CreateSubmissionResponse{
		Submission: s.submissionRepo.ToProto(submission),
	}, nil
}

// GetSubmission gets a submission by ID.
func (s *SubmissionService) GetSubmission(ctx context.Context, req *signingV1.GetSubmissionRequest) (*signingV1.GetSubmissionResponse, error) {
	tenantID := getTenantIDFromContext(ctx)

	submission, err := s.submissionRepo.GetByID(ctx, req.Id)
	if err != nil {
		return nil, err
	}
	if submission == nil {
		return nil, signingV1.ErrorSubmissionNotFound("submission not found")
	}

	// Cross-tenant isolation (applies to all roles)
	if derefUint32(submission.TenantID) != tenantID {
		return nil, signingV1.ErrorAccessDenied("access denied")
	}

	// Signing users can only view their own submissions
	if isSigningUser(ctx) {
		uid := getUserIDAsUint32(ctx)
		if uid == nil || submission.CreateBy == nil || *uid != *submission.CreateBy {
			return nil, signingV1.ErrorAccessDenied("you can only view your own submissions")
		}
	}

	return &signingV1.GetSubmissionResponse{
		Submission: s.submissionRepo.ToProto(submission),
	}, nil
}

// ListSubmissions lists submissions with pagination.
func (s *SubmissionService) ListSubmissions(ctx context.Context, req *signingV1.ListSubmissionsRequest) (*signingV1.ListSubmissionsResponse, error) {
	tenantID := getTenantIDFromContext(ctx)

	page := uint32(1)
	if req.Page != nil {
		page = *req.Page
	}
	pageSize := uint32(20)
	if req.PageSize != nil {
		pageSize = *req.PageSize
	}

	// Signing users can only see their own submissions
	var createdByFilter *uint32
	if isSigningUser(ctx) {
		uid := getUserIDAsUint32(ctx)
		createdByFilter = uid
	}

	submissions, total, err := s.submissionRepo.List(ctx, tenantID, req.TemplateId, req.Status, createdByFilter, page, pageSize)
	if err != nil {
		return nil, err
	}

	protoSubmissions := make([]*signingV1.Submission, 0, len(submissions))
	for _, sub := range submissions {
		protoSubmissions = append(protoSubmissions, s.submissionRepo.ToProto(sub))
	}

	return &signingV1.ListSubmissionsResponse{
		Submissions: protoSubmissions,
		Total:       uint32(total),
	}, nil
}

// SendSubmission sends invitations to signers based on signing mode.
func (s *SubmissionService) SendSubmission(ctx context.Context, req *signingV1.SendSubmissionRequest) (*signingV1.SendSubmissionResponse, error) {
	tenantID := getTenantIDFromContext(ctx)

	submission, err := s.submissionRepo.GetByID(ctx, req.Id)
	if err != nil {
		return nil, err
	}
	if submission == nil {
		return nil, signingV1.ErrorSubmissionNotFound("submission not found")
	}

	// Cross-tenant isolation (applies to all roles)
	if derefUint32(submission.TenantID) != tenantID {
		return nil, signingV1.ErrorAccessDenied("access denied")
	}

	// Signing users can only send their own submissions
	if isSigningUser(ctx) {
		uid := getUserIDAsUint32(ctx)
		if uid == nil || submission.CreateBy == nil || *uid != *submission.CreateBy {
			return nil, signingV1.ErrorAccessDenied("you can only send your own submissions")
		}
	}

	// Update status to IN_PROGRESS
	submission, err = s.submissionRepo.UpdateStatus(ctx, req.Id, "SUBMISSION_STATUS_IN_PROGRESS")
	if err != nil {
		return nil, err
	}

	// Get submitters ordered by signing_order
	submitters, err := s.submitterRepo.ListBySubmission(ctx, req.Id)
	if err != nil {
		return nil, err
	}

	now := time.Now()

	// Collect submitters that receive invitations in this send
	var sentSubmitters []int
	switch submission.SigningMode.String() {
	case "SIGNING_MODE_SEQUENTIAL":
		// Send invitation only to first submitter (order=0)
		if len(submitters) > 0 {
			sentSubmitters = append(sentSubmitters, 0)
		}
	case "SIGNING_MODE_PARALLEL":
		// Send invitations to all submitters
		for i := range submitters {
			sentSubmitters = append(sentSubmitters, i)
		}
	}

	// Log event
	_ = s.eventRepo.Create(ctx, tenantID, "submission.sent", getUserIDFromContext(ctx),
		"submission", req.Id, nil, "")

	// Get template name for the email
	tmpl, tmplErr := s.templateRepo.GetByID(ctx, submission.TemplateID)
	templateName := "Untitled Document"
	if tmplErr == nil && tmpl != nil {
		templateName = tmpl.Name
	}

	senderName := getUsernameFromContext(ctx)
	if senderName == "" {
		senderName = "A user"
	}

	// For each submitter to invite: check if they have a certificate.
	// If yes → send signing invite. If no → create pending cert + send setup email.
	if s.notifHelper != nil && len(sentSubmitters) > 0 {
		sendCtx := client.DetachedMetadataContext(ctx, tenantID)

		for _, idx := range sentSubmitters {
			sub := submitters[idx]

			// Check if submitter already has a completed certificate
			existingCert, certErr := s.certRepo.GetByEmail(ctx, tenantID, sub.Email)
			if certErr != nil {
				s.log.Errorf("failed to check certificate for submitter %s: %v", sub.ID, certErr)
			}

			if existingCert != nil {
				// Certificate exists → send normal signing invitation
				_ = s.submitterRepo.UpdateSentAt(ctx, sub.ID, now)
				subCopy := sub
				go func() {
					defer func() {
						if r := recover(); r != nil {
							s.log.Errorf("Panic in signing invitation goroutine: %v", r)
						}
					}()
					if err := s.notifHelper.SendInvitation(sendCtx, subCopy.Name, subCopy.Email, subCopy.Slug, subCopy.Role, templateName, senderName, ""); err != nil {
						s.log.Errorf("Failed to send signing invitation to submitter %s: %v", subCopy.ID, err)
					}
				}()
			} else {
				// No certificate → check if pending cert setup already exists
				pendingCert, pendErr := s.certRepo.GetPendingByEmail(ctx, tenantID, sub.Email)
				if pendErr != nil {
					s.log.Errorf("failed to check pending cert for submitter %s: %v", sub.ID, pendErr)
				}

				var setupToken string
				if pendingCert != nil {
					setupToken = pendingCert.SetupToken
				} else {
					// Create pending certificate record
					setupToken = generateUUID()
					_, createErr := s.certRepo.CreatePending(ctx, tenantID, sub.Name, defaultCertOrg, sub.Email, setupToken, nil)
					if createErr != nil {
						s.log.Errorf("failed to create pending cert for submitter %s: %v", sub.ID, createErr)
						continue
					}
				}

				// Send certificate setup email
				subCopy := sub
				tokenCopy := setupToken
				go func() {
					defer func() {
						if r := recover(); r != nil {
							s.log.Errorf("Panic in cert setup notification goroutine: %v", r)
						}
					}()
					if err := s.notifHelper.SendCertificateSetupNotification(sendCtx, subCopy.Name, subCopy.Email, tokenCopy, templateName); err != nil {
						s.log.Errorf("Failed to send cert setup notification to submitter %s: %v", subCopy.ID, err)
					}
				}()
			}
		}
	}

	return &signingV1.SendSubmissionResponse{
		Submission: s.submissionRepo.ToProto(submission),
	}, nil
}

// CompleteSubmitter marks a submitter as completed and advances the workflow.
func (s *SubmissionService) CompleteSubmitter(ctx context.Context, req *signingV1.CompleteSubmitterRequest) (*signingV1.CompleteSubmitterResponse, error) {
	tenantID := getTenantIDFromContext(ctx)

	submitter, err := s.submitterRepo.GetByID(ctx, req.SubmitterId)
	if err != nil {
		return nil, err
	}
	if submitter == nil {
		return nil, signingV1.ErrorSubmitterNotFound("submitter not found")
	}

	// Cross-tenant ownership check
	if derefUint32(submitter.TenantID) != tenantID {
		return nil, signingV1.ErrorAccessDenied("access denied")
	}

	// Unmarshal JSON-encoded values from []byte to map[string]interface{}
	var values map[string]interface{}
	if len(req.Values) > 0 {
		if err := json.Unmarshal(req.Values, &values); err != nil {
			values = nil
		}
	}

	// Mark submitter as completed (atomic — prevents double-completion race)
	now := time.Now()
	submitter, err = s.submitterRepo.Complete(ctx, req.SubmitterId, values, req.Ip, req.UserAgent, now)
	if err != nil {
		return nil, err
	}
	if submitter == nil {
		return nil, signingV1.ErrorSubmitterAlreadyCompleted("submitter has already been completed")
	}

	// Log event
	_ = s.eventRepo.Create(ctx, tenantID, "submitter.completed", "",
		"submitter", req.SubmitterId, nil, req.Ip)

	// Handle sequential workflow advancement
	submission, err := s.submissionRepo.GetByID(ctx, submitter.SubmissionID)
	if err != nil {
		return nil, err
	}

	if submission.SigningMode.String() == "SIGNING_MODE_SEQUENTIAL" {
		s.advanceSequentialWorkflow(ctx, tenantID, submitter.SubmissionID, submitter.SigningOrder)
	}

	// Check if all submitters are completed
	s.checkSubmissionCompletion(ctx, tenantID, submitter.SubmissionID)

	return &signingV1.CompleteSubmitterResponse{}, nil
}

// DeclineSubmitter marks a submitter as declined and cancels the submission.
func (s *SubmissionService) DeclineSubmitter(ctx context.Context, req *signingV1.DeclineSubmitterRequest) (*signingV1.DeclineSubmitterResponse, error) {
	tenantID := getTenantIDFromContext(ctx)

	// Load submitter first to check tenant ownership BEFORE mutating state
	submitter, err := s.submitterRepo.GetByID(ctx, req.SubmitterId)
	if err != nil {
		return nil, err
	}
	if submitter == nil {
		return nil, signingV1.ErrorSubmitterNotFound("submitter not found")
	}

	// Cross-tenant ownership check
	if derefUint32(submitter.TenantID) != tenantID {
		return nil, signingV1.ErrorAccessDenied("access denied")
	}

	// Prevent double-decline
	if submitter.Status == "SUBMITTER_STATUS_DECLINED" {
		return nil, signingV1.ErrorBadRequest("submitter has already been declined")
	}
	if submitter.Status == "SUBMITTER_STATUS_COMPLETED" {
		return nil, signingV1.ErrorSubmitterAlreadyCompleted("submitter has already completed signing")
	}

	_, err = s.submitterRepo.Decline(ctx, req.SubmitterId, req.Reason)
	if err != nil {
		return nil, err
	}

	_, err = s.submissionRepo.UpdateStatus(ctx, submitter.SubmissionID, "SUBMISSION_STATUS_CANCELLED")
	if err != nil {
		return nil, err
	}

	// Log events
	_ = s.eventRepo.Create(ctx, tenantID, "submitter.declined", "",
		"submitter", req.SubmitterId, map[string]interface{}{"reason": req.Reason}, "")
	_ = s.eventRepo.Create(ctx, tenantID, "submission.cancelled", "",
		"submission", submitter.SubmissionID, nil, "")

	// Notify all other participants about the decline
	if s.notifHelper != nil {
		submission, subErr := s.submissionRepo.GetByID(ctx, submitter.SubmissionID)
		if subErr == nil && submission != nil {
			tmpl, tmplErr := s.templateRepo.GetByID(ctx, submission.TemplateID)
			templateName := "Untitled Document"
			if tmplErr == nil && tmpl != nil {
				templateName = tmpl.Name
			}

			allSubmitters, listErr := s.submitterRepo.ListBySubmission(ctx, submitter.SubmissionID)
			if listErr == nil {
				sendCtx := client.DetachedMetadataContext(ctx, tenantID)
				for _, other := range allSubmitters {
					if other.ID == req.SubmitterId {
						continue // Skip the decliner
					}
					other := other
					go func() {
						defer func() {
							if r := recover(); r != nil {
								s.log.Errorf("Panic in decline notification goroutine: %v", r)
							}
						}()
						if err := s.notifHelper.SendDeclineNotification(sendCtx, other.Name, other.Email, submitter.Name, templateName, req.Reason); err != nil {
							s.log.Errorf("Failed to send decline notification to submitter %s: %v", other.ID, err)
						}
					}()
				}
			}
		}
	}

	return &signingV1.DeclineSubmitterResponse{}, nil
}

// CancelSubmission cancels an in-progress submission and marks all pending submitters as declined.
func (s *SubmissionService) CancelSubmission(ctx context.Context, req *signingV1.CancelSubmissionRequest) (*signingV1.CancelSubmissionResponse, error) {
	tenantID := getTenantIDFromContext(ctx)

	submission, err := s.submissionRepo.GetByID(ctx, req.Id)
	if err != nil {
		return nil, err
	}
	if submission == nil {
		return nil, signingV1.ErrorSubmissionNotFound("submission not found")
	}

	// Cross-tenant isolation
	if derefUint32(submission.TenantID) != tenantID {
		return nil, signingV1.ErrorAccessDenied("access denied")
	}

	// Can only cancel if not already completed or cancelled
	status := submission.Status.String()
	if status == "SUBMISSION_STATUS_COMPLETED" {
		return nil, signingV1.ErrorBadRequest("submission is already completed")
	}
	if status == "SUBMISSION_STATUS_CANCELLED" {
		return nil, signingV1.ErrorBadRequest("submission is already cancelled")
	}

	// Cancel the submission
	submission, err = s.submissionRepo.UpdateStatus(ctx, req.Id, "SUBMISSION_STATUS_CANCELLED")
	if err != nil {
		return nil, err
	}

	// Decline all pending/opened submitters
	submitters, listErr := s.submitterRepo.ListBySubmission(ctx, req.Id)
	if listErr == nil {
		for _, sub := range submitters {
			if sub.Status.String() == "SUBMITTER_STATUS_PENDING" || sub.Status.String() == "SUBMITTER_STATUS_OPENED" {
				_, _ = s.submitterRepo.Decline(ctx, sub.ID, req.Reason)
			}
		}
	}

	// Log event
	_ = s.eventRepo.Create(ctx, tenantID, "submission.cancelled", getUserIDFromContext(ctx),
		"submission", req.Id, map[string]interface{}{"reason": req.Reason}, "")

	// Publish Redis event
	if s.publisher != nil {
		s.publisher.PublishSubmissionCancelled(ctx, tenantID, req.Id, req.Reason)
	}

	return &signingV1.CancelSubmissionResponse{
		Submission: s.submissionRepo.ToProto(submission),
	}, nil
}

// GetSubmissionDocumentUrl returns a presigned download URL for the signed document.
func (s *SubmissionService) GetSubmissionDocumentUrl(ctx context.Context, req *signingV1.GetSubmissionDocumentUrlRequest) (*signingV1.GetSubmissionDocumentUrlResponse, error) {
	tenantID := getTenantIDFromContext(ctx)

	submission, err := s.submissionRepo.GetByID(ctx, req.Id)
	if err != nil {
		return nil, err
	}
	if submission == nil {
		return nil, signingV1.ErrorSubmissionNotFound("submission not found")
	}

	// Cross-tenant isolation
	if derefUint32(submission.TenantID) != tenantID {
		return nil, signingV1.ErrorAccessDenied("access denied")
	}

	// Signing users can only access their own submissions
	if isSigningUser(ctx) {
		uid := getUserIDAsUint32(ctx)
		if uid == nil || submission.CreateBy == nil || *uid != *submission.CreateBy {
			return nil, signingV1.ErrorAccessDenied("you can only access your own submissions")
		}
	}

	if submission.SignedDocumentKey == "" {
		return nil, signingV1.ErrorBadRequest("no signed document available yet")
	}

	url, err := s.storage.GetPresignedURL(ctx, submission.SignedDocumentKey, 15*time.Minute)
	if err != nil {
		return nil, signingV1.ErrorInternalServerError("failed to generate download URL")
	}

	return &signingV1.GetSubmissionDocumentUrlResponse{
		Url: url,
	}, nil
}

// advanceSequentialWorkflow sends invitation to the next submitter in order.
func (s *SubmissionService) advanceSequentialWorkflow(ctx context.Context, tenantID uint32, submissionID string, completedOrder int) {
	nextSubmitter, err := s.submitterRepo.GetByOrder(ctx, submissionID, completedOrder+1)
	if err != nil || nextSubmitter == nil {
		return // No next submitter or all done
	}

	now := time.Now()
	_ = s.submitterRepo.UpdateSentAt(ctx, nextSubmitter.ID, now)

	// Log event
	_ = s.eventRepo.Create(ctx, tenantID, "submitter.sent", "",
		"submitter", nextSubmitter.ID, nil, "")

	// Send next-signer notification email
	if s.notifHelper != nil {
		submission, subErr := s.submissionRepo.GetByID(ctx, submissionID)
		if subErr != nil || submission == nil {
			return
		}

		tmpl, tmplErr := s.templateRepo.GetByID(ctx, submission.TemplateID)
		templateName := "Untitled Document"
		if tmplErr == nil && tmpl != nil {
			templateName = tmpl.Name
		}

		sendCtx := client.DetachedMetadataContext(ctx, tenantID)
		go func() {
			defer func() {
				if r := recover(); r != nil {
					s.log.Errorf("Panic in next-signer notification goroutine: %v", r)
				}
			}()
			if err := s.notifHelper.SendNextSignerNotification(sendCtx, nextSubmitter.Name, nextSubmitter.Email, nextSubmitter.Slug, nextSubmitter.Role, templateName); err != nil {
				s.log.Errorf("Failed to send next-signer notification to submitter %s: %v", nextSubmitter.ID, err)
			}
		}()
	}
}

// checkSubmissionCompletion checks if all submitters have completed and marks the submission.
func (s *SubmissionService) checkSubmissionCompletion(ctx context.Context, tenantID uint32, submissionID string) {
	allComplete, err := s.submitterRepo.AreAllCompleted(ctx, submissionID)
	if err != nil {
		s.log.Errorf("failed to check completion: %v", err)
		return
	}

	if allComplete {
		now := time.Now()
		_, err := s.submissionRepo.Complete(ctx, submissionID, now)
		if err != nil {
			s.log.Errorf("failed to complete submission: %v", err)
			return
		}

		// Log event
		_ = s.eventRepo.Create(ctx, tenantID, "submission.completed", "",
			"submission", submissionID, nil, "")

		// Publish Redis event for external consumers (e.g., HR module)
		if s.publisher != nil {
			sub, _ := s.submissionRepo.GetByID(ctx, submissionID)
			if sub != nil {
				s.publisher.PublishSubmissionCompleted(ctx, tenantID, submissionID, sub.TemplateID, sub.SignedDocumentKey)
			}
		}
	}
}

