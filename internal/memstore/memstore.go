// Package memstore is an in-memory repo.Store with the semantics of the
// database implementation (repodb): tenant scoping (a foreign id is "not
// found"), case-insensitive unique names, optimistic template versions,
// in-use checks, transactions that roll back on error (Tx serialises all
// transactions, which also gives LockSubmission its row-lock meaning) and a
// PIN-failure counter that survives a rolled-back transaction. It backs every
// service unit test.
package memstore

import (
	"context"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/go-tangra/go-tangra-signing/v4/internal/repo"
	"github.com/go-tangra/go-tangra-signing/v4/internal/store"
)

type data struct {
	folders   map[string]store.Folder
	templates map[string]store.Template
	subs      map[string]store.Submission
	signers   map[string]store.Signer
	versions  map[string]store.DocumentVersion // submission/version
	certs     map[string]store.Certificate
	qes       map[string]store.QESPreparation
	events    []store.Event
	jobs      map[string]store.Job
	audit     []store.AuditRow
}

func newData() *data {
	return &data{folders: map[string]store.Folder{}, templates: map[string]store.Template{}, subs: map[string]store.Submission{},
		signers: map[string]store.Signer{}, versions: map[string]store.DocumentVersion{}, certs: map[string]store.Certificate{},
		qes: map[string]store.QESPreparation{}, jobs: map[string]store.Job{}}
}

func (d *data) clone() *data {
	c := newData()
	for k, v := range d.folders {
		c.folders[k] = v
	}
	for k, v := range d.templates {
		c.templates[k] = cloneTemplate(v)
	}
	for k, v := range d.subs {
		c.subs[k] = cloneSubmission(v)
	}
	for k, v := range d.signers {
		c.signers[k] = cloneSigner(v)
	}
	for k, v := range d.versions {
		c.versions[k] = v
	}
	for k, v := range d.certs {
		c.certs[k] = v
	}
	for k, v := range d.qes {
		c.qes[k] = v
	}
	c.events = append([]store.Event(nil), d.events...)
	for k, v := range d.jobs {
		c.jobs[k] = v
	}
	c.audit = append([]store.AuditRow(nil), d.audit...)
	return c
}

// Mem implements repo.Store in memory.
type Mem struct {
	mu   sync.Mutex
	txMu sync.Mutex
	d    *data
	// Err, when set, is returned by every call (failure injection).
	Err error
	// FailOn, when set, makes calls to the named method return FailErr.
	FailOn  string
	FailErr error
}

var _ repo.Store = (*Mem)(nil)

// New returns an empty store.
func New() *Mem { return &Mem{d: newData()} }

// SetErr injects err into every subsequent call (nil clears it).
func (m *Mem) SetErr(err error) { m.mu.Lock(); m.Err = err; m.mu.Unlock() }

// Fail makes the named method fail with err (empty name clears it).
func (m *Mem) Fail(method string, err error) {
	m.mu.Lock()
	m.FailOn, m.FailErr = method, err
	m.mu.Unlock()
}

func (m *Mem) lock(method string) (func(), error) {
	m.mu.Lock()
	if m.Err != nil {
		err := m.Err
		m.mu.Unlock()
		return func() {}, err
	}
	if m.FailOn != "" && m.FailOn == method {
		err := m.FailErr
		m.mu.Unlock()
		return func() {}, err
	}
	return m.mu.Unlock, nil
}

// Audit returns the recorded audit rows (tests).
func (m *Mem) Audit() []store.AuditRow {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]store.AuditRow(nil), m.d.audit...)
}

// Events returns every history event (tests).
func (m *Mem) Events() []store.Event {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]store.Event(nil), m.d.events...)
}

