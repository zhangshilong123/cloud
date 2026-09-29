package integration

// Controller ↔ Runtime Attempt dispatch + Node-local preparation relay integration (6B.1 / 6B.2B),
// pinned end-to-end against PostgreSQL:
//
//   - attempt_claim / attempt_dispatch / attempt_result / attempt_get / attempt_pending reuse the clone
//     claim→dispatch→result protocol over the Attempt row, touching no operations/external_effects;
//   - dispatch persists node_id + dispatched_epoch before any external IO, and only the fenced
//     attempt_result advances dispatched → running / failed within the frozen seven-state enum;
//   - the server is orchestration-only: it resolves the frozen skillBindings descriptor, mints ephemeral
//     RetrievalCapabilities, and relays the Node's fenced preparedness fact — it never downloads,
//     verifies, projects, or spawns (that chain runs in cmd/ora-skill-materialize on the Node).

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/golang-jwt/jwt/v5"

	"github.com/wanglongan587/cloud/internal/core"
)

// boundSkill is one frozen Skill of the admitted Execution under test.
type boundSkill struct {
	skillID  string
	revision string
	digest   string
	locator  string
	name     string
}

// attemptFixture provisions one admitted Execution whose frozen bindings carry valid object_locators,
// wires the fake retrieval issuer, and exposes direct helpers for the Controller-role control actions.
type attemptFixture struct {
	t         *testing.T
	store     *core.Store
	ws        string
	owner     string
	agent     string
	skills    []boundSkill
	skillID   string // skills[0], for single-skill tests and mint assertions
	revision  string
	locator   string
	execution string
	attempt   string
	issuer    *fakeIssuer
}

func newAttemptFixture(t *testing.T) *attemptFixture {
	return newAttemptFixtureSkills(t, []string{"alpha"})
}

func newAttemptFixtureSkills(t *testing.T, names []string) *attemptFixture {
	t.Helper()
	pool, _ := testSchema(t, "att_")
	store := newStoreOnSchema(t, pool)
	must(t, store.Migrate(context.Background()))
	owner, _, ws, _ := seedSkillBase(t, pool)

	execOK(t, pool, `INSERT INTO user_identities(user_id,source,subject) VALUES($1,'corp','alice')`, owner)
	execOK(t, pool, `INSERT INTO collab_workspace_members(workspace_id,user_id,role,status,created_by) VALUES($1,$2,'owner','active',$2)`, ws, owner)

	agent := insertAgent(t, pool, ws, "runner", owner)

	skills := make([]boundSkill, 0, len(names))
	for _, name := range names {
		skillID := insertSkill(t, pool, ws, name, owner)
		digest := strings.Repeat(strings.ToUpper(name[:1]), 64)
		revision := insertRevision(t, pool, skillID, digest, owner)
		locator := "skills/ora-skill-package/v1/sha256/" + strings.Repeat(strings.ToLower(name[:1]), 64)
		execOK(t, pool, `UPDATE skill_revisions SET object_locator=$2 WHERE id=$1`, revision, locator)
		execOK(t, pool, `UPDATE skills SET current_revision_id=$2,version=version+1,updated_at=now() WHERE id=$1`, skillID, revision)
		insertBinding(t, pool, agent, skillID, true)
		skills = append(skills, boundSkill{skillID: skillID, revision: revision, digest: digest, locator: locator, name: name})
	}

	out, err := store.AdmitExecution(context.Background(), "corp", "alice", "Caller", ws, agent, core.Object{"prompt": "x"})
	must(t, err)
	execution := out.O("execution").S("executionId")
	attempt := out["attempts"].([]core.Object)[0].S("attemptId")

	issuer := &fakeIssuer{}
	store.RetrievalCapabilityIssuer = issuer

	return &attemptFixture{
		t: t, store: store, ws: ws, owner: owner, agent: agent, skills: skills,
		skillID: skills[0].skillID, revision: skills[0].revision, locator: skills[0].locator,
		execution: execution, attempt: attempt, issuer: issuer,
	}
}

// controller returns the Claims for a controller-role service identity.
func controller(subject string) *core.Claims {
	return &core.Claims{Role: "controller", RegisteredClaims: jwt.RegisteredClaims{Subject: subject}}
}

// control issues one Controller-role action without a submission identity.
func (f *attemptFixture) control(subject, action string, body core.Object) (core.Object, error) {
	f.t.Helper()
	return f.store.Control(context.Background(), &core.ControlRequest{Action: action, Body: body, Service: controller(subject)})
}

