// Package config loads and validates the signing service configuration: the
// Freya framework config plus the module's own sections. Every value is
// explicit; insecure opt-outs are named and surfaced at start (Constitution
// I/VII). The service refuses to start without a store, an event bus, an
// object store, a key-encryption key and a gateway issuer.
//
// The module's yaml keys never collide with the framework sections the
// embedded config already owns (server, admin, discovery, limits, identity,
// authz): the module's bounds live under "limits_signing".
package config

import (
	"bytes"
	"errors"
	"fmt"
	"net/url"
	"os"
	"regexp"
	"strings"
	"time"

	fconfig "github.com/go-tangra/go-tangra/v4/config"
	"gopkg.in/yaml.v3"
)

// DefaultPlatformTenant is the tenant of the stack's platform administrators.
const DefaultPlatformTenant = "00000000-0000-0000-0000-000000000001"

// Config is the signing service configuration. The embedded framework config
// (inline) carries service_name, trust_domain, env, identity, authz, limits,
// admin, discovery and server (grpc_addr/http_addr).
type Config struct {
	fconfig.Config `yaml:",inline"`

	DB               DB            `yaml:"db"`
	Valkey           Valkey        `yaml:"valkey"`
	Gateway          Gateway       `yaml:"gateway"`
	MeshEnroll       MeshEnroll    `yaml:"mesh_enroll"`
	Events           Events        `yaml:"events"`
	PlatformTenantID string        `yaml:"platform_tenant_id"`
	KEK              KEK           `yaml:"kek"`
	ObjectStore      ObjectStore   `yaml:"object_store"`
	Auth             Service       `yaml:"auth"`
	Notification     Service       `yaml:"notification"`
	Warden           Service       `yaml:"warden"`
	TaskScheduler    TaskScheduler `yaml:"task_scheduler"`
	Links            Links         `yaml:"links"`
	Limits           Limits        `yaml:"limits_signing"`
	Signing          Signing       `yaml:"signing"`
	Verify           Verify        `yaml:"verify"`
	QES              QES           `yaml:"qes"`
}

// DB configures the PostgreSQL/TimescaleDB store.
type DB struct {
	DSN        string `yaml:"dsn"`
	MigrateDSN string `yaml:"migrate_dsn"`
	MaxConns   int32  `yaml:"max_conns"`
}

// Valkey configures the platform event bus and the signing rate limiter.
type Valkey struct {
	Addresses      []string `yaml:"addresses"`
	Username       string   `yaml:"username"`
	Password       string   `yaml:"password"`
	AllowPlaintext bool     `yaml:"allow_plaintext"`
	CAFile         string   `yaml:"ca_file"`
}

// Gateway names the application gateway and the platform token issuer.
type Gateway struct {
	Service string `yaml:"service"`
	Issuer  string `yaml:"issuer"`
}

// MeshEnroll configures how the module obtains its own mesh SVID by
// enrolling with lcm over the network (identity.provider=provided).
type MeshEnroll struct {
	Enabled       bool   `yaml:"enabled"`
	EnrollURL     string `yaml:"enroll_url"`
	LCMGRPCTarget string `yaml:"lcm_grpc"`
	TenantID      string `yaml:"tenant_id"`
	TokenFile     string `yaml:"token_file"`
	StateFile     string `yaml:"state_file"`
	Insecure      bool   `yaml:"insecure"`
}

// Events toggles the platform event publisher.
type Events struct {
	Enabled bool `yaml:"enabled"`
}

// KEK names where the 32-byte key-encryption key comes from (it seals the
// tenant CA, system and administrator private keys; research D4).
type KEK struct {
	Source string `yaml:"source"` // file | env
	Path   string `yaml:"path"`
	Env    string `yaml:"env"`
}

// ObjectStore configures the S3-compatible store of PDFs and images.
type ObjectStore struct {
	Endpoint  string `yaml:"endpoint"`
	Bucket    string `yaml:"bucket"`
	Region    string `yaml:"region"`
	UseSSL    bool   `yaml:"use_ssl"`
	AccessKey string `yaml:"access_key"`
	SecretKey string `yaml:"secret_key"`
}

// Service names a module dialled over the mesh by its discovery name.
type Service struct {
	Service string `yaml:"service"`
}

// TaskScheduler configures the scheduler module integration (research D12).
// The TaskExecutor service is always served (the mesh policy admits only the
// scheduler); Enabled only controls registering the task types.
type TaskScheduler struct {
	Enabled bool   `yaml:"enabled"`
	Service string `yaml:"service"`
}

// Links builds the portal links placed in e-mails.
type Links struct {
	PortalBaseURL string `yaml:"portal_base_url"` // e.g. https://portal.example.org:8443
}

