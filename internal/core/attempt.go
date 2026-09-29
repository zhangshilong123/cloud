package core

// Controller ↔ Runtime Attempt dispatch + prepared-result relay orchestration (6B.1 / 6B.2B).
//
// This file owns the Attempt-centric slice of the claim → dispatch → result control protocol that is
// already used by clone (clone.go); Skills do not invent a new protocol, they consume the same one.
// The durable effect boundary is the attempt dispatch row itself: attempt_id + node_id +
// dispatched_epoch are persisted before any external IO, and the fenced result handler is the only
// path that advances dispatched → running / failed.
//
// As of 6B.2B the server is orchestration-only for Skill materialization: it resolves frozen bindings
// (skillBindings), mints ephemeral retrieval capabilities (MintSkillRetrievalCapabilities), and relays
// the Node's fenced preparedness fact via attempt_result — it no longer downloads, verifies, projects,
// or spawns anything. The materialization chain (EnsureVerified → Project → Ready → SpawnGate.Open)
// executes on the assigned Node via cmd/ora-skill-materialize
// (see specs/decisions/cloud/skills/20260929-node-runtime-materialization-placement.md).

import (
	"strings"
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
