// Package store holds the signing module's persistence primitives: the pgx
// pool with tenant-scoped (RLS) transactions, the embedded goose migrations,
// UUIDv7 ids and the row models shared by the repository implementations
// (specs/027-signing-v4/data-model.md).
package store

import "time"

// NilTenant is pinned under the system scope so the RLS cast stays valid; it
// is never a real tenant.
const NilTenant = "00000000-0000-0000-0000-000000000000"

// Template statuses.
const (
	TemplateDraft    = "draft"
	TemplateActive   = "active"
	TemplateArchived = "archived"
)

// Submission modes and statuses.
const (
	ModeSequential = "sequential"
	ModeParallel   = "parallel"

	SubmissionDraft      = "draft"
	SubmissionInProgress = "in_progress"
	SubmissionCompleted  = "completed"
	SubmissionExpired    = "expired"
	SubmissionCancelled  = "cancelled"
)

// Signer statuses and signing methods.
const (
	SignerPending  = "pending"
	SignerInvited  = "invited"
	SignerOpened   = "opened"
	SignerSigned   = "signed"
	SignerDeclined = "declined"

	MethodLocal = "local_certificate"
	MethodQES   = "qes"
)

// Certificate kinds, key protections and statuses.
const (
	KindCA     = "ca"
	KindSystem = "system"
	KindSigner = "signer"
	KindAdmin  = "admin"

	KeyPIN    = "pin"
	KeySealed = "sealed"

	CertActive       = "active"
	CertRevoked      = "revoked"
	CertExpired      = "expired"
	CertNeedsReissue = "needs_reissue"
)

// Job kinds.
const JobAuditTrail = "audit_trail"

// Folder is a template folder (nested, materialized path).
type Folder struct {
	ID        string
	TenantID  string
	ParentID  *string
	Name      string
	Path      string
	SortOrder int
	CreatedAt time.Time
	CreatedBy string
	UpdatedAt time.Time
	UpdatedBy string
}

// Party is one signing role of a template ("Employee").
type Party struct {
	Key  string `json:"key"`
	Name string `json:"name"`
}

// Rule is one condition on another field's value.
type Rule struct {
	Field string `json:"field"`
	Op    string `json:"op"`
	Value string `json:"value,omitempty"`
}

// Conditions make a field visible or required depending on other fields.
type Conditions struct {
	Mode   string `json:"mode"`   // all | any
	Effect string `json:"effect"` // visible | required
	Rules  []Rule `json:"rules"`
}

// Field is one placed field. Geometry is a fraction of the page (0..1).
type Field struct {
	ID         string      `json:"id"`
	Name       string      `json:"name"`
	Type       string      `json:"type"`
	Party      string      `json:"party"`
	Page       int         `json:"page"`
	X          float64     `json:"x"`
	Y          float64     `json:"y"`
	W          float64     `json:"w"`
	H          float64     `json:"h"`
	Required   bool        `json:"required,omitempty"`
	Font       string      `json:"font,omitempty"`
	FontSize   float64     `json:"font_size,omitempty"`
	Options    []string    `json:"options,omitempty"`
	Default    string      `json:"default,omitempty"`
	Conditions *Conditions `json:"conditions,omitempty"`
	Formula    string      `json:"formula,omitempty"`
	Prefill    string      `json:"prefill,omitempty"` // submissions only
}

// Reminder settings of a template default or a submission.
type Reminder struct {
	IntervalDays int `json:"interval_days"`
	Max          int `json:"max"`
}

// Template is a signing template.
type Template struct {
	ID                string
	TenantID          string
	FolderID          *string
	Name              string
	Description       string
	Tags              []string
	Status            string
	PDFKey            string
	PDFSHA256         string
	PDFSize           int64
	PDFPages          int
	FileName          string
	PDFSigned         bool
	Parties           []Party
	Fields            []Field
	DefaultExpiryDays *int
	DefaultReminder   *Reminder
	Version           int
	CreatedAt         time.Time
	CreatedBy         string
	UpdatedAt         time.Time
	UpdatedBy         string
}

