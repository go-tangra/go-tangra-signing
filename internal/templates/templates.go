// Package templates manages signing templates and their folders (US1): PDF
// upload through the bounded opener, the field builder's save with
// validation and optimistic versions, clone, archive, delete, the template
// PDF download and placeholder detection. The HTTP layer has already checked
// the route permission; every call is scoped to the caller's tenant and a
// foreign id is "not found".
package templates

import (
	"bytes"
	"context"
	"errors"
	"io"
	"strings"
	"time"

	"github.com/go-tangra/go-tangra-signing/v4/internal/apperr"
	"github.com/go-tangra/go-tangra-signing/v4/internal/audit"
	"github.com/go-tangra/go-tangra-signing/v4/internal/authz"
	"github.com/go-tangra/go-tangra-signing/v4/internal/blob"
	"github.com/go-tangra/go-tangra-signing/v4/internal/pdf/detect"
	"github.com/go-tangra/go-tangra-signing/v4/internal/pdf/limits"
	"github.com/go-tangra/go-tangra-signing/v4/internal/repo"
	"github.com/go-tangra/go-tangra-signing/v4/internal/store"
)

// MaxFolderDepth bounds folder nesting.
const MaxFolderDepth = 8

// Limits bound templates.
type Limits struct {
	MaxPDFBytes  int64
	MaxPages     int
	MaxFields    int
	MaxParties   int
	ParseTimeout time.Duration
	MaxPageSize  int
}

// RuleValidator checks conditions and formulas in depth; nil means
// ValidateRules (package rules, US6).
type RuleValidator func(fields []store.Field) error

// Deps wire the service.
type Deps struct {
	Store  repo.Store
	Blob   blob.Store
	Audit  audit.Recorder
	Now    func() time.Time
	Limits Limits
	Rules  RuleValidator
}

// Service manages templates and folders.
type Service struct{ d Deps }

// New builds the service.
func New(d Deps) *Service {
	if d.Now == nil {
		d.Now = func() time.Time { return time.Now().UTC() }
	}
	if d.Limits.MaxPageSize <= 0 {
		d.Limits.MaxPageSize = 200
	}
	if d.Rules == nil {
		d.Rules = ValidateRules
	}
	return &Service{d: d}
}

func (s *Service) record(ctx context.Context, subj authz.Subjects, t audit.EventType, kind, id, outcome, reason string, detail map[string]any) {
	audit.Emit(ctx, s.d.Audit, audit.Event{TenantID: subj.TenantID, EventType: t, ActorKind: audit.ActorOf(subj.ActorKind),
		ActorID: subj.ActorID(), SubjectKind: kind, SubjectID: id, Outcome: outcome, Reason: reason, Details: detail})
}

// notFound maps repo.ErrNotFound to the API refusal.
func notFound(err error) error {
	if errors.Is(err, repo.ErrNotFound) {
		return apperr.NotFound
	}
	return err
}

// ---------------------------------------------------------------- folders

// FolderInput creates or changes a folder; nil fields are left unchanged on update.
type FolderInput struct {
	Name      *string
	ParentID  **string // non-nil sets the parent (nil value = root)
	SortOrder *int
}

func cleanName(n string, max int, field string) (string, error) {
	n = strings.TrimSpace(n)
	if n == "" || len(n) > max || strings.ContainsAny(n, "/\r\n") {
		return "", apperr.Validation.WithField(field)
	}
	return n, nil
}

// ListFolders returns the tenant's folders ordered by path.
func (s *Service) ListFolders(ctx context.Context, subj authz.Subjects) ([]store.Folder, error) {
	return s.d.Store.ListFolders(ctx, subj.TenantID)
}

// parentPath returns the path and depth under parent (nil = root).
func (s *Service) parentPath(ctx context.Context, tenant string, parent *string) (string, int, error) {
	if parent == nil {
		return "", 0, nil
	}
	p, err := s.d.Store.GetFolder(ctx, tenant, *parent)
	if err != nil {
		return "", 0, notFound(err)
	}
	return p.Path, strings.Count(p.Path, "/"), nil
}

