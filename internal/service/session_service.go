package service

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/go-kratos/kratos/v2/log"
	"github.com/tx7do/kratos-bootstrap/bootstrap"

	"github.com/go-tangra/go-tangra-signing/internal/client"
	"github.com/go-tangra/go-tangra-signing/internal/data"
	"github.com/go-tangra/go-tangra-signing/internal/data/ent"
	"github.com/go-tangra/go-tangra-signing/internal/data/ent/submitter"

	signingV1 "github.com/go-tangra/go-tangra-signing/gen/go/signing/service/v1"
	appViewer "github.com/go-tangra/go-tangra-signing/pkg/viewer"
)

// SessionService handles public signing session operations (no auth, token-based).
type SessionService struct {
	signingV1.UnimplementedSigningSessionServiceServer

	log            *log.Helper
	submitterRepo  *data.SubmitterRepo
	submissionRepo *data.SubmissionRepo
	templateRepo   *data.TemplateRepo
	eventRepo      *data.EventRepo
	storage        *data.StorageClient
	pdfGenerator   *PDFGenerator
	notifHelper    *NotificationHelper
}

// NewSessionService creates a new SessionService instance.
func NewSessionService(
	ctx *bootstrap.Context,
	submitterRepo *data.SubmitterRepo,
	submissionRepo *data.SubmissionRepo,
	templateRepo *data.TemplateRepo,
	eventRepo *data.EventRepo,
	storage *data.StorageClient,
	pdfGenerator *PDFGenerator,
	notificationClient *client.NotificationClient,
	adminClient *client.AdminClient,
) *SessionService {
	l := ctx.NewLoggerHelper("signing/service/session")

	appHost := os.Getenv("APP_HOST")
	if appHost == "" {
		appHost = "http://localhost:8080"
	}

	var notifHelper *NotificationHelper
	if notificationClient != nil {
		notifHelper = NewNotificationHelper(l, notificationClient, adminClient, appHost)
	}

	return &SessionService{
		log:            l,
		submitterRepo:  submitterRepo,
		submissionRepo: submissionRepo,
		templateRepo:   templateRepo,
		eventRepo:      eventRepo,
		storage:        storage,
		pdfGenerator:   pdfGenerator,
		notifHelper:    notifHelper,
	}
}

// GetSigningSession returns the signing session for a submitter identified by token (slug).
func (s *SessionService) GetSigningSession(ctx context.Context, req *signingV1.GetSigningSessionRequest) (*signingV1.GetSigningSessionResponse, error) {
	ctx = appViewer.NewSystemViewerContext(ctx)

	if req.Token == "" {
		return nil, signingV1.ErrorBadRequest("token is required")
	}

	// Look up submitter by slug (token)
	sub, err := s.submitterRepo.GetBySlug(ctx, req.Token)
	if err != nil {
		return nil, err
	}
	if sub == nil {
		return nil, signingV1.ErrorSubmitterNotFound("signing session not found")
	}

	// Reject completed or declined sessions
	if sub.Status == submitter.StatusSUBMITTER_STATUS_COMPLETED {
		return &signingV1.GetSigningSessionResponse{
			Status:  "COMPLETED",
			Message: "This signing session has already been completed.",
		}, nil
	}
	if sub.Status == submitter.StatusSUBMITTER_STATUS_DECLINED {
		return &signingV1.GetSigningSessionResponse{
			Status:  "DECLINED",
			Message: "This signing session has been declined.",
		}, nil
	}

	// Get submission
	submission, err := s.submissionRepo.GetByID(ctx, sub.SubmissionID)
	if err != nil {
		return nil, err
	}
	if submission == nil {
		return nil, signingV1.ErrorSubmissionNotFound("submission not found")
	}

	// Get template for name and file key
	template, err := s.templateRepo.GetByID(ctx, submission.TemplateID)
	if err != nil {
		return nil, err
	}
	if template == nil {
		return nil, signingV1.ErrorTemplateNotFound("template not found")
	}

	// Mark as opened if first time
	if sub.OpenedAt == nil {
		now := time.Now()
		if openErr := s.submitterRepo.UpdateOpenedAt(ctx, sub.ID, now); openErr != nil {
			s.log.Errorf("failed to update opened_at: %v", openErr)
		}

		// Log event
		_ = s.eventRepo.Create(ctx, derefUint32(sub.TenantID), "submitter.opened", "",
			"submitter", sub.ID, nil, "")
	}

	// Generate PDF proxy URL — use the intermediate PDF if a previous signer has completed,
	// otherwise fall back to the original template PDF.
	pdfKey := template.FileKey
	if submission.CurrentPdfKey != "" {
		pdfKey = submission.CurrentPdfKey
	}
	documentURL := fmt.Sprintf("/modules/signing/api/v1/signing/templates/pdf?key=%s", pdfKey)

	// Filter template fields by submitter index matching this submitter's signing_order
	sessionFields := buildSessionFields(submission.TemplateFieldsSnapshot, sub.SigningOrder)

	// Determine display status
	status := "PENDING"
	if sub.OpenedAt != nil || sub.Status == submitter.StatusSUBMITTER_STATUS_OPENED {
		status = "OPENED"
	}

	return &signingV1.GetSigningSessionResponse{
		SubmissionName: submission.Slug,
		TemplateName:   template.Name,
		DocumentUrl:    documentURL,
		SignerName:     sub.Name,
		SignerEmail:    sub.Email,
		Fields:         sessionFields,
		Status:         status,
		Message:        "Please review and sign the document.",
	}, nil
}

