// Package backup exports and imports a tenant's signing data (US9,
// research D15) as a tar.gz archive: a manifest, JSON records (folders,
// templates, submissions with their signers, versions and history,
// certificates) and the stored objects (template PDFs, document versions,
// audit trails, signer uploads).
//
// Keys never leave in clear: PIN-encrypted signer keys travel as they are;
// sealed CA, system and administrator keys travel sealed together with the
// module KEK's key-check value, and an import keeps them only when the
// target's KEK is the same — otherwise the key is dropped and the
// certificate marked needs_reissue.
//
// Access: backup:manage within the caller's tenant; platform administrators
// may name another tenant. Import parsing is bounded (archive size, entry
// count and sizes, object paths confined to the target tenant).
package backup

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"path"
	"sort"
	"strings"
	"time"

	"github.com/go-tangra/go-tangra-signing/v4/internal/apperr"
	"github.com/go-tangra/go-tangra-signing/v4/internal/audit"
	"github.com/go-tangra/go-tangra-signing/v4/internal/authz"
	"github.com/go-tangra/go-tangra-signing/v4/internal/blob"
	"github.com/go-tangra/go-tangra-signing/v4/internal/repo"
	"github.com/go-tangra/go-tangra-signing/v4/internal/store"
)

// SchemaVersion of the archive.
const SchemaVersion = 1

// Import modes.
const (
	ModeSkip      = "skip"
	ModeOverwrite = "overwrite"
)

const (
	maxEntries     = 200000
	maxRecordBytes = 256 << 20
	pageSize       = 100
)

// Manifest describes an archive.
type Manifest struct {
	SchemaVersion int       `json:"schema_version"`
	ExportedAt    time.Time `json:"exported_at"`
	TenantID      string    `json:"tenant_id"`
	KeyCheck      string    `json:"key_check"` // hex KCV of the module KEK
	Counts        Counts    `json:"counts"`
}

// Counts are the records of an archive or an import.
type Counts struct {
	Folders      int `json:"folders"`
	Templates    int `json:"templates"`
	Submissions  int `json:"submissions"`
	Certificates int `json:"certificates"`
	Objects      int `json:"objects"`
}

// SubmissionRecord is a submission with everything that hangs off it.
type SubmissionRecord struct {
	Submission store.Submission        `json:"submission"`
	Signers    []store.Signer          `json:"signers"`
	Versions   []store.DocumentVersion `json:"versions"`
	Events     []store.Event           `json:"events"`
}

// Records are the JSON part of an archive.
type Records struct {
	Folders      []store.Folder      `json:"folders"`
	Templates    []store.Template    `json:"templates"`
	Submissions  []SubmissionRecord  `json:"submissions"`
	Certificates []store.Certificate `json:"certificates"`
}

// Deps wire the service.
type Deps struct {
	Store    repo.Store
	Blob     blob.Store
	Audit    audit.Recorder
	KeyCheck []byte // the module KEK's key-check value
	MaxBytes int64  // archive bound (limits_signing.max_backup_bytes)
	Now      func() time.Time
}

// Service exports and imports.
type Service struct{ d Deps }

// New builds the service.
func New(d Deps) *Service {
	if d.Now == nil {
		d.Now = func() time.Time { return time.Now().UTC() }
	}
	if d.MaxBytes <= 0 {
		d.MaxBytes = 2 << 30
	}
	return &Service{d: d}
}

// target resolves the tenant a request acts on.
func target(subj authz.Subjects, tenantID string) (string, error) {
	if tenantID == "" || tenantID == subj.TenantID {
		if subj.TenantID == "" {
			return "", apperr.Validation.WithField("tenant_id")
		}
		return subj.TenantID, nil
	}
	if err := authz.RequirePlatformAdmin(subj); err != nil {
		return "", apperr.Forbidden.WithField("tenant_id")
	}
	return tenantID, nil
}

func (s *Service) record(ctx context.Context, subj authz.Subjects, tenant string, t audit.EventType, outcome, reason string, detail map[string]any) {
	audit.Emit(ctx, s.d.Audit, audit.Event{TenantID: tenant, EventType: t, ActorKind: audit.ActorOf(subj.ActorKind), ActorID: subj.ActorID(),
		SubjectKind: audit.SubjectBackup, SubjectID: tenant, Outcome: outcome, Reason: reason, Details: detail})
}

func reasonOf(err error) string {
	if e, ok := apperr.As(err); ok {
		return e.Reason
	}
	return "error"
}

// ---------------------------------------------------------------- export

