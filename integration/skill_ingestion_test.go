package integration

// Canonical Skill ingestion saga (Phase 2) integration tests. These drive
// core.Store.IngestSkill / IngestSkills against an isolated PostgreSQL schema with an in-memory
// fakestore.ObjectStore, pinning the saga obligations from
// specs/decisions/cloud/skills/20260927-canonical-ingestion-saga.md:
//
//   - journal-first: a committed candidate leaves skills + skill_revisions + skill_ingestions rows
//     with the durable activation_outcome;
//   - idempotent replay: the same key + same candidate returns the committed result without new rows;
//   - conflict: the same key + same name + different content is a 409 idempotency_conflict;
//   - revision dedup: the same content under the same Skill reuses one immutable revision;
//   - batch partial success: one candidate's failure never rolls back a sibling;
//   - reconcile by identity: a corrupt object at the locator fails closed (object_identity_mismatch);
//   - adopt: an already-present matching object is adopted without a re-PUT;
//   - client-driven continuation: a "storing" strand resumes on the same key;
//   - authorization: a non-admin workspace member cannot ingest;
//   - explicit update: target_skill_id is the authoritative business identity;
//   - the Object Storage port is required.

import (
	"context"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/wanglongan587/cloud/internal/core"
	"github.com/wanglongan587/cloud/internal/skillpkg"
	"github.com/wanglongan587/cloud/internal/skillstore"
	"github.com/wanglongan587/cloud/internal/skillstore/fakestore"
)

// skillSources builds one candidate Skill tree: a root SKILL.md with the given name/description
// frontmatter and Markdown body, plus one extra regular file so the tree is not a lone metadata file.
func skillSources(name, description, body string) []skillpkg.SourceFile {
	md := "---\nname: " + name + "\n"
	if description != "" {
		md += "description: " + description + "\n"
	}
	md += "---\n" + body
	return []skillpkg.SourceFile{
		{Path: "SKILL.md", Data: []byte(md)},
		{Path: "guide.md", Data: []byte("read me")},
	}
}

// skillLocatorOf computes the object locator a candidate would use, so a test can plant an object
// at the key before the saga runs.
func skillLocatorOf(t *testing.T, files []skillpkg.SourceFile) skillstore.Locator {
	t.Helper()
	bundle, err := skillpkg.Build(files, skillpkg.DefaultLimits())
	must(t, err)
	loc, err := skillstore.NewLocator(skillpkg.FormatName, skillpkg.FormatVersion, skillstore.AlgorithmSHA256, skillstore.PackageDigestHex(bundle.Package))
	must(t, err)
	return loc
}

// skillIngestFixture provisions one tenant/workspace with an owner member, wires a Store with an
// in-memory Object Store, and returns the pieces a saga test drives.
func skillIngestFixture(t *testing.T) (*core.Store, *fakestore.Fake, string, string) {
	t.Helper()
	pool, _ := testSchema(t, "skill_ingest_")
	store := newStoreOnSchema(t, pool)
	must(t, store.Migrate(context.Background()))
	user, _, ws, _ := seedSkillBase(t, pool)
	execOK(t, pool, `INSERT INTO collab_workspace_members(workspace_id,user_id,role,status,created_by) VALUES($1,$2,'owner','active',$2)`, ws, user)
	fake := &fakestore.Fake{}
	store.SkillsObjectStore = fake
	return store, fake, user, ws
}

// ingestOK runs one candidate and asserts the saga produced no error.
func ingestOK(t *testing.T, store *core.Store, ws, user, key, name, description, body, target string) core.SkillIngestResult {
	t.Helper()
	res, err := store.IngestSkill(context.Background(), ws, user, &core.SkillIngestRequest{
		IdempotencyKey: key,
		SourceType:     "directory",
		TargetSkillID:  target,
		Files:          skillSources(name, description, body),
	})
	must(t, err)
	return res
}

