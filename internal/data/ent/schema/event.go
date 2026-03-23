package schema

import (
	"entgo.io/ent"
	"entgo.io/ent/dialect/entsql"
	"entgo.io/ent/schema"
	"entgo.io/ent/schema/edge"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"
	"github.com/tx7do/go-crud/entgo/mixin"
)

// Event holds the schema definition for audit events.
type Event struct {
	ent.Schema
}

// Annotations of the Event.
func (Event) Annotations() []schema.Annotation {
	return []schema.Annotation{
		entsql.Annotation{Table: "signing_events"},
		entsql.WithComments(true),
	}
}

// Fields of the Event.
func (Event) Fields() []ent.Field {
	return []ent.Field{
		field.String("id").
			NotEmpty().
			Unique().
			Comment("UUID primary key"),

		field.String("event_type").
			NotEmpty().
			MaxLen(100).
			Comment("Event type: submission.created, submitter.completed, etc."),

		field.String("actor_id").
			Optional().
			MaxLen(36).
			Comment("User or system who triggered the event"),

		field.String("resource_type").
			NotEmpty().
			MaxLen(50).
			Comment("Resource type: submission, submitter, template, certificate"),

		field.String("resource_id").
			NotEmpty().
			MaxLen(36).
			Comment("Resource UUID"),

		field.String("submission_id").
			Optional().
			MaxLen(36).
			Comment("Related submission ID for filtering"),

		field.JSON("metadata", map[string]interface{}{}).
			Optional().
			Comment("Additional event data"),

		field.String("ip").
			Optional().
			MaxLen(45).
			Comment("IP address of actor"),
	}
}

// Edges of the Event.
func (Event) Edges() []ent.Edge {
	return []ent.Edge{
		edge.From("submission", Submission.Type).
			Ref("events").
			Field("submission_id").
			Unique().
			Comment("Related submission"),
	}
}

// Mixin of the Event.
func (Event) Mixin() []ent.Mixin {
	return []ent.Mixin{
		mixin.Time{},
		mixin.TenantID[uint32]{},
	}
}

// Indexes of the Event.
func (Event) Indexes() []ent.Index {
	return []ent.Index{
		index.Fields("tenant_id").StorageKey("idx_signing_evt_tenant"),
		index.Fields("event_type").StorageKey("idx_signing_evt_type"),
		index.Fields("resource_type", "resource_id").StorageKey("idx_signing_evt_resource"),
		index.Fields("submission_id").StorageKey("idx_signing_evt_submission"),
		index.Fields("actor_id").StorageKey("idx_signing_evt_actor"),
	}
}
