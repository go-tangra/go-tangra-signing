package authz

import (
	"context"
	"errors"
	"testing"
)

const (
	tn    = "0190f7c2-6a3e-7c1a-9b2e-2f6f9d1b4c55"
	other = "0190f7c2-6a3e-7c1a-9b2e-2f6f9d1b4c66"
)

var ctx = context.Background()

func TestRequireUser(t *testing.T) {
	c := Static{"viewer": {SigningRead}, "operator": {SigningRead, TemplatesManage, SubmissionsCreate}}
	viewer := User(tn, "viewer", nil)
	op := User(tn, "operator", nil)
	if err := Require(ctx, c, viewer, SigningRead); err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{TemplatesManage, SubmissionsCreate, SubmissionsManage, CertificatesManage, BackupManage} {
		if err := Require(ctx, c, viewer, p); !errors.Is(err, ErrForbidden) {
			t.Fatalf("viewer %s: %v", p, err)
		}
	}
	if !Allowed(ctx, c, op, SubmissionsCreate) || Allowed(ctx, c, op, CertificatesManage) {
		t.Fatal("operator grants")
	}
	if Allowed(ctx, nil, op, SigningRead) {
		t.Fatal("nil checker must refuse")
	}
	if Allowed(ctx, c, User(tn, "", nil), SigningRead) || Allowed(ctx, c, User("", "operator", nil), SigningRead) {
		t.Fatal("user without id or tenant")
	}
	if Allowed(ctx, c, op, "templates:fly") {
		t.Fatal("unknown permission")
	}
	if Allowed(ctx, c, Subjects{TenantID: tn, UserID: "operator", ActorKind: "robot"}, SigningRead) {
		t.Fatal("unknown actor kind")
	}
	called := false
	fn := CheckerFunc(func(_ context.Context, tenant, user, perm string) bool {
		called = true
		return tenant == tn && user == "operator" && perm == BackupManage
	})
	if !Allowed(ctx, fn, op, BackupManage) || !called {
		t.Fatal("checker func")
	}
}

func TestMember(t *testing.T) {
	// Member needs no checker and no permission, only a signed-in tenant user.
	if !Allowed(ctx, nil, User(tn, "u", nil), Member) {
		t.Fatal("member refused")
	}
	for _, s := range []Subjects{User("", "u", nil), User(tn, "", nil), Service("spiffe://example.org/svc/x"),
		{TenantID: tn, UserID: "u", ActorKind: "robot"}} {
		if Allowed(ctx, Static{"u": Permissions}, s, Member) {
			t.Fatalf("member accepted for %+v", s)
		}
	}
	if !Allowed(ctx, nil, System(), Member) {
		t.Fatal("system is allowed everything")
	}
	// A checker answering yes for "member" must not matter: Member is never
	// looked up as a permission.
	if Allowed(ctx, CheckerFunc(func(context.Context, string, string, string) bool { return true }), User("", "u", nil), Member) {
		t.Fatal("member without tenant")
	}
}

func TestNonHuman(t *testing.T) {
	sys := System()
	for _, p := range Permissions {
		if !Allowed(ctx, nil, sys, p) {
			t.Fatalf("system %s", p)
		}
	}
	svc := Service("spiffe://example.org/svc/scheduler")
	if svc.ActorKind != ActorService || svc.ActorID() != "spiffe://example.org/svc/scheduler" {
		t.Fatalf("service = %+v", svc)
	}
	for _, p := range Permissions {
		if Allowed(ctx, Static{"spiffe://example.org/svc/scheduler": Permissions}, svc, p) {
			t.Fatalf("service must not hold %s", p)
		}
	}
	if svc.IsPlatformAdmin() || !sys.IsPlatformAdmin() || svc.IsMember() || sys.IsMember() {
		t.Fatal("platform admin / member of non-humans")
	}
}

func TestPlatformScope(t *testing.T) {
	admin := User(tn, "u", []string{"member", RolePlatformAdmin})
	member := User(tn, "m", []string{"member"})
	if !admin.HasRole("member") || admin.HasRole("owner") || !admin.IsPlatformAdmin() || member.IsPlatformAdmin() {
		t.Fatal("roles")
	}
	if (Subjects{ActorKind: ActorService, Roles: []string{RolePlatformAdmin}}).IsPlatformAdmin() {
		t.Fatal("service platform admin")
	}
	if err := RequirePlatformAdmin(admin); err != nil {
		t.Fatal(err)
	}
	if err := RequirePlatformAdmin(member); !errors.Is(err, ErrForbidden) {
		t.Fatal("member passed platform-admin")
	}
	if !CanSeeTenant(member, tn) || CanSeeTenant(member, other) || !CanSeeTenant(admin, other) || CanSeeTenant(User("", "x", nil), "") {
		t.Fatal("tenant visibility")
	}
	if (Subjects{ActorKind: ActorSystem}).ActorID() != ActorSystem || member.ActorID() != "m" {
		t.Fatal("actor id")
	}
}

func TestRelations(t *testing.T) {
	sender := User(tn, "sender", nil)
	signer := User(tn, "signer", nil)
	stranger := User(tn, "stranger", nil)
	manager := User(tn, "manager", nil)
	reader := User(tn, "reader", nil)
	c := Static{"manager": {SubmissionsManage}, "reader": {SigningRead}}
	signers := []string{"signer", "other-signer"}

	if !IsSender(sender, "sender") || IsSender(stranger, "sender") || IsSender(sender, "") || IsSender(Service("sender"), "sender") {
		t.Fatal("sender")
	}
	if !IsParticipant(signer, signers) || IsParticipant(stranger, signers) || IsParticipant(Subjects{UserID: "signer"}, signers) {
		t.Fatal("participant")
	}
	for _, tc := range []struct {
		s    Subjects
		want bool
	}{{sender, true}, {manager, true}, {signer, false}, {stranger, false}, {reader, false}, {System(), true}} {
		if got := CanControlSubmission(ctx, c, tc.s, "sender"); got != tc.want {
			t.Errorf("control %s = %v", tc.s.UserID, got)
		}
	}
	for _, tc := range []struct {
		s    Subjects
		want bool
	}{{sender, true}, {signer, true}, {reader, true}, {stranger, false}, {manager, false}, {System(), true}} {
		if got := CanViewSubmission(ctx, c, tc.s, "sender", signers); got != tc.want {
			t.Errorf("view %s = %v", tc.s.UserID, got)
		}
	}
}

func TestVocabulary(t *testing.T) {
	if len(Permissions) != 6 || !Known(BackupManage) || !Known(Member) || Known("x:y") {
		t.Fatal("permissions")
	}
	if r, act, ok := Split(SubmissionsCreate); !ok || r != "submissions" || act != "create" {
		t.Fatal("split")
	}
	for _, bad := range []string{"templates", ":read", "templates:"} {
		if _, _, ok := Split(bad); ok {
			t.Fatalf("split %q", bad)
		}
	}
}
