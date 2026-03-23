package data

import (
	"context"
	"fmt"
	"time"

	"github.com/go-kratos/kratos/v2/log"
	"github.com/google/uuid"
	"github.com/tx7do/kratos-bootstrap/bootstrap"
	"google.golang.org/protobuf/types/known/timestamppb"

	entCrud "github.com/tx7do/go-crud/entgo"

	"github.com/go-tangra/go-tangra-signing/internal/data/ent"
	"github.com/go-tangra/go-tangra-signing/internal/data/ent/schema"
	"github.com/go-tangra/go-tangra-signing/internal/data/ent/template"

	signingV1 "github.com/go-tangra/go-tangra-signing/gen/go/signing/service/v1"
)

type TemplateRepo struct {
	entClient *entCrud.EntClient[*ent.Client]
	log       *log.Helper
}

func NewTemplateRepo(ctx *bootstrap.Context, entClient *entCrud.EntClient[*ent.Client]) *TemplateRepo {
	return &TemplateRepo{
		log:       ctx.NewLoggerHelper("signing/template/repo"),
		entClient: entClient,
	}
}

// Create creates a new template with an uploaded PDF file.
func (r *TemplateRepo) Create(
	ctx context.Context,
	tenantID uint32,
	templateID string,
	name string,
	description *string,
	folderID *string,
	fileKey string,
	fileName string,
	fileSize int64,
	createdBy *uint32,
) (*ent.Template, error) {
	slug := uuid.New().String()[:8]

	builder := r.entClient.Client().Template.Create().
		SetID(templateID).
		SetTenantID(tenantID).
		SetName(name).
		SetSlug(slug).
		SetFileKey(fileKey).
		SetFileName(fileName).
		SetFileSize(fileSize).
		SetSource("pdf_upload").
		SetCreateTime(time.Now())

	if description != nil {
		builder.SetDescription(*description)
	}
	if folderID != nil {
		builder.SetFolderID(*folderID)
	}
	if createdBy != nil {
		builder.SetCreateBy(*createdBy)
	}

	entity, err := builder.Save(ctx)
	if err != nil {
		if ent.IsConstraintError(err) {
			return nil, signingV1.ErrorTemplateAlreadyExists("template already exists")
		}
		r.log.Errorf("create template failed: %s", err.Error())
		return nil, signingV1.ErrorInternalServerError("create template failed")
	}

	return entity, nil
}

// GetByID retrieves a template by ID.
func (r *TemplateRepo) GetByID(ctx context.Context, id string) (*ent.Template, error) {
	entity, err := r.entClient.Client().Template.Get(ctx, id)
	if err != nil {
		if ent.IsNotFound(err) {
			return nil, nil
		}
		r.log.Errorf("get template failed: %s", err.Error())
		return nil, signingV1.ErrorInternalServerError("get template failed")
	}
	return entity, nil
}

// List lists templates with optional filters.
func (r *TemplateRepo) List(ctx context.Context, tenantID uint32, folderID *string, nameFilter *string, status *string, page, pageSize uint32) ([]*ent.Template, int, error) {
	query := r.entClient.Client().Template.Query().
		Where(template.TenantIDEQ(tenantID))

	if folderID != nil {
		query = query.Where(template.FolderIDEQ(*folderID))
	}
	if nameFilter != nil && *nameFilter != "" {
		query = query.Where(template.NameContains(*nameFilter))
	}
	if status != nil && *status != "" {
		query = query.Where(template.StatusEQ(template.Status(*status)))
	}

	total, err := query.Clone().Count(ctx)
	if err != nil {
		r.log.Errorf("count templates failed: %s", err.Error())
		return nil, 0, signingV1.ErrorInternalServerError("count templates failed")
	}

	if page > 0 && pageSize > 0 {
		offset := int((page - 1) * pageSize)
		query = query.Offset(offset).Limit(int(pageSize))
	}

	entities, err := query.Order(ent.Desc(template.FieldCreateTime)).All(ctx)
	if err != nil {
		r.log.Errorf("list templates failed: %s", err.Error())
		return nil, 0, signingV1.ErrorInternalServerError("list templates failed")
	}

	return entities, total, nil
}

