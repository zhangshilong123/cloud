package integration

// Minimal Agent & AgentSkillBinding authority (Phase 4B of
// specs/decisions/cloud/agent/0-agent-skill-binding.md), pinned end-to-end:
//
//   - migration 0017 adds agents + agent_skill_bindings with the ownership boundary that matches
//     Skills (collab_workspaces), mutable Agent / membership-style binding, immutable workspace
//     ownership, and active-name uniqueness;
//   - the HTTP surface (create/read/list/patch/archive + list/attach/enable-disable/detach) enforces
//     the D11 authorization (member read, owner/admin write, non-member 404 no-leak) and the D5
//     binding semantics (composite-PK attach 409 on duplicate, versioned enable/disable, idempotent
//     detach);
//   - the §17 authority read seam proves "Agent → enabled durable AgentSkillBinding → bound live
//     Skill → current_revision_id" is deterministically readable (D8/D9/D10): enabled bindings to
//     live Skills only, ordered by canonical_name then skill_id.

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
	"go.uber.org/zap"

	"github.com/wanglongan587/cloud/internal/api/router"
	"github.com/wanglongan587/cloud/internal/core"
	"github.com/wanglongan587/cloud/internal/simulator"
)

func insertAgent(t *testing.T, pool *sql.DB, ws, name, user string) string {
	t.Helper()
	id := uuid.NewString()
	execOK(t, pool, `INSERT INTO agents(id,workspace_id,name,created_by) VALUES($1,$2,$3,$4)`, id, ws, name, user)
	return id
}

func insertBinding(t *testing.T, pool *sql.DB, agentID, skillID string, enabled bool) {
	t.Helper()
	execOK(t, pool, `INSERT INTO agent_skill_bindings(agent_id,skill_id,enabled) VALUES($1,$2,$3)`, agentID, skillID, enabled)
}

// TestAgentMigrationFresh verifies a clean database through 0017: both tables exist, Agent is
// structurally mutable while AgentSkillBinding is membership-style (no deleted_at), and
// agents.workspace_id references collab_workspaces — the same ownership boundary as Skills.
func TestAgentMigrationFresh(t *testing.T) {
	pool, _ := testSchema(t, "agent_fresh_")
	store := newStoreOnSchema(t, pool)
	must(t, store.Migrate(context.Background()))
	must(t, store.Migrate(context.Background()))
	must(t, store.CheckSchema(context.Background()))

	for _, table := range []string{"agents", "agent_skill_bindings"} {
		if !tableExists(t, pool, table) {
			t.Fatalf("missing agent table %s", table)
		}
	}

	// Agent is a mutable business resource: version + updated_at + deleted_at.
	for _, col := range []string{"version", "updated_at", "deleted_at"} {
		if !columnExists(t, pool, "agents", col) {
			t.Fatalf("agents must be mutable: missing column %s", col)
		}
	}
	// AgentSkillBinding is a membership-style association: no soft delete, remove = DELETE.
	if columnExists(t, pool, "agent_skill_bindings", "deleted_at") {
		t.Fatal("agent_skill_bindings must not have deleted_at: remove is a hard DELETE")
	}

	// agents.workspace_id must reference collab_workspaces, never the runtime workspaces table.
	var refTable string
	must(t, pool.QueryRow(`SELECT ccu.table_name
		FROM information_schema.referential_constraints rc
		JOIN information_schema.key_column_usage kcu
			ON rc.constraint_name=kcu.constraint_name AND rc.constraint_schema=kcu.constraint_schema
		JOIN information_schema.constraint_column_usage ccu
			ON rc.unique_constraint_name=ccu.constraint_name AND rc.unique_constraint_schema=ccu.constraint_schema
		WHERE rc.unique_constraint_schema=current_schema() AND kcu.table_schema=current_schema()
			AND kcu.table_name='agents' AND kcu.column_name='workspace_id'`).Scan(&refTable))
	if refTable != "collab_workspaces" {
		t.Fatalf("agents.workspace_id must reference collab_workspaces, got %s", refTable)
	}

	for _, idx := range []string{"agent_active_name_uniq", "agent_workspace_list", "agent_skill_binding_list"} {
		var n int
		must(t, pool.QueryRow(`SELECT count(*) FROM pg_indexes WHERE schemaname=current_schema() AND indexname=$1`, idx).Scan(&n))
		if n != 1 {
			t.Fatalf("index %s missing", idx)
		}
	}
}

