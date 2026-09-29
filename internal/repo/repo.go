// Package repo is the signing module's storage contract. Two implementations
// satisfy it: repodb (pgx over TimescaleDB with row-level security) and
// memstore (the in-memory fake the service tests run against).
//
// Every tenant method takes the tenant explicitly and runs under that
// tenant's RLS scope; methods named *System run under the system scope (the
// scheduled task types, the audit-trail job worker, backups after
// authorisation). Tx runs fn against a transaction-bound Store: everything fn
// does commits or rolls back together, and LockSubmission inside it holds the
// submission's row lock until the end (research D3 — parallel signers are
// serialised). RecordPINFailure is deliberately its own transaction so a
// rolled-back signing still counts the failure (research D5).
package repo

import (
	"context"
	"errors"
	"time"

	"github.com/go-tangra/go-tangra-signing/v4/internal/store"
)

// Sentinel errors.
var (
	ErrNotFound = errors.New("repo: not found")
	ErrConflict = errors.New("repo: conflict") // uniqueness, version or in-use violation
)

// TemplateFilter narrows ListTemplates.
type TemplateFilter struct {
	FolderID *string // nil = any folder; "" = root only
	Tag      string
	Status   string
	Query    string // name contains (case-insensitive)
	Page     int
	PageSize int
}

// SubmissionFilter narrows ListSubmissions.
type SubmissionFilter struct {
	Status     string
	TemplateID string
	Query      string
	CreatedBy  string // "" = anyone
	Page       int
	PageSize   int
}

// CertificateFilter narrows ListCertificates.
type CertificateFilter struct {
	Kind     string
	Status   string
	Query    string // subject or e-mail contains
	OwnerID  string
	Page     int
	PageSize int
}

// InboxItem is one signer slot of the caller with its submission.
type InboxItem struct {
	Signer     store.Signer
	Submission store.Submission
}

// ReminderDue is a signer claimed for one reminder e-mail.
type ReminderDue struct {
	Signer     store.Signer
	Submission store.Submission
	Number     int // 1-based reminder number
}

// SignedDocument is a document signed with a certificate (My certificate page).
type SignedDocument struct {
	SubmissionID   string
	SubmissionName string
	SignerID       string
	SignedAt       time.Time
}

