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

// Submission holds the schema definition for signing submissions.
// A submission is an instance of a template sent to signers.
type Submission struct {
	ent.Schema
}

// Annotations of the Submission.
func (Submission) Annotations() []schema.Annotation {
	return []schema.Annotation{
		entsql.Annotation{Table: "signing_submissions"},
		entsql.WithComments(true),
	}
}

// Fields of the Submission.
func (Submission) Fields() []ent.Field {
	return []ent.Field{
		field.String("id").
			NotEmpty().
			Unique().
			Comment("UUID primary key"),

		field.String("template_id").
			NotEmpty().
			Comment("Source template ID"),

		field.String("slug").
			NotEmpty().
			MaxLen(255).
			Unique().
			Comment("URL-friendly unique identifier"),

		field.Enum("signing_mode").
			Values(
				"SIGNING_MODE_UNSPECIFIED",
				"SIGNING_MODE_SEQUENTIAL",
				"SIGNING_MODE_PARALLEL",
			).
			Default("SIGNING_MODE_SEQUENTIAL").
			Comment("Sequential (one-by-one) or parallel (all at once)"),

		field.Enum("status").
			Values(
				"SUBMISSION_STATUS_UNSPECIFIED",
				"SUBMISSION_STATUS_DRAFT",
				"SUBMISSION_STATUS_PENDING",
				"SUBMISSION_STATUS_IN_PROGRESS",
				"SUBMISSION_STATUS_COMPLETED",
				"SUBMISSION_STATUS_EXPIRED",
				"SUBMISSION_STATUS_CANCELLED",
			).
			Default("SUBMISSION_STATUS_DRAFT").
			Comment("Submission lifecycle status"),

		field.JSON("template_fields_snapshot", []map[string]interface{}{}).
			Optional().
			Comment("Snapshot of template fields at creation time"),

		field.JSON("template_schema_snapshot", map[string]interface{}{}).
			Optional().
			Comment("Snapshot of template schema at creation time"),

		field.JSON("template_submitters_snapshot", []map[string]interface{}{}).
			Optional().
			Comment("Snapshot of template submitters at creation time"),

		field.JSON("preferences", map[string]interface{}{}).
			Optional().
			Comment("Submission preferences: reminders, expiration, locale"),

		field.Time("completed_at").
			Optional().
			Nillable().
			Comment("When all signers completed"),

		field.Time("expires_at").
			Optional().
			Nillable().
			Comment("Submission expiration deadline"),

		field.String("source").
			Optional().
			MaxLen(50).
			Comment("Source: direct_link, form_based, api, bulk"),

		field.String("current_pdf_key").
			Optional().
			MaxLen(512).
			Comment("Storage key for the in-progress PDF with completed signers' data overlaid"),

		field.String("signed_document_key").
			Optional().
			MaxLen(512).
			Comment("Storage key for the final signed PDF"),

		field.String("audit_trail_key").
			Optional().
			MaxLen(512).
			Comment("Storage key for the audit trail PDF"),
	}
}

// Edges of the Submission.
func (Submission) Edges() []ent.Edge {
	return []ent.Edge{
		edge.From("template", Template.Type).
			Ref("submissions").
			Field("template_id").
			Unique().
			Required().
			Comment("Source template"),

		edge.To("submitters", Submitter.Type).
			Comment("Signers for this submission"),

		edge.To("events", Event.Type).
			Comment("Events for this submission"),
	}
}

// Mixin of the Submission.
func (Submission) Mixin() []ent.Mixin {
	return []ent.Mixin{
		mixin.CreateBy{},
		mixin.UpdateBy{},
		mixin.Time{},
		mixin.TenantID[uint32]{},
	}
}

// Indexes of the Submission.
func (Submission) Indexes() []ent.Index {
	return []ent.Index{
		index.Fields("tenant_id").StorageKey("idx_signing_sub_tenant"),
		index.Fields("slug").Unique().StorageKey("idx_signing_sub_slug"),
		index.Fields("template_id").StorageKey("idx_signing_sub_template"),
		index.Fields("status").StorageKey("idx_signing_sub_status"),
		index.Fields("tenant_id", "status").StorageKey("idx_signing_sub_tenant_status"),
		index.Fields("tenant_id", "template_id").StorageKey("idx_signing_sub_tenant_template"),
	}
}
