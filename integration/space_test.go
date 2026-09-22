package integration

import (
	"context"
	"testing"

	"github.com/golang-jwt/jwt/v5"

	"github.com/wanglongan587/cloud/internal/core"
)

// addUser creates a local user through /me and joins them to the tenant as a
// member, returning their claims and user id. The default-space sync makes
// them a default-space member (admins become owners).
func (f *fixture) addUser(t *testing.T, subject, display string) (core.Claims, string) {
	t.Helper()
	gw := core.Claims{RegisteredClaims: jwt.RegisteredClaims{Subject: "gateway-a"}}
	u := core.Claims{RegisteredClaims: jwt.RegisteredClaims{Subject: subject}, Source: "corp", DisplayName: display}
	o, status, e := f.client.Call(context.Background(), "GET", "/api/v1/me", "gateway", gw, &u, "", nil)
	must(t, e)
	if status != 200 {
		t.Fatalf("addUser %s: want 200 got %d", subject, status)
	}
	f.call("PUT", f.path("/members/"+o.S("id")), core.Object{"role": "member", "status": "active", "version": 0}, "", 200)
	return u, o.S("id")
}

// createSpace builds a collaboration space owned by the fixture's alice.
func (f *fixture) createSpace(name, slug, key string) core.Object {
	return f.call("POST", f.path("/spaces"), core.Object{"name": name, "slug": slug, "description": ""}, key, 200)
}

