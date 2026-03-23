package main

import (
	"context"
	"time"

	"github.com/go-kratos/kratos/v2"
	"github.com/go-kratos/kratos/v2/transport/grpc"
	kratosHttp "github.com/go-kratos/kratos/v2/transport/http"

	conf "github.com/tx7do/kratos-bootstrap/api/gen/go/conf/v1"
	"github.com/tx7do/kratos-bootstrap/bootstrap"

	"github.com/go-tangra/go-tangra-common/registration"
	"github.com/go-tangra/go-tangra-common/service"
	"github.com/go-tangra/go-tangra-signing/cmd/server/assets"
)

var (
	// Module info
	moduleID    = "signing"
	moduleName  = "Signing"
	version     = "1.0.0"
	description = "Document signing service with PKI, templates and multi-signer workflows"
)

var globalRegHelper *registration.RegistrationHelper


func newApp(
	ctx *bootstrap.Context,
	gs *grpc.Server,
	hs *kratosHttp.Server,
) *kratos.App {
	globalRegHelper = registration.StartRegistration(ctx, ctx.GetLogger(), &registration.Config{
		ModuleID:          moduleID,
		ModuleName:        moduleName,
		Version:           version,
		Description:       description,
		GRPCEndpoint:      registration.GetGRPCAdvertiseAddr(ctx, "0.0.0.0:10400"),
		AdminEndpoint:     registration.GetEnvOrDefault("ADMIN_GRPC_ENDPOINT", ""),
		FrontendEntryUrl:  registration.GetEnvOrDefault("FRONTEND_ENTRY_URL", ""),
		HttpEndpoint:      registration.GetEnvOrDefault("HTTP_ADVERTISE_ADDR", ""),
		OpenapiSpec:       assets.OpenApiData,
		ProtoDescriptor:   assets.DescriptorData,
		MenusYaml:         assets.MenusData,
		HeartbeatInterval: 30 * time.Second,
		RetryInterval:     5 * time.Second,
		MaxRetries:        60,
	})

	return bootstrap.NewApp(ctx, gs, hs)
}

func runApp() error {
	ctx := bootstrap.NewContext(
		context.Background(),
		&conf.AppInfo{
			Project: service.Project,
			AppId:   "signing.service",
			Version: version,
		},
	)

	// Ensure registration cleanup on exit
	defer globalRegHelper.Stop()

	return bootstrap.RunApp(ctx, initApp)
}

func main() {
	if err := runApp(); err != nil {
		panic(err)
	}
}
 
 
 
 
 
 
 
 
 
 
 
 
 
 
 
 
