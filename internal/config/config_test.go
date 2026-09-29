package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// valid returns a Config that passes both the framework and module Validate.
func valid() Config {
	c := Default()
	c.ServiceName = "signing"
	c.TrustDomain = "example.org"
	c.Authz.Path = "/etc/signing/policy.yaml"
	c.DB.DSN = "postgres://localhost/signing"
	c.Valkey.Addresses = []string{"valkey:6379"}
	c.Gateway.Issuer = "https://gw.example.org"
	c.KEK.Path = "/etc/signing/kek"
	c.ObjectStore.Endpoint = "rustfs:9000"
	c.ObjectStore.AccessKey = "ak"
	c.ObjectStore.SecretKey = "sk"
	c.Links.PortalBaseURL = "https://portal.example.org:8443"
	return c
}

func prod() Config {
	c := valid()
	c.Env = "production"
	c.DB.DSN = "postgres://db/signing?sslmode=verify-full"
	return c
}

func TestDefaultSecure(t *testing.T) {
	d := Default()
	if d.Valkey.AllowPlaintext || !d.Events.Enabled || d.MeshEnroll.Insecure || !d.ObjectStore.UseSSL {
		t.Fatalf("insecure defaults: %+v %+v %+v", d.Valkey, d.Events, d.ObjectStore)
	}
	if d.PlatformTenantID != DefaultPlatformTenant || d.Gateway.Service != "gateway" || d.KEK.Source != "file" ||
		d.ObjectStore.Bucket != "signing" || d.Auth.Service != "auth" || d.Notification.Service != "notification" ||
		d.Warden.Service != "warden" || d.TaskScheduler.Service != "scheduler" || d.TaskScheduler.Enabled {
		t.Fatalf("defaults: %+v", d)
	}
	l := d.Limits
	if l.MaxPDFBytes != 50<<20 || l.MaxPDFPages != 500 || l.MaxFields != 500 || l.MaxSigners != 50 ||
		l.MaxImageBytes != 1<<20 || l.MaxFieldUploadBytes != 5<<20 || l.SigningsPerMinute != 10 || l.QESPreparationMinute != 10 {
		t.Fatalf("limit defaults: %+v", l)
	}
	s := d.Signing
	if s.CAValidityYears != 10 || s.CertValidityYears != 2 || s.PINMin != 6 || s.PINMax != 32 || s.PINIterations != 600000 ||
		s.LockAttempts != 5 || s.LockMinutes != 15 {
		t.Fatalf("signing defaults: %+v", s)
	}
	if err := Default().Validate(); err == nil {
		t.Fatal("Default() must not validate without required fields")
	}
}

func TestValidateOK(t *testing.T) {
	if err := valid().Validate(); err != nil {
		t.Fatalf("valid config rejected: %v", err)
	}
	c := prod()
	c.MeshEnroll = MeshEnroll{Enabled: true}
	c.KEK = KEK{Source: "env", Env: "SIGNING_KEK"}
	c.TaskScheduler.Enabled = true
	if err := c.Validate(); err != nil {
		t.Fatalf("valid production config rejected: %v", err)
	}
}

