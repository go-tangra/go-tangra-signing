package templates

import (
	"context"
	"errors"
	"io"
	"sync"
	"testing"
	"time"

	"github.com/go-tangra/go-tangra-signing/v4/internal/apperr"
	"github.com/go-tangra/go-tangra-signing/v4/internal/audit"
	"github.com/go-tangra/go-tangra-signing/v4/internal/authz"
	"github.com/go-tangra/go-tangra-signing/v4/internal/blob"
	"github.com/go-tangra/go-tangra-signing/v4/internal/memstore"
	"github.com/go-tangra/go-tangra-signing/v4/internal/pdf/pdftest"
	"github.com/go-tangra/go-tangra-signing/v4/internal/repo"
	"github.com/go-tangra/go-tangra-signing/v4/internal/store"
)

const (
	tenant = "0190f7c2-6a3e-7c1a-9b2e-2f6f9d1b4c55"
	other  = "0190f7c2-6a3e-7c1a-9b2e-2f6f9d1b4c66"
)

var ctx = context.Background()

type recorder struct {
	mu     sync.Mutex
	events []audit.Event
}

func (r *recorder) Record(_ context.Context, e audit.Event) error {
	if err := audit.Validate(e); err != nil {
		return err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.events = append(r.events, e)
	return nil
}

func (r *recorder) has(t audit.EventType, outcome string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, e := range r.events {
		if e.EventType == t && e.Outcome == outcome {
			return true
		}
	}
	return false
}

type env struct {
	svc   *Service
	mem   *memstore.Mem
	blob  *blob.Fake
	audit *recorder
	admin authz.Subjects
}

func newEnv(t *testing.T) *env {
	t.Helper()
	e := &env{mem: memstore.New(), blob: blob.NewFake(), audit: &recorder{}, admin: authz.User(tenant, "admin", nil)}
	e.svc = New(Deps{Store: e.mem, Blob: e.blob, Audit: e.audit, Now: func() time.Time { return time.Now().UTC() },
		Limits: Limits{MaxPDFBytes: 5 << 20, MaxPages: 20, MaxFields: 500, MaxParties: 50, ParseTimeout: 10 * time.Second}})
	return e
}

func ptr[T any](v T) *T { return &v }

func (e *env) upload(t *testing.T, name string) store.Template {
	t.Helper()
	tpl, err := e.svc.Create(ctx, e.admin, CreateInput{Name: name, Description: "d", Tags: []string{"hr"}, FileName: "../c:\\Договор.pdf", PDF: pdftest.Contract(2)})
	if err != nil {
		t.Fatal(err)
	}
	return tpl
}

func TestCreateGetListAndPDF(t *testing.T) {
	e := newEnv(t)
	tpl := e.upload(t, "Contract")
	if tpl.Status != store.TemplateDraft || tpl.PDFPages != 2 || tpl.PDFSigned || tpl.PDFSHA256 == "" || tpl.FileName != "Договор.pdf" ||
		len(tpl.Parties) != 1 || tpl.Version != 1 {
		t.Fatalf("template = %+v", tpl)
	}
	if !e.blob.Has(tpl.PDFKey) || !blob.InTenant(tpl.PDFKey, tenant) {
		t.Fatal("pdf not stored under the tenant prefix")
	}
	if !e.audit.has(audit.TemplateCreate, audit.OutcomeOK) {
		t.Fatal("audit")
	}
	if _, err := e.svc.Get(ctx, authz.User(other, "x", nil), tpl.ID); !errors.Is(err, apperr.NotFound) {
		t.Fatalf("cross-tenant get: %v", err)
	}
	if _, _, err := e.svc.OpenPDF(ctx, authz.User(other, "x", nil), tpl.ID); !errors.Is(err, apperr.NotFound) {
		t.Fatalf("cross-tenant pdf: %v", err)
	}
	rc, _, err := e.svc.OpenPDF(ctx, e.admin, tpl.ID)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := io.ReadAll(rc)
	_ = rc.Close()
	if len(b) == 0 {
		t.Fatal("empty pdf")
	}
	list, total, err := e.svc.List(ctx, e.admin, repo.TemplateFilter{Tag: "hr", PageSize: 1000})
	if err != nil || total != 1 || list[0].ID != tpl.ID {
		t.Fatalf("list = %d %v", total, err)
	}
}

func TestCreateRefusals(t *testing.T) {
	e := newEnv(t)
	for name, in := range map[string]CreateInput{
		"no name":     {Name: " ", PDF: pdftest.Contract(1)},
		"slash":       {Name: "a/b", PDF: pdftest.Contract(1)},
		"description": {Name: "x", Description: string(make([]byte, 2001)), PDF: pdftest.Contract(1)},
		"tags":        {Name: "x", Tags: []string{"a,b"}, PDF: pdftest.Contract(1)},
		"not a pdf":   {Name: "x", PDF: []byte("hello")},
		"too big":     {Name: "x", PDF: make([]byte, 6<<20)},
		"folder":      {Name: "x", FolderID: ptr(store.NewID()), PDF: pdftest.Contract(1)},
		"many pages":  {Name: "x", PDF: pdftest.Contract(21)},
	} {
		_, err := e.svc.Create(ctx, e.admin, in)
		switch name {
		case "not a pdf":
			if !errors.Is(err, apperr.InvalidPDF) {
				t.Errorf("%s: %v", name, err)
			}
		case "too big", "many pages":
			if !errors.Is(err, apperr.PayloadTooLarge) {
				t.Errorf("%s: %v", name, err)
			}
		case "folder":
			if !errors.Is(err, apperr.NotFound) {
				t.Errorf("%s: %v", name, err)
			}
		default:
			if !errors.Is(err, apperr.Validation) {
				t.Errorf("%s: %v", name, err)
			}
		}
	}
	if !e.audit.has(audit.TemplateCreate, audit.OutcomeRefused) {
		t.Fatal("refusal not audited")
	}
	e.upload(t, "Dup")
	if _, err := e.svc.Create(ctx, e.admin, CreateInput{Name: "dup", PDF: pdftest.Contract(1)}); !errors.Is(err, apperr.NameTaken) {
		t.Fatalf("duplicate: %v", err)
	}
	// A failed row insert removes the uploaded object again.
	e.mem.Fail("CreateTemplate", errors.New("db down"))
	before, _ := e.blob.List(ctx, "tenants/", 100)
	if _, err := e.svc.Create(ctx, e.admin, CreateInput{Name: "x", PDF: pdftest.Contract(1)}); err == nil {
		t.Fatal("db failure hidden")
	}
	after, _ := e.blob.List(ctx, "tenants/", 100)
	if len(after) != len(before) {
		t.Fatal("orphan object left behind")
	}
}

func TestFieldsActivateAndVersion(t *testing.T) {
	e := newEnv(t)
	tpl := e.upload(t, "Contract")
	parties := []store.Party{{Key: "p1", Name: " Employee "}, {Key: "p2", Name: "Employer"}}
	fields := []store.Field{field("f1", " Salary ", "number", "p1"), field("f2", "Signature", "signature", "p1")}
	saved, err := e.svc.SaveFields(ctx, e.admin, tpl.ID, tpl.Version, parties, fields)
	if err != nil {
		t.Fatal(err)
	}
	if saved.Version != tpl.Version+1 || saved.Fields[0].Name != "Salary" || saved.Parties[0].Name != "Employee" {
		t.Fatalf("saved = %+v", saved)
	}
	if _, err := e.svc.SaveFields(ctx, e.admin, tpl.ID, tpl.Version, parties, fields); !errors.Is(err, apperr.VersionConflict) {
		t.Fatalf("stale version: %v", err)
	}
	// Employer has no field: cannot activate.
	if _, err := e.svc.Update(ctx, e.admin, tpl.ID, PatchInput{Status: ptr(store.TemplateActive)}); !errors.Is(err, apperr.InvalidField) {
		t.Fatalf("activate without employer field: %v", err)
	}
	fields = append(fields, field("f3", "Employer signature", "signature", "p2"))
	saved, err = e.svc.SaveFields(ctx, e.admin, tpl.ID, saved.Version, parties, fields)
	if err != nil {
		t.Fatal(err)
	}
	act, err := e.svc.Update(ctx, e.admin, tpl.ID, PatchInput{Status: ptr(store.TemplateActive), Name: ptr("Contract v2"),
		Description: ptr("desc"), Tags: &[]string{"legal"}, DefaultExpiryDays: ptr(ptr(30)),
		DefaultReminder: ptr(ptr(store.Reminder{IntervalDays: 3, Max: 5}))})
	if err != nil || act.Status != store.TemplateActive || act.Name != "Contract v2" || *act.DefaultExpiryDays != 30 || act.DefaultReminder.Max != 5 {
		t.Fatalf("activate = %+v %v", act, err)
	}
	// An active template cannot lose a party's last field.
	if _, err := e.svc.SaveFields(ctx, e.admin, tpl.ID, act.Version, parties, fields[:2]); !errors.Is(err, apperr.InvalidField) {
		t.Fatalf("active template emptied: %v", err)
	}
	for name, in := range map[string]PatchInput{
		"status":   {Status: ptr("published")},
		"expiry":   {DefaultExpiryDays: ptr(ptr(0))},
		"reminder": {DefaultReminder: ptr(ptr(store.Reminder{IntervalDays: 0, Max: 1}))},
		"name":     {Name: ptr("")},
		"desc":     {Description: ptr(string(make([]byte, 2001)))},
		"tags":     {Tags: &[]string{"a\nb"}},
	} {
		if _, err := e.svc.Update(ctx, e.admin, tpl.ID, in); !errors.Is(err, apperr.Validation) {
			t.Errorf("%s: %v", name, err)
		}
	}
	if _, err := e.svc.Update(ctx, e.admin, tpl.ID, PatchInput{FolderID: ptr(ptr(store.NewID()))}); !errors.Is(err, apperr.NotFound) {
		t.Fatalf("unknown folder: %v", err)
	}
	if _, err := e.svc.Update(ctx, authz.User(other, "x", nil), tpl.ID, PatchInput{}); !errors.Is(err, apperr.NotFound) {
		t.Fatalf("cross-tenant update: %v", err)
	}
	if _, err := e.svc.SaveFields(ctx, e.admin, tpl.ID, act.Version, []store.Party{}, fields); !errors.Is(err, apperr.InvalidField) {
		t.Fatalf("no parties: %v", err)
	}
	// Rule validator hook (package rules in US6).
	e.svc.d.Rules = func([]store.Field) error { return apperr.InvalidRule.WithField("Total") }
	if _, err := e.svc.SaveFields(ctx, e.admin, tpl.ID, act.Version, parties, fields); !errors.Is(err, apperr.InvalidRule) {
		t.Fatalf("rule validator: %v", err)
	}
	if !e.audit.has(audit.TemplateFields, audit.OutcomeOK) || !e.audit.has(audit.TemplateUpdate, audit.OutcomeOK) {
		t.Fatal("audit")
	}
}

func TestCloneDeleteAndDetect(t *testing.T) {
	e := newEnv(t)
	tpl := e.upload(t, "Contract")
	cl, err := e.svc.Clone(ctx, e.admin, tpl.ID, "Contract copy", nil)
	if err != nil || cl.ID == tpl.ID || cl.PDFKey == tpl.PDFKey || cl.PDFSHA256 != tpl.PDFSHA256 || cl.Status != store.TemplateDraft {
		t.Fatalf("clone = %+v %v", cl, err)
	}
	if !e.blob.Has(cl.PDFKey) {
		t.Fatal("clone pdf missing")
	}
	if _, err := e.svc.Clone(ctx, e.admin, tpl.ID, "contract", nil); !errors.Is(err, apperr.NameTaken) {
		t.Fatalf("clone name taken: %v", err)
	}
	if _, err := e.svc.Clone(ctx, authz.User(other, "x", nil), tpl.ID, "x", nil); !errors.Is(err, apperr.NotFound) {
		t.Fatalf("cross-tenant clone: %v", err)
	}
	fields, err := e.svc.DetectFields(ctx, e.admin, tpl.ID)
	if err != nil || len(fields) < 2 || fields[0].Party != "p1" {
		t.Fatalf("detect = %+v %v", fields, err)
	}
	// Referenced by a submission: refused.
	sub := store.Submission{ID: store.NewID(), TenantID: tenant, TemplateID: tpl.ID, Name: "s", PDFKey: tpl.PDFKey, PDFSHA256: "x",
		Mode: store.ModeParallel, Status: store.SubmissionCompleted}
	if err := e.mem.CreateSubmission(ctx, sub, nil, store.DocumentVersion{SubmissionID: sub.ID, TenantID: tenant, ObjectKey: tpl.PDFKey, SHA256: "x"}); err != nil {
		t.Fatal(err)
	}
	if err := e.svc.Delete(ctx, e.admin, tpl.ID); !errors.Is(err, apperr.TemplateInUse) {
		t.Fatalf("delete referenced: %v", err)
	}
	if err := e.svc.Delete(ctx, e.admin, cl.ID); err != nil {
		t.Fatal(err)
	}
	if e.blob.Has(cl.PDFKey) {
		t.Fatal("pdf of deleted template kept")
	}
	if err := e.svc.Delete(ctx, e.admin, cl.ID); !errors.Is(err, apperr.NotFound) {
		t.Fatalf("delete twice: %v", err)
	}
}

func TestFolders(t *testing.T) {
	e := newEnv(t)
	hr, err := e.svc.CreateFolder(ctx, e.admin, FolderInput{Name: ptr(" HR ")})
	if err != nil || hr.Path != "/HR" {
		t.Fatalf("root = %+v %v", hr, err)
	}
	contracts, err := e.svc.CreateFolder(ctx, e.admin, FolderInput{Name: ptr("Contracts"), ParentID: ptr(&hr.ID), SortOrder: ptr(2)})
	if err != nil || contracts.Path != "/HR/Contracts" || contracts.SortOrder != 2 {
		t.Fatalf("child = %+v %v", contracts, err)
	}
	if _, err := e.svc.CreateFolder(ctx, e.admin, FolderInput{Name: ptr("hr")}); !errors.Is(err, apperr.NameTaken) {
		t.Fatalf("duplicate: %v", err)
	}
	for name, in := range map[string]FolderInput{"no name": {}, "slash": {Name: ptr("a/b")}, "empty": {Name: ptr(" ")}} {
		if _, err := e.svc.CreateFolder(ctx, e.admin, in); !errors.Is(err, apperr.Validation) {
			t.Errorf("%s: %v", name, err)
		}
	}
	if _, err := e.svc.CreateFolder(ctx, e.admin, FolderInput{Name: ptr("x"), ParentID: ptr(ptr(store.NewID()))}); !errors.Is(err, apperr.NotFound) {
		t.Fatalf("unknown parent: %v", err)
	}
	// Rename the root: the child's path follows.
	if _, err := e.svc.UpdateFolder(ctx, e.admin, hr.ID, FolderInput{Name: ptr("People"), SortOrder: ptr(1)}); err != nil {
		t.Fatal(err)
	}
	all, _ := e.svc.ListFolders(ctx, e.admin)
	if len(all) != 2 || all[0].Path != "/People" || all[1].Path != "/People/Contracts" {
		t.Fatalf("paths = %+v", all)
	}
	// Moving a folder into itself or its descendant is refused.
	if _, err := e.svc.UpdateFolder(ctx, e.admin, hr.ID, FolderInput{ParentID: ptr(&contracts.ID)}); !errors.Is(err, apperr.Validation) {
		t.Fatalf("cycle: %v", err)
	}
	if _, err := e.svc.UpdateFolder(ctx, e.admin, hr.ID, FolderInput{ParentID: ptr(&hr.ID)}); !errors.Is(err, apperr.Validation) {
		t.Fatalf("self: %v", err)
	}
	// Move the child to the root.
	moved, err := e.svc.UpdateFolder(ctx, e.admin, contracts.ID, FolderInput{ParentID: ptr[*string](nil)})
	if err != nil || moved.Path != "/Contracts" {
		t.Fatalf("move = %+v %v", moved, err)
	}
	if _, err := e.svc.UpdateFolder(ctx, e.admin, contracts.ID, FolderInput{Name: ptr("people")}); !errors.Is(err, apperr.NameTaken) {
		t.Fatalf("rename onto sibling: %v", err)
	}
	if _, err := e.svc.UpdateFolder(ctx, authz.User(other, "x", nil), hr.ID, FolderInput{Name: ptr("x")}); !errors.Is(err, apperr.NotFound) {
		t.Fatalf("cross-tenant update: %v", err)
	}
	// Depth limit.
	parent := &hr.ID
	var err2 error
	for i := 0; i < MaxFolderDepth+1 && err2 == nil; i++ {
		var f store.Folder
		f, err2 = e.svc.CreateFolder(ctx, e.admin, FolderInput{Name: ptr("L" + string(rune('a'+i))), ParentID: ptr(parent)})
		parent = &f.ID
	}
	if !errors.Is(err2, apperr.Validation) {
		t.Fatalf("depth limit: %v", err2)
	}
	// A folder holding a template cannot be deleted.
	tpl, err := e.svc.Create(ctx, e.admin, CreateInput{Name: "In folder", FolderID: &contracts.ID, PDF: pdftest.Contract(1)})
	if err != nil {
		t.Fatal(err)
	}
	if err := e.svc.DeleteFolder(ctx, e.admin, contracts.ID); !errors.Is(err, apperr.FolderNotEmpty) {
		t.Fatalf("delete non-empty: %v", err)
	}
	if _, err := e.svc.Update(ctx, e.admin, tpl.ID, PatchInput{FolderID: ptr[*string](nil)}); err != nil {
		t.Fatal(err)
	}
	if err := e.svc.DeleteFolder(ctx, e.admin, contracts.ID); err != nil {
		t.Fatal(err)
	}
	if err := e.svc.DeleteFolder(ctx, e.admin, contracts.ID); !errors.Is(err, apperr.NotFound) {
		t.Fatalf("delete twice: %v", err)
	}
	if !e.audit.has(audit.FolderDelete, audit.OutcomeRefused) || !e.audit.has(audit.FolderUpdate, audit.OutcomeOK) {
		t.Fatal("audit")
	}
}

func TestSaveFieldsValidatesRules(t *testing.T) {
	e := newEnv(t)
	tpl := e.upload(t, "Rules")
	parties := []store.Party{{Key: "p1", Name: "Employee"}}
	f := func(id, name, typ, formula string) store.Field {
		return store.Field{ID: id, Name: name, Type: typ, Party: "p1", Page: 1, X: 0.1, Y: 0.1, W: 0.2, H: 0.05, Formula: formula}
	}
	cyclic := []store.Field{f("x", "Gross", "number", "{Net} + 1"), f("y", "Net", "number", "{Gross} - 1")}
	_, err := e.svc.SaveFields(ctx, e.admin, tpl.ID, tpl.Version, parties, cyclic)
	var ae *apperr.Error
	if !errors.As(err, &ae) || ae.Reason != "invalid_rule" || ae.Field != "Gross" || ae.Detail["message"] == "" {
		t.Fatalf("cycle: %v %+v", err, ae)
	}
	if _, err := e.svc.SaveFields(ctx, e.admin, tpl.ID, tpl.Version, parties, []store.Field{f("t", "Total", "number", "{Missing} * 2")}); !errors.Is(err, apperr.InvalidRule) {
		t.Fatalf("unknown ref: %v", err)
	}
	ok := []store.Field{f("s", "Salary", "number", ""), f("b", "Bonus", "number", "round({Salary} * 0.1, 2)")}
	if _, err := e.svc.SaveFields(ctx, e.admin, tpl.ID, tpl.Version, parties, ok); err != nil {
		t.Fatalf("valid formula refused: %v", err)
	}
	if !e.audit.has(audit.TemplateFields, audit.OutcomeRefused) {
		t.Fatal("refusal audited")
	}
}
