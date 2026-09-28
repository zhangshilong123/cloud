package integration

// Phase 6A.1 — Skill Delivery Metadata Plumbing. These tests pin:
//
//   - migration 0019 (skill_revisions.package_digest / package_digest_algorithm and
//     execution_skill_bindings.digest_algorithm / package_digest / package_digest_algorithm) on both a
//     fresh database and an existing 0018 deployment, with legacy pre-0019 rows left NULL (never
//     fabricated by migration SQL) (§3/§4/§20);
//   - new ingestion writes persist the trusted byte-level package digest alongside the content digest
//     (§5/§21);
//   - revision reuse is allowed only when the durable package_digest proves byte-equivalence, and
//     stops (revision_delivery_conflict) rather than silently writing a different physical digest into
//     an immutable historical row (§6/§21);
//   - execution admission freezes the full delivery metadata set once, retry reuses it unchanged, and
//     a legacy revision lacking the trusted package_digest fails closed (revision_delivery_unavailable)
//     instead of admitting an unverifiable Execution (§8/§9/§10/§11/§21).

import (
	"context"
	"database/sql"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/wanglongan587/cloud/internal/core"
)

// insertRevisionMeta inserts a revision with independent content and package digests so a frozen
// snapshot can prove the two layers are carried distinctly.
func insertRevisionMeta(t *testing.T, pool *sql.DB, skillID, contentDigest, packageDigest, user string) string {
	t.Helper()
	id := uuid.NewString()
	execOK(t, pool, `INSERT INTO skill_revisions(id,skill_id,digest_algorithm,content_digest,package_digest,package_digest_algorithm,size_bytes,file_count,created_by)
		VALUES($1,$2,'sha256',$3,$4,'sha256',1024,2,$5)`,
		id, skillID, contentDigest, packageDigest, user)
	return id
}

func columnDefault(t *testing.T, pool *sql.DB, table, column string) string {
	t.Helper()
	var def sql.NullString
	must(t, pool.QueryRow(`SELECT column_default FROM information_schema.columns
		WHERE table_schema=current_schema() AND table_name=$1 AND column_name=$2`, table, column).Scan(&def))
	if !def.Valid {
		return ""
	}
	return def.String
}

// TestSkillDeliveryMigrationFresh verifies a clean database through 0019: the delivery metadata
// columns exist with the legacy-safe nullability and defaults, the content digest algorithm stays on
// skill_revisions.digest_algorithm (never overloaded by the package digest layer), and the bindings
// delivery-digest index exists.
func TestSkillDeliveryMigrationFresh(t *testing.T) {
	pool, _ := testSchema(t, "sdm_fresh_")
	store := newStoreOnSchema(t, pool)
	must(t, store.Migrate(context.Background()))
	must(t, store.Migrate(context.Background()))
	must(t, store.CheckSchema(context.Background()))

	for _, col := range []string{"package_digest", "package_digest_algorithm"} {
		if !columnExists(t, pool, "skill_revisions", col) {
			t.Fatalf("skill_revisions missing delivery column %s", col)
		}
	}
	if !columnExists(t, pool, "skill_revisions", "digest_algorithm") {
		t.Fatal("skill_revisions.digest_algorithm must remain (content digest layer)")
	}
	if !columnNullable(t, pool, "skill_revisions", "package_digest") {
		t.Fatal("skill_revisions.package_digest must be nullable (legacy-row safe)")
	}
	if def := columnDefault(t, pool, "skill_revisions", "package_digest_algorithm"); def != "'sha256'::text" {
		t.Fatalf("skill_revisions.package_digest_algorithm default want sha256, got %q", def)
	}

	for _, col := range []string{"digest_algorithm", "package_digest", "package_digest_algorithm"} {
		if !columnExists(t, pool, "execution_skill_bindings", col) {
			t.Fatalf("execution_skill_bindings missing delivery column %s", col)
		}
	}
	for _, col := range []string{"digest_algorithm", "package_digest"} {
		if !columnNullable(t, pool, "execution_skill_bindings", col) {
			t.Fatalf("execution_skill_bindings.%s must be nullable (pre-0019 slice)", col)
		}
	}
	if def := columnDefault(t, pool, "execution_skill_bindings", "package_digest_algorithm"); def != "'sha256'::text" {
		t.Fatalf("execution_skill_bindings.package_digest_algorithm default want sha256, got %q", def)
	}
	var idx int
	must(t, pool.QueryRow(`SELECT count(*) FROM pg_indexes WHERE schemaname=current_schema() AND indexname='execution_skill_bindings_delivery_digest'`).Scan(&idx))
	if idx != 1 {
		t.Fatal("execution_skill_bindings_delivery_digest index missing")
	}
}

