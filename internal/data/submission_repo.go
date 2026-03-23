package data

import (
	"context"
	"time"

	"github.com/go-kratos/kratos/v2/log"
	"github.com/google/uuid"
	"github.com/tx7do/kratos-bootstrap/bootstrap"
	"google.golang.org/protobuf/types/known/timestamppb"

	entCrud "github.com/tx7do/go-crud/entgo"

	"github.com/go-tangra/go-tangra-signing/internal/data/ent"
	"github.com/go-tangra/go-tangra-signing/internal/data/ent/submission"

	signingV1 "github.com/go-tangra/go-tangra-signing/gen/go/signing/service/v1"
)

type SubmissionRepo struct {
	entClient *entCrud.EntClient[*ent.Client]
	log       *log.Helper
}

func NewSubmissionRepo(ctx *bootstrap.Context, entClient *entCrud.EntClient[*ent.Client]) *SubmissionRepo {
	return &SubmissionRepo{
		log:       ctx.NewLoggerHelper("signing/submission/repo"),
		entClient: entClient,
	}
}

// Create creates a new submission with a snapshot of the template state.
func (r *SubmissionRepo) Create(ctx context.Context, tenantID uint32, id, templateID string, signingMode, source string, preferences map[string]interface{}, tmpl *ent.Template, createdBy *uint32) (*ent.Submission, error) {
	slug := uuid.New().String()[:8]

	builder := r.entClient.Client().Submission.Create().
		SetID(id).
		SetTenantID(tenantID).
		SetTemplateID(templateID).
		SetSlug(slug).
		SetCreateTime(time.Now())

	if signingMode != "" {
		builder.SetSigningMode(submission.SigningMode(signingMode))
	}
	if source != "" {
		builder.SetSource(source)
	}
	if preferences != nil {
		builder.SetPreferences(preferences)
	}
	if createdBy != nil {
		builder.SetCreateBy(*createdBy)
	}

	// Snapshot template state (includes font/fontSize)
	if tmpl != nil {
		fieldsSnapshot := make([]map[string]interface{}, 0, len(tmpl.Fields))
		for _, f := range tmpl.Fields {
			fieldsSnapshot = append(fieldsSnapshot, map[string]interface{}{
				"id":              f.ID,
				"name":            f.Name,
				"type":            f.Type,
				"required":        f.Required,
				"page_number":     f.PageNumber,
				"x_percent":       f.XPercent,
				"y_percent":       f.YPercent,
				"width_percent":   f.WidthPercent,
				"height_percent":  f.HeightPercent,
				"submitter_index": f.SubmitterIndex,
				"font":            f.Font,
				"font_size":       f.FontSize,
			})
		}
		builder.SetTemplateFieldsSnapshot(fieldsSnapshot)
	}

	entity, err := builder.Save(ctx)
	if err != nil {
		r.log.Errorf("create submission failed: %s", err.Error())
		return nil, signingV1.ErrorInternalServerError("create submission failed")
	}

	return entity, nil
}

// GetByID retrieves a submission by ID.
func (r *SubmissionRepo) GetByID(ctx context.Context, id string) (*ent.Submission, error) {
	entity, err := r.entClient.Client().Submission.Get(ctx, id)
	if err != nil {
		if ent.IsNotFound(err) {
			return nil, nil
		}
		r.log.Errorf("get submission failed: %s", err.Error())
		return nil, signingV1.ErrorInternalServerError("get submission failed")
	}
	return entity, nil
}

