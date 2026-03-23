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

// Submitter holds the schema definition for individual signers in a submission.
type Submitter struct {
	ent.Schema
}

// Annotations of the Submitter.
func (Submitter) Annotations() []schema.Annotation {
	return []schema.Annotation{
		entsql.Annotation{Table: "signing_submitters"},
		entsql.WithComments(true),
	}
}

// Fields of the Submitter.
func (Submitter) Fields() []ent.Field {
	return []ent.Field{
		field.String("id").
			NotEmpty().
			Unique().
			Comment("UUID primary key"),

		field.String("submission_id").
			NotEmpty().
			Comment("Parent submission ID"),

		field.String("name").
			Optional().
			MaxLen(255).
			Comment("Signer display name"),

		field.String("email").
			Optional().
			MaxLen(255).
			Comment("Signer email for notifications"),

		field.String("phone").
			Optional().
			MaxLen(50).
			Comment("Signer phone for SMS"),

		field.String("slug").
			NotEmpty().
			MaxLen(255).
			Unique().
			Comment("Unique slug for signing link"),

		field.Int("signing_order").
			Default(0).
			Comment("Order in sequential signing (0-based)"),

		field.String("role").
			Optional().
			MaxLen(100).
			Comment("Role label (e.g. Buyer, Seller, Witness)"),

		field.Enum("status").
			Values(
				"SUBMITTER_STATUS_UNSPECIFIED",
				"SUBMITTER_STATUS_PENDING",
				"SUBMITTER_STATUS_OPENED",
				"SUBMITTER_STATUS_COMPLETED",
				"SUBMITTER_STATUS_DECLINED",
			).
			Default("SUBMITTER_STATUS_PENDING").
			Comment("Submitter signing status"),

		field.JSON("values", map[string]interface{}{}).
			Optional().
			Comment("Filled form field values"),

		field.JSON("metadata", map[string]interface{}{}).
			Optional().
			Comment("Additional metadata: template_submitter_id, etc."),

		field.String("ip").
			Optional().
			MaxLen(45).
			Comment("IP address of signer"),

		field.String("user_agent").
			Optional().
			MaxLen(512).
			Comment("User agent of signer"),

		field.String("decline_reason").
			Optional().
			MaxLen(1024).
			Comment("Reason for declining"),

		field.Time("sent_at").
			Optional().
			Nillable().
			Comment("When invitation was sent"),

		field.Time("opened_at").
			Optional().
			Nillable().
			Comment("When signer opened the link"),

		field.Time("completed_at").
			Optional().
			Nillable().
			Comment("When signer completed signing"),
	}
}

// Edges of the Submitter.
func (Submitter) Edges() []ent.Edge {
	return []ent.Edge{
		edge.From("submission", Submission.Type).
			Ref("submitters").
			Field("submission_id").
			Unique().
			Required().
			Comment("Parent submission"),
	}
}

// Mixin of the Submitter.
func (Submitter) Mixin() []ent.Mixin {
	return []ent.Mixin{
		mixin.Time{},
		mixin.TenantID[uint32]{},
	}
}

// Indexes of the Submitter.
func (Submitter) Indexes() []ent.Index {
	return []ent.Index{
		index.Fields("submission_id").StorageKey("idx_signing_smtr_submission"),
		index.Fields("slug").Unique().StorageKey("idx_signing_smtr_slug"),
		index.Fields("submission_id", "signing_order").StorageKey("idx_signing_smtr_submission_order"),
		index.Fields("status").StorageKey("idx_signing_smtr_status"),
		index.Fields("email").StorageKey("idx_signing_smtr_email"),
	}
}
