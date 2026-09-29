// Package repotest is the behavioural contract of repo.Store, run against
// both implementations: memstore (unit tests) and repodb (the integration
// suite against TimescaleDB). Keeping one suite guarantees the fake the
// service tests rely on behaves like the database.
package repotest

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/go-tangra/go-tangra-signing/v4/internal/repo"
	"github.com/go-tangra/go-tangra-signing/v4/internal/store"
)

// Tenants used by the suite (valid uuids for the database).
const (
	TenantA = "0190f7c2-6a3e-7c1a-9b2e-2f6f9d1b4c55"
	TenantB = "0190f7c2-6a3e-7c1a-9b2e-2f6f9d1b4c66"
)

var ctx = context.Background()

func ptr[T any](v T) *T { return &v }

// Template returns a valid template of tenant.
func Template(tenant, name string) store.Template {
	now := time.Now().UTC().Truncate(time.Microsecond)
	return store.Template{ID: store.NewID(), TenantID: tenant, Name: name, Status: store.TemplateActive, Tags: []string{"hr"},
		PDFKey: "tenants/" + tenant + "/templates/x/y.pdf", PDFSHA256: "abc", PDFSize: 10, PDFPages: 1, FileName: "c.pdf",
		Parties: []store.Party{{Key: "p1", Name: "Employee"}, {Key: "p2", Name: "Employer"}},
		Fields: []store.Field{{ID: "f1", Name: "Salary", Type: "number", Party: "p1", Page: 1, X: .1, Y: .1, W: .2, H: .05, Required: true,
			Conditions: &store.Conditions{Mode: "all", Effect: "visible", Rules: []store.Rule{{Field: "x", Op: "checked"}}}}},
		CreatedAt: now, UpdatedAt: now, CreatedBy: "u1", UpdatedBy: "u1"}
}

// Submission returns an in-progress submission of t with two signers.
func Submission(t store.Template) (store.Submission, []store.Signer, store.DocumentVersion) {
	now := time.Now().UTC().Truncate(time.Microsecond)
	s := store.Submission{ID: store.NewID(), TenantID: t.TenantID, TemplateID: t.ID, Name: t.Name, PDFKey: t.PDFKey, PDFSHA256: t.PDFSHA256,
		Fields: t.Fields, Parties: t.Parties, Mode: store.ModeSequential, Status: store.SubmissionInProgress,
		Reminder: &store.Reminder{IntervalDays: 1, Max: 2}, CreatedAt: now, CreatedBy: "sender", UpdatedAt: now}
	signers := []store.Signer{
		{ID: store.NewID(), TenantID: t.TenantID, SubmissionID: s.ID, UserID: "maria", Name: "Maria", Email: "m@example.org", Party: "p1",
			Position: 0, Status: store.SignerInvited, Values: map[string]string{}},
		{ID: store.NewID(), TenantID: t.TenantID, SubmissionID: s.ID, UserID: "ivan", Name: "Ivan", Email: "i@example.org", Party: "p2",
			Position: 1, Status: store.SignerPending, Values: map[string]string{}},
	}
	v0 := store.DocumentVersion{SubmissionID: s.ID, Version: 0, TenantID: t.TenantID, ObjectKey: t.PDFKey, SHA256: t.PDFSHA256, Size: t.PDFSize, CreatedAt: now}
	return s, signers, v0
}

// Certificate returns a certificate row of the given kind.
func Certificate(tenant, kind string, issuer *string, owner *string) store.Certificate {
	now := time.Now().UTC().Truncate(time.Microsecond)
	prot := store.KeySealed
	if kind == store.KindSigner {
		prot = store.KeyPIN
	}
	return store.Certificate{ID: store.NewID(), TenantID: tenant, Kind: kind, IssuerID: issuer, OwnerUserID: owner, SubjectCN: "CN " + kind,
		Email: "x@example.org", Serial: store.NewID(), NotBefore: now.Add(-time.Hour), NotAfter: now.AddDate(1, 0, 0),
		CertDER: []byte{1}, KeyProtection: prot, KeyBlob: []byte{2}, Status: store.CertActive, CreatedAt: now, CreatedBy: "u1"}
}

// Run runs the whole contract against a fresh store from newStore.
func Run(t *testing.T, newStore func(t *testing.T) repo.Store) {
	t.Run("folders", func(t *testing.T) { folders(t, newStore(t)) })
	t.Run("templates", func(t *testing.T) { templates(t, newStore(t)) })
	t.Run("submissions", func(t *testing.T) { submissions(t, newStore(t)) })
	t.Run("tx", func(t *testing.T) { tx(t, newStore(t)) })
	t.Run("certificates", func(t *testing.T) { certificates(t, newStore(t)) })
	t.Run("qes and events", func(t *testing.T) { qesAndEvents(t, newStore(t)) })
	t.Run("jobs", func(t *testing.T) { jobs(t, newStore(t)) })
	t.Run("scheduled", func(t *testing.T) { scheduled(t, newStore(t)) })
}

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