// Collect gathers the tenant's records (the first half of an export; the
// HTTP layer checks the size before it starts streaming).
func (s *Service) Collect(ctx context.Context, subj authz.Subjects, tenantID string) (string, Records, int64, error) {
	tenant, err := target(subj, tenantID)
	if err != nil {
		return "", Records{}, 0, err
	}
	var r Records
	if r.Folders, err = s.d.Store.ListFolders(ctx, tenant); err != nil {
		return "", Records{}, 0, err
	}
	for p := 1; ; p++ {
		items, total, err := s.d.Store.ListTemplates(ctx, tenant, repo.TemplateFilter{Page: p, PageSize: pageSize})
		if err != nil {
			return "", Records{}, 0, err
		}
		r.Templates = append(r.Templates, items...)
		if len(items) == 0 || len(r.Templates) >= total {
			break
		}
	}
	for p := 1; ; p++ {
		items, total, err := s.d.Store.ListSubmissions(ctx, tenant, repo.SubmissionFilter{Page: p, PageSize: pageSize})
		if err != nil {
			return "", Records{}, 0, err
		}
		for _, sub := range items {
			rec := SubmissionRecord{}
			if rec.Submission, rec.Signers, err = s.d.Store.GetSubmission(ctx, tenant, sub.ID); err != nil {
				return "", Records{}, 0, err
			}
			if rec.Versions, err = s.d.Store.ListVersions(ctx, tenant, sub.ID); err != nil {
				return "", Records{}, 0, err
			}
			if rec.Events, err = s.d.Store.ListEvents(ctx, tenant, sub.ID); err != nil {
				return "", Records{}, 0, err
			}
			r.Submissions = append(r.Submissions, rec)
		}
		if len(items) == 0 || len(r.Submissions) >= total {
			break
		}
	}
	for p := 1; ; p++ {
		items, total, err := s.d.Store.ListCertificates(ctx, tenant, repo.CertificateFilter{Page: p, PageSize: pageSize})
		if err != nil {
			return "", Records{}, 0, err
		}
		r.Certificates = append(r.Certificates, items...)
		if len(items) == 0 || len(r.Certificates) >= total {
			break
		}
	}
	var size int64
	for _, t := range r.Templates {
		size += t.PDFSize
	}
	for _, sub := range r.Submissions {
		for _, v := range sub.Versions {
			size += v.Size
		}
	}
	if size > s.d.MaxBytes {
		s.record(ctx, subj, tenant, audit.BackupExport, audit.OutcomeRefused, "backup_too_large", nil)
		return "", Records{}, 0, apperr.BackupTooLarge
	}
	return tenant, r, size, nil
}

// limitWriter fails once more than n bytes are written.
type limitWriter struct {
	w io.Writer
	n int64
}

func (l *limitWriter) Write(p []byte) (int, error) {
	if int64(len(p)) > l.n {
		return 0, apperr.BackupTooLarge
	}
	l.n -= int64(len(p))
	return l.w.Write(p)
}

// Write streams the archive of collected records to w.
func (s *Service) Write(ctx context.Context, subj authz.Subjects, tenant string, r Records, w io.Writer) error {
	err := s.write(ctx, tenant, r, &limitWriter{w: w, n: s.d.MaxBytes})
	if err != nil {
		s.record(ctx, subj, tenant, audit.BackupExport, audit.OutcomeError, reasonOf(err), nil)
		return err
	}
	s.record(ctx, subj, tenant, audit.BackupExport, audit.OutcomeOK, "", map[string]any{"templates": len(r.Templates),
		"submissions": len(r.Submissions), "certificates": len(r.Certificates)})
	return nil
}

func (s *Service) write(ctx context.Context, tenant string, r Records, w io.Writer) error {
	gz := gzip.NewWriter(w)
	tw := tar.NewWriter(gz)
	now := s.d.Now()
	file := func(name string, data []byte) error {
		if err := tw.WriteHeader(&tar.Header{Name: name, Mode: 0o600, Size: int64(len(data)), ModTime: now, Typeflag: tar.TypeReg}); err != nil {
			return err
		}
		_, err := tw.Write(data)
		return err
	}
	keys := objectKeys(tenant, r)
	m := Manifest{SchemaVersion: SchemaVersion, ExportedAt: now, TenantID: tenant, KeyCheck: hex.EncodeToString(s.d.KeyCheck),
		Counts: Counts{Folders: len(r.Folders), Templates: len(r.Templates), Submissions: len(r.Submissions), Certificates: len(r.Certificates), Objects: len(keys)}}
	mj, _ := json.Marshal(m)
	if err := file("manifest.json", mj); err != nil {
		return err
	}
	rj, err := json.Marshal(r)
	if err != nil {
		return err
	}
	if err := file("records.json", rj); err != nil {
		return err
	}
	prefix := blob.TenantPrefix(tenant)
	for _, k := range keys {
		rc, err := s.d.Blob.Get(ctx, k)
		if err != nil {
			continue // a missing object is not fatal (it is reported by its absence)
		}
		data, err := io.ReadAll(io.LimitReader(rc, s.d.MaxBytes+1))
		_ = rc.Close()
		if err != nil {
			return err
		}
		if err := file("objects/"+strings.TrimPrefix(k, prefix), data); err != nil {
			return err
		}
	}
	if err := tw.Close(); err != nil {
		return err
	}
	return gz.Close()
}

