package integration

// Phase 6B.2S — Skills Runtime Handoff Mock Closure.
//
// This file closes the Skills-side runtime handoff contract up to the runtime boundary with a test-only
// mock consumer, entirely in Go (no Rust, no OpenCode, no Docker image, no real process spawn). The
// production handoff seam already exists and is reused unchanged:
//
//	Server (orchestration-only): attempt_claim/attempt_get → frozen skillBindings descriptor,
//	                             MintSkillRetrievalCapabilities, fenced attempt_dispatch/attempt_result.
//	Node helper / runtime:       EnsureVerified → Project → Ready → SpawnGate.Open → LaunchSpec.
//
// The tests below run one full integration path over real PostgreSQL: real DB admission → real
// RetrievalCapability → real canonical-package download + verify → real projection + READY → LaunchSpec,
// then hand that LaunchSpec plus the fenced dispatch identity to a mockRuntime consumer and assert the
// frozen-snapshot invariants (exact frozen == delivered, no mutable re-resolution, retry/run-again,
// deterministic ordering, all-or-nothing, secret non-leak, epoch fencing, no script execution).

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/wanglongan587/cloud/internal/core"
	"github.com/wanglongan587/cloud/internal/skillpkg"
	"github.com/wanglongan587/cloud/internal/skillruntime"
	"github.com/wanglongan587/cloud/internal/skillstore"
)

// mockRuntime is the canonical 6B.2S test-only fake: it consumes the runtime handoff boundary exactly as a
// future production Node runtime will — the fenced dispatch identity (attempt_id, node_id,
// dispatched_epoch) plus the published LaunchSpec (local root + ordered provisions) — and records them for
// the frozen-snapshot invariant assertions. It spawns nothing, executes no content, and models no durable
// runtime table (§28).
type mockRuntime struct {
	attemptID  string
	nodeID     string
	epoch      int64
	root       string
	provisions []skillruntime.SkillProjection
}

func (m *mockRuntime) accept(attemptID, nodeID string, epoch int64, spec skillruntime.LaunchSpec) {
	m.attemptID = attemptID
	m.nodeID = nodeID
	m.epoch = epoch
	m.root = spec.Root
	m.provisions = append([]skillruntime.SkillProjection(nil), spec.Provisions...)
}

// servingIssuer is a RetrievalCapabilityIssuer whose URL actually serves the exact canonical package bytes
// for each locator (so the real materializer downloads and verifies over HTTP), while stamping a
// signature-like query so the bearer-credential non-leak redline is realistic.
type servingIssuer struct {
	base   string
	byLoc  map[string][]byte
	signed []string
	seq    int
}

func (f *servingIssuer) Issue(_ context.Context, locator skillstore.Locator, ttl time.Duration) (skillstore.RetrievalCapability, error) {
	f.seq++
	f.signed = append(f.signed, locator.String())
	return skillstore.RetrievalCapability{
		URL:       f.base + "/by/" + url.PathEscape(locator.String()) + "?X-Amz-Signature=cryptosig" + strconv.Itoa(f.seq),
		Method:    "GET",
		ExpiresAt: time.Now().Add(ttl),
	}, nil
}

// realSkillPackage builds a real canonical ora-skill-package whose SKILL.md embeds seed, so distinct seeds
// yield distinct content_digest / package_digest / object_locator.
func realSkillPackage(t *testing.T, seed string) (pkgBytes []byte, contentDigest, packageDigest string, sizeBytes int64, fileCount int) {
	t.Helper()
	files := []skillpkg.SourceFile{
		{Path: "SKILL.md", Data: []byte("# Skill " + seed + "\n\nReal canonical skill.\n")},
		{Path: "scripts/run.sh", Data: []byte("#!/bin/sh\necho " + seed + "\n")},
	}
	bundle, err := skillpkg.Build(files, skillpkg.DefaultLimits())
	must(t, err)
	var total int64
	for _, e := range bundle.Entries {
		total += int64(e.Size)
	}
	return bundle.Package, bundle.TreeDigestHex(), skillstore.PackageDigestHex(bundle.Package), total, len(bundle.Entries)
}

