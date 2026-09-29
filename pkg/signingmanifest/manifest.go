// Package signingmanifest declares what the signing module registers with the
// application gateway: routes derived from the embedded OpenAPI document, the
// API permissions, the CASL abilities and the navigation entries; plus the
// module roles and built-in role grants it registers with the auth service.
// The module's gRPC surface (the scheduler's task executor) is service to
// service and is not proxied by the gateway.
package signingmanifest

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/getkin/kin-openapi/openapi3"

	"github.com/go-tangra/go-tangra-auth/sdk/v4/pkg/authclient"
	"github.com/go-tangra/go-tangra-portal/sdk/v4/pkg/gatewayclient"
	"github.com/go-tangra/go-tangra-signing/v4/api/openapi"
)

// Module identity.
const (
	Module       = "signing"
	DisplayName  = "Signing"
	Version      = "1.0.0"
	RemotePrefix = "/ui"
	APIPrefix    = "/api/signing"
)

// OpenAPI operation extensions.
const (
	PermissionExtension    = "x-freya-permission"
	PublicExtension        = "x-freya-public"
	BodyLimitExtension     = "x-freya-max-body-bytes"
	ClientAddressExtension = "x-freya-client-address"
	TimeoutExtension       = "x-freya-timeout-seconds"
)

// Permissions the module registers (spec FR-048, research D7).
var Permissions = []gatewayclient.Permission{
	{Resource: "signing", Action: "sign", Description: "Sign or decline documents assigned to me, see my inbox and manage my own signing certificate"},
	{Resource: "signing", Action: "read", Description: "View templates, folders and every submission of the tenant, and verify signed documents"},
	{Resource: "templates", Action: "manage", Description: "Create, edit, clone, archive and delete templates and folders, and use the field builder"},
	{Resource: "submissions", Action: "create", Description: "Send documents for signature and manage the submissions I sent"},
	{Resource: "submissions", Action: "manage", Description: "Cancel, resend, replace signers of and delete any submission of the tenant"},
	{Resource: "certificates", Action: "manage", Description: "Manage the tenant signing CA and certificates, revoke certificates and sign documents with administrator certificates"},
	{Resource: "backup", Action: "manage", Description: "Export and import the tenant's signing data"},
}

// Permission sets of the module roles (FR-049). Every set includes
// signing:sign, which every built-in role also receives.
var (
	adminPermissions    = PermissionRefs()
	operatorPermissions = []string{"signing:sign", "signing:read", "templates:manage", "submissions:create", "submissions:manage"}
	senderPermissions   = []string{"signing:sign", "signing:read", "submissions:create"}
	viewerPermissions   = []string{"signing:sign", "signing:read"}
	memberPermissions   = []string{"signing:sign"}
)

// Roles is the module's role set (feature 019): ready-made roles auth offers in
// every tenant, locked there (administrators assign or clone them).
var Roles = []authclient.ModuleRole{
	{
		Slug: "administrator", DisplayName: DisplayName + " administrator",
		Description: "Everything: templates, submissions, the signing CA and certificates, document signing and backups",
		Permissions: adminPermissions,
	},
	{
		Slug: "operator", DisplayName: DisplayName + " operator",
		Description: "Manage templates and every submission of the tenant",
		Permissions: operatorPermissions,
	},
	{
		Slug: "sender", DisplayName: DisplayName + " sender",
		Description: "Send documents for signature and manage the submissions I sent",
		Permissions: senderPermissions,
	},
	{
		Slug: "viewer", DisplayName: DisplayName + " viewer",
		Description: "View templates and submissions and verify documents",
		Permissions: viewerPermissions,
	},
}

// Grants maps the platform's built-in role slugs to permission sets: owners
// and admins hold the administrator set, operators the operator set, auditors
// the viewer set, and members may sign what is assigned to them.
var Grants = map[string][]string{
	"owner":    adminPermissions,
	"admin":    adminPermissions,
	"operator": operatorPermissions,
	"auditor":  viewerPermissions,
	"member":   memberPermissions,
}

// Methods proxied by the gateway: none.
var Methods []gatewayclient.Method

// Abilities are the CASL rules bound to the permissions.
var Abilities = []gatewayclient.Ability{
	{Action: []string{"sign"}, Subject: []string{"SigningDocument", "SigningCertificate"}, Requires: "signing:sign"},
	{Action: []string{"read"}, Subject: []string{"SigningTemplate", "SigningSubmission", "SigningVerification"}, Requires: "signing:read"},
	{Action: []string{"create", "update", "delete"}, Subject: []string{"SigningTemplate"}, Requires: "templates:manage"},
	{Action: []string{"create"}, Subject: []string{"SigningSubmission"}, Requires: "submissions:create"},
	{Action: []string{"manage"}, Subject: []string{"SigningSubmission"}, Requires: "submissions:manage"},
	{Action: []string{"manage"}, Subject: []string{"SigningCertificate"}, Requires: "certificates:manage"},
	{Action: []string{"manage"}, Subject: []string{"SigningBackup"}, Requires: "backup:manage"},
}

