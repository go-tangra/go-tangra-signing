package app

import (
	"context"

	"google.golang.org/grpc"

	schedulerv1 "github.com/go-tangra/go-tangra-scheduler/sdk/v4/api/proto/scheduler/v1"
	"github.com/go-tangra/go-tangra-scheduler/sdk/v4/pkg/schedulerclient"
	sdktask "github.com/go-tangra/go-tangra-scheduler/sdk/v4/pkg/taskexec"
	"github.com/go-tangra/go-tangra/v4/authn"

	signingv1 "github.com/go-tangra/go-tangra-signing/sdk/v4/api/proto/signing/v1"
	"github.com/go-tangra/go-tangra-signing/v4/internal/grpcapi"
	"github.com/go-tangra/go-tangra-signing/v4/internal/tasks"
)

// wireScheduler serves scheduler.v1.TaskExecutor (always: the mesh policy
// admits only svc/scheduler and the SDK server re-checks the peer) and, when
// task_scheduler.enabled, keeps signing's task types registered with the
// scheduler (research D12).
func (a *App) wireScheduler() {
	cfg := a.Cfg
	a.Tasks = &tasks.Runner{Store: a.Repo, Blob: a.Blob, PKI: a.PKI, Subs: a.Submissions, Mail: a.Mail, Events: a.Events,
		Audit: a.Audit, Log: a.Log, Now: a.Now}
	scheduler := cfg.TaskScheduler.Service
	if scheduler == "" {
		scheduler = sdktask.DefaultScheduler
	}
	schedulerv1.RegisterTaskExecutorServer(a.Freya.GRPC(), sdktask.NewServer(a.Tasks.Handlers(), sdktask.Options{
		Caller: schedulerCaller(cfg.Config.TrustDomain), Scheduler: scheduler, Platform: tasks.Platform(), Log: a.Log,
	}))
	if !cfg.TaskScheduler.Enabled {
		return
	}
	reg := &schedulerclient.Registrar{
		Dial: func(ctx context.Context) (grpc.ClientConnInterface, error) {
			return a.Freya.Client(ctx, cfg.TaskScheduler.Service)
		},
		Types: tasks.Descriptors(),
		Log:   a.Log,
	}
	a.workers = append(a.workers, reg.Run)
}

// schedulerCaller returns the verified peer's service name, only for a peer
// of signing's own trust domain.
func schedulerCaller(trustDomain string) func(ctx context.Context) (string, bool) {
	return func(ctx context.Context) (string, bool) {
		p, ok := authn.FromContext(ctx)
		if !ok || p.ID.TrustDomain() != trustDomain {
			return "", false
		}
		return p.ID.ServiceName(), true
	}
}

// wireModuleAPI serves signing.v1.ModuleSubmissions (feature 028): the mesh
// policy admits only svc/hr, and the server re-checks the peer's trust domain
// and service name.
func (a *App) wireModuleAPI() {
	signingv1.RegisterModuleSubmissionsServer(a.Freya.GRPC(), &grpcapi.Server{Subs: a.Submissions, Caller: moduleCaller(a.Cfg.Config.TrustDomain)})
}

// moduleCaller returns the verified peer of a module API call, only for a
// peer of signing's own trust domain.
func moduleCaller(trustDomain string) func(ctx context.Context) (grpcapi.Peer, bool) {
	return func(ctx context.Context) (grpcapi.Peer, bool) {
		p, ok := authn.FromContext(ctx)
		if !ok || p.ID.TrustDomain() != trustDomain {
			return grpcapi.Peer{}, false
		}
		return grpcapi.Peer{Service: p.ID.ServiceName(), SPIFFEID: p.ID.String()}, true
	}
}
