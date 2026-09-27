package integration

// Execution / Attempt / ExecutionSkillBinding snapshot (Phase 5 of
// specs/decisions/cloud/skills/20260927-execution-snapshot.md), pinned end-to-end:
//
//   - migration 0018 adds executions + attempts + execution_skill_bindings, an immutable fact
//     (trigger-blocked UPDATE, no version/updated_at mutation columns) with a composite FK pinning
//     each frozen revision to its Skill, and no bearer-credential field;
//   - admission freezes the exact current revision of each enabled+live durable AgentSkillBinding in
//     one atomic transaction with the first (eligible) Attempt, and later current-pointer drift or a
//     Skill soft-delete never moves the frozen binding;
//   - retry adds a new Attempt over the same frozen bindings without re-resolving; Run Again creates
//     a new Execution that re-resolves current configuration;
//   - admission reads only durable bindings (disabled / soft-deleted excluded), fails closed on a
//     bound Skill with no current revision, orders bindings by canonical_name then skill_id, and
//     follows workspace membership (non-member / cross-workspace / disabled Agent → 404).

import (
	"context"
	"database/sql"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/wanglongan587/cloud/internal/core"
)

// execFixture provisions one tenant with an owner (alice), an ordinary member (bob) and a non-member
// (carol), plus a second workspace (ws2) for cross-workspace scoping. Unlike the HTTP agent fixture
// it exercises the Store methods directly, which is the test seam the Execution snapshot exposes.
type execFixture struct {
	t     *testing.T
	store *core.Store
	ws1   string
	ws2   string
	owner string // alice, owner of ws1 (also used as created_by for skills/agents)
}

func newExecFixture(t *testing.T) *execFixture {
	t.Helper()
	pool, _ := testSchema(t, "exec_")
	store := newStoreOnSchema(t, pool)
	must(t, store.Migrate(context.Background()))
	owner, tenant, ws1, ws2 := seedSkillBase(t, pool)

	execOK(t, pool, `INSERT INTO user_identities(user_id,source,subject) VALUES($1,'corp','alice')`, owner)
	execOK(t, pool, `INSERT INTO collab_workspace_members(workspace_id,user_id,role,status,created_by) VALUES($1,$2,'owner','active',$2)`, ws1, owner)

	member := uuid.NewString()
	execOK(t, pool, `INSERT INTO users(id,display_name,status) VALUES($1,'Bob','active')`, member)
	execOK(t, pool, `INSERT INTO user_identities(user_id,source,subject) VALUES($1,'corp','bob')`, member)
	execOK(t, pool, `INSERT INTO tenant_memberships(tenant_id,user_id,role,status) VALUES($1,$2,'member','active')`, tenant, member)
	execOK(t, pool, `INSERT INTO collab_workspace_members(workspace_id,user_id,role,status,created_by) VALUES($1,$2,'member','active',$3)`, ws1, member, owner)

	outsider := uuid.NewString()
	execOK(t, pool, `INSERT INTO users(id,display_name,status) VALUES($1,'Carol','active')`, outsider)
	execOK(t, pool, `INSERT INTO user_identities(user_id,source,subject) VALUES($1,'corp','carol')`, outsider)
	execOK(t, pool, `INSERT INTO tenant_memberships(tenant_id,user_id,role,status) VALUES($1,$2,'member','active')`, tenant, outsider)

	return &execFixture{t: t, store: store, ws1: ws1, ws2: ws2, owner: owner}
}

// admit runs an admission as subject into space for the given agent with a fixed input.
func (f *execFixture) admit(subject, space, agent string) (core.Object, error) {
	f.t.Helper()
	return f.store.AdmitExecution(context.Background(), "corp", subject, "Caller", space, agent, core.Object{"prompt": "x"})
}

// seedActiveSkill inserts a live Skill and activates a revision R1 (current_revision_id), returning
// (skillID, revisionID).
func seedActiveSkill(t *testing.T, pool *sql.DB, ws, name, user string) (skillID, revisionID string) {
	t.Helper()
	skillID = insertSkill(t, pool, ws, name, user)
	revisionID = insertRevision(t, pool, skillID, strings.Repeat(strings.ToUpper(name[:1]), 64), user)
	execOK(t, pool, `UPDATE skills SET current_revision_id=$2,version=version+1,updated_at=now() WHERE id=$1`, skillID, revisionID)
	return skillID, revisionID
}