// SubmitSigning processes a signing submission from a signer.
func (s *SessionService) SubmitSigning(ctx context.Context, req *signingV1.SubmitSigningRequest) (*signingV1.SubmitSigningResponse, error) {
	ctx = appViewer.NewSystemViewerContext(ctx)

	if req.Token == "" {
		return nil, signingV1.ErrorBadRequest("token is required")
	}

	// Look up submitter by slug (token)
	sub, err := s.submitterRepo.GetBySlug(ctx, req.Token)
	if err != nil {
		return nil, err
	}
	if sub == nil {
		return nil, signingV1.ErrorSubmitterNotFound("signing session not found")
	}

	// Verify status allows signing
	if sub.Status == submitter.StatusSUBMITTER_STATUS_COMPLETED {
		return nil, signingV1.ErrorSubmitterAlreadyCompleted("this signing session has already been completed")
	}
	if sub.Status == submitter.StatusSUBMITTER_STATUS_DECLINED {
		return nil, signingV1.ErrorBadRequest("this signing session has been declined")
	}

	// Get submission for field validation context
	submission, err := s.submissionRepo.GetByID(ctx, sub.SubmissionID)
	if err != nil {
		return nil, err
	}
	if submission == nil {
		return nil, signingV1.ErrorSubmissionNotFound("submission not found")
	}

	// Validate required fields are filled
	if validationErr := validateRequiredFields(submission.TemplateFieldsSnapshot, sub.SigningOrder, req.FieldValues); validationErr != nil {
		return nil, signingV1.ErrorBadRequest("missing required fields: %s", validationErr.Error())
	}

	// Store field values as map
	values := fieldValuesToMap(req.FieldValues)

	// Upload signature image if provided
	if len(req.SignatureImage) > 0 {
		tenantID := derefUint32(sub.TenantID)
		sigKey := fmt.Sprintf("%d/signatures/%s.png", tenantID, sub.ID)
		_, uploadErr := s.storage.UploadRaw(ctx, sigKey, req.SignatureImage, "image/png")
		if uploadErr != nil {
			s.log.Errorf("failed to upload signature image: %v", uploadErr)
			return nil, signingV1.ErrorStorageOperationError("failed to upload signature image")
		}
		values["_signature_key"] = sigKey
	}

	// Mark submitter as completed
	now := time.Now()
	sub, err = s.submitterRepo.Complete(ctx, sub.ID, values, "", "", now)
	if err != nil {
		return nil, err
	}

	tenantID := derefUint32(sub.TenantID)

	// Log event
	_ = s.eventRepo.Create(ctx, tenantID, "submitter.completed", "",
		"submitter", sub.ID, nil, "")

	// Generate intermediate PDF with this signer's data overlaid
	s.generateIntermediatePDF(ctx, tenantID, sub, submission)

	// Handle sequential workflow: advance to next submitter
	if submission.SigningMode.String() == "SIGNING_MODE_SEQUENTIAL" {
		s.advanceSequentialWorkflow(ctx, tenantID, sub.SubmissionID, sub.SigningOrder)
	}

	// Check if all submitters completed
	allComplete := s.checkAndCompleteSubmission(ctx, tenantID, sub.SubmissionID, submission)

	message := "Your signing has been submitted successfully."
	if allComplete {
		message = "All signers have completed. The document is now fully signed."
	}

	return &signingV1.SubmitSigningResponse{
		Completed: true,
		Message:   message,
	}, nil
}

