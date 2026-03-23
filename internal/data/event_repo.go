package data

import (
	"context"
	"time"

	"github.com/go-kratos/kratos/v2/log"
	"github.com/google/uuid"
	"github.com/tx7do/kratos-bootstrap/bootstrap"

	entCrud "github.com/tx7do/go-crud/entgo"

	"github.com/go-tangra/go-tangra-signing/internal/data/ent"

	signingV1 "github.com/go-tangra/go-tangra-signing/gen/go/signing/service/v1"
)

type EventRepo struct {
	entClient *entCrud.EntClient[*ent.Client]
	log       *log.Helper
}

func NewEventRepo(ctx *bootstrap.Context, entClient *entCrud.EntClient[*ent.Client]) *EventRepo {
	return &EventRepo{
		log:       ctx.NewLoggerHelper("signing/event/repo"),
		entClient: entClient,
	}
}

// Create creates a new event record.
func (r *EventRepo) Create(ctx context.Context, tenantID uint32, eventType, actorID, resourceType, resourceID string, metadata map[string]interface{}, ip string) error {
	id := uuid.New().String()

	builder := r.entClient.Client().Event.Create().
		SetID(id).
		SetTenantID(tenantID).
		SetEventType(eventType).
		SetResourceType(resourceType).
		SetResourceID(resourceID).
		SetCreateTime(time.Now())

	if actorID != "" {
		builder.SetActorID(actorID)
	}
	if metadata != nil {
		builder.SetMetadata(metadata)
	}
	if ip != "" {
		builder.SetIP(ip)
	}

	_, err := builder.Save(ctx)
	if err != nil {
		r.log.Errorf("create event failed: %s", err.Error())
		return signingV1.ErrorInternalServerError("create event failed")
	}

	return nil
}

// ListBySubmission lists events for a submission.
func (r *EventRepo) ListBySubmission(ctx context.Context, submissionID string) ([]*ent.Event, error) {
	entities, err := r.entClient.Client().Event.Query().
		Where().
		Order(ent.Asc("create_time")).
		All(ctx)
	if err != nil {
		r.log.Errorf("list events failed: %s", err.Error())
		return nil, signingV1.ErrorInternalServerError("list events failed")
	}
	return entities, nil
}
