package backup

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
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
	"github.com/go-tangra/go-tangra-signing/v4/internal/pincrypto"
	"github.com/go-tangra/go-tangra-signing/v4/internal/pki"
	"github.com/go-tangra/go-tangra-signing/v4/internal/repo"
	"github.com/go-tangra/go-tangra-signing/v4/internal/sealed"
	"github.com/go-tangra/go-tangra-signing/v4/internal/store"
)

const (
	tenantA = "0190f7c2-6a3e-7c1a-9b2e-2f6f9d1b4c55"
	tenantB = "0190f7c2-6a3e-7c1a-9b2e-2f6f9d1b4c66"
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

type stack struct {
	svc   *Service
	mem   *memstore.Mem
	blob  *blob.Fake
	pki   *pki.PKI
	audit *recorder
	kek   byte
}

func newStack(t *testing.T, kek byte) *stack {
	t.Helper()
	st := &stack{mem: memstore.New(), blob: blob.NewFake(), audit: &recorder{}, kek: kek}
	sealer, err := sealed.NewEnvelope(bytes.Repeat([]byte{kek}, 32))
	if err != nil {
		t.Fatal(err)
	}
	st.pki = pki.New(pki.Deps{Store: st.mem, Sealer: sealer,
		Config: pki.Config{CAValidityYears: 10, CARenewBeforeYears: 2, CertValidityYears: 2, CRLValidityDays: 7, PINIterations: pincrypto.MinIterations}})
	st.svc = New(Deps{Store: st.mem, Blob: st.blob, Audit: st.audit, KeyCheck: sealer.KeyCheck()})
	return st
}

func admin(tenant string) authz.Subjects { return authz.User(tenant, "admin", nil) }

// seed fills tenant A: a folder, a template, a completed submission with two
// versions, an upload and an audit trail, and certificates of every kind.
func (st *stack) seed(t *testing.T) (store.Template, store.Submission) {
	t.Helper()
	now := time.Now().UTC().Truncate(time.Second)
	put := func(key string, data []byte) string {
		sum, err := st.blob.Put(ctx, key, bytes.NewReader(data), int64(len(data)), "application/pdf")
		if err != nil {
			t.Fatal(err)
		}
		return sum
	}
	folder := store.Folder{ID: store.NewID(), TenantID: tenantA, Name: "HR", Path: "HR", CreatedAt: now}
	if err := st.mem.CreateFolder(ctx, folder); err != nil {
		t.Fatal(err)
	}
	child := store.Folder{ID: store.NewID(), TenantID: tenantA, ParentID: &folder.ID, Name: "Contracts", Path: "HR/Contracts", CreatedAt: now}
	if err := st.mem.CreateFolder(ctx, child); err != nil {
		t.Fatal(err)
	}
	pdf := pdftest.Contract(1)
	tid := store.NewID()
	tkey := blob.TemplatePDF(tenantA, tid, store.NewID())
	tpl := store.Template{ID: tid, TenantID: tenantA, FolderID: &child.ID, Name: "Employment", Status: store.TemplateActive, PDFKey: tkey,
		PDFSHA256: put(tkey, pdf), PDFSize: int64(len(pdf)), PDFPages: 1, Parties: []store.Party{{Key: "a", Name: "A"}},
		Fields: []store.Field{{ID: "s", Name: "S", Type: "signature", Party: "a", Page: 1, X: 0.1, Y: 0.1, W: 0.2, H: 0.1}}, Version: 1, CreatedAt: now, UpdatedAt: now}
	if err := st.mem.CreateTemplate(ctx, tpl); err != nil {
		t.Fatal(err)
	}
	sid := store.NewID()
	v0 := blob.DocumentVersion(tenantA, sid, 0)
	v1 := blob.DocumentVersion(tenantA, sid, 1)
	trail := blob.AuditTrail(tenantA, sid)
	sgID := store.NewID()
	upload := blob.SignerUpload(tenantA, sid, sgID, "photo", "png")
	one := 1
	sub := store.Submission{ID: sid, TenantID: tenantA, TemplateID: tid, Name: "Договор", PDFKey: v0, Fields: tpl.Fields, Parties: tpl.Parties,
		Mode: store.ModeParallel, Status: store.SubmissionCompleted, CurrentVersion: 1, FinalVersion: &one, AuditTrailKey: trail,
		CreatedAt: now, CreatedBy: "admin", UpdatedAt: now}
	signed := now
	sg := store.Signer{ID: sgID, TenantID: tenantA, SubmissionID: sid, UserID: "u1", Name: "Мария", Email: "m@example.org", Party: "a",
		Status: store.SignerSigned, Values: map[string]string{"name": "Мария", "photo": upload}, SignedAt: &signed}
	if err := st.mem.CreateSubmission(ctx, sub, []store.Signer{sg}, store.DocumentVersion{SubmissionID: sid, Version: 0, TenantID: tenantA,
		ObjectKey: v0, SHA256: put(v0, pdf), Size: int64(len(pdf)), CreatedAt: now}); err != nil {
		t.Fatal(err)
	}
	if err := st.mem.AddVersion(ctx, store.DocumentVersion{SubmissionID: sid, Version: 1, TenantID: tenantA, ObjectKey: v1,
		SHA256: put(v1, pdf), Size: int64(len(pdf)), SignerID: &sgID, CreatedAt: now}); err != nil {
		t.Fatal(err)
	}
	put(trail, []byte("%PDF-trail"))
	put(upload, []byte("png"))
	if err := st.mem.AddEvent(ctx, store.Event{ID: store.NewID(), TenantID: tenantA, SubmissionID: &sid, Type: "signer.signed", At: now}); err != nil {
		t.Fatal(err)
	}
	if _, err := st.pki.IssueSigner(ctx, pki.SignerInput{Tenant: tenantA, UserID: "u1", Name: "Мария", Email: "m@example.org", PIN: "123456"}); err != nil {
		t.Fatal(err)
	}
	if _, err := st.pki.IssueAdmin(ctx, tenantA, "HR", "", 2, "admin"); err != nil {
		t.Fatal(err)
	}
	return tpl, sub
}

func (st *stack) export(t *testing.T, tenant string) []byte {
	t.Helper()
	tn, recs, _, err := st.svc.Collect(ctx, admin(tenantA), tenant)
	if err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	if err := st.svc.Write(ctx, admin(tenantA), tn, recs, &buf); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func TestRoundTripSameKEK(t *testing.T) {
	src := newStack(t, 7)
	tpl, sub := src.seed(t)
	archive := src.export(t, "")
	if !src.audit.has(audit.BackupExport, audit.OutcomeOK) {
		t.Fatal("export audited")
	}

	dst := newStack(t, 7) // another stack, same module key, another tenant
	res, err := dst.svc.Import(ctx, admin(tenantB), "", ModeSkip, bytes.NewReader(archive))
	if err != nil {
		t.Fatal(err)
	}
	if res.Created.Folders != 2 || res.Created.Templates != 1 || res.Created.Submissions != 1 || res.Created.Certificates < 3 ||
		res.Created.Objects != 5 || res.NeedsReissue != 0 || len(res.Errors) != 0 {
		t.Fatalf("import %+v", res)
	}
	got, err := dst.mem.GetTemplate(ctx, tenantB, tpl.ID)
	if err != nil || !blob.InTenant(got.PDFKey, tenantB) || !dst.blob.Has(got.PDFKey) || got.TenantID != tenantB {
		t.Fatalf("template %+v %v", got, err)
	}
	gs, signers, err := dst.mem.GetSubmission(ctx, tenantB, sub.ID)
	if err != nil || gs.AuditTrailKey != blob.AuditTrail(tenantB, sub.ID) || !dst.blob.Has(gs.AuditTrailKey) {
		t.Fatalf("submission %+v %v", gs, err)
	}
	if up := signers[0].Values["photo"]; !blob.InTenant(up, tenantB) || !dst.blob.Has(up) || signers[0].Values["name"] != "Мария" {
		t.Fatalf("signer values %v", signers[0].Values)
	}
	if vs, _ := dst.mem.ListVersions(ctx, tenantB, sub.ID); len(vs) != 2 {
		t.Fatalf("versions %d", len(vs))
	}
	if evs, _ := dst.mem.ListEvents(ctx, tenantB, sub.ID); len(evs) != 1 {
		t.Fatalf("events %d", len(evs))
	}
	certs, _, _ := dst.mem.ListCertificates(ctx, tenantB, repoFilter())
	for _, c := range certs {
		if c.KeyProtection == store.KeySealed && (len(c.KeyBlob) == 0 || c.Status == store.CertNeedsReissue) {
			t.Fatalf("sealed key dropped with the same KEK: %+v", c.Kind)
		}
	}
	// The imported CA still signs: its sealed key opens with the target KEK.
	if _, err := dst.pki.IssueAdmin(ctx, tenantB, "Again", "", 1, "admin"); err != nil {
		t.Fatalf("imported CA unusable: %v", err)
	}

	// Skip leaves everything; overwrite replaces.
	res, err = dst.svc.Import(ctx, admin(tenantB), "", ModeSkip, bytes.NewReader(archive))
	if err != nil || res.Skipped.Submissions != 1 || res.Skipped.Templates != 1 || res.Created.Objects != 0 {
		t.Fatalf("skip %+v %v", res, err)
	}
	res, err = dst.svc.Import(ctx, admin(tenantB), "", ModeOverwrite, bytes.NewReader(archive))
	if err != nil || res.Updated.Submissions != 1 || res.Updated.Templates != 1 || res.Updated.Folders != 2 || res.Created.Objects != 5 {
		t.Fatalf("overwrite %+v %v", res, err)
	}
	if !dst.audit.has(audit.BackupImport, audit.OutcomeOK) || res.String() == "" {
		t.Fatal("import audited")
	}
}

func repoFilter() repo.CertificateFilter { return repo.CertificateFilter{PageSize: 100} }

func TestOtherKEKDropsSealedKeys(t *testing.T) {
	src := newStack(t, 7)
	src.seed(t)
	archive := src.export(t, tenantA)
	dst := newStack(t, 9)
	res, err := dst.svc.Import(ctx, admin(tenantB), "", ModeSkip, bytes.NewReader(archive))
	if err != nil {
		t.Fatal(err)
	}
	if res.NeedsReissue != 2 { // the CA and the admin certificate
		t.Fatalf("needs reissue %d", res.NeedsReissue)
	}
	certs, _, _ := dst.mem.ListCertificates(ctx, tenantB, repoFilter())
	pinKeys := 0
	for _, c := range certs {
		switch c.KeyProtection {
		case store.KeySealed:
			if len(c.KeyBlob) != 0 || (c.Status != store.CertNeedsReissue && c.Status != store.CertRevoked) {
				t.Fatalf("sealed key kept under another KEK: %s %s", c.Kind, c.Status)
			}
		case store.KeyPIN:
			if len(c.KeyBlob) == 0 {
				t.Fatal("PIN-encrypted key must travel as is")
			}
			pinKeys++
		}
	}
	if pinKeys != 1 {
		t.Fatalf("pin keys %d", pinKeys)
	}
}

func TestAccessAndLimits(t *testing.T) {
	src := newStack(t, 7)
	src.seed(t)
	if _, _, _, err := src.svc.Collect(ctx, admin(tenantB), tenantA); !errors.Is(err, apperr.Forbidden) {
		t.Fatalf("other tenant without platform admin: %v", err)
	}
	pa := authz.User(tenantB, "root", []string{authz.RolePlatformAdmin})
	if tn, _, _, err := src.svc.Collect(ctx, pa, tenantA); err != nil || tn != tenantA {
		t.Fatalf("platform admin: %v", err)
	}
	if _, _, _, err := src.svc.Collect(ctx, authz.System(), ""); !errors.Is(err, apperr.Validation) {
		t.Fatalf("no tenant: %v", err)
	}
	small := New(Deps{Store: src.mem, Blob: src.blob, Audit: src.audit, MaxBytes: 1000})
	if _, _, _, err := small.Collect(ctx, admin(tenantA), ""); !errors.Is(err, apperr.BackupTooLarge) {
		t.Fatalf("export too large: %v", err)
	}
	tn, recs, _, _ := src.svc.Collect(ctx, admin(tenantA), "")
	if err := New(Deps{Store: src.mem, Blob: src.blob, Audit: src.audit, MaxBytes: 3000}).Write(ctx, admin(tenantA), tn, recs, io.Discard); !errors.Is(err, apperr.BackupTooLarge) {
		t.Fatalf("stream cap: %v", err)
	}
	if _, err := src.svc.Import(ctx, admin(tenantA), "", "merge", bytes.NewReader(nil)); !errors.Is(err, apperr.Validation) {
		t.Fatalf("mode: %v", err)
	}
	if _, err := src.svc.Import(ctx, admin(tenantA), tenantB, ModeSkip, bytes.NewReader(nil)); !errors.Is(err, apperr.Forbidden) {
		t.Fatalf("import into another tenant: %v", err)
	}
}

func archive(t *testing.T, entries map[string][]byte, order ...string) []byte {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	for _, name := range order {
		data := entries[name]
		_ = tw.WriteHeader(&tar.Header{Name: name, Mode: 0o600, Size: int64(len(data)), Typeflag: tar.TypeReg})
		_, _ = tw.Write(data)
	}
	_ = tw.Close()
	_ = gz.Close()
	return buf.Bytes()
}

func TestHostileArchives(t *testing.T) {
	dst := newStack(t, 7)
	m, _ := json.Marshal(Manifest{SchemaVersion: SchemaVersion, TenantID: tenantA})
	r, _ := json.Marshal(Records{})
	cases := map[string][]byte{
		"not gzip":      []byte("junk"),
		"no records":    archive(t, map[string][]byte{"manifest.json": m}, "manifest.json"),
		"bad manifest":  archive(t, map[string][]byte{"manifest.json": []byte("{"), "records.json": r}, "manifest.json", "records.json"),
		"bad records":   archive(t, map[string][]byte{"manifest.json": m, "records.json": []byte("[")}, "manifest.json", "records.json"),
		"traversal":     archive(t, map[string][]byte{"manifest.json": m, "records.json": r, "objects/../../etc/x": {1}}, "manifest.json", "records.json", "objects/../../etc/x"),
		"foreign area":  archive(t, map[string][]byte{"manifest.json": m, "records.json": r, "objects/uploads/x.pdf": {1}}, "manifest.json", "records.json", "objects/uploads/x.pdf"),
		"unknown entry": archive(t, map[string][]byte{"manifest.json": m, "records.json": r, "evil.sh": {1}}, "manifest.json", "records.json", "evil.sh"),
	}
	future, _ := json.Marshal(Manifest{SchemaVersion: 99, TenantID: tenantA})
	cases["future schema"] = archive(t, map[string][]byte{"manifest.json": future, "records.json": r}, "manifest.json", "records.json")
	for name, data := range cases {
		if _, err := dst.svc.Import(ctx, admin(tenantB), "", ModeSkip, bytes.NewReader(data)); !errors.Is(err, apperr.InvalidBackup) {
			t.Errorf("%s: %v", name, err)
		}
	}
	tiny := New(Deps{Store: dst.mem, Blob: dst.blob, MaxBytes: 10})
	big := archive(t, map[string][]byte{"manifest.json": m, "records.json": r}, "manifest.json", "records.json")
	if _, err := tiny.Import(ctx, admin(tenantB), "", ModeSkip, bytes.NewReader(big)); err == nil {
		t.Fatal("oversized archive accepted")
	}
	// Records pointing outside the source tenant are refused per record.
	bad := Records{Templates: []store.Template{{ID: store.NewID(), Name: "X", PDFKey: "tenants/other/templates/x.pdf"}},
		Submissions: []SubmissionRecord{{Submission: store.Submission{ID: store.NewID(), Name: "S"}}}}
	bj, _ := json.Marshal(bad)
	res, err := dst.svc.Import(ctx, admin(tenantB), "", ModeSkip, bytes.NewReader(archive(t, map[string][]byte{"manifest.json": m, "records.json": bj}, "manifest.json", "records.json")))
	if err != nil || len(res.Errors) != 2 {
		t.Fatalf("bad records: %+v %v", res, err)
	}
	if !dst.audit.has(audit.BackupImport, audit.OutcomeRefused) {
		t.Fatal("refusal audited")
	}
}