// TestAgentMigrationUpgradePath reproduces a deployment migrated through 0016, then lets the current
// binary apply 0017 on top: no duplicate DDL, no checksum mismatch, both tables present.
func TestAgentMigrationUpgradePath(t *testing.T) {
	pool, _ := testSchema(t, "agent_upg_")
	applyMigrationsUpTo(t, pool, []string{
		"0001_core.sql", "0002_aggregate_guards.sql", "0003_resource_versions.sql",
		"0004_effect_intent_and_ticket_scope.sql", "0005_gateway_auth.sql",
		"0006_collab_spaces.sql", "0007_project_space_scope.sql",
		"0008_issues.sql", "0009_issue_extensions.sql", "0010_issue_collaboration.sql",
		"0011_issue_interactions.sql", "0012_issue_interaction_input.sql",
		"0013_project_space_optional.sql", "0014_clone_coordination.sql",
		"0015_skills.sql", "0016_skill_ingestion_idempotency.sql",
	})
	store := newStoreOnSchema(t, pool)
	must(t, store.Migrate(context.Background()))
	must(t, store.Migrate(context.Background()))
	must(t, store.CheckSchema(context.Background()))
	for _, table := range []string{"agents", "agent_skill_bindings"} {
		if !tableExists(t, pool, table) {
			t.Fatalf("missing agent table %s after upgrade", table)
		}
	}
}

// TestAgentOwnershipAndNameUniqueness pins the Agent identity invariants: a real Collaboration
// Workspace owner, active-name uniqueness per workspace, soft delete releasing the name, and
// immutable workspace ownership across ordinary updates.
func TestAgentOwnershipAndNameUniqueness(t *testing.T) {
	pool, _ := testSchema(t, "agent_own_")
	store := newStoreOnSchema(t, pool)
	must(t, store.Migrate(context.Background()))
	user, _, ws1, ws2 := seedSkillBase(t, pool)

	// An Agent must belong to a real Collaboration Workspace.
	execErr(t, pool, `INSERT INTO agents(id,workspace_id,name,created_by) VALUES($1,$2,'x',$3)`, uuid.NewString(), uuid.NewString(), user)
	execOK(t, pool, `INSERT INTO agents(id,workspace_id,name,created_by) VALUES($1,$2,'alpha',$3)`, uuid.NewString(), ws1, user)

	// Same name in the same Workspace is rejected...
	execErr(t, pool, `INSERT INTO agents(id,workspace_id,name,created_by) VALUES($1,$2,'alpha',$3)`, uuid.NewString(), ws1, user)
	// ...but the same name in a different Workspace is an independent, valid Agent.
	execOK(t, pool, `INSERT INTO agents(id,workspace_id,name,created_by) VALUES($1,$2,'alpha',$3)`, uuid.NewString(), ws2, user)

	// Soft delete releases the name for a replacement in the same Workspace.
	deleted := insertAgent(t, pool, ws1, "beta", user)
	execOK(t, pool, `UPDATE agents SET deleted_at=now(),version=version+1,updated_at=now() WHERE id=$1`, deleted)
	execOK(t, pool, `INSERT INTO agents(id,workspace_id,name,created_by) VALUES($1,$2,'beta',$3)`, uuid.NewString(), ws1, user)

	// The owning Workspace is immutable: an ordinary update must not move an Agent between Workspaces.
	agent := insertAgent(t, pool, ws1, "gamma", user)
	execErr(t, pool, `UPDATE agents SET workspace_id=$2 WHERE id=$1`, agent, ws2)
	execOK(t, pool, `UPDATE agents SET status='disabled',version=version+1,updated_at=now() WHERE id=$1`, agent)
}