func folders(t *testing.T, s repo.Store) {
	root := store.Folder{ID: store.NewID(), TenantID: TenantA, Name: "HR", Path: "/HR", CreatedAt: time.Now(), UpdatedAt: time.Now()}
	must(t, s.CreateFolder(ctx, root))
	if err := s.CreateFolder(ctx, store.Folder{ID: store.NewID(), TenantID: TenantA, Name: "hr", Path: "/hr"}); !errors.Is(err, repo.ErrConflict) {
		t.Fatalf("duplicate root name: %v", err)
	}
	must(t, s.CreateFolder(ctx, store.Folder{ID: store.NewID(), TenantID: TenantB, Name: "HR", Path: "/HR"})) // other tenant
	child := store.Folder{ID: store.NewID(), TenantID: TenantA, ParentID: &root.ID, Name: "Contracts", Path: "/HR/Contracts"}
	must(t, s.CreateFolder(ctx, child))
	if _, err := s.GetFolder(ctx, TenantB, child.ID); !errors.Is(err, repo.ErrNotFound) {
		t.Fatalf("cross-tenant folder: %v", err)
	}
	list, err := s.ListFolders(ctx, TenantA)
	must(t, err)
	if len(list) != 2 || list[0].Path != "/HR" || list[1].Path != "/HR/Contracts" {
		t.Fatalf("folders = %+v", list)
	}
	if used, _ := s.FolderInUse(ctx, TenantA, root.ID); !used {
		t.Fatal("root with child must be in use")
	}
	root.Name, root.Path = "People", "/People"
	must(t, s.UpdateFolder(ctx, root))
	must(t, s.RewriteFolderPaths(ctx, TenantA, "/HR", "/People"))
	got, _ := s.GetFolder(ctx, TenantA, child.ID)
	if got.Path != "/People/Contracts" {
		t.Fatalf("rewritten path = %q", got.Path)
	}
	if err := s.UpdateFolder(ctx, store.Folder{ID: child.ID, TenantID: TenantB, Name: "x"}); !errors.Is(err, repo.ErrNotFound) {
		t.Fatalf("cross-tenant update: %v", err)
	}
	must(t, s.DeleteFolder(ctx, TenantA, child.ID))
	if used, _ := s.FolderInUse(ctx, TenantA, root.ID); used {
		t.Fatal("empty root in use")
	}
	if err := s.DeleteFolder(ctx, TenantA, child.ID); !errors.Is(err, repo.ErrNotFound) {
		t.Fatalf("delete twice: %v", err)
	}
	tpl := Template(TenantA, "In folder")
	tpl.FolderID = &root.ID
	must(t, s.CreateTemplate(ctx, tpl))
	if used, _ := s.FolderInUse(ctx, TenantA, root.ID); !used {
		t.Fatal("folder with template must be in use")
	}
}

