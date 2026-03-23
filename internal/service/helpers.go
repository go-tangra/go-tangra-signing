package service

import (
	"github.com/go-tangra/go-tangra-common/grpcx"
	"github.com/google/uuid"
)

// Context helpers — delegate to go-tangra-common/grpcx which extracts
// tenant ID, user ID, etc. from gRPC incoming metadata set by the
// admin-service gateway (x-md-global-tenant-id, x-md-global-user-id, etc.).
var (
	getTenantIDFromContext = grpcx.GetTenantIDFromContext
	getUserIDFromContext   = grpcx.GetUserIDFromContext
	getUserIDAsUint32      = grpcx.GetUserIDAsUint32
	getUsernameFromContext = grpcx.GetUsernameFromContext
)

// generateUUID generates a new UUID v4.
func generateUUID() string {
	return uuid.New().String()
}

// generateSlug generates a URL-friendly unique slug.
func generateSlug() string {
	return uuid.New().String()[:8]
}

// derefUint32 dereferences a *uint32 pointer, returning 0 if nil.
func derefUint32(p *uint32) uint32 {
	if p == nil {
		return 0
	}
	return *p
}