// Nav lists the navigation contributions.
var Nav = []gatewayclient.NavEntry{
	{Title: "To sign", Path: "/signing", Icon: "mdi-inbox-outline", Order: 960, Requires: "signing:sign"},
	{Title: "Submissions", Path: "/signing/submissions", Icon: "mdi-send-outline", Order: 961, Requires: "submissions:create"},
	{Title: "Templates", Path: "/signing/templates", Icon: "mdi-file-document-edit-outline", Order: 962, Requires: "signing:read"},
	{Title: "My certificate", Path: "/signing/certificate", Icon: "mdi-file-certificate-outline", Order: 963, Requires: "signing:sign"},
	{Title: "Verify", Path: "/signing/verify", Icon: "mdi-shield-check-outline", Order: 964, Requires: "signing:read"},
	{Title: "Certificates", Path: "/signing/certificates", Icon: "mdi-certificate-outline", Order: 965, Requires: "certificates:manage"},
}

// PermissionRefs lists "resource:action" for every declared permission.
func PermissionRefs() []string {
	out := make([]string, 0, len(Permissions))
	for _, p := range Permissions {
		out = append(out, p.Resource+":"+p.Action)
	}
	return out
}

// Routes derives the gateway routes from the OpenAPI document.
func Routes(doc *openapi3.T) ([]gatewayclient.Route, error) {
	known := map[string]bool{}
	for _, p := range PermissionRefs() {
		known[p] = true
	}
	var routes []gatewayclient.Route
	for p, item := range doc.Paths.Map() {
		for m, op := range item.Operations() {
			r := gatewayclient.Route{Method: strings.ToUpper(m), Path: p}
			perm, _ := op.Extensions[PermissionExtension].(string)
			public, _ := op.Extensions[PublicExtension].(bool)
			switch {
			case public:
				r.Public = true
			case perm == "":
				return nil, fmt.Errorf("signingmanifest: %s %s declares no permission", r.Method, p)
			case !known[perm]:
				return nil, fmt.Errorf("signingmanifest: %s %s uses undeclared permission %q", r.Method, p, perm)
			default:
				r.Permission = perm
			}
			if v, ok := op.Extensions[BodyLimitExtension]; ok {
				n, ok := v.(float64)
				if !ok || n <= 0 {
					return nil, fmt.Errorf("signingmanifest: %s %s has a bad body limit", r.Method, p)
				}
				r.MaxBodyBytes = uint64(n)
			}
			if v, ok := op.Extensions[ClientAddressExtension].(bool); ok && v {
				r.ClientAddress = true
			}
			if v, ok := op.Extensions[TimeoutExtension]; ok {
				n, ok := v.(float64)
				if !ok || n <= 0 || n > 600 {
					return nil, fmt.Errorf("signingmanifest: %s %s has a bad timeout", r.Method, p)
				}
				r.Timeout = time.Duration(n) * time.Second
			}
			routes = append(routes, r)
		}
	}
	sort.Slice(routes, func(i, j int) bool { return routes[i].Method+" "+routes[i].Path < routes[j].Method+" "+routes[j].Path })
	return routes, nil
}

// Load parses the embedded document.
func Load() (*openapi3.T, error) {
	return openapi3.NewLoader().LoadFromData(openapi.Signing)
}

// Manifest builds the gateway manifest from the embedded OpenAPI document.
func Manifest() (gatewayclient.Manifest, error) {
	doc, err := Load()
	if err != nil {
		return gatewayclient.Manifest{}, err
	}
	routes, err := Routes(doc)
	if err != nil {
		return gatewayclient.Manifest{}, err
	}
	return gatewayclient.Manifest{
		Module: Module, DisplayName: DisplayName, Version: Version,
		Prefixes:    []string{APIPrefix},
		Routes:      routes,
		Methods:     Methods,
		Permissions: Permissions,
		Abilities:   Abilities,
		Exposes:     []string{"./routes", "./nav"},
		Nav:         Nav,
	}, nil
}

// BuiltinRoles lists the built-in role slugs granted, in a stable order.
var BuiltinRoles = []string{"owner", "admin", "member", "auditor", "operator"}

// Registration is what the module registers with auth: every module
// permission, the module roles and the built-in role grants.
func Registration() authclient.Registration {
	reg := authclient.Registration{Module: Module, DisplayName: DisplayName, Roles: Roles, BuiltinGrants: Grants}
	for _, p := range Permissions {
		reg.Permissions = append(reg.Permissions, authclient.Permission{Resource: p.Resource, Action: p.Action, Description: p.Description})
	}
	return reg
}
