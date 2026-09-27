package core

// Minimal durable Agent and AgentSkillBinding authority (Phase 4 of
// specs/decisions/cloud/agent/0-agent-skill-binding.md).
//
// This file owns the workspace-scoped Agent resource (create/read/list/patch/archive) and the
// mutable AgentSkillBinding configuration (list/attach/enable-disable/detach). It resolves the
// Skill-selection authority future Execution admission will consume (D8), but it implements NO
// Execution/Attempt/ExecutionSkillBinding surface and no capability/signed-URL/object-locator
// field (D13/D14): an Agent request carries an agent_id, never a Skill id collection, and the
// snapshot of exact revisions is a later phase's job.

// agentSpace verifies the caller is an active member of a live Collaboration Workspace, and
// optionally an owner/admin for writes. Non-member → 404 (no existence leak, D11).
func agentSpace(t *transaction, spaceID, uid string, write bool) {
	role := workspaceRole(t, spaceID, uid)
	require(role != "", 404, "not_found")
	if write {
		require(role == "owner" || role == "admin", 403, "workspace_admin_required")
	}
}

// agent loads a live Agent within its owning Collaboration Workspace and applies the D11
// authorization (member+ read; owner/admin write; non-member/foreign-workspace → 404).
func agent(t *transaction, spaceID, agentID, uid string, write bool) Object {
	require(validID(agentID), 404, "not_found")
	a := t.one("SELECT * FROM agents WHERE id=$1 AND workspace_id=$2 AND deleted_at IS NULL", agentID, spaceID)
	require(a != nil, 404, "not_found")
	if write {
		agentSpace(t, spaceID, uid, true)
		return a
	}
	agentSpace(t, spaceID, uid, false)
	return a
}

// createAgent inserts an Agent owned by the Collaboration Workspace. active names are unique per
// workspace (partial unique index agent_active_name_uniq), so a duplicate name is 409.
func createAgent(t *transaction, r *PublicRequest, uid string) Object {
	agentSpace(t, r.SpaceID, uid, true)
	name := validText(r.Body.S("name"), 128)
	require(t.one("SELECT id FROM agents WHERE workspace_id=$1 AND name=$2 AND deleted_at IS NULL", r.SpaceID, name) == nil, 409, "agent_name_conflict")
	id := newID()
	if t.execRows("INSERT INTO agents(id, workspace_id, name, created_by) VALUES($1,$2,$3,$4) ON CONFLICT (workspace_id, name) WHERE deleted_at IS NULL DO NOTHING", id, r.SpaceID, name, uid) == 0 {
		reject(409, "agent_name_conflict")
	}
	return t.one("SELECT * FROM agents WHERE id=$1", id)
}

// listAgents pages the live Agents of a Collaboration Workspace (member+).
func listAgents(t *transaction, r *PublicRequest, uid string) Object {
	agentSpace(t, r.SpaceID, uid, false)
	return page(t, "SELECT * FROM agents WHERE workspace_id=$1 AND deleted_at IS NULL", []any{r.SpaceID}, "id", r)
}

// getAgent reads one live Agent (member+).
func getAgent(t *transaction, r *PublicRequest, uid string) Object {
	return agent(t, r.SpaceID, r.AgentID, uid, false)
}

// patchAgent updates name and/or status (active|disabled). Either field may be omitted; omitted
// fields keep their current value. Owner/admin only; requires a matching version.
func patchAgent(t *transaction, r *PublicRequest, uid string) Object {
	a := agent(t, r.SpaceID, r.AgentID, uid, true)
	version(a, r.Body.N("version"))
	name, status := a.S("name"), a.S("status")
	if v := r.Body.S("name"); v != "" {
		name = validText(v, 128)
		require(t.one("SELECT id FROM agents WHERE workspace_id=$1 AND name=$2 AND deleted_at IS NULL AND id<>$3", r.SpaceID, name, r.AgentID) == nil, 409, "agent_name_conflict")
	}
	if v := r.Body.S("status"); v != "" {
		require(v == "active" || v == "disabled", 400, "invalid_input")
		status = v
	}
	t.exec("UPDATE agents SET name=$2, status=$3, version=version+1, updated_at=now() WHERE id=$1", r.AgentID, name, status)
	return t.one("SELECT * FROM agents WHERE id=$1", r.AgentID)
}

// archiveAgent soft-deletes an Agent. The unified delete rule applies: the creator may always
// delete their own Agent; otherwise the actor must be a workspace owner/admin (D11). A soft-deleted
// Agent denies future Executions; historical state is untouched.
func archiveAgent(t *transaction, r *PublicRequest, uid string) Object {
	a := agent(t, r.SpaceID, r.AgentID, uid, false)
	require(workspaceCanDelete(t, r.SpaceID, uid, a.S("createdBy")), 403, "space_role_required")
	version(a, r.Body.N("version"))
	t.exec("UPDATE agents SET deleted_at=now(), version=version+1, updated_at=now() WHERE id=$1", r.AgentID)
	return t.one("SELECT * FROM agents WHERE id=$1", r.AgentID)
}

