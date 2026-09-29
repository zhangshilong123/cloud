package main

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/wanglongan587/cloud/internal/skillpkg"
	"github.com/wanglongan587/cloud/internal/skillstore"
)

// buildPkg builds a real canonical ora-skill-package v1 and returns its bytes, tree content digest,
// physical package digest, and content total (sum of file sizes — the frozen size_bytes semantics).
func buildPkg(t *testing.T) (pkg []byte, contentDigest, packageDigest string, sizeBytes int64) {
	t.Helper()
	files := []skillpkg.SourceFile{
		{Path: "SKILL.md", Data: []byte("# Alpha\n")},
		{Path: "scripts/run.sh", Data: []byte("#!/bin/sh\necho hi\n")},
	}
	b, err := skillpkg.Build(files, skillpkg.DefaultLimits())
	if err != nil {
		t.Fatalf("build package: %v", err)
	}
	var total int64
	for _, e := range b.Entries {
		total += int64(e.Size)
	}
	return b.Package, b.TreeDigestHex(), skillstore.PackageDigestHex(b.Package), total
}

// servePkg returns an httptest server that serves exact package bytes at /pkg.
func servePkg(t *testing.T, pkg []byte) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/pkg" {
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write(pkg)
	}))
	t.Cleanup(srv.Close)
	return srv
}

// newRequest assembles a valid helper request for a single revision served at the given URL.
func newRequest(attemptID, cacheRoot, attemptRoot, revisionID, contentDigest, packageDigest string, sizeBytes int64, url string) request {
	return request{
		AttemptID:   attemptID,
		CacheRoot:   cacheRoot,
		AttemptRoot: attemptRoot,
		Skills: []skillEntry{{
			SkillID:           "00000000-0000-0000-0000-000000000001",
			CanonicalName:     "alpha",
			SkillRevisionID:   revisionID,
			ContentDigestAlgo: "sha256",
			ContentDigest:     contentDigest,
			PackageDigestAlgo: "sha256",
			PackageDigest:     packageDigest,
			PackageFormat:     "ora-skill-package",
			PackageFormatVer:  1,
			SizeBytes:         sizeBytes,
		}},
		Capabilities: []capability{{SkillRevisionID: revisionID, Method: "GET", URL: url}},
	}
}

// invoke runs the helper entry point over req and returns the exit code and the decoded stdout result.
func invoke(t *testing.T, req request) (int, result) {
	t.Helper()
	body, err := json.Marshal(req)
	if err != nil {
		t.Fatalf("marshal request: %v", err)
	}
	var out bytes.Buffer
	code := run(context.Background(), bytes.NewReader(body), &out)
	var res result
	if err := json.Unmarshal(out.Bytes(), &res); err != nil {
		t.Fatalf("decode result %q: %v", out.String(), err)
	}
	return code, res
}

// TestMaterializeHappyPathCanonicalChain runs the full canonical chain end-to-end over a real package:
// prepared, local_root is the published projection, and the projected tree is on disk.
func TestMaterializeHappyPathCanonicalChain(t *testing.T) {
	pkg, contentDigest, packageDigest, sizeBytes := buildPkg(t)
	srv := servePkg(t, pkg)
	cacheRoot := t.TempDir()
	attemptRoot := t.TempDir()
	attemptID := "11111111-1111-1111-1111-111111111111"
	revisionID := "22222222-2222-2222-2222-222222222222"

	code, res := invoke(t, newRequest(attemptID, cacheRoot, attemptRoot, revisionID, contentDigest, packageDigest, sizeBytes, srv.URL+"/pkg"))
	if code != 0 || !res.Prepared {
		t.Fatalf("want prepared (exit 0), got exit %d result %+v", code, res)
	}
	want := filepath.Join(attemptRoot, "attempts", attemptID)
	if res.LocalRoot != want {
		t.Fatalf("local_root must be the published projection, got %q want %q", res.LocalRoot, want)
	}
	// The projected canonical tree (skill_revision_id dir + SKILL.md) is on disk, READY marker present.
	for _, p := range []string{
		res.LocalRoot,
		filepath.Join(res.LocalRoot, revisionID),
		filepath.Join(res.LocalRoot, revisionID, "SKILL.md"),
		filepath.Join(res.LocalRoot, ".ora-attempt-ready"),
	} {
		if _, err := os.Stat(p); err != nil {
			t.Fatalf("expected projection path %q: %v", p, err)
		}
	}
	// The verified cache entry is published keyed by digest, with its commit marker.
	for _, p := range []string{
		filepath.Join(cacheRoot, "sha256", contentDigest),
		filepath.Join(cacheRoot, "sha256", contentDigest, ".ora-skill-complete"),
		filepath.Join(cacheRoot, "sha256", contentDigest, "SKILL.md"),
	} {
		if _, err := os.Stat(p); err != nil {
			t.Fatalf("expected cache path %q: %v", p, err)
		}
	}
}

