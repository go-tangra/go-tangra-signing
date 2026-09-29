// Package app wires the signing service: configuration -> Freya runtime (mesh
// identity, admin listener) -> store/audit/events/metrics/object store/module
// KEK -> the domain services -> the mesh HTTP surface (reached only through the
// gateway) and the scheduler task executor (module-to-module), plus gateway
// registration and permission seeding. It refuses to start without a store,
// an event bus, an object store, a KEK and a gateway issuer (config.Validate).
package app

import (
	"context"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"strings"
	"time"

	"github.com/go-tangra/go-tangra-lcm/sdk/v4/pkg/lcmidentity"
	"github.com/go-tangra/go-tangra/v4"

	authv1 "github.com/go-tangra/go-tangra-auth/sdk/v4/api/proto/auth/v1"
	"github.com/go-tangra/go-tangra-auth/sdk/v4/pkg/authclient"
	"github.com/go-tangra/go-tangra-portal/sdk/v4/pkg/gatewayclient"

	"github.com/go-tangra/go-tangra-signing/v4/internal/audit"
	"github.com/go-tangra/go-tangra-signing/v4/internal/authz"
	"github.com/go-tangra/go-tangra-signing/v4/internal/blob"
	"github.com/go-tangra/go-tangra-signing/v4/internal/config"
	"github.com/go-tangra/go-tangra-signing/v4/internal/events"
	"github.com/go-tangra/go-tangra-signing/v4/internal/httpapi"
	"github.com/go-tangra/go-tangra-signing/v4/internal/metrics"
	"github.com/go-tangra/go-tangra-signing/v4/internal/repo"
	"github.com/go-tangra/go-tangra-signing/v4/internal/repo/repodb"
	"github.com/go-tangra/go-tangra-signing/v4/internal/sealed"
	"github.com/go-tangra/go-tangra-signing/v4/internal/store"
	"github.com/go-tangra/go-tangra-signing/v4/internal/stream"
	"github.com/go-tangra/go-tangra-signing/v4/internal/stream/valkeykv"
	"github.com/go-tangra/go-tangra-signing/v4/pkg/signingmanifest"
)

// Options override infrastructure (tests) and attach optional parts.
type Options struct {
	Logger   slog.Handler
	Verifier httpapi.Verifier
	Checker  authz.Checker    // API-permission checker override (default: auth Authorization/Check)
	Repo     repo.Store       // store override (tests: memstore); skips the DB
	Stream   stream.Client    // event-bus client override (tests: stream.NewMemory())
	Blob     blob.Store       // object store override (tests: blob.NewFake())
	KEK      []byte           // key-encryption key override (tests)
	Now      func() time.Time // clock override (tests)
	Freya    []freya.Option
	Migrate  bool
	Remote   fs.FS // built federated UI remote (nil serves no remote)
}

// App is the wired service.
type App struct {
	Cfg      config.Config
	Log      *slog.Logger
	Freya    *freya.App
	Store    *store.Store
	Repo     repo.Store
	Audit    *audit.Writer
	Verifier httpapi.Verifier
	Checker  authz.Checker
	Hub      *stream.Hub
	Limiter  *stream.Limiter
	Events   events.Emitter
	Metrics  *metrics.Metrics
	Blob     blob.Store
	Sealer   *sealed.Envelope
	HTTP     *httpapi.Server
	Now      func() time.Time

	workers []func(context.Context)
	closers []func()
}

// Build wires the service.
func Build(ctx context.Context, cfg config.Config, o Options) (a *App, err error) {
	a = &App{Cfg: cfg}
	handler := o.Logger
	if handler == nil {
		handler = slog.NewJSONHandler(os.Stderr, nil)
	}
	a.Log = slog.New(handler)
	built := a
	defer func() { // release what was opened when wiring fails half-way
		if err != nil {
			built.Close()
		}
	}()
	a.Now = o.Now
	if a.Now == nil {
		a.Now = func() time.Time { return time.Now().UTC() }
	}
	if err = a.buildRuntime(ctx, cfg, handler, o.Freya); err != nil {
		return nil, err
	}
	if err = a.buildStorage(ctx, cfg, o); err != nil {
		return nil, err
	}
	if err = a.buildKeys(ctx, cfg, o); err != nil {
		return nil, err
	}
	if err = a.buildPeers(ctx, cfg, o); err != nil {
		return nil, err
	}
	if err = a.buildEvents(cfg, o.Stream); err != nil {
		return nil, err
	}
	if a.Metrics, err = metrics.New(a.Freya.Metrics().Meter(metrics.Scope), nil); err != nil {
		return nil, fmt.Errorf("metrics: %w", err)
	}

	// Mesh HTTP surface (reached only through the gateway).
	hopts := []httpapi.Option{httpapi.WithVerifier(a.Verifier), httpapi.WithChecker(a.Checker), httpapi.WithLogger(a.Log)}
	if o.Remote != nil {
		hopts = append(hopts, httpapi.WithRemote(o.Remote))
	}
	if a.HTTP, err = httpapi.NewHandler(a.Freya, hopts...); err != nil {
		return nil, err
	}
	a.HTTP.Register(httpapi.Deps{Hub: a.Hub, Health: a.health})
	a.Freya.HTTP().HandlePrefix("/", a.HTTP.Handler())
	return a, nil
}