// TestAgentSkillBindingInvariants pins the association: binding must reference a real Agent and a real
// Skill, one binding per (agent, skill), and a Skill soft-delete keeps the configuration row (D6): the
// binding survives so a stale entry stays observable/detachable, while only live Skills are effective.
func TestAgentSkillBindingInvariants(t *testing.T) {
	pool, _ := testSchema(t, "agent_bind_")
	store := newStoreOnSchema(t, pool)
	must(t, store.Migrate(context.Background()))
	user, _, ws1, _ := seedSkillBase(t, pool)

	agent := insertAgent(t, pool, ws1, "a", user)
	skill := insertSkill(t, pool, ws1, "s", user)

	// A binding must reference a real Agent and a real Skill.
	execErr(t, pool, `INSERT INTO agent_skill_bindings(agent_id,skill_id) VALUES($1,$2)`, uuid.NewString(), skill)
	execErr(t, pool, `INSERT INTO agent_skill_bindings(agent_id,skill_id) VALUES($1,$2)`, agent, uuid.NewString())
	insertBinding(t, pool, agent, skill, true)

	// One binding per (agent, skill), enforced by the composite PK.
	execErr(t, pool, `INSERT INTO agent_skill_bindings(agent_id,skill_id) VALUES($1,$2)`, agent, skill)

	// Soft-deleting the Skill keeps the configuration row (D6); the binding survives.
	execOK(t, pool, `UPDATE skills SET deleted_at=now(),version=version+1,updated_at=now() WHERE id=$1`, skill)
	var n int
	must(t, pool.QueryRow(`SELECT count(*) FROM agent_skill_bindings WHERE agent_id=$1 AND skill_id=$2`, agent, skill).Scan(&n))
	if n != 1 {
		t.Fatalf("soft-deleting a Skill must keep its binding row, got %d", n)
	}
}

// TestAgentSkillBindingAuthorityRead is the §17 acceptance proof: given an Agent with enabled and
// disabled bindings and a soft-deleted Skill, the read seam "enabled durable AgentSkillBindings"
// resolves exactly the enabled bindings to live Skills, each reachable to a non-null
// current_revision_id, deterministically ordered by canonical_name then skill_id (§18).
func TestAgentSkillBindingAuthorityRead(t *testing.T) {
	pool, _ := testSchema(t, "agent_auth_")
	store := newStoreOnSchema(t, pool)
	must(t, store.Migrate(context.Background()))
	user, _, ws1, _ := seedSkillBase(t, pool)

	agent := insertAgent(t, pool, ws1, "agent", user)

	setRevision := func(name string) (skillID string) {
		skillID = insertSkill(t, pool, ws1, name, user)
		rev := insertRevision(t, pool, skillID, strings.Repeat(strings.ToUpper(name[:1]), 64), user)
		execOK(t, pool, `UPDATE skills SET current_revision_id=$2,version=version+1,updated_at=now() WHERE id=$1`, skillID, rev)
		return skillID
	}

	zebra := setRevision("zebra")
	alpha := setRevision("alpha")
	beta := setRevision("beta")
	gamma := setRevision("gamma")

	insertBinding(t, pool, agent, zebra, true) // enabled, live
	insertBinding(t, pool, agent, alpha, true) // enabled, live
	insertBinding(t, pool, agent, beta, false) // disabled, live — excluded from selection
	insertBinding(t, pool, agent, gamma, true) // enabled, but Skill soft-deleted — excluded
	execOK(t, pool, `UPDATE skills SET deleted_at=now(),version=version+1,updated_at=now() WHERE id=$1`, gamma)

	// The seam query (mirrors enabledAgentSkillBindings): enabled + live Skill, ordered by
	// canonical_name then id.
	rows, e := pool.Query(`SELECT ab.skill_id, s.canonical_name, s.current_revision_id
		FROM agent_skill_bindings ab
		JOIN skills s ON s.id=ab.skill_id
		WHERE ab.agent_id=$1 AND ab.enabled=true AND s.deleted_at IS NULL
		ORDER BY s.canonical_name, s.id`, agent)
	must(t, e)
	defer rows.Close()

	type seamRow struct{ skillID, name, current string }
	var got []seamRow
	for rows.Next() {
		var r seamRow
		must(t, rows.Scan(&r.skillID, &r.name, &r.current))
		got = append(got, r)
	}
	must(t, rows.Err())

	// Exactly the two enabled, live bindings; disabled (beta) and soft-deleted (gamma) are excluded.
	if len(got) != 2 {
		t.Fatalf("want 2 effective bindings, got %d (%v)", len(got), got)
	}
	// Deterministic ordering: canonical_name ascending.
	if got[0].name != "alpha" || got[1].name != "zebra" {
		t.Fatalf("binding order must be canonical_name ascending, got %s, %s", got[0].name, got[1].name)
	}
	// §17 chain: each effective binding resolves to a Skill with a real current revision.
	for _, r := range got {
		if r.current == "" {
			t.Fatalf("effective binding %s must resolve to a current_revision_id", r.name)
		}
	}
}

// agentFixture provisions one tenant with owner (alice), ordinary member (bob) and non-member
// (carol), plus a second workspace (ws2) for cross-workspace scoping, and a live HTTP server.
type agentFixture struct {
	t        *testing.T
	store    *core.Store
	creds    *simulator.Credentials
	server   *httptest.Server
	tid      string
	ws1, ws2 string
	ownerUID string
}

