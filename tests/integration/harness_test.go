//go:build integration

// Package integration runs the whole signing service (app.Build) against a
// real TimescaleDB under the NOBYPASSRLS app role: concurrent signers
// (T056), cross-tenant isolation of every route (T093, SC-004) and the
// end-to-end secret leak test (T092, SC-005). Run with:
//
//	go test -tags integration ./tests/integration/
//
// It skips cleanly when Docker/testcontainers is unavailable.
package integration

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/wait"

	"github.com/go-tangra/go-tangra-auth/sdk/v4/pkg/authclient"
	"github.com/go-tangra/go-tangra-notification/sdk/v4/pkg/notifyclient"
	"github.com/go-tangra/go-tangra/v4"

	"github.com/go-tangra/go-tangra-signing/v4/internal/app"
	"github.com/go-tangra/go-tangra-signing/v4/internal/authz"
	"github.com/go-tangra/go-tangra-signing/v4/internal/blob"
	"github.com/go-tangra/go-tangra-signing/v4/internal/config"
	"github.com/go-tangra/go-tangra-signing/v4/internal/contacts"
	"github.com/go-tangra/go-tangra-signing/v4/internal/pdf/pdftest"
	"github.com/go-tangra/go-tangra-signing/v4/internal/repo/repodb"
	"github.com/go-tangra/go-tangra-signing/v4/internal/store"
	"github.com/go-tangra/go-tangra-signing/v4/internal/stream"
)

const (
	tenantA = "0190f7c2-6a3e-7c1a-9b2e-2f6f9d1b4c55"
	tenantB = "0190f7c2-6a3e-7c1a-9b2e-2f6f9d1b4c66"
)

var ctx = context.Background()

type dbEnv struct{ adminDSN, appDSN string }

var (
	dbOnce sync.Once
	dbVal  dbEnv
	dbErr  error
)

// startDB starts one TimescaleDB for the package (migrated, app role).
func startDB(t *testing.T) dbEnv {
	t.Helper()
	dbOnce.Do(func() {
		c, err := testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{
			ContainerRequest: testcontainers.ContainerRequest{
				Image: "timescale/timescaledb:latest-pg16", ExposedPorts: []string{"5432/tcp"},
				Env:        map[string]string{"POSTGRES_PASSWORD": "test", "POSTGRES_DB": "signing"},
				WaitingFor: wait.ForLog("database system is ready to accept connections").WithOccurrence(2).WithStartupTimeout(2 * time.Minute),
			}, Started: true,
		})
		if err != nil {
			dbErr = err
			return
		}
		host, _ := c.Host(ctx)
		port, _ := c.MappedPort(ctx, "5432/tcp")
		dbVal = dbEnv{
			adminDSN: "postgres://postgres:test@" + host + ":" + port.Port() + "/signing?sslmode=disable",
			appDSN:   "postgres://signing_app:app@" + host + ":" + port.Port() + "/signing?sslmode=disable",
		}
		conn, err := pgx.Connect(ctx, dbVal.adminDSN)
		if err != nil {
			dbErr = err
			return
		}
		defer conn.Close(ctx)
		if _, err := conn.Exec(ctx, "CREATE ROLE signing_app LOGIN PASSWORD 'app' NOBYPASSRLS"); err != nil {
			dbErr = err
			return
		}
		dbErr = store.Migrate(ctx, dbVal.adminDSN)
	})
	if dbErr != nil {
		t.Skipf("database unavailable: %v", dbErr)
	}
	return dbVal
}

// ---- identities

type verifier map[string]authclient.Identity

func (v verifier) Verify(_ context.Context, tok string) (authclient.Identity, error) {
	id, ok := v[tok]
	if !ok {
		return authclient.Identity{}, errors.New("unauthenticated")
	}
	return id, nil
}

var tokens = verifier{
	"adminA":  {UserID: "adminA", TenantID: tenantA},
	"aliceA":  {UserID: "aliceA", TenantID: tenantA},
	"bobA":    {UserID: "bobA", TenantID: tenantA},
	"adminB":  {UserID: "adminB", TenantID: tenantB},
	"memberB": {UserID: "memberB", TenantID: tenantB},
}

var perms = authz.Static{
	"adminA": authz.Permissions, "adminB": authz.Permissions,
	"aliceA": {authz.SigningSign}, "bobA": {authz.SigningSign}, "memberB": {authz.SigningSign},
}

