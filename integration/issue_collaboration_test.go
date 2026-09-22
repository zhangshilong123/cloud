package integration

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/stdlib"

	"github.com/wanglongan587/cloud/internal/core"
)

// TestIssueCollaborationMigrationBackfills applies the pre-0010 schema (0001-0009, before the
// issue-collaboration backfill), seeds legacy-shaped rows, then runs Migrate() to confirm
// 0010 backfills the assignee ActorRef and per-issue comment seq/author.
func TestIssueCollaborationMigrationBackfills(t *testing.T) {
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		if os.Getenv("REQUIRE_POSTGRES") == "1" {
			t.Fatal("TEST_DATABASE_URL is required; PostgreSQL integration must not skip")
		}
		t.Skip("real PostgreSQL: set TEST_DATABASE_URL (task test:integration requires it)")
	}
	config, err := pgx.ParseConfig(dsn)
	must(t, err)
	admin := stdlib.OpenDB(*config)
	must(t, admin.Ping())
	schema := "test_collab_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	_, err = admin.Exec("CREATE SCHEMA " + schema)
	must(t, err)
	config.RuntimeParams["search_path"] = schema
	pool := stdlib.OpenDB(*config)
	t.Cleanup(func() {
		must(t, pool.Close())
		_, e := admin.Exec("DROP SCHEMA " + schema + " CASCADE")
		must(t, e)
		must(t, admin.Close())
	})

	// Apply everything before 0010 (the issue-collaboration backfill) so issue_comments
	// still has its legacy single-author shape.
	_, err = pool.Exec("CREATE TABLE schema_migrations(version text PRIMARY KEY, checksum text NOT NULL, applied_at timestamptz NOT NULL DEFAULT now())")
	must(t, err)
	for _, version := range []string{"0001_core.sql", "0002_aggregate_guards.sql", "0003_resource_versions.sql", "0004_effect_intent_and_ticket_scope.sql", "0005_gateway_auth.sql", "0006_collab_spaces.sql", "0007_project_space_scope.sql", "0008_issues.sql", "0009_issue_extensions.sql"} {
		migration, e := os.ReadFile(filepath.Join("..", "internal", "core", "migrations", version))
		must(t, e)
		_, e = pool.Exec(string(migration))
		must(t, e)
		sum := sha256.Sum256(migration)
		_, e = pool.Exec("INSERT INTO schema_migrations(version,checksum) VALUES($1,$2)", version, hex.EncodeToString(sum[:]))
		must(t, e)
	}

	tenant, user, issue := uuid.NewString(), uuid.NewString(), uuid.NewString()
	older, newer := uuid.NewString(), uuid.NewString()
	// Seed in one transaction: the deferred tenant_admin trigger requires an active admin member to
	// exist by commit time, so the active tenant and its membership must land together.
	tx, err := pool.Begin()
	must(t, err)
	seed := []struct {
		query string
		args  []any
	}{
		{"INSERT INTO users(id,display_name,status) VALUES($1,'Backfill user','active')", []any{user}},
		{"INSERT INTO tenants(id,name,status) VALUES($1,'Backfill tenant','active')", []any{tenant}},
		{"INSERT INTO tenant_memberships(tenant_id,user_id,role,status) VALUES($1,$2,'admin','active')", []any{tenant, user}},
		{"INSERT INTO issues(id,tenant_id,creator_user_id,assignee_user_id,title,number) VALUES($1,$2,$3,$3,'Backfill issue',1)", []any{issue, tenant, user}},
		{"INSERT INTO issue_comments(id,tenant_id,issue_id,author_user_id,body,created_at) VALUES($1,$2,$3,$4,'older',now()-interval '1 hour')", []any{older, tenant, issue, user}},
		{"INSERT INTO issue_comments(id,tenant_id,issue_id,author_user_id,body,created_at) VALUES($1,$2,$3,$4,'newer',now())", []any{newer, tenant, issue, user}},
	}
	for _, s := range seed {
		_, err = tx.Exec(s.query, s.args...)
		must(t, err)
	}
	must(t, tx.Commit())

	store := &core.Store{Pool: pool}
	must(t, store.Migrate(context.Background()))

	var assigneeType, assigneeID string
	must(t, pool.QueryRow("SELECT assignee_type, assignee_id::text FROM issues WHERE id=$1", issue).Scan(&assigneeType, &assigneeID))
	if assigneeType != "user" || assigneeID != user {
		t.Fatalf("issue assignee backfill wrong: type=%s id=%s want user %s", assigneeType, assigneeID, user)
	}
	rows, e := pool.Query("SELECT id, seq, author_type, author_id::text FROM issue_comments WHERE issue_id=$1", issue)
	must(t, e)
	got := map[string]struct {
		seq        int64
		authorType string
		authorID   string
	}{}
	for rows.Next() {
		var id, at, aid string
		var seq int64
		must(t, rows.Scan(&id, &seq, &at, &aid))
		got[id] = struct {
			seq        int64
			authorType string
			authorID   string
		}{seq, at, aid}
	}
	must(t, rows.Err())
	rows.Close()
	if got[older].seq != 1 || got[newer].seq != 2 {
		t.Fatalf("comment seq backfill wrong (want older=1 newer=2): %v", got)
	}
	for id, r := range got {
		if r.authorType != "user" || r.authorID != user {
			t.Fatalf("comment %s author backfill wrong: %v", id, got)
		}
	}
}

