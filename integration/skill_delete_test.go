package integration

// Skill soft-delete (DELETE /skills/:skillId) pinned end-to-end over real HTTP:
//
//   - D11 authority: the creator may always delete; an owner/admin may delete another member's
//     Skill; an ordinary member deleting someone else's Skill is 403 space_role_required; a
//     non-member and a foreign workspace are uniformly 404 (no existence leak);
//   - version discipline: missing version → 428 version_required, stale version → 409
//     version_conflict, matching version → 200;
//   - soft-delete semantics: only deleted_at is set — the SkillRevision row and the frozen
//     ExecutionSnapshot both survive, a frozen Execution stays readable at its exact revision,
//     the live list and detail 404, and future admission excludes the deleted Skill.

import (
	"net/http"
	"testing"
)

// TestSkillDeleteAuthority pins the D11 authorization boundary for the DELETE endpoint.
func TestSkillDeleteAuthority(t *testing.T) {
	f := newAgentFixture(t)
	skillID, _ := seedSkillWithRevision(t, f, "alpha")

	// Alice created the Skill, so she deletes it directly (version is read back first).
	_, body := f.do("alice", http.MethodGet, f.ws1, "/skills/"+skillID, nil, "")
	version := int(body["version"].(float64))
	if status, got := f.do("alice", http.MethodDelete, f.ws1, "/skills/"+skillID, map[string]any{"version": version}, "del-create"); status != http.StatusOK {
		t.Fatalf("creator delete: got %d (%v)", status, got)
	} else if got["canonicalName"] != "alpha" || got["currentRevision"] == nil {
		t.Fatalf("creator delete must return the frozen skill with currentRevision, got %v", got)
	}

	// A fresh-key delete of the already-soft-deleted Skill is a deterministic 404 (no leak).
	if status, got := f.do("alice", http.MethodDelete, f.ws1, "/skills/"+skillID, map[string]any{"version": version}, "del-again"); status != http.StatusNotFound || got["code"] != "not_found" {
		t.Fatalf("already-deleted delete must 404 not_found, got %d (%v)", status, got)
	}
}

// TestSkillDeleteRoleAndScope pins member/non-member/cross-workspace and owner-on-behalf-of-member.
func TestSkillDeleteRoleAndScope(t *testing.T) {
	f := newAgentFixture(t)
	skillID, _ := seedSkillWithRevision(t, f, "alpha")

	_, body := f.do("alice", http.MethodGet, f.ws1, "/skills/"+skillID, nil, "")
	version := int(body["version"].(float64))

	// A member who is not the creator cannot delete (403 space_role_required).
	if status, got := f.do("bob", http.MethodDelete, f.ws1, "/skills/"+skillID, map[string]any{"version": version}, "del-bob"); status != http.StatusForbidden || got["code"] != "space_role_required" {
		t.Fatalf("member delete must 403 space_role_required, got %d (%v)", status, got)
	}

	// A non-member is 404 (no existence leak).
	if status, got := f.do("carol", http.MethodDelete, f.ws1, "/skills/"+skillID, map[string]any{"version": version}, "del-carol"); status != http.StatusNotFound || got["code"] != "not_found" {
		t.Fatalf("non-member delete must 404 not_found, got %d (%v)", status, got)
	}

	// Addressing the Skill under a foreign workspace is 404.
	if status, got := f.do("alice", http.MethodDelete, f.ws2, "/skills/"+skillID, map[string]any{"version": version}, "del-xws"); status != http.StatusNotFound || got["code"] != "not_found" {
		t.Fatalf("cross-workspace delete must 404 not_found, got %d (%v)", status, got)
	}

	// The owner may delete a Skill created by another member (admin/owner branch of D11).
	var bobUID string
	must(t, f.store.Pool.QueryRow("SELECT u.id FROM users u JOIN user_identities i ON i.user_id=u.id WHERE i.subject='bob' AND i.source='corp'").Scan(&bobUID))
	others := insertSkill(t, f.store.Pool, f.ws1, "bobs-skill", bobUID)
	if status, got := f.do("alice", http.MethodDelete, f.ws1, "/skills/"+others, map[string]any{"version": 1}, "del-other"); status != http.StatusOK || got["canonicalName"] != "bobs-skill" {
		t.Fatalf("owner delete of member's skill: got %d (%v)", status, got)
	}
}

