package signingmanifest

import (
	"reflect"
	"regexp"
	"sort"
	"strings"
	"testing"

	"github.com/getkin/kin-openapi/openapi3"

	"github.com/go-tangra/go-tangra-signing/v4/api/openapi"
	"github.com/go-tangra/go-tangra-signing/v4/internal/authz"
)

func TestOpenAPIParsesAndValidates(t *testing.T) {
	openapi3.DefineStringFormatValidator("uuid", openapi3.NewRegexpFormatValidator(openapi3.FormatOfStringForUUIDOfRFC9562))
	loader := openapi3.NewLoader()
	doc, err := loader.LoadFromData(openapi.Signing)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if err := doc.Validate(loader.Context, openapi3.DisableExamplesValidation()); err != nil {
		t.Fatalf("validate: %v", err)
	}
}

func TestManifestBuilds(t *testing.T) {
	m, err := Manifest()
	if err != nil {
		t.Fatalf("manifest: %v", err)
	}
	if m.Module != "signing" || m.DisplayName != "Signing" || len(m.Prefixes) != 1 || m.Prefixes[0] != "/api/signing" {
		t.Fatalf("identity = %+v", m)
	}
	if len(m.Routes) != 51 {
		t.Fatalf("routes = %d", len(m.Routes))
	}
	public, clientAddr := 0, 0
	for _, r := range m.Routes {
		if !strings.HasPrefix(r.Path, "/api/signing/v1/") {
			t.Fatalf("route outside prefix: %s", r.Path)
		}
		if r.Public {
			public++
			if r.Path != "/api/signing/v1/health" {
				t.Fatalf("unexpected public route %s", r.Path)
			}
		}
		if r.ClientAddress {
			clientAddr++
			if !strings.HasPrefix(r.Path, "/api/signing/v1/signing/") {
				t.Fatalf("client address forwarded outside the signing routes: %s", r.Path)
			}
		}
		if r.Path == "/api/signing/v1/templates" && r.Method == "POST" && r.MaxBodyBytes != 51<<20 {
			t.Fatalf("upload body limit = %d", r.MaxBodyBytes)
		}
	}
	if public != 1 || clientAddr != 3 {
		t.Fatalf("public routes = %d, client-address routes = %d", public, clientAddr)
	}
	var titles []string
	for _, n := range m.Nav {
		titles = append(titles, n.Title+"|"+n.Path+"|"+n.Requires)
	}
	if strings.Join(titles, ",") != "To sign|/signing|signing:sign,Submissions|/signing/submissions|submissions:create,"+
		"Templates|/signing/templates|signing:read,My certificate|/signing/certificate|signing:sign,"+
		"Verify|/signing/verify|signing:read,Certificates|/signing/certificates|certificates:manage" {
		t.Fatalf("nav = %v", titles)
	}
	if strings.Join(m.Exposes, ",") != "./routes,./nav" || len(m.Methods) != 0 {
		t.Fatalf("exposes/methods = %v %v", m.Exposes, m.Methods)
	}
	if len(m.Abilities) != 7 || len(m.Permissions) != 7 {
		t.Fatalf("abilities/permissions = %d/%d", len(m.Abilities), len(m.Permissions))
	}
	for _, p := range m.Permissions {
		if p.Description == "" {
			t.Fatalf("permission %s:%s lacks a description", p.Resource, p.Action)
		}
	}
}

// Every route's permission is a module permission, and only the signer
// routes (and the ones that re-check the sender/participant relation) are
// open to signing:sign.
func TestSignRoutesAreRelationChecked(t *testing.T) {
	m, err := Manifest()
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range m.Routes {
		if r.Permission != "signing:sign" {
			continue
		}
		ok := strings.HasPrefix(r.Path, "/api/signing/v1/signing/") || strings.HasPrefix(r.Path, "/api/signing/v1/me/") ||
			strings.HasPrefix(r.Path, "/api/signing/v1/submissions") || r.Path == "/api/signing/v1/inbox" || r.Path == "/api/signing/v1/stream"
		if !ok {
			t.Errorf("%s %s is open to every member", r.Method, r.Path)
		}
	}
}

// The manifest's permission vocabulary is exactly the module's authz set.
func TestPermissionsMatchAuthz(t *testing.T) {
	got := PermissionRefs()
	want := append([]string(nil), authz.Permissions...)
	sort.Strings(got)
	sort.Strings(want)
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("manifest %v != authz %v", got, want)
	}
}