// Update updates template metadata (name, description, status).
func (r *TemplateRepo) Update(ctx context.Context, id string, name, description, status *string, updatedBy *uint32) (*ent.Template, error) {
	builder := r.entClient.Client().Template.UpdateOneID(id).
		SetUpdateTime(time.Now())

	if name != nil {
		builder.SetName(*name)
	}
	if description != nil {
		builder.SetDescription(*description)
	}
	if status != nil {
		builder.SetStatus(template.Status(*status))
	}
	if updatedBy != nil {
		builder.SetUpdateBy(*updatedBy)
	}

	entity, err := builder.Save(ctx)
	if err != nil {
		if ent.IsNotFound(err) {
			return nil, signingV1.ErrorTemplateNotFound("template not found")
		}
		r.log.Errorf("update template failed: %s", err.Error())
		return nil, signingV1.ErrorInternalServerError("update template failed")
	}

	return entity, nil
}

// UpdateFields updates the field definitions JSON column of a template.
func (r *TemplateRepo) UpdateFields(ctx context.Context, id string, fields []schema.TemplateFieldDef, updatedBy *uint32) (*ent.Template, error) {
	builder := r.entClient.Client().Template.UpdateOneID(id).
		SetFields(fields).
		SetUpdateTime(time.Now())

	if updatedBy != nil {
		builder.SetUpdateBy(*updatedBy)
	}

	entity, err := builder.Save(ctx)
	if err != nil {
		if ent.IsNotFound(err) {
			return nil, signingV1.ErrorTemplateNotFound("template not found")
		}
		r.log.Errorf("update template fields failed: %s", err.Error())
		return nil, signingV1.ErrorInternalServerError("update template fields failed")
	}

	return entity, nil
}

// Delete deletes a template.
func (r *TemplateRepo) Delete(ctx context.Context, id string) error {
	err := r.entClient.Client().Template.DeleteOneID(id).Exec(ctx)
	if err != nil {
		if ent.IsNotFound(err) {
			return signingV1.ErrorTemplateNotFound("template not found")
		}
		r.log.Errorf("delete template failed: %s", err.Error())
		return signingV1.ErrorInternalServerError("delete template failed")
	}
	return nil
}

// Clone creates a copy of an existing template, including its PDF file in storage.
func (r *TemplateRepo) Clone(ctx context.Context, tenantID uint32, sourceID, newName string, storage *StorageClient, createdBy *uint32) (*ent.Template, error) {
	source, err := r.GetByID(ctx, sourceID)
	if err != nil {
		return nil, err
	}
	if source == nil {
		return nil, signingV1.ErrorTemplateNotFound("source template not found")
	}

	newID := uuid.New().String()
	slug := uuid.New().String()[:8]
	name := newName
	if name == "" {
		name = source.Name + " (Copy)"
	}

	// Copy PDF file in storage
	newFileKey := fmt.Sprintf("%d/signing-templates/%s/%s", tenantID, newID, source.FileName)
	if err := storage.CopyObject(ctx, source.FileKey, newFileKey); err != nil {
		r.log.Errorf("failed to copy template PDF: %v", err)
		return nil, signingV1.ErrorStorageOperationError("failed to copy template PDF")
	}

	builder := r.entClient.Client().Template.Create().
		SetID(newID).
		SetTenantID(derefUint32(source.TenantID)).
		SetName(name).
		SetSlug(slug).
		SetFileKey(newFileKey).
		SetFileName(source.FileName).
		SetFileSize(source.FileSize).
		SetFields(source.Fields).
		SetSource("cloned").
		SetCreateTime(time.Now())

	if source.FolderID != nil {
		builder.SetFolderID(*source.FolderID)
	}
	if createdBy != nil {
		builder.SetCreateBy(*createdBy)
	}

	entity, err := builder.Save(ctx)
	if err != nil {
		// Clean up the copied file on DB failure
		if delErr := storage.Delete(ctx, newFileKey); delErr != nil {
			r.log.Warnf("failed to clean up copied file: %v", delErr)
		}
		r.log.Errorf("clone template failed: %s", err.Error())
		return nil, signingV1.ErrorInternalServerError("clone template failed")
	}

	return entity, nil
}

