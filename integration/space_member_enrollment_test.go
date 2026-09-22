package integration

import (
	"context"
	"testing"

	"github.com/golang-jwt/jwt/v5"

	"github.com/wanglongan587/cloud/internal/core"
)

// registerUser creates a local user through /me (the identity-only side effect)
// but deliberately does NOT join them to the tenant. This mirrors "an
// already-registered user who is not yet a tenant member": the add-by-email flow
// must atomically enroll them into the tenant as part of adding them.
func (f *fixture) registerUser(t *testing.T, subject, display string) (core.Claims, string) {
	t.Helper()
	gw := core.Claims{RegisteredClaims: jwt.RegisteredClaims{Subject: "gateway-a"}}
	u := core.Claims{RegisteredClaims: jwt.RegisteredClaims{Subject: subject}, Source: "corp", DisplayName: display}
	o, status, e := f.client.Call(context.Background(), "GET", "/api/v1/me", "gateway", gw, &u, "", nil)
	must(t, e)
	if status != 200 {
		t.Fatalf("registerUser %s: want 200 got %d", subject, status)
	}
	return u, o.S("id")
}

// enrollByEmail adds the user identified by email into the space via the new
// add-by-email endpoint, returning the membership row.
func (f *fixture) enrollByEmail(sid, email, key string) core.Object {
	return f.call("POST", f.path("/spaces/"+sid+"/members"), core.Object{"email": email}, key, 200)
}

func (f *fixture) workspaceMemberCount(sid, uid string) int {
	return f.scalar("SELECT count(*) FROM collab_workspace_members WHERE workspace_id=$1 AND user_id=$2", sid, uid)
}

// TestEnrollRegisteredUserByEmail covers the happy path: an admin/owner adds an
// already-registered user by email (case-variant normalized), the member row is
// created with role=member, and the user becomes a space member.
func TestEnrollRegisteredUserByEmail(t *testing.T) {
	f := setup(t)
	space := f.createSpace("Team", "team", "space-create")
	sid := space.S("id")
	// Bob registers with a lowercase subject; alice adds "Bob@Example.com" — the
	// case-variant must resolve to the same identity row.
	_, bobID := f.registerUser(t, "bob@example.com", "Bob")

	member := f.enrollByEmail(sid, "Bob@Example.com", "enroll-bob")
	if member.S("role") != "member" || member.S("status") != "active" || member.S("userId") != bobID {
		t.Fatalf("enrolled member unexpected: %v", member)
	}
	if member.S("workspaceId") != sid {
		t.Fatalf("member on wrong space: %v", member)
	}
	if f.workspaceMemberCount(sid, bobID) != 1 {
		t.Fatal("bob is not exactly one workspace member")
	}
	if f.scalar("SELECT count(*) FROM collab_workspace_members WHERE workspace_id=$1 AND user_id=$2 AND role='member' AND status='active'", sid, bobID) != 1 {
		t.Fatal("enrolled member is not role=member/active")
	}
	// The new member now appears in the member list.
	list := f.call("GET", f.path("/spaces/"+sid+"/members"), nil, "", 200)
	found := false
	for _, item := range list["items"].([]any) {
		if core.Object(item.(map[string]any)).S("userId") == bobID {
			found = true
		}
	}
	if !found {
		t.Fatal("enrolled user missing from member list")
	}
}

// TestEnrollUnknownEmailRejected covers SD1: an email that resolves to no active
// user is 404 user_not_registered and leaves zero dirty membership rows — no
// auto-create, no invitation, no pending member.
func TestEnrollUnknownEmailRejected(t *testing.T) {
	f := setup(t)
	space := f.createSpace("Team", "team", "space-create")
	sid := space.S("id")
	tenantBefore := f.scalar("SELECT count(*) FROM tenant_memberships WHERE tenant_id=$1", f.tid)
	spaceBefore := f.scalar("SELECT count(*) FROM collab_workspace_members WHERE workspace_id=$1", sid)

	o := f.call("POST", f.path("/spaces/"+sid+"/members"), core.Object{"email": "nobody@example.com"}, "enroll-unknown", 404)
	if o.S("code") != "user_not_registered" {
		t.Fatalf("unknown email: want user_not_registered got %v", o)
	}
	if f.scalar("SELECT count(*) FROM tenant_memberships WHERE tenant_id=$1", f.tid) != tenantBefore {
		t.Fatal("unknown email created a tenant membership row")
	}
	if f.scalar("SELECT count(*) FROM collab_workspace_members WHERE workspace_id=$1", sid) != spaceBefore {
		t.Fatal("unknown email created a workspace membership row")
	}
	// Invalid email shape is a 400, distinct from unknown-registered.
	bad := f.call("POST", f.path("/spaces/"+sid+"/members"), core.Object{"email": "not-an-email"}, "enroll-bad", 400)
	if bad.S("code") != "invalid_email" {
		t.Fatalf("malformed email: want invalid_email got %v", bad)
	}
}