// Limits bound documents, uploads and request rates (research D10).
type Limits struct {
	MaxPDFBytes          int64 `yaml:"max_pdf_bytes"`
	MaxPDFPages          int   `yaml:"max_pdf_pages"`
	MaxFields            int   `yaml:"max_fields"`
	MaxSigners           int   `yaml:"max_signers"`
	MaxImageBytes        int64 `yaml:"max_image_bytes"`
	MaxFieldUploadBytes  int64 `yaml:"max_field_upload_bytes"`
	ParseTimeoutSeconds  int   `yaml:"parse_timeout_seconds"`
	MaxPageSize          int   `yaml:"max_page_size"`
	MaxBackupBytes       int64 `yaml:"max_backup_bytes"`
	SigningsPerMinute    int   `yaml:"signings_per_minute"`
	QESPreparationMinute int   `yaml:"qes_preparation_minutes"`
}

// Signing tunes the signing PKI and PIN protection (research D4/D5).
type Signing struct {
	CAValidityYears        int `yaml:"ca_validity_years"`
	CARenewBeforeYears     int `yaml:"ca_renew_before_years"`
	CertValidityYears      int `yaml:"cert_validity_years"`
	CRLValidityDays        int `yaml:"crl_validity_days"`
	PINMin                 int `yaml:"pin_min"`
	PINMax                 int `yaml:"pin_max"`
	PINIterations          int `yaml:"pin_iterations"`
	LockAttempts           int `yaml:"lock_attempts"`
	LockMinutes            int `yaml:"lock_minutes"`
	AuditJobMaxAttempts    int `yaml:"audit_job_max_attempts"`
	AuditJobIntervalMillis int `yaml:"audit_job_interval_ms"`
}

// Verify configures signature verification trust (research D11).
type Verify struct {
	ExtraRootsFile string `yaml:"extra_roots_file"` // PEM bundle, e.g. qualified trust service roots
	UseSystemRoots bool   `yaml:"use_system_roots"`
}

// QES configures qualified signatures through B-Trust BISS (research D6):
// the origin certificate and key (PEM files) sign BISS's "signedContents" so
// the local application can check which site asks for a signature. Both or
// neither; without them the proof is not sent.
type QES struct {
	OriginCertFile string `yaml:"origin_cert_file"`
	OriginKeyFile  string `yaml:"origin_key_file"`
}

// Default returns secure defaults on top of the Freya defaults.
func Default() Config {
	return Config{
		Config:           fconfig.Default(),
		DB:               DB{MaxConns: 16},
		Events:           Events{Enabled: true},
		Gateway:          Gateway{Service: "gateway"},
		PlatformTenantID: DefaultPlatformTenant,
		KEK:              KEK{Source: "file"},
		ObjectStore:      ObjectStore{Bucket: "signing", Region: "us-east-1", UseSSL: true},
		Auth:             Service{Service: "auth"},
		Notification:     Service{Service: "notification"},
		Warden:           Service{Service: "warden"},
		TaskScheduler:    TaskScheduler{Service: "scheduler"},
		Limits: Limits{MaxPDFBytes: 50 << 20, MaxPDFPages: 500, MaxFields: 500, MaxSigners: 50,
			MaxImageBytes: 1 << 20, MaxFieldUploadBytes: 5 << 20, ParseTimeoutSeconds: 20, MaxPageSize: 100,
			MaxBackupBytes: 2 << 30, SigningsPerMinute: 10, QESPreparationMinute: 10},
		Signing: Signing{CAValidityYears: 10, CARenewBeforeYears: 2, CertValidityYears: 2, CRLValidityDays: 7,
			PINMin: 6, PINMax: 32, PINIterations: 600000, LockAttempts: 5, LockMinutes: 15,
			AuditJobMaxAttempts: 10, AuditJobIntervalMillis: 2000},
		Verify: Verify{UseSystemRoots: true},
	}
}

// Load reads YAML over Default(); unknown fields are rejected.
func Load(path string) (Config, error) {
	cfg := Default()
	raw, err := os.ReadFile(path) // #nosec G304 -- operator-supplied config path
	if err != nil {
		return cfg, fmt.Errorf("config: %w", err)
	}
	dec := yaml.NewDecoder(bytes.NewReader(raw))
	dec.KnownFields(true)
	if err := dec.Decode(&cfg); err != nil {
		return cfg, fmt.Errorf("config: %s: %w", path, err)
	}
	return cfg, nil
}

