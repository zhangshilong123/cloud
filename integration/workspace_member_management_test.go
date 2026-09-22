package integration

import (
	"context"
	"testing"

	"github.com/golang-jwt/jwt/v5"

	"github.com/wanglongan587/cloud/internal/core"
)

// TestMemberManagement covers owner promote member→admin, demote admin→member,
// and remove member (hard delete of the workspace membership only). The removed
// member's own list no longer shows the workspace and the member list stops
// listing them.
func TestOwnerMemberManagement(t *testing.T) {
	f := setup(t)
	space := f.createSpace("Team", "team", "space-create")
	sid := space.S("id")
	bob, bobID := f.registerUser(t, "bob@example.com", "Bob")
	f.enrollByEmail(sid, "bob@example.com", "enroll-bob")

	// Promote member -> admin (version moves 1 -> 2).
	promoted := f.call("PUT", f.path("/spaces/"+sid+"/members/"+bobID), core.Object{"role": "admin", "status": "active", "version": 1}, "", 200)
	if promoted.S("role") != "admin" {
		t.Fatalf("promote: want admin got %v", promoted)
	}
	// Demote admin -> member (version 2 -> 3).
	demoted := f.call("PUT", f.path("/spaces/"+sid+"/members/"+bobID), core.Object{"role": "member", "status": "active", "version": 2}, "", 200)
	if demoted.S("role") != "member" {
		t.Fatalf("demote: want member got %v", demoted)
	}
	// Remove the member (version 3); the row is hard-deleted and lists stop
	// showing the workspace and the member.
	removed := f.call("DELETE", f.path("/spaces/"+sid+"/members/"+bobID), core.Object{"version": 3}, "remove-bob", 200)
	if removed.S("userId") != bobID {
		t.Fatalf("remove returned wrong member: %v", removed)
	}
	if f.workspaceMemberCount(sid, bobID) != 0 {
		t.Fatal("removed member's workspace membership still exists")
	}
	members := f.call("GET", f.path("/spaces/"+sid+"/members"), nil, "", 200)
	for _, item := range members["items"].([]any) {
		if core.Object(item.(map[string]any)).S("userId") == bobID {
			t.Fatal("removed member still in the member list")
		}
	}
	bobList := f.callUser(t, bob, "GET", f.path("/spaces"), nil, "", 200)
	if len(bobList["items"].([]any)) != 0 {
		t.Fatalf("removed member still sees the workspace: %v", bobList)
	}
}

// TestRemovedMemberAccessRevokedAndResourcesRemain covers §17/§23/§25: removing
// a member revokes their access to the workspace, its projects and their
// runtime workspaces (all 404 via the membership gate — even for a project
// they created), while the workspace and its resources persist for the
// remaining members.
func TestRemovedMemberAccessRevokedAndResourcesRemain(t *testing.T) {
	f := setup(t)
	space := f.createSpace("Team", "team", "space-create")
	sid := space.S("id")
	bob, bobID := f.registerUser(t, "bob@example.com", "Bob")
	f.enrollByEmail(sid, "bob@example.com", "enroll-bob")

	// Bob creates a project inside the workspace (member + creator).
	created := f.callUser(t, bob, "POST", f.path("/spaces/"+sid+"/projects"), core.Object{"name": "P", "repositoryUrl": "https://example.invalid/repo.git", "defaultBranch": "main"}, "bob-project", 202)
	pid := created.O("resource").S("id")
	wid := created.O("workspace").S("id")
	f.drain()

	// The owner removes bob (membership version stays 1 — never changed).
	f.call("DELETE", f.path("/spaces/"+sid+"/members/"+bobID), core.Object{"version": 1}, "remove-bob", 200)

	// Bob's access is revoked: workspace, project and runtime workspace all 404,
	// and the space list is empty. His creator id cannot bypass the membership
	// gate (§23) — not even to delete his own project.
	f.callUser(t, bob, "GET", f.path("/spaces/"+sid), nil, "", 404)
	f.callUser(t, bob, "GET", f.path("/projects/"+pid), nil, "", 404)
	f.callUser(t, bob, "GET", f.path("/workspaces/"+wid), nil, "", 404)
	f.callUser(t, bob, "DELETE", f.path("/projects/"+pid), core.Object{"version": 1}, "bob-delete", 404)
	bobList := f.callUser(t, bob, "GET", f.path("/spaces"), nil, "", 200)
	if len(bobList["items"].([]any)) != 0 {
		t.Fatalf("removed member still sees the workspace: %v", bobList)
	}

	// The project remains in the workspace and the owner can still manage it.
	ownerList := f.call("GET", f.path("/spaces/"+sid+"/projects"), nil, "", 200)
	if len(ownerList["items"].([]any)) != 1 || core.Object(ownerList["items"].([]any)[0].(map[string]any)).S("id") != pid {
		t.Fatalf("project lost after member removal: %v", ownerList)
	}
	active := f.call("GET", f.path("/projects/"+pid), nil, "", 200)
	if active.S("lifecycle") != "active" {
		t.Fatalf("project did not reach active: %s", active.S("lifecycle"))
	}
	f.call("DELETE", f.path("/projects/"+pid), core.Object{"version": active.N("version")}, "owner-delete", 202)
	f.drain()
	if f.scalar("SELECT count(*) FROM projects WHERE id=$1 AND deleted_at IS NULL", pid) != 0 {
		t.Fatal("owner could not delete the removed member's project")
	}
}