// objectKeys lists the tenant objects the records reference, plus signer
// uploads under the submissions.
func objectKeys(tenant string, r Records) []string {
	seen := map[string]bool{}
	var out []string
	add := func(k string) {
		if k != "" && blob.InTenant(k, tenant) && !seen[k] {
			seen[k] = true
			out = append(out, k)
		}
	}
	for _, t := range r.Templates {
		add(t.PDFKey)
	}
	for _, sub := range r.Submissions {
		add(sub.Submission.PDFKey)
		add(sub.Submission.AuditTrailKey)
		for _, v := range sub.Versions {
			add(v.ObjectKey)
		}
		for _, sg := range sub.Signers {
			for _, v := range sg.Values {
				if strings.HasPrefix(v, blob.SubmissionPrefix(tenant, sub.Submission.ID)) {
					add(v)
				}
			}
		}
	}
	sort.Strings(out)
	return out
}

// ---------------------------------------------------------------- import

// Result summarises an import.
type Result struct {
	Created      Counts   `json:"created"`
	Updated      Counts   `json:"updated"`
	Skipped      Counts   `json:"skipped"`
	NeedsReissue int      `json:"needs_reissue"`
	Errors       []string `json:"errors,omitempty"`
}

// Import restores an archive into the target tenant.
func (s *Service) Import(ctx context.Context, subj authz.Subjects, tenantID, mode string, archive io.Reader) (Result, error) {
	tenant, err := target(subj, tenantID)
	if err != nil {
		return Result{}, err
	}
	if mode == "" {
		mode = ModeSkip
	}
	if mode != ModeSkip && mode != ModeOverwrite {
		return Result{}, apperr.Validation.WithField("mode")
	}
	res, err := s.importArchive(ctx, tenant, mode, archive)
	if err != nil {
		s.record(ctx, subj, tenant, audit.BackupImport, audit.OutcomeRefused, reasonOf(err), nil)
		return Result{}, err
	}
	s.record(ctx, subj, tenant, audit.BackupImport, audit.OutcomeOK, "", map[string]any{"mode": mode,
		"created": res.Created.Submissions + res.Created.Templates, "needs_reissue": res.NeedsReissue})
	return res, nil
}

type parsed struct {
	manifest Manifest
	records  Records
	objects  map[string][]byte // relative key → bytes
}

func (s *Service) read(archive io.Reader) (parsed, error) {
	bad := apperr.InvalidBackup
	gz, err := gzip.NewReader(io.LimitReader(archive, s.d.MaxBytes+1))
	if err != nil {
		return parsed{}, bad
	}
	tr := tar.NewReader(gz)
	p := parsed{objects: map[string][]byte{}}
	var total int64
	haveManifest, haveRecords := false, false
	for n := 0; ; n++ {
		h, err := tr.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil || n > maxEntries || h.Typeflag != tar.TypeReg || h.Size < 0 {
			return parsed{}, bad
		}
		total += h.Size
		if total > s.d.MaxBytes {
			return parsed{}, apperr.BackupTooLarge
		}
		name := h.Name
		switch {
		case name == "manifest.json" || name == "records.json":
			if h.Size > maxRecordBytes {
				return parsed{}, bad
			}
			data, err := io.ReadAll(io.LimitReader(tr, h.Size))
			if err != nil {
				return parsed{}, bad
			}
			if name == "manifest.json" {
				haveManifest = json.Unmarshal(data, &p.manifest) == nil
			} else {
				haveRecords = json.Unmarshal(data, &p.records) == nil
			}
			if !haveManifest && name == "manifest.json" || !haveRecords && name == "records.json" {
				return parsed{}, bad
			}
		case strings.HasPrefix(name, "objects/"):
			rel := strings.TrimPrefix(name, "objects/")
			if !validRel(rel) {
				return parsed{}, bad
			}
			data, err := io.ReadAll(io.LimitReader(tr, h.Size))
			if err != nil {
				return parsed{}, bad
			}
			p.objects[rel] = data
		default:
			return parsed{}, bad
		}
	}
	if !haveManifest || !haveRecords || p.manifest.SchemaVersion != SchemaVersion || p.manifest.TenantID == "" {
		return parsed{}, bad
	}
	return p, nil
}