// TestExecutionMigrationFresh verifies a clean database through 0018: the three tables exist,
// Execution and ExecutionSkillBinding are structurally immutable while Attempt is a mutable
// lifecycle row, and no bearer-credential field exists anywhere.
func TestExecutionMigrationFresh(t *testing.T) {
	pool, _ := testSchema(t, "exec_fresh_")
	store := newStoreOnSchema(t, pool)
	must(t, store.Migrate(context.Background()))
	must(t, store.Migrate(context.Background()))
	must(t, store.CheckSchema(context.Background()))

	for _, table := range []string{"executions", "attempts", "execution_skill_bindings"} {
		if !tableExists(t, pool, table) {
			t.Fatalf("missing execution table %s", table)
		}
	}

	// Execution is a logical identity: no in-place mutation columns.
	for _, col := range []string{"version", "updated_at", "deleted_at"} {
		if columnExists(t, pool, "executions", col) {
			t.Fatalf("executions must be immutable: column %s must not exist", col)
		}
	}
	// Attempt is a physical lifecycle row: updated_at exists, no soft-delete column.
	if !columnExists(t, pool, "attempts", "updated_at") {
		t.Fatal("attempts must have updated_at (lifecycle)")
	}
	if columnExists(t, pool, "attempts", "deleted_at") {
		t.Fatal("attempts must not have deleted_at")
	}
	// ExecutionSkillBinding is an immutable fact: no mutation columns.
	for _, col := range []string{"version", "updated_at", "deleted_at"} {
		if columnExists(t, pool, "execution_skill_bindings", col) {
			t.Fatalf("execution_skill_bindings must be immutable: column %s must not exist", col)
		}
	}

	// No bearer-credential / object-locator field is created (ADR D7/D8, plan §22).
	for _, table := range []string{"executions", "attempts", "execution_skill_bindings"} {
		for _, col := range []string{"url", "signed_url", "token", "credential", "authorization_header", "presign"} {
			if columnExists(t, pool, table, col) {
				t.Fatalf("%s must not carry a credential field %s", table, col)
			}
		}
	}

	// executions.workspace_id must reference collab_workspaces, never the runtime workspaces table.
	var refTable string
	must(t, pool.QueryRow(`SELECT ccu.table_name
		FROM information_schema.referential_constraints rc
		JOIN information_schema.key_column_usage kcu
			ON rc.constraint_name=kcu.constraint_name AND rc.constraint_schema=kcu.constraint_schema
		JOIN information_schema.constraint_column_usage ccu
			ON rc.unique_constraint_name=ccu.constraint_name AND rc.unique_constraint_schema=ccu.constraint_schema
		WHERE rc.unique_constraint_schema=current_schema() AND kcu.table_schema=current_schema()
			AND kcu.table_name='executions' AND kcu.column_name='workspace_id'`).Scan(&refTable))
	if refTable != "collab_workspaces" {
		t.Fatalf("executions.workspace_id must reference collab_workspaces, got %s", refTable)
	}

	// The immutability trigger guards in-place UPDATE of a binding.
	var trig int
	must(t, pool.QueryRow(`SELECT count(*) FROM information_schema.triggers WHERE trigger_schema=current_schema() AND trigger_name='execution_skill_binding_immutable'`).Scan(&trig))
	if trig != 1 {
		t.Fatal("execution_skill_binding_immutable trigger missing")
	}
}

// TestExecutionMigrationUpgradePath reproduces a deployment migrated through 0017, then lets the
// current binary apply 0018 on top: no duplicate DDL, no checksum mismatch, tables present.
func TestExecutionMigrationUpgradePath(t *testing.T) {
	pool, _ := testSchema(t, "exec_upg_")
	applyMigrationsUpTo(t, pool, []string{
		"0001_core.sql", "0002_aggregate_guards.sql", "0003_resource_versions.sql",
		"0004_effect_intent_and_ticket_scope.sql", "0005_gateway_auth.sql",
		"0006_collab_spaces.sql", "0007_project_space_scope.sql",
		"0008_issues.sql", "0009_issue_extensions.sql", "0010_issue_collaboration.sql",
		"0011_issue_interactions.sql", "0012_issue_interaction_input.sql",
		"0013_project_space_optional.sql", "0014_clone_coordination.sql",
		"0015_skills.sql", "0016_skill_ingestion_idempotency.sql", "0017_agents_and_skill_bindings.sql",
	})
	store := newStoreOnSchema(t, pool)
	must(t, store.Migrate(context.Background()))
	must(t, store.Migrate(context.Background()))
	must(t, store.CheckSchema(context.Background()))
	for _, table := range []string{"executions", "attempts", "execution_skill_bindings"} {
		if !tableExists(t, pool, table) {
			t.Fatalf("missing execution table %s after upgrade", table)
		}
	}
}

