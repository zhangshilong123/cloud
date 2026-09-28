package integration

// Step UI-API public contract (specs/decisions/cloud/skills/20260927-execution-skill-snapshot.md
// Step UI-API) pinned end-to-end over real HTTP:
//
//   - GET /skills and GET /skills/:skillId expose only public identity + the exact current revision
//     (id, content_digest, size_bytes, package_format, package_format_version) — never an object
//     locator, bucket, storage key, provider, or signed URL;
//   - the Skill list is workspace-scoped (member+; non-member / cross-workspace → 404 no-leak),
//     excludes soft-deleted Skills, and is deterministically ordered by (canonical_name, skill id)
//     with the repository's limit/after cursor;
//   - POST /executions accepts only {agentId}, delegates to the Phase 5 authority, and returns the
//     execution plus its first (real, eligible) attempt and frozen bindings; the generic
//     Idempotency-Key replays the original Execution instead of creating a second one;
//   - GET /executions/:executionId reads the frozen snapshot, which survives later revision drift and
//     skill soft-delete, and never leaks credentials.

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/google/uuid"
)

// seedSkillWithRevision inserts a live Skill and activates a revision, returning skillID/revisionID.
// (Reuses the Phase 5 test seed via the shared integration helpers.)
func seedSkillWithRevision(t *testing.T, f *agentFixture, name string) (skillID, revisionID string) {
	t.Helper()
	return seedActiveSkill(t, f.store.Pool, f.ws1, name, f.ownerUID)
}

// TestSkillListAndDetailPublic pins the Skill read surface: ordering, currentRevision shape,
// soft-delete exclusion, stable pagination, and the member/cross-workspace/no-leak boundaries.
func TestSkillListAndDetailPublic(t *testing.T) {
	f := newAgentFixture(t)

	alpha, revA := seedSkillWithRevision(t, f, "alpha")
	_, _ = seedSkillWithRevision(t, f, "beta")
	_, _ = seedSkillWithRevision(t, f, "gamma")
	gone := insertSkill(t, f.store.Pool, f.ws1, "delta", f.ownerUID)
	execOK(t, f.store.Pool, `UPDATE skills SET deleted_at=now(),version=version+1,updated_at=now() WHERE id=$1`, gone)

	// The full list is deterministic (canonical_name ascending) and excludes the soft-deleted skill.
	status, body := f.do("alice", http.MethodGet, f.ws1, "/skills", nil, "")
	if status != http.StatusOK {
		t.Fatalf("list skills: got %d (%v)", status, body)
	}
	items := body["items"].([]any)
	if len(items) != 3 {
		t.Fatalf("list skills: want 3 live skills, got %d (%v)", len(items), items)
	}
	for i, want := range []string{"alpha", "beta", "gamma"} {
		if got := items[i].(map[string]any)["canonicalName"]; got != want {
			t.Fatalf("list skills must be canonical_name ordered: index %d want %s got %v", i, want, got)
		}
	}
	// Every item carries public identity and a nested currentRevision with public fields only.
	for _, it := range items {
		m := it.(map[string]any)
		for _, key := range []string{"id", "workspaceId", "canonicalName", "displayName", "summary", "version", "createdAt", "updatedAt"} {
			if m[key] == nil {
				t.Fatalf("skill item missing public field %s: %v", key, m)
			}
		}
		rev, ok := m["currentRevision"].(map[string]any)
		if !ok {
			t.Fatalf("skill item must nest currentRevision: %v", m)
		}
		for _, key := range []string{"id", "contentDigest", "sizeBytes", "packageFormat", "packageFormatVersion"} {
			if rev[key] == nil {
				t.Fatalf("currentRevision missing public field %s: %v", key, rev)
			}
		}
	}

	// Stable pagination: limit=2 returns the first two plus an opaque cursor; after=cursor returns
	// the remainder without repeating or skipping.
	status, page1 := f.do("alice", http.MethodGet, f.ws1, "/skills?limit=2", nil, "")
	if status != http.StatusOK {
		t.Fatalf("list skills limit=2: got %d (%v)", status, page1)
	}
	p1 := page1["items"].([]any)
	if len(p1) != 2 || p1[0].(map[string]any)["canonicalName"] != "alpha" || p1[1].(map[string]any)["canonicalName"] != "beta" {
		t.Fatalf("limit=2 page wrong: %v", p1)
	}
	cursor, _ := page1["nextCursor"].(string)
	if cursor == "" {
		t.Fatal("limit=2 must report a nextCursor")
	}
	status, page2 := f.do("alice", http.MethodGet, f.ws1, "/skills?limit=2&after="+cursor, nil, "")
	if status != http.StatusOK {
		t.Fatalf("list skills after cursor: got %d (%v)", status, page2)
	}
	p2 := page2["items"].([]any)
	if len(p2) != 1 || p2[0].(map[string]any)["canonicalName"] != "gamma" {
		t.Fatalf("cursor page must return exactly the remainder, got %v", p2)
	}

	// Detail reads one skill with its current revision.
	status, body = f.do("alice", http.MethodGet, f.ws1, "/skills/"+alpha, nil, "")
	if status != http.StatusOK || body["canonicalName"] != "alpha" {
		t.Fatalf("get skill: got %d (%v)", status, body)
	}
	rev, _ := body["currentRevision"].(map[string]any)
	if rev == nil || rev["id"] != revA {
		t.Fatalf("get skill must expose the exact current revision, got %v", body["currentRevision"])
	}

	// A soft-deleted skill and an absent skill are uniformly 404 (no existence leak).
	if status, body := f.do("alice", http.MethodGet, f.ws1, "/skills/"+gone, nil, ""); status != http.StatusNotFound || body["code"] != "not_found" {
		t.Fatalf("soft-deleted skill must 404 not_found, got %d (%v)", status, body)
	}
	if status, body := f.do("alice", http.MethodGet, f.ws1, "/skills/"+uuid.NewString(), nil, ""); status != http.StatusNotFound || body["code"] != "not_found" {
		t.Fatalf("absent skill must 404 not_found, got %d (%v)", status, body)
	}

	// Member can read; non-member and cross-workspace are 404.
	if status, body := f.do("bob", http.MethodGet, f.ws1, "/skills/"+alpha, nil, ""); status != http.StatusOK {
		t.Fatalf("member read skill: %d (%v)", status, body)
	}
	if status, body := f.do("carol", http.MethodGet, f.ws1, "/skills/"+alpha, nil, ""); status != http.StatusNotFound || body["code"] != "not_found" {
		t.Fatalf("non-member get skill must 404 not_found, got %d (%v)", status, body)
	}
	if status, body := f.do("alice", http.MethodGet, f.ws2, "/skills/"+alpha, nil, ""); status != http.StatusNotFound || body["code"] != "not_found" {
		t.Fatalf("cross-workspace get skill must 404 not_found, got %d (%v)", status, body)
	}
	if status, body := f.do("carol", http.MethodGet, f.ws1, "/skills", nil, ""); status != http.StatusNotFound {
		t.Fatalf("non-member list skills must 404, got %d (%v)", status, body)
	}
}