// validRel accepts clean relative object paths under the known areas.
func validRel(rel string) bool {
	return rel != "" && path.Clean(rel) == rel && !strings.HasPrefix(rel, "/") && !strings.Contains(rel, "..") &&
		(strings.HasPrefix(rel, "templates/") || strings.HasPrefix(rel, "submissions/"))
}

// rekey moves an object key of the source tenant to the target tenant.
func rekey(k, from, to string) (string, bool) {
	if k == "" {
		return "", true
	}
	rel, ok := strings.CutPrefix(k, blob.TenantPrefix(from))
	if !ok || !validRel(rel) {
		return "", false
	}
	return blob.TenantPrefix(to) + rel, true
}

func (s *Service) importArchive(ctx context.Context, tenant, mode string, archive io.Reader) (Result, error) {
	p, err := s.read(archive)
	if err != nil {
		return Result{}, err
	}
	from := p.manifest.TenantID
	sameKEK := false
	if kc, err := hex.DecodeString(p.manifest.KeyCheck); err == nil && len(kc) > 0 && len(s.d.KeyCheck) > 0 {
		sameKEK = subtle.ConstantTimeCompare(kc, s.d.KeyCheck) == 1
	}
	var res Result
	fail := func(what string, err error) { res.Errors = append(res.Errors, what+": "+reasonOf(err)) }

	// Objects first, so rows never point at missing bytes.
	prefix := blob.TenantPrefix(tenant)
	rels := make([]string, 0, len(p.objects))
	for rel := range p.objects {
		rels = append(rels, rel)
	}
	sort.Strings(rels)
	for _, rel := range rels {
		key := prefix + rel
		if mode == ModeSkip {
			if rc, err := s.d.Blob.Get(ctx, key); err == nil {
				_ = rc.Close()
				res.Skipped.Objects++
				continue
			}
		}
		data := p.objects[rel]
		if _, err := s.d.Blob.Put(ctx, key, bytes.NewReader(data), int64(len(data)), "application/octet-stream"); err != nil {
			return res, err
		}
		res.Created.Objects++
	}

	// Folders parents first.
	folders := append([]store.Folder(nil), p.records.Folders...)
	sort.SliceStable(folders, func(i, j int) bool { return strings.Count(folders[i].Path, "/") < strings.Count(folders[j].Path, "/") })
	for _, f := range folders {
		f.TenantID = tenant
		_, err := s.d.Store.GetFolder(ctx, tenant, f.ID)
		switch {
		case errors.Is(err, repo.ErrNotFound):
			if err := s.d.Store.CreateFolder(ctx, f); err != nil {
				fail("folder "+f.Name, err)
				continue
			}
			res.Created.Folders++
		case err != nil:
			return res, err
		case mode == ModeOverwrite:
			if err := s.d.Store.UpdateFolder(ctx, f); err != nil {
				fail("folder "+f.Name, err)
				continue
			}
			res.Updated.Folders++
		default:
			res.Skipped.Folders++
		}
	}

	for _, t := range p.records.Templates {
		t.TenantID = tenant
		var ok bool
		if t.PDFKey, ok = rekey(t.PDFKey, from, tenant); !ok {
			fail("template "+t.Name, apperr.InvalidBackup)
			continue
		}
		cur, err := s.d.Store.GetTemplate(ctx, tenant, t.ID)
		switch {
		case errors.Is(err, repo.ErrNotFound):
			if err := s.d.Store.CreateTemplate(ctx, t); err != nil {
				fail("template "+t.Name, err)
				continue
			}
			res.Created.Templates++
		case err != nil:
			return res, err
		case mode == ModeOverwrite:
			if _, err := s.d.Store.UpdateTemplate(ctx, t, cur.Version); err != nil {
				fail("template "+t.Name, err)
				continue
			}
			res.Updated.Templates++
		default:
			res.Skipped.Templates++
		}
	}

	for _, rec := range p.records.Submissions {
		if err := s.importSubmission(ctx, tenant, from, mode, rec, &res); err != nil {
			fail("submission "+rec.Submission.Name, err)
		}
	}

	certs := append([]store.Certificate(nil), p.records.Certificates...)
	sort.SliceStable(certs, func(i, j int) bool { return certs[i].Kind == store.KindCA && certs[j].Kind != store.KindCA })
	for _, c := range certs {
		c.TenantID = tenant
		if c.KeyProtection == store.KeySealed && !sameKEK {
			c.KeyBlob = nil
			if c.Status == store.CertActive {
				c.Status = store.CertNeedsReissue
			}
			res.NeedsReissue++
		}
		_, err := s.d.Store.GetCertificate(ctx, tenant, c.ID)
		switch {
		case errors.Is(err, repo.ErrNotFound):
			if err := s.d.Store.CreateCertificate(ctx, c); err != nil {
				fail("certificate "+c.SubjectCN, err)
				continue
			}
			res.Created.Certificates++
		case err != nil:
			return res, err
		case mode == ModeOverwrite:
			if err := s.d.Store.UpdateCertificate(ctx, c); err != nil {
				fail("certificate "+c.SubjectCN, err)
				continue
			}
			res.Updated.Certificates++
		default:
			res.Skipped.Certificates++
		}
	}
	return res, nil
}