func templates(t *testing.T, s repo.Store) {
	a := Template(TenantA, "Contract")
	must(t, s.CreateTemplate(ctx, a))
	dup := Template(TenantA, "contract")
	if err := s.CreateTemplate(ctx, dup); !errors.Is(err, repo.ErrConflict) {
		t.Fatalf("duplicate name: %v", err)
	}
	must(t, s.CreateTemplate(ctx, Template(TenantB, "Contract")))
	got, err := s.GetTemplate(ctx, TenantA, a.ID)
	must(t, err)
	if got.Version != 1 || len(got.Fields) != 1 || got.Fields[0].Conditions == nil || got.Parties[1].Name != "Employer" || got.Tags[0] != "hr" {
		t.Fatalf("template = %+v", got)
	}
	if _, err := s.GetTemplate(ctx, TenantB, a.ID); !errors.Is(err, repo.ErrNotFound) {
		t.Fatalf("cross-tenant: %v", err)
	}
	draft := Template(TenantA, "Draft NDA")
	draft.Status, draft.Tags = store.TemplateDraft, []string{"legal"}
	draft.UpdatedAt = a.UpdatedAt.Add(time.Second)
	must(t, s.CreateTemplate(ctx, draft))
	list, total, err := s.ListTemplates(ctx, TenantA, repo.TemplateFilter{})
	must(t, err)
	if total != 2 || list[0].ID != draft.ID {
		t.Fatalf("list = %d %+v", total, list)
	}
	for name, f := range map[string]repo.TemplateFilter{"tag": {Tag: "LEGAL"}, "status": {Status: store.TemplateDraft},
		"query": {Query: "nda"}, "page": {Page: 1, PageSize: 1}} {
		l, tot, err := s.ListTemplates(ctx, TenantA, f)
		must(t, err)
		if len(l) != 1 || (name != "page" && l[0].ID != draft.ID) || (name == "page" && tot != 2) {
			t.Errorf("%s: %d %+v", name, tot, l)
		}
	}
	if l, _, _ := s.ListTemplates(ctx, TenantA, repo.TemplateFilter{FolderID: ptr("")}); len(l) != 2 {
		t.Fatalf("root filter = %d", len(l))
	}
	if l, _, _ := s.ListTemplates(ctx, TenantA, repo.TemplateFilter{FolderID: ptr(store.NewID())}); len(l) != 0 {
		t.Fatalf("folder filter = %d", len(l))
	}
	got.Name = "Contract v2"
	upd, err := s.UpdateTemplate(ctx, got, 1)
	must(t, err)
	if upd.Version != 2 {
		t.Fatalf("version = %d", upd.Version)
	}
	if _, err := s.UpdateTemplate(ctx, got, 1); !errors.Is(err, repo.ErrConflict) {
		t.Fatalf("stale version: %v", err)
	}
	got.Name = "Draft NDA"
	if _, err := s.UpdateTemplate(ctx, got, 2); !errors.Is(err, repo.ErrConflict) {
		t.Fatalf("rename onto existing: %v", err)
	}
	if _, err := s.UpdateTemplate(ctx, store.Template{ID: a.ID, TenantID: TenantB, Name: "x"}, 2); !errors.Is(err, repo.ErrNotFound) {
		t.Fatalf("cross-tenant update: %v", err)
	}
	// In use while a draft/in-progress submission refers to it; never deletable while referenced.
	sub, signers, v0 := Submission(upd)
	must(t, s.CreateSubmission(ctx, sub, signers, v0))
	if used, _ := s.TemplateInUse(ctx, TenantA, a.ID); !used {
		t.Fatal("in use")
	}
	sub.Status = store.SubmissionCompleted
	must(t, s.UpdateSubmission(ctx, sub))
	if used, _ := s.TemplateInUse(ctx, TenantA, a.ID); used {
		t.Fatal("completed submission keeps it in use")
	}
	if err := s.DeleteTemplate(ctx, TenantA, a.ID); !errors.Is(err, repo.ErrConflict) {
		t.Fatalf("delete referenced: %v", err)
	}
	must(t, s.DeleteTemplate(ctx, TenantA, draft.ID))
	if err := s.DeleteTemplate(ctx, TenantA, draft.ID); !errors.Is(err, repo.ErrNotFound) {
		t.Fatalf("delete twice: %v", err)
	}
}

