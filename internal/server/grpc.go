package server

import (
	"context"

	"github.com/go-kratos/kratos/v2/middleware"
	"github.com/go-kratos/kratos/v2/middleware/logging"
	"github.com/go-kratos/kratos/v2/middleware/metadata"
	"github.com/go-kratos/kratos/v2/middleware/recovery"
	"github.com/go-kratos/kratos/v2/middleware/tracing"
	"github.com/go-kratos/kratos/v2/middleware/validate"
	"github.com/go-kratos/kratos/v2/transport/grpc"
	"github.com/tx7do/kratos-bootstrap/bootstrap"

	signingV1 "github.com/go-tangra/go-tangra-signing/gen/go/signing/service/v1"
	"github.com/go-tangra/go-tangra-signing/internal/cert"
	"github.com/go-tangra/go-tangra-signing/internal/service"

	appViewer "github.com/go-tangra/go-tangra-signing/pkg/viewer"
	"github.com/go-tangra/go-tangra-common/middleware/mtls"
)

// systemViewerMiddleware injects system viewer context for all requests
func systemViewerMiddleware() middleware.Middleware {
	return func(handler middleware.Handler) middleware.Handler {
		return func(ctx context.Context, req interface{}) (interface{}, error) {
			ctx = appViewer.NewSystemViewerContext(ctx)
			return handler(ctx, req)
		}
	}
}

// NewGRPCServer creates a gRPC server with mTLS and audit logging
func NewGRPCServer(
	ctx *bootstrap.Context,
	certManager *cert.CertManager,
	templateSvc *service.TemplateService,
	submissionSvc *service.SubmissionService,
	signingSvc *service.SigningService,
	certificateSvc *service.CertificateService,
	userSvc *service.UserService,
	sessionSvc *service.SessionService,
) *grpc.Server {
	cfg := ctx.GetConfig()
	l := ctx.NewLoggerHelper("signing/grpc")

	var opts []grpc.ServerOption

	// Get gRPC server configuration
	if cfg.Server != nil && cfg.Server.Grpc != nil {
		if cfg.Server.Grpc.Network != "" {
			opts = append(opts, grpc.Network(cfg.Server.Grpc.Network))
		}
		if cfg.Server.Grpc.Addr != "" {
			opts = append(opts, grpc.Address(cfg.Server.Grpc.Addr))
		}
		if cfg.Server.Grpc.Timeout != nil {
			opts = append(opts, grpc.Timeout(cfg.Server.Grpc.Timeout.AsDuration()))
		}
	}

	// Configure TLS if certificates are available
	if certManager != nil && certManager.IsTLSEnabled() {
		tlsConfig, err := certManager.GetServerTLSConfig()
		if err != nil {
			l.Warnf("Failed to get TLS config, running without TLS: %v", err)
		} else {
			opts = append(opts, grpc.TLSConfig(tlsConfig))
			l.Info("gRPC server configured with mTLS")
		}
	} else {
		l.Warn("TLS not enabled, running without mTLS")
	}

	// Add middleware
	var ms []middleware.Middleware
	ms = append(ms, recovery.Recovery())
	ms = append(ms, systemViewerMiddleware())
	ms = append(ms, tracing.Server())
	ms = append(ms, metadata.Server())
	ms = append(ms, logging.Server(ctx.GetLogger()))

	// Add mTLS middleware — session endpoints are public (no auth, token-based)
	ms = append(ms, mtls.MTLSMiddleware(
		ctx.GetLogger(),
		mtls.WithPublicEndpoints(
			"/grpc.health.v1.Health/Check",
			"/grpc.health.v1.Health/Watch",
			"/signing.service.v1.SigningSessionService/GetSigningSession",
			"/signing.service.v1.SigningSessionService/SubmitSigning",
			"/signing.service.v1.SigningSessionService/PrepareForBissSigning",
			"/signing.service.v1.SigningSessionService/CompleteBissSigning",
			"/signing.service.v1.SigningSessionService/GetCertificateSetup",
			"/signing.service.v1.SigningSessionService/CompleteCertificateSetup",
		),
	))

	ms = append(ms, validate.Validator())

	opts = append(opts, grpc.Middleware(ms...))

	// Create gRPC server
	srv := grpc.NewServer(opts...)

	// Register services
	signingV1.RegisterSigningTemplateServiceServer(srv, templateSvc)
	signingV1.RegisterSigningSubmissionServiceServer(srv, submissionSvc)
	signingV1.RegisterSigningDocumentServiceServer(srv, signingSvc)
	signingV1.RegisterSigningCertificateServiceServer(srv, certificateSvc)
	signingV1.RegisterSigningUserServiceServer(srv, userSvc)
	signingV1.RegisterSigningSessionServiceServer(srv, sessionSvc)

	return srv
}
