package integration

// Controller ↔ Runtime Attempt dispatch + preparation integration (6B.1 of
// specs/decisions/cloud/skills/20260928-controller-attempt-dispatch-preparation.md), pinned end-to-end
// against PostgreSQL with the preparation seams doubled:
//
//   - attempt_claim / attempt_dispatch / attempt_result / attempt_get / attempt_pending reuse the clone
//     claim→dispatch→result protocol over the Attempt row, touching no operations/external_effects;
//   - dispatch persists node_id + dispatched_epoch before any external IO, and only the fenced
//     attempt_result advances dispatched → running / failed within the frozen seven-state enum;
//   - Store.PrepareAttempt orchestrates mint → EnsureVerified → Project → Ready → Open entirely outside
//     any database transaction, is idempotent (verified-cache reuse), stops at a LaunchSpec, consumes
//     only frozen bindings, and fails closed with no partial projection on an integrity failure;
//   - the loop is lease-fenced and submission-idempotent, mints fresh ephemeral capabilities that are
//     never persisted, and never materializes a signed URL, a runtime path, or a process spawn.

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/golang-jwt/jwt/v5"

	"github.com/wanglongan587/cloud/internal/core"
	"github.com/wanglongan587/cloud/internal/skillruntime"
	"github.com/wanglongan587/cloud/internal/skillstore"
)

// fakeMaterializer is a preparation-dependency test double for SkillMaterializer (Seam A). It models
// the immutable verified cache keyed by content digest: a hit returns the cached entry, a miss
// performs one "download" and stores the entry. It can inject a one-shot ErrRetrievalUnauthorized (to
// drive the bounded capability refresh) and a per-digest or global permanent failure.
type fakeMaterializer struct {
	cache      map[string]*skillruntime.VerifiedSkillBundle
	downloads  int
	seen       []skillstore.RetrievalCapability
	bundles    []skillruntime.FrozenSkillBundle
	unauthOnce bool
	failAll    error
	failFor    map[string]error // content digest → permanent materialization error
}

func (m *fakeMaterializer) EnsureVerified(ctx context.Context, b skillruntime.FrozenSkillBundle, capability skillstore.RetrievalCapability) (*skillruntime.VerifiedSkillBundle, error) {
	m.seen = append(m.seen, capability)
	m.bundles = append(m.bundles, b)
	if v, ok := m.cache[b.ContentDigest]; ok {
		return v, nil
	}
	if e, ok := m.failFor[b.ContentDigest]; ok {
		return nil, e
	}
	if m.failAll != nil {
		return nil, m.failAll
	}
	if m.unauthOnce {
		m.unauthOnce = false
		return nil, skillruntime.ErrRetrievalUnauthorized
	}
	m.downloads++
	v := &skillruntime.VerifiedSkillBundle{ContentDigest: b.ContentDigest, DigestAlgo: b.ContentDigestAlgo, CacheDir: "cache/" + b.ContentDigest}
	m.cache[b.ContentDigest] = v
	return v, nil
}

// fakeProjector doubles both SkillProjector (Project) and SkillReadiness (Ready), mirroring the
// production arrangement where one *skillruntime.Projector serves both seams. It records how many
// times each barrier ran and the skills it was asked to project.
type fakeProjector struct {
	projects   int
	readyCalls int
	lastSkills []skillruntime.ProjectionSkill
	projectErr error
}

func (p *fakeProjector) Project(ctx context.Context, attemptID string, skills []skillruntime.ProjectionSkill) (*skillruntime.PreparedAttempt, error) {
	p.projects++
	if p.projectErr != nil {
		return nil, p.projectErr
	}
	p.lastSkills = append([]skillruntime.ProjectionSkill(nil), skills...)
	return &skillruntime.PreparedAttempt{AttemptID: attemptID, Root: "attempts/" + attemptID}, nil
}