// TestMaterializeNeverLeaksCapability pins the secret redline: the signed URL (a bearer, modelled here
// as a working endpoint with a signature-like query) never appears in the success result, and even on
// failure stdout carries only the stable code.
func TestMaterializeNeverLeaksCapability(t *testing.T) {
	pkg, contentDigest, packageDigest, sizeBytes := buildPkg(t)
	srv := servePkg(t, pkg)
	secret := srv.URL + "/pkg?X-Amz-Signature=deadbeefcrypto&X-Amz-Credential=AKIA123"

	cacheRoot := t.TempDir()
	attemptRoot := t.TempDir()
	attemptID := "11111111-1111-1111-1111-111111111111"
	revisionID := "22222222-2222-2222-2222-222222222222"

	// Success path: the URL is carried only in the request; the result must not echo it.
	code, res := invoke(t, newRequest(attemptID, cacheRoot, attemptRoot, revisionID, contentDigest, packageDigest, sizeBytes, secret))
	if code != 0 || !res.Prepared {
		t.Fatalf("want prepared, got %d %+v", code, res)
	}
	if got := resultJSON(res); strings.Contains(got, secret) || strings.Contains(got, "X-Amz") {
		t.Fatalf("success result must not leak the capability URL: %s", got)
	}

	// Failure path: a fresh cache/attempt (no prior population) plus a digest mismatch forces a real
	// download and a materialization failure; stdout must carry no URL/signature.
	bad := newRequest(attemptID, t.TempDir(), t.TempDir(), revisionID, contentDigest, strings.Repeat("a", 64), sizeBytes, secret)
	body, err := json.Marshal(bad)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var out bytes.Buffer
	code = run(context.Background(), bytes.NewReader(body), &out)
	if code != 1 {
		t.Fatalf("want preparation_failed exit 1, got %d", code)
	}
	if got := out.String(); strings.Contains(got, secret) || strings.Contains(got, "X-Amz") || strings.Contains(got, "signature") {
		t.Fatalf("failure result must not leak the capability URL: %s", got)
	}
}

// TestMaterializeInputComesOnlyFromStdin documents the invocation contract at the source level: the
// helper binds credentials to os.Stdin (a closed channel), never to os.Args or the environment.
func TestMaterializeInputComesOnlyFromStdin(t *testing.T) {
	src, err := os.ReadFile("main.go")
	if err != nil {
		t.Fatalf("read main.go: %v", err)
	}
	for _, banned := range []string{"os.Args", "os.Getenv", "flag."} {
		if bytes.Contains(src, []byte(banned)) {
			t.Fatalf("helper input must be stdin-only (found %q in main.go)", banned)
		}
	}
	if !bytes.Contains(src, []byte("os.Stdin")) {
		t.Fatalf("helper must read the request from os.Stdin")
	}
}

