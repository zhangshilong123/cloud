package skillruntime

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// fakeProjCache writes a fake verified cache tree filled with the given relative-path -> content map. It
// is a plain directory tree; the projector only ever reads it (copy source), so no real codec/materializer
// is needed. A symlink-free tree is the normal case; a test that needs a non-regular entry adds it itself.
func fakeProjCache(t *testing.T, files map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	for rel, content := range files {
		p := filepath.Join(dir, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

// projSkill builds a coherent ProjectionSkill: unique 64-hex content digest, safe immutable revision id,
// and the frozen metadata the projection fail-closes on.
func projSkill(i int, cacheDir string, revisionID string) ProjectionSkill {
	d := fmt.Sprintf("%064x", i)
	return ProjectionSkill{
		SkillID:       fmt.Sprintf("skill-%06d", i),
		CanonicalName: fmt.Sprintf("alpha-%03d", i),
		Bundle: FrozenSkillBundle{
			SkillRevisionID:   revisionID,
			ContentDigestAlgo: "sha256",
			ContentDigest:     d,
			PackageDigestAlgo: "sha256",
			PackageDigest:     fmt.Sprintf("%064x", i+1000000),
			PackageFormat:     "ora-skill-package",
			PackageFormatVer:  1,
			SizeBytes:         4,
		},
		Verified: VerifiedSkillBundle{ContentDigest: d, DigestAlgo: "sha256", CacheDir: cacheDir},
	}
}

func TestNewProjector_RequiresRoot(t *testing.T) {
	if _, err := NewProjector(""); err == nil {
		t.Fatal("empty attempt root should be rejected")
	}
	if _, err := NewProjector("   "); err == nil {
		t.Fatal("whitespace attempt root should be rejected")
	}
}

func TestProject_MaterializesAllSkillsAndReadies(t *testing.T) {
	p, _ := NewProjector(t.TempDir())
	attemptID := "attempt-000001"
	cacheA := fakeProjCache(t, map[string]string{"SKILL.md": "skill-a", "docs/guide.md": "guide-a"})
	cacheB := fakeProjCache(t, map[string]string{"SKILL.md": "skill-b"})
	skills := []ProjectionSkill{
		projSkill(1, cacheA, "rev-000001"),
		projSkill(2, cacheB, "rev-000002"),
	}

	prepared, err := p.Project(context.Background(), attemptID, skills)
	if err != nil {
		t.Fatalf("Project: %v", err)
	}
	if prepared.AttemptID != attemptID {
		t.Fatalf("AttemptID = %q, want %q", prepared.AttemptID, attemptID)
	}
	wantRoot := filepath.Join(p.attemptRoot, "attempts", attemptID)
	if prepared.Root != wantRoot {
		t.Fatalf("Root = %q, want %q", prepared.Root, wantRoot)
	}
	if _, err := os.Stat(filepath.Join(prepared.Root, attemptMarkerName)); err != nil {
		t.Fatalf("READY marker missing: %v", err)
	}

	ready, err := p.Ready(context.Background(), *prepared)
	if err != nil {
		t.Fatalf("Ready: %v", err)
	}
	if len(ready.Skills) != 2 {
		t.Fatalf("Ready skills = %d, want 2", len(ready.Skills))
	}
	for _, sp := range ready.Skills {
		if !strings.HasPrefix(sp.RuntimeName, "rev-") {
			t.Fatalf("runtime name %q is not the immutable revision id", sp.RuntimeName)
		}
		data, err := os.ReadFile(filepath.Join(sp.Dir, "SKILL.md"))
		if err != nil {
			t.Fatalf("projected SKILL.md missing in %s: %v", sp.Dir, err)
		}
		if !strings.Contains(string(data), "skill-") {
			t.Fatalf("projected SKILL.md content %q unexpected", string(data))
		}
	}
}

func TestProject_CopiesNotHardlinks(t *testing.T) {
	p, _ := NewProjector(t.TempDir())
	cache := fakeProjCache(t, map[string]string{"SKILL.md": "original"})
	prepared, err := p.Project(context.Background(), "attempt-000002", []ProjectionSkill{projSkill(1, cache, "rev-000001")})
	if err != nil {
		t.Fatal(err)
	}
	ready, err := p.Ready(context.Background(), *prepared)
	if err != nil {
		t.Fatal(err)
	}
	projected := filepath.Join(ready.Skills[0].Dir, "SKILL.md")
	if err := os.WriteFile(projected, []byte("mutated by runtime"), 0o600); err != nil {
		t.Fatal(err)
	}
	src, err := os.ReadFile(filepath.Join(cache, "SKILL.md"))
	if err != nil {
		t.Fatal(err)
	}
	if string(src) != "original" {
		t.Fatalf("verified cache was mutated by projection: %q", string(src))
	}
}

func TestProject_InvalidInputFailsClosedBeforePublish(t *testing.T) {
	p, _ := NewProjector(t.TempDir())
	cache := fakeProjCache(t, map[string]string{"SKILL.md": "x"})

	// Missing cache dir: verified result cannot be a real tree.
	bad := projSkill(1, cache, "rev-000001")
	bad.Verified.CacheDir = filepath.Join(t.TempDir(), "does-not-exist")
	_, err := p.Project(context.Background(), "attempt-000003", []ProjectionSkill{bad})
	if !errors.Is(err, ErrProjectionMismatch) {
		t.Fatalf("err = %v, want ErrProjectionMismatch", err)
	}
	if _, statErr := os.Stat(filepath.Join(p.attemptRoot, "attempts", "attempt-000003")); !os.IsNotExist(statErr) {
		t.Fatalf("no published attempt expected on failure, got %v", statErr)
	}

	// Digest mismatch: verified result must correspond to the frozen bundle.
	mismatch := projSkill(2, cache, "rev-000002")
	mismatch.Verified.ContentDigest = fmt.Sprintf("%064x", 999)
	_, err = p.Project(context.Background(), "attempt-000004", []ProjectionSkill{mismatch})
	if !errors.Is(err, ErrProjectionMismatch) {
		t.Fatalf("err = %v, want ErrProjectionMismatch", err)
	}
}

func TestProject_AbortedProjectionLeavesNothingVisible(t *testing.T) {
	p, _ := NewProjector(t.TempDir())
	cache := fakeProjCache(t, map[string]string{"SKILL.md": "x"})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := p.Project(ctx, "attempt-000005", []ProjectionSkill{projSkill(1, cache, "rev-000001")})
	if err == nil {
		t.Fatal("cancelled context should fail the projection")
	}
	if _, statErr := os.Stat(filepath.Join(p.attemptRoot, "attempts", "attempt-000005")); !os.IsNotExist(statErr) {
		t.Fatalf("no published attempt expected, got %v", statErr)
	}
	staging, _ := os.ReadDir(filepath.Join(p.attemptRoot, ".staging"))
	if len(staging) != 0 {
		t.Fatalf("staging should be empty after abort, got %d entries", len(staging))
	}
}

func TestReady_ExplicitAndValidatesEverySkill(t *testing.T) {
	p, _ := NewProjector(t.TempDir())
	cacheA := fakeProjCache(t, map[string]string{"SKILL.md": "a"})
	cacheB := fakeProjCache(t, map[string]string{"SKILL.md": "b"})
	skills := []ProjectionSkill{projSkill(1, cacheA, "rev-000001"), projSkill(2, cacheB, "rev-000002")}

	prepared, err := p.Project(context.Background(), "attempt-000006", skills)
	if err != nil {
		t.Fatal(err)
	}

	// READY must not be inferred from the directory alone: remove the marker and READY fails closed.
	if err := os.Remove(filepath.Join(prepared.Root, attemptMarkerName)); err != nil {
		t.Fatal(err)
	}
	if _, err := p.Ready(context.Background(), *prepared); !errors.Is(err, ErrNotReady) {
		t.Fatalf("Ready without marker = %v, want ErrNotReady", err)
	}

	// A garbled marker is corruption, not readiness.
	if err := os.WriteFile(filepath.Join(prepared.Root, attemptMarkerName), []byte("{not json"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := p.Ready(context.Background(), *prepared); !errors.Is(err, ErrAttemptCorrupt) {
		t.Fatalf("Ready with corrupt marker = %v, want ErrAttemptCorrupt", err)
	}

	// Rebuild cleanly, then remove one skill's projection: READY fails on the missing required Skill.
	prepared2, err := p.Project(context.Background(), "attempt-000007", skills)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(filepath.Join(prepared2.Root, "rev-000002")); err != nil {
		t.Fatal(err)
	}
	if _, err := p.Ready(context.Background(), *prepared2); !errors.Is(err, ErrNotReady) {
		t.Fatalf("Ready with missing skill = %v, want ErrNotReady", err)
	}
}

func TestProject_DeterministicOrderAndImmutableNaming(t *testing.T) {
	p, _ := NewProjector(t.TempDir())
	cache := fakeProjCache(t, map[string]string{"SKILL.md": "content"})

	// Deliberately shuffled input: "gamma" (3) should sort after "alpha" (1) and before "zeta" (2).
	skills := []ProjectionSkill{
		projSkill(3, cache, "rev-000003"),
		projSkill(1, cache, "rev-000001"),
		projSkill(2, cache, "rev-000002"),
	}
	skills[0].CanonicalName = "gamma"
	skills[1].CanonicalName = "alpha"
	skills[2].CanonicalName = "zeta"

	prepared, err := p.Project(context.Background(), "attempt-000008", skills)
	if err != nil {
		t.Fatal(err)
	}
	ready, err := p.Ready(context.Background(), *prepared)
	if err != nil {
		t.Fatal(err)
	}

	var order []string
	for _, sp := range ready.Skills {
		order = append(order, sp.RuntimeName)
	}
	want := []string{"rev-000001", "rev-000003", "rev-000002"}
	for i := range want {
		if order[i] != want[i] {
			t.Fatalf("projection order = %v, want %v", order, want)
		}
	}
	// The runtime name must be the immutable revision id, never the canonical/display name.
	for _, sp := range ready.Skills {
		if got := filepath.Base(sp.Dir); got != sp.RuntimeName {
			t.Fatalf("dir base %q != runtime name %q", got, sp.RuntimeName)
		}
	}
}

func TestProject_RetryNewAttemptFreshProjectionReusesCache(t *testing.T) {
	p, _ := NewProjector(t.TempDir())
	cache := fakeProjCache(t, map[string]string{"SKILL.md": "frozen-content"})
	skills := []ProjectionSkill{projSkill(1, cache, "rev-000001")}

	a1, err := p.Project(context.Background(), "attempt-000010", skills)
	if err != nil {
		t.Fatal(err)
	}
	a2, err := p.Project(context.Background(), "attempt-000011", skills)
	if err != nil {
		t.Fatal(err)
	}
	if a1.Root == a2.Root {
		t.Fatalf("two attempts must have distinct projections: %q", a1.Root)
	}
	for _, root := range []string{a1.Root, a2.Root} {
		data, err := os.ReadFile(filepath.Join(root, "rev-000001", "SKILL.md"))
		if err != nil {
			t.Fatalf("projection %s missing SKILL.md: %v", root, err)
		}
		if string(data) != "frozen-content" {
			t.Fatalf("projection content = %q", string(data))
		}
	}
	src, err := os.ReadFile(filepath.Join(cache, "SKILL.md"))
	if err != nil || string(src) != "frozen-content" {
		t.Fatalf("verified cache must remain untouched across retries: %v %q", err, string(src))
	}
}

func TestProject_CollisionFailsClosed(t *testing.T) {
	p, _ := NewProjector(t.TempDir())
	cache := fakeProjCache(t, map[string]string{"SKILL.md": "x"})
	skills := []ProjectionSkill{
		projSkill(1, cache, "rev-000001"),
		projSkill(2, fakeProjCache(t, map[string]string{"SKILL.md": "y"}), "rev-000001"),
	}
	if _, err := p.Project(context.Background(), "attempt-000012", skills); !errors.Is(err, ErrProjectionCollision) {
		t.Fatalf("err = %v, want ErrProjectionCollision", err)
	}
}

func TestProject_SkipsCacheMarkerAndCopiesBytesVerbatim(t *testing.T) {
	p, _ := NewProjector(t.TempDir())
	script := "#!/bin/sh\necho hello\n"
	cache := fakeProjCache(t, map[string]string{
		"SKILL.md":       "content",
		markerFileName:   "cache-internal-marker",
		"scripts/run.sh": script,
	})
	prepared, err := p.Project(context.Background(), "attempt-000013", []ProjectionSkill{projSkill(1, cache, "rev-000001")})
	if err != nil {
		t.Fatal(err)
	}
	skillDir := filepath.Join(prepared.Root, "rev-000001")
	if _, err := os.Stat(filepath.Join(skillDir, markerFileName)); !os.IsNotExist(err) {
		t.Fatalf("cache-internal marker must not leak into projection: %v", err)
	}
	got, err := os.ReadFile(filepath.Join(skillDir, "scripts", "run.sh"))
	if err != nil {
		t.Fatalf("script not copied: %v", err)
	}
	if string(got) != script {
		t.Fatalf("script bytes changed: %q", string(got))
	}
}

func TestCleanupAttempt_IdempotentAndScoped(t *testing.T) {
	p, _ := NewProjector(t.TempDir())
	cache := fakeProjCache(t, map[string]string{"SKILL.md": "x"})
	skills := []ProjectionSkill{projSkill(1, cache, "rev-000001")}
	if _, err := p.Project(context.Background(), "attempt-000020", skills); err != nil {
		t.Fatal(err)
	}
	if _, err := p.Project(context.Background(), "attempt-000021", skills); err != nil {
		t.Fatal(err)
	}

	if err := p.CleanupAttempt("attempt-000020"); err != nil {
		t.Fatal(err)
	}
	if err := p.CleanupAttempt("attempt-000020"); err != nil { // idempotent
		t.Fatalf("second cleanup should be a no-op: %v", err)
	}
	if _, err := os.Stat(filepath.Join(p.attemptRoot, "attempts", "attempt-000020")); !os.IsNotExist(err) {
		t.Fatalf("cleaned attempt should be gone: %v", err)
	}
	if _, err := os.Stat(filepath.Join(p.attemptRoot, "attempts", "attempt-000021")); err != nil {
		t.Fatalf("sibling attempt must be preserved: %v", err)
	}
	if _, err := os.Stat(filepath.Join(cache, "SKILL.md")); err != nil {
		t.Fatalf("verified cache must be preserved: %v", err)
	}
}

func TestCollect_BoundsCacheByAgeAndCount(t *testing.T) {
	m, err := New(Config{CacheRoot: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	entriesRoot := filepath.Join(m.cfg.CacheRoot, m.contentAlg)
	mk := func(digest string, mtime time.Time) {
		dir := filepath.Join(entriesRoot, digest)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.Chtimes(dir, mtime, mtime); err != nil {
			t.Fatal(err)
		}
	}
	old := time.Now().Add(-48 * time.Hour)
	now := time.Now()
	mk(fmt.Sprintf("%064x", 1), now)
	mk(fmt.Sprintf("%064x", 2), now)
	mk(fmt.Sprintf("%064x", 3), old)

	// Age bound removes the stale entry while leaving recent ones.
	removed, err := m.Collect(24*time.Hour, -1)
	if err != nil {
		t.Fatal(err)
	}
	if removed != 1 {
		t.Fatalf("age eviction removed %d, want 1", removed)
	}
	if _, err := os.Stat(filepath.Join(entriesRoot, fmt.Sprintf("%064x", 3))); !os.IsNotExist(err) {
		t.Fatalf("stale entry should be evicted: %v", err)
	}

	// Count bound evicts oldest-first down to the retain count.
	mk(fmt.Sprintf("%064x", 4), now)
	removed, err = m.Collect(0, 2)
	if err != nil {
		t.Fatal(err)
	}
	if removed != 1 {
		t.Fatalf("count eviction removed %d, want 1", removed)
	}
}

func TestSpawnGate_OpenOnlyFromReady(t *testing.T) {
	p, _ := NewProjector(t.TempDir())
	cacheA := fakeProjCache(t, map[string]string{"SKILL.md": "a"})
	cacheB := fakeProjCache(t, map[string]string{"SKILL.md": "b"})
	prepared, err := p.Project(context.Background(), "attempt-000030", []ProjectionSkill{
		projSkill(1, cacheA, "rev-000001"),
		projSkill(2, cacheB, "rev-000002"),
	})
	if err != nil {
		t.Fatal(err)
	}
	ready, err := p.Ready(context.Background(), *prepared)
	if err != nil {
		t.Fatal(err)
	}

	spec := NewSpawnGate(nil).Open(*ready)
	if spec.AttemptID != "attempt-000030" {
		t.Fatalf("launch spec attempt = %q", spec.AttemptID)
	}
	if spec.Root != ready.Root {
		t.Fatalf("launch spec root = %q", spec.Root)
	}
	if len(spec.Provisions) != 2 {
		t.Fatalf("launch spec provisions = %d, want 2", len(spec.Provisions))
	}
	for _, prov := range spec.Provisions {
		if info, err := os.Stat(prov.Dir); err != nil || !info.IsDir() {
			t.Fatalf("provision dir %s invalid: %v", prov.Dir, err)
		}
	}
}
