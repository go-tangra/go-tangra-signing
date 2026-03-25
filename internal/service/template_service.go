package service

import (
	"context"
	"fmt"
	"net/http"

	"github.com/go-kratos/kratos/v2/log"
	"github.com/tx7do/kratos-bootstrap/bootstrap"
	"google.golang.org/protobuf/types/known/emptypb"

	"github.com/go-tangra/go-tangra-signing/internal/data"
	"github.com/go-tangra/go-tangra-signing/internal/data/ent/schema"

	signingV1 "github.com/go-tangra/go-tangra-signing/gen/go/signing/service/v1"
)

type TemplateService struct {
	signingV1.UnimplementedSigningTemplateServiceServer

	log          *log.Helper
	templateRepo *data.TemplateRepo
	storage      *data.StorageClient
}

func NewTemplateService(
	ctx *bootstrap.Context,
	templateRepo *data.TemplateRepo,
	storage *data.StorageClient,
) *TemplateService {
	return &TemplateService{
		log:          ctx.NewLoggerHelper("signing/service/template"),
		templateRepo: templateRepo,
		storage:      storage,
	}
}

// CreateTemplate creates a new signing template from an uploaded PDF.
func (s *TemplateService) CreateTemplate(ctx context.Context, req *signingV1.CreateTemplateRequest) (*signingV1.CreateTemplateResponse, error) {
	tenantID := getTenantIDFromContext(ctx)
	createdBy := getUserIDAsUint32(ctx)

	// Validate the uploaded file is a PDF
	if len(req.FileContent) == 0 {
		return nil, signingV1.ErrorInvalidTemplatePdf("file content is required")
	}
	const maxPdfSize = 50 * 1024 * 1024 // 50 MB
	if len(req.FileContent) > maxPdfSize {
		return nil, signingV1.ErrorInvalidTemplatePdf("PDF too large (max 50 MB)")
	}
	mimeType := http.DetectContentType(req.FileContent)
	if mimeType != "application/pdf" {
		return nil, signingV1.ErrorInvalidTemplatePdf("file must be a PDF document, got %s", mimeType)
	}

	// Generate template ID for the storage path
	templateID := generateUUID()

	// Upload PDF to storage: {tenantID}/signing-templates/{templateID}/{fileName}
	storageKey := fmt.Sprintf("%d/signing-templates/%s/%s", tenantID, templateID, req.FileName)
	uploadResult, err := s.storage.UploadRaw(ctx, storageKey, req.FileContent, "application/pdf")
	if err != nil {
		s.log.Errorf("failed to upload template PDF: %v", err)
		return nil, signingV1.ErrorStorageOperationError("failed to upload template PDF")
	}

	// Create DB record with file metadata (fields are added later via the visual builder)
	template, err := s.templateRepo.Create(
		ctx, tenantID, templateID, req.Name, req.Description, req.FolderId,
		storageKey, req.FileName, uploadResult.Size, createdBy,
	)
	if err != nil {
		// Clean up the uploaded file on DB failure
		if delErr := s.storage.Delete(ctx, storageKey); delErr != nil {
			s.log.Warnf("failed to clean up uploaded file after DB error: %v", delErr)
		}
		return nil, err
	}

	return &signingV1.CreateTemplateResponse{
		Template: s.templateRepo.ToProto(template),
	}, nil
}

// GetTemplate gets a template by ID.
func (s *TemplateService) GetTemplate(ctx context.Context, req *signingV1.GetTemplateRequest) (*signingV1.GetTemplateResponse, error) {
	tenantID := getTenantIDFromContext(ctx)

	template, err := s.templateRepo.GetByID(ctx, req.Id)
	if err != nil {
		return nil, err
	}
	if template == nil {
		return nil, signingV1.ErrorTemplateNotFound("template not found")
	}

	// Tenant ownership check
	if derefUint32(template.TenantID) != tenantID {
		return nil, signingV1.ErrorAccessDenied("access denied")
	}

	return &signingV1.GetTemplateResponse{
		Template: s.templateRepo.ToProto(template),
	}, nil
}