// CreateFolder creates a folder.
func (s *Service) CreateFolder(ctx context.Context, subj authz.Subjects, in FolderInput) (store.Folder, error) {
	if in.Name == nil {
		return store.Folder{}, apperr.Validation.WithField("name")
	}
	name, err := cleanName(*in.Name, 120, "name")
	if err != nil {
		return store.Folder{}, err
	}
	var parent *string
	if in.ParentID != nil {
		parent = *in.ParentID
	}
	base, depth, err := s.parentPath(ctx, subj.TenantID, parent)
	if err != nil {
		return store.Folder{}, err
	}
	if depth >= MaxFolderDepth {
		return store.Folder{}, apperr.Validation.WithField("parent_id")
	}
	now := s.d.Now()
	f := store.Folder{ID: store.NewID(), TenantID: subj.TenantID, ParentID: parent, Name: name, Path: base + "/" + name,
		CreatedAt: now, CreatedBy: subj.ActorID(), UpdatedAt: now, UpdatedBy: subj.ActorID()}
	if in.SortOrder != nil {
		f.SortOrder = *in.SortOrder
	}
	if err := s.d.Store.CreateFolder(ctx, f); err != nil {
		if errors.Is(err, repo.ErrConflict) {
			return store.Folder{}, apperr.NameTaken.WithField("name")
		}
		return store.Folder{}, err
	}
	s.record(ctx, subj, audit.FolderCreate, audit.SubjectFolder, f.ID, audit.OutcomeOK, "", nil)
	return f, nil
}

// UpdateFolder renames, moves or re-sorts a folder; descendant paths follow.
func (s *Service) UpdateFolder(ctx context.Context, subj authz.Subjects, id string, in FolderInput) (out store.Folder, err error) {
	err = s.d.Store.Tx(ctx, subj.TenantID, func(r repo.Store) error {
		f, err := r.GetFolder(ctx, subj.TenantID, id)
		if err != nil {
			return notFound(err)
		}
		oldPath := f.Path
		if in.Name != nil {
			if f.Name, err = cleanName(*in.Name, 120, "name"); err != nil {
				return err
			}
		}
		if in.ParentID != nil {
			np := *in.ParentID
			if np != nil && (*np == f.ID || strings.HasPrefix(s.pathOf(ctx, r, subj.TenantID, *np)+"/", oldPath+"/")) {
				return apperr.Validation.WithField("parent_id") // into itself or a descendant
			}
			f.ParentID = np
		}
		if in.SortOrder != nil {
			f.SortOrder = *in.SortOrder
		}
		base, depth, err := parentPathTx(ctx, r, subj.TenantID, f.ParentID)
		if err != nil {
			return err
		}
		f.Path = base + "/" + f.Name
		if depth+maxSubDepth(ctx, r, subj.TenantID, oldPath) > MaxFolderDepth {
			return apperr.Validation.WithField("parent_id")
		}
		f.UpdatedAt, f.UpdatedBy = s.d.Now(), subj.ActorID()
		if err := r.UpdateFolder(ctx, f); err != nil {
			if errors.Is(err, repo.ErrConflict) {
				return apperr.NameTaken.WithField("name")
			}
			return notFound(err)
		}
		if f.Path != oldPath {
			if err := r.RewriteFolderPaths(ctx, subj.TenantID, oldPath, f.Path); err != nil {
				return err
			}
		}
		out = f
		return nil
	})
	if err != nil {
		return store.Folder{}, err
	}
	s.record(ctx, subj, audit.FolderUpdate, audit.SubjectFolder, id, audit.OutcomeOK, "", nil)
	return out, nil
}

func (s *Service) pathOf(ctx context.Context, r repo.Store, tenant, id string) string {
	f, err := r.GetFolder(ctx, tenant, id)
	if err != nil {
		return "\x00missing"
	}
	return f.Path
}