// List lists submissions with optional filters.
func (r *SubmissionRepo) List(ctx context.Context, tenantID uint32, templateID, status *string, page, pageSize uint32) ([]*ent.Submission, int, error) {
	query := r.entClient.Client().Submission.Query().
		Where(submission.TenantIDEQ(tenantID))

	if templateID != nil && *templateID != "" {
		query = query.Where(submission.TemplateIDEQ(*templateID))
	}
	if status != nil && *status != "" {
		query = query.Where(submission.StatusEQ(submission.Status(*status)))
	}

	total, err := query.Clone().Count(ctx)
	if err != nil {
		r.log.Errorf("count submissions failed: %s", err.Error())
		return nil, 0, signingV1.ErrorInternalServerError("count submissions failed")
	}

	if page > 0 && pageSize > 0 {
		offset := int((page - 1) * pageSize)
		query = query.Offset(offset).Limit(int(pageSize))
	}

	entities, err := query.Order(ent.Desc(submission.FieldCreateTime)).All(ctx)
	if err != nil {
		r.log.Errorf("list submissions failed: %s", err.Error())
		return nil, 0, signingV1.ErrorInternalServerError("list submissions failed")
	}

	return entities, total, nil
}

// UpdateStatus updates the submission status.
func (r *SubmissionRepo) UpdateStatus(ctx context.Context, id, status string) (*ent.Submission, error) {
	entity, err := r.entClient.Client().Submission.UpdateOneID(id).
		SetStatus(submission.Status(status)).
		SetUpdateTime(time.Now()).
		Save(ctx)
	if err != nil {
		r.log.Errorf("update submission status failed: %s", err.Error())
		return nil, signingV1.ErrorInternalServerError("update submission status failed")
	}
	return entity, nil
}

// UpdateCurrentPdfKey sets the storage key of the in-progress PDF (with completed signers' data).
func (r *SubmissionRepo) UpdateCurrentPdfKey(ctx context.Context, id, pdfKey string) error {
	_, err := r.entClient.Client().Submission.UpdateOneID(id).
		SetCurrentPdfKey(pdfKey).
		SetUpdateTime(time.Now()).
		Save(ctx)
	if err != nil {
		r.log.Errorf("update current pdf key failed: %s", err.Error())
		return signingV1.ErrorInternalServerError("update current pdf key failed")
	}
	return nil
}

// UpdateSignedDocumentKey sets the storage key of the final signed PDF on a submission.
func (r *SubmissionRepo) UpdateSignedDocumentKey(ctx context.Context, id, signedKey string) error {
	_, err := r.entClient.Client().Submission.UpdateOneID(id).
		SetSignedDocumentKey(signedKey).
		SetUpdateTime(time.Now()).
		Save(ctx)
	if err != nil {
		r.log.Errorf("update signed document key failed: %s", err.Error())
		return signingV1.ErrorInternalServerError("update signed document key failed")
	}
	return nil
}

// Complete marks a submission as completed.
func (r *SubmissionRepo) Complete(ctx context.Context, id string, completedAt time.Time) (*ent.Submission, error) {
	entity, err := r.entClient.Client().Submission.UpdateOneID(id).
		SetStatus(submission.StatusSUBMISSION_STATUS_COMPLETED).
		SetCompletedAt(completedAt).
		SetUpdateTime(time.Now()).
		Save(ctx)
	if err != nil {
		r.log.Errorf("complete submission failed: %s", err.Error())
		return nil, signingV1.ErrorInternalServerError("complete submission failed")
	}
	return entity, nil
}

// ToProto converts an ent.Submission to signingV1.Submission.
func (r *SubmissionRepo) ToProto(entity *ent.Submission) *signingV1.Submission {
	if entity == nil {
		return nil
	}

	proto := &signingV1.Submission{
		Id:                entity.ID,
		TenantId:          derefUint32(entity.TenantID),
		TemplateId:        entity.TemplateID,
		Slug:              entity.Slug,
		SigningMode:       entity.SigningMode.String(),
		Status:            entity.Status.String(),
		Source:            entity.Source,
		SignedDocumentKey: entity.SignedDocumentKey,
		CurrentPdfKey:     entity.CurrentPdfKey,
	}

	if entity.CompletedAt != nil {
		proto.CompletedAt = timestamppb.New(*entity.CompletedAt)
	}
	if entity.CreateBy != nil {
		proto.CreatedBy = entity.CreateBy
	}
	if entity.CreateTime != nil && !entity.CreateTime.IsZero() {
		proto.CreateTime = timestamppb.New(*entity.CreateTime)
	}

	return proto
}

 