// TestExecutionSnapshotFreezesExactRevision pins the core invariant: after admission, advancing
// current_revision_id and soft-deleting the Skill leave the frozen binding pointing at the original
// revision and digest.
func TestExecutionSnapshotFreezesExactRevision(t *testing.T) {
	f := newExecFixture(t)
	agent := insertAgent(t, f.store.Pool, f.ws1, "runner", f.owner)
	skillID, rev1 := seedActiveSkill(t, f.store.Pool, f.ws1, "alpha", f.owner)
	insertBinding(t, f.store.Pool, agent, skillID, true)

	out, err := f.admit("alice", f.ws1, agent)
	must(t, err)
	executionID := out.O("execution").S("executionId")
	if executionID == "" {
		t.Fatal("admission returned no execution id")
	}

	// Post-admission drift: a new revision becomes current, then the Skill is soft-deleted.
	rev2 := insertRevision(t, f.store.Pool, skillID, strings.Repeat("b", 64), f.owner)
	execOK(t, f.store.Pool, `UPDATE skills SET current_revision_id=$2,version=version+1,updated_at=now() WHERE id=$1`, skillID, rev2)
	execOK(t, f.store.Pool, `UPDATE skills SET deleted_at=now(),version=version+1,updated_at=now() WHERE id=$1`, skillID)

	var gotRevision, gotDigest string
	must(t, f.store.Pool.QueryRow(`SELECT skill_revision_id, content_digest FROM execution_skill_bindings WHERE execution_id=$1 AND skill_id=$2`, executionID, skillID).Scan(&gotRevision, &gotDigest))
	if gotRevision != rev1 {
		t.Fatalf("frozen revision drifted: want %s got %s", rev1, gotRevision)
	}
	if gotDigest != strings.Repeat("A", 64) {
		t.Fatalf("frozen digest drifted: got %q", gotDigest)
	}
}

// TestExecutionSkillBindingImmutable pins the immutable-fact constraint: an in-place UPDATE of a
// binding is rejected by the trigger, and the table carries no mutation columns.
func TestExecutionSkillBindingImmutable(t *testing.T) {
	f := newExecFixture(t)
	agent := insertAgent(t, f.store.Pool, f.ws1, "runner", f.owner)
	skillID, _ := seedActiveSkill(t, f.store.Pool, f.ws1, "alpha", f.owner)
	insertBinding(t, f.store.Pool, agent, skillID, true)
	out, err := f.admit("alice", f.ws1, agent)
	must(t, err)
	executionID := out.O("execution").S("executionId")

	execErr(t, f.store.Pool, `UPDATE execution_skill_bindings SET content_digest='x' WHERE execution_id=$1 AND skill_id=$2`, executionID, skillID)
	execErr(t, f.store.Pool, `UPDATE execution_skill_bindings SET skill_revision_id=skill_revision_id WHERE execution_id=$1 AND skill_id=$2`, executionID, skillID)

	for _, col := range []string{"version", "updated_at"} {
		if columnExists(t, f.store.Pool, "execution_skill_bindings", col) {
			t.Fatalf("execution_skill_bindings must be immutable: column %s must not exist", col)
		}
	}
}