// ListTemplates lists templates with pagination.
func (s *TemplateService) ListTemplates(ctx context.Context, req *signingV1.ListTemplatesRequest) (*signingV1.ListTemplatesResponse, error) {
	tenantID := getTenantIDFromContext(ctx)

	page := uint32(1)
	if req.Page != nil {
		page = *req.Page
	}
	pageSize := uint32(20)
	if req.PageSize != nil {
		pageSize = *req.PageSize
	}

	templates, total, err := s.templateRepo.List(ctx, tenantID, req.FolderId, req.NameFilter, req.Status, page, pageSize)
	if err != nil {
		return nil, err
	}

	protoTemplates := make([]*signingV1.Template, 0, len(templates))
	for _, t := range templates {
		protoTemplates = append(protoTemplates, s.templateRepo.ToProto(t))
	}

	return &signingV1.ListTemplatesResponse{
		Templates: protoTemplates,
		Total:     uint32(total),
	}, nil
}

// UpdateTemplate updates template metadata (name, description, status).
func (s *TemplateService) UpdateTemplate(ctx context.Context, req *signingV1.UpdateTemplateRequest) (*signingV1.UpdateTemplateResponse, error) {
	tenantID := getTenantIDFromContext(ctx)
	updatedBy := getUserIDAsUint32(ctx)

	// Verify ownership before update
	existing, err := s.templateRepo.GetByID(ctx, req.Id)
	if err != nil {
		return nil, err
	}
	if existing == nil {
		return nil, signingV1.ErrorTemplateNotFound("template not found")
	}
	if derefUint32(existing.TenantID) != tenantID {
		return nil, signingV1.ErrorAccessDenied("access denied")
	}

	template, err := s.templateRepo.Update(ctx, req.Id, req.Name, req.Description, req.Status, updatedBy)
	if err != nil {
		return nil, err
	}

	return &signingV1.UpdateTemplateResponse{
		Template: s.templateRepo.ToProto(template),
	}, nil
}

// DeleteTemplate deletes a template and its PDF from storage.
func (s *TemplateService) DeleteTemplate(ctx context.Context, req *signingV1.DeleteTemplateRequest) (*emptypb.Empty, error) {
	tenantID := getTenantIDFromContext(ctx)

	// Get template to retrieve file key for storage cleanup
	template, err := s.templateRepo.GetByID(ctx, req.Id)
	if err != nil {
		return nil, err
	}
	if template == nil {
		return nil, signingV1.ErrorTemplateNotFound("template not found")
	}

	// Tenant ownership check
	if derefUint32(template.TenantID) != tenantID {
		return nil, signingV1.ErrorAccessDenied("access denied")
	}

	// Delete from database
	if err := s.templateRepo.Delete(ctx, req.Id); err != nil {
		return nil, err
	}

	// Delete PDF from storage (best-effort)
	if err := s.storage.Delete(ctx, template.FileKey); err != nil {
		s.log.Warnf("failed to delete template file from storage: %v", err)
	}

	return &emptypb.Empty{}, nil
}

// CloneTemplate creates a copy of an existing template, including its PDF file.
func (s *TemplateService) CloneTemplate(ctx context.Context, req *signingV1.CloneTemplateRequest) (*signingV1.CloneTemplateResponse, error) {
	createdBy := getUserIDAsUint32(ctx)
	tenantID := getTenantIDFromContext(ctx)

	// Verify source template exists and belongs to this tenant
	source, err := s.templateRepo.GetByID(ctx, req.Id)
	if err != nil {
		return nil, err
	}
	if source == nil {
		return nil, signingV1.ErrorTemplateNotFound("template not found")
	}
	if derefUint32(source.TenantID) != tenantID {
		return nil, signingV1.ErrorAccessDenied("access denied")
	}

	template, err := s.templateRepo.Clone(ctx, tenantID, req.Id, req.Name, s.storage, createdBy)
	if err != nil {
		return nil, err
	}

	return &signingV1.CloneTemplateResponse{
		Template: s.templateRepo.ToProto(template),
	}, nil
}