// TestSkillDeleteVersionDiscipline pins 428/409/200 across the version check.
func TestSkillDeleteVersionDiscipline(t *testing.T) {
	f := newAgentFixture(t)
	skillID, _ := seedSkillWithRevision(t, f, "alpha")

	_, body := f.do("alice", http.MethodGet, f.ws1, "/skills/"+skillID, nil, "")
	version := int(body["version"].(float64))

	// Missing version → 428; stale version → 409; matching version → 200.
	if status, got := f.do("alice", http.MethodDelete, f.ws1, "/skills/"+skillID, map[string]any{}, "del-nov"); status != http.StatusPreconditionRequired || got["code"] != "version_required" {
		t.Fatalf("missing version must 428 version_required, got %d (%v)", status, got)
	}
	if status, got := f.do("alice", http.MethodDelete, f.ws1, "/skills/"+skillID, map[string]any{"version": version + 1}, "del-stale"); status != http.StatusConflict || got["code"] != "version_conflict" {
		t.Fatalf("stale version must 409 version_conflict, got %d (%v)", status, got)
	}
	if status, got := f.do("alice", http.MethodDelete, f.ws1, "/skills/"+skillID, map[string]any{"version": version}, "del-match"); status != http.StatusOK || got["id"] != skillID {
		t.Fatalf("matching version delete: got %d (%v)", status, got)
	}
}

// TestSkillDeleteKeepsHistory pins the soft-delete semantics over the public surface: the Skill
// disappears from list/detail, the frozen Execution stays readable at its exact revision, future
// admission excludes the Skill, and the SkillRevision row survives.
func TestSkillDeleteKeepsHistory(t *testing.T) {
	f := newAgentFixture(t)
	agent := f.createAgent("runner", "del-agent")
	skillID, rev := seedSkillWithRevision(t, f, "alpha")
	insertBinding(t, f.store.Pool, agent, skillID, true)

	// Freeze Execution E1 → R1 before deleting the Skill.
	status, body := f.do("alice", http.MethodPost, f.ws1, "/executions", map[string]any{"agentId": agent}, "del-e1")
	if status != http.StatusOK {
		t.Fatalf("admit E1: %d (%v)", status, body)
	}
	e1, _ := body["execution"].(map[string]any)["executionId"].(string)

	// Delete the Skill (creator = owner).
	_, got := f.do("alice", http.MethodGet, f.ws1, "/skills/"+skillID, nil, "")
	version := int(got["version"].(float64))
	if status, got := f.do("alice", http.MethodDelete, f.ws1, "/skills/"+skillID, map[string]any{"version": version}, "del-keep"); status != http.StatusOK {
		t.Fatalf("delete skill: %d (%v)", status, got)
	}

	// The live list and detail 404.
	if status, got := f.do("alice", http.MethodGet, f.ws1, "/skills", nil, ""); status != http.StatusOK {
		t.Fatalf("list skills: %d (%v)", status, got)
	} else if items := got["items"].([]any); len(items) != 0 {
		t.Fatalf("live list must exclude the soft-deleted skill, got %v", items)
	}
	if status, got := f.do("alice", http.MethodGet, f.ws1, "/skills/"+skillID, nil, ""); status != http.StatusNotFound {
		t.Fatalf("deleted skill detail must 404, got %d (%v)", status, got)
	}

	// The frozen Execution E1 is still readable and still bound to R1.
	status, got = f.do("alice", http.MethodGet, f.ws1, "/executions/"+e1, nil, "")
	if status != http.StatusOK {
		t.Fatalf("frozen execution after delete: %d (%v)", status, got)
	}
	bindings := got["skillBindings"].([]any)
	if len(bindings) != 1 || bindings[0].(map[string]any)["skillRevisionId"] != rev {
		t.Fatalf("E1 must stay frozen at R1 after delete, got %v", bindings)
	}

	// Future admission excludes the deleted Skill: a fresh execution freezes zero bindings.
	status, body = f.do("alice", http.MethodPost, f.ws1, "/executions", map[string]any{"agentId": agent}, "del-e2")
	if status != http.StatusOK {
		t.Fatalf("admit E2: %d (%v)", status, body)
	}
	if bindings := body["skillBindings"].([]any); len(bindings) != 0 {
		t.Fatalf("future admission must exclude the deleted skill, got %v", bindings)
	}

	// The immutable SkillRevision survives (no hard delete, no eager GC).
	var revisions int
	must(t, f.store.Pool.QueryRow("SELECT count(*) FROM skill_revisions WHERE id=$1", rev).Scan(&revisions))
	if revisions != 1 {
		t.Fatalf("skill_revision must survive the soft delete, got %d", revisions)
	}
	var deletedAt any
	must(t, f.store.Pool.QueryRow("SELECT deleted_at IS NOT NULL FROM skills WHERE id=$1", skillID).Scan(&deletedAt))
	if deletedAt != true {
		t.Fatalf("skill must carry a deleted_at after delete")
	}
}