// TestEnrollAlreadyMemberIdempotent covers SD3: adding an existing member returns
// the current membership unchanged — role/status/version intact, exactly one row.
func TestEnrollAlreadyMemberIdempotent(t *testing.T) {
	f := setup(t)
	space := f.createSpace("Team", "team", "space-create")
	sid := space.S("id")
	_, bobID := f.registerUser(t, "bob@example.com", "Bob")

	first := f.enrollByEmail(sid, "bob@example.com", "enroll-bob-1")
	if first.S("role") != "member" || first.S("status") != "active" {
		t.Fatalf("first enrollment unexpected: %v", first)
	}
	// Second add uses a fresh key (not the idempotency replay) and returns the
	// existing row: same role/status/version, still one row.
	second := f.enrollByEmail(sid, "bob@example.com", "enroll-bob-2")
	if second.S("role") != first.S("role") || second.S("status") != first.S("status") || second.N("version") != first.N("version") || second.S("userId") != bobID {
		t.Fatalf("duplicate add mutated membership: %v vs %v", first, second)
	}
	if f.workspaceMemberCount(sid, bobID) != 1 {
		t.Fatal("duplicate add created a second membership row")
	}
	// A re-add never mutates an existing member's role: enrollment only ever
	// resolves the email to a registered user and ensures a membership row, so an
	// existing admin stays an admin.
	_, adminID := f.registerUser(t, "admin2@example.com", "Admin2")
	f.enrollByEmail(sid, "admin2@example.com", "enroll-admin2")
	f.call("PUT", f.path("/spaces/"+sid+"/members/"+adminID), core.Object{"role": "admin", "status": "active", "version": 1}, "", 200)
	again := f.enrollByEmail(sid, "admin2@example.com", "enroll-admin2-again")
	if again.S("role") != "admin" {
		t.Fatalf("re-add demoted an admin: %v", again)
	}
}

// TestEnrollRoleEnforcement covers SD1/SD3: owner and admin may add by email, a
// plain member is rejected with 403 space_role_required (business-level role
// enforcement, enforced server-side, not just hidden in the UI).
func TestEnrollRoleEnforcement(t *testing.T) {
	f := setup(t)
	gw := core.Claims{RegisteredClaims: jwt.RegisteredClaims{Subject: "gateway-a"}}
	space := f.createSpace("Team", "team", "space-create")
	sid := space.S("id")

	// owner adds bob -> 200.
	bob, bobID := f.registerUser(t, "bob@example.com", "Bob")
	f.enrollByEmail(sid, "bob@example.com", "enroll-bob")
	// owner promotes bob to admin, admin adds carol -> 200.
	f.call("PUT", f.path("/spaces/"+sid+"/members/"+bobID), core.Object{"role": "admin", "status": "active", "version": 1}, "", 200)
	carol, _ := f.registerUser(t, "carol@example.com", "Carol")
	_, status, e := f.client.Call(context.Background(), "POST", f.path("/spaces/"+sid+"/members"), "gateway", gw, &bob, "enroll-carol", core.Object{"email": "carol@example.com"})
	must(t, e)
	if status != 200 {
		t.Fatalf("admin added by email: want 200 got %d", status)
	}
	// plain member (carol) tries to add dave -> 403, no row created.
	_, daveID := f.registerUser(t, "dave@example.com", "Dave")
	o, status, e := f.client.Call(context.Background(), "POST", f.path("/spaces/"+sid+"/members"), "gateway", gw, &carol, "enroll-dave", core.Object{"email": "dave@example.com"})
	must(t, e)
	if status != 403 || o.S("code") != "space_role_required" {
		t.Fatalf("member added by email: want 403 space_role_required got %d %v", status, o)
	}
	if f.workspaceMemberCount(sid, daveID) != 0 {
		t.Fatal("rejected member add left a membership row")
	}
}

