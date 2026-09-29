//go:build integration

package integration

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"io"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"

	"github.com/go-tangra/go-tangra-signing/v4/internal/stream"
)

// T092 / SC-005: a full flow with a known PIN and known field values; none of
// them — nor any private key — appears in logs, audit, events, e-mails,
// backups or database columns in clear.
func TestNoSecretsLeak(t *testing.T) {
	const (
		pinAlice = "739514"
		pinBob   = "862047"
		value    = "СекретнаСтойност-4242"
		prefill  = "987654.32"
		salary   = "112233.44"
	)
	e := newEnv(t)
	f := e.setupFlow(t, "sequential", map[string]string{"salary": prefill})
	e.setupCert(t, "aliceA", pinAlice)
	e.setupCert(t, "bobA", pinBob)
	expect(t, e.sign("aliceA", f.aliceSlot, pinAlice, map[string]string{"name": value}), 200, "alice")
	expect(t, e.sign("bobA", f.bobSlot, "000000", map[string]string{"salary": salary}), 403, "bob wrong pin")
	expect(t, e.sign("bobA", f.bobSlot, pinBob, map[string]string{"salary": salary}), 200, "bob")
	if done, _, err := e.a.Jobs.Drain(ctx); err != nil || done != 1 {
		t.Fatalf("audit trail job: %d %v", done, err)
	}
	r := e.call("POST", "/api/signing/v1/backup/export", "adminA", nil, "")
	expect(t, r, 200, "backup")
	backup := untar(t, r.Body.Bytes())
	e.a.Audit.Flush(ctx)

	secrets := []string{pinAlice, pinBob, value, prefill, salary, "PRIVATE KEY"}
	places := map[string]string{
		"logs":    e.logText(),
		"events":  e.events(t),
		"e-mails": e.mails.dump(),
		"backup":  backup,
		"db":      e.dbText(t),
	}
	for where, text := range places {
		if text == "" && where != "events" {
			t.Errorf("%s: nothing captured (the check would be vacuous)", where)
		}
		for _, s := range secrets {
			if strings.Contains(text, s) {
				t.Errorf("%q found in %s", s, where)
			}
		}
	}
	// The values are still there for the people who may see them.
	r = e.call("GET", "/api/signing/v1/signing/"+f.bobSlot, "bobA", nil, "")
	if !strings.Contains(r.Body.String(), value) {
		t.Fatal("bob no longer sees alice's value in his session")
	}
}

func (e *env) logText() string {
	e.logMu.Lock()
	defer e.logMu.Unlock()
	return e.logs.String()
}

func (e *env) events(t *testing.T) string {
	t.Helper()
	var b strings.Builder
	for _, tn := range []string{tenantA, tenantB} {
		entries, err := e.stream.XRange(ctx, stream.Key(tn), "0", 100000)
		if err != nil {
			t.Fatal(err)
		}
		for _, en := range entries {
			for k, v := range en.Fields {
				b.WriteString(k + "=" + v + "\n")
			}
		}
	}
	return b.String()
}

func (e *env) dbText(t *testing.T) string {
	t.Helper()
	conn, err := pgx.Connect(ctx, startDB(t).adminDSN)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close(ctx)
	var b strings.Builder
	for _, table := range []string{"signing_template_folders", "signing_templates", "signing_submissions", "signing_signers",
		"signing_document_versions", "signing_certificates", "signing_qes_preparations", "signing_events", "signing_jobs", "signing_audit_events"} {
		rows, err := conn.Query(ctx, "SELECT row_to_json(t)::text FROM "+table+" t")
		if err != nil {
			t.Fatal(err)
		}
		for rows.Next() {
			var s string
			if err := rows.Scan(&s); err != nil {
				t.Fatal(err)
			}
			b.WriteString(s + "\n")
		}
		rows.Close()
	}
	return b.String()
}

func untar(t *testing.T, archive []byte) string {
	t.Helper()
	gz, err := gzip.NewReader(bytes.NewReader(archive))
	if err != nil {
		t.Fatal(err)
	}
	tr := tar.NewReader(gz)
	var b strings.Builder
	for {
		h, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		data, _ := io.ReadAll(tr)
		b.WriteString(h.Name + "\n")
		b.Write(data)
	}
	return b.String()
}