// TestSpaceLifecycleMembershipAndOwnerInvariants covers scenarios 1, 5-8, 13, 14.
func TestSpaceLifecycleMembershipAndOwnerInvariants(t *testing.T) {
	f := setup(t)
	gw := core.Claims{RegisteredClaims: jwt.RegisteredClaims{Subject: "gateway-a"}}

	// Scenario 1: creator becomes owner; slug is normalized and immutably stored.
	space := f.createSpace("Collab", "Collab-Space", "space-create")
	sid := space.S("id")
	if space.S("slug") != "collab-space" || space.S("name") != "Collab" || space.S("archivedAt") != "" {
		t.Fatalf("unexpected space: %v", space)
	}
	if f.scalar("SELECT count(*) FROM collab_workspace_members WHERE workspace_id=$1 AND user_id=$2 AND role='owner' AND status='active'", sid, f.uid) != 1 {
		t.Fatal("creator is not owner")
	}
	// Slug conflicts and malformed slugs are rejected.
	f.call("POST", f.path("/spaces"), core.Object{"name": "Dup", "slug": "collab-space"}, "space-dup", 409)
	f.call("POST", f.path("/spaces"), core.Object{"name": "Bad", "slug": "Bad Slug!"}, "space-bad", 400)

	bob, bobID := f.addUser(t, "bob", "Bob")
	f.call("PUT", f.path("/spaces/"+sid+"/members/"+bobID), core.Object{"role": "member", "status": "active", "version": 0}, "", 200)

	// Scenario 6: a member cannot add members.
	carol, carolID := f.addUser(t, "carol", "Carol")
	_, status, e := f.client.Call(context.Background(), "PUT", f.path("/spaces/"+sid+"/members/"+carolID), "gateway", gw, &bob, "", core.Object{"role": "member", "status": "active", "version": 0})
	must(t, e)
	if status != 403 {
		t.Fatalf("member added a member: want 403 got %d", status)
	}
	// A member cannot self-promote.
	_, status, e = f.client.Call(context.Background(), "PUT", f.path("/spaces/"+sid+"/members/"+bobID), "gateway", gw, &bob, "", core.Object{"role": "admin", "status": "active", "version": 1})
	must(t, e)
	if status != 403 {
		t.Fatalf("member self-promoted: want 403 got %d", status)
	}
	// Scenario 5: admin can add members.
	_, status, e = f.client.Call(context.Background(), "PUT", f.path("/spaces/"+sid+"/members/"+bobID), "gateway", gw, &f.user, "", core.Object{"role": "admin", "status": "active", "version": 1})
	must(t, e)
	if status != 200 {
		t.Fatalf("owner promoted member: want 200 got %d", status)
	}
	_, status, e = f.client.Call(context.Background(), "PUT", f.path("/spaces/"+sid+"/members/"+carolID), "gateway", gw, &bob, "", core.Object{"role": "member", "status": "active", "version": 0})
	must(t, e)
	if status != 200 {
		t.Fatalf("admin added member: want 200 got %d", status)
	}

	// Scenarios 7-8: the last owner can never be demoted, by admin or by owner.
	_, status, e = f.client.Call(context.Background(), "PUT", f.path("/spaces/"+sid+"/members/"+f.uid), "gateway", gw, &bob, "", core.Object{"role": "member", "status": "active", "version": 1})
	must(t, e)
	if status != 409 {
		t.Fatalf("admin demoted last owner: want 409 got %d", status)
	}
	f.call("PUT", f.path("/spaces/"+sid+"/members/"+f.uid), core.Object{"role": "member", "status": "active", "version": 1}, "", 409)
	// An owner grant by an owner is allowed; a second owner can demote the first.
	_, status, e = f.client.Call(context.Background(), "PUT", f.path("/spaces/"+sid+"/members/"+bobID), "gateway", gw, &f.user, "", core.Object{"role": "owner", "status": "active", "version": 2})
	must(t, e)
	if status != 200 {
		t.Fatalf("owner granted owner: want 200 got %d", status)
	}
	_, status, e = f.client.Call(context.Background(), "PUT", f.path("/spaces/"+sid+"/members/"+f.uid), "gateway", gw, &f.user, "", core.Object{"role": "member", "status": "active", "version": 1})
	must(t, e)
	if status != 200 {
		t.Fatalf("owner demoted with second owner present: want 200 got %d", status)
	}

	// Scenario 13: lists contain only joined spaces.
	list := f.call("GET", f.path("/spaces"), nil, "", 200)
	joined := map[string]bool{}
	for _, item := range list["items"].([]any) {
		joined[core.Object(item.(map[string]any)).S("id")] = true
	}
	if !joined[sid] || len(joined) != 2 {
		t.Fatalf("alice list wrong: %v", joined)
	}
	_, status, e = f.client.Call(context.Background(), "GET", f.path("/spaces"), "gateway", gw, &carol, "", nil)
	must(t, e)
	if status != 200 {
		t.Fatalf("member list: want 200 got %d", status)
	}

	// Scenario 14: archive is owner-only and removes the space from lists.
	// bob is the sole owner after alice's demotion; alice (member) cannot archive.
	_, status, e = f.client.Call(context.Background(), "DELETE", f.path("/spaces/"+sid), "gateway", gw, &f.user, "alice-archive", core.Object{"version": space.N("version")})
	must(t, e)
	if status != 403 {
		t.Fatalf("member archived space: want 403 got %d", status)
	}
	_, status, e = f.client.Call(context.Background(), "DELETE", f.path("/spaces/"+sid), "gateway", gw, &bob, "bob-archive", core.Object{"version": space.N("version")})
	must(t, e)
	if status != 200 {
		t.Fatalf("owner archive: want 200 got %d", status)
	}
	archived := f.call("GET", f.path("/spaces/"+sid), nil, "", 404)
	if archived.S("archivedAt") != "" {
		t.Fatal("archived space leaked content")
	}
	list = f.call("GET", f.path("/spaces"), nil, "", 200)
	for _, item := range list["items"].([]any) {
		if core.Object(item.(map[string]any)).S("id") == sid {
			t.Fatal("archived space still listed")
		}
	}
	_, status, e = f.client.Call(context.Background(), "GET", f.path("/spaces/"+sid), "gateway", gw, &bob, "", nil)
	must(t, e)
	if status != 404 {
		t.Fatalf("archived space readable: want 404 got %d", status)
	}
}
