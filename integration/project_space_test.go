package integration

import (
	"context"
	"testing"

	"github.com/golang-jwt/jwt/v5"

	"github.com/wanglongan587/cloud/internal/core"
)

// TestProjectSpaceScopingAndRoleGates covers scenarios 2-4, 9-12.
func TestProjectSpaceScopingAndRoleGates(t *testing.T) {
	f := setup(t)
	gw := core.Claims{RegisteredClaims: jwt.RegisteredClaims{Subject: "gateway-a"}}
	space := f.createSpace("Scoped", "scoped", "space-scoped")
	sid := space.S("id")
	bob, bobID := f.addUser(t, "bob", "Bob")
	f.call("PUT", f.path("/spaces/"+sid+"/members/"+bobID), core.Object{"role": "member", "status": "active", "version": 0}, "", 200)
	carol, _ := f.addUser(t, "carol", "Carol") // default space only

	// Scenario 2: a member creates a project inside the space.
	_, status, e := f.client.Call(context.Background(), "POST", f.path("/spaces/"+sid+"/projects"), "gateway", gw, &bob, "bob-project", core.Object{"name": "Shared", "repositoryUrl": "https://example.invalid/repo.git", "defaultBranch": "main"})
	must(t, e)
	if status != 202 {
		t.Fatalf("member create project: want 202 got %d", status)
	}
	// Space projects are visible to members and isolated from non-members.
	list := f.call("GET", f.path("/spaces/"+sid+"/projects"), nil, "", 200)
	items := list["items"].([]any)
	if len(items) != 1 {
		t.Fatalf("space project list: want 1 got %d", len(items))
	}
	pid := core.Object(items[0].(map[string]any)).S("id")
	if core.Object(items[0].(map[string]any)).S("spaceId") != sid {
		t.Fatal("project missing spaceId")
	}
	// Scenario 3/11: a non-member of this space cannot read the project.
	_, status, e = f.client.Call(context.Background(), "GET", f.path("/projects/"+pid), "gateway", gw, &carol, "", nil)
	must(t, e)
	if status != 404 {
		t.Fatalf("non-member read: want 404 got %d", status)
	}
	// Scenario 4: a member can update business content.
	p := f.call("GET", f.path("/projects/"+pid), nil, "", 200)
	_, status, e = f.client.Call(context.Background(), "PATCH", f.path("/projects/"+pid), "gateway", gw, &bob, "", core.Object{"name": "Shared Renamed", "version": p.N("version")})
	must(t, e)
	if status != 200 {
		t.Fatalf("member patch: want 200 got %d", status)
	}
	// Scenario 12: optimistic conflict returns 409.
	_, status, e = f.client.Call(context.Background(), "PATCH", f.path("/projects/"+pid), "gateway", gw, &bob, "", core.Object{"name": "Stale", "version": p.N("version")})
	must(t, e)
	if status != 409 {
		t.Fatalf("stale patch: want 409 got %d", status)
	}

	// Scenario 9: a member cannot delete the project.
	after := f.call("GET", f.path("/projects/"+pid), nil, "", 200)
	_, status, e = f.client.Call(context.Background(), "DELETE", f.path("/projects/"+pid), "gateway", gw, &bob, "bob-delete", core.Object{"version": after.N("version")})
	must(t, e)
	if status != 403 {
		t.Fatalf("member delete project: want 403 got %d", status)
	}

	// Scenario 10: an owner deletes through the existing lifecycle state machine.
	f.drain()
	active := f.call("GET", f.path("/projects/"+pid), nil, "", 200)
	if active.S("lifecycle") != "active" {
		t.Fatalf("project did not reach active: %s", active.S("lifecycle"))
	}
	deleted := f.call("DELETE", f.path("/projects/"+pid), core.Object{"version": active.N("version")}, "owner-delete", 202)
	if deleted.O("resource").S("lifecycle") != "deleting" || deleted.O("operation").S("id") == "" {
		t.Fatal("owner delete did not enter lifecycle state machine")
	}
	f.drain()
	if f.scalar("SELECT count(*) FROM projects WHERE id=$1 AND deleted_at IS NULL", pid) != 0 {
		t.Fatal("delete lifecycle did not complete")
	}
}

// TestSpaceMemberConcurrentPatchAndLastOwnerRace mirrors the tenant last-admin
// protection for spaces: concurrent demotion of the last owner must fail.
func TestSpaceMemberLastOwnerRace(t *testing.T) {
	f := setup(t)
	gw := core.Claims{RegisteredClaims: jwt.RegisteredClaims{Subject: "gateway-a"}}
	space := f.createSpace("Race", "race", "space-race")
	sid := space.S("id")
	bob, bobID := f.addUser(t, "bob", "Bob")
	f.call("PUT", f.path("/spaces/"+sid+"/members/"+bobID), core.Object{"role": "owner", "status": "active", "version": 0}, "", 200)
	// Two owners; demoting both concurrently must leave exactly one owner.
	subjects := []core.Claims{f.user, bob}
	ids := []string{f.uid, bobID}
	statuses := make(chan int, 2)
	for i := range subjects {
		go func(i int) {
			_, status, _ := f.client.Call(context.Background(), "PUT", f.path("/spaces/"+sid+"/members/"+ids[i]), "gateway", gw, &subjects[i], "", core.Object{"role": "member", "status": "active", "version": 1})
			statuses <- status
		}(i)
	}
	success, conflict := 0, 0
	for range 2 {
		switch <-statuses {
		case 200:
			success++
		case 409:
			conflict++
		}
	}
	if success != 1 || conflict != 1 {
		t.Fatalf("last owner race: success=%d conflict=%d", success, conflict)
	}
	if f.scalar("SELECT count(*) FROM collab_workspace_members WHERE workspace_id=$1 AND role='owner' AND status='active'", sid) != 1 {
		t.Fatal("space lost its last owner")
	}
}