// TestExecutionAdmissionPublic pins the POST /executions contract: agentId-only input, the real
// first eligible attempt, the frozen binding set, idempotent replay, and membership boundaries.
func TestExecutionAdmissionPublic(t *testing.T) {
	f := newAgentFixture(t)
	agent := f.createAgent("runner", "exec-agent")
	skillID, rev := seedSkillWithRevision(t, f, "alpha")
	insertBinding(t, f.store.Pool, agent, skillID, true)

	status, body := f.do("alice", http.MethodPost, f.ws1, "/executions", map[string]any{"agentId": agent}, "exec-1")
	if status != http.StatusOK {
		t.Fatalf("admit execution: got %d (%v)", status, body)
	}
	execution := body["execution"].(map[string]any)
	executionID, _ := execution["executionId"].(string)
	if executionID == "" || execution["agentId"] != agent {
		t.Fatalf("execution identity wrong: %v", execution)
	}
	// The first attempt is real and durable-eligible, never a faked READY/running/succeeded.
	attempts := body["attempts"].([]any)
	if len(attempts) != 1 || attempts[0].(map[string]any)["state"] != "eligible" {
		t.Fatalf("first attempt must be exactly one eligible attempt, got %v", attempts)
	}
	// The frozen bindings carry the exact revision.
	bindings := body["skillBindings"].([]any)
	if len(bindings) != 1 || bindings[0].(map[string]any)["skillRevisionId"] != rev || bindings[0].(map[string]any)["canonicalName"] != "alpha" {
		t.Fatalf("frozen binding wrong: %v", bindings)
	}

	// Same Idempotency-Key + same body replays the original Execution — no second one is created.
	status, replayed := f.do("alice", http.MethodPost, f.ws1, "/executions", map[string]any{"agentId": agent}, "exec-1")
	if status != http.StatusOK || replayed["execution"].(map[string]any)["executionId"] != executionID {
		t.Fatalf("replay must return the same execution, got %d (%v)", status, replayed)
	}
	var n int
	must(t, f.store.Pool.QueryRow("SELECT count(*) FROM executions").Scan(&n))
	if n != 1 {
		t.Fatalf("idempotent admission must create exactly one execution, got %d", n)
	}

	// GET reads the same aggregate back.
	status, body = f.do("alice", http.MethodGet, f.ws1, "/executions/"+executionID, nil, "")
	if status != http.StatusOK || body["execution"].(map[string]any)["executionId"] != executionID {
		t.Fatalf("get execution: got %d (%v)", status, body)
	}

	// Missing/invalid agentId is 404; an unknown body field is 400.
	if status, body := f.do("alice", http.MethodPost, f.ws1, "/executions", map[string]any{}, "exec-empty"); status != http.StatusNotFound || body["code"] != "not_found" {
		t.Fatalf("admission without agentId must 404, got %d (%v)", status, body)
	}
	if status, body := f.do("alice", http.MethodPost, f.ws1, "/executions", map[string]any{"agentId": agent, "skillIds": []string{}}, "exec-extra"); status != http.StatusBadRequest || body["code"] != "unknown_field" {
		t.Fatalf("unknown field must 400 unknown_field, got %d (%v)", status, body)
	}

	// Non-member admission is 404; an active member admits (a fresh key → a new Execution).
	if status, body := f.do("carol", http.MethodPost, f.ws1, "/executions", map[string]any{"agentId": agent}, "exec-carol"); status != http.StatusNotFound {
		t.Fatalf("non-member admission must 404, got %d (%v)", status, body)
	}
	if status, body := f.do("bob", http.MethodPost, f.ws1, "/executions", map[string]any{"agentId": agent}, "exec-bob"); status != http.StatusOK {
		t.Fatalf("member admission must 200, got %d (%v)", status, body)
	}
}

