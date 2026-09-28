package integration

// Skill persistence foundation (Phase 2 / Step 1A): the 0015 migration introduces exactly three
// first-class resources — skills, skill_revisions, skill_ingestions — and the constraints below pin
// the invariants from specs/decisions/cloud/skills/0-cloud-skills.md that can be enforced in SQL:
//
//   - a Skill belongs to exactly one Collaboration Workspace (collab_workspaces, never the runtime
//     workspaces table) and that owner is immutable across updates;
//   - active Skill canonical names are unique within a Workspace; soft delete releases the name;
//   - SkillRevision is immutable (no version/updated_at/deleted_at) and belongs to one Skill;
//   - one SkillRevision per (skill_id, digest_algorithm, content_digest), while equal content in
//     different Skills stays two independent revisions;
//   - current_revision_id can only name a revision owned by the very same Skill;
//   - SkillIngestion is journal-first saga evidence with the Cloud-side state naming.
//
// These tests are pure-SQL constraint tests against an isolated PostgreSQL schema, the same
// technique as migration_upgrade_path_test.go.

import (
	"context"
	"database/sql"
	"strings"
	"testing"

	"github.com/google/uuid"
)

func columnExists(t *testing.T, pool *sql.DB, table, column string) bool {
	t.Helper()
	var n int
	must(t, pool.QueryRow(`SELECT count(*) FROM information_schema.columns WHERE table_schema=current_schema() AND table_name=$1 AND column_name=$2`, table, column).Scan(&n))
	return n == 1
}

func execOK(t *testing.T, pool *sql.DB, q string, args ...any) {
	t.Helper()
	if _, e := pool.Exec(q, args...); e != nil {
		t.Fatalf("expected success: %v\n%s", e, q)
	}
}

func execErr(t *testing.T, pool *sql.DB, q string, args ...any) {
	t.Helper()
	if _, e := pool.Exec(q, args...); e == nil {
		t.Fatalf("expected error: %s", q)
	}
}

// seedSkillBase provisions one tenant with an admin member and two Collaboration Workspaces,
// returning the ids needed to create Skills. The tenant/membership/workspace rows live in one
// transaction so the deferred last_admin trigger passes at commit.
func seedSkillBase(t *testing.T, pool *sql.DB) (user, tenant, ws1, ws2 string) {
	t.Helper()
	user, tenant, ws1, ws2 = uuid.NewString(), uuid.NewString(), uuid.NewString(), uuid.NewString()
	tx, e := pool.Begin()
	must(t, e)
	_, e = tx.Exec(`INSERT INTO users(id,display_name,status) VALUES($1,'Skill user','active')`, user)
	must(t, e)
	_, e = tx.Exec(`INSERT INTO tenants(id,name,status) VALUES($1,'Skill tenant','active')`, tenant)
	must(t, e)
	_, e = tx.Exec(`INSERT INTO tenant_memberships(tenant_id,user_id,role,status) VALUES($1,$2,'admin','active')`, tenant, user)
	must(t, e)
	_, e = tx.Exec(`INSERT INTO collab_workspaces(id,tenant_id,name,slug,description,created_by) VALUES($1,$2,'Default','default','',$3)`, ws1, tenant, user)
	must(t, e)
	_, e = tx.Exec(`INSERT INTO collab_workspaces(id,tenant_id,name,slug,description,created_by) VALUES($1,$2,'Other','other','',$3)`, ws2, tenant, user)
	must(t, e)
	must(t, tx.Commit())
	return user, tenant, ws1, ws2
}

func insertSkill(t *testing.T, pool *sql.DB, ws, canonicalName, user string) string {
	t.Helper()
	id := uuid.NewString()
	execOK(t, pool, `INSERT INTO skills(id,workspace_id,canonical_name,display_name,created_by) VALUES($1,$2,$3,$4,$5)`,
		id, ws, canonicalName, canonicalName, user)
	return id
}

func insertRevision(t *testing.T, pool *sql.DB, skillID, digest, user string) string {
	t.Helper()
	id := uuid.NewString()
	// Since Phase 6A.1 new writes carry the trusted delivery package_digest + its algorithm (plus the
	// content digest_algorithm) alongside the immutable content digest; the migration keeps these
	// nullable for legacy rows, but the core admission path fails closed when they are absent.
	execOK(t, pool, `INSERT INTO skill_revisions(id,skill_id,digest_algorithm,content_digest,package_digest,package_digest_algorithm,size_bytes,file_count,created_by)
		VALUES($1,$2,'sha256',$3,$3,'sha256',1024,2,$4)`,
		id, skillID, digest, user)
	return id
}

// insertRevisionLegacy inserts a pre-0019 SkillRevision row with no durable package digest (the
// legacy rollout case) so the fail-closed admission path can be pinned.
func insertRevisionLegacy(t *testing.T, pool *sql.DB, skillID, digest, user string) string {
	t.Helper()
	id := uuid.NewString()
	execOK(t, pool, `INSERT INTO skill_revisions(id,skill_id,content_digest,size_bytes,file_count,created_by) VALUES($1,$2,$3,1024,2,$4)`,
		id, skillID, digest, user)
	return id
}

