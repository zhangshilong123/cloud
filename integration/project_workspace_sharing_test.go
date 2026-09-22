package integration

import (
	"context"
	"testing"

	"github.com/wanglongan587/cloud/internal/core"
)

// projectSharingFixture builds one workspace with the role spread used by every
// project workspace-sharing test (Step 3): alice (fixture user) = owner,
// bob = plain member, carol = admin (promoted via the role PUT), dave = tenant
// member but NOT a member of the space. It returns the space id and the three
// user claims (alice is f.user, acted via f.call).
func projectSharingFixture(t *testing.T) (f *fixture, sid string, bob, carol, dave core.Claims) {
	t.Helper()
	f = setup(t)
	space := f.createSpace("Team", "team", "space-create")
	sid = space.S("id")

	bob, _ = f.registerUser(t, "bob@example.com", "Bob")
	f.enrollByEmail(sid, "bob@example.com", "enroll-bob")

	carol, carolID := f.registerUser(t, "carol@example.com", "Carol")
	f.enrollByEmail(sid, "carol@example.com", "enroll-carol")
	f.call("PUT", f.path("/spaces/"+sid+"/members/"+carolID), core.Object{"role": "admin", "status": "active", "version": 1}, "", 200)

	dave, _ = f.addUser(t, "dave", "Dave") // tenant member, not a member of `sid`
	return f, sid, bob, carol, dave
}

// TestScopedProjectSharedWithWorkspaceMembers covers §14: a space-scoped project
// is visible to every active member of the workspace (the sharing boundary),
// while a non-member stays fully hidden — the space collection is gated, and the
// project and its runtime workspace are 404 (no existence leak).
func TestScopedProjectSharedWithWorkspaceMembers(t *testing.T) {
	f, sid, bob, _, dave := projectSharingFixture(t)

	// A (owner) creates a project inside the space.
	created := f.call("POST", f.path("/spaces/"+sid+"/projects"), core.Object{"name": "P", "repositoryUrl": "https://example.invalid/repo.git", "defaultBranch": "main"}, "project-create", 202)
	pid := created.O("resource").S("id")
	wid := created.O("workspace").S("id")

	// B (plain member) sees the project in the space list and can open it and its
	// runtime workspace.
	projects := f.callUser(t, bob, "GET", f.path("/spaces/"+sid+"/projects"), nil, "", 200)
	items := projects["items"].([]any)
	if len(items) != 1 || core.Object(items[0].(map[string]any)).S("id") != pid {
		t.Fatalf("B space project list: want A's project, got %v", projects)
	}
	f.callUser(t, bob, "GET", f.path("/projects/"+pid), nil, "", 200)
	f.callUser(t, bob, "GET", f.path("/workspaces/"+wid), nil, "", 200)

	// C (tenant member, not a space member) sees nothing: the space collection is
	// gated (404), and the project + runtime stay hidden (404, no existence leak).
	f.callUser(t, dave, "GET", f.path("/spaces/"+sid+"/projects"), nil, "", 404)
	f.callUser(t, dave, "GET", f.path("/projects/"+pid), nil, "", 404)
	f.callUser(t, dave, "GET", f.path("/workspaces/"+wid), nil, "", 404)
}