// Jobs returns every job (tests).
func (m *Mem) Jobs() []store.Job {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]store.Job, 0, len(m.d.jobs))
	for _, j := range m.d.jobs {
		out = append(out, j)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

// PutCertificate stores c as is (tests).
func (m *Mem) PutCertificate(c store.Certificate) { m.mu.Lock(); m.d.certs[c.ID] = c; m.mu.Unlock() }

// PutQES stores a preparation as given (tests).
func (m *Mem) PutQES(q store.QESPreparation) { m.mu.Lock(); m.d.qes[q.ID] = q; m.mu.Unlock() }

// PutJob stores a job as given (tests: re-queue or odd jobs).
func (m *Mem) PutJob(j store.Job) { m.mu.Lock(); m.d.jobs[j.ID] = j; m.mu.Unlock() }

// PutSubmission stores s as is (tests).
func (m *Mem) PutSubmission(s store.Submission) {
	m.mu.Lock()
	m.d.subs[s.ID] = cloneSubmission(s)
	m.mu.Unlock()
}

// PutSigner stores s as is (tests).
func (m *Mem) PutSigner(s store.Signer) {
	m.mu.Lock()
	m.d.signers[s.ID] = cloneSigner(s)
	m.mu.Unlock()
}

// Tx runs fn; every change fn makes is undone when it returns an error, except
// PIN-failure counters (their own transaction in repodb).
func (m *Mem) Tx(ctx context.Context, tenantID string, fn func(repo.Store) error) error {
	m.txMu.Lock()
	defer m.txMu.Unlock()
	unlock, err := m.lock("Tx")
	if err != nil {
		unlock()
		return err
	}
	snap := m.d.clone()
	unlock()
	if err := fn(m); err != nil {
		m.mu.Lock()
		for id, c := range m.d.certs { // keep PIN counters
			if s, ok := snap.certs[id]; ok {
				s.FailedPINCount, s.LockedUntil = c.FailedPINCount, c.LockedUntil
				snap.certs[id] = s
			}
		}
		m.d = snap
		m.mu.Unlock()
		return err
	}
	return nil
}

func cloneTime(t *time.Time) *time.Time {
	if t == nil {
		return nil
	}
	c := *t
	return &c
}

func cloneStr(s *string) *string {
	if s == nil {
		return nil
	}
	c := *s
	return &c
}

func cloneFields(f []store.Field) []store.Field {
	out := make([]store.Field, len(f))
	for i, x := range f {
		x.Options = append([]string(nil), x.Options...)
		if x.Conditions != nil {
			c := *x.Conditions
			c.Rules = append([]store.Rule(nil), c.Rules...)
			x.Conditions = &c
		}
		out[i] = x
	}
	return out
}

func cloneTemplate(t store.Template) store.Template {
	t.FolderID = cloneStr(t.FolderID)
	t.Tags = append([]string(nil), t.Tags...)
	t.Parties = append([]store.Party(nil), t.Parties...)
	t.Fields = cloneFields(t.Fields)
	if t.DefaultExpiryDays != nil {
		d := *t.DefaultExpiryDays
		t.DefaultExpiryDays = &d
	}
	if t.DefaultReminder != nil {
		r := *t.DefaultReminder
		t.DefaultReminder = &r
	}
	return t
}

func cloneSubmission(s store.Submission) store.Submission {
	s.Fields = cloneFields(s.Fields)
	s.Parties = append([]store.Party(nil), s.Parties...)
	s.ExpiresAt, s.SentAt, s.CompletedAt, s.CancelledAt = cloneTime(s.ExpiresAt), cloneTime(s.SentAt), cloneTime(s.CompletedAt), cloneTime(s.CancelledAt)
	if s.Reminder != nil {
		r := *s.Reminder
		s.Reminder = &r
	}
	if s.FinalVersion != nil {
		v := *s.FinalVersion
		s.FinalVersion = &v
	}
	return s
}

func cloneSigner(s store.Signer) store.Signer {
	v := make(map[string]string, len(s.Values))
	for k, x := range s.Values {
		v[k] = x
	}
	s.Values = v
	s.CertificateID = cloneStr(s.CertificateID)
	s.InvitedAt, s.OpenedAt, s.SignedAt, s.DeclinedAt, s.NextReminderAt = cloneTime(s.InvitedAt), cloneTime(s.OpenedAt),
		cloneTime(s.SignedAt), cloneTime(s.DeclinedAt), cloneTime(s.NextReminderAt)
	return s
}

func eqFold(a, b string) bool { return strings.EqualFold(a, b) }

func sameParent(a, b *string) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return *a == *b
}

func page(items int, p, size int) (int, int) {
	p, size = store.Page(p, size, 100)
	lo := (p - 1) * size
	if lo > items {
		lo = items
	}
	hi := lo + size
	if hi > items {
		hi = items
	}
	return lo, hi
}

// ---- Folders ----

// CreateFolder implements repo.Store.
func (m *Mem) CreateFolder(_ context.Context, f store.Folder) error {
	unlock, err := m.lock("CreateFolder")
	defer unlock()
	if err != nil {
		return err
	}
	for _, x := range m.d.folders {
		if x.TenantID == f.TenantID && sameParent(x.ParentID, f.ParentID) && eqFold(x.Name, f.Name) {
			return repo.ErrConflict
		}
	}
	f.ParentID = cloneStr(f.ParentID)
	m.d.folders[f.ID] = f
	return nil
}

// GetFolder implements repo.Store.
func (m *Mem) GetFolder(_ context.Context, tenantID, id string) (store.Folder, error) {
	unlock, err := m.lock("GetFolder")
	defer unlock()
	if err != nil {
		return store.Folder{}, err
	}
	f, ok := m.d.folders[id]
	if !ok || f.TenantID != tenantID {
		return store.Folder{}, repo.ErrNotFound
	}
	return f, nil
}

// ListFolders implements repo.Store (ordered by path).
func (m *Mem) ListFolders(_ context.Context, tenantID string) ([]store.Folder, error) {
	unlock, err := m.lock("ListFolders")
	defer unlock()
	if err != nil {
		return nil, err
	}
	var out []store.Folder
	for _, f := range m.d.folders {
		if f.TenantID == tenantID {
			out = append(out, f)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Path != out[j].Path {
			return out[i].Path < out[j].Path
		}
		return out[i].ID < out[j].ID
	})
	return out, nil
}

// UpdateFolder implements repo.Store.
func (m *Mem) UpdateFolder(_ context.Context, f store.Folder) error {
	unlock, err := m.lock("UpdateFolder")
	defer unlock()
	if err != nil {
		return err
	}
	old, ok := m.d.folders[f.ID]
	if !ok || old.TenantID != f.TenantID {
		return repo.ErrNotFound
	}
	for _, x := range m.d.folders {
		if x.ID != f.ID && x.TenantID == f.TenantID && sameParent(x.ParentID, f.ParentID) && eqFold(x.Name, f.Name) {
			return repo.ErrConflict
		}
	}
	f.ParentID = cloneStr(f.ParentID)
	m.d.folders[f.ID] = f
	return nil
}

