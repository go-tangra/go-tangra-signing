package event

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/go-kratos/kratos/v2/log"
	"github.com/redis/go-redis/v9"
	"github.com/tx7do/kratos-bootstrap/bootstrap"
)

const (
	channelSubmissionCompleted = "signing.submission.completed"
	channelSubmissionCancelled = "signing.submission.cancelled"
)

// Event is the envelope published to Redis pub/sub.
type Event struct {
	ID        string          `json:"id"`
	Type      string          `json:"type"`
	Source    string          `json:"source"`
	Timestamp time.Time       `json:"timestamp"`
	TenantID  uint32          `json:"tenant_id"`
	Data      json.RawMessage `json:"data"`
}

// SubmissionCompletedData is the payload for submission.completed events.
type SubmissionCompletedData struct {
	SubmissionID     string `json:"submission_id"`
	TemplateID       string `json:"template_id"`
	SignedDocumentKey string `json:"signed_document_key"`
	TenantID         uint32 `json:"tenant_id"`
}

// SubmissionCancelledData is the payload for submission.cancelled events.
type SubmissionCancelledData struct {
	SubmissionID string `json:"submission_id"`
	Reason       string `json:"reason"`
	TenantID     uint32 `json:"tenant_id"`
}

// Publisher publishes signing events to Redis pub/sub.
type Publisher struct {
	redis *redis.Client
	log   *log.Helper
}

// NewPublisher creates a new event publisher.
func NewPublisher(ctx *bootstrap.Context, redisClient *redis.Client) *Publisher {
	return &Publisher{
		redis: redisClient,
		log:   ctx.NewLoggerHelper("signing/event/publisher"),
	}
}

// PublishSubmissionCompleted publishes a submission.completed event.
func (p *Publisher) PublishSubmissionCompleted(ctx context.Context, tenantID uint32, submissionID, templateID, signedDocumentKey string) {
	if p.redis == nil {
		return
	}

	data := SubmissionCompletedData{
		SubmissionID:     submissionID,
		TemplateID:       templateID,
		SignedDocumentKey: signedDocumentKey,
		TenantID:         tenantID,
	}

	p.publish(ctx, channelSubmissionCompleted, tenantID, data)
}

// PublishSubmissionCancelled publishes a submission.cancelled event.
func (p *Publisher) PublishSubmissionCancelled(ctx context.Context, tenantID uint32, submissionID, reason string) {
	if p.redis == nil {
		return
	}

	data := SubmissionCancelledData{
		SubmissionID: submissionID,
		Reason:       reason,
		TenantID:     tenantID,
	}

	p.publish(ctx, channelSubmissionCancelled, tenantID, data)
}

func (p *Publisher) publish(ctx context.Context, channel string, tenantID uint32, payload interface{}) {
	dataBytes, err := json.Marshal(payload)
	if err != nil {
		p.log.Errorf("failed to marshal event data: %v", err)
		return
	}

	evt := Event{
		ID:        fmt.Sprintf("%d-%d", time.Now().UnixNano(), tenantID),
		Type:      channel,
		Source:    "signing-service",
		Timestamp: time.Now().UTC(),
		TenantID:  tenantID,
		Data:      dataBytes,
	}

	evtBytes, err := json.Marshal(evt)
	if err != nil {
		p.log.Errorf("failed to marshal event envelope: %v", err)
		return
	}

	if err := p.redis.Publish(ctx, channel, evtBytes).Err(); err != nil {
		p.log.Errorf("failed to publish event to %s: %v", channel, err)
		return
	}

	p.log.Infof("published event %s for submission %s (tenant %d)", channel, extractSubmissionID(payload), tenantID)
}

func extractSubmissionID(payload interface{}) string {
	switch p := payload.(type) {
	case SubmissionCompletedData:
		return p.SubmissionID
	case SubmissionCancelledData:
		return p.SubmissionID
	default:
		return "unknown"
	}
}