func (a *App) buildRuntime(ctx context.Context, cfg config.Config, handler slog.Handler, extra []freya.Option) error {
	fopts := append([]freya.Option{freya.WithLogger(handler)}, extra...)
	if cfg.MeshEnroll.Enabled {
		raw, err := os.ReadFile(cfg.MeshEnroll.TokenFile)
		if err != nil {
			return fmt.Errorf("signing: mesh enroll token: %w", err)
		}
		prov, err := lcmidentity.NewNet(ctx, lcmidentity.NetConfig{
			EnrollURL: cfg.MeshEnroll.EnrollURL, LCMGRPCTarget: cfg.MeshEnroll.LCMGRPCTarget,
			TenantID: cfg.MeshEnroll.TenantID, TrustDomain: cfg.Config.TrustDomain, ServiceName: cfg.Config.ServiceName,
			EnrollmentToken: strings.TrimSpace(string(raw)), Insecure: cfg.MeshEnroll.Insecure, StateFile: cfg.MeshEnroll.StateFile,
		})
		if err != nil {
			return fmt.Errorf("signing: mesh enroll: %w", err)
		}
		a.closers = append(a.closers, func() { _ = prov.Close() })
		fopts = append(fopts, freya.WithIdentityProvider(prov))
	}
	f, err := freya.New(cfg.Config, fopts...)
	if err != nil {
		return err
	}
	a.Freya = f
	a.closers = append(a.closers, f.Close)
	return nil
}

func (a *App) buildStorage(ctx context.Context, cfg config.Config, o Options) (err error) {
	a.Repo = o.Repo
	if a.Repo == nil {
		if o.Migrate {
			mdsn := cfg.DB.MigrateDSN
			if mdsn == "" {
				mdsn = cfg.DB.DSN
			}
			if err = store.Migrate(ctx, mdsn); err != nil {
				return err
			}
		}
		if a.Store, err = store.Open(ctx, cfg.DB.DSN, cfg.DB.MaxConns); err != nil {
			return err
		}
		a.closers = append(a.closers, a.Store.Close)
		a.Repo = repodb.New(a.Store)
	}
	a.Audit = audit.NewWriter(a.Repo, func(err error) { a.Log.Error("audit write failed", "err", err) })
	a.closers = append(a.closers, a.Audit.Close)
	a.Blob = o.Blob
	if a.Blob == nil {
		oc := cfg.ObjectStore
		if a.Blob, err = blob.New(blob.Config{Endpoint: oc.Endpoint, Bucket: oc.Bucket, Region: oc.Region, AccessKey: oc.AccessKey,
			SecretKey: oc.SecretKey, UseSSL: oc.UseSSL}); err != nil {
			return fmt.Errorf("object store: %w", err)
		}
	}
	bctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	if err := a.Blob.EnsureBucket(bctx); err != nil {
		// Not fatal: the store may come up after the module; uploads fail until it does.
		a.Log.Warn("object store: bucket not ready", "err", err)
	}
	return nil
}

// buildKeys loads the module KEK (it seals the tenant CA, audit-trail and
// administrator private keys).
func (a *App) buildKeys(_ context.Context, cfg config.Config, o Options) (err error) {
	kek := o.KEK
	if kek == nil {
		if kek, err = sealed.LoadKEK(cfg.KEK.Source, cfg.KEK.Path, cfg.KEK.Env); err != nil {
			return fmt.Errorf("kek: %w", err)
		}
	}
	if a.Sealer, err = sealed.NewEnvelope(kek); err != nil {
		return fmt.Errorf("kek: %w", err)
	}
	return nil
}

// buildPeers wires what the module asks auth: the platform-token verifier and
// the permission checker. The connection is dialled lazily by the framework,
// so the module starts while auth is down.
func (a *App) buildPeers(ctx context.Context, cfg config.Config, o Options) error {
	a.Verifier, a.Checker = o.Verifier, o.Checker
	if a.Verifier == nil || a.Checker == nil {
		conn, err := a.Freya.Client(ctx, cfg.Auth.Service)
		if err != nil {
			return fmt.Errorf("auth client: %w", err)
		}
		if a.Verifier == nil {
			a.Verifier = authclient.New(authclient.Config{Issuer: cfg.Gateway.Issuer},
				authclient.GRPCKeys{Client: authv1.NewKeysClient(conn)},
				authclient.GRPCRevocations{Client: authv1.NewSessionsClient(conn)})
		}
		if a.Checker == nil {
			a.Checker = AuthPerms{Client: authv1.NewAuthorizationClient(conn)}
		}
	}
	return nil
}