// realRevision is one SkillRevision whose delivery identity exactly matches a real package.
type realRevision struct {
	revisionID    string
	contentDigest string
	packageDigest string
	sizeBytes     int64
	locator       string
	pkgBytes      []byte
}

// insertRealRevision inserts a SkillRevision whose content_digest, package_digest, size_bytes,
// package_format/version and object_locator all match a real canonical package, so the frozen snapshot
// drives a real byte-verification materialization (unlike the shared insertRevision, which fakes digests).
func insertRealRevision(t *testing.T, pool *sql.DB, skillID, owner, seed string) realRevision {
	t.Helper()
	pkgBytes, contentDigest, packageDigest, sizeBytes, fileCount := realSkillPackage(t, seed)
	rev := realRevision{
		revisionID:    uuid.NewString(),
		contentDigest: contentDigest,
		packageDigest: packageDigest,
		sizeBytes:     sizeBytes,
		locator:       "skills/ora-skill-package/v1/sha256/" + packageDigest,
		pkgBytes:      pkgBytes,
	}
	execOK(t, pool, `INSERT INTO skill_revisions(id,skill_id,digest_algorithm,content_digest,package_digest,package_digest_algorithm,size_bytes,file_count,package_format,package_format_version,object_locator,created_by)
		VALUES($1,$2,'sha256',$3,$4,'sha256',$5,$6,'ora-skill-package',1,$7,$8)`,
		rev.revisionID, skillID, contentDigest, packageDigest, sizeBytes, fileCount, rev.locator, owner)
	return rev
}

// handoffPackage is one frozen Skill of the admitted Execution under test.
type handoffPackage struct {
	skillID       string
	revisionID    string
	canonicalName string
	contentDigest string
	locator       string
}

// handoffFixture provisions one admitted Execution whose frozen bindings carry real package identities,
// wires a working retrieval issuer, and exposes the materialize→LaunchSpec→mock-consumer handoff driver.
type handoffFixture struct {
	t           *testing.T
	store       *core.Store
	ws, owner   string
	agent       string
	skills      []handoffPackage
	execution   string
	attempt     string
	issuer      *servingIssuer
	srv         *httptest.Server
	cacheRoot   string
	attemptRoot string
}

func newHandoffFixture(t *testing.T, names ...string) *handoffFixture {
	t.Helper()
	pool, _ := testSchema(t, "rth_")
	store := newStoreOnSchema(t, pool)
	must(t, store.Migrate(context.Background()))
	owner, _, ws, _ := seedSkillBase(t, pool)

	execOK(t, pool, `INSERT INTO user_identities(user_id,source,subject) VALUES($1,'corp','alice')`, owner)
	execOK(t, pool, `INSERT INTO collab_workspace_members(workspace_id,user_id,role,status,created_by) VALUES($1,$2,'owner','active',$2)`, ws, owner)

	agent := insertAgent(t, pool, ws, "runner", owner)

	issuer := &servingIssuer{byLoc: map[string][]byte{}}
	skills := make([]handoffPackage, 0, len(names))
	for _, name := range names {
		skillID := insertSkill(t, pool, ws, name, owner)
		rev := insertRealRevision(t, pool, skillID, owner, name)
		execOK(t, pool, `UPDATE skills SET current_revision_id=$2,version=version+1,updated_at=now() WHERE id=$1`, skillID, rev.revisionID)
		insertBinding(t, pool, agent, skillID, true)
		issuer.byLoc[rev.locator] = rev.pkgBytes
		skills = append(skills, handoffPackage{
			skillID: skillID, revisionID: rev.revisionID, canonicalName: name,
			contentDigest: rev.contentDigest, locator: rev.locator,
		})
	}

	out, err := store.AdmitExecution(context.Background(), "corp", "alice", "Caller", ws, agent, core.Object{"prompt": "x"})
	must(t, err)
	execution := out.O("execution").S("executionId")
	attempt := out["attempts"].([]core.Object)[0].S("attemptId")

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		key, err := url.PathUnescape(strings.TrimPrefix(r.URL.Path, "/by/"))
		if err != nil {
			http.NotFound(w, r)
			return
		}
		b, ok := issuer.byLoc[key]
		if !ok {
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write(b)
	}))
	t.Cleanup(srv.Close)
	issuer.base = srv.URL

	store.RetrievalCapabilityIssuer = issuer
	store.RetrievalCapabilityTTL = 300 * time.Second

	return &handoffFixture{
		t: t, store: store, ws: ws, owner: owner, agent: agent, skills: skills,
		execution: execution, attempt: attempt, issuer: issuer, srv: srv,
		cacheRoot: t.TempDir(), attemptRoot: t.TempDir(),
	}
}