// TestMaterializeCrossProcessSameDigestConverges simulates two independent helper invocations (separate
// Materializer instances, i.e. separate processes) over one shared cache root: both converge, the second
// reuses the first's published entry, and the cache still holds a single entry.
func TestMaterializeCrossProcessSameDigestConverges(t *testing.T) {
	pkg, contentDigest, packageDigest, sizeBytes := buildPkg(t)
	srv := servePkg(t, pkg)
	cacheRoot := t.TempDir()
	attemptRoot := t.TempDir()
	attemptID := "11111111-1111-1111-1111-111111111111"
	revisionID := "22222222-2222-2222-2222-222222222222"

	req := newRequest(attemptID, cacheRoot, attemptRoot, revisionID, contentDigest, packageDigest, sizeBytes, srv.URL+"/pkg")
	for i := 0; i < 2; i++ {
		code, res := invoke(t, req)
		if code != 0 || !res.Prepared {
			t.Fatalf("invocation %d: want prepared, got %d %+v", i, code, res)
		}
	}
	// A single digest-addressed entry (plus the .staging dir), never a second population.
	entries, err := os.ReadDir(filepath.Join(cacheRoot, "sha256"))
	if err != nil {
		t.Fatalf("read cache entries: %v", err)
	}
	var names []string
	for _, e := range entries {
		if e.IsDir() {
			names = append(names, e.Name())
		}
	}
	if len(names) != 1 || names[0] != contentDigest {
		t.Fatalf("cache must hold exactly one digest entry, got %v", names)
	}
}

// TestMaterializeSameAttemptRepeatIdempotent pins same-Attempt reconciliation: a repeat converges to the
// same local_root and does not corrupt the published projection.
func TestMaterializeSameAttemptRepeatIdempotent(t *testing.T) {
	pkg, contentDigest, packageDigest, sizeBytes := buildPkg(t)
	srv := servePkg(t, pkg)
	cacheRoot := t.TempDir()
	attemptRoot := t.TempDir()
	attemptID := "11111111-1111-1111-1111-111111111111"
	revisionID := "22222222-2222-2222-2222-222222222222"

	req := newRequest(attemptID, cacheRoot, attemptRoot, revisionID, contentDigest, packageDigest, sizeBytes, srv.URL+"/pkg")
	_, first := invoke(t, req)
	_, second := invoke(t, req)
	if !first.Prepared || !second.Prepared || first.LocalRoot != second.LocalRoot {
		t.Fatalf("repeat must converge to the same projection: %+v vs %+v", first, second)
	}
}

// TestMaterializeDigestMismatchFailsClosed pins both digest redlines: a package_digest mismatch and a
// content_digest mismatch each fail closed with the exact stable code and leave no projection behind.
func TestMaterializeDigestMismatchFailsClosed(t *testing.T) {
	pkg, contentDigest, packageDigest, sizeBytes := buildPkg(t)
	srv := servePkg(t, pkg)
	attemptRoot := t.TempDir()
	attemptID := "11111111-1111-1111-1111-111111111111"
	revisionID := "22222222-2222-2222-2222-222222222222"

	// package_digest mismatch: physical bytes are correct, but the frozen package digest is wrong.
	wrongPkg := newRequest(attemptID, t.TempDir(), attemptRoot, revisionID, contentDigest, strings.Repeat("b", 64), sizeBytes, srv.URL+"/pkg")
	code, res := invoke(t, wrongPkg)
	if code != 1 || res.Prepared || res.StableErrorCode != "package_digest_mismatch" {
		t.Fatalf("package_digest mismatch must fail closed: exit %d res %+v", code, res)
	}
	if _, err := os.Stat(filepath.Join(attemptRoot, "attempts", attemptID)); !os.IsNotExist(err) {
		t.Fatalf("package_digest mismatch must leave no projection")
	}

	// content_digest mismatch: package bytes digest to a different tree than the frozen content_digest.
	wrongContent := newRequest(attemptID, t.TempDir(), attemptRoot, revisionID, strings.Repeat("c", 64), packageDigest, sizeBytes, srv.URL+"/pkg")
	code, res = invoke(t, wrongContent)
	if code != 1 || res.Prepared || res.StableErrorCode != "content_digest_mismatch" {
		t.Fatalf("content_digest mismatch must fail closed: exit %d res %+v", code, res)
	}
}