func TestSkillIngestCommitsAndActivates(t *testing.T) {
	store, fake, user, ws := skillIngestFixture(t)

	res := ingestOK(t, store, ws, user, "k1", "alpha", "desc", "body one\n", "")

	if res.State != "committed" || res.Activation != "activated" {
		t.Fatalf("want committed/activated, got %s/%s", res.State, res.Activation)
	}
	if res.SkillID == "" || res.RevisionID == "" || res.IngestionID == "" {
		t.Fatalf("expected resolved ids, got %+v", res)
	}

	// The package landed in Object Storage at the logical locator.
	loc := skillLocatorOf(t, skillSources("alpha", "desc", "body one\n"))
	if got := fake.Get(context.Background(), loc, 1<<30); got.Outcome != skillstore.GetPresent {
		t.Fatalf("object not stored: %v", got.Outcome)
	}

	// One Skill created with derived display_name (defaults to canonical_name) and empty summary.
	pool := store.Pool
	var canonicalName, displayName, summary string
	var version int64
	var currentRevision string
	must(t, pool.QueryRow(`SELECT canonical_name, display_name, summary, version, current_revision_id FROM skills`).Scan(&canonicalName, &displayName, &summary, &version, &currentRevision))
	if canonicalName != "alpha" || displayName != "alpha" || summary != "" {
		t.Fatalf("skill metadata: %q %q %q", canonicalName, displayName, summary)
	}
	if version != 2 { // created at 1, then the activation CAS bumped it
		t.Fatalf("want version 2 after activation CAS, got %d", version)
	}
	if currentRevision != res.RevisionID {
		t.Fatalf("current_revision_id %s != resolved revision %s", currentRevision, res.RevisionID)
	}

	// The journal row is committed with the durable activation outcome and backfilled target.
	var state, activation, target string
	must(t, pool.QueryRow(`SELECT state, activation_outcome, coalesce(target_skill_id::text,'') FROM skill_ingestions`).Scan(&state, &activation, &target))
	if state != "committed" || activation != "activated" || target != res.SkillID {
		t.Fatalf("journal: state=%s activation=%s target=%s", state, activation, target)
	}

	// One revision with the package identity columns populated.
	var packageName, packageDescription, objectLocator string
	var fileCount int
	must(t, pool.QueryRow(`SELECT package_name, package_description, object_locator, file_count FROM skill_revisions`).Scan(&packageName, &packageDescription, &objectLocator, &fileCount))
	if packageName != "alpha" || packageDescription != "desc" || fileCount != 2 {
		t.Fatalf("revision: %q %q files=%d", packageName, packageDescription, fileCount)
	}
	if !strings.HasPrefix(objectLocator, "skills/ora-skill-package/v1/sha256/") {
		t.Fatalf("unexpected object locator %q", objectLocator)
	}
}

func TestSkillIngestReplayIdempotent(t *testing.T) {
	store, _, user, ws := skillIngestFixture(t)

	first := ingestOK(t, store, ws, user, "k1", "alpha", "", "body\n", "")
	second := ingestOK(t, store, ws, user, "k1", "alpha", "", "body\n", "")

	if !second.Replayed {
		t.Fatal("second ingest must replay the committed journal row")
	}
	if second.State != "committed" || second.Activation != "activated" {
		t.Fatalf("replay state/activation: %s/%s", second.State, second.Activation)
	}
	if second.SkillID != first.SkillID {
		t.Fatalf("replay must return the same skill: %s != %s", second.SkillID, first.SkillID)
	}

	var skills, revisions, ingestions int
	must(t, store.Pool.QueryRow(`SELECT (SELECT count(*) FROM skills), (SELECT count(*) FROM skill_revisions), (SELECT count(*) FROM skill_ingestions)`).Scan(&skills, &revisions, &ingestions))
	if skills != 1 || revisions != 1 || ingestions != 1 {
		t.Fatalf("replay must not create rows: skills=%d revisions=%d ingestions=%d", skills, revisions, ingestions)
	}
}

func TestSkillIngestConflict(t *testing.T) {
	store, _, user, ws := skillIngestFixture(t)

	ingestOK(t, store, ws, user, "k1", "alpha", "", "body v1\n", "")

	// Same key, same name (same idempotency namespace), different content → deterministic conflict.
	_, err := store.IngestSkill(context.Background(), ws, user, &core.SkillIngestRequest{
		IdempotencyKey: "k1",
		SourceType:     "directory",
		Files:          skillSources("alpha", "", "body v2\n"),
	})
	if err == nil {
		t.Fatal("expected idempotency_conflict")
	}
	f := core.ErrorCode(err)
	if f.Code != "idempotency_conflict" || f.Status != 409 {
		t.Fatalf("want idempotency_conflict/409, got %s/%d", f.Code, f.Status)
	}
}