func TestValidateRefusals(t *testing.T) {
	cases := map[string]func(*Config){
		"framework":    func(c *Config) { c.ServiceName = "" },
		"no dsn":       func(c *Config) { c.DB.DSN = "" },
		"prod sslmode": func(c *Config) { c.Env = "production" },
		"max conns":    func(c *Config) { c.DB.MaxConns = 1000 },
		"no valkey":    func(c *Config) { c.Valkey.Addresses = nil },
		"prod plaintext": func(c *Config) {
			*c = prod()
			c.Valkey.AllowPlaintext = true
		},
		"no gateway":  func(c *Config) { c.Gateway.Service = "" },
		"http issuer": func(c *Config) { c.Gateway.Issuer = "http://gw" },
		"bad issuer":  func(c *Config) { c.Gateway.Issuer = "://" },
		"prod insecure mesh": func(c *Config) {
			*c = prod()
			c.MeshEnroll = MeshEnroll{Enabled: true, Insecure: true}
		},
		"platform tenant":      func(c *Config) { c.PlatformTenantID = "platform" },
		"no endpoint":          func(c *Config) { c.ObjectStore.Endpoint = "" },
		"endpoint with scheme": func(c *Config) { c.ObjectStore.Endpoint = "http://rustfs:9000" },
		"bucket":               func(c *Config) { c.ObjectStore.Bucket = "Bad_Bucket" },
		"no access key":        func(c *Config) { c.ObjectStore.AccessKey = "" },
		"no secret key":        func(c *Config) { c.ObjectStore.SecretKey = "" },
		"prod plain objects": func(c *Config) {
			*c = prod()
			c.ObjectStore.UseSSL = false
		},
		"no portal":         func(c *Config) { c.Links.PortalBaseURL = "" },
		"http portal":       func(c *Config) { c.Links.PortalBaseURL = "http://portal" },
		"portal path":       func(c *Config) { c.Links.PortalBaseURL = "https://portal/x" },
		"bad portal":        func(c *Config) { c.Links.PortalBaseURL = "://" },
		"kek file no path":  func(c *Config) { c.KEK.Path = "" },
		"kek env no name":   func(c *Config) { c.KEK = KEK{Source: "env"} },
		"kek source":        func(c *Config) { c.KEK.Source = "vault" },
		"auth service":      func(c *Config) { c.Auth.Service = "Auth!" },
		"notify service":    func(c *Config) { c.Notification.Service = "" },
		"warden service":    func(c *Config) { c.Warden.Service = "-w" },
		"scheduler missing": func(c *Config) { c.TaskScheduler = TaskScheduler{Enabled: true} },
		"scheduler name":    func(c *Config) { c.TaskScheduler.Service = "Sched" },
		"pdf bytes":         func(c *Config) { c.Limits.MaxPDFBytes = 10 },
		"pdf pages":         func(c *Config) { c.Limits.MaxPDFPages = 0 },
		"fields":            func(c *Config) { c.Limits.MaxFields = 0 },
		"signers":           func(c *Config) { c.Limits.MaxSigners = 1000 },
		"image":             func(c *Config) { c.Limits.MaxImageBytes = 1 },
		"field upload":      func(c *Config) { c.Limits.MaxFieldUploadBytes = 1 },
		"parse timeout":     func(c *Config) { c.Limits.ParseTimeoutSeconds = 0 },
		"page size":         func(c *Config) { c.Limits.MaxPageSize = 1000 },
		"backup":            func(c *Config) { c.Limits.MaxBackupBytes = 1 },
		"rate":              func(c *Config) { c.Limits.SigningsPerMinute = 0 },
		"qes ttl":           func(c *Config) { c.Limits.QESPreparationMinute = 0 },
		"ca validity":       func(c *Config) { c.Signing.CAValidityYears = 1 },
		"ca renew":          func(c *Config) { c.Signing.CARenewBeforeYears = 10 },
		"cert validity":     func(c *Config) { c.Signing.CertValidityYears = 10 },
		"crl":               func(c *Config) { c.Signing.CRLValidityDays = 0 },
		"pin min":           func(c *Config) { c.Signing.PINMin = 2 },
		"pin max":           func(c *Config) { c.Signing.PINMax = 3 },
		"iterations":        func(c *Config) { c.Signing.PINIterations = 1000 },
		"lock attempts":     func(c *Config) { c.Signing.LockAttempts = 1 },
		"lock minutes":      func(c *Config) { c.Signing.LockMinutes = 0 },
		"job attempts":      func(c *Config) { c.Signing.AuditJobMaxAttempts = 0 },
		"job interval":      func(c *Config) { c.Signing.AuditJobIntervalMillis = 1 },
	}
	for name, mut := range cases {
		c := valid()
		mut(&c)
		if err := c.Validate(); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}

func TestLoad(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "signing.yaml")
	body := `service_name: signing
trust_domain: example.org
authz: {path: /etc/signing/policy.yaml}
db: {dsn: "postgres://localhost/signing"}
valkey: {addresses: ["valkey:6379"], allow_plaintext: true}
gateway: {issuer: "https://gw.example.org"}
kek: {path: /etc/signing/kek}
object_store: {endpoint: "rustfs:9000", access_key: a, secret_key: b, use_ssl: false}
links: {portal_base_url: "https://portal.example.org"}
limits_signing: {max_signers: 20}
signing: {lock_minutes: 30}
`
	if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	c, err := Load(p)
	if err != nil {
		t.Fatal(err)
	}
	if err := c.Validate(); err != nil {
		t.Fatal(err)
	}
	if c.Limits.MaxSigners != 20 || c.Limits.MaxFields != 500 || c.LockDuration() != 30*time.Minute {
		t.Fatalf("loaded: %+v %+v", c.Limits, c.Signing)
	}
	if err := os.WriteFile(p, []byte(body+"bogus: 1\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(p); err == nil || !strings.Contains(err.Error(), "bogus") {
		t.Fatalf("unknown key accepted: %v", err)
	}
	if _, err := Load(filepath.Join(dir, "missing.yaml")); err == nil {
		t.Fatal("missing file accepted")
	}
}

func TestWarningsAndDurations(t *testing.T) {
	c := valid()
	c.Valkey.AllowPlaintext = true
	c.MeshEnroll = MeshEnroll{Enabled: true, Insecure: true}
	c.Events.Enabled = false
	c.ObjectStore.UseSSL = false
	w := strings.Join(c.Warnings(), "\n")
	for _, want := range []string{"valkey.allow_plaintext", "mesh_enroll.insecure", "events.enabled=false",
		"object_store.use_ssl=false", "task_scheduler.enabled=false"} {
		if !strings.Contains(w, want) {
			t.Errorf("warnings lack %s: %s", want, w)
		}
	}
	s := valid()
	s.TaskScheduler.Enabled = true
	if len(s.Warnings()) != len(s.Config.Warnings()) {
		t.Fatalf("secure config warns: %v", s.Warnings())
	}
	if c.ParseTimeout() != 20*time.Second || c.QESPreparationTTL() != 10*time.Minute || c.LockDuration() != 15*time.Minute ||
		c.AuditJobInterval() != 2*time.Second {
		t.Fatal("durations")
	}
}

// The shipped example configuration loads and validates (dev opt-outs only).
func TestDeployExampleValidates(t *testing.T) {
	c, err := Load(filepath.Join("..", "..", "deploy", "container.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if err := c.Validate(); err != nil {
		t.Fatalf("deploy/container.yaml: %v", err)
	}
	if c.Server.GRPCAddr != "0.0.0.0:9915" || c.Server.HTTPAddr != "0.0.0.0:9916" || c.Admin.Addr != "127.0.0.1:9860" {
		t.Fatalf("ports: %+v %+v", c.Server, c.Admin)
	}
}