// RewriteFolderPaths implements repo.Store.
func (m *Mem) RewriteFolderPaths(_ context.Context, tenantID, oldPrefix, newPrefix string) error {
	unlock, err := m.lock("RewriteFolderPaths")
	defer unlock()
	if err != nil {
		return err
	}
	for id, f := range m.d.folders {
		if f.TenantID == tenantID && strings.HasPrefix(f.Path, oldPrefix+"/") {
			f.Path = newPrefix + strings.TrimPrefix(f.Path, oldPrefix)
			m.d.folders[id] = f
		}
	}
	return nil
}

// FolderInUse implements repo.Store (child folders or templates).
func (m *Mem) FolderInUse(_ context.Context, tenantID, id string) (bool, error) {
	unlock, err := m.lock("FolderInUse")
	defer unlock()
	if err != nil {
		return false, err
	}
	for _, f := range m.d.folders {
		if f.TenantID == tenantID && f.ParentID != nil && *f.ParentID == id {
			return true, nil
		}
	}
	for _, t := range m.d.templates {
		if t.TenantID == tenantID && t.FolderID != nil && *t.FolderID == id {
			return true, nil
		}
	}
	return false, nil
}

// DeleteFolder implements repo.Store.
func (m *Mem) DeleteFolder(_ context.Context, tenantID, id string) error {
	unlock, err := m.lock("DeleteFolder")
	defer unlock()
	if err != nil {
		return err
	}
	f, ok := m.d.folders[id]
	if !ok || f.TenantID != tenantID {
		return repo.ErrNotFound
	}
	delete(m.d.folders, id)
	return nil
}

// ---- Templates ----

func (m *Mem) templateNameTaken(t store.Template) bool {
	for _, x := range m.d.templates {
		if x.ID != t.ID && x.TenantID == t.TenantID && sameParent(x.FolderID, t.FolderID) && eqFold(x.Name, t.Name) {
			return true
		}
	}
	return false
}

// CreateTemplate implements repo.Store.
func (m *Mem) CreateTemplate(_ context.Context, t store.Template) error {
	unlock, err := m.lock("CreateTemplate")
	defer unlock()
	if err != nil {
		return err
	}
	if m.templateNameTaken(t) {
		return repo.ErrConflict
	}
	if t.Version == 0 {
		t.Version = 1
	}
	m.d.templates[t.ID] = cloneTemplate(t)
	return nil
}

// GetTemplate implements repo.Store.
func (m *Mem) GetTemplate(_ context.Context, tenantID, id string) (store.Template, error) {
	unlock, err := m.lock("GetTemplate")
	defer unlock()
	if err != nil {
		return store.Template{}, err
	}
	t, ok := m.d.templates[id]
	if !ok || t.TenantID != tenantID {
		return store.Template{}, repo.ErrNotFound
	}
	return cloneTemplate(t), nil
}

func hasTag(tags []string, tag string) bool {
	for _, t := range tags {
		if eqFold(t, tag) {
			return true
		}
	}
	return false
}

// ListTemplates implements repo.Store (newest update first).
func (m *Mem) ListTemplates(_ context.Context, tenantID string, f repo.TemplateFilter) ([]store.Template, int, error) {
	unlock, err := m.lock("ListTemplates")
	defer unlock()
	if err != nil {
		return nil, 0, err
	}
	var all []store.Template
	for _, t := range m.d.templates {
		switch {
		case t.TenantID != tenantID,
			f.FolderID != nil && *f.FolderID == "" && t.FolderID != nil,
			f.FolderID != nil && *f.FolderID != "" && (t.FolderID == nil || *t.FolderID != *f.FolderID),
			f.Tag != "" && !hasTag(t.Tags, f.Tag),
			f.Status != "" && t.Status != f.Status,
			f.Query != "" && !strings.Contains(strings.ToLower(t.Name), strings.ToLower(f.Query)):
			continue
		}
		all = append(all, cloneTemplate(t))
	}
	sort.Slice(all, func(i, j int) bool {
		if !all[i].UpdatedAt.Equal(all[j].UpdatedAt) {
			return all[i].UpdatedAt.After(all[j].UpdatedAt)
		}
		return all[i].ID > all[j].ID
	})
	lo, hi := page(len(all), f.Page, f.PageSize)
	return all[lo:hi], len(all), nil
}

// UpdateTemplate implements repo.Store.
func (m *Mem) UpdateTemplate(_ context.Context, t store.Template, expectedVersion int) (store.Template, error) {
	unlock, err := m.lock("UpdateTemplate")
	defer unlock()
	if err != nil {
		return store.Template{}, err
	}
	old, ok := m.d.templates[t.ID]
	if !ok || old.TenantID != t.TenantID {
		return store.Template{}, repo.ErrNotFound
	}
	if old.Version != expectedVersion || m.templateNameTaken(t) {
		return store.Template{}, repo.ErrConflict
	}
	t.Version = old.Version + 1
	m.d.templates[t.ID] = cloneTemplate(t)
	return cloneTemplate(t), nil
}

// TemplateInUse implements repo.Store.
func (m *Mem) TemplateInUse(_ context.Context, tenantID, id string) (bool, error) {
	unlock, err := m.lock("TemplateInUse")
	defer unlock()
	if err != nil {
		return false, err
	}
	for _, s := range m.d.subs {
		if s.TenantID == tenantID && s.TemplateID == id && (s.Status == store.SubmissionDraft || s.Status == store.SubmissionInProgress) {
			return true, nil
		}
	}
	return false, nil
}