// TestMaterializeMultiSkillAllOrNothing pins the V1 rule: when one of two required skills fails, nothing
// is projected (no partial projection), and the failure is surfaced with its stable code.
func TestMaterializeMultiSkillAllOrNothing(t *testing.T) {
	pkg, contentDigest, packageDigest, sizeBytes := buildPkg(t)
	srv := servePkg(t, pkg)
	attemptRoot := t.TempDir()
	attemptID := "11111111-1111-1111-1111-111111111111"
	good := "22222222-2222-2222-2222-222222222222"
	bad := "33333333-3333-3333-3333-333333333333"

	req := newRequest(attemptID, t.TempDir(), attemptRoot, good, contentDigest, packageDigest, sizeBytes, srv.URL+"/pkg")
	req.Skills = append(req.Skills, skillEntry{
		SkillID:           "00000000-0000-0000-0000-000000000002",
		CanonicalName:     "beta",
		SkillRevisionID:   bad,
		ContentDigestAlgo: "sha256",
		ContentDigest:     strings.Repeat("d", 64),
		PackageDigestAlgo: "sha256",
		PackageDigest:     packageDigest,
		PackageFormat:     "ora-skill-package",
		PackageFormatVer:  1,
		SizeBytes:         sizeBytes,
	})
	req.Capabilities = append(req.Capabilities, capability{SkillRevisionID: bad, Method: "GET", URL: srv.URL + "/pkg"})

	code, res := invoke(t, req)
	if code != 1 || res.Prepared || res.StableErrorCode != "content_digest_mismatch" {
		t.Fatalf("multi-skill failure must fail closed: exit %d res %+v", code, res)
	}
	if _, err := os.Stat(filepath.Join(attemptRoot, "attempts", attemptID)); !os.IsNotExist(err) {
		t.Fatalf("multi-skill failure must leave no partial projection")
	}
}

// TestMaterializeUnsupportedFormatFailsClosed pins the fail-closed codec gate: a non-sha256 algorithm or a
// non-ora-skill-package format is rejected with its exact stable code before any network IO.
func TestMaterializeUnsupportedFormatFailsClosed(t *testing.T) {
	pkg, contentDigest, packageDigest, sizeBytes := buildPkg(t)
	srv := servePkg(t, pkg)
	attemptRoot := t.TempDir()
	attemptID := "11111111-1111-1111-1111-111111111111"
	revisionID := "22222222-2222-2222-2222-222222222222"

	badAlgo := newRequest(attemptID, t.TempDir(), attemptRoot, revisionID, contentDigest, packageDigest, sizeBytes, srv.URL+"/pkg")
	badAlgo.Skills[0].ContentDigestAlgo = "md5"
	code, res := invoke(t, badAlgo)
	if code != 1 || res.Prepared || res.StableErrorCode != "unsupported_digest_algorithm" {
		t.Fatalf("non-sha256 must fail closed: exit %d res %+v", code, res)
	}

	badFmt := newRequest(attemptID, t.TempDir(), attemptRoot, revisionID, contentDigest, packageDigest, sizeBytes, srv.URL+"/pkg")
	badFmt.Skills[0].PackageFormat = "ora-skill-package"
	badFmt.Skills[0].PackageFormatVer = 2
	code, res = invoke(t, badFmt)
	if code != 1 || res.Prepared || res.StableErrorCode != "unsupported_package_format" {
		t.Fatalf("unknown format version must fail closed: exit %d res %+v", code, res)
	}
}

// TestMaterializeMalformedRequest pins the exit-2 malformed-input contract for unreadable/oversized/invalid
// stdin without attempting any materialization.
func TestMaterializeMalformedRequest(t *testing.T) {
	// Invalid JSON.
	var out bytes.Buffer
	if code := run(context.Background(), strings.NewReader("{not json"), &out); code != 2 {
		t.Fatalf("invalid JSON must exit 2, got %d (%s)", code, out.String())
	}
	if got := out.String(); strings.Contains(got, "invalid_request") == false {
		t.Fatalf("invalid JSON must report invalid_request, got %s", got)
	}
}

// resultJSON marshals a result back to JSON for secret-scan assertions.
func resultJSON(res result) string {
	b, _ := json.Marshal(res)
	return string(b)
}