// TestSkillMigrationFresh verifies a clean database through 0015: the three tables exist, Skill is
// structurally mutable while SkillRevision is structurally immutable, and skills.workspace_id
// references collab_workspaces (the Collaboration Workspace), not the runtime workspaces table.
func TestSkillMigrationFresh(t *testing.T) {
	pool, _ := testSchema(t, "skill_fresh_")
	store := newStoreOnSchema(t, pool)
	must(t, store.Migrate(context.Background()))
	must(t, store.Migrate(context.Background()))
	must(t, store.CheckSchema(context.Background()))

	for _, table := range []string{"skills", "skill_revisions", "skill_ingestions"} {
		if !tableExists(t, pool, table) {
			t.Fatalf("missing skill table %s", table)
		}
	}

	// Skill is a mutable business resource: version + updated_at + deleted_at.
	for _, col := range []string{"version", "updated_at", "deleted_at"} {
		if !columnExists(t, pool, "skills", col) {
			t.Fatalf("skills must be mutable: missing column %s", col)
		}
	}
	// SkillRevision is append-only: none of those mutation columns exist.
	for _, col := range []string{"version", "updated_at", "deleted_at"} {
		if columnExists(t, pool, "skill_revisions", col) {
			t.Fatalf("skill_revisions must be immutable: column %s must not exist", col)
		}
	}

	// The referenced table behind skills.workspace_id must be collab_workspaces.
	var refTable string
	must(t, pool.QueryRow(`SELECT ccu.table_name
		FROM information_schema.referential_constraints rc
		JOIN information_schema.key_column_usage kcu
			ON rc.constraint_name=kcu.constraint_name AND rc.constraint_schema=kcu.constraint_schema
		JOIN information_schema.constraint_column_usage ccu
			ON rc.unique_constraint_name=ccu.constraint_name AND rc.unique_constraint_schema=ccu.constraint_schema
		WHERE rc.unique_constraint_schema=current_schema() AND kcu.table_schema=current_schema()
			AND kcu.table_name='skills' AND kcu.column_name='workspace_id'`).Scan(&refTable))
	if refTable != "collab_workspaces" {
		t.Fatalf("skills.workspace_id must reference collab_workspaces, got %s", refTable)
	}

	var idx int
	must(t, pool.QueryRow(`SELECT count(*) FROM pg_indexes WHERE schemaname=current_schema() AND indexname='skill_active_name_uniq'`).Scan(&idx))
	if idx != 1 {
		t.Fatal("skill_active_name_uniq partial unique index missing")
	}
}

// TestSkillMigrationUpgradePath reproduces an existing deployment migrated through 0014, then lets
// the current binary apply 0015 on top: no duplicate DDL, no checksum mismatch, tables present.
func TestSkillMigrationUpgradePath(t *testing.T) {
	pool, _ := testSchema(t, "skill_upg_")
	applyMigrationsUpTo(t, pool, []string{
		"0001_core.sql", "0002_aggregate_guards.sql", "0003_resource_versions.sql",
		"0004_effect_intent_and_ticket_scope.sql", "0005_gateway_auth.sql",
		"0006_collab_spaces.sql", "0007_project_space_scope.sql",
		"0008_issues.sql", "0009_issue_extensions.sql", "0010_issue_collaboration.sql",
		"0011_issue_interactions.sql", "0012_issue_interaction_input.sql",
		"0013_project_space_optional.sql", "0014_clone_coordination.sql",
	})
	store := newStoreOnSchema(t, pool)
	must(t, store.Migrate(context.Background()))
	must(t, store.Migrate(context.Background()))
	must(t, store.CheckSchema(context.Background()))
	for _, table := range []string{"skills", "skill_revisions", "skill_ingestions"} {
		if !tableExists(t, pool, table) {
			t.Fatalf("missing skill table %s after upgrade", table)
		}
	}
}