func TestSkillIngestRevisionDedup(t *testing.T) {
	store, _, user, ws := skillIngestFixture(t)

	first := ingestOK(t, store, ws, user, "k1", "alpha", "", "body\n", "")
	second := ingestOK(t, store, ws, user, "k2", "alpha", "", "body\n", "")

	// A different key with the same name + content converges to the same Skill and reuses the
	// same immutable revision (no duplicate row, no new revision).
	if second.State != "committed" || second.Activation != "activated" {
		t.Fatalf("dedup ingest: %s/%s", second.State, second.Activation)
	}
	if second.SkillID != first.SkillID || second.RevisionID != first.RevisionID {
		t.Fatalf("dedup must reuse skill+revision: %s/%s vs %s/%s", second.SkillID, second.RevisionID, first.SkillID, first.RevisionID)
	}

	var revisions, ingestions int
	must(t, store.Pool.QueryRow(`SELECT (SELECT count(*) FROM skill_revisions), (SELECT count(*) FROM skill_ingestions)`).Scan(&revisions, &ingestions))
	if revisions != 1 || ingestions != 2 {
		t.Fatalf("want 1 revision reused across 2 ingestions, got revisions=%d ingestions=%d", revisions, ingestions)
	}
}

func TestSkillIngestBatchPartialSuccess(t *testing.T) {
	store, _, user, ws := skillIngestFixture(t)

	reqs := []core.SkillIngestRequest{
		{Files: skillSources("alpha", "", "a\n")},
		{Files: []skillpkg.SourceFile{{Path: "SKILL.md", Data: []byte("---\ndescription: no name\n---\nbody\n")}, {Path: "x.md", Data: []byte("x")}}},
		{Files: skillSources("beta", "", "b\n")},
	}
	results, err := store.IngestSkills(context.Background(), ws, user, "batch-key", "directory", reqs)
	must(t, err)

	if len(results) != 3 {
		t.Fatalf("want 3 results, got %d", len(results))
	}
	if results[0].State != "committed" || results[2].State != "committed" {
		t.Fatalf("siblings must commit: %+v", results)
	}
	if results[1].ErrorCode != "skill_md_missing_name" {
		t.Fatalf("invalid candidate must fail closed with skill_md_missing_name, got %+v", results[1])
	}

	var skills, revisions int
	must(t, store.Pool.QueryRow(`SELECT (SELECT count(*) FROM skills), (SELECT count(*) FROM skill_revisions)`).Scan(&skills, &revisions))
	if skills != 2 || revisions != 2 {
		t.Fatalf("partial success: want 2 skills + 2 revisions, got %d + %d", skills, revisions)
	}
}

func TestSkillIngestReconcileMismatch(t *testing.T) {
	store, fake, user, ws := skillIngestFixture(t)

	// Plant a corrupt object at the exact locator the candidate resolves to.
	files := skillSources("alpha", "", "body\n")
	loc := skillLocatorOf(t, files)
	if !fake.Seed(loc, []byte("corrupt bytes, not a package")) {
		t.Fatal("seed must succeed")
	}

	res, err := store.IngestSkill(context.Background(), ws, user, &core.SkillIngestRequest{
		IdempotencyKey: "k1",
		SourceType:     "directory",
		Files:          files,
	})
	must(t, err)
	if res.State != "failed" || res.ErrorCode != "object_identity_mismatch" {
		t.Fatalf("want failed/object_identity_mismatch, got %s/%s", res.State, res.ErrorCode)
	}
}

func TestSkillIngestAdoptExisting(t *testing.T) {
	store, fake, user, ws := skillIngestFixture(t)

	files := skillSources("alpha", "", "body\n")
	loc := skillLocatorOf(t, files)
	bundle, err := skillpkg.Build(files, skillpkg.DefaultLimits())
	must(t, err)
	if !fake.Seed(loc, bundle.Package) {
		t.Fatal("seed must succeed")
	}
	// A matching pre-existing object must be adopted, never re-PUT.
	fake.PutFn = func(context.Context, *skillstore.PutRequest) skillstore.PutResult {
		t.Fatal("Put must not be called when the object already matches")
		return skillstore.PutResult{}
	}

	res := ingestOK(t, store, ws, user, "k1", "alpha", "", "body\n", "")
	if res.State != "committed" || res.Activation != "activated" {
		t.Fatalf("adopt: %s/%s", res.State, res.Activation)
	}
}