func (p *fakeProjector) Ready(ctx context.Context, a skillruntime.PreparedAttempt) (*skillruntime.ReadyAttempt, error) {
	p.readyCalls++
	provisions := make([]skillruntime.SkillProjection, 0, len(p.lastSkills))
	for _, sk := range p.lastSkills {
		provisions = append(provisions, skillruntime.SkillProjection{
			SkillID:           sk.SkillID,
			SkillRevisionID:   sk.Bundle.SkillRevisionID,
			RuntimeName:       sk.Bundle.SkillRevisionID,
			ContentDigest:     sk.Bundle.ContentDigest,
			ContentDigestAlgo: sk.Bundle.ContentDigestAlgo,
			Dir:               a.Root + "/" + sk.Bundle.SkillRevisionID,
		})
	}
	return &skillruntime.ReadyAttempt{AttemptID: a.AttemptID, Root: a.Root, Skills: provisions}, nil
}

// fakeSpawnGate doubles the SkillSpawnGate seam (Seam B): it records how many times it opened and
// assembles the provider-neutral LaunchSpec from the ReadyAttempt.
type fakeSpawnGate struct{ opens int }

func (g *fakeSpawnGate) Open(ready skillruntime.ReadyAttempt) skillruntime.LaunchSpec {
	g.opens++
	return skillruntime.LaunchSpec{AttemptID: ready.AttemptID, Root: ready.Root, Provisions: ready.Skills}
}

// boundSkill is one frozen Skill of the admitted Execution under test.
type boundSkill struct {
	skillID  string
	revision string
	digest   string
	locator  string
	name     string
}