func (s *Service) importSubmission(ctx context.Context, tenant, from, mode string, rec SubmissionRecord, res *Result) error {
	sub := rec.Submission
	sub.TenantID = tenant
	var ok bool
	if sub.PDFKey, ok = rekey(sub.PDFKey, from, tenant); !ok {
		return apperr.InvalidBackup
	}
	if sub.AuditTrailKey, ok = rekey(sub.AuditTrailKey, from, tenant); !ok {
		return apperr.InvalidBackup
	}
	versions := make([]store.DocumentVersion, 0, len(rec.Versions))
	for _, v := range rec.Versions {
		v.TenantID = tenant
		if v.ObjectKey, ok = rekey(v.ObjectKey, from, tenant); !ok || v.SubmissionID != sub.ID {
			return apperr.InvalidBackup
		}
		versions = append(versions, v)
	}
	sort.Slice(versions, func(i, j int) bool { return versions[i].Version < versions[j].Version })
	if len(versions) == 0 || versions[0].Version != 0 {
		return apperr.InvalidBackup
	}
	signers := make([]store.Signer, 0, len(rec.Signers))
	for _, sg := range rec.Signers {
		sg.TenantID = tenant
		if sg.SubmissionID != sub.ID {
			return apperr.InvalidBackup
		}
		for k, v := range sg.Values {
			if strings.HasPrefix(v, blob.TenantPrefix(from)) {
				if sg.Values[k], ok = rekey(v, from, tenant); !ok {
					return apperr.InvalidBackup
				}
			}
		}
		signers = append(signers, sg)
	}
	_, _, err := s.d.Store.GetSubmission(ctx, tenant, sub.ID)
	exists := err == nil
	if err != nil && !errors.Is(err, repo.ErrNotFound) {
		return err
	}
	if exists && mode == ModeSkip {
		res.Skipped.Submissions++
		return nil
	}
	return s.d.Store.Tx(ctx, tenant, func(tx repo.Store) error {
		if exists {
			if err := tx.DeleteSubmission(ctx, tenant, sub.ID); err != nil { // overwrite = replace as a whole
				return err
			}
		}
		if err := tx.CreateSubmission(ctx, sub, signers, versions[0]); err != nil {
			return err
		}
		for _, v := range versions[1:] {
			if err := tx.AddVersion(ctx, v); err != nil {
				return err
			}
		}
		for _, e := range rec.Events {
			e.TenantID = tenant
			if e.SubmissionID == nil || *e.SubmissionID != sub.ID {
				continue
			}
			if err := tx.AddEvent(ctx, e); err != nil {
				return err
			}
		}
		if sub.Status == store.SubmissionCompleted && sub.AuditTrailKey == "" {
			if err := tx.EnqueueJob(ctx, store.Job{ID: store.NewID(), TenantID: tenant, Kind: "audit_trail", SubmissionID: sub.ID,
				NextAttemptAt: s.d.Now(), CreatedAt: s.d.Now()}); err != nil {
				return err
			}
		}
		if exists {
			res.Updated.Submissions++
		} else {
			res.Created.Submissions++
		}
		return nil
	})
}

// String is a short summary (no values).
func (r Result) String() string {
	return fmt.Sprintf("created %d templates, %d submissions; %d need re-issue", r.Created.Templates, r.Created.Submissions, r.NeedsReissue)
}
