package app

import (
	"bytes"
	"context"
	"errors"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/go-tangra/go-tangra-auth/sdk/v4/pkg/authclient"
	"github.com/go-tangra/go-tangra/v4"

	"github.com/go-tangra/go-tangra-signing/v4/internal/authz"
	"github.com/go-tangra/go-tangra-signing/v4/internal/blob"
	"github.com/go-tangra/go-tangra-signing/v4/internal/config"
	"github.com/go-tangra/go-tangra-signing/v4/internal/memstore"
	"github.com/go-tangra/go-tangra-signing/v4/internal/stream"
)

const appTenant = "11111111-1111-7111-8111-111111111111"

type fakeVerifier struct{}

func (fakeVerifier) Verify(_ context.Context, token string) (authclient.Identity, error) {
	if token == "user" {
		return authclient.Identity{UserID: "u1", TenantID: appTenant}, nil
	}
	return authclient.Identity{}, errors.New("unauthenticated")
}

func testConfig() config.Config {
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
	return c
}

func options() Options {
	return Options{
		Verifier: fakeVerifier{}, Checker: authz.Static{"u1": {authz.SigningSign, authz.SigningRead}},
		Repo: memstore.New(), Stream: stream.NewMemory(), Blob: blob.NewFake(), KEK: bytes.Repeat([]byte{7}, 32),
		Freya: []freya.Option{freya.WithInsecureLocalDev(), freya.WithAllowAllPolicy()},
	}
}

func TestBuildWiresTheService(t *testing.T) {
	a, err := Build(context.Background(), testConfig(), options())
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	defer a.Close()
	if a.Freya == nil || a.Repo == nil || a.HTTP == nil || a.Hub == nil || a.Audit == nil || a.Metrics == nil || a.Blob == nil ||
		a.Sealer == nil || a.Limiter == nil || a.Events.Pub == nil || a.Templates == nil {
		t.Fatalf("app not fully wired: %+v", a)
	}
	do := func(path, tok string) *httptest.ResponseRecorder {
		r := httptest.NewRequest("GET", "https://localhost"+path, nil)
		if tok != "" {
			r.Header.Set("Authorization", "Bearer "+tok)
		}
		w := httptest.NewRecorder()
		a.HTTP.Handler().ServeHTTP(w, r)
		return w
	}
	if w := do("/api/signing/v1/health", ""); w.Code != 200 || !strings.Contains(w.Body.String(), `"store":"ok"`) {
		t.Fatalf("health: %d %s", w.Code, w.Body)
	}
	if w := do("/api/signing/v1/templates", ""); w.Code != 401 {
		t.Fatalf("anonymous: %d", w.Code)
	}
	if w := do("/api/signing/v1/templates", "user"); w.Code != 200 || !strings.Contains(w.Body.String(), `"total":0`) {
		t.Fatalf("templates: %d %s", w.Code, w.Body)
	}
	if w := do("/api/signing/v1/certificates", "user"); w.Code != 403 {
		t.Fatalf("missing permission: %d", w.Code)
	}
	a.Metrics.PINFailure()
	rec := httptest.NewRecorder()
	a.Freya.Metrics().Handler().ServeHTTP(rec, httptest.NewRequest("GET", "/metrics", nil))
	if !strings.Contains(rec.Body.String(), `signing_pin_failures_total 1`) {
		t.Fatalf("module metrics not exposed:\n%s", rec.Body)
	}
	a.Close()
	a.Close() // idempotent
}

func TestRunStopsOnCancel(t *testing.T) {
	cfg := testConfig()
	cfg.Events.Enabled = false
	a, err := Build(context.Background(), cfg, options())
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	ran := make(chan struct{})
	a.workers = append(a.workers, func(ctx context.Context) { close(ran); <-ctx.Done() })
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- a.Run(ctx) }()
	select {
	case <-ran:
	case <-time.After(5 * time.Second):
		t.Fatal("worker not started")
	}
	cancel()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("Run did not stop")
	}
}

func TestBuildFailures(t *testing.T) {
	cfg := testConfig()
	cfg.MeshEnroll = config.MeshEnroll{Enabled: true, TokenFile: "/nonexistent/token"}
	if _, err := Build(context.Background(), cfg, options()); err == nil {
		t.Fatal("missing enrolment token accepted")
	}
	cfg = testConfig()
	cfg.Valkey.CAFile = "/nonexistent/ca.pem"
	o := options()
	o.Stream = nil
	if _, err := Build(context.Background(), cfg, o); err == nil {
		t.Fatal("missing valkey CA accepted")
	}
	o = options()
	o.KEK = nil // falls back to the (missing) KEK file
	if _, err := Build(context.Background(), testConfig(), o); err == nil || !strings.Contains(err.Error(), "kek") {
		t.Fatalf("missing KEK accepted: %v", err)
	}
	o = options()
	o.KEK = []byte("short")
	if _, err := Build(context.Background(), testConfig(), o); err == nil {
		t.Fatal("short KEK accepted")
	}
	cfg = testConfig()
	o = options()
	o.Repo = nil
	o.Migrate = true
	cfg.DB.DSN = "postgres://127.0.0.1:1/none?connect_timeout=1"
	if _, err := Build(context.Background(), cfg, o); err == nil {
		t.Fatal("unreachable database accepted")
	}
}