// TestEnrollNonTenantMemberEnrollsAtomically covers SD2: a user who is registered
// but not yet a tenant member is enrolled into both the tenant and the space in a
// single transaction — no half state, no external effects.
func TestEnrollNonTenantMemberEnrollsAtomically(t *testing.T) {
	f := setup(t)
	space := f.createSpace("Team", "team", "space-create")
	sid := space.S("id")
	_, daveID := f.registerUser(t, "dave@example.com", "Dave")
	if f.scalar("SELECT count(*) FROM tenant_memberships WHERE tenant_id=$1 AND user_id=$2", f.tid, daveID) != 0 {
		t.Fatal("precondition: dave must not be a tenant member yet")
	}

	f.enrollByEmail(sid, "dave@example.com", "enroll-dave")

	// Both the tenant membership and the space membership appear together.
	if f.scalar("SELECT count(*) FROM tenant_memberships WHERE tenant_id=$1 AND user_id=$2 AND role='member' AND status='active'", f.tid, daveID) != 1 {
		t.Fatal("dave was not enrolled into the tenant atomically")
	}
	if f.workspaceMemberCount(sid, daveID) != 1 {
		t.Fatal("dave was not enrolled into the space atomically")
	}
	// A subsequent add is still idempotent (no second tenant row).
	f.enrollByEmail(sid, "dave@example.com", "enroll-dave-2")
	if f.scalar("SELECT count(*) FROM tenant_memberships WHERE tenant_id=$1 AND user_id=$2", f.tid, daveID) != 1 {
		t.Fatal("duplicate add duplicated the tenant membership")
	}
}

// TestEnrollCrossTenantNoExistenceLeak covers §13/§19: a caller who is not a
// member of the space's tenant is rejected with 403 membership_required — never a
// 404 that would reveal whether the email is registered.
func TestEnrollCrossTenantNoExistenceLeak(t *testing.T) {
	f := setup(t)
	gw := core.Claims{RegisteredClaims: jwt.RegisteredClaims{Subject: "gateway-a"}}
	space := f.createSpace("Team", "team", "space-create")
	sid := space.S("id")
	// A real, registered email that zed must not be able to probe.
	_, bobID := f.registerUser(t, "bob@example.com", "Bob")
	f.enrollByEmail(sid, "bob@example.com", "enroll-bob")
	if f.scalar("SELECT count(*) FROM user_identities WHERE source='corp' AND subject='bob@example.com'") != 1 {
		t.Fatal("precondition: bob must be registered")
	}

	// zed is registered but belongs to no tenant here; any attempt to add by email
	// fails at the membership gate, before any lookup reveals bob's existence.
	zed, _ := f.registerUser(t, "zed@example.com", "Zed")
	o, status, e := f.client.Call(context.Background(), "POST", f.path("/spaces/"+sid+"/members"), "gateway", gw, &zed, "enroll-zed", core.Object{"email": "bob@example.com"})
	must(t, e)
	if status != 403 || o.S("code") != "membership_required" {
		t.Fatalf("cross-tenant add: want 403 membership_required got %d %v", status, o)
	}
	if f.workspaceMemberCount(sid, bobID) != 1 {
		t.Fatal("cross-tenant probe altered membership")
	}
}

// TestProjectAccessOwnerOnlyUntilWorkspaceSharing and
// TestRuntimeWorkspaceOwnerOnlyUntilWorkspaceSharing (the owner-only regressions
// from Step 2B) were superseded by Step 3 — the project workspace-sharing
// migration. Their workspace-sharing assertions now live in
// project_workspace_sharing_test.go:
//   - TestScopedProjectSharedWithWorkspaceMembers (§14)
//   - TestRuntimeWorkspaceInheritsProjectAccess (§17)
