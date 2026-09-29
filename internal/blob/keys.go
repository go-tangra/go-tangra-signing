package blob

import (
	"fmt"
	"strings"
)

// Object keys of the signing bucket (data-model.md "Objects"). Every key
// starts with the tenant prefix, so a key built for one tenant can never name
// another tenant's object, and the ids are server-generated UUIDs (never user
// input).

// TenantPrefix is the prefix of every object of a tenant.
func TenantPrefix(tenantID string) string { return "tenants/" + tenantID + "/" }

// TemplatePDF is an uploaded template PDF (immutable; a re-upload gets a new object id).
func TemplatePDF(tenantID, templateID, objectID string) string {
	return TenantPrefix(tenantID) + "templates/" + templateID + "/" + objectID + ".pdf"
}

// SubmissionPrefix holds every object of one submission.
func SubmissionPrefix(tenantID, submissionID string) string {
	return TenantPrefix(tenantID) + "submissions/" + submissionID + "/"
}

// DocumentVersion is version n of a submission's document.
func DocumentVersion(tenantID, submissionID string, version int) string {
	return fmt.Sprintf("%sv%d.pdf", SubmissionPrefix(tenantID, submissionID), version)
}

// AuditTrail is a submission's audit-trail PDF.
func AuditTrail(tenantID, submissionID string) string {
	return SubmissionPrefix(tenantID, submissionID) + "audit-trail.pdf"
}

// SignerUpload is a signature/image/file field upload of one signer.
func SignerUpload(tenantID, submissionID, signerID, fieldID, ext string) string {
	return SubmissionPrefix(tenantID, submissionID) + "signer/" + signerID + "/" + fieldID + "." + ext
}

// QESPrepared is a prepared qualified-signature document.
func QESPrepared(tenantID, preparationID string) string {
	return TenantPrefix(tenantID) + "qes/" + preparationID + ".pdf"
}

// Upload is a transient upload (verification, administrator signing).
func Upload(tenantID, uploadID string) string {
	return TenantPrefix(tenantID) + "uploads/" + uploadID + ".pdf"
}

// InTenant reports whether key belongs to tenantID (defence in depth before
// serving a stored key).
func InTenant(key, tenantID string) bool {
	return tenantID != "" && strings.HasPrefix(key, TenantPrefix(tenantID)) && !strings.Contains(key, "..")
}