func TestSkillIngestStrandAndResume(t *testing.T) {
	store, fake, user, ws := skillIngestFixture(t)

	// First attempt strands: Stat is indeterminate, so the saga records 'storing' and returns
	// (never blind-writes, never fails on ambiguity).
	fake.StatFn = func(context.Context, skillstore.Locator) skillstore.StatResult {
		return skillstore.StatResult{Outcome: skillstore.StatIndeterminate}
	}
	res := ingestOK(t, store, ws, user, "k1", "alpha", "", "body\n", "")
	if res.State != "storing" {
		t.Fatalf("want storing strand, got %s", res.State)
	}

	// The provider recovers; re-submitting the same key resumes the same journal row to committed.
	fake.StatFn = nil
	res = ingestOK(t, store, ws, user, "k1", "alpha", "", "body\n", "")
	if res.State != "committed" || res.Activation != "activated" {
		t.Fatalf("resume: %s/%s", res.State, res.Activation)
	}

	var ingestions int
	must(t, store.Pool.QueryRow(`SELECT count(*) FROM skill_ingestions`).Scan(&ingestions))
	if ingestions != 1 {
		t.Fatalf("resume must reuse one journal row, got %d", ingestions)
	}
}

func TestSkillIngestAuthorization(t *testing.T) {
	store, _, user, ws := skillIngestFixture(t)

	// A plain member (not owner/admin) is denied the write.
	member := uuid.NewString()
	execOK(t, store.Pool, `INSERT INTO users(id,display_name,status) VALUES($1,'member','active')`, member)
	execOK(t, store.Pool, `INSERT INTO collab_workspace_members(workspace_id,user_id,role,status,created_by) VALUES($1,$2,'member','active',$3)`, ws, member, user)

	_, err := store.IngestSkill(context.Background(), ws, member, &core.SkillIngestRequest{
		IdempotencyKey: "k1",
		SourceType:     "directory",
		Files:          skillSources("alpha", "", "body\n"),
	})
	if err == nil {
		t.Fatal("member must be denied")
	}
	f := core.ErrorCode(err)
	if f.Code != "workspace_admin_required" || f.Status != 403 {
		t.Fatalf("want workspace_admin_required/403, got %s/%d", f.Code, f.Status)
	}
}

func TestSkillIngestExplicitUpdate(t *testing.T) {
	store, _, user, ws := skillIngestFixture(t)

	first := ingestOK(t, store, ws, user, "k1", "alpha", "", "v1\n", "")
	second := ingestOK(t, store, ws, user, "k2", "alpha", "", "v2\n", first.SkillID)

	if second.SkillID != first.SkillID {
		t.Fatalf("explicit update must keep the target skill: %s != %s", second.SkillID, first.SkillID)
	}
	if second.RevisionID == first.RevisionID {
		t.Fatal("explicit update with new content must produce a new revision")
	}

	var canonicalName, currentRevision string
	var revisions int
	must(t, store.Pool.QueryRow(`SELECT s.canonical_name, coalesce(s.current_revision_id::text,''), (SELECT count(*) FROM skill_revisions r WHERE r.skill_id=s.id) FROM skills s`).Scan(&canonicalName, &currentRevision, &revisions))
	if canonicalName != "alpha" {
		t.Fatalf("explicit update must not rename the Skill, got %q", canonicalName)
	}
	if revisions != 2 {
		t.Fatalf("want 2 revisions after explicit update, got %d", revisions)
	}
	if currentRevision != second.RevisionID {
		t.Fatalf("current_revision_id must be the new revision: %s != %s", currentRevision, second.RevisionID)
	}
}

func TestSkillIngestObjectStoreUnavailable(t *testing.T) {
	store, _, user, ws := skillIngestFixture(t)
	store.SkillsObjectStore = nil

	res, err := store.IngestSkill(context.Background(), ws, user, &core.SkillIngestRequest{
		IdempotencyKey: "k1",
		SourceType:     "directory",
		Files:          skillSources("alpha", "", "body\n"),
	})
	must(t, err)
	if res.State != "failed" || res.ErrorCode != "object_store_unavailable" {
		t.Fatalf("want failed/object_store_unavailable, got %s/%s", res.State, res.ErrorCode)
	}
}