// TestRetryCreatesNewAttemptOverSameBindings pins the retry semantics: a new Attempt (ordinal 2)
// reuses the same frozen bindings even after mutable state changes, without re-resolving.
func TestRetryCreatesNewAttemptOverSameBindings(t *testing.T) {
	f := newExecFixture(t)
	agent := insertAgent(t, f.store.Pool, f.ws1, "runner", f.owner)
	skillID, rev1 := seedActiveSkill(t, f.store.Pool, f.ws1, "alpha", f.owner)
	insertBinding(t, f.store.Pool, agent, skillID, true)
	out, err := f.admit("alice", f.ws1, agent)
	must(t, err)
	executionID := out.O("execution").S("executionId")

	// Mutate mutable state before retry.
	rev2 := insertRevision(t, f.store.Pool, skillID, strings.Repeat("b", 64), f.owner)
	execOK(t, f.store.Pool, `UPDATE skills SET current_revision_id=$2,version=version+1,updated_at=now() WHERE id=$1`, skillID, rev2)

	retried, err := f.store.CreateRetryAttempt(context.Background(), executionID)
	must(t, err)

	var rev string
	must(t, f.store.Pool.QueryRow(`SELECT skill_revision_id FROM execution_skill_bindings WHERE execution_id=$1 AND skill_id=$2`, executionID, skillID).Scan(&rev))
	if rev != rev1 {
		t.Fatalf("retry re-resolved revision: want %s got %s", rev1, rev)
	}

	attempts := retried["attempts"].([]core.Object)
	if len(attempts) != 2 {
		t.Fatalf("retry must add one attempt (total 2), got %d", len(attempts))
	}
	if attempts[0].N("ordinal") != 1 || attempts[1].N("ordinal") != 2 {
		t.Fatalf("attempt ordinals must be 1 then 2, got %d then %d", attempts[0].N("ordinal"), attempts[1].N("ordinal"))
	}
	if attempts[1].S("state") != "eligible" || attempts[1].S("nodeId") != "" {
		t.Fatalf("retry attempt must be eligible with empty node_id, got state=%s nodeId=%q", attempts[1].S("state"), attempts[1].S("nodeId"))
	}
}

// TestRunAgainResolvesCurrentConfiguration pins Run Again: a new Execution re-resolves the current
// enabled bindings and current revisions, while the prior snapshot is left untouched.
func TestRunAgainResolvesCurrentConfiguration(t *testing.T) {
	f := newExecFixture(t)
	agent := insertAgent(t, f.store.Pool, f.ws1, "runner", f.owner)
	skillA, rev1 := seedActiveSkill(t, f.store.Pool, f.ws1, "alpha", f.owner)
	insertBinding(t, f.store.Pool, agent, skillA, true)
	out, err := f.admit("alice", f.ws1, agent)
	must(t, err)
	executionID := out.O("execution").S("executionId")

	// Change current configuration: A advances to R2, and a new Skill B is enabled.
	rev2 := insertRevision(t, f.store.Pool, skillA, strings.Repeat("b", 64), f.owner)
	execOK(t, f.store.Pool, `UPDATE skills SET current_revision_id=$2,version=version+1,updated_at=now() WHERE id=$1`, skillA, rev2)
	skillB, revB := seedActiveSkill(t, f.store.Pool, f.ws1, "beta", f.owner)
	insertBinding(t, f.store.Pool, agent, skillB, true)

	again, err := f.store.RunAgainExecution(context.Background(), "corp", "alice", "Alice", executionID, core.Object{"prompt": "again"})
	must(t, err)
	if newID := again.O("execution").S("executionId"); newID == "" || newID == executionID {
		t.Fatalf("Run Again must create a new execution, got %q", newID)
	}

	bindings := again["skillBindings"].([]core.Object)
	if len(bindings) != 2 {
		t.Fatalf("Run Again must re-resolve both skills, got %d", len(bindings))
	}
	// Deterministic order: alpha then beta.
	if bindings[0].S("canonicalName") != "alpha" || bindings[0].S("skillRevisionId") != rev2 {
		t.Fatalf("Run Again must freeze alpha→R2, got %s→%s", bindings[0].S("canonicalName"), bindings[0].S("skillRevisionId"))
	}
	if bindings[1].S("canonicalName") != "beta" || bindings[1].S("skillRevisionId") != revB {
		t.Fatalf("Run Again must freeze beta→%s, got %s→%s", revB, bindings[1].S("canonicalName"), bindings[1].S("skillRevisionId"))
	}

	// The prior execution's snapshot is untouched.
	var oldRev string
	must(t, f.store.Pool.QueryRow(`SELECT skill_revision_id FROM execution_skill_bindings WHERE execution_id=$1 AND skill_id=$2`, executionID, skillA).Scan(&oldRev))
	if oldRev != rev1 {
		t.Fatalf("Run Again must not mutate the prior snapshot: want %s got %s", rev1, oldRev)
	}
}

