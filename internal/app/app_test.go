package app

import (
	"bytes"
	"context"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"github.com/go-tangra/go-tangra-signing/v4/internal/pdf/pdftest"
	"github.com/go-tangra/go-tangra-signing/v4/internal/warden"
	"net/http/httptest"
	"os"
	"path/filepath"
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
		a.Sealer == nil || a.Limiter == nil || a.Events.Pub == nil || a.Templates == nil ||
		a.PKI == nil || a.Me == nil || a.Contacts == nil || a.Submissions == nil || a.Signing == nil || a.Mail.Sender == nil ||
		a.Admin == nil || a.Documents == nil {
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
	if w := do("/api/signing/v1/inbox", "user"); w.Code != 200 || !strings.Contains(w.Body.String(), `"total":0`) {
		t.Fatalf("inbox: %d %s", w.Code, w.Body)
	}
	// Without a reachable notification module mail is a retryable failure.
	if res, err := a.Mail.Sender.SendKey(context.Background(), "t", "signing.invitation", "a@b", nil, ""); err != nil || !res.Retryable {
		t.Fatalf("unreachable notification: %+v %v", res, err)
	}
	if limited, err := a.Limiter.Limited(context.Background(), "sign", "t:u", 0, time.Now()); err != nil || limited {
		t.Fatal("no limit configured")
	}
	// Without a reachable warden a TSA secret reads as unavailable.
	lw := &lazyWarden{app: a, service: "warden"}
	wctx, cancel := context.WithTimeout(warden.WithUserToken(context.Background(), "tok"), 2*time.Second)
	defer cancel()
	ref := "01928f7e-3c1a-7b44-9d2e-5a6b7c8d9e0f"
	if _, err := lw.Credentials(wctx, ref); !errors.Is(err, warden.ErrUnavailable) {
		t.Fatalf("warden down: %v", err)
	}
	if _, err := lw.Meta(wctx, ref); !errors.Is(err, warden.ErrUnavailable) {
		t.Fatalf("warden down: %v", err)
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

func TestTrustRoots(t *testing.T) {
	if p, err := trustRoots(config.Verify{}); p != nil || err != nil {
		t.Fatal("no roots configured")
	}
	dir := t.TempDir()
	good := filepath.Join(dir, "roots.pem")
	ca := pdftest.CA("Qualified Root")
	_ = os.WriteFile(good, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: ca.Cert.Raw}), 0o600)
	if p, err := trustRoots(config.Verify{ExtraRootsFile: good}); p == nil || err != nil {
		t.Fatalf("extra roots: %v", err)
	}
	if p, err := trustRoots(config.Verify{UseSystemRoots: true, ExtraRootsFile: good}); p == nil || err != nil {
		t.Fatalf("system + extra: %v", err)
	}
	bad := filepath.Join(dir, "bad.pem")
	_ = os.WriteFile(bad, []byte("nothing"), 0o600)
	if _, err := trustRoots(config.Verify{ExtraRootsFile: bad}); err == nil {
		t.Fatal("empty bundle accepted")
	}
	if _, err := trustRoots(config.Verify{ExtraRootsFile: filepath.Join(dir, "missing")}); err == nil {
		t.Fatal("missing file accepted")
	}
	cfg := testConfig()
	cfg.Verify.ExtraRootsFile = bad
	if _, err := Build(context.Background(), cfg, options()); err == nil {
		t.Fatal("build with a bad roots file")
	}
}

func TestQESOrigin(t *testing.T) {
	if o, err := qesOrigin(config.QES{}); o != nil || err != nil {
		t.Fatal("not configured")
	}
	dir := t.TempDir()
	card := pdftest.Card("portal.example.org")
	certFile, keyFile := filepath.Join(dir, "c.pem"), filepath.Join(dir, "k.pem")
	_ = os.WriteFile(certFile, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: card.Cert.Raw}), 0o600)
	_ = os.WriteFile(keyFile, pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(card.Key)}), 0o600)
	o, err := qesOrigin(config.QES{OriginCertFile: certFile, OriginKeyFile: keyFile})
	if err != nil || o.Cert.Subject.CommonName != "portal.example.org" || o.Signer == nil {
		t.Fatalf("origin: %v", err)
	}
	if _, err := qesOrigin(config.QES{OriginCertFile: certFile, OriginKeyFile: certFile}); err == nil {
		t.Fatal("certificate as key accepted")
	}
	cfg := testConfig()
	cfg.QES = config.QES{OriginCertFile: keyFile, OriginKeyFile: keyFile}
	if _, err := Build(context.Background(), cfg, options()); err == nil {
		t.Fatal("build with a bad origin")
	}
}

func TestSchedulerWiring(t *testing.T) {
	cfg := testConfig()
	cfg.TaskScheduler.Enabled = true
	a, err := Build(context.Background(), cfg, options())
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	if a.Tasks == nil || len(a.workers) != 2 { // jobs worker + registrar
		t.Fatalf("tasks %v workers %d", a.Tasks, len(a.workers))
	}
	if _, ok := schedulerCaller("example.org")(context.Background()); ok {
		t.Fatal("a call without a verified peer has no caller")
	}
}