// TestRoles pins the module role set (FR-049).
func TestRoles(t *testing.T) {
	want := map[string][]string{
		"administrator": {"signing:sign", "signing:read", "templates:manage", "submissions:create", "submissions:manage", "certificates:manage", "backup:manage"},
		"operator":      {"signing:sign", "signing:read", "templates:manage", "submissions:create", "submissions:manage"},
		"sender":        {"signing:sign", "signing:read", "submissions:create"},
		"viewer":        {"signing:sign", "signing:read"},
	}
	names := map[string]string{"administrator": "Signing administrator", "operator": "Signing operator", "sender": "Signing sender",
		"viewer": "Signing viewer"}
	if len(Roles) != len(want) {
		t.Fatalf("want %d roles, got %d", len(want), len(Roles))
	}
	own := map[string]bool{}
	for _, r := range PermissionRefs() {
		own[r] = true
	}
	slug := regexp.MustCompile(`^[a-z0-9](?:[a-z0-9-]{0,30}[a-z0-9])?$`)
	for _, r := range Roles {
		if !slug.MatchString(r.Slug) {
			t.Errorf("role slug %q", r.Slug)
		}
		if r.DisplayName != names[r.Slug] || r.Description == "" {
			t.Errorf("role %q: display name %q, description %q", r.Slug, r.DisplayName, r.Description)
		}
		if !reflect.DeepEqual(r.Permissions, want[r.Slug]) {
			t.Errorf("role %q: permissions %v, want %v", r.Slug, r.Permissions, want[r.Slug])
		}
		for _, p := range r.Permissions {
			if !own[p] {
				t.Errorf("role %q names %q, not a signing permission", r.Slug, p)
			}
		}
	}
}

// TestBuiltinGrants: every built-in role can sign what is assigned to it.
func TestBuiltinGrants(t *testing.T) {
	for _, slug := range BuiltinRoles {
		perms := Grants[slug]
		if len(perms) == 0 || perms[0] != "signing:sign" {
			t.Fatalf("built-in role %s cannot sign: %v", slug, perms)
		}
	}
	if !reflect.DeepEqual(Grants["member"], []string{"signing:sign"}) {
		t.Fatalf("members get exactly signing:sign, got %v", Grants["member"])
	}
	if len(BuiltinRoles) != len(Grants) {
		t.Fatalf("BuiltinRoles %v vs grants %v", BuiltinRoles, Grants)
	}
}

// TestRegistration: the auth registration carries the module identity, every
// permission, the complete role set and the built-in grants, and is valid.
func TestRegistration(t *testing.T) {
	reg := Registration()
	if err := reg.Validate(); err != nil {
		t.Fatal(err)
	}
	req := reg.Request()
	if req.GetModule() != "signing" || req.GetModuleDisplayName() != "Signing" || !req.GetDeclaresRoles() {
		t.Fatalf("module %q display %q declares_roles %v", req.GetModule(), req.GetModuleDisplayName(), req.GetDeclaresRoles())
	}
	if len(req.GetPermissions()) != 7 || len(req.GetRoles()) != 4 {
		t.Fatalf("%d permissions, %d roles", len(req.GetPermissions()), len(req.GetRoles()))
	}
	got := map[string][]string{}
	for _, g := range req.GetBuiltinGrants() {
		got[g.GetRole()] = g.GetPermissions()
	}
	if !reflect.DeepEqual(got, Grants) {
		t.Fatalf("builtin grants %v", got)
	}
}

func docWith(ext map[string]any) *openapi3.T {
	paths := openapi3.NewPaths()
	paths.Set("/x", &openapi3.PathItem{Get: &openapi3.Operation{Extensions: ext}})
	return &openapi3.T{Paths: paths}
}

func TestRoutesValidationBranches(t *testing.T) {
	routes, err := Routes(docWith(map[string]any{PublicExtension: true}))
	if err != nil || !routes[0].Public {
		t.Fatalf("public: %v", err)
	}
	routes, err = Routes(docWith(map[string]any{PermissionExtension: "signing:read", BodyLimitExtension: float64(2048),
		TimeoutExtension: float64(30), ClientAddressExtension: true}))
	if err != nil || routes[0].MaxBodyBytes != 2048 || routes[0].Timeout.Seconds() != 30 || !routes[0].ClientAddress {
		t.Fatalf("valid: %v %+v", err, routes)
	}
	bad := []map[string]any{
		{},
		{PermissionExtension: "made:up"},
		{PermissionExtension: "signing:read", BodyLimitExtension: float64(0)},
		{PermissionExtension: "signing:read", BodyLimitExtension: "big"},
		{PermissionExtension: "signing:read", TimeoutExtension: float64(9999)},
		{PermissionExtension: "signing:read", TimeoutExtension: "slow"},
	}
	for i, ext := range bad {
		if _, err := Routes(docWith(ext)); err == nil {
			t.Errorf("case %d accepted", i)
		}
	}
}