// agentSkillBinding returns the enriched binding row (binding + Skill canonical_name/display_name)
// for one (agent, skill). It only joins — it never resolves a revision/digest/locator (D3).
func agentSkillBinding(t *transaction, agentID, skillID string) Object {
	return t.one(`SELECT ab.agent_id, ab.skill_id, ab.enabled, ab.version, ab.created_at, ab.updated_at,
		s.canonical_name, s.display_name
		FROM agent_skill_bindings ab JOIN skills s ON s.id=ab.skill_id
		WHERE ab.agent_id=$1 AND ab.skill_id=$2`, agentID, skillID)
}

// listAgentSkills pages the Agent's bindings (all of them, including bindings to a soft-deleted
// Skill, so stale configuration stays observable and detachable). member+. The list cursor is the
// bound Skill id, aliased to `id` so the window() cursor matches the pagination column — the same
// pattern as the members endpoints.
func listAgentSkills(t *transaction, r *PublicRequest, uid string) Object {
	agent(t, r.SpaceID, r.AgentID, uid, false)
	return page(t, `SELECT ab.skill_id AS id, ab.agent_id, ab.skill_id, ab.enabled, ab.version, ab.created_at, ab.updated_at,
		s.canonical_name, s.display_name
		FROM agent_skill_bindings ab JOIN skills s ON s.id=ab.skill_id
		WHERE ab.agent_id=$1`, []any{r.AgentID}, "ab.skill_id", r)
}

// attachAgentSkill adds a binding (enabled=true) referencing a Skill's business identity (D3/D4).
// The Skill must be live and in the same Collaboration Workspace. A duplicate (agent, skill) is 409
// binding_exists (a same-idempotency-key resubmission replays).
func attachAgentSkill(t *transaction, r *PublicRequest, uid string) Object {
	agent(t, r.SpaceID, r.AgentID, uid, true)
	skillID := r.Body.S("skillId")
	require(validID(skillID), 404, "not_found")
	skill := t.one("SELECT * FROM skills WHERE id=$1 AND deleted_at IS NULL", skillID)
	require(skill != nil, 404, "not_found")
	require(skill.S("workspaceId") == r.SpaceID, 404, "not_found")
	require(t.one("SELECT skill_id FROM agent_skill_bindings WHERE agent_id=$1 AND skill_id=$2", r.AgentID, skillID) == nil, 409, "binding_exists")
	t.exec("INSERT INTO agent_skill_bindings(agent_id, skill_id) VALUES($1,$2)", r.AgentID, skillID)
	return agentSkillBinding(t, r.AgentID, skillID)
}

// putAgentSkill enables/disables a binding (enabled=true/false). enabled must be present; requires
// a matching version (428 missing / 409 conflict). disable keeps the row as configuration, excluded
// from effective execution selection (D5).
func putAgentSkill(t *transaction, r *PublicRequest, uid string) Object {
	agent(t, r.SpaceID, r.AgentID, uid, true)
	require(validID(r.SkillID), 404, "not_found")
	b := t.one("SELECT * FROM agent_skill_bindings WHERE agent_id=$1 AND skill_id=$2", r.AgentID, r.SkillID)
	require(b != nil, 404, "binding_not_found")
	version(b, r.Body.N("version"))
	enabled, present := r.Body["enabled"].(bool)
	require(present, 400, "invalid_input")
	t.exec("UPDATE agent_skill_bindings SET enabled=$3, version=version+1, updated_at=now() WHERE agent_id=$1 AND skill_id=$2", r.AgentID, r.SkillID, enabled)
	return agentSkillBinding(t, r.AgentID, r.SkillID)
}

// detachAgentSkill removes a binding (hard delete, no version — D5). An absent binding is 404
// binding_not_found; a same-idempotency-key resubmission replays the original detach.
func detachAgentSkill(t *transaction, r *PublicRequest, uid string) Object {
	agent(t, r.SpaceID, r.AgentID, uid, true)
	require(validID(r.SkillID), 404, "not_found")
	require(t.one("SELECT skill_id FROM agent_skill_bindings WHERE agent_id=$1 AND skill_id=$2", r.AgentID, r.SkillID) != nil, 404, "binding_not_found")
	b := agentSkillBinding(t, r.AgentID, r.SkillID)
	t.exec("DELETE FROM agent_skill_bindings WHERE agent_id=$1 AND skill_id=$2", r.AgentID, r.SkillID)
	return b
}

// enabledAgentSkillBindings is the Skill-selection read seam for future Execution admission
// (Execution snapshot ADR D3 step 2 / Agent ADR D8/D9). It returns the enabled durable bindings for
// an Agent joined to their live (non-soft-deleted) Skill, deterministically ordered by the bound
// Skill's canonical_name then skill_id (D10). It resolves NO revision/digest/locator — exact-revision
// freezing is the snapshot's job, not this mutable-config read.
func enabledAgentSkillBindings(t *transaction, agentID string) []Object { //nolint:unused // Phase 5 Execution admission is the sole caller (Agent ADR D8/D9).
	return t.list(`SELECT ab.agent_id, ab.skill_id, ab.enabled, s.canonical_name, s.display_name
		FROM agent_skill_bindings ab
		JOIN skills s ON s.id=ab.skill_id
		WHERE ab.agent_id=$1 AND ab.enabled=true AND s.deleted_at IS NULL
		ORDER BY s.canonical_name, s.id`, agentID)
}