// DeleteTemplate implements repo.Store (refused while any submission refers to it).
func (m *Mem) DeleteTemplate(_ context.Context, tenantID, id string) error {
	unlock, err := m.lock("DeleteTemplate")
	defer unlock()
	if err != nil {
		return err
	}
	t, ok := m.d.templates[id]
	if !ok || t.TenantID != tenantID {
		return repo.ErrNotFound
	}
	for _, s := range m.d.subs {
		if s.TemplateID == id {
			return repo.ErrConflict
		}
	}
	delete(m.d.templates, id)
	return nil
}

// ---- Submissions ----

// CreateSubmission implements repo.Store.
func (m *Mem) CreateSubmission(_ context.Context, s store.Submission, signers []store.Signer, v0 store.DocumentVersion) error {
	unlock, err := m.lock("CreateSubmission")
	defer unlock()
	if err != nil {
		return err
	}
	t, ok := m.d.templates[s.TemplateID]
	if !ok || t.TenantID != s.TenantID {
		return repo.ErrNotFound
	}
	m.d.subs[s.ID] = cloneSubmission(s)
	for _, sg := range signers {
		m.d.signers[sg.ID] = cloneSigner(sg)
	}
	m.d.versions[verKey(v0.SubmissionID, v0.Version)] = v0
	return nil
}

func verKey(sub string, v int) string { return sub + "/" + strconv.Itoa(v) }

func (m *Mem) signersOf(subID string) []store.Signer {
	var out []store.Signer
	for _, sg := range m.d.signers {
		if sg.SubmissionID == subID {
			out = append(out, cloneSigner(sg))
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Position != out[j].Position {
			return out[i].Position < out[j].Position
		}
		return out[i].ID < out[j].ID
	})
	return out
}

// GetSubmission implements repo.Store.
func (m *Mem) GetSubmission(_ context.Context, tenantID, id string) (store.Submission, []store.Signer, error) {
	unlock, err := m.lock("GetSubmission")
	defer unlock()
	if err != nil {
		return store.Submission{}, nil, err
	}
	s, ok := m.d.subs[id]
	if !ok || s.TenantID != tenantID {
		return store.Submission{}, nil, repo.ErrNotFound
	}
	return cloneSubmission(s), m.signersOf(id), nil
}

// LockSubmission implements repo.Store (Tx already serialises).
func (m *Mem) LockSubmission(ctx context.Context, tenantID, id string) (store.Submission, []store.Signer, error) {
	if unlock, err := m.lock("LockSubmission"); err != nil {
		unlock()
		return store.Submission{}, nil, err
	} else {
		unlock()
	}
	return m.GetSubmission(ctx, tenantID, id)
}

// ListSubmissions implements repo.Store (newest first).
func (m *Mem) ListSubmissions(_ context.Context, tenantID string, f repo.SubmissionFilter) ([]store.Submission, int, error) {
	unlock, err := m.lock("ListSubmissions")
	defer unlock()
	if err != nil {
		return nil, 0, err
	}
	var all []store.Submission
	for _, s := range m.d.subs {
		switch {
		case s.TenantID != tenantID,
			f.Status != "" && s.Status != f.Status,
			f.TemplateID != "" && s.TemplateID != f.TemplateID,
			f.CreatedBy != "" && s.CreatedBy != f.CreatedBy,
			f.Query != "" && !strings.Contains(strings.ToLower(s.Name), strings.ToLower(f.Query)):
			continue
		}
		all = append(all, cloneSubmission(s))
	}
	sort.Slice(all, func(i, j int) bool {
		if !all[i].CreatedAt.Equal(all[j].CreatedAt) {
			return all[i].CreatedAt.After(all[j].CreatedAt)
		}
		return all[i].ID > all[j].ID
	})
	lo, hi := page(len(all), f.Page, f.PageSize)
	return all[lo:hi], len(all), nil
}

// UpdateSubmission implements repo.Store.
func (m *Mem) UpdateSubmission(_ context.Context, s store.Submission) error {
	unlock, err := m.lock("UpdateSubmission")
	defer unlock()
	if err != nil {
		return err
	}
	old, ok := m.d.subs[s.ID]
	if !ok || old.TenantID != s.TenantID {
		return repo.ErrNotFound
	}
	m.d.subs[s.ID] = cloneSubmission(s)
	return nil
}

// UpdateSigner implements repo.Store.
func (m *Mem) UpdateSigner(_ context.Context, s store.Signer) error {
	unlock, err := m.lock("UpdateSigner")
	defer unlock()
	if err != nil {
		return err
	}
	old, ok := m.d.signers[s.ID]
	if !ok || old.TenantID != s.TenantID {
		return repo.ErrNotFound
	}
	m.d.signers[s.ID] = cloneSigner(s)
	return nil
}

