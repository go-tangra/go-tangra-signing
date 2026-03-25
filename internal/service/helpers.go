package service

import (
	"context"
	"fmt"
	"strings"

	"github.com/go-tangra/go-tangra-common/grpcx"
	"github.com/google/uuid"

	signingV1 "github.com/go-tangra/go-tangra-signing/gen/go/signing/service/v1"
)

// Context helpers — delegate to go-tangra-common/grpcx which extracts
// tenant ID, user ID, etc. from gRPC incoming metadata set by the
// admin-service gateway (x-md-global-tenant-id, x-md-global-user-id, etc.).
var (
	getTenantIDFromContext  = grpcx.GetTenantIDFromContext
	getUserIDFromContext    = grpcx.GetUserIDFromContext
	getUserIDAsUint32      = grpcx.GetUserIDAsUint32
	getUsernameFromContext = grpcx.GetUsernameFromContext
	getRolesFromContext    = grpcx.GetRolesFromContext
)

// isSigningUser returns true if the caller has the signing:user role
// but NOT platform:admin or tenant:manager (i.e., limited access user).
func isSigningUser(ctx context.Context) bool {
	roles := getRolesFromContext(ctx)
	for _, r := range roles {
		if r == "platform:admin" || r == "tenant:manager" {
			return false
		}
	}
	for _, r := range roles {
		if r == "signing:user" {
			return true
		}
	}
	return false
}

// generateUUID generates a new UUID v4.
func generateUUID() string {
	return uuid.New().String()
}

// derefUint32 dereferences a *uint32 pointer, returning 0 if nil.
func derefUint32(p *uint32) uint32 {
	if p == nil {
		return 0
	}
	return *p
}

// validateStorageKey checks that a storage key belongs to the given tenant
// and matches allowed path patterns. Prevents path traversal and cross-tenant access.
func validateStorageKey(key string, tenantID uint32) error {
	if strings.Contains(key, "..") {
		return signingV1.ErrorBadRequest("invalid document key")
	}

	// Key must start with "{tenantID}/"
	prefix := fmt.Sprintf("%d/", tenantID)
	if !strings.HasPrefix(key, prefix) {
		return signingV1.ErrorAccessDenied("access denied")
	}

	return nil
}