var directory = &contacts.Fake{Users: map[string][]contacts.Contact{
	tenantA: {
		{UserID: "adminA", DisplayName: "Ada Admin", Email: "ada@a.example"},
		{UserID: "aliceA", DisplayName: "Alice Иванова", Email: "alice@a.example"},
		{UserID: "bobA", DisplayName: "Bob Builder", Email: "bob@a.example"},
	},
	tenantB: {
		{UserID: "adminB", DisplayName: "Bea Admin", Email: "bea@b.example"},
		{UserID: "memberB", DisplayName: "Mo", Email: "mo@b.example"},
	},
}}

// ---- captured side channels

type mails struct {
	mu   sync.Mutex
	vars []map[string]string
}

func (m *mails) SendKey(_ context.Context, _, key, to string, vars map[string]string, _ string) (notifyclient.Result, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	cp := map[string]string{"_key": key, "_to": to}
	for k, v := range vars {
		cp[k] = v
	}
	m.vars = append(m.vars, cp)
	return notifyclient.Result{Sent: true}, nil
}

func (m *mails) dump() string {
	m.mu.Lock()
	defer m.mu.Unlock()
	b, _ := json.Marshal(m.vars)
	return string(b)
}

type env struct {
	a      *app.App
	st     *store.Store
	blob   *blob.Fake
	stream *stream.Memory
	mails  *mails
	logs   *bytes.Buffer
	logMu  *sync.Mutex
}

type lockedWriter struct {
	mu *sync.Mutex
	w  io.Writer
}

func (l lockedWriter) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.w.Write(p)
}

func truncate(t *testing.T, db dbEnv) {
	t.Helper()
	conn, err := pgx.Connect(ctx, db.adminDSN)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close(ctx)
	if _, err := conn.Exec(ctx, `TRUNCATE signing_jobs, signing_events, signing_qes_preparations, signing_document_versions,
		signing_signers, signing_submissions, signing_certificates, signing_templates, signing_template_folders CASCADE`); err != nil {
		t.Fatal(err)
	}
}

func config_() config.Config {
	c := config.Default()
	c.ServiceName, c.TrustDomain, c.Env = "signing", "example.org", "dev"
	c.DB.DSN = "postgres://unused"
	c.Valkey.Addresses, c.Valkey.AllowPlaintext = []string{"127.0.0.1:1"}, true
	c.Server.GRPCAddr, c.Server.HTTPAddr, c.Admin.Addr = "127.0.0.1:0", "127.0.0.1:0", "127.0.0.1:0"
	c.Discovery.Static = map[string][]string{"lcm": {"127.0.0.1:1"}, "auth": {"127.0.0.1:1"}, "gateway": {"127.0.0.1:1"},
		"notification": {"127.0.0.1:1"}, "scheduler": {"127.0.0.1:1"}, "warden": {"127.0.0.1:1"}}
	c.Gateway.Service, c.Gateway.Issuer = "gateway", "https://localhost:8443"
	c.KEK.Path = "/unused"
	c.ObjectStore = config.ObjectStore{Endpoint: "127.0.0.1:1", Bucket: "signing", Region: "us-east-1", AccessKey: "a", SecretKey: "b"}
	c.Links.PortalBaseURL = "https://localhost:8443"
	c.Signing.PINIterations = 100000
	return c
}