// TestScopedProjectDeletePolicy covers §15 as merged with upstream: project
// deletion is gated on the workspace role — only a workspace owner or admin may
// delete a project; an ordinary member cannot, even for a project they created
// (403 space_role_required). An admin may delete another member's project.
func TestScopedProjectDeletePolicy(t *testing.T) {
	f, sid, bob, carol, _ := projectSharingFixture(t)

	// A (owner) creates a project and lets it reach active.
	created := f.call("POST", f.path("/spaces/"+sid+"/projects"), core.Object{"name": "P", "repositoryUrl": "https://example.invalid/repo.git", "defaultBranch": "main"}, "project-create", 202)
	pid := created.O("resource").S("id")
	f.drain()
	active := f.callUser(t, f.user, "GET", f.path("/projects/"+pid), nil, "", 200)
	if active.S("lifecycle") != "active" {
		t.Fatalf("project did not reach active: %s", active.S("lifecycle"))
	}
	ver := active.N("version")

	// Ordinary member B cannot delete A's project: 403 space_role_required.
	denied := f.callUser(t, bob, "DELETE", f.path("/projects/"+pid), core.Object{"version": ver}, "member-delete", 403)
	if denied.S("code") != "space_role_required" {
		t.Fatalf("member delete: want space_role_required got %v", denied)
	}

	// B creates their own project P2; the creator rule is gone under the merged
	// semantics, so B (a plain member) still cannot delete it.
	p2 := f.callUser(t, bob, "POST", f.path("/spaces/"+sid+"/projects"), core.Object{"name": "P2", "repositoryUrl": "https://example.invalid/repo.git", "defaultBranch": "main"}, "bob-project", 202)
	p2id := p2.O("resource").S("id")
	f.drain()
	p2active := f.callUser(t, bob, "GET", f.path("/projects/"+p2id), nil, "", 200)
	f.callUser(t, bob, "DELETE", f.path("/projects/"+p2id), core.Object{"version": p2active.N("version")}, "bob-delete-own", 403)

	// Admin carol deletes A's project (still active; bob's deletes were rejected
	// and changed nothing).
	f.callUser(t, carol, "DELETE", f.path("/projects/"+pid), core.Object{"version": ver}, "admin-delete", 202)
	f.drain()
	if f.scalar("SELECT count(*) FROM projects WHERE id=$1 AND deleted_at IS NULL", pid) != 0 {
		t.Fatal("admin delete did not complete")
	}
}

// TestRuntimeWorkspaceInheritsProjectAccess covers §17: a runtime workspace
// inherits its parent project's access — B (workspace member) can open A's
// space-scoped project and its runtime workspace (including the workspaces list),
// while C (same-tenant non-member) cannot reach either.
func TestRuntimeWorkspaceInheritsProjectAccess(t *testing.T) {
	f, sid, bob, _, dave := projectSharingFixture(t)

	// A creates a space-scoped project; its main runtime workspace is shared.
	created := f.call("POST", f.path("/spaces/"+sid+"/projects"), core.Object{"name": "P", "repositoryUrl": "https://example.invalid/repo.git", "defaultBranch": "main"}, "project-create", 202)
	pid := created.O("resource").S("id")
	wid := created.O("workspace").S("id")

	// B accesses the project, its runtime workspace, and the workspaces list.
	f.callUser(t, bob, "GET", f.path("/projects/"+pid), nil, "", 200)
	f.callUser(t, bob, "GET", f.path("/workspaces/"+wid), nil, "", 200)
	ws := f.callUser(t, bob, "GET", f.path("/projects/"+pid+"/workspaces"), nil, "", 200)
	if len(ws["items"].([]any)) != 1 {
		t.Fatalf("B runtime workspace list: %v", ws)
	}

	// C (tenant member, not a space member) cannot access the project or runtime.
	f.callUser(t, dave, "GET", f.path("/projects/"+pid), nil, "", 404)
	f.callUser(t, dave, "GET", f.path("/workspaces/"+wid), nil, "", 404)
}

// TestScopedProjectCrossTenantIsolation covers §18: a user of another tenant gets
// 403 membership_required — never a 404 that would reveal the project's
// existence, even knowing its id; a same-tenant non-member is hidden with 404.
func TestScopedProjectCrossTenantIsolation(t *testing.T) {
	f, sid, _, _, dave := projectSharingFixture(t)

	created := f.call("POST", f.path("/spaces/"+sid+"/projects"), core.Object{"name": "P", "repositoryUrl": "https://example.invalid/repo.git", "defaultBranch": "main"}, "project-create", 202)
	pid := created.O("resource").S("id")

	// A user of a different tenant cannot see the project (403, no existence leak).
	other, e := f.store.Bootstrap(context.Background(), "Other", "corp", "other", "Other")
	must(t, e)
	original := f.tid
	f.tid = other.S("tenantId")
	f.call("GET", f.path("/projects/"+pid), nil, "", 403)
	f.tid = original

	// A same-tenant user who is not a workspace member is fully hidden (404).
	f.callUser(t, dave, "GET", f.path("/projects/"+pid), nil, "", 404)
}