// ToProto converts an ent.Template to signingV1.Template.
func (r *TemplateRepo) ToProto(entity *ent.Template) *signingV1.Template {
	if entity == nil {
		return nil
	}

	proto := &signingV1.Template{
		Id:          entity.ID,
		TenantId:    derefUint32(entity.TenantID),
		Name:        entity.Name,
		Slug:        entity.Slug,
		Description: entity.Description,
		Status:      entity.Status.String(),
		Source:      entity.Source,
		FileKey:     entity.FileKey,
		FileName:    entity.FileName,
		FileSize:    entity.FileSize,
	}

	// Convert schema fields to proto fields
	if len(entity.Fields) > 0 {
		protoFields := make([]*signingV1.TemplateFieldDef, 0, len(entity.Fields))
		for _, f := range entity.Fields {
			protoFields = append(protoFields, &signingV1.TemplateFieldDef{
				Id:             f.ID,
				Name:           f.Name,
				Type:           stringToProtoFieldType(f.Type),
				Required:       f.Required,
				PageNumber:     int32(f.PageNumber),
				XPercent:       f.XPercent,
				YPercent:       f.YPercent,
				WidthPercent:   f.WidthPercent,
				HeightPercent:  f.HeightPercent,
				SubmitterIndex: int32(f.SubmitterIndex),
				Font:           f.Font,
				FontSize:       f.FontSize,
			})
		}
		proto.Fields = protoFields
	}

	if entity.FolderID != nil {
		proto.FolderId = entity.FolderID
	}
	if entity.CreateBy != nil {
		proto.CreatedBy = entity.CreateBy
	}
	if entity.CreateTime != nil && !entity.CreateTime.IsZero() {
		proto.CreateTime = timestamppb.New(*entity.CreateTime)
	}
	if entity.UpdateTime != nil && !entity.UpdateTime.IsZero() {
		proto.UpdateTime = timestamppb.New(*entity.UpdateTime)
	}

	return proto
}

func derefUint32(p *uint32) uint32 {
	if p == nil {
		return 0
	}
	return *p
}

// stringToProtoFieldType converts a string field type to the proto enum.
func stringToProtoFieldType(s string) signingV1.TemplateFieldType {
	switch s {
	case "text":
		return signingV1.TemplateFieldType_TEMPLATE_FIELD_TYPE_TEXT
	case "number":
		return signingV1.TemplateFieldType_TEMPLATE_FIELD_TYPE_NUMBER
	case "signature":
		return signingV1.TemplateFieldType_TEMPLATE_FIELD_TYPE_SIGNATURE
	case "initials":
		return signingV1.TemplateFieldType_TEMPLATE_FIELD_TYPE_INITIALS
	case "date":
		return signingV1.TemplateFieldType_TEMPLATE_FIELD_TYPE_DATE
	case "checkbox":
		return signingV1.TemplateFieldType_TEMPLATE_FIELD_TYPE_CHECKBOX
	case "select":
		return signingV1.TemplateFieldType_TEMPLATE_FIELD_TYPE_SELECT
	case "radio":
		return signingV1.TemplateFieldType_TEMPLATE_FIELD_TYPE_RADIO
	case "image":
		return signingV1.TemplateFieldType_TEMPLATE_FIELD_TYPE_IMAGE
	case "file":
		return signingV1.TemplateFieldType_TEMPLATE_FIELD_TYPE_FILE
	case "cells":
		return signingV1.TemplateFieldType_TEMPLATE_FIELD_TYPE_CELLS
	case "stamp":
		return signingV1.TemplateFieldType_TEMPLATE_FIELD_TYPE_STAMP
	case "payment":
		return signingV1.TemplateFieldType_TEMPLATE_FIELD_TYPE_PAYMENT
	default:
		return signingV1.TemplateFieldType_TEMPLATE_FIELD_TYPE_UNSPECIFIED
	}
}
