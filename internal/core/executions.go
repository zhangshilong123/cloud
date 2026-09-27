package core

// Execution / Attempt / ExecutionSkillBinding durable snapshot (Phase 5 of
// specs/decisions/cloud/skills/20260927-execution-snapshot.md).
//
// This file owns the logical Execution identity and its atomic admission freeze: it admits an
// Execution for one Agent by resolving the Agent's durable enabled Skill bindings to their exact
// current SkillRevisions and persisting them as immutable ExecutionSkillBinding rows, together with
// the first Attempt, in a single database-only transaction (D3). It also owns retry (a new Attempt
// over the same frozen bindings, never re-reading mutable Agent/Skill state, D5) and Run Again (a
// new Execution that re-resolves current configuration, D5).
//
// It implements NO RetrievalCapability minting, NO dispatch/claim/lease plumbing, and NO
// credential/object-locator field (D8/D7): those belong to later phases. An admission request
// carries an agent_id, never a Skill id collection (Agent ADR D8); the only selection authority is
// the durable AgentSkillBinding read here.

import (
	"context"
)

// AdmitExecution atomically creates a logical Execution for an Agent in a Collaboration Workspace,
// freezing the Agent's enabled Skill bindings into immutable ExecutionSkillBinding rows plus the
// first Attempt (D3). The caller identity must be an active member of the owning Workspace; the
// request carries only agent_id — the Skill set is resolved server-side from durable
// AgentSkillBinding, never from caller input. It returns the full aggregate: the execution, its
// attempts (one, eligible), and its frozen bindings in deterministic order.
func (s *Store) AdmitExecution(ctx context.Context, source, subject, display, spaceID, agentID string, input Object) (Object, error) {
	return s.transact(ctx, func(t *transaction) Object {
		uid := identity(t, source, subject, display).S("id")
		return admitExecution(t, uid, spaceID, agentID, input)
	})
}

// CreateRetryAttempt adds a new physical Attempt (ordinal increments) to an existing Execution,
// reusing the frozen ExecutionSkillBinding set without re-reading mutable AgentSkillBinding or
// current SkillRevision (D3/D5). It is a Cloud-side operation on the durable Execution; no caller
// identity is resolved.
func (s *Store) CreateRetryAttempt(ctx context.Context, executionID string) (Object, error) {
	return s.transact(ctx, func(t *transaction) Object {
		return retryAttempt(t, executionID)
	})
}

// RunAgainExecution creates a brand-new Execution for the same Agent, re-resolving the Agent's
// current enabled bindings and current SkillRevisions (D5). It re-authorizes the caller (active
// member of the owning Workspace) and never reuses the prior Execution's snapshot.
func (s *Store) RunAgainExecution(ctx context.Context, source, subject, display, executionID string, input Object) (Object, error) {
	return s.transact(ctx, func(t *transaction) Object {
		uid := identity(t, source, subject, display).S("id")
		e := t.one("SELECT workspace_id, agent_id FROM executions WHERE execution_id=$1", executionID)
		require(e != nil, 404, "not_found")
		return admitExecution(t, uid, e.S("workspaceId"), e.S("agentId"), input)
	})
}

// frozenBinding is the exact SkillRevision identity an Execution freezes at admission (D2).
type frozenBinding struct {
	skillID        string
	revisionID     string
	contentDigest  string
	sizeBytes      int64
	packageFormat  string
	packageVersion int64
}

