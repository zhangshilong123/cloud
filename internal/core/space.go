package core

import (
	"regexp"
	"strings"
)

// Collab workspaces (product term "Workspace") are collaboration and visibility
// boundaries inside a tenant. They are distinct from the runtime `workspaces`
// table, which models execution environments. Every project belongs to exactly
// one space; access to projects and spaces always passes the membership check
// in spaceMember. Tenant membership stays the precondition, roles stay
// independent (TenantMember != SpaceMember).

var slugPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,63}$`)

// validSlug rejects uppercase, empty, and malformed workspace slugs.
func validSlug(s string) bool { return slugPattern.MatchString(s) }

// spaceMember verifies the user is an active member of a live, unarchived space
// whose tenant is active, and that the user still holds an active tenant
// membership. It returns the membership row; the caller decides role checks.
func spaceMember(t *transaction, spaceID, uid string) Object {
	require(validID(spaceID) && validID(uid), 404, "not_found")
	m := t.one(`SELECT wm.* FROM collab_workspace_members wm
JOIN collab_workspaces w ON w.id=wm.workspace_id
JOIN tenant_memberships tm ON tm.tenant_id=w.tenant_id AND tm.user_id=wm.user_id
JOIN users u ON u.id=wm.user_id
JOIN tenants tn ON tn.id=w.tenant_id
WHERE wm.workspace_id=$1 AND wm.user_id=$2 AND wm.status='active'
AND w.archived_at IS NULL
AND tm.status='active' AND u.status='active' AND u.deleted_at IS NULL
AND tn.status='active' AND tn.deleted_at IS NULL`, spaceID, uid)
	require(m != nil, 404, "not_found")
	return m
}

// requireSpaceRole verifies a member carries at least one of the given roles.
func requireSpaceRole(m Object, roles ...string) {
	for _, role := range roles {
		if m.S("role") == role {
			return
		}
	}
	reject(403, "space_role_required")
}

// defaultSpace returns the tenant's default collaboration space, which
// Bootstrap and migration 0006 guarantee exists.
func defaultSpace(t *transaction, tid string) Object {
	w := t.one("SELECT * FROM collab_workspaces WHERE tenant_id=$1 AND slug='default' AND archived_at IS NULL", tid)
	require(w != nil, 404, "not_found")
	return w
}

// createSpace atomically inserts a workspace and its first owner.
func createSpace(t *transaction, r *PublicRequest, uid string) Object {
	name := validText(r.Body.S("name"), 128)
	slug := strings.ToLower(strings.TrimSpace(r.Body.S("slug")))
	require(validSlug(slug), 400, "invalid_slug")
	description := r.Body.S("description")
	require(len(description) <= 2000, 400, "invalid_input")
	require(t.one("SELECT id FROM collab_workspaces WHERE tenant_id=$1 AND slug=$2", r.TenantID, slug) == nil, 409, "space_slug_conflict")
	id := newID()
	t.exec("INSERT INTO collab_workspaces(id,tenant_id,name,slug,description,created_by) VALUES($1,$2,$3,$4,$5,$6)", id, r.TenantID, name, slug, description, uid)
	t.exec("INSERT INTO collab_workspace_members(workspace_id,user_id,role,status,created_by) VALUES($1,$2,'owner','active',$2)", id, uid)
	return t.one("SELECT * FROM collab_workspaces WHERE id=$1", id)
}

// listSpaces returns the live spaces the caller joined, newest membership first.
func listSpaces(t *transaction, r *PublicRequest, uid string) Object {
	return page(t, "SELECT w.*, wm.role FROM collab_workspaces w JOIN collab_workspace_members wm ON wm.workspace_id=w.id WHERE w.tenant_id=$1 AND wm.user_id=$2 AND wm.status='active' AND w.archived_at IS NULL", []any{r.TenantID, uid}, "w.id", r)
}

// patchSpace updates name and description; slug is immutable. admin or owner only.
func patchSpace(t *transaction, r *PublicRequest, uid string) Object {
	w := t.one("SELECT * FROM collab_workspaces WHERE id=$1 AND tenant_id=$2", r.SpaceID, r.TenantID)
	require(w != nil, 404, "not_found")
	m := spaceMember(t, r.SpaceID, uid)
	requireSpaceRole(m, "admin", "owner")
	version(w, r.Body.N("version"))
	name := validText(r.Body.S("name"), 128)
	description := r.Body.S("description")
	require(len(description) <= 2000, 400, "invalid_input")
	t.exec("UPDATE collab_workspaces SET name=$2,description=$3,version=version+1,updated_at=now() WHERE id=$1", r.SpaceID, name, description)
	return t.one("SELECT * FROM collab_workspaces WHERE id=$1", r.SpaceID)
}

// archiveSpace soft-deletes the workspace; owner only. Projects are unaffected
// and keep their own lifecycle state machine.
func archiveSpace(t *transaction, r *PublicRequest, uid string) Object {
	w := t.one("SELECT * FROM collab_workspaces WHERE id=$1 AND tenant_id=$2", r.SpaceID, r.TenantID)
	require(w != nil, 404, "not_found")
	m := spaceMember(t, r.SpaceID, uid)
	requireSpaceRole(m, "owner")
	version(w, r.Body.N("version"))
	t.exec("UPDATE collab_workspaces SET archived_at=now(),version=version+1,updated_at=now() WHERE id=$1", r.SpaceID)
	return t.one("SELECT * FROM collab_workspaces WHERE id=$1", r.SpaceID)
}

// enrollSpaceMemberByEmail adds an already-registered user to the space as a
// plain member, resolved by (source, normalized email) in the caller's identity
// source. It never auto-creates a user and never invites; an unknown address is
// 404 user_not_registered. Admin or owner may enroll; the target is atomically
// ensured a tenant membership (keeping an existing role) if they are not
// already a tenant member. Adding an existing member returns the current
// membership unchanged — idempotent, no role/status/version mutation.
func enrollSpaceMemberByEmail(t *transaction, r *PublicRequest, uid string) Object {
	actor := spaceMember(t, r.SpaceID, uid)
	requireSpaceRole(actor, "admin", "owner")
	email := normalizeEmail(r.Body.S("email"))
	require(validEmail(email), 400, "invalid_email")
	u := t.one(`SELECT u.id FROM users u
