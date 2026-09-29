// Package authz is the signing module's access model (research D7). Every
// browser route declares one API permission (x-freya-permission) or "member"
// (any signed-in user of the tenant); the module enforces it itself (defence
// in depth behind the gateway) through a Checker (the auth service's
// Authorization/Check). A caller acts within its own tenant.
//
// signing:sign is granted to every built-in tenant role (members included):
// it opens the signer pages, the inbox and the caller's own certificate, and
// the service then checks the relationship. Two relationships are checked in
// code on top of the permissions:
//   - participant: the caller is a signer of the submission — they may view it,
//     sign or decline their own slot and download the document (FR-017);
//   - sender: the caller created the submission — they may cancel, resend,
//     replace signers and delete it without submissions:manage (FR-015).
//
// Platform administrators additionally act on every tenant for administration
// and backup (FR-047). Everybody else gets "not found" for foreign rows.
package authz

import (
	"context"
	"errors"
	"fmt"
	"strings"
)

// ErrForbidden is returned when a caller lacks the required permission or scope.
var ErrForbidden = errors.New("authz: forbidden")

// Actor kinds (closed set; the audit vocabulary uses the same names).
const (
	ActorUser    = "user"    // a signed-in platform user (via the gateway)
	ActorService = "service" // a mesh peer (SPIFFE)
	ActorSystem  = "system"  // scheduled task types / trusted internal maintenance
)

// API permissions (resource:action).
const (
	SigningSign        = "signing:sign"
	SigningRead        = "signing:read"
	TemplatesManage    = "templates:manage"
	SubmissionsCreate  = "submissions:create"
	SubmissionsManage  = "submissions:manage"
	CertificatesManage = "certificates:manage"
	BackupManage       = "backup:manage"
)

// Permissions lists every module permission.
var Permissions = []string{SigningSign, SigningRead, TemplatesManage, SubmissionsCreate, SubmissionsManage, CertificatesManage, BackupManage}

// RolePlatformAdmin confers the cross-tenant scope.
const RolePlatformAdmin = "platform-admin"

// Subjects is the authenticated caller.
type Subjects struct {
	TenantID  string
	UserID    string
	Roles     []string
	ActorKind string // user | service | system
}

// Checker answers "does this user hold this API permission in this tenant". A
// failed lookup is a "no".
type Checker interface {
	Has(ctx context.Context, tenantID, userID, permission string) bool
}

// CheckerFunc adapts a function to Checker.
type CheckerFunc func(ctx context.Context, tenantID, userID, permission string) bool

// Has implements Checker.
func (f CheckerFunc) Has(ctx context.Context, tenantID, userID, permission string) bool {
	return f(ctx, tenantID, userID, permission)
}

// Static is a Checker over a fixed user → permissions map (tests, dev).
type Static map[string][]string

// Has implements Checker.
func (s Static) Has(_ context.Context, _, userID, permission string) bool {
	for _, p := range s[userID] {
		if p == permission {
			return true
		}
	}
	return false
}

// User returns the subject of a signed-in user.
func User(tenantID, userID string, roles []string) Subjects {
	return Subjects{TenantID: tenantID, UserID: userID, Roles: roles, ActorKind: ActorUser}
}

// System returns the trusted internal subject.
func System() Subjects { return Subjects{UserID: ActorSystem, ActorKind: ActorSystem} }

// Service returns the subject of a mesh peer.
func Service(spiffeID string) Subjects { return Subjects{UserID: spiffeID, ActorKind: ActorService} }

// HasRole reports whether the caller carries role.
func (s Subjects) HasRole(role string) bool {
	for _, r := range s.Roles {
		if r == role {
			return true
		}
	}
	return false
}

// IsPlatformAdmin reports whether the caller has the cross-tenant scope: a
// user with the platform-admin role, or the system subject.
func (s Subjects) IsPlatformAdmin() bool {
	return s.ActorKind == ActorSystem || (s.ActorKind == ActorUser && s.HasRole(RolePlatformAdmin))
}

// IsMember reports whether the caller is a signed-in user of a tenant.
func (s Subjects) IsMember() bool {
	return s.ActorKind == ActorUser && s.TenantID != "" && s.UserID != ""
}

// ActorID is the user id, falling back to the actor kind.
func (s Subjects) ActorID() string {
	if s.UserID != "" {
		return s.UserID
	}
	return s.ActorKind
}

// Known reports whether perm is a module permission.
func Known(perm string) bool {
	for _, p := range Permissions {
		if p == perm {
			return true
		}
	}
	return false
}

// Require checks that the caller holds perm. Users are checked through c in
// their own tenant (nil c refuses); the system subject is allowed everything;
// services hold no browser permission.
func Require(ctx context.Context, c Checker, s Subjects, perm string) error {
	if !Known(perm) {
		return fmt.Errorf("%w: unknown permission %q", ErrForbidden, perm)
	}
	switch s.ActorKind {
	case ActorSystem:
		return nil
	case ActorUser:
		if c != nil && s.IsMember() && c.Has(ctx, s.TenantID, s.UserID, perm) {
			return nil
		}
	}
	return fmt.Errorf("%w: %s required", ErrForbidden, perm)
}

// Allowed is Require as a boolean.
func Allowed(ctx context.Context, c Checker, s Subjects, perm string) bool {
	return Require(ctx, c, s, perm) == nil
}

// RequirePlatformAdmin permits only platform administrators (or the system).
func RequirePlatformAdmin(s Subjects) error {
	if s.IsPlatformAdmin() {
		return nil
	}
	return fmt.Errorf("%w: platform-admin required", ErrForbidden)
}

// CanSeeTenant reports whether rows of tenantID are visible to the caller.
func CanSeeTenant(s Subjects, tenantID string) bool {
	return s.IsPlatformAdmin() || (s.TenantID != "" && s.TenantID == tenantID)
}

// IsSender reports whether the caller created the submission.
func IsSender(s Subjects, createdBy string) bool {
	return s.IsMember() && createdBy != "" && s.UserID == createdBy
}

// IsParticipant reports whether the caller is one of the submission's signers.
func IsParticipant(s Subjects, signerUserIDs []string) bool {
	if !s.IsMember() {
		return false
	}
	for _, id := range signerUserIDs {
		if id == s.UserID {
			return true
		}
	}
	return false
}

// CanControlSubmission reports whether the caller may cancel, resend, replace
// signers of, or delete a submission: its sender, or submissions:manage.
func CanControlSubmission(ctx context.Context, c Checker, s Subjects, createdBy string) bool {
	return s.ActorKind == ActorSystem || IsSender(s, createdBy) || Allowed(ctx, c, s, SubmissionsManage)
}

// CanViewSubmission reports whether the caller may see a submission: signing
// read permission, its sender, or one of its signers.
func CanViewSubmission(ctx context.Context, c Checker, s Subjects, createdBy string, signerUserIDs []string) bool {
	return s.ActorKind == ActorSystem || IsSender(s, createdBy) || IsParticipant(s, signerUserIDs) ||
		Allowed(ctx, c, s, SigningRead)
}

// Split returns the resource and action of a "resource:action" permission.
func Split(perm string) (resource, action string, ok bool) {
	resource, action, ok = strings.Cut(perm, ":")
	return resource, action, ok && resource != "" && action != ""
}
