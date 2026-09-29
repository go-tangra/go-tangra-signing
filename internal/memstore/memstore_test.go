package memstore

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/go-tangra/go-tangra-signing/v4/internal/repo"
	"github.com/go-tangra/go-tangra-signing/v4/internal/repo/repotest"
	"github.com/go-tangra/go-tangra-signing/v4/internal/store"
)

func TestContract(t *testing.T) {
	repotest.Run(t, func(*testing.T) repo.Store { return New() })
}

func TestFailureInjection(t *testing.T) {
	m := New()
	boom := errors.New("down")
	m.SetErr(boom)
	ctx := context.Background()
	if _, err := m.ListFolders(ctx, repotest.TenantA); !errors.Is(err, boom) {
		t.Fatal("global error")
	}
	if err := m.Tx(ctx, repotest.TenantA, func(repo.Store) error { return nil }); !errors.Is(err, boom) {
		t.Fatal("tx error")
	}
	m.SetErr(nil)
	m.Fail("CreateTemplate", boom)
	if err := m.CreateTemplate(ctx, repotest.Template(repotest.TenantA, "x")); !errors.Is(err, boom) {
		t.Fatal("method error")
	}
	if _, err := m.ListFolders(ctx, repotest.TenantA); err != nil {
		t.Fatal("other methods unaffected")
	}
	m.Fail("LockSubmission", boom)
	if _, _, err := m.LockSubmission(ctx, repotest.TenantA, "x"); !errors.Is(err, boom) {
		t.Fatal("lock error")
	}
	m.Fail("", nil)
}

func TestHelpers(t *testing.T) {
	m := New()
	ctx := context.Background()
	tpl := repotest.Template(repotest.TenantA, "Contract")
	_ = m.CreateTemplate(ctx, tpl)
	sub, signers, v0 := repotest.Submission(tpl)
	m.PutSubmission(sub)
	m.PutSigner(signers[0])
	_ = m.AddVersion(ctx, v0)
	c := repotest.Certificate(repotest.TenantA, store.KindCA, nil, nil)
	m.PutCertificate(c)
	now := time.Now()
	_ = m.AddEvent(ctx, store.Event{ID: "e", TenantID: repotest.TenantA, Type: "x", At: now})
	_ = m.EnqueueJob(ctx, store.Job{ID: "j", TenantID: repotest.TenantA, SubmissionID: sub.ID, Kind: store.JobAuditTrail})
	_ = m.AppendAudit(ctx, store.AuditRow{ID: "a"})
	if len(m.Events()) != 1 || len(m.Jobs()) != 1 || len(m.Audit()) != 1 {
		t.Fatal("helpers")
	}
	if _, sg, _ := m.GetSubmission(ctx, repotest.TenantA, sub.ID); len(sg) != 1 {
		t.Fatal("put signer")
	}
}