func submissions(t *testing.T, s repo.Store) {
	tpl := Template(TenantA, "Contract")
	must(t, s.CreateTemplate(ctx, tpl))
	sub, signers, v0 := Submission(tpl)
	must(t, s.CreateSubmission(ctx, sub, signers, v0))
	foreign := Template(TenantB, "B")
	must(t, s.CreateTemplate(ctx, foreign))
	bad, bs, bv := Submission(foreign)
	bad.TenantID = TenantA // a tenant-A submission of a tenant-B template
	for i := range bs {
		bs[i].TenantID = TenantA
	}
	bv.TenantID = TenantA
	if err := s.CreateSubmission(ctx, bad, bs, bv); err == nil {
		t.Fatal("submission of a foreign template accepted")
	}
	got, sg, err := s.GetSubmission(ctx, TenantA, sub.ID)
	must(t, err)
	if got.Mode != store.ModeSequential || len(sg) != 2 || sg[0].UserID != "maria" || got.Reminder.Max != 2 || len(got.Fields) != 1 {
		t.Fatalf("submission = %+v %+v", got, sg)
	}
	if _, _, err := s.GetSubmission(ctx, TenantB, sub.ID); !errors.Is(err, repo.ErrNotFound) {
		t.Fatalf("cross-tenant: %v", err)
	}
	if _, err := s.GetSigner(ctx, TenantB, sg[0].ID); !errors.Is(err, repo.ErrNotFound) {
		t.Fatalf("cross-tenant signer: %v", err)
	}
	sg[0].Status, sg[0].Values = store.SignerSigned, map[string]string{"f1": "5000"}
	now := time.Now().UTC().Truncate(time.Microsecond)
	sg[0].SignedAt, sg[0].CertificateID, sg[0].Method = &now, ptr(store.NewID()), store.MethodLocal
	must(t, s.UpdateSigner(ctx, sg[0]))
	one, err := s.GetSigner(ctx, TenantA, sg[0].ID)
	must(t, err)
	if one.Values["f1"] != "5000" || one.Method != store.MethodLocal || one.SignedAt == nil {
		t.Fatalf("signer = %+v", one)
	}
	if err := s.UpdateSigner(ctx, store.Signer{ID: sg[0].ID, TenantID: TenantB}); !errors.Is(err, repo.ErrNotFound) {
		t.Fatalf("cross-tenant signer update: %v", err)
	}
	must(t, s.AddVersion(ctx, store.DocumentVersion{SubmissionID: sub.ID, Version: 1, TenantID: TenantA, ObjectKey: "k1", SHA256: "h1", Size: 5,
		SignerID: &sg[0].ID, CreatedAt: now}))
	if err := s.AddVersion(ctx, store.DocumentVersion{SubmissionID: sub.ID, Version: 1, TenantID: TenantA, ObjectKey: "k", SHA256: "h"}); !errors.Is(err, repo.ErrConflict) {
		t.Fatalf("duplicate version: %v", err)
	}
	vs, err := s.ListVersions(ctx, TenantA, sub.ID)
	must(t, err)
	if len(vs) != 2 || vs[1].Version != 1 || *vs[1].SignerID != sg[0].ID {
		t.Fatalf("versions = %+v", vs)
	}
	if _, err := s.GetVersion(ctx, TenantB, sub.ID, 1); !errors.Is(err, repo.ErrNotFound) {
		t.Fatalf("cross-tenant version: %v", err)
	}
	// Inbox: Ivan (pending) sees nothing, Maria sees her signed slot.
	if items, n, _ := s.Inbox(ctx, TenantA, "ivan", false, 1, 10); n != 0 || len(items) != 0 {
		t.Fatalf("ivan to-sign = %d", n)
	}
	sg[1].Status = store.SignerInvited
	must(t, s.UpdateSigner(ctx, sg[1]))
	if items, n, _ := s.Inbox(ctx, TenantA, "ivan", false, 1, 10); n != 1 || items[0].Submission.ID != sub.ID {
		t.Fatalf("ivan to-sign = %d", n)
	}
	if items, n, _ := s.Inbox(ctx, TenantA, "maria", true, 1, 10); n != 1 || items[0].Signer.Status != store.SignerSigned {
		t.Fatalf("maria signed = %d", n)
	}
	if _, n, _ := s.Inbox(ctx, TenantB, "ivan", false, 1, 10); n != 0 {
		t.Fatal("cross-tenant inbox")
	}
	list, total, err := s.ListSubmissions(ctx, TenantA, repo.SubmissionFilter{CreatedBy: "sender", Status: store.SubmissionInProgress, Query: "contr"})
	must(t, err)
	if total != 1 || list[0].ID != sub.ID {
		t.Fatalf("list = %d", total)
	}
	if _, total, _ := s.ListSubmissions(ctx, TenantA, repo.SubmissionFilter{TemplateID: store.NewID()}); total != 0 {
		t.Fatal("template filter")
	}
	sub.Status = store.SubmissionCancelled
	must(t, s.UpdateSubmission(ctx, sub))
	if items, n, _ := s.Inbox(ctx, TenantA, "ivan", false, 1, 10); n != 0 || len(items) != 0 {
		t.Fatal("cancelled submission in inbox")
	}
	if err := s.UpdateSubmission(ctx, store.Submission{ID: sub.ID, TenantID: TenantB, Name: "x", Mode: "parallel", Status: "draft"}); !errors.Is(err, repo.ErrNotFound) {
		t.Fatalf("cross-tenant submission update: %v", err)
	}
	must(t, s.AddEvent(ctx, store.Event{ID: store.NewID(), TenantID: TenantA, SubmissionID: &sub.ID, Type: "submission.created", Meta: map[string]any{}, At: now}))
	must(t, s.EnqueueJob(ctx, store.Job{ID: store.NewID(), TenantID: TenantA, Kind: store.JobAuditTrail, SubmissionID: sub.ID, NextAttemptAt: now, CreatedAt: now}))
	if err := s.DeleteSubmission(ctx, TenantB, sub.ID); !errors.Is(err, repo.ErrNotFound) {
		t.Fatalf("cross-tenant delete: %v", err)
	}
	must(t, s.DeleteSubmission(ctx, TenantA, sub.ID))
	if _, _, err := s.GetSubmission(ctx, TenantA, sub.ID); !errors.Is(err, repo.ErrNotFound) {
		t.Fatal("deleted submission still there")
	}
	if vs, _ := s.ListVersions(ctx, TenantA, sub.ID); len(vs) != 0 {
		t.Fatal("versions survive delete")
	}
	if ev, _ := s.ListEvents(ctx, TenantA, sub.ID); len(ev) != 0 {
		t.Fatal("events survive delete")
	}
}