func newAgentFixture(t *testing.T) *agentFixture {
	t.Helper()
	pool, _ := testSchema(t, "agent_http_")
	store := newStoreOnSchema(t, pool)
	must(t, store.Migrate(context.Background()))
	owner, tenant, ws1, ws2 := seedSkillBase(t, pool)

	// Owner of ws1 resolves from identity (corp, alice).
	execOK(t, pool, `INSERT INTO user_identities(user_id,source,subject) VALUES($1,'corp','alice')`, owner)
	execOK(t, pool, `INSERT INTO collab_workspace_members(workspace_id,user_id,role,status,created_by) VALUES($1,$2,'owner','active',$2)`, ws1, owner)

	// Ordinary member of ws1 (bob).
	member := uuid.NewString()
	execOK(t, pool, `INSERT INTO users(id,display_name,status) VALUES($1,'Bob','active')`, member)
	execOK(t, pool, `INSERT INTO user_identities(user_id,source,subject) VALUES($1,'corp','bob')`, member)
	execOK(t, pool, `INSERT INTO tenant_memberships(tenant_id,user_id,role,status) VALUES($1,$2,'member','active')`, tenant, member)
	execOK(t, pool, `INSERT INTO collab_workspace_members(workspace_id,user_id,role,status,created_by) VALUES($1,$2,'member','active',$3)`, ws1, member, owner)

	// Non-member of ws1 (carol): active tenant member, no workspace membership.
	outsider := uuid.NewString()
	execOK(t, pool, `INSERT INTO users(id,display_name,status) VALUES($1,'Carol','active')`, outsider)
	execOK(t, pool, `INSERT INTO user_identities(user_id,source,subject) VALUES($1,'corp','carol')`, outsider)
	execOK(t, pool, `INSERT INTO tenant_memberships(tenant_id,user_id,role,status) VALUES($1,$2,'member','active')`, tenant, outsider)

	creds, err := simulator.NewCredentials()
	must(t, err)
	auth, err := core.NewAuthenticator("ora-cloud", creds.Trust)
	must(t, err)
	gin.SetMode(gin.TestMode)
	server := httptest.NewServer(router.New(store, auth, zap.NewNop()))
	t.Cleanup(server.Close)

	return &agentFixture{t: t, store: store, creds: creds, server: server, tid: tenant, ws1: ws1, ws2: ws2, ownerUID: owner}
}

// do issues one authorized request as `subject` (an identity subject) with an optional JSON body and
// optional Idempotency-Key, returning the HTTP status and decoded body.
func (f *agentFixture) do(subject, method, space, suffix string, body map[string]any, key string) (int, map[string]any) {
	f.t.Helper()
	if space == "" {
		space = f.ws1
	}
	var rd io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		must(f.t, err)
		rd = bytes.NewReader(b)
	}
	req, err := http.NewRequest(method, f.server.URL+"/api/v1/tenants/"+f.tid+"/spaces/"+space+suffix, rd)
	must(f.t, err)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	svc, err := f.creds.Token("gateway", core.Claims{RegisteredClaims: jwt.RegisteredClaims{Subject: "gateway-a"}})
	must(f.t, err)
	req.Header.Set("Authorization", "Bearer "+svc)
	usr, err := f.creds.Token("user", core.Claims{RegisteredClaims: jwt.RegisteredClaims{Subject: subject}, Source: "corp", DisplayName: "Caller", Caller: "gateway-a"})
	must(f.t, err)
	req.Header.Set("X-Ora-User-Token", usr)
	if key != "" {
		req.Header.Set("Idempotency-Key", key)
	}
	resp, err := http.DefaultClient.Do(req)
	must(f.t, err)
	defer resp.Body.Close()
	b, err := io.ReadAll(resp.Body)
	must(f.t, err)
	var out map[string]any
	if len(b) > 0 {
		if err := json.Unmarshal(b, &out); err != nil {
			f.t.Fatalf("decode %q: %v", b, err)
		}
	}
	return resp.StatusCode, out
}

// createAgent issues the owner POST and returns the created Agent id.
func (f *agentFixture) createAgent(name, key string) string {
	f.t.Helper()
	status, body := f.do("alice", http.MethodPost, f.ws1, "/agents", map[string]any{"name": name}, key)
	if status != http.StatusOK || body["id"] == nil {
		f.t.Fatalf("create agent: want 200 with id, got %d (%v)", status, body)
	}
	id, _ := body["id"].(string)
	return id
}