func (a *App) buildEvents(cfg config.Config, client stream.Client) error {
	if client == nil {
		sc := valkeykv.Config{Addresses: cfg.Valkey.Addresses, Username: cfg.Valkey.Username, Password: cfg.Valkey.Password, AllowPlaintext: cfg.Valkey.AllowPlaintext}
		if cfg.Valkey.CAFile != "" {
			pem, err := os.ReadFile(cfg.Valkey.CAFile)
			if err != nil {
				return fmt.Errorf("valkey ca: %w", err)
			}
			sc.CAPEM = pem
		}
		c, err := valkeykv.New(sc)
		if err != nil {
			return fmt.Errorf("event bus: %w", err)
		}
		client = c
	}
	a.Hub = stream.NewHub(client, stream.Config{}, a.Log)
	a.closers = append(a.closers, a.Hub.Close)
	a.Limiter = stream.NewLimiter(client)
	if cfg.Events.Enabled {
		a.Events = events.Emitter{Pub: events.HubPublisher{Hub: a.Hub}}
	}
	return nil
}

// health reports component reachability for /health.
func (a *App) health() map[string]string {
	out := map[string]string{"store": "ok"}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if a.Store != nil {
		if err := a.Store.Ping(ctx); err != nil {
			out["store"] = "unreachable"
		}
	}
	return out
}

// Run starts the verifier, gateway registration, permission seeding, the
// background workers and the Freya runtime.
func (a *App) Run(ctx context.Context) error {
	wctx, cancel := context.WithCancel(ctx)
	defer cancel()
	if v, ok := a.Verifier.(*authclient.Verifier); ok {
		go func() {
			for wctx.Err() == nil {
				if err := v.Start(wctx, func(err error) { a.Log.Warn("verifier", "err", err) }); err == nil {
					return
				}
				select {
				case <-wctx.Done():
					return
				case <-time.After(2 * time.Second):
				}
			}
		}()
	}
	go a.register(wctx)
	done := make(chan struct{}, len(a.workers))
	for _, w := range a.workers {
		w := w
		go func() {
			defer func() { done <- struct{}{} }()
			w(wctx)
		}()
	}
	go func() {
		for wctx.Err() == nil && !a.Freya.Ready() {
			time.Sleep(100 * time.Millisecond)
		}
		a.seedLoop(wctx)
	}()
	err := a.Freya.Run(ctx)
	cancel()
	for range a.workers {
		<-done
	}
	return err
}

// Close releases resources (idempotent).
func (a *App) Close() {
	for i := len(a.closers) - 1; i >= 0; i-- {
		a.closers[i]()
	}
	a.closers = nil
}

// register keeps the gateway lease for the manifest.
func (a *App) register(ctx context.Context) {
	for ctx.Err() == nil && !a.Freya.Ready() {
		select {
		case <-ctx.Done():
			return
		case <-time.After(100 * time.Millisecond):
		}
	}
	man, err := signingmanifest.Manifest()
	if err != nil {
		a.Log.Error("gateway manifest", "err", err)
		return
	}
	httpEP, err := a.Freya.HTTP().Endpoint()
	if err != nil {
		a.Log.Error("gateway registration: http endpoint", "err", err)
		return
	}
	grpcEP, err := a.Freya.GRPC().Endpoint()
	if err != nil {
		a.Log.Error("gateway registration: grpc endpoint", "err", err)
		return
	}
	var client *gatewayclient.Client
	for ctx.Err() == nil && client == nil {
		conn, cerr := a.Freya.Client(ctx, a.Cfg.Gateway.Service)
		if cerr != nil {
			select {
			case <-ctx.Done():
				return
			case <-time.After(2 * time.Second):
			}
			continue
		}
		client, err = gatewayclient.New(conn, gatewayclient.Options{Manifest: man, HTTPURL: "https://" + httpEP.Host, GRPCTarget: grpcEP.Host, Logger: a.Log,
			OnState: func(s gatewayclient.State) {
				a.Log.Info("gateway lease", "registered", s.Registered, "lease", s.LeaseID, "err", s.Err)
			}})
		if err != nil {
			a.Log.Error("gateway client", "err", err)
			return
		}
	}
	if client != nil {
		if err := client.Run(ctx); err != nil {
			a.Log.Error("gateway registration", "err", err)
		}
	}
}