// Store is the complete storage contract.
type Store interface {
	// Tx runs fn in one transaction scoped to tenantID.
	Tx(ctx context.Context, tenantID string, fn func(Store) error) error

	// Folders.
	CreateFolder(ctx context.Context, f store.Folder) error
	GetFolder(ctx context.Context, tenantID, id string) (store.Folder, error)
	ListFolders(ctx context.Context, tenantID string) ([]store.Folder, error)
	UpdateFolder(ctx context.Context, f store.Folder) error
	// RewriteFolderPaths replaces the oldPrefix of every descendant path.
	RewriteFolderPaths(ctx context.Context, tenantID, oldPrefix, newPrefix string) error
	FolderInUse(ctx context.Context, tenantID, id string) (bool, error)
	DeleteFolder(ctx context.Context, tenantID, id string) error

	// Templates.
	CreateTemplate(ctx context.Context, t store.Template) error
	GetTemplate(ctx context.Context, tenantID, id string) (store.Template, error)
	ListTemplates(ctx context.Context, tenantID string, f TemplateFilter) ([]store.Template, int, error)
	// UpdateTemplate stores t when the stored version equals expectedVersion
	// (else ErrConflict) and bumps the version.
	UpdateTemplate(ctx context.Context, t store.Template, expectedVersion int) (store.Template, error)
	// TemplateInUse reports whether a draft or in-progress submission uses it.
	TemplateInUse(ctx context.Context, tenantID, id string) (bool, error)
	DeleteTemplate(ctx context.Context, tenantID, id string) error

	// Submissions, signers, versions.
	CreateSubmission(ctx context.Context, s store.Submission, signers []store.Signer, v0 store.DocumentVersion) error
	GetSubmission(ctx context.Context, tenantID, id string) (store.Submission, []store.Signer, error)
	// SubmissionByIdempotency finds a module-created submission by its
	// idempotency key (module API replays).
	SubmissionByIdempotency(ctx context.Context, tenantID, source, key string) (store.Submission, []store.Signer, error)
	// LockSubmission is GetSubmission holding the row lock until the end of Tx.
	LockSubmission(ctx context.Context, tenantID, id string) (store.Submission, []store.Signer, error)
	ListSubmissions(ctx context.Context, tenantID string, f SubmissionFilter) ([]store.Submission, int, error)
	UpdateSubmission(ctx context.Context, s store.Submission) error
	UpdateSigner(ctx context.Context, s store.Signer) error
	DeleteSubmission(ctx context.Context, tenantID, id string) error
	GetSigner(ctx context.Context, tenantID, id string) (store.Signer, error)
	Inbox(ctx context.Context, tenantID, userID string, signed bool, page, size int) ([]InboxItem, int, error)
	AddVersion(ctx context.Context, v store.DocumentVersion) error
	GetVersion(ctx context.Context, tenantID, submissionID string, version int) (store.DocumentVersion, error)
	ListVersions(ctx context.Context, tenantID, submissionID string) ([]store.DocumentVersion, error)

	// Certificates.
	CreateCertificate(ctx context.Context, c store.Certificate) error
	GetCertificate(ctx context.Context, tenantID, id string) (store.Certificate, error)
	ListCertificates(ctx context.Context, tenantID string, f CertificateFilter) ([]store.Certificate, int, error)
	UpdateCertificate(ctx context.Context, c store.Certificate) error
	// ActiveSignerCertificate is the owner's active signer certificate.
	ActiveSignerCertificate(ctx context.Context, tenantID, userID string) (store.Certificate, error)
	// CurrentCA is the tenant's active CA that has not been superseded.
	CurrentCA(ctx context.Context, tenantID string) (store.Certificate, error)
	// ListCAs lists every CA of the tenant (current and previous).
	ListCAs(ctx context.Context, tenantID string) ([]store.Certificate, error)
	// IssuedBy lists certificates of an issuer (CRL building).
	IssuedBy(ctx context.Context, tenantID, issuerID string) ([]store.Certificate, error)
	// SystemCertificate is the active audit-trail certificate of an issuer.
	SystemCertificate(ctx context.Context, tenantID, issuerID string) (store.Certificate, error)
	// RecordPINFailure increments the counter in its own transaction; at
	// lockAfter failures it sets locked_until and resets the counter.
	RecordPINFailure(ctx context.Context, tenantID, certID string, lockAfter int, lockUntil time.Time) (failures int, locked bool, err error)
	ResetPINFailures(ctx context.Context, tenantID, certID string) error
	SignedWithCertificate(ctx context.Context, tenantID, certID string, limit int) ([]SignedDocument, error)

	// QES preparations.
	CreateQES(ctx context.Context, q store.QESPreparation) error
	GetQES(ctx context.Context, tenantID, id string) (store.QESPreparation, error)
	MarkQESUsed(ctx context.Context, tenantID, id string, at time.Time) error

	// History.
	AddEvent(ctx context.Context, e store.Event) error
	ListEvents(ctx context.Context, tenantID, submissionID string) ([]store.Event, error)

	// Jobs.
	EnqueueJob(ctx context.Context, j store.Job) error
	ClaimJobsSystem(ctx context.Context, now time.Time, limit int, lease time.Duration) ([]store.Job, error)
	CompleteJobSystem(ctx context.Context, id string, at time.Time) error
	FailJobSystem(ctx context.Context, id, reason string, next time.Time) error

	// Scheduled work (system scope, research D12).
	ExpireDueSystem(ctx context.Context, now time.Time, limit int) ([]store.Submission, error)
	ClaimRemindersSystem(ctx context.Context, now time.Time, limit int) ([]ReminderDue, error)
	ExpiredQESSystem(ctx context.Context, now time.Time, limit int) ([]store.QESPreparation, error)
	DeleteQESSystem(ctx context.Context, id string) error
	DueCRLsSystem(ctx context.Context, now time.Time, limit int) ([]store.Certificate, error)
	TenantsSystem(ctx context.Context) ([]string, error)

	// Audit.
	AppendAudit(ctx context.Context, row store.AuditRow) error
}