func (f *handoffFixture) control(subject, action string, body core.Object) (core.Object, error) {
	return f.store.Control(context.Background(), &core.ControlRequest{Action: action, Body: body, Service: controller(subject)})
}

func (f *handoffFixture) lease(subject string) int64 {
	f.t.Helper()
	o, err := f.control(subject, "lease_acquire", nil)
	must(f.t, err)
	return o.N("epoch")
}

func (f *handoffFixture) dispatch(subject string, epoch int64, nodeID string) core.Object {
	f.t.Helper()
	o, err := f.control(subject, "attempt_dispatch", core.Object{"epoch": epoch, "attemptId": f.attempt, "nodeId": nodeID})
	must(f.t, err)
	return o
}

// materialize replays the production materialization chain (EnsureVerified → Project → Ready →
// SpawnGate.Open) exactly as cmd/ora-skill-materialize does, over the frozen attempt descriptor, then hands
// the LaunchSpec plus the fenced dispatch identity to the mock runtime consumer. It returns an error before
// any handoff when any required Skill fails verification (all-or-nothing).
func (f *handoffFixture) materialize(nodeID string, epoch int64) (*mockRuntime, error) {
	f.t.Helper()
	claimed, err := f.control("c-a", "attempt_get", core.Object{"attemptId": f.attempt})
	must(f.t, err)
	bindings := claimed["skillBindings"].([]core.Object)

	revIDs := make([]string, 0, len(bindings))
	for _, b := range bindings {
		revIDs = append(revIDs, b.S("skillRevisionId"))
	}
	caps, err := f.store.MintSkillRetrievalCapabilities(context.Background(), f.execution, f.attempt, revIDs)
	if err != nil {
		return nil, err
	}
	byRev := make(map[string]skillstore.RetrievalCapability, len(caps))
	for _, c := range caps {
		byRev[c.SkillRevisionID] = skillstore.RetrievalCapability{URL: c.URL, Method: c.Method, ExpiresAt: c.ExpiresAt}
	}

	materializer, err := skillruntime.New(skillruntime.Config{CacheRoot: f.cacheRoot})
	must(f.t, err)
	projector, err := skillruntime.NewProjector(f.attemptRoot)
	must(f.t, err)
	gate := skillruntime.NewSpawnGate(nil)

	skills := make([]skillruntime.ProjectionSkill, 0, len(bindings))
	for _, b := range bindings {
		frozen := skillruntime.FrozenSkillBundle{
			SkillRevisionID:   b.S("skillRevisionId"),
			ContentDigestAlgo: b.S("digestAlgorithm"),
			ContentDigest:     b.S("contentDigest"),
			PackageDigestAlgo: b.S("packageDigestAlgorithm"),
			PackageDigest:     b.S("packageDigest"),
			PackageFormat:     b.S("packageFormat"),
			PackageFormatVer:  int(b.N("packageFormatVersion")),
			SizeBytes:         b.N("sizeBytes"),
		}
		cap, ok := byRev[frozen.SkillRevisionID]
		if !ok {
			return nil, fmt.Errorf("no retrieval capability for frozen revision %s", frozen.SkillRevisionID)
		}
		vb, err := materializer.EnsureVerified(context.Background(), frozen, cap)
		if err != nil {
			return nil, err
		}
		skills = append(skills, skillruntime.ProjectionSkill{
			SkillID:       b.S("skillId"),
			CanonicalName: b.S("canonicalName"),
			Bundle:        frozen,
			Verified:      *vb,
		})
	}

	prepared, err := projector.Project(context.Background(), f.attempt, skills)
	if err != nil {
		return nil, err
	}
	ready, err := projector.Ready(context.Background(), *prepared)
	if err != nil {
		return nil, err
	}

	rt := &mockRuntime{}
	rt.accept(f.attempt, nodeID, epoch, gate.Open(*ready))
	return rt, nil
}