func parentPathTx(ctx context.Context, r repo.Store, tenant string, parent *string) (string, int, error) {
	if parent == nil {
		return "", 0, nil
	}
	p, err := r.GetFolder(ctx, tenant, *parent)
	if err != nil {
		return "", 0, notFound(err)
	}
	return p.Path, strings.Count(p.Path, "/"), nil
}

// maxSubDepth is the depth of the subtree under path (1 for a leaf).
func maxSubDepth(ctx context.Context, r repo.Store, tenant, path string) int {
	all, err := r.ListFolders(ctx, tenant)
	if err != nil {
		return 1
	}
	base := strings.Count(path, "/")
	max := 1
	for _, f := range all {
		if strings.HasPrefix(f.Path, path+"/") {
			if d := strings.Count(f.Path, "/") - base + 1; d > max {
				max = d
			}
		}
	}
	return max
}

// DeleteFolder deletes an empty folder.
func (s *Service) DeleteFolder(ctx context.Context, subj authz.Subjects, id string) error {
	if _, err := s.d.Store.GetFolder(ctx, subj.TenantID, id); err != nil {
		return notFound(err)
	}
	used, err := s.d.Store.FolderInUse(ctx, subj.TenantID, id)
	if err != nil {
		return err
	}
	if used {
		s.record(ctx, subj, audit.FolderDelete, audit.SubjectFolder, id, audit.OutcomeRefused, "folder_not_empty", nil)
		return apperr.FolderNotEmpty
	}
	if err := s.d.Store.DeleteFolder(ctx, subj.TenantID, id); err != nil {
		return notFound(err)
	}
	s.record(ctx, subj, audit.FolderDelete, audit.SubjectFolder, id, audit.OutcomeOK, "", nil)
	return nil
}

// ---------------------------------------------------------------- templates

// CreateInput is a template upload.
type CreateInput struct {
	Name        string
	Description string
	FolderID    *string
	Tags        []string
	FileName    string
	PDF         []byte
}

func (s *Service) pdfLimits() limits.Limits {
	return limits.Limits{MaxBytes: s.d.Limits.MaxPDFBytes, MaxPages: s.d.Limits.MaxPages, Timeout: s.d.Limits.ParseTimeout}
}

// OpenPDFBytes checks an uploaded PDF (shared with verification and admin signing).
func OpenPDFBytes(ctx context.Context, data []byte, l limits.Limits) (*limits.Doc, error) {
	doc, err := limits.Open(ctx, data, l)
	switch {
	case err == nil:
		return doc, nil
	case errors.Is(err, limits.ErrTooLarge), errors.Is(err, limits.ErrTooManyPages):
		return nil, apperr.PayloadTooLarge
	default:
		return nil, apperr.InvalidPDF
	}
}