var (
	uuidRE        = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)
	serviceNameRE = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?$`)
	bucketRE      = regexp.MustCompile(`^[a-z0-9][a-z0-9.-]{1,61}[a-z0-9]$`)
)

func within[T int | int64](v, lo, hi T) bool { return v >= lo && v <= hi }

// Validate checks the Freya config and every module section.
func (c Config) Validate() error {
	if err := c.Config.Validate(); err != nil {
		return err
	}
	for _, check := range []func() error{c.validateInfra, c.validateKeys, c.validateServices, c.validateLimits, c.validateSigning} {
		if err := check(); err != nil {
			return err
		}
	}
	return nil
}

func (c Config) validateInfra() error {
	prod := c.IsProduction()
	if c.DB.DSN == "" {
		return errors.New("config: db.dsn is required")
	}
	if prod && !strings.Contains(c.DB.DSN, "sslmode=verify-full") && !strings.Contains(c.DB.DSN, "sslmode=verify-ca") {
		return errors.New("config: db.dsn must use sslmode=verify-full (or verify-ca) in production")
	}
	if c.DB.MaxConns < 0 || c.DB.MaxConns > 256 {
		return errors.New("config: db.max_conns must be within [0, 256]")
	}
	if len(c.Valkey.Addresses) == 0 {
		return errors.New("config: valkey.addresses is required")
	}
	if prod && c.Valkey.AllowPlaintext {
		return errors.New("config: valkey.allow_plaintext is not permitted in production")
	}
	if c.Gateway.Service == "" {
		return errors.New("config: gateway.service is required")
	}
	if iu, err := url.Parse(c.Gateway.Issuer); err != nil || iu.Scheme != "https" || iu.Host == "" {
		return errors.New("config: gateway.issuer must be an https origin")
	}
	if prod && c.MeshEnroll.Enabled && c.MeshEnroll.Insecure {
		return errors.New("config: mesh_enroll.insecure is not permitted in production")
	}
	if !uuidRE.MatchString(c.PlatformTenantID) {
		return errors.New("config: platform_tenant_id must be a uuid")
	}
	o := c.ObjectStore
	if o.Endpoint == "" || strings.Contains(o.Endpoint, "/") {
		return errors.New("config: object_store.endpoint is required (host:port, no scheme)")
	}
	if !bucketRE.MatchString(o.Bucket) {
		return errors.New("config: object_store.bucket must be a valid bucket name")
	}
	if o.AccessKey == "" || o.SecretKey == "" {
		return errors.New("config: object_store.access_key and object_store.secret_key are required")
	}
	if prod && !o.UseSSL {
		return errors.New("config: object_store.use_ssl=false is not permitted in production")
	}
	if pu, err := url.Parse(c.Links.PortalBaseURL); err != nil || pu.Scheme != "https" || pu.Host == "" || (pu.Path != "" && pu.Path != "/") {
		return errors.New("config: links.portal_base_url must be an https origin")
	}
	return nil
}

func (c Config) validateKeys() error {
	switch c.KEK.Source {
	case "file":
		if c.KEK.Path == "" {
			return errors.New("config: kek.path is required for kek.source file")
		}
	case "env":
		if c.KEK.Env == "" {
			return errors.New("config: kek.env is required for kek.source env")
		}
	default:
		return errors.New("config: kek.source must be file or env")
	}
	return nil
}

func (c Config) validateServices() error {
	for name, s := range map[string]string{"auth.service": c.Auth.Service, "notification.service": c.Notification.Service,
		"warden.service": c.Warden.Service} {
		if !serviceNameRE.MatchString(s) {
			return fmt.Errorf("config: %s must be a discovery service name", name)
		}
	}
	if c.TaskScheduler.Enabled && c.TaskScheduler.Service == "" {
		return errors.New("config: task_scheduler.service is required when task_scheduler.enabled")
	}
	if c.TaskScheduler.Service != "" && !serviceNameRE.MatchString(c.TaskScheduler.Service) {
		return errors.New("config: task_scheduler.service must be a discovery service name")
	}
	return nil
}

func (c Config) validateLimits() error {
	l := c.Limits
	switch {
	case !within(l.MaxPDFBytes, 1<<20, 200<<20):
		return errors.New("config: limits_signing.max_pdf_bytes must be within [1 MiB, 200 MiB]")
	case !within(l.MaxPDFPages, 1, 5000):
		return errors.New("config: limits_signing.max_pdf_pages must be within [1, 5000]")
	case !within(l.MaxFields, 1, 5000):
		return errors.New("config: limits_signing.max_fields must be within [1, 5000]")
	case !within(l.MaxSigners, 1, 100):
		return errors.New("config: limits_signing.max_signers must be within [1, 100]")
	case !within(l.MaxImageBytes, 16<<10, 10<<20):
		return errors.New("config: limits_signing.max_image_bytes must be within [16 KiB, 10 MiB]")
	case !within(l.MaxFieldUploadBytes, 16<<10, 50<<20):
		return errors.New("config: limits_signing.max_field_upload_bytes must be within [16 KiB, 50 MiB]")
	case !within(l.ParseTimeoutSeconds, 1, 300):
		return errors.New("config: limits_signing.parse_timeout_seconds must be within [1, 300]")
	case !within(l.MaxPageSize, 1, 100):
		return errors.New("config: limits_signing.max_page_size must be within [1, 100]")
	case !within(l.MaxBackupBytes, 1<<20, 64<<30):
		return errors.New("config: limits_signing.max_backup_bytes must be within [1 MiB, 64 GiB]")
	case !within(l.SigningsPerMinute, 1, 1000):
		return errors.New("config: limits_signing.signings_per_minute must be within [1, 1000]")
	case !within(l.QESPreparationMinute, 1, 60):
		return errors.New("config: limits_signing.qes_preparation_minutes must be within [1, 60]")
	}
	return nil
}

func (c Config) validateSigning() error {
	s := c.Signing
	switch {
	case !within(s.CAValidityYears, 2, 30):
		return errors.New("config: signing.ca_validity_years must be within [2, 30]")
	case !within(s.CARenewBeforeYears, 1, s.CAValidityYears-1):
		return errors.New("config: signing.ca_renew_before_years must be at least 1 and below ca_validity_years")
	case !within(s.CertValidityYears, 1, 5):
		return errors.New("config: signing.cert_validity_years must be within [1, 5]")
	case !within(s.CRLValidityDays, 1, 90):
		return errors.New("config: signing.crl_validity_days must be within [1, 90]")
	case !within(s.PINMin, 4, 32) || !within(s.PINMax, s.PINMin, 128):
		return errors.New("config: signing.pin_min must be within [4, 32] and pin_max within [pin_min, 128]")
	case !within(s.PINIterations, 100000, 10000000):
		return errors.New("config: signing.pin_iterations must be within [100000, 10000000]")
	case !within(s.LockAttempts, 3, 20):
		return errors.New("config: signing.lock_attempts must be within [3, 20]")
	case !within(s.LockMinutes, 1, 1440):
		return errors.New("config: signing.lock_minutes must be within [1, 1440]")
	case !within(s.AuditJobMaxAttempts, 1, 100):
		return errors.New("config: signing.audit_job_max_attempts must be within [1, 100]")
	case !within(s.AuditJobIntervalMillis, 100, 60000):
		return errors.New("config: signing.audit_job_interval_ms must be within [100, 60000]")
	case (c.QES.OriginCertFile == "") != (c.QES.OriginKeyFile == ""):
		return errors.New("config: qes.origin_cert_file and qes.origin_key_file are set together")
	}
	return nil
}

// Warnings lists accepted insecure opt-outs (surfaced at start).
func (c Config) Warnings() []string {
	w := c.Config.Warnings()
	if c.Valkey.AllowPlaintext {
		w = append(w, "valkey.allow_plaintext: event-bus traffic without TLS (development only)")
	}
	if c.MeshEnroll.Enabled && c.MeshEnroll.Insecure {
		w = append(w, "mesh_enroll.insecure: SVID enrollment without TLS (development only)")
	}
	if !c.ObjectStore.UseSSL {
		w = append(w, "object_store.use_ssl=false: document traffic without TLS (development only)")
	}
	if !c.Events.Enabled {
		w = append(w, "events.enabled=false: other modules and the UI receive no signing events")
	}
	if !c.TaskScheduler.Enabled {
		w = append(w, "task_scheduler.enabled=false: submissions never expire and no reminders are sent")
	}
	return w
}

// ParseTimeout bounds opening one PDF.
func (c Config) ParseTimeout() time.Duration {
	return time.Duration(c.Limits.ParseTimeoutSeconds) * time.Second
}

// QESPreparationTTL is how long a prepared qualified signature stays usable.
func (c Config) QESPreparationTTL() time.Duration {
	return time.Duration(c.Limits.QESPreparationMinute) * time.Minute
}

// LockDuration is how long a certificate stays locked after too many wrong PINs.
func (c Config) LockDuration() time.Duration {
	return time.Duration(c.Signing.LockMinutes) * time.Minute
}

// AuditJobInterval is the audit-trail job worker poll interval.
func (c Config) AuditJobInterval() time.Duration {
	return time.Duration(c.Signing.AuditJobIntervalMillis) * time.Millisecond
}
