//go:build wireinject
// +build wireinject

//go:generate go run github.com/google/wire/cmd/wire

package providers

import (
	"github.com/google/wire"

	"github.com/go-tangra/go-tangra-signing/internal/client"
	"github.com/go-tangra/go-tangra-signing/internal/event"
	"github.com/go-tangra/go-tangra-signing/internal/service"
)

// ProviderSet is the Wire provider set for service layer
var ProviderSet = wire.NewSet(
	service.NewTemplateService,
	service.NewSubmissionService,
	service.NewSigningService,
	service.NewCertificateService,
	service.NewUserService,
	service.NewPDFGenerator,
	service.NewSessionService,
	event.NewPublisher,
	client.NewAdminClient,
	client.NewRegistrationClient,
	client.NewModuleDialer,
	client.NewNotificationClient,
)