// TestMemberManagementAuthorization covers §26 (and §13): plain members cannot
// change roles or remove anyone (403 space_role_required); admins can change
// member roles but cannot grant owner and cannot remove anyone; add-by-email
// stays available to owner and admin (admin add preserved).
func TestMemberManagementAuthorization(t *testing.T) {
	f := setup(t)
	gw := core.Claims{RegisteredClaims: jwt.RegisteredClaims{Subject: "gateway-a"}}
	space := f.createSpace("Team", "team", "space-create")
	sid := space.S("id")
	bob, bobID := f.registerUser(t, "bob@example.com", "Bob")
	f.enrollByEmail(sid, "bob@example.com", "enroll-bob")
	carol, carolID := f.registerUser(t, "carol@example.com", "Carol")
	f.enrollByEmail(sid, "carol@example.com", "enroll-carol")
	_, daveID := f.registerUser(t, "dave@example.com", "Dave")

	// The owner promotes carol to admin (the only supported way to reach admin).
	f.call("PUT", f.path("/spaces/"+sid+"/members/"+carolID), core.Object{"role": "admin", "status": "active", "version": 1}, "", 200)

	// An admin cannot grant owner (owner-only) nor remove anyone (owner-only).
	_, status, e := f.client.Call(context.Background(), "PUT", f.path("/spaces/"+sid+"/members/"+bobID), "gateway", gw, &carol, "", core.Object{"role": "owner", "status": "active", "version": 2})
	must(t, e)
	if status != 403 {
		t.Fatalf("admin granted owner: want 403 got %d", status)
	}
	o, status, e := f.client.Call(context.Background(), "DELETE", f.path("/spaces/"+sid+"/members/"+bobID), "gateway", gw, &carol, "carol-remove", core.Object{"version": 2})
	must(t, e)
	if status != 403 || o.S("code") != "space_role_required" {
		t.Fatalf("admin removed a member: want 403 space_role_required got %d %v", status, o)
	}

	// A plain member cannot change roles or remove anyone.
	_, status, e = f.client.Call(context.Background(), "PUT", f.path("/spaces/"+sid+"/members/"+bobID), "gateway", gw, &bob, "", core.Object{"role": "member", "status": "active", "version": 2})
	must(t, e)
	if status != 403 {
		t.Fatalf("member changed a role: want 403 got %d", status)
	}
	_, status, e = f.client.Call(context.Background(), "DELETE", f.path("/spaces/"+sid+"/members/"+carolID), "gateway", gw, &bob, "bob-remove", core.Object{"version": 2})
	must(t, e)
	if status != 403 {
		t.Fatalf("member removed a member: want 403 got %d", status)
	}

	// Admin add-by-email is preserved: carol (admin) adds dave.
	_, status, e = f.client.Call(context.Background(), "POST", f.path("/spaces/"+sid+"/members"), "gateway", gw, &carol, "carol-add-dave", core.Object{"email": "dave@example.com"})
	must(t, e)
	if status != 200 {
		t.Fatalf("admin add-by-email: want 200 got %d", status)
	}
	if f.workspaceMemberCount(sid, daveID) != 1 {
		t.Fatal("admin add did not create the membership row")
	}
}