func cleanFileName(n string) string {
	n = strings.TrimSpace(n)
	if i := strings.LastIndexAny(n, `/\`); i >= 0 {
		n = n[i+1:]
	}
	n = strings.Map(func(r rune) rune {
		if r < 0x20 || r == 0x7f {
			return -1
		}
		return r
	}, n)
	if len(n) > 200 {
		n = n[:200]
	}
	if n == "" {
		n = "document.pdf"
	}
	return n
}

// Create uploads a PDF as a new draft template.
func (s *Service) Create(ctx context.Context, subj authz.Subjects, in CreateInput) (store.Template, error) {
	name, err := cleanName(in.Name, 200, "name")
	if err != nil {
		return store.Template{}, err
	}
	if len(in.Description) > 2000 {
		return store.Template{}, apperr.Validation.WithField("description")
	}
	tags, err := NormalizeTags(in.Tags)
	if err != nil {
		return store.Template{}, err
	}
	if in.FolderID != nil {
		if _, err := s.d.Store.GetFolder(ctx, subj.TenantID, *in.FolderID); err != nil {
			return store.Template{}, notFound(err)
		}
	}
	doc, err := OpenPDFBytes(ctx, in.PDF, s.pdfLimits())
	if err != nil {
		s.record(ctx, subj, audit.TemplateCreate, audit.SubjectTemplate, "", audit.OutcomeRefused, reasonOf(err), nil)
		return store.Template{}, err
	}
	now := s.d.Now()
	t := store.Template{ID: store.NewID(), TenantID: subj.TenantID, FolderID: in.FolderID, Name: name, Description: in.Description,
		Tags: tags, Status: store.TemplateDraft, PDFSize: int64(len(in.PDF)), PDFPages: doc.Pages, FileName: cleanFileName(in.FileName),
		PDFSigned: doc.Signed, Parties: []store.Party{{Key: "p1", Name: "First party"}}, Fields: []store.Field{}, Version: 1,
		CreatedAt: now, CreatedBy: subj.ActorID(), UpdatedAt: now, UpdatedBy: subj.ActorID()}
	t.PDFKey = blob.TemplatePDF(subj.TenantID, t.ID, store.NewID())
	if t.PDFSHA256, err = s.d.Blob.Put(ctx, t.PDFKey, bytes.NewReader(in.PDF), int64(len(in.PDF)), "application/pdf"); err != nil {
		return store.Template{}, err
	}
	if err := s.d.Store.CreateTemplate(ctx, t); err != nil {
		_ = s.d.Blob.Delete(ctx, t.PDFKey)
		if errors.Is(err, repo.ErrConflict) {
			return store.Template{}, apperr.NameTaken.WithField("name")
		}
		return store.Template{}, err
	}
	s.record(ctx, subj, audit.TemplateCreate, audit.SubjectTemplate, t.ID, audit.OutcomeOK, "", map[string]any{"pages": t.PDFPages})
	return t, nil
}

func reasonOf(err error) string {
	if e, ok := apperr.As(err); ok {
		return e.Reason
	}
	return "error"
}

// Get returns a template of the caller's tenant.
func (s *Service) Get(ctx context.Context, subj authz.Subjects, id string) (store.Template, error) {
	t, err := s.d.Store.GetTemplate(ctx, subj.TenantID, id)
	return t, notFound(err)
}

// List pages the caller's templates.
func (s *Service) List(ctx context.Context, subj authz.Subjects, f repo.TemplateFilter) ([]store.Template, int, error) {
	if f.PageSize > s.d.Limits.MaxPageSize {
		return nil, 0, apperr.Validation.WithDetail(map[string]any{"param": "page_size"})
	}
	return s.d.Store.ListTemplates(ctx, subj.TenantID, f)
}

// PatchInput changes template attributes; nil fields are unchanged.
type PatchInput struct {
	Name              *string
	Description       *string
	FolderID          **string
	Tags              *[]string
	Status            *string
	DefaultExpiryDays **int
	DefaultReminder   **store.Reminder
}

// Update applies a patch (status changes: draft → active needs a field per party).
func (s *Service) Update(ctx context.Context, subj authz.Subjects, id string, in PatchInput) (store.Template, error) {
	t, err := s.d.Store.GetTemplate(ctx, subj.TenantID, id)
	if err != nil {
		return store.Template{}, notFound(err)
	}
	if in.Name != nil {
		if t.Name, err = cleanName(*in.Name, 200, "name"); err != nil {
			return store.Template{}, err
		}
	}
	if in.Description != nil {
		if len(*in.Description) > 2000 {
			return store.Template{}, apperr.Validation.WithField("description")
		}
		t.Description = *in.Description
	}
	if in.FolderID != nil {
		if f := *in.FolderID; f != nil {
			if _, err := s.d.Store.GetFolder(ctx, subj.TenantID, *f); err != nil {
				return store.Template{}, notFound(err)
			}
		}
		t.FolderID = *in.FolderID
	}
	if in.Tags != nil {
		if t.Tags, err = NormalizeTags(*in.Tags); err != nil {
			return store.Template{}, err
		}
	}
	if in.DefaultExpiryDays != nil {
		if d := *in.DefaultExpiryDays; d != nil && (*d < 1 || *d > 365) {
			return store.Template{}, apperr.Validation.WithField("default_expiry_days")
		}
		t.DefaultExpiryDays = *in.DefaultExpiryDays
	}
	if in.DefaultReminder != nil {
		if r := *in.DefaultReminder; r != nil && !validReminder(*r) {
			return store.Template{}, apperr.Validation.WithField("default_reminder")
		}
		t.DefaultReminder = *in.DefaultReminder
	}
	if in.Status != nil {
		switch *in.Status {
		case store.TemplateActive:
			if err := ReadyToActivate(t); err != nil {
				return store.Template{}, err
			}
		case store.TemplateDraft, store.TemplateArchived:
		default:
			return store.Template{}, apperr.Validation.WithField("status")
		}
		t.Status = *in.Status
	}
	t.UpdatedAt, t.UpdatedBy = s.d.Now(), subj.ActorID()
	out, err := s.d.Store.UpdateTemplate(ctx, t, t.Version)
	if err != nil {
		if errors.Is(err, repo.ErrConflict) {
			return store.Template{}, apperr.NameTaken.WithField("name")
		}
		return store.Template{}, notFound(err)
	}
	s.record(ctx, subj, audit.TemplateUpdate, audit.SubjectTemplate, id, audit.OutcomeOK, "", nil)
	return out, nil
}

func validReminder(r store.Reminder) bool {
	return r.IntervalDays >= 1 && r.IntervalDays <= 30 && r.Max >= 1 && r.Max <= 20
}

// ValidReminder is validReminder for other packages.
func ValidReminder(r store.Reminder) bool { return validReminder(r) }

// SaveFields stores the builder's parties and fields when version matches.
func (s *Service) SaveFields(ctx context.Context, subj authz.Subjects, id string, version int, parties []store.Party, fields []store.Field) (store.Template, error) {
	t, err := s.d.Store.GetTemplate(ctx, subj.TenantID, id)
	if err != nil {
		return store.Template{}, notFound(err)
	}
	if t.Version != version {
		return store.Template{}, apperr.VersionConflict
	}
	for i := range parties {
		parties[i].Name = strings.TrimSpace(parties[i].Name)
	}
	for i := range fields {
		fields[i].Name = strings.TrimSpace(fields[i].Name)
		fields[i].Prefill = ""
	}
	if err := ValidateParties(parties, s.d.Limits.MaxParties); err != nil {
		return store.Template{}, err
	}
	if err := ValidateFields(fields, parties, t.PDFPages, s.d.Limits.MaxFields); err != nil {
		s.record(ctx, subj, audit.TemplateFields, audit.SubjectTemplate, id, audit.OutcomeRefused, reasonOf(err), nil)
		return store.Template{}, err
	}
	if err := s.d.Rules(fields); err != nil {
		s.record(ctx, subj, audit.TemplateFields, audit.SubjectTemplate, id, audit.OutcomeRefused, reasonOf(err), nil)
		return store.Template{}, err
	}
	t.Parties, t.Fields = parties, fields
	if t.Status == store.TemplateActive {
		if err := ReadyToActivate(t); err != nil {
			return store.Template{}, err
		}
	}
	t.UpdatedAt, t.UpdatedBy = s.d.Now(), subj.ActorID()
	out, err := s.d.Store.UpdateTemplate(ctx, t, version)
	if err != nil {
		if errors.Is(err, repo.ErrConflict) {
			return store.Template{}, apperr.VersionConflict
		}
		return store.Template{}, notFound(err)
	}
	s.record(ctx, subj, audit.TemplateFields, audit.SubjectTemplate, id, audit.OutcomeOK, "", map[string]any{"fields": len(fields), "parties": len(parties)})
	return out, nil
}

// Clone copies a template (PDF object included) as a new draft.
func (s *Service) Clone(ctx context.Context, subj authz.Subjects, id, name string, folderID *string) (store.Template, error) {
	src, err := s.d.Store.GetTemplate(ctx, subj.TenantID, id)
	if err != nil {
		return store.Template{}, notFound(err)
	}
	if name, err = cleanName(name, 200, "name"); err != nil {
		return store.Template{}, err
	}
	if folderID != nil {
		if _, err := s.d.Store.GetFolder(ctx, subj.TenantID, *folderID); err != nil {
			return store.Template{}, notFound(err)
		}
	}
	rc, err := s.d.Blob.Get(ctx, src.PDFKey)
	if err != nil {
		return store.Template{}, err
	}
	data, err := io.ReadAll(io.LimitReader(rc, s.d.Limits.MaxPDFBytes+1))
	_ = rc.Close()
	if err != nil {
		return store.Template{}, err
	}
	now := s.d.Now()
	t := src
	t.ID, t.Name, t.FolderID, t.Status, t.Version = store.NewID(), name, folderID, store.TemplateDraft, 1
	t.CreatedAt, t.CreatedBy, t.UpdatedAt, t.UpdatedBy = now, subj.ActorID(), now, subj.ActorID()
	t.PDFKey = blob.TemplatePDF(subj.TenantID, t.ID, store.NewID())
	if t.PDFSHA256, err = s.d.Blob.Put(ctx, t.PDFKey, bytes.NewReader(data), int64(len(data)), "application/pdf"); err != nil {
		return store.Template{}, err
	}
	if err := s.d.Store.CreateTemplate(ctx, t); err != nil {
		_ = s.d.Blob.Delete(ctx, t.PDFKey)
		if errors.Is(err, repo.ErrConflict) {
			return store.Template{}, apperr.NameTaken.WithField("name")
		}
		return store.Template{}, err
	}
	s.record(ctx, subj, audit.TemplateClone, audit.SubjectTemplate, t.ID, audit.OutcomeOK, "", map[string]any{"from": id})
	return t, nil
}

// Delete removes a template no submission refers to (archive it otherwise).
func (s *Service) Delete(ctx context.Context, subj authz.Subjects, id string) error {
	t, err := s.d.Store.GetTemplate(ctx, subj.TenantID, id)
	if err != nil {
		return notFound(err)
	}
	if err := s.d.Store.DeleteTemplate(ctx, subj.TenantID, id); err != nil {
		if errors.Is(err, repo.ErrConflict) {
			s.record(ctx, subj, audit.TemplateDelete, audit.SubjectTemplate, id, audit.OutcomeRefused, "template_in_use", nil)
			return apperr.TemplateInUse
		}
		return notFound(err)
	}
	_ = s.d.Blob.Delete(ctx, t.PDFKey) // the row is gone; an orphan object is harmless and swept
	s.record(ctx, subj, audit.TemplateDelete, audit.SubjectTemplate, id, audit.OutcomeOK, "", nil)
	return nil
}

// OpenPDF returns the template's PDF (tenant-checked key).
func (s *Service) OpenPDF(ctx context.Context, subj authz.Subjects, id string) (io.ReadCloser, store.Template, error) {
	t, err := s.d.Store.GetTemplate(ctx, subj.TenantID, id)
	if err != nil {
		return nil, store.Template{}, notFound(err)
	}
	if !blob.InTenant(t.PDFKey, subj.TenantID) {
		return nil, store.Template{}, apperr.NotFound
	}
	rc, err := s.d.Blob.Get(ctx, t.PDFKey)
	return rc, t, err
}

// DetectFields proposes text fields for the first party (not saved).
func (s *Service) DetectFields(ctx context.Context, subj authz.Subjects, id string) ([]store.Field, error) {
	rc, t, err := s.OpenPDF(ctx, subj, id)
	if err != nil {
		return nil, err
	}
	data, err := io.ReadAll(io.LimitReader(rc, s.d.Limits.MaxPDFBytes+1))
	_ = rc.Close()
	if err != nil {
		return nil, err
	}
	party := "p1"
	if len(t.Parties) > 0 {
		party = t.Parties[0].Key
	}
	timeout := s.d.Limits.ParseTimeout
	if timeout <= 0 {
		timeout = 20 * time.Second
	}
	dctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	fields, err := detect.Detect(dctx, data, party)
	if err != nil {
		return nil, apperr.InvalidPDF
	}
	if fields == nil {
		fields = []store.Field{}
	}
	return fields, nil
}