// TestIssueAssignmentAndProjectRef covers legacy assigneeUserId compat, typed assignee storage, and
// the shape-only project_ref — plus a light regression that the existing issue board still works.
func TestIssueAssignmentAndProjectRef(t *testing.T) {
	f := setup(t)

	// Legacy assigneeUserId maps to a user ActorRef and preserves the legacy column.
	legacy := f.call("POST", f.path("/issues"), core.Object{"title": "Legacy assignee", "assigneeUserId": f.uid}, "a-1", 200).O("resource")
	if legacy.S("assigneeType") != "user" || legacy.S("assigneeId") != f.uid || legacy.S("assigneeUserId") != f.uid {
		t.Fatalf("legacy assignee mapping wrong: %v", legacy)
	}

	// New assigneeType/assigneeId with a live user.
	typed := f.call("POST", f.path("/issues"), core.Object{"title": "Typed assignee", "assigneeType": "user", "assigneeId": f.uid}, "a-2", 200).O("resource")
	if typed.S("assigneeType") != "user" || typed.S("assigneeId") != f.uid || typed.S("assigneeUserId") != f.uid {
		t.Fatalf("typed user assignee wrong: %v", typed)
	}
	// User assignees are validated against active users this wave.
	f.call("POST", f.path("/issues"), core.Object{"title": "Ghost", "assigneeType": "user", "assigneeId": uuid.NewString()}, "a-3", 404)

	// Agent assignee is an opaque UUID ref (no ActorResolver yet); assigneeUserId stays null.
	agentID := uuid.NewString()
	agent := f.call("POST", f.path("/issues"), core.Object{"title": "Agent assignee", "assigneeType": "agent", "assigneeId": agentID}, "a-4", 200).O("resource")
	if agent.S("assigneeType") != "agent" || agent.S("assigneeId") != agentID || agent["assigneeUserId"] != nil {
		t.Fatalf("opaque agent assignee wrong: %v", agent)
	}

	// projectRef round-trips (shape-only, no existence check this wave).
	proj := uuid.NewString()
	withRef := f.call("POST", f.path("/issues"), core.Object{"title": "Ref", "projectRef": proj}, "a-5", 200).O("resource")
	if withRef.S("projectRef") != proj {
		t.Fatalf("projectRef not stored: %v", withRef)
	}
	cleared := f.call("PUT", f.path("/issues/"+withRef.S("id")), core.Object{"projectRef": "", "version": withRef.N("version")}, "", 200)
	if cleared["projectRef"] != nil {
		t.Fatalf("projectRef clear failed: %v", cleared)
	}
	f.call("PUT", f.path("/issues/"+withRef.S("id")), core.Object{"projectRef": "not-a-uuid", "version": cleared.N("version")}, "", 400)

	// Regression: board list/get/update still behave.
	board := issueItems(f.call("GET", f.path("/issues"), nil, "", 200))
	if len(board) == 0 {
		t.Fatal("issue board regressed to empty")
	}
	renamed := f.call("PUT", f.path("/issues/"+legacy.S("id")), core.Object{"title": "Renamed", "version": legacy.N("version")}, "", 200)
	if renamed.S("title") != "Renamed" {
		t.Fatalf("issue update regressed: %v", renamed)
	}
}