// TestSkillOwnershipAndNameUniqueness pins the Skill identity invariants: a Skill belongs to a
// Collaboration Workspace, active canonical names are unique per Workspace (not across), soft delete
// releases the name, and the owning Workspace cannot change on an ordinary update.
func TestSkillOwnershipAndNameUniqueness(t *testing.T) {
	pool, _ := testSchema(t, "skill_own_")
	store := newStoreOnSchema(t, pool)
	must(t, store.Migrate(context.Background()))
	user, _, ws1, ws2 := seedSkillBase(t, pool)

	// A Skill must belong to a real Collaboration Workspace.
	execErr(t, pool, `INSERT INTO skills(id,workspace_id,canonical_name,display_name,created_by) VALUES($1,$2,'x','x',$3)`, uuid.NewString(), uuid.NewString(), user)
	execOK(t, pool, `INSERT INTO skills(id,workspace_id,canonical_name,display_name,created_by) VALUES($1,$2,'alpha','alpha',$3)`, uuid.NewString(), ws1, user)

	// Same canonical name in the same Workspace is rejected...
	execErr(t, pool, `INSERT INTO skills(id,workspace_id,canonical_name,display_name,created_by) VALUES($1,$2,'alpha','other',$3)`, uuid.NewString(), ws1, user)
	// ...but the same name in a different Workspace is an independent, valid Skill.
	execOK(t, pool, `INSERT INTO skills(id,workspace_id,canonical_name,display_name,created_by) VALUES($1,$2,'alpha','alpha',$3)`, uuid.NewString(), ws2, user)

	// Soft delete releases the name for a replacement in the same Workspace.
	deleted := insertSkill(t, pool, ws1, "beta", user)
	execOK(t, pool, `UPDATE skills SET deleted_at=now(),version=version+1,updated_at=now() WHERE id=$1`, deleted)
	execOK(t, pool, `INSERT INTO skills(id,workspace_id,canonical_name,display_name,created_by) VALUES($1,$2,'beta','beta',$3)`, uuid.NewString(), ws1, user)

	// The owning Workspace is immutable: an ordinary update must not move a Skill between Workspaces.
	skill := insertSkill(t, pool, ws1, "gamma", user)
	execErr(t, pool, `UPDATE skills SET workspace_id=$2 WHERE id=$1`, skill, ws2)
	// Metadata updates that keep the owner are fine.
	execOK(t, pool, `UPDATE skills SET display_name='gamma-renamed',version=version+1,updated_at=now() WHERE id=$1`, skill)
}

// TestSkillRevisionInvariants pins SkillRevision identity and the current_revision pointer: one
// revision per (skill, digest), distinct revisions for equal content across Skills, and a
// current_revision_id that can only point at a revision of the very same Skill.
func TestSkillRevisionInvariants(t *testing.T) {
	pool, _ := testSchema(t, "skill_rev_")
	store := newStoreOnSchema(t, pool)
	must(t, store.Migrate(context.Background()))
	user, _, ws1, _ := seedSkillBase(t, pool)

	skillA := insertSkill(t, pool, ws1, "a", user)
	skillB := insertSkill(t, pool, ws1, "b", user)
	digestX := strings.Repeat("a", 64)
	digestY := strings.Repeat("b", 64)

	// A revision must belong to a real Skill.
	execErr(t, pool, `INSERT INTO skill_revisions(id,skill_id,content_digest,created_by) VALUES($1,$2,$3,$4)`, uuid.NewString(), uuid.NewString(), digestX, user)

	revA1 := insertRevision(t, pool, skillA, digestX, user)
	// Same Skill + same digest must reuse one revision: a duplicate is rejected.
	execErr(t, pool, `INSERT INTO skill_revisions(id,skill_id,content_digest,created_by) VALUES($1,$2,$3,$4)`, uuid.NewString(), skillA, digestX, user)
	// Same Skill + different digest is a distinct revision.
	execOK(t, pool, `INSERT INTO skill_revisions(id,skill_id,content_digest,size_bytes,file_count,created_by) VALUES($1,$2,$3,1024,2,$4)`, uuid.NewString(), skillA, digestY, user)
	// Different Skill + same digest stays an independent business revision (no merge).
	execOK(t, pool, `INSERT INTO skill_revisions(id,skill_id,content_digest,size_bytes,file_count,created_by) VALUES($1,$2,$3,1024,2,$4)`, uuid.NewString(), skillB, digestX, user)

	// current_revision_id may point at the Skill's own revision...
	execOK(t, pool, `UPDATE skills SET current_revision_id=$2,version=version+1,updated_at=now() WHERE id=$1`, skillA, revA1)
	// ...and must never point at another Skill's revision (composite FK current_revision_id,id ->
	// skill_revisions.id,skill_id). Build a revision belonging to B, then try to pin it onto A.
	revB := insertRevision(t, pool, skillB, digestY, user)
	execErr(t, pool, `UPDATE skills SET current_revision_id=$2,version=version+1,updated_at=now() WHERE id=$1`, skillA, revB)
}

// TestSkillIngestionStates verifies the saga state naming consumes exactly the required stages and
// rejects unknown states or source types.
func TestSkillIngestionStates(t *testing.T) {
	pool, _ := testSchema(t, "skill_ing_")
	store := newStoreOnSchema(t, pool)
	must(t, store.Migrate(context.Background()))
	user, _, ws1, _ := seedSkillBase(t, pool)

	for _, state := range []string{"planned", "storing", "verified", "committed", "failed"} {
		execOK(t, pool, `INSERT INTO skill_ingestions(id,workspace_id,idempotency_key,source_type,state,created_by) VALUES($1,$2,$3,'directory',$4,$5)`,
			uuid.NewString(), ws1, "key-"+state, state, user)
	}
	execErr(t, pool, `INSERT INTO skill_ingestions(id,workspace_id,idempotency_key,source_type,state,created_by) VALUES($1,$2,$3,'directory','uploading',$4)`,
		uuid.NewString(), ws1, "bad-state", user)
	execErr(t, pool, `INSERT INTO skill_ingestions(id,workspace_id,idempotency_key,source_type,state,created_by) VALUES($1,$2,$3,'git','planned',$4)`,
		uuid.NewString(), ws1, "bad-source", user)
}