// TestSkillDeliveryMigrationUpgradePath reproduces an existing 0018 deployment that already holds a
// legacy pre-0019 revision (no package_digest), then lets the current binary apply 0019 on top:
// no duplicate DDL, no checksum mismatch, the legacy row is left NULL (0019 never fabricates a
// digest by parsing object_locator or touching Object Storage), and new columns become available.
func TestSkillDeliveryMigrationUpgradePath(t *testing.T) {
	pool, _ := testSchema(t, "sdm_upg_")
	applyMigrationsUpTo(t, pool, []string{
		"0001_core.sql", "0002_aggregate_guards.sql", "0003_resource_versions.sql",
		"0004_effect_intent_and_ticket_scope.sql", "0005_gateway_auth.sql",
		"0006_collab_spaces.sql", "0007_project_space_scope.sql",
		"0008_issues.sql", "0009_issue_extensions.sql", "0010_issue_collaboration.sql",
		"0011_issue_interactions.sql", "0012_issue_interaction_input.sql",
		"0013_project_space_optional.sql", "0014_clone_coordination.sql",
		"0015_skills.sql", "0016_skill_ingestion_idempotency.sql", "0017_agents_and_skill_bindings.sql",
		"0018_execution_snapshot.sql",
	})
	// A legacy pre-0019 revision row (delivery columns do not exist yet).
	user, _, ws, _ := seedSkillBase(t, pool)
	legacySkill := insertSkill(t, pool, ws, "legacy", user)
	legacyRev := insertRevisionLegacy(t, pool, legacySkill, strings.Repeat("a", 64), user)
	execOK(t, pool, `UPDATE skills SET current_revision_id=$2,version=version+1,updated_at=now() WHERE id=$1`, legacySkill, legacyRev)

	store := newStoreOnSchema(t, pool)
	must(t, store.Migrate(context.Background()))
	must(t, store.Migrate(context.Background()))
	must(t, store.CheckSchema(context.Background()))

	for _, col := range []string{"package_digest", "package_digest_algorithm"} {
		if !columnExists(t, pool, "skill_revisions", col) {
			t.Fatalf("skill_revisions missing %s after upgrade", col)
		}
	}
	for _, col := range []string{"digest_algorithm", "package_digest", "package_digest_algorithm"} {
		if !columnExists(t, pool, "execution_skill_bindings", col) {
			t.Fatalf("execution_skill_bindings missing %s after upgrade", col)
		}
	}
	// 0019 must NOT fabricate a digest for the legacy row (§4).
	var pd sql.NullString
	must(t, pool.QueryRow(`SELECT package_digest FROM skill_revisions WHERE id=$1`, legacyRev).Scan(&pd))
	if pd.Valid {
		t.Fatalf("0019 must not backfill a fabricated package_digest for a legacy row, got %q", pd.String)
	}
}