// TestRuntimeHandoffDeliversExactFrozenSnapshot pins the core handoff invariant: what the mock consumer
// receives (attempt identity, local root, ordered provisions) is exactly the frozen admission — the same
// SkillRevision ids and content digests the Execution froze — never a re-resolved current revision.
func TestRuntimeHandoffDeliversExactFrozenSnapshot(t *testing.T) {
	f := newHandoffFixture(t, "alpha", "beta")
	epoch := f.lease("c-a")
	f.dispatch("c-a", epoch, "node-a")

	rt, err := f.materialize("node-a", epoch)
	must(t, err)

	if rt.attemptID != f.attempt || rt.nodeID != "node-a" || rt.epoch != epoch {
		t.Fatalf("handoff identity mismatch: attempt=%q node=%q epoch=%d (want %q node-a %d)", rt.attemptID, rt.nodeID, rt.epoch, f.attempt, epoch)
	}
	if want := filepath.Join(f.attemptRoot, "attempts", f.attempt); rt.root != want {
		t.Fatalf("handoff root = %q, want the Attempt-local projection %q", rt.root, want)
	}
	if _, err := os.Stat(filepath.Join(rt.root, ".ora-attempt-ready")); err != nil {
		t.Fatalf("published projection must carry the READY marker: %v", err)
	}

	if len(rt.provisions) != 2 {
		t.Fatalf("want 2 provisions, got %d", len(rt.provisions))
	}
	for i, p := range rt.provisions {
		want := f.skills[i] // admission order is canonical_name ascending (alpha, beta)
		if p.SkillID != want.skillID || p.SkillRevisionID != want.revisionID || p.ContentDigest != want.contentDigest {
			t.Fatalf("provision %d = %+v, want frozen skill %q rev %q digest %q", i, p, want.skillID, want.revisionID, want.contentDigest)
		}
		if p.RuntimeName != want.revisionID {
			t.Fatalf("provision %d runtime name %q != revision id %q", i, p.RuntimeName, want.revisionID)
		}
		if _, err := os.Stat(filepath.Join(p.Dir, "SKILL.md")); err != nil {
			t.Fatalf("provision %d projected tree must contain SKILL.md: %v", i, err)
		}
	}
}

// TestRuntimeHandoffIgnoresPostAdmissionMutableState pins the no-re-resolution redline: after admission the
// Skill's current_revision_id may advance, the binding may be disabled, and other Skills may be added — but
// the handoff still delivers exactly the frozen R1, never the drifted current state.
func TestRuntimeHandoffIgnoresPostAdmissionMutableState(t *testing.T) {
	f := newHandoffFixture(t, "alpha")
	epoch := f.lease("c-a")
	f.dispatch("c-a", epoch, "node-a")

	r2 := insertRealRevision(t, f.store.Pool, f.skills[0].skillID, f.owner, "alpha-v2")
	f.issuer.byLoc[r2.locator] = r2.pkgBytes
	execOK(t, f.store.Pool, `UPDATE skills SET current_revision_id=$2,version=version+1,updated_at=now() WHERE id=$1`, f.skills[0].skillID, r2.revisionID)
	execOK(t, f.store.Pool, `UPDATE agent_skill_bindings SET enabled=false WHERE agent_id=$1 AND skill_id=$2`, f.agent, f.skills[0].skillID)

	rt, err := f.materialize("node-a", epoch)
	must(t, err)
	if len(rt.provisions) != 1 {
		t.Fatalf("want 1 provision, got %d", len(rt.provisions))
	}
	p := rt.provisions[0]
	if p.SkillRevisionID != f.skills[0].revisionID || p.ContentDigest != f.skills[0].contentDigest {
		t.Fatalf("handoff must deliver the frozen R1 (rev=%s digest=%s), got rev=%s", f.skills[0].revisionID, f.skills[0].contentDigest, p.SkillRevisionID)
	}
	if p.SkillRevisionID == r2.revisionID {
		t.Fatal("handoff must never re-resolve the drifted current revision R2")
	}
}

