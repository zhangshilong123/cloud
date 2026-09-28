package core

// Controller ↔ Runtime Attempt dispatch + preparation orchestration (6B.1).
//
// This file owns the Attempt-centric slice of the claim → dispatch → result control protocol that is
// already used by clone (clone.go); Skills do not invent a new protocol, they consume the same one.
// The durable effect boundary is the attempt dispatch row itself: attempt_id + node_id +
// dispatched_epoch are persisted before any external IO, and the fenced result handler is the only
// path that advances dispatched → running / failed. Server-side preparation (Store.PrepareAttempt)
// runs outside any database transaction and stops at a LaunchSpec — it performs no process spawn and
// never writes Attempt state (the Controller owns durable state via attempt_result).

import (
	"context"
	"errors"
	"strings"

	"github.com/wanglongan587/cloud/internal/skillruntime"
	"github.com/wanglongan587/cloud/internal/skillstore"
)

// attemptCommand runs the Attempt-centric control actions. Recovery reads (attempt_get /
// attempt_pending) need no lease; the state-changing actions are reached only after Control validates
// the caller's role, lease and epoch. It mirrors cloneCommand: claim is a pure read, dispatch
// atomically establishes the fenced generation, and result records the fenced outcome idempotently.
func attemptCommand(t *transaction, r *ControlRequest) Object {
	switch r.Action {
	case "attempt_get":
		require(validID(r.Body.S("attemptId")), 404, "not_found")
		a := t.one("SELECT * FROM attempts WHERE attempt_id=$1", r.Body.S("attemptId"))
		require(a != nil, 404, "not_found")
		return Object{"attempt": a, "skillBindings": skillBundles(t, a.S("executionId"))}
	case "attempt_pending":
		node := r.Body.S("nodeId")
		if node == "" {
			return Object{"attempts": t.list("SELECT * FROM attempts WHERE state='dispatched' AND result IS NULL ORDER BY created_at,attempt_id")}
		}
		return Object{"attempts": t.list("SELECT * FROM attempts WHERE state='dispatched' AND result IS NULL AND node_id=$1 ORDER BY created_at,attempt_id", node)}
	case "attempt_claim":
		// A pure read: ownership moves only when dispatch is recorded, so a Controller that dies
		// between claim and dispatch leaves nothing to recover.
		a := t.one("SELECT * FROM attempts WHERE state='eligible' ORDER BY created_at,attempt_id LIMIT 1")
		if a == nil {
			return Object{"attempt": nil}
		}
		return Object{"attempt": a, "skillBindings": skillBundles(t, a.S("executionId"))}
	case "attempt_dispatch":
		return submitted(t, r, func() Object { return attemptDispatch(t, r) })
	case "attempt_result":
		return submitted(t, r, func() Object { return attemptResult(t, r) })
	default:
		reject(404, "not_found")
	}
	return nil
}

// isAttemptAction reports whether a control action belongs to the Attempt dispatch registry.
func isAttemptAction(action string) bool { return strings.HasPrefix(action, "attempt_") }

// attemptDispatch durably establishes the fenced dispatch generation before any runtime preparation:
// it records node_id + dispatched_epoch and advances eligible → dispatched. A repeat with the same
// (attempt, node, epoch) replays idempotently; a differing owner or generation conflicts.
func attemptDispatch(t *transaction, r *ControlRequest) Object {
	attemptID, node := r.Body.S("attemptId"), r.Body.S("nodeId")
	require(validID(attemptID) && node != "", 400, "invalid_dispatch")
	a := t.one("SELECT * FROM attempts WHERE attempt_id=$1", attemptID)
	require(a != nil, 404, "not_found")
	switch a.S("state") {
	case "eligible":
		t.exec("UPDATE attempts SET node_id=$2,dispatched_epoch=$3,state='dispatched',updated_at=now() WHERE attempt_id=$1", attemptID, node, r.Body.N("epoch"))
	case "dispatched":
		require(a.S("nodeId") == node && a.N("dispatchedEpoch") == r.Body.N("epoch"), 409, "dispatch_conflict")
	default:
		reject(409, "dispatch_conflict")
	}
	return t.one("SELECT * FROM attempts WHERE attempt_id=$1", attemptID)
}