// lease acquires the global lease for subject and returns the epoch it is now valid at.
func (f *attemptFixture) lease(subject string) int64 {
	f.t.Helper()
	o, err := f.control(subject, "lease_acquire", nil)
	must(f.t, err)
	return o.N("epoch")
}

// release drops subject's lease, paving a hand-over to a different holder.
func (f *attemptFixture) release(subject string, epoch int64) {
	f.t.Helper()
	_, err := f.control(subject, "lease_release", core.Object{"epoch": epoch})
	must(f.t, err)
}

// dispatch issues attempt_dispatch for the fixture attempt under the given epoch/node.
func (f *attemptFixture) dispatch(subject string, epoch int64, node string) core.Object {
	f.t.Helper()
	o, err := f.control(subject, "attempt_dispatch", core.Object{"epoch": epoch, "attemptId": f.attempt, "nodeId": node})
	must(f.t, err)
	return o
}

// TestAttemptDispatchReusesCloneModelNoSchemaChange pins D1: the three-step loop reuses the clone
// claim/dispatch/result model over the Attempt row and never touches operations/external_effects.
func TestAttemptDispatchReusesCloneModelNoSchemaChange(t *testing.T) {
	f := newAttemptFixture(t)
	epoch := f.lease("c-a")

	// Attempt_claim is a pure read: oldest eligible Attempt + frozen bindings, no state change.
	claimed, err := f.control("c-a", "attempt_claim", core.Object{"epoch": epoch})
	must(t, err)
	if got := claimed.O("attempt").S("attemptId"); got != f.attempt {
		t.Fatalf("claim must return the oldest eligible attempt %s, got %q", f.attempt, got)
	}
	if got := claimed.O("attempt").S("state"); got != "eligible" {
		t.Fatalf("claim must not advance state, got %q", got)
	}
	if bindings, ok := claimed["skillBindings"].([]core.Object); !ok || len(bindings) != 1 {
		t.Fatalf("claim must return the frozen bindings, got %v", claimed["skillBindings"])
	}

	// Dispatch persists node_id + dispatched_epoch and advances eligible → dispatched.
	dispatched := f.dispatch("c-a", epoch, "node-a")
	if dispatched.S("state") != "dispatched" || dispatched.S("nodeId") != "node-a" || dispatched.N("dispatchedEpoch") != epoch {
		t.Fatalf("dispatch must set node_id+dispatched_epoch and state=dispatched, got %v", dispatched)
	}

	// Result writes back the result and advances dispatched → running.
	resulted, err := f.control("c-a", "attempt_result", core.Object{"epoch": epoch, "attemptId": f.attempt, "nodeId": "node-a", "outcome": "prepared"})
	must(t, err)
	if resulted.S("state") != "running" || resulted.O("result").S("outcome") != "prepared" {
		t.Fatalf("result must advance to running with outcome prepared, got %v", resulted)
	}

	// The workspace effect model is never touched by the Attempt loop.
	for _, q := range []string{"SELECT count(*) FROM operations", "SELECT count(*) FROM external_effects"} {
		var n int
		must(t, f.store.Pool.QueryRow(q).Scan(&n))
		if n != 0 {
			t.Fatalf("%s → attempt dispatch must not touch operations/external_effects, got %d", q, n)
		}
	}
}

// TestAttemptDispatchPersistsIdentityLeavesNoRuntimeState pins the durable-effect boundary of 6B.2B:
// node_id + dispatched_epoch are durable before any runtime handoff, and the dispatch itself creates no
// durable runtime/materialization identity on the server (the Node helper owns that locally).
func TestAttemptDispatchPersistsIdentityLeavesNoRuntimeState(t *testing.T) {
	f := newAttemptFixture(t)
	epoch := f.lease("c-a")
	f.dispatch("c-a", epoch, "node-a")

	var nodeID string
	var dEpoch int64
	must(t, f.store.Pool.QueryRow(`SELECT node_id, dispatched_epoch FROM attempts WHERE attempt_id=$1`, f.attempt).Scan(&nodeID, &dEpoch))
	if nodeID != "node-a" || dEpoch != epoch {
		t.Fatalf("dispatch must persist identity before any runtime handoff, got node=%q epoch=%d", nodeID, dEpoch)
	}

	// Dispatch creates no durable runtime identity: exactly one attempt remains, no materialization row.
	var attempts int
	must(t, f.store.Pool.QueryRow(`SELECT count(*) FROM attempts WHERE execution_id=$1`, f.execution).Scan(&attempts))
	if attempts != 1 {
		t.Fatalf("dispatch must not create durable runtime identity, got %d attempts", attempts)
	}
}