func tx(t *testing.T, s repo.Store) {
	tpl := Template(TenantA, "Contract")
	must(t, s.CreateTemplate(ctx, tpl))
	sub, signers, v0 := Submission(tpl)
	must(t, s.CreateSubmission(ctx, sub, signers, v0))
	signer := Certificate(TenantA, store.KindSigner, nil, ptr("maria"))
	ca := Certificate(TenantA, store.KindCA, nil, nil)
	must(t, s.CreateCertificate(ctx, ca))
	signer.IssuerID = &ca.ID
	must(t, s.CreateCertificate(ctx, signer))
	boom := errors.New("boom")
	err := s.Tx(ctx, TenantA, func(r repo.Store) error {
		got, _, err := r.LockSubmission(ctx, TenantA, sub.ID)
		if err != nil {
			return err
		}
		got.CurrentVersion = 1
		if err := r.UpdateSubmission(ctx, got); err != nil {
			return err
		}
		if _, _, err := r.RecordPINFailure(ctx, TenantA, signer.ID, 5, time.Now().Add(time.Minute)); err != nil {
			return err
		}
		return boom
	})
	if !errors.Is(err, boom) {
		t.Fatalf("tx error = %v", err)
	}
	got, _, _ := s.GetSubmission(ctx, TenantA, sub.ID)
	if got.CurrentVersion != 0 {
		t.Fatal("rolled-back update persisted")
	}
	c, _ := s.GetCertificate(ctx, TenantA, signer.ID)
	if c.FailedPINCount != 1 {
		t.Fatalf("PIN failure must survive the rollback, count = %d", c.FailedPINCount)
	}
	must(t, s.Tx(ctx, TenantA, func(r repo.Store) error {
		got, _, err := r.LockSubmission(ctx, TenantA, sub.ID)
		if err != nil {
			return err
		}
		got.CurrentVersion = 2
		return r.UpdateSubmission(ctx, got)
	}))
	got, _, _ = s.GetSubmission(ctx, TenantA, sub.ID)
	if got.CurrentVersion != 2 {
		t.Fatal("committed update lost")
	}
	if err := s.Tx(ctx, TenantB, func(r repo.Store) error {
		_, _, err := r.LockSubmission(ctx, TenantB, sub.ID)
		return err
	}); !errors.Is(err, repo.ErrNotFound) {
		t.Fatalf("cross-tenant lock: %v", err)
	}
}