// TestIngestionPersistsPackageDigest pins §5: a freshly ingested revision carries the trusted
// byte-level package digest and its algorithm, structurally distinct from the content/tree digest.
func TestIngestionPersistsPackageDigest(t *testing.T) {
	store, _, user, ws := skillIngestFixture(t)
	res := ingestOK(t, store, ws, user, "k1", "alpha", "", "body one\n", "")

	var contentDigest, digestAlgorithm, packageDigest, packageDigestAlgo string
	must(t, store.Pool.QueryRow(`SELECT content_digest, digest_algorithm, package_digest, package_digest_algorithm
		FROM skill_revisions WHERE id=$1`, res.RevisionID).Scan(&contentDigest, &digestAlgorithm, &packageDigest, &packageDigestAlgo))
	if contentDigest == "" || packageDigest == "" {
		t.Fatalf("expected durable content+package digests, got %q / %q", contentDigest, packageDigest)
	}
	if digestAlgorithm != "sha256" || packageDigestAlgo != "sha256" {
		t.Fatalf("digest algorithms must be sha256/sha256, got %s/%s", digestAlgorithm, packageDigestAlgo)
	}
	for _, d := range []string{contentDigest, packageDigest} {
		if len(d) != 64 {
			t.Fatalf("digest must be 64 hex chars, got %q (%d)", d, len(d))
		}
	}
}

// TestRevisionReuseConflictsOnPhysicalDigestMismatch pins §6's STOP path: content_digest alone does
// not prove the exact physical bytes, so when a content-identical revision exists but its durable
// package_digest differs from the candidate, ingestion stops (revision_delivery_conflict) rather than
// silently reusing an immutable row whose physical bytes cannot equal the new candidate.
func TestRevisionReuseConflictsOnPhysicalDigestMismatch(t *testing.T) {
	store, _, user, ws := skillIngestFixture(t)
	first := ingestOK(t, store, ws, user, "k1", "alpha", "", "body\n", "")

	// Simulate a historical desync: the committed revision's durable package_digest no longer matches
	// what this exact content actually encodes. Content digest still matches the candidate, so only
	// the package_digest audit can catch the reuse as unsafe.
	execOK(t, store.Pool, `UPDATE skill_revisions SET package_digest=$2 WHERE id=$1`, first.RevisionID, strings.Repeat("f", 64))

	res, err := store.IngestSkill(context.Background(), ws, user, &core.SkillIngestRequest{
		IdempotencyKey: "k2",
		SourceType:     "directory",
		Files:          skillSources("alpha", "", "body\n"),
	})
	if err == nil {
		t.Fatalf("expected revision_delivery_conflict, got committed %+v", res)
	}
	f := core.ErrorCode(err)
	if f.Code != "revision_delivery_conflict" || f.Status != 409 {
		t.Fatalf("want 409 revision_delivery_conflict, got %d %s", f.Status, f.Code)
	}

	// The immutable historical revision must be untouched by the rejected candidate.
	var pd string
	must(t, store.Pool.QueryRow(`SELECT package_digest FROM skill_revisions WHERE id=$1`, first.RevisionID).Scan(&pd))
	if pd != strings.Repeat("f", 64) {
		t.Fatalf("rejected reuse must not overwrite the historical revision digest, got %q", pd)
	}
}

// TestRevisionReuseDigestEquivalence pins §6's allowed path: a byte-equivalent re-ingest (content and
// durable package_digest both match) reuses the immutable revision without a new row.
func TestRevisionReuseDigestEquivalence(t *testing.T) {
	store, _, user, ws := skillIngestFixture(t)
	first := ingestOK(t, store, ws, user, "k1", "alpha", "", "body\n", "")
	second := ingestOK(t, store, ws, user, "k2", "alpha", "", "body\n", "")

	if second.RevisionID != first.RevisionID {
		t.Fatalf("byte-equivalent content must reuse the same revision, got %s vs %s", first.RevisionID, second.RevisionID)
	}
	var revisions int
	must(t, store.Pool.QueryRow(`SELECT count(*) FROM skill_revisions`).Scan(&revisions))
	if revisions != 1 {
		t.Fatalf("reuse must not create a second revision, got %d", revisions)
	}
}