// TestAttemptStateStaysWithinFrozenEnum pins D3: prepared → running, preparation_failed → failed, both
// inside the frozen seven-state enum which rejects any value outside it.
func TestAttemptStateStaysWithinFrozenEnum(t *testing.T) {
	f := newAttemptFixture(t)
	epoch := f.lease("c-a")

	f.dispatch("c-a", epoch, "node-a")
	_, err := f.control("c-a", "attempt_result", core.Object{"epoch": epoch, "attemptId": f.attempt, "nodeId": "node-a", "outcome": "prepared"})
	must(t, err)

	retried, err := f.store.CreateRetryAttempt(context.Background(), f.execution)
	must(t, err)
	attempt2 := retried["attempts"].([]core.Object)[1].S("attemptId")

	_, err = f.control("c-a", "attempt_dispatch", core.Object{"epoch": epoch, "attemptId": attempt2, "nodeId": "node-a"})
	must(t, err)
	_, err = f.control("c-a", "attempt_result", core.Object{"epoch": epoch, "attemptId": attempt2, "nodeId": "node-a", "outcome": "preparation_failed", "code": "package_digest_mismatch"})
	must(t, err)

	for id, want := range map[string]string{f.attempt: "running", attempt2: "failed"} {
		var state string
		must(t, f.store.Pool.QueryRow(`SELECT state FROM attempts WHERE attempt_id=$1`, id).Scan(&state))
		if state != want {
			t.Fatalf("attempt %s → want %s got %s", id, want, state)
		}
	}

	// The state must stay inside the frozen enum: an out-of-enum value is rejected by the constraint.
	execErr(t, f.store.Pool, `UPDATE attempts SET state='prepared' WHERE attempt_id=$1`, f.attempt)
}

// TestAttemptResultIdempotentAndConflict pins D2/D7: the same submission identity replays the recorded
// response, the same fact replays idempotently without a submission, and a differing fact conflicts
// leaving the original untouched.
func TestAttemptResultIdempotentAndConflict(t *testing.T) {
	f := newAttemptFixture(t)
	ctx := context.Background()
	epoch := f.lease("c-a")

	// Dispatch under a submission identity; the same request replays the recorded response.
	dispatchBody := core.Object{"epoch": epoch, "attemptId": f.attempt, "nodeId": "node-a"}
	dispatchReq := &core.ControlRequest{Action: "attempt_dispatch", SubmissionID: "s-d", Body: dispatchBody, Service: controller("c-a")}
	first, err := f.store.Control(ctx, dispatchReq)
	must(t, err)
	replay, err := f.store.Control(ctx, dispatchReq)
	must(t, err)
	if first.N("dispatchedEpoch") != replay.N("dispatchedEpoch") || first.S("state") != "dispatched" {
		t.Fatalf("dispatch replay must return the recorded response: %v vs %v", first, replay)
	}

	// The same submission identity with different content conflicts.
	altered := &core.ControlRequest{Action: "attempt_dispatch", SubmissionID: "s-d", Body: core.Object{"epoch": epoch, "attemptId": f.attempt, "nodeId": "node-b"}, Service: controller("c-a")}
	if _, err := f.store.Control(ctx, altered); core.ErrorCode(err).Code != "submission_conflict" {
		t.Fatalf("same submission with different content must conflict, got %v", core.ErrorCode(err))
	}

	// Result under a submission identity replays idempotently.
	resultReq := &core.ControlRequest{Action: "attempt_result", SubmissionID: "s-r", Body: core.Object{"epoch": epoch, "attemptId": f.attempt, "nodeId": "node-a", "outcome": "prepared"}, Service: controller("c-a")}
	r1, err := f.store.Control(ctx, resultReq)
	must(t, err)
	r2, err := f.store.Control(ctx, resultReq)
	must(t, err)
	if r1.S("state") != "running" || r2.O("result").S("outcome") != "prepared" {
		t.Fatalf("result replay must be idempotent: %v / %v", r1, r2)
	}

	// A no-submission identical result replays idempotently against the recorded fact.
	if _, err := f.control("c-a", "attempt_result", core.Object{"epoch": epoch, "attemptId": f.attempt, "nodeId": "node-a", "outcome": "prepared"}); err != nil {
		t.Fatalf("identical fact without submission must be idempotent, got %v", core.ErrorCode(err))
	}

	// A differing fact conflicts and leaves the original untouched.
	if _, err := f.control("c-a", "attempt_result", core.Object{"epoch": epoch, "attemptId": f.attempt, "nodeId": "node-a", "outcome": "preparation_failed", "code": "x"}); core.ErrorCode(err).Code != "result_conflict" {
		t.Fatalf("differing fact must conflict, got %v", core.ErrorCode(err))
	}
	var outcome string
	must(t, f.store.Pool.QueryRow(`SELECT result->>'outcome' FROM attempts WHERE attempt_id=$1`, f.attempt).Scan(&outcome))
	if outcome != "prepared" {
		t.Fatalf("conflicting fact must not overwrite the original result, got %s", outcome)
	}
}