JOIN user_identities i ON i.user_id=u.id
WHERE i.source=$1 AND i.subject=$2 AND u.status='active' AND u.deleted_at IS NULL`, r.Identity.Source, email)
	require(u != nil, 404, "user_not_registered")
	target := u.S("id")
	// Atomic with the workspace membership below: a registered user who is not yet
	// a tenant member is enrolled into the tenant first, then into the space. The
	// ON CONFLICT keeps an existing tenant role (admin stays admin) untouched.
	t.exec("INSERT INTO tenant_memberships(tenant_id,user_id,role,status) VALUES($1,$2,'member','active') ON CONFLICT (tenant_id,user_id) DO NOTHING", r.TenantID, target)
	if t.one("SELECT * FROM collab_workspace_members WHERE workspace_id=$1 AND user_id=$2", r.SpaceID, target) == nil {
		t.exec("INSERT INTO collab_workspace_members(workspace_id,user_id,role,status,created_by) VALUES($1,$2,'member','active',$3)", r.SpaceID, target, uid)
	}
	return t.one("SELECT * FROM collab_workspace_members WHERE workspace_id=$1 AND user_id=$2", r.SpaceID, target)
}

// putSpaceMember upserts one membership with optimistic version checks and the
// last-owner invariant. Adding members needs admin or owner; granting owner
// needs owner. The target user must be an active member of the same tenant.
func putSpaceMember(t *transaction, r *PublicRequest, uid string) Object {
	actor := spaceMember(t, r.SpaceID, uid)
	requireSpaceRole(actor, "admin", "owner")
	role, status := r.Body.S("role"), r.Body.S("status")
	require((role == "owner" || role == "admin" || role == "member") && (status == "active" || status == "disabled"), 400, "invalid_member")
	if role == "owner" {
		requireSpaceRole(actor, "owner")
	}
	require(validID(r.UserID), 400, "invalid_user")
	require(t.one(`SELECT u.id FROM users u