func certificates(t *testing.T, s repo.Store) {
	ca := Certificate(TenantA, store.KindCA, nil, nil)
	must(t, s.CreateCertificate(ctx, ca))
	if err := s.CreateCertificate(ctx, store.Certificate{ID: store.NewID(), TenantID: TenantA, Kind: store.KindCA, Serial: ca.Serial,
		SubjectCN: "x", NotBefore: ca.NotBefore, NotAfter: ca.NotAfter, CertDER: []byte{1}, KeyProtection: store.KeySealed,
		KeyBlob: []byte{1}, Status: store.CertActive}); !errors.Is(err, repo.ErrConflict) {
		t.Fatalf("duplicate serial: %v", err)
	}
	cur, err := s.CurrentCA(ctx, TenantA)
	must(t, err)
	if cur.ID != ca.ID {
		t.Fatal("current CA")
	}
	if _, err := s.CurrentCA(ctx, TenantB); !errors.Is(err, repo.ErrNotFound) {
		t.Fatalf("tenant B CA: %v", err)
	}
	sys := Certificate(TenantA, store.KindSystem, &ca.ID, nil)
	must(t, s.CreateCertificate(ctx, sys))
	if got, err := s.SystemCertificate(ctx, TenantA, ca.ID); err != nil || got.ID != sys.ID {
		t.Fatalf("system cert: %v", err)
	}
	if _, err := s.SystemCertificate(ctx, TenantA, store.NewID()); !errors.Is(err, repo.ErrNotFound) {
		t.Fatalf("missing system cert: %v", err)
	}
	maria := Certificate(TenantA, store.KindSigner, &ca.ID, ptr("maria"))
	must(t, s.CreateCertificate(ctx, maria))
	second := Certificate(TenantA, store.KindSigner, &ca.ID, ptr("maria"))
	if err := s.CreateCertificate(ctx, second); !errors.Is(err, repo.ErrConflict) {
		t.Fatalf("second active signer certificate: %v", err)
	}
	if got, err := s.ActiveSignerCertificate(ctx, TenantA, "maria"); err != nil || got.ID != maria.ID {
		t.Fatalf("active signer: %v", err)
	}
	if _, err := s.ActiveSignerCertificate(ctx, TenantB, "maria"); !errors.Is(err, repo.ErrNotFound) {
		t.Fatalf("cross-tenant signer cert: %v", err)
	}
	// PIN failures: 2 failures then lock at the 3rd (lockAfter 3), counter reset.
	until := time.Now().Add(15 * time.Minute).UTC().Truncate(time.Microsecond)
	for i := 1; i <= 2; i++ {
		n, locked, err := s.RecordPINFailure(ctx, TenantA, maria.ID, 3, until)
		must(t, err)
		if n != i || locked {
			t.Fatalf("failure %d = %d %v", i, n, locked)
		}
	}
	n, locked, err := s.RecordPINFailure(ctx, TenantA, maria.ID, 3, until)
	must(t, err)
	if n != 3 || !locked {
		t.Fatalf("lock = %d %v", n, locked)
	}
	c, _ := s.GetCertificate(ctx, TenantA, maria.ID)
	if c.FailedPINCount != 0 || c.LockedUntil == nil || !c.LockedUntil.Equal(until) {
		t.Fatalf("locked cert = %+v", c)
	}
	must(t, s.ResetPINFailures(ctx, TenantA, maria.ID))
	if c, _ := s.GetCertificate(ctx, TenantA, maria.ID); c.LockedUntil != nil || c.FailedPINCount != 0 {
		t.Fatal("reset")
	}
	if _, _, err := s.RecordPINFailure(ctx, TenantB, maria.ID, 3, until); !errors.Is(err, repo.ErrNotFound) {
		t.Fatalf("cross-tenant failure: %v", err)
	}
	if err := s.ResetPINFailures(ctx, TenantB, maria.ID); !errors.Is(err, repo.ErrNotFound) {
		t.Fatalf("cross-tenant reset: %v", err)
	}
	// Revoking frees the owner for a new active certificate.
	maria.Status = store.CertRevoked
	now := time.Now().UTC().Truncate(time.Microsecond)
	maria.RevokedAt, maria.RevocationReason = &now, "superseded"
	must(t, s.UpdateCertificate(ctx, maria))
	must(t, s.CreateCertificate(ctx, second))
	if err := s.UpdateCertificate(ctx, store.Certificate{ID: maria.ID, TenantID: TenantB}); !errors.Is(err, repo.ErrNotFound) {
		t.Fatalf("cross-tenant update: %v", err)
	}
	issued, err := s.IssuedBy(ctx, TenantA, ca.ID)
	must(t, err)
	if len(issued) != 3 {
		t.Fatalf("issued = %d", len(issued))
	}
	list, total, err := s.ListCertificates(ctx, TenantA, repo.CertificateFilter{Kind: store.KindSigner, Status: store.CertRevoked})
	must(t, err)
	if total != 1 || list[0].ID != maria.ID {
		t.Fatalf("revoked list = %d", total)
	}
	if _, total, _ := s.ListCertificates(ctx, TenantA, repo.CertificateFilter{OwnerID: "maria"}); total != 2 {
		t.Fatalf("owner filter = %d", total)
	}
	if _, total, _ := s.ListCertificates(ctx, TenantA, repo.CertificateFilter{Query: "cn ca"}); total != 1 {
		t.Fatalf("query filter = %d", total)
	}
	// CA renewal: the superseded CA is no longer current but still listed.
	ca2 := Certificate(TenantA, store.KindCA, nil, nil)
	ca2.NotBefore = ca.NotBefore.Add(time.Minute)
	must(t, s.CreateCertificate(ctx, ca2))
	ca.SupersededBy = &ca2.ID
	must(t, s.UpdateCertificate(ctx, ca))
	if cur, _ := s.CurrentCA(ctx, TenantA); cur.ID != ca2.ID {
		t.Fatal("renewed CA not current")
	}
	if cas, _ := s.ListCAs(ctx, TenantA); len(cas) != 2 || cas[0].ID != ca.ID {
		t.Fatalf("CAs = %+v", cas)
	}
	// Documents signed with a certificate.
	tpl := Template(TenantA, "Contract")
	must(t, s.CreateTemplate(ctx, tpl))
	sub, signers, v0 := Submission(tpl)
	signers[0].CertificateID, signers[0].SignedAt, signers[0].Status = &second.ID, &now, store.SignerSigned
	must(t, s.CreateSubmission(ctx, sub, signers, v0))
	docs, err := s.SignedWithCertificate(ctx, TenantA, second.ID, 10)
	must(t, err)
	if len(docs) != 1 || docs[0].SubmissionID != sub.ID || docs[0].SubmissionName != "Contract" {
		t.Fatalf("signed docs = %+v", docs)
	}
}