// TestIssueCommentThreadingAndSharedTimeline covers same-issue thread parents and the single
// per-issue seq namespace shared by comments and run activities.
func TestIssueCommentThreadingAndSharedTimeline(t *testing.T) {
	f := setup(t)
	a := f.call("POST", f.path("/issues"), core.Object{"title": "Thread A"}, "t-a", 200).O("resource")
	b := f.call("POST", f.path("/issues"), core.Object{"title": "Thread B"}, "t-b", 200).O("resource")

	parent := f.call("POST", f.path("/issues/"+a.S("id")+"/comments"), core.Object{"body": "root"}, "t-1", 200).O("resource")
	reply := f.call("POST", f.path("/issues/"+a.S("id")+"/comments"), core.Object{"body": "reply", "parentId": parent.S("id")}, "t-2", 200).O("resource")
	if reply.S("parentId") != parent.S("id") {
		t.Fatalf("parentId not stored: %v", reply)
	}
	// Cross-issue parent violates the same-issue invariant.
	f.call("POST", f.path("/issues/"+b.S("id")+"/comments"), core.Object{"body": "x", "parentId": parent.S("id")}, "t-3", 404)
	// Malformed parent id is rejected.
	f.call("POST", f.path("/issues/"+a.S("id")+"/comments"), core.Object{"body": "x", "parentId": "nope"}, "t-4", 400)

	// Fresh issue so the timeline starts at seq 1: comments and run activities interleave in one space.
	c := f.call("POST", f.path("/issues"), core.Object{"title": "Timeline"}, "t-tl", 200).O("resource")
	c1 := f.call("POST", f.path("/issues/"+c.S("id")+"/comments"), core.Object{"body": "one"}, "t-c1", 200).O("resource")
	f.call("POST", f.path("/issues/"+c.S("id")+"/runs"), core.Object{"executorType": "agent", "executorId": uuid.NewString()}, "t-r1", 200)
	c2 := f.call("POST", f.path("/issues/"+c.S("id")+"/comments"), core.Object{"body": "three"}, "t-c2", 200).O("resource")
	f.call("POST", f.path("/issues/"+c.S("id")+"/runs"), core.Object{"executorType": "agent", "executorId": uuid.NewString()}, "t-r2", 200)

	if f.scalar("SELECT seq FROM issue_comments WHERE id=$1", c1.S("id")) != 1 {
		t.Fatal("comment 1 seq != 1")
	}
	if f.scalar("SELECT seq FROM issue_comments WHERE id=$1", c2.S("id")) != 3 {
		t.Fatal("comment 2 seq != 3")
	}
	rows, e := f.store.Pool.Query("SELECT seq FROM issue_activities WHERE issue_id=$1 ORDER BY seq", c.S("id"))
	must(t, e)
	seqs := []int{}
	for rows.Next() {
		var s int
		must(t, rows.Scan(&s))
		seqs = append(seqs, s)
	}
	must(t, rows.Err())
	rows.Close()
	if len(seqs) != 2 || seqs[0] != 2 || seqs[1] != 4 {
		t.Fatalf("activity seqs should be [2 4] (shared namespace): %v", seqs)
	}
}