// Submission is one document sent for signature.
type Submission struct {
	ID               string
	TenantID         string
	TemplateID       string
	Name             string
	PDFKey           string
	PDFSHA256        string
	Fields           []Field
	Parties          []Party
	Mode             string
	Status           string
	ExpiresAt        *time.Time
	Reminder         *Reminder
	CurrentVersion   int
	FinalVersion     *int
	AuditTrailKey    string
	AuditTrailSHA256 string
	SentAt           *time.Time
	CompletedAt      *time.Time
	CancelledAt      *time.Time
	CancelReason     string
	CreatedAt        time.Time
	CreatedBy        string
	UpdatedAt        time.Time
}

// Open reports whether signers may act on the submission at now.
func (s Submission) Open(now time.Time) bool {
	return s.Status == SubmissionInProgress && (s.ExpiresAt == nil || now.Before(*s.ExpiresAt))
}

// Signer is one signer slot of a submission.
type Signer struct {
	ID             string
	TenantID       string
	SubmissionID   string
	UserID         string
	Name           string
	Email          string
	Party          string
	Position       int
	Status         string
	Values         map[string]string
	Method         string
	CertificateID  *string
	CertSubject    string
	CertSerial     string
	CertIssuer     string
	IP             string
	UserAgent      string
	DeclineReason  string
	InvitedAt      *time.Time
	OpenedAt       *time.Time
	SignedAt       *time.Time
	DeclinedAt     *time.Time
	RemindersSent  int
	NextReminderAt *time.Time
	MailError      string
}

// Final reports whether the signer has signed or declined.
func (s Signer) Final() bool { return s.Status == SignerSigned || s.Status == SignerDeclined }

// DocumentVersion is one stored version of a submission's PDF.
type DocumentVersion struct {
	SubmissionID string
	Version      int
	TenantID     string
	ObjectKey    string
	SHA256       string
	Size         int64
	SignerID     *string
	CreatedAt    time.Time
}

// Certificate is a signing certificate (tenant CA, system, signer or admin).
type Certificate struct {
	ID               string
	TenantID         string
	Kind             string
	IssuerID         *string
	OwnerUserID      *string
	SubjectCN        string
	Email            string
	Serial           string
	NotBefore        time.Time
	NotAfter         time.Time
	CertDER          []byte
	KeyProtection    string
	KeyBlob          []byte
	Status           string
	RevokedAt        *time.Time
	RevocationReason string
	FailedPINCount   int
	LockedUntil      *time.Time
	CRLDER           []byte
	CRLNextUpdate    *time.Time
	SupersededBy     *string
	CreatedAt        time.Time
	CreatedBy        string
}

// Usable reports whether the certificate may sign at now.
func (c Certificate) Usable(now time.Time) bool {
	return c.Status == CertActive && !now.Before(c.NotBefore) && now.Before(c.NotAfter) &&
		(c.LockedUntil == nil || !now.Before(*c.LockedUntil))
}

// QESPreparation is a prepared qualified signature awaiting the card signature.
type QESPreparation struct {
	ID             string
	TenantID       string
	SignerID       string
	ChainDER       [][]byte
	SignedAttrs    []byte
	Digest         []byte
	PreparedKey    string
	BasedOnVersion int
	Values         map[string]string
	ExpiresAt      time.Time
	UsedAt         *time.Time
	CreatedAt      time.Time
}

// Event is one signing-history entry (FR-044); Meta never holds field values.
type Event struct {
	ID            string
	TenantID      string
	SubmissionID  *string
	SignerID      *string
	CertificateID *string
	ActorUserID   *string
	Type          string
	IP            string
	Meta          map[string]any
	At            time.Time
}

// Job is a queued background job (audit-trail generation).
type Job struct {
	ID            string
	TenantID      string
	Kind          string
	SubmissionID  string
	Attempts      int
	NextAttemptAt time.Time
	LastError     string
	DoneAt        *time.Time
	CreatedAt     time.Time
}

// AuditRow is an append-only audit event.
type AuditRow struct {
	ID          string
	TenantID    string
	At          time.Time
	ActorKind   string
	ActorID     string
	Action      string
	SubjectKind string
	SubjectID   string
	Outcome     string
	Reason      string
	Detail      map[string]any
}

// Page normalises a page/page size pair (1-based page, size 1..max).
func Page(page, size, max int) (int, int) {
	if max <= 0 {
		max = 100
	}
	if page < 1 {
		page = 1
	}
	if size < 1 {
		size = 25
	}
	if size > max {
		size = max
	}
	return page, size
}