// DeleteSubmission implements repo.Store (cascades to signers, versions, events, jobs, preparations).
func (m *Mem) DeleteSubmission(_ context.Context, tenantID, id string) error {
	unlock, err := m.lock("DeleteSubmission")
	defer unlock()
	if err != nil {
		return err
	}
	s, ok := m.d.subs[id]
	if !ok || s.TenantID != tenantID {
		return repo.ErrNotFound
	}
	delete(m.d.subs, id)
	for k, sg := range m.d.signers {
		if sg.SubmissionID == id {
			delete(m.d.signers, k)
			for qk, q := range m.d.qes {
				if q.SignerID == k {
					delete(m.d.qes, qk)
				}
			}
		}
	}
	for k, v := range m.d.versions {
		if v.SubmissionID == id {
			delete(m.d.versions, k)
		}
	}
	for k, j := range m.d.jobs {
		if j.SubmissionID == id {
			delete(m.d.jobs, k)
		}
	}
	kept := m.d.events[:0]
	for _, e := range m.d.events {
		if e.SubmissionID == nil || *e.SubmissionID != id {
			kept = append(kept, e)
		}
	}
	m.d.events = kept
	return nil
}

// GetSigner implements repo.Store.
func (m *Mem) GetSigner(_ context.Context, tenantID, id string) (store.Signer, error) {
	unlock, err := m.lock("GetSigner")
	defer unlock()
	if err != nil {
		return store.Signer{}, err
	}
	s, ok := m.d.signers[id]
	if !ok || s.TenantID != tenantID {
		return store.Signer{}, repo.ErrNotFound
	}
	return cloneSigner(s), nil
}

// Inbox implements repo.Store: to-sign = invited/opened on an in-progress
// submission; signed = signed slots. Newest first.
func (m *Mem) Inbox(_ context.Context, tenantID, userID string, signed bool, p, size int) ([]repo.InboxItem, int, error) {
	unlock, err := m.lock("Inbox")
	defer unlock()
	if err != nil {
		return nil, 0, err
	}
	var all []repo.InboxItem
	for _, sg := range m.d.signers {
		if sg.TenantID != tenantID || sg.UserID != userID {
			continue
		}
		s := m.d.subs[sg.SubmissionID]
		if signed {
			if sg.Status != store.SignerSigned {
				continue
			}
		} else if (sg.Status != store.SignerInvited && sg.Status != store.SignerOpened) || s.Status != store.SubmissionInProgress {
			continue
		}
		all = append(all, repo.InboxItem{Signer: cloneSigner(sg), Submission: cloneSubmission(s)})
	}
	sort.Slice(all, func(i, j int) bool {
		if !all[i].Submission.CreatedAt.Equal(all[j].Submission.CreatedAt) {
			return all[i].Submission.CreatedAt.After(all[j].Submission.CreatedAt)
		}
		return all[i].Signer.ID > all[j].Signer.ID
	})
	lo, hi := page(len(all), p, size)
	return all[lo:hi], len(all), nil
}

// AddVersion implements repo.Store.
func (m *Mem) AddVersion(_ context.Context, v store.DocumentVersion) error {
	unlock, err := m.lock("AddVersion")
	defer unlock()
	if err != nil {
		return err
	}
	k := verKey(v.SubmissionID, v.Version)
	if _, dup := m.d.versions[k]; dup {
		return repo.ErrConflict
	}
	m.d.versions[k] = v
	return nil
}

// GetVersion implements repo.Store.
func (m *Mem) GetVersion(_ context.Context, tenantID, submissionID string, version int) (store.DocumentVersion, error) {
	unlock, err := m.lock("GetVersion")
	defer unlock()
	if err != nil {
		return store.DocumentVersion{}, err
	}
	v, ok := m.d.versions[verKey(submissionID, version)]
	if !ok || v.TenantID != tenantID {
		return store.DocumentVersion{}, repo.ErrNotFound
	}
	return v, nil
}

// ListVersions implements repo.Store (ascending).
func (m *Mem) ListVersions(_ context.Context, tenantID, submissionID string) ([]store.DocumentVersion, error) {
	unlock, err := m.lock("ListVersions")
	defer unlock()
	if err != nil {
		return nil, err
	}
	var out []store.DocumentVersion
	for _, v := range m.d.versions {
		if v.TenantID == tenantID && v.SubmissionID == submissionID {
			out = append(out, v)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Version < out[j].Version })
	return out, nil
}

// ---- Certificates ----

// CreateCertificate implements repo.Store.
func (m *Mem) CreateCertificate(_ context.Context, c store.Certificate) error {
	unlock, err := m.lock("CreateCertificate")
	defer unlock()
	if err != nil {
		return err
	}
	for _, x := range m.d.certs {
		if x.TenantID != c.TenantID {
			continue
		}
		if x.Serial == c.Serial {
			return repo.ErrConflict
		}
		if c.Kind == store.KindSigner && x.Kind == store.KindSigner && c.Status == store.CertActive && x.Status == store.CertActive &&
			x.OwnerUserID != nil && c.OwnerUserID != nil && *x.OwnerUserID == *c.OwnerUserID {
			return repo.ErrConflict
		}
		if c.Kind == store.KindCA && x.Kind == store.KindCA && c.Status == store.CertActive && x.Status == store.CertActive &&
			c.SupersededBy == nil && x.SupersededBy == nil {
			return repo.ErrConflict
		}
		if c.Kind == store.KindSystem && x.Kind == store.KindSystem && c.Status == store.CertActive && x.Status == store.CertActive &&
			c.IssuerID != nil && x.IssuerID != nil && *c.IssuerID == *x.IssuerID {
			return repo.ErrConflict
		}
	}
	m.d.certs[c.ID] = c
	return nil
}