func TestAgentCRUDAndAuth(t *testing.T) {
	f := newAgentFixture(t)

	// Owner creates, then reads back the Agent.
	id := f.createAgent("runner", "cr-a")
	if status, body := f.do("alice", http.MethodGet, f.ws1, "/agents/"+id, nil, ""); status != http.StatusOK || body["name"] != "runner" || body["status"] != "active" {
		t.Fatalf("get agent: got %d (%v)", status, body)
	}
	// The list includes it.
	if status, body := f.do("alice", http.MethodGet, f.ws1, "/agents", nil, ""); status != http.StatusOK {
		t.Fatalf("list agents: %d", status)
	} else if items, _ := body["items"].([]any); len(items) != 1 {
		t.Fatalf("list agents: want 1 item, got %v", body["items"])
	}

	// Owner patches name + status with the matching version; a stale version is 409.
	if status, body := f.do("alice", http.MethodPatch, f.ws1, "/agents/"+id, map[string]any{"name": "runner-2", "status": "disabled", "version": 1}, ""); status != http.StatusOK || body["name"] != "runner-2" || body["status"] != "disabled" {
		t.Fatalf("patch agent: got %d (%v)", status, body)
	}
	if status, body := f.do("alice", http.MethodPatch, f.ws1, "/agents/"+id, map[string]any{"status": "active", "version": 1}, ""); status != http.StatusConflict || body["code"] != "version_conflict" {
		t.Fatalf("stale patch must 409 version_conflict, got %d (%v)", status, body)
	}

	// A member can read but cannot write (D11).
	if status, body := f.do("bob", http.MethodGet, f.ws1, "/agents/"+id, nil, ""); status != http.StatusOK {
		t.Fatalf("member read agent: %d (%v)", status, body)
	}
	if status, body := f.do("bob", http.MethodPost, f.ws1, "/agents", map[string]any{"name": "bob-agent"}, "cr-bob"); status != http.StatusForbidden || body["code"] != "workspace_admin_required" {
		t.Fatalf("member create must 403 workspace_admin_required, got %d (%v)", status, body)
	}

	// A non-member of ws1 is 404 (no existence leak), even for a read.
	if status, body := f.do("carol", http.MethodGet, f.ws1, "/agents/"+id, nil, ""); status != http.StatusNotFound || body["code"] != "not_found" {
		t.Fatalf("non-member get must 404 not_found, got %d (%v)", status, body)
	}
	// A foreign workspace cannot see the agent (owner of ws1, addressing it under ws2).
	if status, body := f.do("alice", http.MethodGet, f.ws2, "/agents/"+id, nil, ""); status != http.StatusNotFound || body["code"] != "not_found" {
		t.Fatalf("cross-workspace get must 404 not_found, got %d (%v)", status, body)
	}

	// Owner archives (DELETE with version + idempotency key) and a later read is 404.
	if status, body := f.do("alice", http.MethodDelete, f.ws1, "/agents/"+id, map[string]any{"version": 2}, "cr-del"); status != http.StatusOK {
		t.Fatalf("archive agent: got %d (%v)", status, body)
	}
	if status, body := f.do("alice", http.MethodGet, f.ws1, "/agents/"+id, nil, ""); status != http.StatusNotFound {
		t.Fatalf("archived agent must 404, got %d (%v)", status, body)
	}
}