// admitExecution runs the D3 freeze inside a caller's transaction: authorize, resolve the enabled
// Skill set from durable bindings, resolve each to its exact current revision (fail closed on any
// gap), then atomically write Execution + first Attempt + all bindings. No external effect runs
// inside this transaction.
func admitExecution(t *transaction, uid, spaceID, agentID string, input Object) Object {
	// Load the live Agent within its owning Workspace; this also applies the D11 member+ read
	// authorization (non-member / foreign-workspace / archived Agent → 404, no existence leak).
	a := agent(t, spaceID, agentID, uid, false)
	// A disabled Agent is readable but denies new Executions (Agent ADR D7).
	require(a.S("status") == "active", 404, "agent_not_available")

	// Resolve the exact frozen Skill set from the durable enabled bindings (D3 step 2). The caller
	// supplied only agent_id; disabled bindings and soft-deleted Skills are already excluded.
	bindings := enabledAgentSkillBindings(t, agentID)

	// Resolve each bound Skill to its exact current SkillRevision and freeze its content identity
	// (D3 step 3). A live Skill with no activated revision fails closed (Agent ADR D4) — never a
	// silently skipped Skill. Resolution completes before any write so a gap rolls back everything.
	frozen := make([]frozenBinding, 0, len(bindings))
	for _, b := range bindings {
		skillID := b.S("skillId")
		rev := currentSkillRevision(t, skillID)
		require(rev != nil, 409, "skill_revision_not_available")
		frozen = append(frozen, frozenBinding{
			skillID:        skillID,
			revisionID:     rev.S("id"),
			contentDigest:  rev.S("contentDigest"),
			sizeBytes:      rev.N("sizeBytes"),
			packageFormat:  rev.S("packageFormat"),
			packageVersion: rev.N("packageFormatVersion"),
		})
	}

	// The owning tenant comes from the Collaboration Workspace (the resource boundary).
	ws := t.one("SELECT tenant_id FROM collab_workspaces WHERE id=$1", spaceID)
	require(ws != nil, 404, "not_found")

	// Atomic persist: Execution + first Attempt (ordinal=1, durable eligible) + all bindings (D3
	// step 4). The Controller only claims an already-existing Attempt; it never creates the first.
	executionID := newID()
	attemptID := newID()
	if input == nil {
		input = Object{}
	}
	t.exec(`INSERT INTO executions(execution_id, tenant_id, workspace_id, agent_id, actor_user_id, input)
		VALUES($1,$2,$3,$4,$5,$6)`,
		executionID, ws.S("tenantId"), spaceID, agentID, uid, jsonText(input))
	t.exec(`INSERT INTO attempts(attempt_id, execution_id, ordinal) VALUES($1,$2,1)`, attemptID, executionID)
	for _, f := range frozen {
		t.exec(`INSERT INTO execution_skill_bindings(execution_id, skill_id, skill_revision_id, content_digest,
			size_bytes, package_format, package_format_version)
			VALUES($1,$2,$3,$4,$5,$6,$7)`,
			executionID, f.skillID, f.revisionID, f.contentDigest, f.sizeBytes, f.packageFormat, f.packageVersion)
	}
	return executionRecord(t, executionID)
}

// retryAttempt adds a new physical Attempt (ordinal increments) to an existing Execution, reusing
// the frozen bindings without re-reading AgentSkillBinding or current SkillRevision (D5). The next
// ordinal is decided under the transaction's advisory lock, so concurrent retries serialize.
func retryAttempt(t *transaction, executionID string) Object {
	require(validID(executionID), 404, "not_found")
	e := t.one("SELECT * FROM executions WHERE execution_id=$1", executionID)
	require(e != nil, 404, "not_found")
	ordinal := t.one("SELECT coalesce(max(ordinal),0)+1 AS next FROM attempts WHERE execution_id=$1", executionID).N("next")
	attemptID := newID()
	t.exec(`INSERT INTO attempts(attempt_id, execution_id, ordinal) VALUES($1,$2,$3)`, attemptID, executionID, ordinal)
	return executionRecord(t, executionID)
}

// currentSkillRevision resolves the exact current SkillRevision of a live Skill at admission time,
// or nil when the Skill has no activated revision (the caller fails closed). The join pins the
// revision through the same composite identity (id, skill_id) as skills.current_revision_id, so a
// pointer to another Skill's revision can never resolve.
func currentSkillRevision(t *transaction, skillID string) Object {
	return t.one(`SELECT sr.id, sr.skill_id, sr.content_digest, sr.size_bytes,
			sr.package_format, sr.package_format_version
		FROM skills s
		JOIN skill_revisions sr ON sr.id = s.current_revision_id AND sr.skill_id = s.id
		WHERE s.id=$1 AND s.deleted_at IS NULL`, skillID)
}

// skillBundles returns the frozen ExecutionSkillBinding set as an ordered dispatch descriptor (D4):
// each entry carries the exact skill_revision_id plus the denormalized content identity, joined to
// the Skill's canonical_name purely for stable ordering (D12). It reads only the immutable binding
// table and the Skill name; it never re-resolves a current revision or re-reads AgentSkillBinding.
func skillBundles(t *transaction, executionID string) []Object {
	return t.list(`SELECT esb.skill_id, esb.skill_revision_id, esb.content_digest, esb.size_bytes,
			esb.package_format, esb.package_format_version, s.canonical_name
		FROM execution_skill_bindings esb
		JOIN skills s ON s.id=esb.skill_id
		WHERE esb.execution_id=$1
		ORDER BY s.canonical_name, esb.skill_id`, executionID)
}

// executionRecord assembles the full Execution aggregate: the execution row, its attempts ordered by
// ordinal, and its frozen bindings in deterministic order. It is the read-back seam the tests and a
// later dispatch/query surface consume.
func executionRecord(t *transaction, executionID string) Object {
	e := t.one("SELECT * FROM executions WHERE execution_id=$1", executionID)
	if e == nil {
		return nil
	}
	return Object{
		"execution":     e,
		"attempts":      t.list("SELECT * FROM attempts WHERE execution_id=$1 ORDER BY ordinal", executionID),
		"skillBindings": skillBundles(t, executionID),
	}
}