func qesAndEvents(t *testing.T, s repo.Store) {
	tpl := Template(TenantA, "Contract")
	must(t, s.CreateTemplate(ctx, tpl))
	sub, signers, v0 := Submission(tpl)
	must(t, s.CreateSubmission(ctx, sub, signers, v0))
	now := time.Now().UTC().Truncate(time.Microsecond)
	q := store.QESPreparation{ID: store.NewID(), TenantID: TenantA, SignerID: signers[0].ID, ChainDER: [][]byte{{1, 2}, {3}},
		SignedAttrs: []byte{4}, Digest: []byte{5}, PreparedKey: "k", BasedOnVersion: 0, Values: map[string]string{"f1": "1"},
		ExpiresAt: now.Add(10 * time.Minute), CreatedAt: now}
	must(t, s.CreateQES(ctx, q))
	got, err := s.GetQES(ctx, TenantA, q.ID)
	must(t, err)
	if len(got.ChainDER) != 2 || got.Values["f1"] != "1" || got.UsedAt != nil {
		t.Fatalf("qes = %+v", got)
	}
	if _, err := s.GetQES(ctx, TenantB, q.ID); !errors.Is(err, repo.ErrNotFound) {
		t.Fatalf("cross-tenant qes: %v", err)
	}
	must(t, s.MarkQESUsed(ctx, TenantA, q.ID, now))
	if err := s.MarkQESUsed(ctx, TenantA, q.ID, now); !errors.Is(err, repo.ErrConflict) {
		t.Fatalf("second use: %v", err)
	}
	if err := s.MarkQESUsed(ctx, TenantB, q.ID, now); !errors.Is(err, repo.ErrNotFound) {
		t.Fatalf("cross-tenant use: %v", err)
	}
	for i, typ := range []string{"submission.created", "submission.sent"} {
		must(t, s.AddEvent(ctx, store.Event{ID: store.NewID(), TenantID: TenantA, SubmissionID: &sub.ID, ActorUserID: ptr("sender"),
			Type: typ, IP: "10.0.0.1", Meta: map[string]any{"n": float64(i)}, At: now.Add(time.Duration(i) * time.Second)}))
	}
	ev, err := s.ListEvents(ctx, TenantA, sub.ID)
	must(t, err)
	if len(ev) != 2 || ev[0].Type != "submission.created" || ev[1].Meta["n"] != float64(1) {
		t.Fatalf("events = %+v", ev)
	}
	if ev, _ := s.ListEvents(ctx, TenantB, sub.ID); len(ev) != 0 {
		t.Fatal("cross-tenant events")
	}
	must(t, s.AppendAudit(ctx, store.AuditRow{ID: store.NewID(), TenantID: TenantA, At: now, ActorKind: "user", ActorID: "u",
		Action: "template.create", SubjectKind: "template", SubjectID: "t", Outcome: "ok", Detail: map[string]any{"k": "v"}}))
}

func jobs(t *testing.T, s repo.Store) {
	tpl := Template(TenantA, "Contract")
	must(t, s.CreateTemplate(ctx, tpl))
	sub, signers, v0 := Submission(tpl)
	must(t, s.CreateSubmission(ctx, sub, signers, v0))
	now := time.Now().UTC().Truncate(time.Microsecond)
	j := store.Job{ID: store.NewID(), TenantID: TenantA, Kind: store.JobAuditTrail, SubmissionID: sub.ID, NextAttemptAt: now, CreatedAt: now}
	must(t, s.EnqueueJob(ctx, j))
	must(t, s.EnqueueJob(ctx, store.Job{ID: store.NewID(), TenantID: TenantA, Kind: store.JobAuditTrail, SubmissionID: sub.ID,
		NextAttemptAt: now, CreatedAt: now})) // idempotent per submission and kind
	claimed, err := s.ClaimJobsSystem(ctx, now, 10, time.Minute)
	must(t, err)
	if len(claimed) != 1 || claimed[0].ID != j.ID || claimed[0].Attempts != 1 {
		t.Fatalf("claimed = %+v", claimed)
	}
	if again, _ := s.ClaimJobsSystem(ctx, now, 10, time.Minute); len(again) != 0 {
		t.Fatal("leased job claimed twice")
	}
	must(t, s.FailJobSystem(ctx, j.ID, "render failed", now))
	if again, _ := s.ClaimJobsSystem(ctx, now, 10, time.Minute); len(again) != 1 || again[0].Attempts != 2 || again[0].LastError != "render failed" {
		t.Fatalf("retry = %+v", again)
	}
	must(t, s.CompleteJobSystem(ctx, j.ID, now))
	if again, _ := s.ClaimJobsSystem(ctx, now.Add(time.Hour), 10, time.Minute); len(again) != 0 {
		t.Fatal("completed job claimed")
	}
	if err := s.CompleteJobSystem(ctx, store.NewID(), now); !errors.Is(err, repo.ErrNotFound) {
		t.Fatalf("complete missing: %v", err)
	}
	if err := s.FailJobSystem(ctx, store.NewID(), "x", now); !errors.Is(err, repo.ErrNotFound) {
		t.Fatalf("fail missing: %v", err)
	}
}