// UpdateTemplateFields updates the field definitions of a template (from the visual builder).
func (s *TemplateService) UpdateTemplateFields(ctx context.Context, req *signingV1.UpdateTemplateFieldsRequest) (*signingV1.UpdateTemplateFieldsResponse, error) {
	tenantID := getTenantIDFromContext(ctx)
	updatedBy := getUserIDAsUint32(ctx)

	// Verify template exists
	existing, err := s.templateRepo.GetByID(ctx, req.Id)
	if err != nil {
		return nil, err
	}
	if existing == nil {
		return nil, signingV1.ErrorTemplateNotFound("template not found")
	}

	// Tenant ownership check
	if derefUint32(existing.TenantID) != tenantID {
		return nil, signingV1.ErrorAccessDenied("access denied")
	}

	// Convert proto fields to schema format
	schemaFields := make([]schema.TemplateFieldDef, 0, len(req.Fields))
	for _, f := range req.Fields {
		schemaFields = append(schemaFields, schema.TemplateFieldDef{
			ID:             f.Id,
			Name:           f.Name,
			Type:           protoFieldTypeToString(f.Type),
			Required:       f.Required,
			PageNumber:     int(f.PageNumber),
			XPercent:       f.XPercent,
			YPercent:       f.YPercent,
			WidthPercent:   f.WidthPercent,
			HeightPercent:  f.HeightPercent,
			SubmitterIndex: int(f.SubmitterIndex),
			Font:           f.Font,
			FontSize:       f.FontSize,
		})
	}

	updated, err := s.templateRepo.UpdateFields(ctx, req.Id, schemaFields, updatedBy)
	if err != nil {
		return nil, err
	}

	return &signingV1.UpdateTemplateFieldsResponse{
		Template: s.templateRepo.ToProto(updated),
	}, nil
}

// GetTemplatePdfUrl returns a proxy URL for viewing the template PDF.
// The PDF is served through the module's HTTP server to avoid CORS/presigned URL issues.
func (s *TemplateService) GetTemplatePdfUrl(ctx context.Context, req *signingV1.GetTemplatePdfUrlRequest) (*signingV1.GetTemplatePdfUrlResponse, error) {
	template, err := s.templateRepo.GetByID(ctx, req.Id)
	if err != nil {
		return nil, err
	}
	if template == nil {
		return nil, signingV1.ErrorTemplateNotFound("template not found")
	}

	// Return a proxy URL through the admin gateway → module HTTP server (via /modules/ asset proxy)
	proxyURL := fmt.Sprintf("/modules/signing/api/v1/signing/templates/pdf?key=%s", template.FileKey)

	return &signingV1.GetTemplatePdfUrlResponse{
		Url: proxyURL,
	}, nil
}

// protoFieldTypeToString converts a proto TemplateFieldType enum to its string representation.
func protoFieldTypeToString(t signingV1.TemplateFieldType) string {
	switch t {
	case signingV1.TemplateFieldType_TEMPLATE_FIELD_TYPE_TEXT:
		return "text"
	case signingV1.TemplateFieldType_TEMPLATE_FIELD_TYPE_NUMBER:
		return "number"
	case signingV1.TemplateFieldType_TEMPLATE_FIELD_TYPE_SIGNATURE:
		return "signature"
	case signingV1.TemplateFieldType_TEMPLATE_FIELD_TYPE_INITIALS:
		return "initials"
	case signingV1.TemplateFieldType_TEMPLATE_FIELD_TYPE_DATE:
		return "date"
	case signingV1.TemplateFieldType_TEMPLATE_FIELD_TYPE_CHECKBOX:
		return "checkbox"
	case signingV1.TemplateFieldType_TEMPLATE_FIELD_TYPE_SELECT:
		return "select"
	case signingV1.TemplateFieldType_TEMPLATE_FIELD_TYPE_RADIO:
		return "radio"
	case signingV1.TemplateFieldType_TEMPLATE_FIELD_TYPE_IMAGE:
		return "image"
	case signingV1.TemplateFieldType_TEMPLATE_FIELD_TYPE_FILE:
		return "file"
	case signingV1.TemplateFieldType_TEMPLATE_FIELD_TYPE_CELLS:
		return "cells"
	case signingV1.TemplateFieldType_TEMPLATE_FIELD_TYPE_STAMP:
		return "stamp"
	case signingV1.TemplateFieldType_TEMPLATE_FIELD_TYPE_PAYMENT:
		return "payment"
	default:
		return "text"
	}
}

