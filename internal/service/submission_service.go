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

	signingV1 "github.com/go-tangra/go-tangra-signing/gen/go/signing/service/v1"
)

type SubmissionService struct {
	signingV1.UnimplementedSigningSubmissionServiceServer

	log            *log.Helper
	submissionRepo *data.SubmissionRepo
	submitterRepo  *data.SubmitterRepo
	templateRepo   *data.TemplateRepo
	eventRepo      *data.EventRepo
	storage        *data.StorageClient
	notifHelper    *NotificationHelper
}

func NewSubmissionService(
	ctx *bootstrap.Context,
	submissionRepo *data.SubmissionRepo,
	submitterRepo *data.SubmitterRepo,
	templateRepo *data.TemplateRepo,
	eventRepo *data.EventRepo,
	storage *data.StorageClient,
	notificationClient *client.NotificationClient,
	adminClient *client.AdminClient,
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
		storage:        storage,
		notifHelper:    notifHelper,
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

	// Create submission with template snapshot
	submissionID := generateUUID()
	submission, err := s.submissionRepo.Create(ctx, tenantID, submissionID, req.TemplateId,
		req.SigningMode, req.Source, preferences, template, createdBy)
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
	submission, err := s.submissionRepo.GetByID(ctx, req.Id)
	if err != nil {
		return nil, err
	}
	if submission == nil {
		return nil, signingV1.ErrorSubmissionNotFound("submission not found")
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

	submissions, total, err := s.submissionRepo.List(ctx, tenantID, req.TemplateId, req.Status, page, pageSize)
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
			_ = s.submitterRepo.UpdateSentAt(ctx, submitters[0].ID, now)
			sentSubmitters = append(sentSubmitters, 0)
		}
	case "SIGNING_MODE_PARALLEL":
		// Send invitations to all submitters
		for i, sub := range submitters {
			_ = s.submitterRepo.UpdateSentAt(ctx, sub.ID, now)
			sentSubmitters = append(sentSubmitters, i)
		}
	}

	// Log event
	_ = s.eventRepo.Create(ctx, tenantID, "submission.sent", getUserIDFromContext(ctx),
		"submission", req.Id, nil, "")

	// Send email notifications asynchronously
	if s.notifHelper != nil && len(sentSubmitters) > 0 {
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

		// Use detached context — gRPC cancels the request context after handler returns
		sendCtx := client.DetachedMetadataContext(ctx, tenantID)

		for _, idx := range sentSubmitters {
			sub := submitters[idx]
			go func() {
				defer func() {
					if r := recover(); r != nil {
						s.log.Errorf("Panic in signing invitation goroutine: %v", r)
					}
				}()
				if err := s.notifHelper.SendInvitation(sendCtx, sub.Name, sub.Email, sub.Slug, sub.Role, templateName, senderName, ""); err != nil {
					s.log.Errorf("Failed to send signing invitation to %s: %v", sub.Email, err)
				}
			}()
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

	// Unmarshal JSON-encoded values from []byte to map[string]interface{}
	var values map[string]interface{}
	if len(req.Values) > 0 {
		if err := json.Unmarshal(req.Values, &values); err != nil {
			values = nil
		}
	}

	// Mark submitter as completed
	now := time.Now()
	submitter, err = s.submitterRepo.Complete(ctx, req.SubmitterId, values, req.Ip, req.UserAgent, now)
	if err != nil {
		return nil, err
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

	_, err := s.submitterRepo.Decline(ctx, req.SubmitterId, req.Reason)
	if err != nil {
		return nil, err
	}

	// Get submission and cancel it
	submitter, err := s.submitterRepo.GetByID(ctx, req.SubmitterId)
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
							s.log.Errorf("Failed to send decline notification to %s: %v", other.Email, err)
						}
					}()
				}
			}
		}
	}

	return &signingV1.DeclineSubmitterResponse{}, nil
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
				s.log.Errorf("Failed to send next-signer notification to %s: %v", nextSubmitter.Email, err)
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
	}
}