// advanceSequentialWorkflow sends invitation to the next submitter in order.
func (s *SessionService) advanceSequentialWorkflow(ctx context.Context, tenantID uint32, submissionID string, completedOrder int) {
	nextSubmitter, err := s.submitterRepo.GetByOrder(ctx, submissionID, completedOrder+1)
	if err != nil || nextSubmitter == nil {
		return
	}

	now := time.Now()
	_ = s.submitterRepo.UpdateSentAt(ctx, nextSubmitter.ID, now)

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

// generateIntermediatePDF creates a PDF with the completed signer's data overlaid
// and stores it as the current_pdf_key on the submission. This allows subsequent
// signers to see previous signers' filled data in the document.
func (s *SessionService) generateIntermediatePDF(ctx context.Context, tenantID uint32, sub *ent.Submitter, submission *ent.Submission) {
	if s.pdfGenerator == nil {
		return
	}

	template, err := s.templateRepo.GetByID(ctx, submission.TemplateID)
	if err != nil || template == nil {
		s.log.Errorf("failed to get template for intermediate PDF: %v", err)
		return
	}

	fieldValues := buildFieldValuesForOverlay(submission.TemplateFieldsSnapshot, sub.SigningOrder, sub.Values)
	if len(fieldValues) == 0 {
		return
	}

	intermediateKey, err := s.pdfGenerator.GenerateIntermediatePDF(
		ctx, tenantID, submission.ID, template.FileKey, submission.CurrentPdfKey, fieldValues,
	)
	if err != nil {
		s.log.Errorf("failed to generate intermediate PDF: %v", err)
		return
	}

	if err := s.submissionRepo.UpdateCurrentPdfKey(ctx, submission.ID, intermediateKey); err != nil {
		s.log.Errorf("failed to update current_pdf_key: %v", err)
	}
}

// buildFieldValuesForOverlay merges template field positions with the submitter's values
// to create field value maps suitable for PDF overlay generation.
func buildFieldValuesForOverlay(fieldsSnapshot []map[string]interface{}, signerOrder int, values map[string]interface{}) []map[string]interface{} {
	result := make([]map[string]interface{}, 0)

	for _, f := range fieldsSnapshot {
		idx, ok := f["submitter_index"]
		if !ok {
			continue
		}

		var submitterIndex int
		switch v := idx.(type) {
		case float64:
			submitterIndex = int(v)
		case int:
			submitterIndex = v
		default:
			continue
		}

		if submitterIndex != signerOrder {
			continue
		}

		fieldID := getStringField(f, "id")
		value := ""
		if v, ok := values[fieldID]; ok {
			if s, ok := v.(string); ok {
				value = s
			}
		}
		if value == "" {
			continue
		}

		result = append(result, map[string]interface{}{
			"name":         getStringField(f, "name"),
			"type":         getStringField(f, "type"),
			"value":        value,
			"pageNumber":   f["page_number"],
			"xPercent":     f["x_percent"],
			"yPercent":     f["y_percent"],
			"widthPercent": f["width_percent"],
			"heightPercent": f["height_percent"],
		})
	}

	return result
}

// checkAndCompleteSubmission checks if all submitters completed, triggers PDF generation and marks submission done.
func (s *SessionService) checkAndCompleteSubmission(ctx context.Context, tenantID uint32, submissionID string, submission *ent.Submission) bool {
	allComplete, err := s.submitterRepo.AreAllCompleted(ctx, submissionID)
	if err != nil {
		s.log.Errorf("failed to check completion: %v", err)
		return false
	}

	if !allComplete {
		return false
	}

	// Get template for file key
	template, err := s.templateRepo.GetByID(ctx, submission.TemplateID)
	if err != nil || template == nil {
		s.log.Errorf("failed to get template for PDF generation: %v", err)
	}

	// Re-read submission to get latest state (e.g., BISS might have set signed_document_key)
	submission, _ = s.submissionRepo.GetByID(ctx, submissionID)

	// Trigger PDF generation (best-effort) — skip if already signed (e.g., via BISS PAdES)
	if template != nil && submission != nil && submission.SignedDocumentKey == "" {
		signedKey, genErr := s.pdfGenerator.GenerateSignedPDF(ctx, tenantID, submissionID, template.FileKey, submission.TemplateFieldsSnapshot)
		if genErr != nil {
			s.log.Errorf("failed to generate signed PDF: %v", genErr)
		} else {
			_ = s.submissionRepo.UpdateSignedDocumentKey(ctx, submissionID, signedKey)
		}
	}

	// Mark submission as completed
	now := time.Now()
	_, completeErr := s.submissionRepo.Complete(ctx, submissionID, now)
	if completeErr != nil {
		s.log.Errorf("failed to complete submission: %v", completeErr)
		return false
	}

	_ = s.eventRepo.Create(ctx, tenantID, "submission.completed", "",
		"submission", submissionID, nil, "")

	// Notify all participants that the document is fully signed
	if s.notifHelper != nil {
		tmpl, tmplErr := s.templateRepo.GetByID(ctx, submission.TemplateID)
		templateName := "Untitled Document"
		if tmplErr == nil && tmpl != nil {
			templateName = tmpl.Name
		}

		submitters, listErr := s.submitterRepo.ListBySubmission(ctx, submissionID)
		if listErr == nil {
			sendCtx := client.DetachedMetadataContext(ctx, tenantID)
			for _, sub := range submitters {
				sub := sub
				go func() {
					defer func() {
						if r := recover(); r != nil {
							s.log.Errorf("Panic in completion notification goroutine: %v", r)
						}
					}()
					if err := s.notifHelper.SendCompletionNotification(sendCtx, sub.Name, sub.Email, templateName, submissionID); err != nil {
						s.log.Errorf("Failed to send completion notification to %s: %v", sub.Email, err)
					}
				}()
			}
		}
	}

	return true
}

// buildSessionFields filters template field snapshot by submitter index and converts to proto.
func buildSessionFields(fieldsSnapshot []map[string]interface{}, submitterOrder int) []*signingV1.SessionField {
	fields := make([]*signingV1.SessionField, 0)

	for _, f := range fieldsSnapshot {
		idx, ok := f["submitter_index"]
		if !ok {
			continue
		}

		// Handle both float64 (JSON) and int types
		var submitterIndex int
		switch v := idx.(type) {
		case float64:
			submitterIndex = int(v)
		case int:
			submitterIndex = v
		default:
			continue
		}

		if submitterIndex != submitterOrder {
			continue
		}

		field := &signingV1.SessionField{
			FieldId:       getStringField(f, "id"),
			Name:          getStringField(f, "name"),
			Type:          getStringField(f, "type"),
			Required:      getBoolField(f, "required"),
			PageNumber:    getInt32Field(f, "page_number"),
			XPercent:      getFloat64Field(f, "x_percent"),
			YPercent:      getFloat64Field(f, "y_percent"),
			WidthPercent:  getFloat64Field(f, "width_percent"),
			HeightPercent: getFloat64Field(f, "height_percent"),
			Font:          getStringField(f, "font"),
			FontSize:      getFloat64Field(f, "font_size"),
		}
		fields = append(fields, field)
	}

	return fields
}

// validateRequiredFields checks that all required fields for this submitter have values.
func validateRequiredFields(fieldsSnapshot []map[string]interface{}, submitterOrder int, fieldValues []*signingV1.FieldValueSubmission) error {
	// Build a set of submitted field IDs
	submittedIDs := make(map[string]string, len(fieldValues))
	for _, fv := range fieldValues {
		submittedIDs[fv.FieldId] = fv.Value
	}

	var missing []string
	for _, f := range fieldsSnapshot {
		idx, ok := f["submitter_index"]
		if !ok {
			continue
		}

		var submitterIndex int
		switch v := idx.(type) {
		case float64:
			submitterIndex = int(v)
		case int:
			submitterIndex = v
		default:
			continue
		}

		if submitterIndex != submitterOrder {
			continue
		}

		required := getBoolField(f, "required")
		if !required {
			continue
		}

		fieldID := getStringField(f, "id")
		val, exists := submittedIDs[fieldID]
		if !exists || val == "" {
			name := getStringField(f, "name")
			if name == "" {
				name = fieldID
			}
			missing = append(missing, name)
		}
	}

	if len(missing) > 0 {
		return fmt.Errorf("%v", missing)
	}
	return nil
}

// fieldValuesToMap converts field value submissions to a map for storage.
func fieldValuesToMap(fieldValues []*signingV1.FieldValueSubmission) map[string]interface{} {
	result := make(map[string]interface{}, len(fieldValues))
	for _, fv := range fieldValues {
		result[fv.FieldId] = fv.Value
	}
	return result
}

// Helper functions for extracting typed values from map[string]interface{}.
// Note: getStringField, getFloat64Field, getIntField, getBoolField, getInt32Field
// are defined in pdf_generator.go. This file uses those shared helpers.
 
