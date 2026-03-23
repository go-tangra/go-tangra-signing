//go:build wireinject
// +build wireinject

//go:generate go run github.com/google/wire/cmd/wire

package providers

import (
	"github.com/google/wire"

	"github.com/go-tangra/go-tangra-signing/internal/data"
)

// ProviderSet is the Wire provider set for data layer
var ProviderSet = wire.NewSet(
	data.NewRedisClient,
	data.NewEntClient,
	data.NewStorageClient,
	data.NewTemplateRepo,
	data.NewSubmissionRepo,
	data.NewSubmitterRepo,
	data.NewCertificateRepo,
	data.NewEventRepo,
)