// attemptFixture provisions one admitted Execution whose frozen bindings carry valid object_locators,
// wires the fake retrieval issuer and all four preparation seams, and exposes direct helpers for the
// Controller-role control actions.
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
	mat       *fakeMaterializer
	proj      *fakeProjector
	gate      *fakeSpawnGate
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

	mat := &fakeMaterializer{cache: map[string]*skillruntime.VerifiedSkillBundle{}, failFor: map[string]error{}}
	proj := &fakeProjector{}
	gate := &fakeSpawnGate{}
	store.SkillMaterializer = mat
	store.SkillProjector = proj
	store.SkillReadiness = proj
	store.SkillSpawnGate = gate

	return &attemptFixture{
		t: t, store: store, ws: ws, owner: owner, agent: agent, skills: skills,
		skillID: skills[0].skillID, revision: skills[0].revision, locator: skills[0].locator,
		execution: execution, attempt: attempt, issuer: issuer, mat: mat, proj: proj, gate: gate,
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

// TestAttemptDispatchPersistsIdentityBeforePreparation pins D1 invariant 2: node_id + dispatched_epoch
// are durable before any external IO, and the LaunchSpec is keyed by attempt_id with no new durable
// identity.
func TestAttemptDispatchPersistsIdentityBeforePreparation(t *testing.T) {
	f := newAttemptFixture(t)
	epoch := f.lease("c-a")
	f.dispatch("c-a", epoch, "node-a")

	var nodeID string
	var dEpoch int64
	must(t, f.store.Pool.QueryRow(`SELECT node_id, dispatched_epoch FROM attempts WHERE attempt_id=$1`, f.attempt).Scan(&nodeID, &dEpoch))
	if nodeID != "node-a" || dEpoch != epoch {
		t.Fatalf("dispatch must persist identity before preparation, got node=%q epoch=%d", nodeID, dEpoch)
	}

	launch, err := f.store.PrepareAttempt(context.Background(), f.execution, f.attempt)
	must(t, err)
	if launch.AttemptID != f.attempt {
		t.Fatalf("LaunchSpec must be keyed by attempt_id, got %q", launch.AttemptID)
	}

	// Exactly one attempt remains; preparation created no durable identity.
	var attempts int
	must(t, f.store.Pool.QueryRow(`SELECT count(*) FROM attempts WHERE execution_id=$1`, f.execution).Scan(&attempts))
	if attempts != 1 {
		t.Fatalf("preparation must not create durable identity, got %d attempts", attempts)
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

// TestPrepareAttemptIdempotentStopsAtLaunchSpec pins D4/D7/D8: preparation is a pure, idempotent,
// transaction-free seam that never advances Attempt state, reuses the verified cache, and stops at a
// LaunchSpec without spawning anything.
func TestPrepareAttemptIdempotentStopsAtLaunchSpec(t *testing.T) {
	f := newAttemptFixture(t)
	epoch := f.lease("c-a")
	f.dispatch("c-a", epoch, "node-a")

	first, err := f.store.PrepareAttempt(context.Background(), f.execution, f.attempt)
	must(t, err)
	if first == nil || first.AttemptID != f.attempt || first.Root == "" || len(first.Provisions) != 1 {
		t.Fatalf("PrepareAttempt must return a LaunchSpec keyed by attempt_id with provisions, got %v", first)
	}

	// PrepareAttempt never advances Attempt state itself (the fenced result owns that).
	var state string
	must(t, f.store.Pool.QueryRow(`SELECT state FROM attempts WHERE attempt_id=$1`, f.attempt).Scan(&state))
	if state != "dispatched" {
		t.Fatalf("PrepareAttempt must not advance state (still dispatched), got %s", state)
	}

	second, err := f.store.PrepareAttempt(context.Background(), f.execution, f.attempt)
	must(t, err)
	if second.AttemptID != first.AttemptID || second.Root != first.Root || len(second.Provisions) != len(first.Provisions) {
		t.Fatalf("PrepareAttempt must be idempotent: %v vs %v", first, second)
	}

	// Each prepare runs the pipeline; the materializer serves the second call from its verified cache.
	if f.proj.projects != 2 || f.proj.readyCalls != 2 || f.gate.opens != 2 {
		t.Fatalf("seams must run per prepare: projects=%d ready=%d opens=%d", f.proj.projects, f.proj.readyCalls, f.gate.opens)
	}
	if f.mat.downloads != 1 {
		t.Fatalf("second prepare must hit the verified cache (1 download), got %d", f.mat.downloads)
	}
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

// TestPrepareAttemptMintsFreshAndPersistsNothing pins D4 invariant 4/D6: every preparation mints a
// fresh capability over the same frozen revision and persists nothing durable, no credential material
// appears in any row.
func TestPrepareAttemptMintsFreshAndPersistsNothing(t *testing.T) {
	f := newAttemptFixture(t)
	epoch := f.lease("c-a")
	f.dispatch("c-a", epoch, "node-a")

	for i := 0; i < 2; i++ {
		_, err := f.store.PrepareAttempt(context.Background(), f.execution, f.attempt)
		must(t, err)
	}

	if len(f.issuer.signed) != 2 || f.issuer.signed[0] != f.locator || f.issuer.signed[1] != f.locator {
		t.Fatalf("each prepare must mint fresh over the same frozen locator, signed %v", f.issuer.signed)
	}

	// PrepareAttempt persists nothing: the attempt result is still unset.
	var result sql.NullString
	must(t, f.store.Pool.QueryRow(`SELECT result::text FROM attempts WHERE attempt_id=$1`, f.attempt).Scan(&result))
	if result.Valid {
		t.Fatalf("PrepareAttempt must persist nothing (attempts.result still NULL), got %s", result.String)
	}

	// No signed-URL material leaks into any durable row.
	var leaked int
	must(t, f.store.Pool.QueryRow(`SELECT count(*) FROM skill_revisions WHERE object_locator LIKE '%X-Amz%' OR object_locator LIKE '%sig%'`).Scan(&leaked))
	if leaked != 0 {
		t.Fatalf("bearer credential material leaked into durable state: %d rows", leaked)
	}
}

// TestPrepareAttemptUsesFrozenBindingsNotCurrent pins D5: preparation consumes only the frozen
// execution_skill_bindings, never re-resolving a current_revision_id that changed after admission.
func TestPrepareAttemptUsesFrozenBindingsNotCurrent(t *testing.T) {
	f := newAttemptFixture(t)
	epoch := f.lease("c-a")
	f.dispatch("c-a", epoch, "node-a")

	// Post-admission drift: the Skill's current revision advances to R2.
	rev2 := insertRevision(t, f.store.Pool, f.skillID, strings.Repeat("b", 64), f.owner)
	execOK(t, f.store.Pool, `UPDATE skills SET current_revision_id=$2,version=version+1,updated_at=now() WHERE id=$1`, f.skillID, rev2)

	_, err := f.store.PrepareAttempt(context.Background(), f.execution, f.attempt)
	must(t, err)

	if len(f.mat.bundles) != 1 || f.mat.bundles[0].SkillRevisionID != f.revision {
		t.Fatalf("PrepareAttempt must consume the frozen revision R1, got %v", f.mat.bundles)
	}
}

// TestPrepareAttemptIntegrityFailureFailsClosedNoPartial pins D7: a permanent integrity failure
// returns the typed materialization error, projects nothing (no partial projection), and records a
// preparation_failed result with a stable code and no path or URL.
func TestPrepareAttemptIntegrityFailureFailsClosedNoPartial(t *testing.T) {
	f := newAttemptFixture(t)
	f.mat.failAll = skillruntime.ErrPackageDigestMismatch
	epoch := f.lease("c-a")
	f.dispatch("c-a", epoch, "node-a")

	_, err := f.store.PrepareAttempt(context.Background(), f.execution, f.attempt)
	if !errors.Is(err, skillruntime.ErrPackageDigestMismatch) {
		t.Fatalf("integrity failure must surface the typed materialization error, got %v", err)
	}
	if f.proj.projects != 0 {
		t.Fatalf("integrity failure must project nothing (no partial projection), got %d", f.proj.projects)
	}

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

// TestPrepareAttemptMultiSkillAllOrNothing pins the V1 all-or-nothing rule: with two required Skills,
// a failure in one verifies the other in the cache but projects nothing, so no partial projection ever
// becomes visible.
func TestPrepareAttemptMultiSkillAllOrNothing(t *testing.T) {
	f := newAttemptFixtureSkills(t, []string{"alpha", "beta"})
	// beta's content digest: strings.Repeat("B", 64), from the fixture builder.
	f.mat.failFor[strings.Repeat("B", 64)] = skillruntime.ErrContentDigestMismatch
	epoch := f.lease("c-a")
	f.dispatch("c-a", epoch, "node-a")

	_, err := f.store.PrepareAttempt(context.Background(), f.execution, f.attempt)
	if !errors.Is(err, skillruntime.ErrContentDigestMismatch) {
		t.Fatalf("multi-skill failure must surface the typed error, got %v", err)
	}
	// alpha was verified (its download happened) but no projection was published.
	if f.mat.downloads != 1 {
		t.Fatalf("alpha should download before beta fails, got %d downloads", f.mat.downloads)
	}
	if f.proj.projects != 0 {
		t.Fatalf("a mid-skill failure must project nothing (all-or-nothing), got %d", f.proj.projects)
	}
}

// TestAttemptDispatchTerminalAttemptRejected pins D7/D14: a terminal Attempt never mints, never
// prepares, and never advances.
func TestAttemptDispatchTerminalAttemptRejected(t *testing.T) {
	f := newAttemptFixture(t)
	epoch := f.lease("c-a")
	execOK(t, f.store.Pool, `UPDATE attempts SET state='succeeded', updated_at=now() WHERE attempt_id=$1`, f.attempt)

	_, err := f.store.PrepareAttempt(context.Background(), f.execution, f.attempt)
	if core.ErrorCode(err).Code != "attempt_not_eligible" || core.ErrorCode(err).Status != 409 {
		t.Fatalf("terminal attempt must not prepare: got %d %s", core.ErrorCode(err).Status, core.ErrorCode(err).Code)
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

// TestAttemptDispatchNoSecretPathOrSpawn pins D6/D8: the persisted result is exactly the minimal fact,
// no signed URL/runtime path materializes anywhere in the loop, and the core prepare path spawns no
// process.
func TestAttemptDispatchNoSecretPathOrSpawn(t *testing.T) {
	f := newAttemptFixture(t)
	epoch := f.lease("c-a")
	f.dispatch("c-a", epoch, "node-a")

	_, err := f.store.PrepareAttempt(context.Background(), f.execution, f.attempt)
	must(t, err)
	_, err = f.control("c-a", "attempt_result", core.Object{"epoch": epoch, "attemptId": f.attempt, "nodeId": "node-a", "outcome": "prepared"})
	must(t, err)

	// The persisted result is exactly {outcome: prepared}: no extra secret/path field.
	var resultJSON string
	must(t, f.store.Pool.QueryRow(`SELECT result::text FROM attempts WHERE attempt_id=$1`, f.attempt).Scan(&resultJSON))
	var result map[string]any
	must(t, json.Unmarshal([]byte(resultJSON), &result))
	if len(result) != 1 || result["outcome"] != "prepared" {
		t.Fatalf("persisted result must be exactly {outcome:prepared}, got %v", result)
	}
	for _, bad := range []string{"http", "x-amz", "signature", "://", "/"} {
		if strings.Contains(strings.ToLower(resultJSON), bad) {
			t.Fatalf("persisted result must carry no secret/path, got %s", resultJSON)
		}
	}

	// Static: the core prepare path imports and spawns no process.
	src, err := os.ReadFile(filepath.Join("..", "internal", "core", "attempt.go"))
	must(t, err)
	for _, banned := range []string{"os/exec", "exec.Command", "exec.CommandContext"} {
		if bytes.Contains(src, []byte(banned)) {
			t.Fatalf("core prepare path must not spawn a process (found %q)", banned)
		}
	}
}

// TestPrepareAttemptCapabilityRefreshBounded pins D4/D7's bounded refresh: an expired/unauthorized
// capability re-mints exactly once over the same frozen (execution, attempt, revision, digest),
// hands the materializer a fresh credential, and converges without touching current Skill state.
func TestPrepareAttemptCapabilityRefreshBounded(t *testing.T) {
	f := newAttemptFixture(t)
	f.mat.unauthOnce = true
	epoch := f.lease("c-a")
	f.dispatch("c-a", epoch, "node-a")

	launch, err := f.store.PrepareAttempt(context.Background(), f.execution, f.attempt)
	must(t, err)
	if launch == nil || launch.AttemptID != f.attempt {
		t.Fatalf("refresh must still converge to a LaunchSpec, got %v", launch)
	}

	// One initial mint + one bounded refresh mint, both over the same frozen locator.
	if len(f.issuer.signed) != 2 || f.issuer.signed[0] != f.locator || f.issuer.signed[1] != f.locator {
		t.Fatalf("refresh must mint twice over the same frozen locator, signed %v", f.issuer.signed)
	}
	// The materializer saw the unauthorized capability then one fresh retry.
	if len(f.mat.seen) != 2 || f.mat.seen[0].URL == f.mat.seen[1].URL {
		t.Fatalf("refresh must retry once with a fresh capability: seen %d caps", len(f.mat.seen))
	}
	// Identity is preserved: the same frozen revision and digest, never a current-Skill re-lookup.
	if len(f.mat.bundles) != 2 || f.mat.bundles[0].SkillRevisionID != f.revision || f.mat.bundles[1].SkillRevisionID != f.revision || f.mat.bundles[0].ContentDigest != f.mat.bundles[1].ContentDigest {
		t.Fatalf("refresh must preserve the frozen identity, got %v", f.mat.bundles)
	}
}
