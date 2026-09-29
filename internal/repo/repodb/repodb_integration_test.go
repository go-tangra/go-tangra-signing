//go:build integration

// Package repodb integration test: runs the shared repo contract
// (repotest) against a real TimescaleDB (testcontainers) under the
// NOBYPASSRLS app role, and checks what only a database can prove: migrations
// are idempotent, row-level security isolates tenants even for raw SQL, and
// LockSubmission serialises two concurrent signers. Run with:
//
//	go test -tags integration ./internal/repo/repodb/
//
// It skips cleanly when Docker/testcontainers is unavailable.
package repodb_test

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/wait"

	"github.com/go-tangra/go-tangra-signing/v4/internal/repo"
	"github.com/go-tangra/go-tangra-signing/v4/internal/repo/repodb"
	"github.com/go-tangra/go-tangra-signing/v4/internal/repo/repotest"
	"github.com/go-tangra/go-tangra-signing/v4/internal/store"
)

var ctx = context.Background()

type dbEnv struct{ adminDSN, appDSN string }

func startDB(t *testing.T) dbEnv {
	t.Helper()
	c, err := testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{
		ContainerRequest: testcontainers.ContainerRequest{
			Image: "timescale/timescaledb:latest-pg16", ExposedPorts: []string{"5432/tcp"},
			Env:        map[string]string{"POSTGRES_PASSWORD": "test", "POSTGRES_DB": "signing"},
			WaitingFor: wait.ForLog("database system is ready to accept connections").WithOccurrence(2).WithStartupTimeout(2 * time.Minute),
		}, Started: true,
	})
	if err != nil {
		t.Skipf("testcontainers unavailable: %v", err)
	}
	t.Cleanup(func() { _ = c.Terminate(ctx) })
	host, _ := c.Host(ctx)
	port, _ := c.MappedPort(ctx, "5432/tcp")
	env := dbEnv{
		adminDSN: "postgres://postgres:test@" + host + ":" + port.Port() + "/signing?sslmode=disable",
		appDSN:   "postgres://signing_app:app@" + host + ":" + port.Port() + "/signing?sslmode=disable",
	}
	conn, err := pgx.Connect(ctx, env.adminDSN)
	if err != nil {
		t.Fatal(err)
	}
	// the stack's init-db creates the role; migrations only grant to it
	if _, err := conn.Exec(ctx, "CREATE ROLE signing_app LOGIN PASSWORD 'app' NOBYPASSRLS"); err != nil {
		t.Fatal(err)
	}
	_ = conn.Close(ctx)
	if err := store.Migrate(ctx, env.adminDSN); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	if err := store.Migrate(ctx, env.adminDSN); err != nil {
		t.Fatalf("migrate idempotent: %v", err)
	}
	return env
}

func TestRepoDB(t *testing.T) {
	env := startDB(t)
	st, err := store.Open(ctx, env.appDSN, 8)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(st.Close)
	admin, err := pgx.Connect(ctx, env.adminDSN)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = admin.Close(ctx) })
	truncate := func(t *testing.T) {
		t.Helper()
		if _, err := admin.Exec(ctx, `TRUNCATE signing_jobs, signing_events, signing_qes_preparations, signing_document_versions,
			signing_signers, signing_submissions, signing_certificates, signing_templates, signing_template_folders CASCADE`); err != nil {
			t.Fatal(err)
		}
	}

	repotest.Run(t, func(t *testing.T) repo.Store {
		truncate(t)
		return repodb.New(st)
	})

	t.Run("rls isolates tenants for raw SQL", func(t *testing.T) {
		truncate(t)
		db := repodb.New(st)
		if err := db.CreateTemplate(ctx, repotest.Template(repotest.TenantA, "Secret contract")); err != nil {
			t.Fatal(err)
		}
		var n int
		// Under tenant B's scope, even an unfiltered SELECT sees nothing.
		if err := st.Tx(ctx, store.Scope{TenantID: repotest.TenantB}, func(tx pgx.Tx) error {
			return tx.QueryRow(ctx, `SELECT count(*) FROM signing_templates`).Scan(&n)
		}); err != nil || n != 0 {
			t.Fatalf("tenant B sees %d templates (%v)", n, err)
		}
		// Without any scope the app role sees nothing either.
		if err := st.Tx(ctx, store.Scope{}, func(tx pgx.Tx) error {
			return tx.QueryRow(ctx, `SELECT count(*) FROM signing_templates`).Scan(&n)
		}); err == nil && n != 0 {
			t.Fatalf("unscoped app role sees %d templates", n)
		}
		// A write for tenant A under tenant B's scope is refused by the policy.
		err := st.Tx(ctx, store.Scope{TenantID: repotest.TenantB}, func(tx pgx.Tx) error {
			_, err := tx.Exec(ctx, `INSERT INTO signing_template_folders (id, tenant_id, name, path) VALUES ($1, $2, 'x', '/x')`,
				store.NewID(), repotest.TenantA)
			return err
		})
		if err == nil {
			t.Fatal("cross-tenant insert accepted")
		}
		if err := st.Tx(ctx, store.Scope{System: true}, func(tx pgx.Tx) error {
			return tx.QueryRow(ctx, `SELECT count(*) FROM signing_templates`).Scan(&n)
		}); err != nil || n != 1 {
			t.Fatalf("system scope sees %d (%v)", n, err)
		}
	})

	t.Run("lock serialises concurrent signers", func(t *testing.T) {
		truncate(t)
		db := repodb.New(st)
		tpl := repotest.Template(repotest.TenantA, "Contract")
		if err := db.CreateTemplate(ctx, tpl); err != nil {
			t.Fatal(err)
		}
		sub, signers, v0 := repotest.Submission(tpl)
		if err := db.CreateSubmission(ctx, sub, signers, v0); err != nil {
			t.Fatal(err)
		}
		// Two transactions each read current_version, "sign" and write
		// version+1. Without the row lock one would overwrite the other.
		var wg sync.WaitGroup
		errs := make(chan error, 2)
		for i := 0; i < 2; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				errs <- db.Tx(ctx, repotest.TenantA, func(r repo.Store) error {
					s, _, err := r.LockSubmission(ctx, repotest.TenantA, sub.ID)
					if err != nil {
						return err
					}
					time.Sleep(100 * time.Millisecond) // the signing work
					next := s.CurrentVersion + 1
					if err := r.AddVersion(ctx, store.DocumentVersion{SubmissionID: sub.ID, Version: next, TenantID: repotest.TenantA,
						ObjectKey: "k", SHA256: "h"}); err != nil {
						return err
					}
					s.CurrentVersion = next
					return r.UpdateSubmission(ctx, s)
				})
			}()
		}
		wg.Wait()
		close(errs)
		for err := range errs {
			if err != nil {
				t.Fatalf("concurrent signer: %v", err)
			}
		}
		got, _, _ := db.GetSubmission(ctx, repotest.TenantA, sub.ID)
		vs, _ := db.ListVersions(ctx, repotest.TenantA, sub.ID)
		if got.CurrentVersion != 2 || len(vs) != 3 {
			t.Fatalf("current = %d, versions = %d (a signature was lost)", got.CurrentVersion, len(vs))
		}
	})
}