// GetCertificate implements repo.Store.
func (m *Mem) GetCertificate(_ context.Context, tenantID, id string) (store.Certificate, error) {
	unlock, err := m.lock("GetCertificate")
	defer unlock()
	if err != nil {
		return store.Certificate{}, err
	}
	c, ok := m.d.certs[id]
	if !ok || c.TenantID != tenantID {
		return store.Certificate{}, repo.ErrNotFound
	}
	return c, nil
}

// ListCertificates implements repo.Store (newest first).
func (m *Mem) ListCertificates(_ context.Context, tenantID string, f repo.CertificateFilter) ([]store.Certificate, int, error) {
	unlock, err := m.lock("ListCertificates")
	defer unlock()
	if err != nil {
		return nil, 0, err
	}
	q := strings.ToLower(f.Query)
	var all []store.Certificate
	for _, c := range m.d.certs {
		switch {
		case c.TenantID != tenantID,
			f.Kind != "" && c.Kind != f.Kind,
			f.Status != "" && c.Status != f.Status,
			f.OwnerID != "" && (c.OwnerUserID == nil || *c.OwnerUserID != f.OwnerID),
			q != "" && !strings.Contains(strings.ToLower(c.SubjectCN), q) && !strings.Contains(strings.ToLower(c.Email), q):
			continue
		}
		all = append(all, c)
	}
	sort.Slice(all, func(i, j int) bool {
		if !all[i].CreatedAt.Equal(all[j].CreatedAt) {
			return all[i].CreatedAt.After(all[j].CreatedAt)
		}
		return all[i].ID > all[j].ID
	})
	lo, hi := page(len(all), f.Page, f.PageSize)
	return all[lo:hi], len(all), nil
}

// UpdateCertificate implements repo.Store.
func (m *Mem) UpdateCertificate(_ context.Context, c store.Certificate) error {
	unlock, err := m.lock("UpdateCertificate")
	defer unlock()
	if err != nil {
		return err
	}
	old, ok := m.d.certs[c.ID]
	if !ok || old.TenantID != c.TenantID {
		return repo.ErrNotFound
	}
	m.d.certs[c.ID] = c
	return nil
}

// ActiveSignerCertificate implements repo.Store.
func (m *Mem) ActiveSignerCertificate(_ context.Context, tenantID, userID string) (store.Certificate, error) {
	unlock, err := m.lock("ActiveSignerCertificate")
	defer unlock()
	if err != nil {
		return store.Certificate{}, err
	}
	for _, c := range m.d.certs {
		if c.TenantID == tenantID && c.Kind == store.KindSigner && c.Status == store.CertActive && c.OwnerUserID != nil && *c.OwnerUserID == userID {
			return c, nil
		}
	}
	return store.Certificate{}, repo.ErrNotFound
}

// CurrentCA implements repo.Store.
func (m *Mem) CurrentCA(_ context.Context, tenantID string) (store.Certificate, error) {
	unlock, err := m.lock("CurrentCA")
	defer unlock()
	if err != nil {
		return store.Certificate{}, err
	}
	var best *store.Certificate
	for _, c := range m.d.certs {
		if c.TenantID == tenantID && c.Kind == store.KindCA && c.Status == store.CertActive && c.SupersededBy == nil {
			if best == nil || c.NotAfter.After(best.NotAfter) {
				cc := c
				best = &cc
			}
		}
	}
	if best == nil {
		return store.Certificate{}, repo.ErrNotFound
	}
	return *best, nil
}

// ListCAs implements repo.Store (oldest first).
func (m *Mem) ListCAs(_ context.Context, tenantID string) ([]store.Certificate, error) {
	unlock, err := m.lock("ListCAs")
	defer unlock()
	if err != nil {
		return nil, err
	}
	var out []store.Certificate
	for _, c := range m.d.certs {
		if c.TenantID == tenantID && c.Kind == store.KindCA {
			out = append(out, c)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].NotBefore.Before(out[j].NotBefore) })
	return out, nil
}