// TestAttemptClaimReturnsFrozenBindingsNotCurrent pins the frozen-bindings authority of 6B.2B: both the
// dispatch descriptor (attempt_claim → skillBindings) and the retrieval mint consume the immutable
// execution_skill_bindings, never re-resolving a current_revision_id that advanced after admission.
func TestAttemptClaimReturnsFrozenBindingsNotCurrent(t *testing.T) {
	f := newAttemptFixture(t)
	epoch := f.lease("c-a")

	// Post-admission drift: the Skill's current revision advances to R2.
	rev2 := insertRevision(t, f.store.Pool, f.skillID, strings.Repeat("b", 64), f.owner)
	execOK(t, f.store.Pool, `UPDATE skills SET current_revision_id=$2,version=version+1,updated_at=now() WHERE id=$1`, f.skillID, rev2)

	// The descriptor still delivers the frozen R1 binding.
	claimed, err := f.control("c-a", "attempt_claim", core.Object{"epoch": epoch})
	must(t, err)
	bindings := claimed["skillBindings"].([]core.Object)
	if len(bindings) != 1 || bindings[0].S("skillRevisionId") != f.revision {
		t.Fatalf("attempt_claim must deliver only the frozen R1 binding, got %v", bindings)
	}

	// The capability mint signs the frozen R1 locator, never the drifted current revision.
	if _, err := f.store.MintSkillRetrievalCapabilities(context.Background(), f.execution, f.attempt, []string{f.revision}); err != nil {
		t.Fatalf("mint over frozen R1 must succeed, got %v", err)
	}
	if len(f.issuer.signed) != 1 || f.issuer.signed[0] != f.locator {
		t.Fatalf("mint must sign the frozen R1 locator, signed %v", f.issuer.signed)
	}
}

// TestAttemptResultPreparationFailedRecordsStableCodeNoSecret pins the prepared-result relay outcome: a
// preparation_failed fact advances the attempt to failed with a stable code, and the persisted result
// carries no runtime path or signed URL.
func TestAttemptResultPreparationFailedRecordsStableCodeNoSecret(t *testing.T) {
	f := newAttemptFixture(t)
	epoch := f.lease("c-a")
	f.dispatch("c-a", epoch, "node-a")

	resulted, err := f.control("c-a", "attempt_result", core.Object{"epoch": epoch, "attemptId": f.attempt, "nodeId": "node-a", "outcome": "preparation_failed", "code": "package_digest_mismatch"})
	must(t, err)
	if resulted.S("state") != "failed" || resulted.O("result").S("code") != "package_digest_mismatch" {
		t.Fatalf("preparation_failed must record failed + stable code, got %v", resulted)
	}

	// The persisted result carries no path or signed URL.
	var resultJSON string
	must(t, f.store.Pool.QueryRow(`SELECT result::text FROM attempts WHERE attempt_id=$1`, f.attempt).Scan(&resultJSON))
	for _, bad := range []string{"http", "x-amz", "signature", "://", "/"} {
		if strings.Contains(strings.ToLower(resultJSON), bad) {
			t.Fatalf("persisted result must carry no path/URL, got %s", resultJSON)
		}
	}
}

