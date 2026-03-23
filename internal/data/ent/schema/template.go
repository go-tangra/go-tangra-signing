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

// TemplateFieldDef represents a field definition stored as JSON.
// Position fields are percentage-based coordinates set via the visual field builder.
type TemplateFieldDef struct {
	ID             string  `json:"id"`
	Name           string  `json:"name"`
	Type           string  `json:"type"`            // text, number, signature, initials, date, checkbox, select, radio, image, file, cells, stamp, payment
	Required       bool    `json:"required"`
	PageNumber     int     `json:"page_number"`
	XPercent       float64 `json:"x_percent"`
	YPercent       float64 `json:"y_percent"`
	WidthPercent   float64 `json:"width_percent"`
	HeightPercent  float64 `json:"height_percent"`
	SubmitterIndex int     `json:"submitter_index"` // which signer this field belongs to
	Font           string  `json:"font,omitempty"`  // font name detected from the PDF placeholder
	FontSize       float64 `json:"font_size,omitempty"` // font size in points detected from the PDF placeholder
}

// Template holds the schema definition for signing templates.
// Templates contain a PDF file with placeholder fields for document signing.
type Template struct {
	ent.Schema
}

// Annotations of the Template.
func (Template) Annotations() []schema.Annotation {
	return []schema.Annotation{
		entsql.Annotation{Table: "signing_templates"},
		entsql.WithComments(true),
	}
}

// Fields of the Template.
func (Template) Fields() []ent.Field {
	return []ent.Field{
		field.String("id").
			NotEmpty().
			Unique().
			Comment("UUID primary key"),

		field.String("folder_id").
			Optional().
			Nillable().
			Comment("Parent folder ID for organization"),

		field.String("name").
			NotEmpty().
			MaxLen(255).
			Comment("Template display name"),

		field.String("slug").
			NotEmpty().
			MaxLen(255).
			Unique().
			Comment("URL-friendly unique identifier"),

		field.String("description").
			Optional().
			MaxLen(4096).
			Comment("Template description"),

		field.String("file_key").
			NotEmpty().
			MaxLen(512).
			Comment("Storage key in RustFS/S3"),

		field.String("file_name").
			NotEmpty().
			MaxLen(255).
			Comment("Original uploaded file name"),

		field.Int64("file_size").
			Default(0).
			Comment("File size in bytes"),

		field.JSON("fields", []TemplateFieldDef{}).
			Optional().
			Comment("Field definitions with positions from the visual builder"),

		field.Enum("status").
			Values(
				"TEMPLATE_STATUS_UNSPECIFIED",
				"TEMPLATE_STATUS_DRAFT",
				"TEMPLATE_STATUS_ACTIVE",
				"TEMPLATE_STATUS_ARCHIVED",
			).
			Default("TEMPLATE_STATUS_DRAFT").
			Comment("Template lifecycle status"),

		field.String("source").
			Optional().
			MaxLen(50).
			Comment("Source: pdf_upload, blank, cloned"),

		field.JSON("tags", []string{}).
			Optional().
			Comment("Tags for categorization"),
	}
}

// Edges of the Template.
func (Template) Edges() []ent.Edge {
	return []ent.Edge{
		edge.From("folder", TemplateFolder.Type).
			Ref("templates").
			Field("folder_id").
			Unique().
			Comment("Parent folder"),

		edge.To("submissions", Submission.Type).
			Comment("Submissions created from this template"),
	}
}

// Mixin of the Template.
func (Template) Mixin() []ent.Mixin {
	return []ent.Mixin{
		mixin.CreateBy{},
		mixin.UpdateBy{},
		mixin.Time{},
		mixin.TenantID[uint32]{},
	}
}

// Indexes of the Template.
func (Template) Indexes() []ent.Index {
	return []ent.Index{
		index.Fields("tenant_id", "name").StorageKey("idx_signing_tpl_tenant_name"),
		index.Fields("tenant_id").StorageKey("idx_signing_tpl_tenant"),
		index.Fields("slug").Unique().StorageKey("idx_signing_tpl_slug"),
		index.Fields("status").StorageKey("idx_signing_tpl_status"),
		index.Fields("tenant_id", "folder_id").StorageKey("idx_signing_tpl_tenant_folder"),
	}
}