// TestOwnerProtectionAndLastOwnerInvariant covers §27 and §10/§12 as merged with
// upstream: the owner role is grantable by an owner, but the last active owner
// can never be demoted, disabled, or removed (409 space_last_owner); a second
// owner enables transfer; members and admins cannot reach owner.
func TestOwnerProtectionAndLastOwnerInvariant(t *testing.T) {
	f := setup(t)
	gw := core.Claims{RegisteredClaims: jwt.RegisteredClaims{Subject: "gateway-a"}}
	space := f.createSpace("Team", "team", "space-create")
	sid := space.S("id")
	bob, bobID := f.registerUser(t, "bob@example.com", "Bob")
	f.enrollByEmail(sid, "bob@example.com", "enroll-bob")
	carol, carolID := f.registerUser(t, "carol@example.com", "Carol")
	f.enrollByEmail(sid, "carol@example.com", "enroll-carol")
	f.call("PUT", f.path("/spaces/"+sid+"/members/"+carolID), core.Object{"role": "admin", "status": "active", "version": 1}, "", 200)

	// The last owner cannot demote themself, be disabled, or be removed.
	o := f.call("PUT", f.path("/spaces/"+sid+"/members/"+f.uid), core.Object{"role": "member", "status": "active", "version": 1}, "", 409)
	if o.S("code") != "space_last_owner" {
		t.Fatalf("last-owner self-demotion: want space_last_owner got %v", o)
	}
	o = f.call("PUT", f.path("/spaces/"+sid+"/members/"+f.uid), core.Object{"role": "owner", "status": "disabled", "version": 1}, "", 409)
	if o.S("code") != "space_last_owner" {
		t.Fatalf("last-owner disable: want space_last_owner got %v", o)
	}
	o = f.call("DELETE", f.path("/spaces/"+sid+"/members/"+f.uid), core.Object{"version": 1}, "self-remove", 409)
	if o.S("code") != "space_last_owner" {
		t.Fatalf("last-owner self-removal: want space_last_owner got %v", o)
	}

	// An admin cannot grant owner; a plain member cannot self-promote.
	_, status, e := f.client.Call(context.Background(), "PUT", f.path("/spaces/"+sid+"/members/"+bobID), "gateway", gw, &carol, "", core.Object{"role": "owner", "status": "active", "version": 1})
	must(t, e)
	if status != 403 {
		t.Fatalf("admin granted owner: want 403 got %d", status)
	}
	_, status, e = f.client.Call(context.Background(), "PUT", f.path("/spaces/"+sid+"/members/"+bobID), "gateway", gw, &bob, "", core.Object{"role": "admin", "status": "active", "version": 1})
	must(t, e)
	if status != 403 {
		t.Fatalf("member self-promotion: want 403 got %d", status)
	}

	// The owner grants bob owner (owner-only, grantable). Now there are two
	// owners, so the original owner can transfer away and be removed.
	f.call("PUT", f.path("/spaces/"+sid+"/members/"+bobID), core.Object{"role": "owner", "status": "active", "version": 1}, "", 200)
	if f.scalar("SELECT count(*) FROM collab_workspace_members WHERE workspace_id=$1 AND role='owner' AND status='active'", sid) != 2 {
		t.Fatal("owner grant did not create a second owner")
	}
	f.call("PUT", f.path("/spaces/"+sid+"/members/"+f.uid), core.Object{"role": "member", "status": "active", "version": 1}, "", 200)
	if f.scalar("SELECT count(*) FROM collab_workspace_members WHERE workspace_id=$1 AND role='owner' AND status='active'", sid) != 1 {
		t.Fatal("ownership transfer did not leave exactly one owner")
	}
	if f.scalar("SELECT count(*) FROM collab_workspace_members WHERE workspace_id=$1 AND user_id=$2 AND role='owner' AND status='active'", sid, bobID) != 1 {
		t.Fatal("bob is not the surviving owner")
	}
}

// TestRemoveMemberCrossTenantNoLeak covers §16: a user of another tenant cannot
// remove a member or even read the member list — 403 membership_required, never
// a 404 that would reveal the workspace's existence; no state changes.
func TestRemoveMemberCrossTenantNoLeak(t *testing.T) {
	f := setup(t)
	space := f.createSpace("Team", "team", "space-create")
	sid := space.S("id")
	_, bobID := f.registerUser(t, "bob@example.com", "Bob")
	f.enrollByEmail(sid, "bob@example.com", "enroll-bob")

	other, e := f.store.Bootstrap(context.Background(), "Other", "corp", "other", "Other")
	must(t, e)
	original := f.tid
	f.tid = other.S("tenantId")
	o := f.call("DELETE", f.path("/spaces/"+sid+"/members/"+bobID), core.Object{"version": 1}, "other-remove", 403)
	if o.S("code") != "membership_required" {
		t.Fatalf("cross-tenant remove: want membership_required got %v", o)
	}
	f.call("GET", f.path("/spaces/"+sid+"/members"), nil, "", 403)
	f.tid = original

	// No state changed: bob is still an active member of the workspace.
	if f.workspaceMemberCount(sid, bobID) != 1 {
		t.Fatal("cross-tenant attempt altered membership")
	}
}