// TestExecutionSnapshotImmutabilityPublic pins the frozen-snapshot invariant over the public surface:
// E1 freezes R1; the Skill then advances to R2; GET E1 still reports R1; a new admission E2 freezes R2.
func TestExecutionSnapshotImmutabilityPublic(t *testing.T) {
	f := newAgentFixture(t)
	agent := f.createAgent("runner", "imm-agent")
	skillID, rev1 := seedSkillWithRevision(t, f, "alpha")
	insertBinding(t, f.store.Pool, agent, skillID, true)

	// E1 freezes R1.
	status, body := f.do("alice", http.MethodPost, f.ws1, "/executions", map[string]any{"agentId": agent}, "imm-1")
	if status != http.StatusOK {
		t.Fatalf("admit E1: %d (%v)", status, body)
	}
	e1, _ := body["execution"].(map[string]any)["executionId"].(string)

	// Advance the Skill to R2 (still live).
	rev2 := insertRevision(t, f.store.Pool, skillID, strings.Repeat("b", 64), f.ownerUID)
	execOK(t, f.store.Pool, `UPDATE skills SET current_revision_id=$2,version=version+1,updated_at=now() WHERE id=$1`, skillID, rev2)

	// GET E1 still reports R1 — the snapshot never re-reads the mutable current pointer.
	status, got := f.do("alice", http.MethodGet, f.ws1, "/executions/"+e1, nil, "")
	if status != http.StatusOK {
		t.Fatalf("get E1: %d (%v)", status, got)
	}
	b1 := got["skillBindings"].([]any)
	if len(b1) != 1 || b1[0].(map[string]any)["skillRevisionId"] != rev1 {
		t.Fatalf("E1 snapshot must stay at R1, got %v", b1)
	}

	// E2 (fresh key) re-resolves current configuration and freezes R2.
	status, body = f.do("alice", http.MethodPost, f.ws1, "/executions", map[string]any{"agentId": agent}, "imm-2")
	if status != http.StatusOK {
		t.Fatalf("admit E2: %d (%v)", status, body)
	}
	e2, _ := body["execution"].(map[string]any)["executionId"].(string)
	b2 := body["skillBindings"].([]any)
	if len(b2) != 1 || b2[0].(map[string]any)["skillRevisionId"] != rev2 || b2[0].(map[string]any)["canonicalName"] != "alpha" {
		t.Fatalf("E2 must freeze alpha→R2, got %v", b2)
	}
	if e2 == e1 {
		t.Fatal("E2 must be a distinct execution")
	}
}

// TestExecutionPublicNoCredentialLeakage pins the D7/D8 boundary across every new response: skill
// list/detail and execution admission/read never carry an object locator, bucket, storage key,
// provider, credential, or signed URL.
func TestExecutionPublicNoCredentialLeakage(t *testing.T) {
	f := newAgentFixture(t)
	agent := f.createAgent("runner", "leak-agent")
	skillID, _ := seedSkillWithRevision(t, f, "alpha")
	insertBinding(t, f.store.Pool, agent, skillID, true)

	var responses []map[string]any
	if status, body := f.do("alice", http.MethodGet, f.ws1, "/skills", nil, ""); status == http.StatusOK {
		responses = append(responses, body)
	}
	if status, body := f.do("alice", http.MethodGet, f.ws1, "/skills/"+skillID, nil, ""); status == http.StatusOK {
		responses = append(responses, body)
	}
	if status, body := f.do("alice", http.MethodPost, f.ws1, "/executions", map[string]any{"agentId": agent}, "leak-1"); status == http.StatusOK {
		responses = append(responses, body)
		if id, _ := body["execution"].(map[string]any)["executionId"].(string); id != "" {
			if status, got := f.do("alice", http.MethodGet, f.ws1, "/executions/"+id, nil, ""); status == http.StatusOK {
				responses = append(responses, got)
			}
		}
	}

	for _, r := range responses {
		encoded, _ := json.Marshal(r)
		low := strings.ToLower(string(encoded))
		for _, forbidden := range []string{
			"object_locator", "objectlocator", "signed", "bucket", "presign",
			"storage_key", "storagekey", "credential", "secret", "authorization", "provider",
		} {
			if strings.Contains(low, forbidden) {
				t.Fatalf("public response leaks %q: %s", forbidden, encoded)
			}
		}
	}
}
