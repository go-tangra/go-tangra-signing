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

// TemplateFolder holds the schema definition for template organization.
type TemplateFolder struct {
	ent.Schema
}

// Annotations of the TemplateFolder.
func (TemplateFolder) Annotations() []schema.Annotation {
	return []schema.Annotation{
		entsql.Annotation{Table: "signing_template_folders"},
		entsql.WithComments(true),
	}
}

// Fields of the TemplateFolder.
func (TemplateFolder) Fields() []ent.Field {
	return []ent.Field{
		field.String("id").
			NotEmpty().
			Unique().
			Comment("UUID primary key"),

		field.String("parent_id").
			Optional().
			Nillable().
			Comment("Parent folder ID (null for root)"),

		field.String("name").
			NotEmpty().
			MaxLen(255).
			Comment("Folder name"),

		field.String("path").
			Optional().
			MaxLen(4096).
			Comment("Materialized path"),

		field.Int("depth").
			Default(0).
			Comment("Nesting level"),

		field.Int("sort_order").
			Default(0).
			Comment("Sort order within parent"),
	}
}

// Edges of the TemplateFolder.
func (TemplateFolder) Edges() []ent.Edge {
	return []ent.Edge{
		edge.To("templates", Template.Type).
			Comment("Templates in this folder"),

		edge.To("children", TemplateFolder.Type).
			From("parent").
			Field("parent_id").
			Unique().
			Comment("Parent folder"),
	}
}

// Mixin of the TemplateFolder.
func (TemplateFolder) Mixin() []ent.Mixin {
	return []ent.Mixin{
		mixin.CreateBy{},
		mixin.Time{},
		mixin.TenantID[uint32]{},
	}
}

// Indexes of the TemplateFolder.
func (TemplateFolder) Indexes() []ent.Index {
	return []ent.Index{
		index.Fields("tenant_id", "parent_id", "name").Unique().StorageKey("idx_signing_tf_tenant_parent_name"),
		index.Fields("tenant_id").StorageKey("idx_signing_tf_tenant"),
		index.Fields("tenant_id", "path").StorageKey("idx_signing_tf_tenant_path"),
	}
}