// TestAdmissionReadsDurableBindingsOnly pins the selection authority: admission consumes only the
// enabled durable bindings to live Skills — disabled and soft-deleted Skills never enter the snapshot.
func TestAdmissionReadsDurableBindingsOnly(t *testing.T) {
	f := newExecFixture(t)
	agent := insertAgent(t, f.store.Pool, f.ws1, "runner", f.owner)
	skillA, _ := seedActiveSkill(t, f.store.Pool, f.ws1, "alpha", f.owner)
	skillB, _ := seedActiveSkill(t, f.store.Pool, f.ws1, "beta", f.owner)
	skillC, _ := seedActiveSkill(t, f.store.Pool, f.ws1, "gamma", f.owner)
	insertBinding(t, f.store.Pool, agent, skillA, true)
	insertBinding(t, f.store.Pool, agent, skillB, false) // disabled — excluded
	insertBinding(t, f.store.Pool, agent, skillC, true)  // enabled, but Skill soft-deleted — excluded
	execOK(t, f.store.Pool, `UPDATE skills SET deleted_at=now(),version=version+1,updated_at=now() WHERE id=$1`, skillC)

	out, err := f.admit("alice", f.ws1, agent)
	must(t, err)
	bindings := out["skillBindings"].([]core.Object)
	if len(bindings) != 1 || bindings[0].S("skillId") != skillA {
		t.Fatalf("admission must freeze only the enabled live skill, got %v", bindings)
	}
}

// TestAdmissionRejectsNoRevisionSkill pins fail-closed admission: a bound live Skill with no current
// revision rejects the whole admission and persists nothing.
func TestAdmissionRejectsNoRevisionSkill(t *testing.T) {
	f := newExecFixture(t)
	agent := insertAgent(t, f.store.Pool, f.ws1, "runner", f.owner)
	skillID := insertSkill(t, f.store.Pool, f.ws1, "alpha", f.owner) // no revision activated
	insertBinding(t, f.store.Pool, agent, skillID, true)

	_, err := f.admit("alice", f.ws1, agent)
	if err == nil {
		t.Fatal("admission must fail closed on a bound skill with no current revision")
	}
	if fault := core.ErrorCode(err); fault.Status != 409 || fault.Code != "skill_revision_not_available" {
		t.Fatalf("want 409 skill_revision_not_available, got %d %s", fault.Status, fault.Code)
	}

	for _, q := range []string{
		"SELECT count(*) FROM executions",
		"SELECT count(*) FROM attempts",
		"SELECT count(*) FROM execution_skill_bindings",
	} {
		var n int
		must(t, f.store.Pool.QueryRow(q).Scan(&n))
		if n != 0 {
			t.Fatalf("fail-closed admission must persist nothing: %s → %d", q, n)
		}
	}
}

// TestFirstAttemptAtomicWithExecution pins the D3 atomicity invariant: admission persists exactly one
// Execution, one eligible ordinal-1 Attempt with empty node/lease/epoch, and all bindings in one commit.
func TestFirstAttemptAtomicWithExecution(t *testing.T) {
	f := newExecFixture(t)
	agent := insertAgent(t, f.store.Pool, f.ws1, "runner", f.owner)
	skillID, rev := seedActiveSkill(t, f.store.Pool, f.ws1, "alpha", f.owner)
	insertBinding(t, f.store.Pool, agent, skillID, true)
	out, err := f.admit("alice", f.ws1, agent)
	must(t, err)
	executionID := out.O("execution").S("executionId")

	for q, want := range map[string]int{
		"SELECT count(*) FROM executions":                                     1,
		"SELECT count(*) FROM attempts WHERE execution_id=$1":                 1,
		"SELECT count(*) FROM execution_skill_bindings WHERE execution_id=$1": 1,
	} {
		var n int
		if strings.Contains(q, "$1") {
			must(t, f.store.Pool.QueryRow(q, executionID).Scan(&n))
		} else {
			must(t, f.store.Pool.QueryRow(q).Scan(&n))
		}
		if n != want {
			t.Fatalf("%s → want %d got %d", q, want, n)
		}
	}

	var ordinal int
	var state string
	var nodeID sql.NullString
	var epoch sql.NullInt64
	var revision string
	must(t, f.store.Pool.QueryRow(`SELECT ordinal, state, node_id, dispatched_epoch FROM attempts WHERE execution_id=$1`, executionID).Scan(&ordinal, &state, &nodeID, &epoch))
	if ordinal != 1 || state != "eligible" {
		t.Fatalf("first attempt must be ordinal 1 eligible, got %d %s", ordinal, state)
	}
	if nodeID.Valid || epoch.Valid {
		t.Fatalf("first attempt must have empty node_id/dispatched_epoch, got %v/%v", nodeID, epoch)
	}
	must(t, f.store.Pool.QueryRow(`SELECT skill_revision_id FROM execution_skill_bindings WHERE execution_id=$1 AND skill_id=$2`, executionID, skillID).Scan(&revision))
	if revision != rev {
		t.Fatalf("frozen revision mismatch: want %s got %s", rev, revision)
	}
}