func TestAgentSkillBindingLifecycle(t *testing.T) {
	f := newAgentFixture(t)
	agent := f.createAgent("runner", "bind-a")
	skill := insertSkill(t, f.store.Pool, f.ws1, "skills", f.ownerUID)
	if status, body := f.do("alice", http.MethodPost, f.ws1, "/agents/"+agent+"/skills", map[string]any{"skillId": skill}, "bind-1"); status != http.StatusOK || body["skillId"] != skill || body["enabled"] != true {
		t.Fatalf("attach skill: got %d (%v)", status, body)
	}
	// Duplicate attach is 409 binding_exists.
	if status, body := f.do("alice", http.MethodPost, f.ws1, "/agents/"+agent+"/skills", map[string]any{"skillId": skill}, "bind-2"); status != http.StatusConflict || body["code"] != "binding_exists" {
		t.Fatalf("duplicate attach must 409 binding_exists, got %d (%v)", status, body)
	}
	// The list shows the binding (with canonical_name).
	if status, body := f.do("alice", http.MethodGet, f.ws1, "/agents/"+agent+"/skills", nil, ""); status != http.StatusOK {
		t.Fatalf("list agent skills: %d", status)
	} else if items, _ := body["items"].([]any); len(items) != 1 || items[0].(map[string]any)["canonicalName"] != "skills" || items[0].(map[string]any)["enabled"] != true {
		t.Fatalf("list agent skills unexpected: %v", body["items"])
	}

	// Disable requires the current version; missing version is 428, stale is 409.
	if status, body := f.do("alice", http.MethodPut, f.ws1, "/agents/"+agent+"/skills/"+skill, map[string]any{"enabled": false}, ""); status == http.StatusOK || body["code"] != "version_required" {
		t.Fatalf("enable/disable without version must 428 version_required, got %d (%v)", status, body)
	}
	if status, body := f.do("alice", http.MethodPut, f.ws1, "/agents/"+agent+"/skills/"+skill, map[string]any{"enabled": false, "version": 99}, ""); status != http.StatusConflict || body["code"] != "version_conflict" {
		t.Fatalf("stale enable/disable must 409 version_conflict, got %d (%v)", status, body)
	}
	if status, body := f.do("alice", http.MethodPut, f.ws1, "/agents/"+agent+"/skills/"+skill, map[string]any{"enabled": false, "version": 1}, ""); status != http.StatusOK || body["enabled"] != false {
		t.Fatalf("disable: got %d (%v)", status, body)
	}

	// Detach is idempotent (DELETE + key); a second detach is 404 binding_not_found.
	if status, body := f.do("alice", http.MethodDelete, f.ws1, "/agents/"+agent+"/skills/"+skill, map[string]any{}, "det-1"); status != http.StatusOK {
		t.Fatalf("detach: got %d (%v)", status, body)
	}
	if status, body := f.do("alice", http.MethodDelete, f.ws1, "/agents/"+agent+"/skills/"+skill, map[string]any{}, "det-2"); status != http.StatusNotFound || body["code"] != "binding_not_found" {
		t.Fatalf("detach absent must 404 binding_not_found, got %d (%v)", status, body)
	}

	// A member cannot attach or detach (write is owner/admin only).
	if status, body := f.do("bob", http.MethodPost, f.ws1, "/agents/"+agent+"/skills", map[string]any{"skillId": skill}, "bind-bob"); status != http.StatusForbidden {
		t.Fatalf("member attach must 403, got %d (%v)", status, body)
	}
}

// TestAgentSkillCrossWorkspaceAndSoftDeletedReject pins the admission boundaries of attach: a Skill
// from another workspace and a soft-deleted Skill are both 404 (fail closed, no cross-workspace leak).
func TestAgentSkillCrossWorkspaceAndSoftDeletedReject(t *testing.T) {
	f := newAgentFixture(t)
	agent := f.createAgent("runner", "xws-a")

	// A Skill in ws2 is not attachable from an agent in ws1.
	foreign := insertSkill(t, f.store.Pool, f.ws2, "foreign", f.ownerUID)
	if status, body := f.do("alice", http.MethodPost, f.ws1, "/agents/"+agent+"/skills", map[string]any{"skillId": foreign}, "xws-1"); status != http.StatusNotFound || body["code"] != "not_found" {
		t.Fatalf("cross-workspace skill must 404 not_found, got %d (%v)", status, body)
	}

	// A soft-deleted Skill in ws1 is not attachable (D4).
	gone := insertSkill(t, f.store.Pool, f.ws1, "gone", f.ownerUID)
	execOK(t, f.store.Pool, `UPDATE skills SET deleted_at=now(),version=version+1,updated_at=now() WHERE id=$1`, gone)
	if status, body := f.do("alice", http.MethodPost, f.ws1, "/agents/"+agent+"/skills", map[string]any{"skillId": gone}, "xws-2"); status != http.StatusNotFound || body["code"] != "not_found" {
		t.Fatalf("soft-deleted skill must 404 not_found, got %d (%v)", status, body)
	}

	// An absent skill id is 404, never a 200.
	if status, body := f.do("alice", http.MethodPost, f.ws1, "/agents/"+agent+"/skills", map[string]any{"skillId": uuid.NewString()}, "xws-3"); status != http.StatusNotFound || body["code"] != "not_found" {
		t.Fatalf("absent skill must 404 not_found, got %d (%v)", status, body)
	}
}