JOIN tenant_memberships tm ON tm.user_id=u.id
WHERE u.id=$1 AND tm.tenant_id=$2 AND tm.status='active' AND u.status='active' AND u.deleted_at IS NULL`, r.UserID, r.TenantID) != nil, 404, "not_found")
	w := t.one("SELECT * FROM collab_workspaces WHERE id=$1 AND tenant_id=$2 AND archived_at IS NULL", r.SpaceID, r.TenantID)
	require(w != nil, 404, "not_found")
	old := t.one("SELECT * FROM collab_workspace_members WHERE workspace_id=$1 AND user_id=$2", r.SpaceID, r.UserID)
	if old != nil {
		version(old, r.Body.N("version"))
		if old.S("role") == "owner" && old.S("status") == "active" && (role != "owner" || status != "active") {
			require(t.one("SELECT user_id FROM collab_workspace_members WHERE workspace_id=$1 AND user_id<>$2 AND role='owner' AND status='active'", r.SpaceID, r.UserID) != nil, 409, "space_last_owner")
		}
		t.exec("UPDATE collab_workspace_members SET role=$3,status=$4,version=version+1 WHERE workspace_id=$1 AND user_id=$2", r.SpaceID, r.UserID, role, status)
	} else {
		require(r.Body.N("version") == 0, 409, "version_conflict")
		t.exec("INSERT INTO collab_workspace_members(workspace_id,user_id,role,status,created_by) VALUES($1,$2,$3,$4,$5)", r.SpaceID, r.UserID, role, status, uid)
	}
	return t.one("SELECT * FROM collab_workspace_members WHERE workspace_id=$1 AND user_id=$2", r.SpaceID, r.UserID)
}

// removeSpaceMember removes a member's workspace membership (hard delete, owner
// only). The owner role is governed by the same last-owner invariant as role
// changes: an owner may only be removed while another active owner remains,
// otherwise 409 space_last_owner. The user account, tenant membership and any
// resources they created are untouched: the workspace and its projects remain,
// and the removed member's access to the workspace, its projects and their
// runtime workspaces is revoked by the membership gate.
func removeSpaceMember(t *transaction, r *PublicRequest, uid string) Object {
	actor := spaceMember(t, r.SpaceID, uid)
	requireSpaceRole(actor, "owner")
	require(validID(r.UserID), 400, "invalid_user")
	require(t.one(`SELECT u.id FROM users u
JOIN tenant_memberships tm ON tm.user_id=u.id
WHERE u.id=$1 AND tm.tenant_id=$2 AND tm.status='active' AND u.status='active' AND u.deleted_at IS NULL`, r.UserID, r.TenantID) != nil, 404, "not_found")
	old := t.one("SELECT * FROM collab_workspace_members WHERE workspace_id=$1 AND user_id=$2", r.SpaceID, r.UserID)
	require(old != nil, 404, "not_found")
	version(old, r.Body.N("version"))
	if old.S("role") == "owner" && old.S("status") == "active" {
		require(t.one("SELECT user_id FROM collab_workspace_members WHERE workspace_id=$1 AND user_id<>$2 AND role='owner' AND status='active'", r.SpaceID, r.UserID) != nil, 409, "space_last_owner")
	}
	t.exec("DELETE FROM collab_workspace_members WHERE workspace_id=$1 AND user_id=$2", r.SpaceID, r.UserID)
	return old
}

// projectInSpace loads a live project in the tenant and verifies the caller's
// space membership, returning both rows. Entity routes derive the space from
// the project itself; client-supplied space identifiers are never trusted.
func projectInSpace(t *transaction, tid, uid, pid string) (project, membership Object) {
	require(validID(pid), 404, "not_found")
	p := t.one("SELECT * FROM projects WHERE id=$1 AND tenant_id=$2 AND deleted_at IS NULL", pid, tid)
	require(p != nil, 404, "not_found")
	return p, spaceMember(t, p.S("spaceId"), uid)
}