// TestBindingsOrderedDeterministically pins the D12 ordering: frozen bindings are read back in
// canonical_name ascending order regardless of insertion order.
func TestBindingsOrderedDeterministically(t *testing.T) {
	f := newExecFixture(t)
	agent := insertAgent(t, f.store.Pool, f.ws1, "runner", f.owner)
	zebra, _ := seedActiveSkill(t, f.store.Pool, f.ws1, "zebra", f.owner)
	alpha, _ := seedActiveSkill(t, f.store.Pool, f.ws1, "alpha", f.owner)
	mango, _ := seedActiveSkill(t, f.store.Pool, f.ws1, "mango", f.owner)
	insertBinding(t, f.store.Pool, agent, zebra, true)
	insertBinding(t, f.store.Pool, agent, alpha, true)
	insertBinding(t, f.store.Pool, agent, mango, true)

	out, err := f.admit("alice", f.ws1, agent)
	must(t, err)
	bindings := out["skillBindings"].([]core.Object)
	if len(bindings) != 3 {
		t.Fatalf("want 3 bindings, got %d", len(bindings))
	}
	for i, want := range []string{"alpha", "mango", "zebra"} {
		if got := bindings[i].S("canonicalName"); got != want {
			t.Fatalf("binding order must be canonical_name ascending: index %d want %s got %s", i, want, got)
		}
	}
}

// TestAdmissionAuthorization pins the workspace-membership boundary: active members may admit, while
// non-members, cross-workspace callers and a disabled Agent are all 404 without existence leaks.
func TestAdmissionAuthorization(t *testing.T) {
	f := newExecFixture(t)
	agent := insertAgent(t, f.store.Pool, f.ws1, "runner", f.owner)
	skillID, _ := seedActiveSkill(t, f.store.Pool, f.ws1, "alpha", f.owner)
	insertBinding(t, f.store.Pool, agent, skillID, true)

	if _, err := f.admit("alice", f.ws1, agent); err != nil {
		t.Fatalf("owner must admit: %v", err)
	}
	if _, err := f.admit("bob", f.ws1, agent); err != nil {
		t.Fatalf("active member must admit: %v", err)
	}

	if _, err := f.admit("carol", f.ws1, agent); err == nil {
		t.Fatal("non-member must be rejected")
	} else if fault := core.ErrorCode(err); fault.Status != 404 || fault.Code != "not_found" {
		t.Fatalf("non-member: want 404 not_found, got %d %s", fault.Status, fault.Code)
	}

	if _, err := f.admit("alice", f.ws2, agent); err == nil {
		t.Fatal("cross-workspace admission must be rejected")
	} else if fault := core.ErrorCode(err); fault.Status != 404 {
		t.Fatalf("cross-workspace: want 404, got %d", fault.Status)
	}

	disabled := insertAgent(t, f.store.Pool, f.ws1, "paused", f.owner)
	insertBinding(t, f.store.Pool, disabled, skillID, true)
	execOK(t, f.store.Pool, `UPDATE agents SET status='disabled',version=version+1,updated_at=now() WHERE id=$1`, disabled)
	if _, err := f.admit("alice", f.ws1, disabled); err == nil {
		t.Fatal("disabled agent must be rejected")
	} else if fault := core.ErrorCode(err); fault.Status != 404 || fault.Code != "agent_not_available" {
		t.Fatalf("disabled agent: want 404 agent_not_available, got %d %s", fault.Status, fault.Code)
	}
}