// TestIssueRuns covers enqueue/list/get, pending dedup, the status CHECK constraint, and that a
// terminal status is ordinary history rather than deletion.
func TestIssueRuns(t *testing.T) {
	f := setup(t)
	issue := f.call("POST", f.path("/issues"), core.Object{"title": "Run me"}, "r-issue", 200).O("resource")

	executor := uuid.NewString()
	run := f.call("POST", f.path("/issues/"+issue.S("id")+"/runs"), core.Object{"executorType": "agent", "executorId": executor, "input": core.Object{"prompt": "hi"}}, "run-1", 200).O("resource")
	if run.S("status") != "queued" || run.S("executorType") != "agent" || run.S("executorId") != executor {
		t.Fatalf("unexpected enqueued run: %v", run)
	}
	if run.O("input").S("prompt") != "hi" {
		t.Fatalf("run input not stored: %v", run)
	}

	got := f.call("GET", f.path("/issues/"+issue.S("id")+"/runs/"+run.S("id")), nil, "", 200)
	if got.S("id") != run.S("id") {
		t.Fatalf("run fetch wrong: %v", got)
	}
	if len(issueItems(f.call("GET", f.path("/issues/"+issue.S("id")+"/runs"), nil, "", 200))) != 1 {
		t.Fatal("run not listed")
	}
	// A second pending run for the same executor conflicts.
	f.call("POST", f.path("/issues/"+issue.S("id")+"/runs"), core.Object{"executorType": "agent", "executorId": executor}, "run-2", 409)

	// An unknown status violates the database CHECK constraint.
	if _, e := f.store.Pool.Exec("INSERT INTO issue_runs(id,tenant_id,issue_id,executor_type,executor_id,status) VALUES($1,$2,$3,'agent',$4,'bogus')", uuid.NewString(), f.tid, issue.S("id"), executor); e == nil {
		t.Fatal("bogus run status was accepted")
	}

	// Terminal status is normal history (deleted_at stays null).
	if _, e := f.store.Pool.Exec("UPDATE issue_runs SET status='completed' WHERE id=$1", run.S("id")); e != nil {
		t.Fatal(e)
	}
	if len(issueItems(f.call("GET", f.path("/issues/"+issue.S("id")+"/runs"), nil, "", 200))) != 1 {
		t.Fatal("completed run disappeared from list")
	}
	completed := f.call("GET", f.path("/issues/"+issue.S("id")+"/runs/"+run.S("id")), nil, "", 200)
	if completed.S("status") != "completed" || completed["deletedAt"] != nil {
		t.Fatalf("terminal status should not delete: %v", completed)
	}
}

// TestIssueContextRefsAndIsolation covers context-ref CRUD plus cross-tenant isolation for the new routes.
func TestIssueContextRefsAndIsolation(t *testing.T) {
	f := setup(t)
	issue := f.call("POST", f.path("/issues"), core.Object{"title": "Refs"}, "c-issue", 200).O("resource")

	proj := uuid.NewString()
	ref := f.call("POST", f.path("/issues/"+issue.S("id")+"/context-refs"), core.Object{"refType": "project", "refId": proj}, "cr-1", 200).O("resource")
	if ref.S("refType") != "project" || ref.S("refId") != proj {
		t.Fatalf("context ref not stored: %v", ref)
	}
	// Duplicate (issue, ref_type, ref_id) conflicts; unknown type is rejected.
	f.call("POST", f.path("/issues/"+issue.S("id")+"/context-refs"), core.Object{"refType": "project", "refId": proj}, "cr-2", 409)
	f.call("POST", f.path("/issues/"+issue.S("id")+"/context-refs"), core.Object{"refType": "nope", "refId": proj}, "cr-3", 400)

	if len(issueItems(f.call("GET", f.path("/issues/"+issue.S("id")+"/context-refs"), nil, "", 200))) != 1 {
		t.Fatal("context ref not listed")
	}
	// Hard delete (join-like row, no version/soft delete); replaying against a new key is 404.
	f.call("DELETE", f.path("/issues/"+issue.S("id")+"/context-refs/"+ref.S("id")), core.Object{}, "cr-4", 200)
	if len(issueItems(f.call("GET", f.path("/issues/"+issue.S("id")+"/context-refs"), nil, "", 200))) != 0 {
		t.Fatal("delete did not remove context ref")
	}
	f.call("DELETE", f.path("/issues/"+issue.S("id")+"/context-refs/"+ref.S("id")), core.Object{}, "cr-5", 404)

	// Seed a run so the isolation check also covers the run routes.
	f.call("POST", f.path("/issues/"+issue.S("id")+"/runs"), core.Object{"executorType": "team", "executorId": uuid.NewString()}, "cr-run", 200)

	other, e := f.store.Bootstrap(context.Background(), "Other", "corp", "other", "Other")
	must(t, e)
	original := f.tid
	f.tid = other.S("tenantId")
	f.call("GET", f.path("/issues/"+issue.S("id")+"/runs"), nil, "", 403)
	f.call("GET", f.path("/issues/"+issue.S("id")+"/context-refs"), nil, "", 403)
	f.call("GET", f.path("/issues/"+issue.S("id")+"/comments"), nil, "", 403)
	f.tid = original
}