// TestAttemptDispatchTerminalAttemptRejected pins the loop-level fail-closed boundary: a terminal
// Attempt never mints a retrieval capability and never takes a prepared result.
func TestAttemptDispatchTerminalAttemptRejected(t *testing.T) {
	f := newAttemptFixture(t)
	epoch := f.lease("c-a")
	execOK(t, f.store.Pool, `UPDATE attempts SET state='succeeded', updated_at=now() WHERE attempt_id=$1`, f.attempt)

	if _, err := f.store.MintSkillRetrievalCapabilities(context.Background(), f.execution, f.attempt, []string{f.revision}); core.ErrorCode(err).Code != "attempt_not_eligible" || core.ErrorCode(err).Status != 409 {
		t.Fatalf("terminal attempt must not mint: got %d %s", core.ErrorCode(err).Status, core.ErrorCode(err).Code)
	}
	if len(f.issuer.signed) != 0 {
		t.Fatalf("terminal attempt must not mint, signed %v", f.issuer.signed)
	}

	if _, err := f.control("c-a", "attempt_result", core.Object{"epoch": epoch, "attemptId": f.attempt, "nodeId": "node-a", "outcome": "prepared"}); core.ErrorCode(err).Code != "attempt_not_dispatchable" {
		t.Fatalf("terminal attempt must not take a result, got %v", core.ErrorCode(err))
	}
}

// TestAttemptDispatchLeaseFenced pins D2/D7: a stale epoch is fenced, dispatched_epoch attribution is
// durable, and a new valid owner converges the same Attempt under the same node.
func TestAttemptDispatchLeaseFenced(t *testing.T) {
	f := newAttemptFixture(t)
	epochA := f.lease("c-a")
	f.dispatch("c-a", epochA, "node-a")

	// Hand the lease over: c-a releases, c-b acquires and the epoch bumps.
	f.release("c-a", epochA)
	epochB := f.lease("c-b")

	if _, err := f.control("c-a", "attempt_result", core.Object{"epoch": epochA, "attemptId": f.attempt, "nodeId": "node-a", "outcome": "prepared"}); core.ErrorCode(err).Code != "stale_controller" || core.ErrorCode(err).Status != 409 {
		t.Fatalf("stale epoch must be fenced: got %d %s", core.ErrorCode(err).Status, core.ErrorCode(err).Code)
	}

	var dEpoch int64
	must(t, f.store.Pool.QueryRow(`SELECT dispatched_epoch FROM attempts WHERE attempt_id=$1`, f.attempt).Scan(&dEpoch))
	if dEpoch != epochA {
		t.Fatalf("dispatched_epoch must remain attributed to the dispatch epoch, got %d", dEpoch)
	}

	// The new valid owner (epoch B) converges the same Attempt under the same node.
	if _, err := f.control("c-b", "attempt_result", core.Object{"epoch": epochB, "attemptId": f.attempt, "nodeId": "node-a", "outcome": "prepared"}); err != nil {
		t.Fatalf("new owner must converge the attempt, got %v", core.ErrorCode(err))
	}
	var state string
	must(t, f.store.Pool.QueryRow(`SELECT state FROM attempts WHERE attempt_id=$1`, f.attempt).Scan(&state))
	if state != "running" {
		t.Fatalf("new owner must converge the attempt to running, got %s", state)
	}
}

// TestServerNoLongerMaterializes pins the 6B.2B ownership relocation at the source level: the server
// exposes no PrepareAttempt seam, no materialization/projection/readiness/spawn-gate field, and no
// skillruntime import — the materialization chain lives only in cmd/ora-skill-materialize.
func TestServerNoLongerMaterializes(t *testing.T) {
	for _, rel := range []string{"attempt.go", "store.go"} {
		src, err := os.ReadFile(filepath.Join("..", "internal", "core", rel))
		must(t, err)
		for _, banned := range []string{"PrepareAttempt", "SkillMaterializer", "SkillProjector", "SkillReadiness", "SkillSpawnGate", "skillruntime"} {
			if bytes.Contains(src, []byte(banned)) {
				t.Fatalf("internal/core/%s must no longer reference the server-side materialization seam (found %q)", rel, banned)
			}
		}
	}

	// The server-side dispatch path imports and spawns no process.
	src, err := os.ReadFile(filepath.Join("..", "internal", "core", "attempt.go"))
	must(t, err)
	for _, banned := range []string{"os/exec", "exec.Command"} {
		if bytes.Contains(src, []byte(banned)) {
			t.Fatalf("core dispatch path must not spawn a process (found %q)", banned)
		}
	}
}