// TestRuntimeHandoffRetryReusesFrozenBindings pins D5: a Retry (new Attempt over the same Execution) reuses
// the frozen ExecutionSkillBinding set, even though current_revision_id has since advanced to R2.
func TestRuntimeHandoffRetryReusesFrozenBindings(t *testing.T) {
	f := newHandoffFixture(t, "alpha")
	r2 := insertRealRevision(t, f.store.Pool, f.skills[0].skillID, f.owner, "alpha-v2")
	f.issuer.byLoc[r2.locator] = r2.pkgBytes
	execOK(t, f.store.Pool, `UPDATE skills SET current_revision_id=$2,version=version+1,updated_at=now() WHERE id=$1`, f.skills[0].skillID, r2.revisionID)

	retried, err := f.store.CreateRetryAttempt(context.Background(), f.execution)
	must(t, err)
	f.attempt = retried["attempts"].([]core.Object)[1].S("attemptId")

	epoch := f.lease("c-a")
	f.dispatch("c-a", epoch, "node-a")
	rt, err := f.materialize("node-a", epoch)
	must(t, err)
	if len(rt.provisions) != 1 || rt.provisions[0].SkillRevisionID != f.skills[0].revisionID {
		t.Fatalf("retry must deliver the frozen R1, got %+v", rt.provisions)
	}
}

// TestRuntimeHandoffRunAgainReResolvesCurrentRevision pins D5: Run Again (a brand-new Execution) re-resolves
// the current configuration and therefore delivers R2 after current_revision_id advanced.
func TestRuntimeHandoffRunAgainReResolvesCurrentRevision(t *testing.T) {
	f := newHandoffFixture(t, "alpha")
	r2 := insertRealRevision(t, f.store.Pool, f.skills[0].skillID, f.owner, "alpha-v2")
	f.issuer.byLoc[r2.locator] = r2.pkgBytes
	execOK(t, f.store.Pool, `UPDATE skills SET current_revision_id=$2,version=version+1,updated_at=now() WHERE id=$1`, f.skills[0].skillID, r2.revisionID)

	again, err := f.store.RunAgainExecution(context.Background(), "corp", "alice", "Caller", f.execution, core.Object{"prompt": "again"})
	must(t, err)
	f.execution = again.O("execution").S("executionId")
	f.attempt = again["attempts"].([]core.Object)[0].S("attemptId")

	epoch := f.lease("c-a")
	f.dispatch("c-a", epoch, "node-a")
	rt, err := f.materialize("node-a", epoch)
	must(t, err)
	if len(rt.provisions) != 1 || rt.provisions[0].SkillRevisionID != r2.revisionID {
		t.Fatalf("run-again must re-resolve to R2, got %+v", rt.provisions)
	}
}

// TestRuntimeHandoffDeterministicOrdering pins D12: provisions are ordered by canonical_name →
// skill_id → skill_revision_id, independent of provisioning/admission input order.
func TestRuntimeHandoffDeterministicOrdering(t *testing.T) {
	f := newHandoffFixture(t, "zebra", "alpha", "beta") // deliberately non-sorted input
	epoch := f.lease("c-a")
	f.dispatch("c-a", epoch, "node-a")

	rt, err := f.materialize("node-a", epoch)
	must(t, err)
	if len(rt.provisions) != 3 {
		t.Fatalf("want 3 provisions, got %d", len(rt.provisions))
	}
	byRev := map[string]string{}
	for _, s := range f.skills {
		byRev[s.revisionID] = s.canonicalName
	}
	want := []string{"alpha", "beta", "zebra"}
	for i, p := range rt.provisions {
		if got := byRev[p.SkillRevisionID]; got != want[i] {
			t.Fatalf("provision order[%d] = %q, want canonical-name order %v", i, got, want)
		}
	}
}