// attemptResult commits a fenced preparation fact against a dispatched Attempt: prepared advances
// dispatched → running; preparation_failed advances dispatched → failed. The result carries only the
// outcome and a stable non-secret error code — never a runtime path, a signed URL or any signature.
// Identical facts are idempotent; differing facts or a foreign node conflict and leave the row untouched.
func attemptResult(t *transaction, r *ControlRequest) Object {
	attemptID, node := r.Body.S("attemptId"), r.Body.S("nodeId")
	require(validID(attemptID), 404, "not_found")
	a := t.one("SELECT * FROM attempts WHERE attempt_id=$1", attemptID)
	require(a != nil, 404, "not_found")
	require(a.S("state") == "dispatched" || a.S("state") == "running", 409, "attempt_not_dispatchable")
	// The result must come from the node that owns the dispatch: a foreign node can never record it.
	require(a.S("nodeId") == node, 409, "result_conflict")

	outcome := r.Body.S("outcome")
	require(outcome == "prepared" || outcome == "preparation_failed", 400, "invalid_outcome")
	result := Object{"outcome": outcome}
	if outcome == "preparation_failed" {
		code := r.Body.S("code")
		require(code != "" && len(code) <= 200, 400, "invalid_result_code")
		result["code"] = code
	}

	if previous := a.O("result"); len(previous) > 0 {
		require(jsonText(previous) == jsonText(result), 409, "result_conflict")
	} else {
		state := "running"
		if outcome == "preparation_failed" {
			state = "failed"
		}
		t.exec("UPDATE attempts SET result=$2,state=$3,updated_at=now() WHERE attempt_id=$1", attemptID, jsonText(result), state)
	}
	return t.one("SELECT * FROM attempts WHERE attempt_id=$1", attemptID)
}

// PrepareAttempt orchestrates server-side Skill runtime preparation for a dispatched Attempt entirely
// outside any database transaction: it reads the frozen execution + bindings, mints ephemeral retrieval
// capabilities, verifies every required Skill into the immutable verified cache, projects the exact
// Attempt, passes the explicit READY barrier, and opens the spawn gate onto a provider-neutral
// LaunchSpec. It performs no state advance (the fenced attempt_result owns that) and stops at LaunchSpec
// (no process spawn). It is idempotent and re-runnable: the verified cache and the published projection
// are reused on a retry, so an ambiguous success converges to the same logical LaunchSpec.
func (s *Store) PrepareAttempt(ctx context.Context, executionID, attemptID string) (*skillruntime.LaunchSpec, error) {
	// Phase 1 (database-only read): resolve the exact frozen Attempt and its immutable bindings.
	var skills []preparedSkill
	if _, err := s.transact(ctx, func(t *transaction) Object {
		require(validID(executionID) && validID(attemptID), 404, "authorization_failed")
		e := t.one("SELECT execution_id FROM executions WHERE execution_id=$1", executionID)
		require(e != nil, 404, "not_found")
		a := t.one("SELECT state FROM attempts WHERE attempt_id=$1 AND execution_id=$2", attemptID, executionID)
		require(a != nil, 404, "not_found")
		require(!attemptTerminal(a.S("state")), 409, "attempt_not_eligible")
		require(a.S("state") == "dispatched", 409, "attempt_not_dispatchable")
		skills = prepareSkills(t, executionID)
		return nil
	}); err != nil {
		return nil, err
	}

	// Every runtime seam must be wired before any external IO; an unwired seam is unavailable, never a
	// silently skipped materialization.
	if s.SkillMaterializer == nil || s.SkillProjector == nil || s.SkillReadiness == nil || s.SkillSpawnGate == nil {
		return nil, &Fault{Code: "skill_runtime_unavailable", Status: 503, Params: Object{}}
	}

	// Phase 2 (external): mint ephemeral capabilities, then verify → project → READY → open.
	revisionIDs := make([]string, 0, len(skills))
	for _, sk := range skills {
		revisionIDs = append(revisionIDs, sk.revisionID)
	}
	capabilities, err := s.MintSkillRetrievalCapabilities(ctx, executionID, attemptID, revisionIDs)
	if err != nil {
		return nil, err
	}

	projections := make([]skillruntime.ProjectionSkill, 0, len(skills))
	for _, sk := range skills {
		capability, ok := capabilityFor(capabilities, sk.revisionID)
		if !ok {
			return nil, &Fault{Code: "revision_not_bound", Status: 404, Params: Object{}}
		}
		verified, err := s.verifyVerified(ctx, executionID, attemptID, sk, capability)
		if err != nil {
			return nil, err
		}
		projections = append(projections, skillruntime.ProjectionSkill{
			SkillID:       sk.skillID,
			CanonicalName: sk.canonicalName,
			Bundle:        sk.bundle,
			Verified:      *verified,
		})
	}

	prepared, err := s.SkillProjector.Project(ctx, attemptID, projections)
	if err != nil {
		return nil, err
	}
	ready, err := s.SkillReadiness.Ready(ctx, *prepared)
	if err != nil {
		return nil, err
	}
	launch := s.SkillSpawnGate.Open(*ready)
	return &launch, nil
}

