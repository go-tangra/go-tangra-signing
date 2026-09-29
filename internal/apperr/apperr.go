// Package apperr is the signing module's domain refusal: a stable reason from
// the closed vocabulary of the API (api/openapi/signing.yaml Error.reason),
// its HTTP status, and optionally the offending field and a content-safe
// detail. Services return *Error for every expected refusal; the HTTP layer
// writes it verbatim. Anything else is an internal failure (503, logged).
package apperr

import (
	"errors"
	"net/http"
)

// Error is a domain refusal.
type Error struct {
	Status int
	Reason string
	Field  string         // offending field name or id (never its value)
	Detail map[string]any // content-safe detail (counts, times, attempts left)
}

func (e *Error) Error() string {
	if e.Field != "" {
		return e.Reason + ": " + e.Field
	}
	return e.Reason
}

// Is matches errors of the same reason (errors.Is(err, apperr.PINInvalid)).
func (e *Error) Is(target error) bool {
	t, ok := target.(*Error)
	return ok && t.Reason == e.Reason
}

// WithField returns a copy naming the offending field.
func (e *Error) WithField(f string) *Error { c := *e; c.Field = f; return &c }

// WithDetail returns a copy with detail.
func (e *Error) WithDetail(d map[string]any) *Error { c := *e; c.Detail = d; return &c }

func r(status int, reason string) *Error { return &Error{Status: status, Reason: reason} }

// Refusals (the API's closed vocabulary).
var (
	NotFound               = r(http.StatusNotFound, "not_found")
	Forbidden              = r(http.StatusForbidden, "forbidden")
	Validation             = r(http.StatusUnprocessableEntity, "validation_failed")
	Conflict               = r(http.StatusConflict, "conflict")
	InvalidPDF             = r(http.StatusBadRequest, "invalid_pdf")
	PayloadTooLarge        = r(http.StatusRequestEntityTooLarge, "payload_too_large")
	InvalidField           = r(http.StatusUnprocessableEntity, "invalid_field")
	InvalidRule            = r(http.StatusUnprocessableEntity, "invalid_rule")
	VersionConflict        = r(http.StatusConflict, "version_conflict")
	TemplateInUse          = r(http.StatusConflict, "template_in_use")
	TemplateNotActive      = r(http.StatusUnprocessableEntity, "template_not_active")
	FolderNotEmpty         = r(http.StatusConflict, "folder_not_empty")
	NameTaken              = r(http.StatusConflict, "name_taken")
	InvalidSigner          = r(http.StatusUnprocessableEntity, "invalid_signer")
	InvalidPrefill         = r(http.StatusUnprocessableEntity, "invalid_prefill")
	NotDraft               = r(http.StatusConflict, "not_draft")
	SignerFinal            = r(http.StatusConflict, "signer_final")
	MissingRequired        = r(http.StatusUnprocessableEntity, "missing_required")
	InvalidValue           = r(http.StatusUnprocessableEntity, "invalid_value")
	PINInvalid             = r(http.StatusForbidden, "pin_invalid")
	CertificateLocked      = r(http.StatusLocked, "certificate_locked")
	CertificateMissing     = r(http.StatusConflict, "certificate_missing")
	CertificateUnusable    = r(http.StatusConflict, "certificate_unusable")
	CertificateExists      = r(http.StatusConflict, "certificate_exists")
	NotYourTurn            = r(http.StatusConflict, "not_your_turn")
	SubmissionNotOpen      = r(http.StatusConflict, "submission_not_open")
	AlreadySigned          = r(http.StatusConflict, "already_signed")
	RateLimited            = r(http.StatusTooManyRequests, "rate_limited")
	QESSignatureInvalid    = r(http.StatusUnprocessableEntity, "qes_signature_invalid")
	PreparationExpired     = r(http.StatusGone, "preparation_expired")
	DocumentChanged        = r(http.StatusConflict, "document_changed")
	TSAFailed              = r(http.StatusBadGateway, "tsa_failed")
	ContactMissing         = r(http.StatusUnprocessableEntity, "contact_missing")
	BackupTooLarge         = r(http.StatusRequestEntityTooLarge, "backup_too_large")
	InvalidBackup          = r(http.StatusUnprocessableEntity, "invalid_backup")
	TemporarilyUnavailable = r(http.StatusServiceUnavailable, "temporarily_unavailable")
)

// As returns the *Error in err's chain.
func As(err error) (*Error, bool) {
	var e *Error
	ok := errors.As(err, &e)
	return e, ok
}