// TestAdmissionFreezesDeliveryMetadata pins §7/§8/§10/§11: admission persists the full delivery
// metadata once (content digest_algorithm + content digest + package digest + its algorithm), retry
// reuses the same frozen values, and Run Again re-freezes current values into a new Execution.
func TestAdmissionFreezesDeliveryMetadata(t *testing.T) {
	f := newExecFixture(t)
	agent := insertAgent(t, f.store.Pool, f.ws1, "runner", f.owner)
	skillID := insertSkill(t, f.store.Pool, f.ws1, "alpha", f.owner)
	// Independent content vs package digest so the two layers are proven distinct in the snapshot.
	rev := insertRevisionMeta(t, f.store.Pool, skillID, strings.Repeat("a", 64), strings.Repeat("9", 64), f.owner)
	execOK(t, f.store.Pool, `UPDATE skills SET current_revision_id=$2,version=version+1,updated_at=now() WHERE id=$1`, skillID, rev)
	insertBinding(t, f.store.Pool, agent, skillID, true)

	out, err := f.admit("alice", f.ws1, agent)
	must(t, err)
	executionID := out.O("execution").S("executionId")

	var digestAlgo, contentDigest, packageDigest, packageDigestAlgo string
	must(t, f.store.Pool.QueryRow(`SELECT digest_algorithm, content_digest, package_digest, package_digest_algorithm
		FROM execution_skill_bindings WHERE execution_id=$1 AND skill_id=$2`, executionID, skillID).
		Scan(&digestAlgo, &contentDigest, &packageDigest, &packageDigestAlgo))
	if digestAlgo != "sha256" || contentDigest != strings.Repeat("a", 64) ||
		packageDigest != strings.Repeat("9", 64) || packageDigestAlgo != "sha256" {
		t.Fatalf("frozen delivery metadata mismatch: %s %s %s %s", digestAlgo, contentDigest, packageDigest, packageDigestAlgo)
	}

	// Retry reuses the same frozen values without re-reading mutable Skill state.
	_, err = f.store.CreateRetryAttempt(context.Background(), executionID)
	must(t, err)
	var cnt int
	must(t, f.store.Pool.QueryRow(`SELECT count(*) FROM execution_skill_bindings WHERE execution_id=$1 AND skill_id=$2`, executionID, skillID).Scan(&cnt))
	if cnt != 1 {
		t.Fatalf("retry must not add or rewrite a binding, got %d rows", cnt)
	}

	// Run Again re-resolves current and freezes into a brand-new Execution.
	again, err := f.store.RunAgainExecution(context.Background(), "corp", "alice", "Alice", executionID, core.Object{"prompt": "again"})
	must(t, err)
	againID := again.O("execution").S("executionId")
	if againID == "" || againID == executionID {
		t.Fatalf("Run Again must create a distinct execution, got %q", againID)
	}
	b := again["skillBindings"].([]core.Object)
	if len(b) != 1 || b[0].S("packageDigest") != strings.Repeat("9", 64) || b[0].S("digestAlgorithm") != "sha256" {
		t.Fatalf("Run Again must re-freeze current delivery metadata, got %+v", b)
	}
}

// TestAdmissionLegacyMissingDigestFailsClosed pins §9: a live revision that cannot provide the
// trusted package_digest (a pre-0019 legacy row) rejects the whole admission with an explicit error
// and persists nothing — it never parses object_locator, skips byte-verification, or substitutes
// content_digest.
func TestAdmissionLegacyMissingDigestFailsClosed(t *testing.T) {
	f := newExecFixture(t)
	agent := insertAgent(t, f.store.Pool, f.ws1, "runner", f.owner)
	skillID := insertSkill(t, f.store.Pool, f.ws1, "alpha", f.owner)
	rev := insertRevisionLegacy(t, f.store.Pool, skillID, strings.Repeat("a", 64), f.owner) // NULL package_digest
	execOK(t, f.store.Pool, `UPDATE skills SET current_revision_id=$2,version=version+1,updated_at=now() WHERE id=$1`, skillID, rev)
	insertBinding(t, f.store.Pool, agent, skillID, true)

	_, err := f.admit("alice", f.ws1, agent)
	if err == nil {
		t.Fatal("admission must fail closed on a revision with no trusted package digest")
	}
	if fault := core.ErrorCode(err); fault.Status != 409 || fault.Code != "revision_delivery_unavailable" {
		t.Fatalf("want 409 revision_delivery_unavailable, got %d %s", fault.Status, fault.Code)
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
