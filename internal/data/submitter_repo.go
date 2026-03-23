package data

import (
	"context"
	"time"

	"github.com/go-kratos/kratos/v2/log"
	"github.com/google/uuid"
	"github.com/tx7do/kratos-bootstrap/bootstrap"

	entCrud "github.com/tx7do/go-crud/entgo"

	"github.com/go-tangra/go-tangra-signing/internal/data/ent"
	"github.com/go-tangra/go-tangra-signing/internal/data/ent/submitter"

	signingV1 "github.com/go-tangra/go-tangra-signing/gen/go/signing/service/v1"
)

type SubmitterRepo struct {
	entClient *entCrud.EntClient[*ent.Client]
	log       *log.Helper
}

func NewSubmitterRepo(ctx *bootstrap.Context, entClient *entCrud.EntClient[*ent.Client]) *SubmitterRepo {
	return &SubmitterRepo{
		log:       ctx.NewLoggerHelper("signing/submitter/repo"),
		entClient: entClient,
	}
}

// Create creates a new submitter for a submission.
func (r *SubmitterRepo) Create(ctx context.Context, tenantID uint32, submissionID, name, email, phone, role string, order int) (*ent.Submitter, error) {
	id := uuid.New().String()
	slug := uuid.New().String()[:8]

	builder := r.entClient.Client().Submitter.Create().
		SetID(id).
		SetTenantID(tenantID).
		SetSubmissionID(submissionID).
		SetSlug(slug).
		SetSigningOrder(order).
		SetCreateTime(time.Now())

	if name != "" {
		builder.SetName(name)
	}
	if email != "" {
		builder.SetEmail(email)
	}
	if phone != "" {
		builder.SetPhone(phone)
	}
	if role != "" {
		builder.SetRole(role)
	}

	entity, err := builder.Save(ctx)
	if err != nil {
		r.log.Errorf("create submitter failed: %s", err.Error())
		return nil, signingV1.ErrorInternalServerError("create submitter failed")
	}

	return entity, nil
}

// GetBySlug retrieves a submitter by its unique slug (token used in signing URLs).
func (r *SubmitterRepo) GetBySlug(ctx context.Context, slug string) (*ent.Submitter, error) {
	entity, err := r.entClient.Client().Submitter.Query().
		Where(submitter.SlugEQ(slug)).
		Only(ctx)
	if err != nil {
		if ent.IsNotFound(err) {
			return nil, nil
		}
		r.log.Errorf("get submitter by slug failed: %s", err.Error())
		return nil, signingV1.ErrorInternalServerError("get submitter by slug failed")
	}
	return entity, nil
}

// GetByID retrieves a submitter by ID.
func (r *SubmitterRepo) GetByID(ctx context.Context, id string) (*ent.Submitter, error) {
	entity, err := r.entClient.Client().Submitter.Get(ctx, id)
	if err != nil {
		if ent.IsNotFound(err) {
			return nil, nil
		}
		r.log.Errorf("get submitter failed: %s", err.Error())
		return nil, signingV1.ErrorInternalServerError("get submitter failed")
	}
	return entity, nil
}

// ListBySubmission lists all submitters for a submission, ordered by signing_order.
func (r *SubmitterRepo) ListBySubmission(ctx context.Context, submissionID string) ([]*ent.Submitter, error) {
	entities, err := r.entClient.Client().Submitter.Query().
		Where(submitter.SubmissionIDEQ(submissionID)).
		Order(ent.Asc(submitter.FieldSigningOrder)).
		All(ctx)
	if err != nil {
		r.log.Errorf("list submitters failed: %s", err.Error())
		return nil, signingV1.ErrorInternalServerError("list submitters failed")
	}
	return entities, nil
}

// GetByOrder retrieves a submitter by submission ID and signing order.
func (r *SubmitterRepo) GetByOrder(ctx context.Context, submissionID string, order int) (*ent.Submitter, error) {
	entity, err := r.entClient.Client().Submitter.Query().
		Where(
			submitter.SubmissionIDEQ(submissionID),
			submitter.SigningOrderEQ(order),
		).
		Only(ctx)
	if err != nil {
		if ent.IsNotFound(err) {
			return nil, nil
		}
		r.log.Errorf("get submitter by order failed: %s", err.Error())
		return nil, signingV1.ErrorInternalServerError("get submitter by order failed")
	}
	return entity, nil
}

// UpdateOpenedAt marks the time a submitter first opened the signing link.
func (r *SubmitterRepo) UpdateOpenedAt(ctx context.Context, id string, openedAt time.Time) error {
	_, err := r.entClient.Client().Submitter.UpdateOneID(id).
		SetOpenedAt(openedAt).
		SetStatus(submitter.StatusSUBMITTER_STATUS_OPENED).
		Save(ctx)
	if err != nil {
		r.log.Errorf("update opened_at failed: %s", err.Error())
		return signingV1.ErrorInternalServerError("update opened_at failed")
	}
	return nil
}

// UpdateSentAt marks a submitter's invitation as sent.
func (r *SubmitterRepo) UpdateSentAt(ctx context.Context, id string, sentAt time.Time) error {
	_, err := r.entClient.Client().Submitter.UpdateOneID(id).
		SetSentAt(sentAt).
		Save(ctx)
	if err != nil {
		r.log.Errorf("update sent_at failed: %s", err.Error())
		return signingV1.ErrorInternalServerError("update sent_at failed")
	}
	return nil
}

// Complete marks a submitter as completed.
func (r *SubmitterRepo) Complete(ctx context.Context, id string, values map[string]interface{}, ip, userAgent string, completedAt time.Time) (*ent.Submitter, error) {
	builder := r.entClient.Client().Submitter.UpdateOneID(id).
		SetStatus(submitter.StatusSUBMITTER_STATUS_COMPLETED).
		SetCompletedAt(completedAt)

	if values != nil {
		builder.SetValues(values)
	}
	if ip != "" {
		builder.SetIP(ip)
	}
	if userAgent != "" {
		builder.SetUserAgent(userAgent)
	}

	entity, err := builder.Save(ctx)
	if err != nil {
		r.log.Errorf("complete submitter failed: %s", err.Error())
		return nil, signingV1.ErrorInternalServerError("complete submitter failed")
	}
	return entity, nil
}

// Decline marks a submitter as declined.
func (r *SubmitterRepo) Decline(ctx context.Context, id, reason string) (*ent.Submitter, error) {
	entity, err := r.entClient.Client().Submitter.UpdateOneID(id).
		SetStatus(submitter.StatusSUBMITTER_STATUS_DECLINED).
		SetDeclineReason(reason).
		Save(ctx)
	if err != nil {
		r.log.Errorf("decline submitter failed: %s", err.Error())
		return nil, signingV1.ErrorInternalServerError("decline submitter failed")
	}
	return entity, nil
}

// AreAllCompleted checks if all submitters in a submission have completed.
func (r *SubmitterRepo) AreAllCompleted(ctx context.Context, submissionID string) (bool, error) {
	total, err := r.entClient.Client().Submitter.Query().
		Where(submitter.SubmissionIDEQ(submissionID)).
		Count(ctx)
	if err != nil {
		return false, err
	}

	completed, err := r.entClient.Client().Submitter.Query().
		Where(
			submitter.SubmissionIDEQ(submissionID),
			submitter.StatusEQ(submitter.StatusSUBMITTER_STATUS_COMPLETED),
		).
		Count(ctx)
	if err != nil {
		return false, err
	}

	return total > 0 && total == completed, nil
}