// TestRuntimeHandoffPartialFailureNeverOpensHandoff pins the all-or-nothing rule: with one of two Skills
// failing byte verification, no LaunchSpec is produced and no partial Attempt projection is published.
func TestRuntimeHandoffPartialFailureNeverOpensHandoff(t *testing.T) {
	f := newHandoffFixture(t, "alpha", "beta")
	epoch := f.lease("c-a")
	f.dispatch("c-a", epoch, "node-a")

	tampered := append([]byte(nil), f.issuer.byLoc[f.skills[1].locator]...)
	tampered[0] ^= 0xff
	f.issuer.byLoc[f.skills[1].locator] = tampered

	_, err := f.materialize("node-a", epoch)
	if err == nil || !errors.Is(err, skillruntime.ErrPackageDigestMismatch) {
		t.Fatalf("want ErrPackageDigestMismatch, got %v", err)
	}
	if _, statErr := os.Stat(filepath.Join(f.attemptRoot, "attempts", f.attempt)); !os.IsNotExist(statErr) {
		t.Fatalf("partial failure must leave no Attempt projection, stat err=%v", statErr)
	}
}

// TestRuntimeHandoffNeverLeaksCapability pins the secret redline at the handoff boundary: the signed URL
// never appears in the READY marker or the projected tree; the projected content is the canonical bytes only.
func TestRuntimeHandoffNeverLeaksCapability(t *testing.T) {
	f := newHandoffFixture(t, "alpha")
	epoch := f.lease("c-a")
	f.dispatch("c-a", epoch, "node-a")

	rt, err := f.materialize("node-a", epoch)
	must(t, err)

	marker, err := os.ReadFile(filepath.Join(rt.root, ".ora-attempt-ready"))
	must(t, err)
	for _, bad := range []string{"X-Amz", "signature", "Signature", "cryptosig", "https://", "http://"} {
		if bytes.Contains(marker, []byte(bad)) {
			t.Fatalf("READY marker must carry no capability URL material (found %q): %s", bad, marker)
		}
	}

	got, err := os.ReadFile(filepath.Join(rt.provisions[0].Dir, "SKILL.md"))
	must(t, err)
	if !bytes.Contains(got, []byte("# Skill alpha")) || bytes.Contains(got, []byte("cryptosig")) {
		t.Fatalf("projected SKILL.md must be canonical content only, got %q", got)
	}
}

// TestRuntimeHandoffFencing pins that the handoff records the fenced dispatch epoch, and that a stale
// controller epoch is rejected at the control layer (the mock consumer only ever sees valid generations).
func TestRuntimeHandoffFencing(t *testing.T) {
	f := newHandoffFixture(t, "alpha")
	epochA := f.lease("c-a")
	f.dispatch("c-a", epochA, "node-a")

	must(t, func() error { _, err := f.control("c-a", "lease_release", core.Object{"epoch": epochA}); return err }())
	epochB := f.lease("c-b")

	if _, err := f.control("c-a", "attempt_result", core.Object{"epoch": epochA, "attemptId": f.attempt, "nodeId": "node-a", "outcome": "prepared"}); core.ErrorCode(err).Code != "stale_controller" {
		t.Fatalf("stale epoch must be fenced, got %v", core.ErrorCode(err))
	}

	rt, err := f.materialize("node-a", epochB)
	must(t, err)
	if rt.epoch != epochB {
		t.Fatalf("consumer epoch = %d, want %d", rt.epoch, epochB)
	}
}

// TestRuntimeHandoffChainNeverExecutes pins the fail-closed execution boundary at the source level: the
// materialization/projection/READY/spawn-gate chain itself never spawns a process (no os/exec, no
// StartProcess, no syscall.Exec) — it copies bytes and writes the READY marker, nothing more.
func TestRuntimeHandoffChainNeverExecutes(t *testing.T) {
	files, err := filepath.Glob(filepath.Join("..", "internal", "skillruntime", "*.go"))
	must(t, err)
	if len(files) == 0 {
		t.Fatal("no skillruntime sources found")
	}
	for _, rel := range files {
		src, err := os.ReadFile(rel)
		must(t, err)
		for _, banned := range []string{"os/exec", "exec.Command", "os.StartProcess", "syscall.Exec"} {
			if bytes.Contains(src, []byte(banned)) {
				t.Fatalf("%s must never execute a process (found %q)", rel, banned)
			}
		}
	}
}
