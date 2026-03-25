package schema

import (
	"entgo.io/ent"
	"entgo.io/ent/dialect/entsql"
	"entgo.io/ent/schema"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"
	"github.com/tx7do/go-crud/entgo/mixin"
)

// Certificate holds the schema definition for X.509 certificates.
type Certificate struct {
	ent.Schema
}

// Annotations of the Certificate.
func (Certificate) Annotations() []schema.Annotation {
	return []schema.Annotation{
		entsql.Annotation{Table: "signing_certificates"},
		entsql.WithComments(true),
	}
}

// Fields of the Certificate.
func (Certificate) Fields() []ent.Field {
	return []ent.Field{
		field.String("id").
			NotEmpty().
			Unique().
			Comment("UUID primary key"),

		field.String("subject_cn").
			NotEmpty().
			MaxLen(255).
			Comment("Certificate Subject Common Name"),

		field.String("subject_org").
			Optional().
			MaxLen(255).
			Comment("Certificate Subject Organization"),

		field.String("serial_number").
			NotEmpty().
			MaxLen(255).
			Comment("Certificate serial number (hex)"),

		field.Time("not_before").
			Comment("Certificate validity start"),

		field.Time("not_after").
			Comment("Certificate validity end"),

		field.Bool("is_ca").
			Default(false).
			Comment("Whether this is a CA certificate"),

		field.String("parent_id").
			Optional().
			Nillable().
			Comment("Issuer certificate ID (null for root CA)"),

		field.String("key_pem_encrypted").
			Optional().
			Sensitive().
			Comment("PEM-encoded private key (encrypted at rest)"),

		field.Text("cert_pem").
			NotEmpty().
			Comment("PEM-encoded certificate"),

		field.Text("crl_pem").
			Optional().
			Comment("PEM-encoded Certificate Revocation List"),

		field.Enum("status").
			Values(
				"CERT_STATUS_UNSPECIFIED",
				"CERT_STATUS_ACTIVE",
				"CERT_STATUS_REVOKED",
				"CERT_STATUS_EXPIRED",
			).
			Default("CERT_STATUS_ACTIVE").
			Comment("Certificate lifecycle status"),

		field.Enum("key_algorithm").
			Values(
				"KEY_ALGO_UNSPECIFIED",
				"KEY_ALGO_ECDSA_P256",
				"KEY_ALGO_ECDSA_P384",
				"KEY_ALGO_RSA_2048",
				"KEY_ALGO_RSA_4096",
			).
			Default("KEY_ALGO_ECDSA_P256").
			Comment("Key algorithm used"),

		field.Time("revoked_at").
			Optional().
			Nillable().
			Comment("When the certificate was revoked"),

		field.String("revocation_reason").
			Optional().
			MaxLen(255).
			Comment("Reason for revocation"),

		field.String("user_email").
			Optional().
			MaxLen(255).
			Comment("Email of the user this certificate belongs to"),

		field.String("user_id").
			Optional().
			MaxLen(255).
			Comment("Platform user ID this certificate belongs to"),

		field.String("setup_token").
			Optional().
			MaxLen(255).
			Comment("Unique token for certificate setup page"),

		field.Bool("setup_completed").
			Default(false).
			Comment("Whether the certificate PIN setup has been completed"),
	}
}

// Edges of the Certificate.
func (Certificate) Edges() []ent.Edge {
	return nil
}

// Mixin of the Certificate.
func (Certificate) Mixin() []ent.Mixin {
	return []ent.Mixin{
		mixin.CreateBy{},
		mixin.Time{},
		mixin.TenantID[uint32]{},
	}
}

// Indexes of the Certificate.
func (Certificate) Indexes() []ent.Index {
	return []ent.Index{
		index.Fields("tenant_id").StorageKey("idx_signing_cert_tenant"),
		index.Fields("serial_number").Unique().StorageKey("idx_signing_cert_serial"),
		index.Fields("tenant_id", "subject_cn").StorageKey("idx_signing_cert_tenant_cn"),
		index.Fields("status").StorageKey("idx_signing_cert_status"),
		index.Fields("is_ca").StorageKey("idx_signing_cert_is_ca"),
		index.Fields("parent_id").StorageKey("idx_signing_cert_parent"),
		index.Fields("not_after").StorageKey("idx_signing_cert_not_after"),
		index.Fields("tenant_id", "user_email").StorageKey("idx_signing_cert_tenant_email"),
		index.Fields("tenant_id", "user_id").StorageKey("idx_signing_cert_tenant_user"),
		index.Fields("setup_token").Unique().StorageKey("idx_signing_cert_setup_token"),
	}
}