func newEnv(t *testing.T) *env {
	t.Helper()
	db := startDB(t)
	truncate(t, db)
	st, err := store.Open(ctx, db.appDSN, 16)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(st.Close)
	e := &env{st: st, blob: blob.NewFake(), stream: stream.NewMemory(), mails: &mails{}, logs: &bytes.Buffer{}, logMu: &sync.Mutex{}}
	e.a, err = app.Build(ctx, config_(), app.Options{
		Verifier: tokens, Checker: perms, Contacts: directory, Notify: e.mails, Repo: repodb.New(st), Stream: e.stream, Blob: e.blob,
		KEK:    bytes.Repeat([]byte{7}, 32),
		Logger: slog.NewJSONHandler(lockedWriter{e.logMu, e.logs}, &slog.HandlerOptions{Level: slog.LevelDebug}),
		Freya:  []freya.Option{freya.WithInsecureLocalDev(), freya.WithAllowAllPolicy()},
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(e.a.Close)
	return e
}

type resp struct{ *httptest.ResponseRecorder }

func (r resp) json(t *testing.T) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal(r.Body.Bytes(), &m); err != nil {
		t.Fatalf("not json (%d): %s", r.Code, r.Body)
	}
	return m
}

func (e *env) call(method, path, token string, body io.Reader, ct string) resp {
	r := httptest.NewRequest(method, "https://localhost"+path, body)
	if token != "" {
		r.Header.Set("Authorization", "Bearer "+token)
	}
	if method != http.MethodGet {
		r.Header.Set("X-CSRF-Token", "t")
	}
	if ct != "" {
		r.Header.Set("Content-Type", ct)
	}
	w := httptest.NewRecorder()
	e.a.HTTP.Handler().ServeHTTP(w, r)
	return resp{w}
}

func (e *env) json(method, path, token string, v any) resp {
	if v == nil {
		return e.call(method, path, token, nil, "")
	}
	b, _ := json.Marshal(v)
	return e.call(method, path, token, bytes.NewReader(b), "application/json")
}

func multipartBody(fields map[string]string, files map[string][]byte) (*bytes.Buffer, string) {
	var b bytes.Buffer
	mw := multipart.NewWriter(&b)
	for k, v := range fields {
		_ = mw.WriteField(k, v)
	}
	for k, v := range files {
		fw, _ := mw.CreateFormFile(k, "file.pdf")
		_, _ = fw.Write(v)
	}
	_ = mw.Close()
	return &b, mw.FormDataContentType()
}

func expect(t *testing.T, r resp, status int, what string) {
	t.Helper()
	if r.Code != status {
		t.Fatalf("%s: status %d, want %d: %s", what, r.Code, status, r.Body)
	}
}

// flow is a tenant-A submission with its ids.
type flow struct {
	template, submission, aliceSlot, bobSlot string
}

// setupFlow creates an active two-party template and a sent submission
// (alice = employee, bob = employer) in tenant A, with a prefill.
func (e *env) setupFlow(t *testing.T, mode string, prefill map[string]string) flow {
	t.Helper()
	body, ct := multipartBody(map[string]string{"name": "Contract " + mode + " " + store.NewID()[:13]}, map[string][]byte{"file": pdftest.Contract(2)})
	r := e.call("POST", "/api/signing/v1/templates", "adminA", body, ct)
	expect(t, r, 201, "create template")
	tpl := r.json(t)
	id := tpl["id"].(string)
	field := func(fid, name, typ, party string, page int, y float64, required bool) map[string]any {
		return map[string]any{"id": fid, "name": name, "type": typ, "party": party, "page": page, "x": 0.1, "y": y, "w": 0.3, "h": 0.04, "required": required}
	}
	r = e.json("PUT", "/api/signing/v1/templates/"+id+"/fields", "adminA", map[string]any{
		"version": tpl["version"], "parties": []map[string]any{{"key": "employee", "name": "Employee"}, {"key": "employer", "name": "Employer"}},
		"fields": []map[string]any{
			field("name", "Name", "text", "employee", 1, 0.15, true),
			field("esig", "Employee signature", "signature", "employee", 1, 0.8, false),
			field("salary", "Salary", "number", "employer", 1, 0.25, false),
			field("rsig", "Employer signature", "signature", "employer", 2, 0.8, false),
		}})
	expect(t, r, 200, "save fields")
	expect(t, e.json("PATCH", "/api/signing/v1/templates/"+id, "adminA", map[string]any{"status": "active"}), 200, "activate")
	create := map[string]any{"template_id": id, "mode": mode,
		"signers": []map[string]any{{"user_id": "aliceA", "party": "employee"}, {"user_id": "bobA", "party": "employer", "position": 1}}}
	if prefill != nil {
		create["prefill"] = prefill
	}
	r = e.json("POST", "/api/signing/v1/submissions", "adminA", create)
	expect(t, r, 201, "create submission")
	sub := r.json(t)
	f := flow{template: id, submission: sub["id"].(string)}
	for _, s := range sub["signers"].([]any) {
		m := s.(map[string]any)
		if m["user_id"] == "aliceA" {
			f.aliceSlot = m["id"].(string)
		} else {
			f.bobSlot = m["id"].(string)
		}
	}
	expect(t, e.call("POST", "/api/signing/v1/submissions/"+f.submission+"/send", "adminA", nil, ""), 200, "send")
	return f
}

func (e *env) setupCert(t *testing.T, token, pin string) {
	t.Helper()
	expect(t, e.json("POST", "/api/signing/v1/me/certificate", token, map[string]any{"pin": pin}), 201, "certificate for "+token)
}

func (e *env) sign(token, slot, pin string, values map[string]string) resp {
	v, _ := json.Marshal(values)
	body, ct := multipartBody(map[string]string{"values": string(v), "pin": pin}, nil)
	return e.call("POST", "/api/signing/v1/signing/"+slot+"/sign", token, body, ct)
}
