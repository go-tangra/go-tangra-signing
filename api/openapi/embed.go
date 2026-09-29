// Package openapi embeds the signing browser API contract.
package openapi

import _ "embed"

// Signing is the OpenAPI 3.1 document served and validated by the service.
//
//go:embed signing.yaml
var Signing []byte