// IssuedBy implements repo.Store.
func (m *Mem) IssuedBy(_ context.Context, tenantID, issuerID string) ([]store.Certificate, error) {
	unlock, err := m.lock("IssuedBy")
	defer unlock()
	if err != nil {
		return nil, err
	}
	var out []store.Certificate
	for _, c := range m.d.certs {
		if c.TenantID == tenantID && c.IssuerID != nil && *c.IssuerID == issuerID {
			out = append(out, c)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out, nil
}

// SystemCertificate implements repo.Store.
func (m *Mem) SystemCertificate(_ context.Context, tenantID, issuerID string) (store.Certificate, error) {
	unlock, err := m.lock("SystemCertificate")
	defer unlock()
	if err != nil {
		return store.Certificate{}, err
	}
	for _, c := range m.d.certs {
		if c.TenantID == tenantID && c.Kind == store.KindSystem && c.Status == store.CertActive && c.IssuerID != nil && *c.IssuerID == issuerID {
			return c, nil
		}
	}
	return store.Certificate{}, repo.ErrNotFound
}

// RecordPINFailure implements repo.Store.
func (m *Mem) RecordPINFailure(_ context.Context, tenantID, certID string, lockAfter int, lockUntil time.Time) (int, bool, error) {
	unlock, err := m.lock("RecordPINFailure")
	defer unlock()
	if err != nil {
		return 0, false, err
	}
	c, ok := m.d.certs[certID]
	if !ok || c.TenantID != tenantID {
		return 0, false, repo.ErrNotFound
	}
	c.FailedPINCount++
	n, locked := c.FailedPINCount, false
	if c.FailedPINCount >= lockAfter {
		lu := lockUntil
		c.LockedUntil, c.FailedPINCount, locked = &lu, 0, true
	}
	m.d.certs[certID] = c
	return n, locked, nil
}

// ResetPINFailures implements repo.Store.
func (m *Mem) ResetPINFailures(_ context.Context, tenantID, certID string) error {
	unlock, err := m.lock("ResetPINFailures")
	defer unlock()
	if err != nil {
		return err
	}
	c, ok := m.d.certs[certID]
	if !ok || c.TenantID != tenantID {
		return repo.ErrNotFound
	}
	c.FailedPINCount, c.LockedUntil = 0, nil
	m.d.certs[certID] = c
	return nil
}

// SignedWithCertificate implements repo.Store (newest first).
func (m *Mem) SignedWithCertificate(_ context.Context, tenantID, certID string, limit int) ([]repo.SignedDocument, error) {
	unlock, err := m.lock("SignedWithCertificate")
	defer unlock()
	if err != nil {
		return nil, err
	}
	var out []repo.SignedDocument
	for _, sg := range m.d.signers {
		if sg.TenantID == tenantID && sg.CertificateID != nil && *sg.CertificateID == certID && sg.SignedAt != nil {
			out = append(out, repo.SignedDocument{SubmissionID: sg.SubmissionID, SubmissionName: m.d.subs[sg.SubmissionID].Name,
				SignerID: sg.ID, SignedAt: *sg.SignedAt})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].SignedAt.After(out[j].SignedAt) })
	if len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

// ---- QES ----

// CreateQES implements repo.Store.
func (m *Mem) CreateQES(_ context.Context, q store.QESPreparation) error {
	unlock, err := m.lock("CreateQES")
	defer unlock()
	if err != nil {
		return err
	}
	m.d.qes[q.ID] = q
	return nil
}

// GetQES implements repo.Store.
func (m *Mem) GetQES(_ context.Context, tenantID, id string) (store.QESPreparation, error) {
	unlock, err := m.lock("GetQES")
	defer unlock()
	if err != nil {
		return store.QESPreparation{}, err
	}
	q, ok := m.d.qes[id]
	if !ok || q.TenantID != tenantID {
		return store.QESPreparation{}, repo.ErrNotFound
	}
	return q, nil
}

// MarkQESUsed implements repo.Store (a used preparation cannot be used again).
func (m *Mem) MarkQESUsed(_ context.Context, tenantID, id string, at time.Time) error {
	unlock, err := m.lock("MarkQESUsed")
	defer unlock()
	if err != nil {
		return err
	}
	q, ok := m.d.qes[id]
	if !ok || q.TenantID != tenantID {
		return repo.ErrNotFound
	}
	if q.UsedAt != nil {
		return repo.ErrConflict
	}
	q.UsedAt = &at
	m.d.qes[id] = q
	return nil
}

// ---- History ----

// AddEvent implements repo.Store.
func (m *Mem) AddEvent(_ context.Context, e store.Event) error {
	unlock, err := m.lock("AddEvent")
	defer unlock()
	if err != nil {
		return err
	}
	m.d.events = append(m.d.events, e)
	return nil
}

// ListEvents implements repo.Store (chronological).
func (m *Mem) ListEvents(_ context.Context, tenantID, submissionID string) ([]store.Event, error) {
	unlock, err := m.lock("ListEvents")
	defer unlock()
	if err != nil {
		return nil, err
	}
	var out []store.Event
	for _, e := range m.d.events {
		if e.TenantID == tenantID && e.SubmissionID != nil && *e.SubmissionID == submissionID {
			out = append(out, e)
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].At.Before(out[j].At) })
	return out, nil
}

// ---- Jobs ----

// EnqueueJob implements repo.Store (one job per submission and kind).
func (m *Mem) EnqueueJob(_ context.Context, j store.Job) error {
	unlock, err := m.lock("EnqueueJob")
	defer unlock()
	if err != nil {
		return err
	}
	for _, x := range m.d.jobs {
		if x.SubmissionID == j.SubmissionID && x.Kind == j.Kind {
			return nil
		}
	}
	m.d.jobs[j.ID] = j
	return nil
}

// ClaimJobsSystem implements repo.Store: due, unfinished jobs; the lease moves
// next_attempt_at forward so a crashed worker's job is retried later.
func (m *Mem) ClaimJobsSystem(_ context.Context, now time.Time, limit int, lease time.Duration) ([]store.Job, error) {
	unlock, err := m.lock("ClaimJobsSystem")
	defer unlock()
	if err != nil {
		return nil, err
	}
	var due []store.Job
	for _, j := range m.d.jobs {
		if j.DoneAt == nil && !j.NextAttemptAt.After(now) {
			due = append(due, j)
		}
	}
	sort.Slice(due, func(i, j int) bool { return due[i].NextAttemptAt.Before(due[j].NextAttemptAt) })
	if len(due) > limit {
		due = due[:limit]
	}
	for i, j := range due {
		j.Attempts++
		j.NextAttemptAt = now.Add(lease)
		m.d.jobs[j.ID] = j
		due[i] = j
	}
	return due, nil
}

// CompleteJobSystem implements repo.Store.
func (m *Mem) CompleteJobSystem(_ context.Context, id string, at time.Time) error {
	unlock, err := m.lock("CompleteJobSystem")
	defer unlock()
	if err != nil {
		return err
	}
	j, ok := m.d.jobs[id]
	if !ok {
		return repo.ErrNotFound
	}
	j.DoneAt = &at
	m.d.jobs[id] = j
	return nil
}

// FailJobSystem implements repo.Store.
func (m *Mem) FailJobSystem(_ context.Context, id, reason string, next time.Time) error {
	unlock, err := m.lock("FailJobSystem")
	defer unlock()
	if err != nil {
		return err
	}
	j, ok := m.d.jobs[id]
	if !ok {
		return repo.ErrNotFound
	}
	j.LastError, j.NextAttemptAt = reason, next
	m.d.jobs[id] = j
	return nil
}

// ---- Scheduled work ----

// ExpireDueSystem implements repo.Store: in-progress submissions past their
// expiry become expired, each exactly once.
func (m *Mem) ExpireDueSystem(_ context.Context, now time.Time, limit int) ([]store.Submission, error) {
	unlock, err := m.lock("ExpireDueSystem")
	defer unlock()
	if err != nil {
		return nil, err
	}
	var out []store.Submission
	for id, s := range m.d.subs {
		if len(out) >= limit {
			break
		}
		if s.Status == store.SubmissionInProgress && s.ExpiresAt != nil && !s.ExpiresAt.After(now) {
			s.Status, s.UpdatedAt = store.SubmissionExpired, now
			m.d.subs[id] = s
			out = append(out, cloneSubmission(s))
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out, nil
}

// ClaimRemindersSystem implements repo.Store: each due, pending signer of an
// in-progress submission is claimed once per interval (next_reminder_at moves
// forward or clears at the maximum).
func (m *Mem) ClaimRemindersSystem(_ context.Context, now time.Time, limit int) ([]repo.ReminderDue, error) {
	unlock, err := m.lock("ClaimRemindersSystem")
	defer unlock()
	if err != nil {
		return nil, err
	}
	var ids []string
	for id, sg := range m.d.signers {
		if (sg.Status == store.SignerInvited || sg.Status == store.SignerOpened) && sg.NextReminderAt != nil && !sg.NextReminderAt.After(now) {
			ids = append(ids, id)
		}
	}
	sort.Strings(ids)
	var out []repo.ReminderDue
	for _, id := range ids {
		if len(out) >= limit {
			break
		}
		sg := m.d.signers[id]
		s := m.d.subs[sg.SubmissionID]
		if s.Status != store.SubmissionInProgress || s.Reminder == nil {
			sg.NextReminderAt = nil
			m.d.signers[id] = sg
			continue
		}
		sg.RemindersSent++
		if sg.RemindersSent >= s.Reminder.Max {
			sg.NextReminderAt = nil
		} else {
			n := now.Add(time.Duration(s.Reminder.IntervalDays) * 24 * time.Hour)
			sg.NextReminderAt = &n
		}
		m.d.signers[id] = sg
		out = append(out, repo.ReminderDue{Signer: cloneSigner(sg), Submission: cloneSubmission(s), Number: sg.RemindersSent})
	}
	return out, nil
}

// ExpiredQESSystem implements repo.Store.
func (m *Mem) ExpiredQESSystem(_ context.Context, now time.Time, limit int) ([]store.QESPreparation, error) {
	unlock, err := m.lock("ExpiredQESSystem")
	defer unlock()
	if err != nil {
		return nil, err
	}
	var out []store.QESPreparation
	for _, q := range m.d.qes {
		if !q.ExpiresAt.After(now) || q.UsedAt != nil {
			out = append(out, q)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	if len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

// DeleteQESSystem implements repo.Store.
func (m *Mem) DeleteQESSystem(_ context.Context, id string) error {
	unlock, err := m.lock("DeleteQESSystem")
	defer unlock()
	if err != nil {
		return err
	}
	delete(m.d.qes, id)
	return nil
}

// DueCRLsSystem implements repo.Store: CAs whose CRL is missing or due.
func (m *Mem) DueCRLsSystem(_ context.Context, now time.Time, limit int) ([]store.Certificate, error) {
	unlock, err := m.lock("DueCRLsSystem")
	defer unlock()
	if err != nil {
		return nil, err
	}
	var out []store.Certificate
	for _, c := range m.d.certs {
		if c.Kind == store.KindCA && c.Status == store.CertActive && (c.CRLNextUpdate == nil || !c.CRLNextUpdate.After(now)) {
			out = append(out, c)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	if len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

// TenantsSystem implements repo.Store (tenants holding any signing data).
func (m *Mem) TenantsSystem(_ context.Context) ([]string, error) {
	unlock, err := m.lock("TenantsSystem")
	defer unlock()
	if err != nil {
		return nil, err
	}
	seen := map[string]bool{}
	for _, t := range m.d.templates {
		seen[t.TenantID] = true
	}
	for _, c := range m.d.certs {
		seen[c.TenantID] = true
	}
	out := make([]string, 0, len(seen))
	for t := range seen {
		out = append(out, t)
	}
	sort.Strings(out)
	return out, nil
}

// AppendAudit implements repo.Store.
func (m *Mem) AppendAudit(_ context.Context, row store.AuditRow) error {
	unlock, err := m.lock("AppendAudit")
	defer unlock()
	if err != nil {
		return err
	}
	m.d.audit = append(m.d.audit, row)
	return nil
}