// preparedSkill is one frozen delivery descriptor resolved from immutable execution_skill_bindings,
// joined with the canonical name purely for deterministic projection ordering. It carries nothing
// mutable: no current revision, no object locator, no capability.
type preparedSkill struct {
	skillID       string
	revisionID    string
	canonicalName string
	bundle        skillruntime.FrozenSkillBundle
}

// prepareSkills maps the frozen execution_skill_bindings descriptor to the runtime's FrozenSkillBundle
// input. It reads only the immutable binding table and the Skill name (via skillBundles); it never
// re-resolves a current revision or re-reads AgentSkillBinding.
func prepareSkills(t *transaction, executionID string) []preparedSkill {
	rows := skillBundles(t, executionID)
	out := make([]preparedSkill, 0, len(rows))
	for _, o := range rows {
		out = append(out, preparedSkill{
			skillID:       o.S("skillId"),
			revisionID:    o.S("skillRevisionId"),
			canonicalName: o.S("canonicalName"),
			bundle: skillruntime.FrozenSkillBundle{
				SkillRevisionID:   o.S("skillRevisionId"),
				ContentDigestAlgo: o.S("digestAlgorithm"),
				ContentDigest:     o.S("contentDigest"),
				PackageDigestAlgo: o.S("packageDigestAlgorithm"),
				PackageDigest:     o.S("packageDigest"),
				PackageFormat:     o.S("packageFormat"),
				PackageFormatVer:  int(o.N("packageFormatVersion")),
				SizeBytes:         o.N("sizeBytes"),
			},
		})
	}
	return out
}

// capabilityFor returns the minted capability for one revoked frozen revision skill, or false when the
// mint returned nothing for it (a revision not bound to this execution).
func capabilityFor(caps []RetrievedCapability, revisionID string) (RetrievedCapability, bool) {
	for _, c := range caps {
		if c.SkillRevisionID == revisionID {
			return c, true
		}
	}
	return RetrievedCapability{}, false
}

// toRetrievalCapability converts the core mint result into the skillstore bearer the materializer
// consumes; the URL is copied for the single download and then discarded (never persisted or logged).
func toRetrievalCapability(c RetrievedCapability) skillstore.RetrievalCapability {
	return skillstore.RetrievalCapability{URL: c.URL, Method: c.Method, ExpiresAt: c.ExpiresAt}
}

// verifyVerified verifies one frozen Skill into the immutable cache, refreshing an expired/unauthorized
// capability exactly once (bounded): the refresh re-mints over the same (execution, attempt, revision)
// triple and never re-resolves mutable Skill state.
func (s *Store) verifyVerified(ctx context.Context, executionID, attemptID string, sk preparedSkill, capability RetrievedCapability) (*skillruntime.VerifiedSkillBundle, error) {
	verified, err := s.SkillMaterializer.EnsureVerified(ctx, sk.bundle, toRetrievalCapability(capability))
	if errors.Is(err, skillruntime.ErrRetrievalUnauthorized) {
		fresh, merr := s.MintSkillRetrievalCapabilities(ctx, executionID, attemptID, []string{sk.revisionID})
		if merr != nil {
			return nil, merr
		}
		re, ok := capabilityFor(fresh, sk.revisionID)
		if !ok {
			return nil, &Fault{Code: "revision_not_bound", Status: 404, Params: Object{}}
		}
		verified, err = s.SkillMaterializer.EnsureVerified(ctx, sk.bundle, toRetrievalCapability(re))
	}
	return verified, err
}