func scheduled(t *testing.T, s repo.Store) {
	now := time.Now().UTC().Truncate(time.Microsecond)
	tpl := Template(TenantA, "Contract")
	must(t, s.CreateTemplate(ctx, tpl))
	overdue, os, ov := Submission(tpl)
	overdue.ExpiresAt = ptr(now.Add(-time.Minute))
	os[0].NextReminderAt = ptr(now.Add(-time.Minute))
	must(t, s.CreateSubmission(ctx, overdue, os, ov))
	tplB := Template(TenantB, "Other")
	must(t, s.CreateTemplate(ctx, tplB))
	live, ls, lv := Submission(tplB)
	live.ExpiresAt = ptr(now.Add(time.Hour))
	ls[0].NextReminderAt = ptr(now.Add(-time.Minute))
	must(t, s.CreateSubmission(ctx, live, ls, lv))

	// Reminders first (the overdue one is still in progress), across tenants, once per interval.
	due, err := s.ClaimRemindersSystem(ctx, now, 10)
	must(t, err)
	if len(due) != 2 || due[0].Number != 1 {
		t.Fatalf("reminders = %+v", due)
	}
	if again, _ := s.ClaimRemindersSystem(ctx, now, 10); len(again) != 0 {
		t.Fatal("reminder claimed twice in one interval")
	}
	later, _ := s.ClaimRemindersSystem(ctx, now.Add(25*time.Hour), 10)
	if len(later) != 2 || later[0].Number != 2 {
		t.Fatalf("second reminder = %+v", later)
	}
	if last, _ := s.ClaimRemindersSystem(ctx, now.Add(50*time.Hour), 10); len(last) != 0 {
		t.Fatal("reminders beyond the maximum")
	}
	expired, err := s.ExpireDueSystem(ctx, now, 10)
	must(t, err)
	if len(expired) != 1 || expired[0].ID != overdue.ID || expired[0].Status != store.SubmissionExpired {
		t.Fatalf("expired = %+v", expired)
	}
	if again, _ := s.ExpireDueSystem(ctx, now, 10); len(again) != 0 {
		t.Fatal("expired twice")
	}
	// QES sweep: expired or used preparations.
	q := store.QESPreparation{ID: store.NewID(), TenantID: TenantA, SignerID: os[0].ID, ChainDER: [][]byte{{1}}, SignedAttrs: []byte{1},
		Digest: []byte{1}, PreparedKey: "k", ExpiresAt: now.Add(-time.Second), CreatedAt: now, Values: map[string]string{}}
	must(t, s.CreateQES(ctx, q))
	old, err := s.ExpiredQESSystem(ctx, now, 10)
	must(t, err)
	if len(old) != 1 || old[0].ID != q.ID {
		t.Fatalf("expired qes = %+v", old)
	}
	must(t, s.DeleteQESSystem(ctx, q.ID))
	if old, _ := s.ExpiredQESSystem(ctx, now, 10); len(old) != 0 {
		t.Fatal("qes not deleted")
	}
	// CRLs: a CA without a CRL is due.
	ca := Certificate(TenantA, store.KindCA, nil, nil)
	must(t, s.CreateCertificate(ctx, ca))
	crls, err := s.DueCRLsSystem(ctx, now, 10)
	must(t, err)
	if len(crls) != 1 || crls[0].ID != ca.ID {
		t.Fatalf("due CRLs = %+v", crls)
	}
	ca.CRLNextUpdate = ptr(now.Add(time.Hour))
	must(t, s.UpdateCertificate(ctx, ca))
	if crls, _ := s.DueCRLsSystem(ctx, now, 10); len(crls) != 0 {
		t.Fatal("fresh CRL due")
	}
	tenants, err := s.TenantsSystem(ctx)
	must(t, err)
	if len(tenants) != 2 {
		t.Fatalf("tenants = %v", tenants)
	}
}
