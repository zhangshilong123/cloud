package skillruntime

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/wanglongan587/cloud/internal/skillpkg"
	"github.com/wanglongan587/cloud/internal/skillstore"
)

// skillFixture builds a real canonical package through the single codec and returns its bundle and the
// content total that mirrors skill_revisions.size_bytes.
func skillFixture(t *testing.T) (*skillpkg.Bundle, int64) {
	t.Helper()
	files := []skillpkg.SourceFile{
		{Path: "SKILL.md", Data: []byte("# Test Skill\n\nA test skill used by materialization tests.\n")},
		{Path: "docs/guide.md", Data: []byte("Guide content.\n")},
		{Path: "scripts/run.sh", Data: []byte("#!/bin/sh\necho ok\n")},
	}
	b, err := skillpkg.Build(files, skillpkg.DefaultLimits())
	if err != nil {
		t.Fatalf("build skill: %v", err)
	}
	var total uint64
	for _, e := range b.Entries {
		total += e.Size
	}
	return b, int64(total)
}

// frozen returns a fully-consistent FrozenSkillBundle for a real bundle.
func frozen(b *skillpkg.Bundle, sizeBytes int64) FrozenSkillBundle {
	return FrozenSkillBundle{
		SkillRevisionID:   "rev-test",
		ContentDigestAlgo: skillstore.AlgorithmSHA256,
		ContentDigest:     b.TreeDigestHex(),
		PackageDigestAlgo: skillstore.AlgorithmSHA256,
		PackageDigest:     skillstore.PackageDigestHex(b.Package),
		PackageFormat:     skillpkg.FormatName,
		PackageFormatVer:  skillpkg.FormatVersion,
		SizeBytes:         sizeBytes,
	}
}

func capability(u string) skillstore.RetrievalCapability {
	return skillstore.RetrievalCapability{URL: u, Method: "GET", ExpiresAt: time.Now().Add(time.Hour)}
}

func newTestMaterializer(t *testing.T) *Materializer {
	t.Helper()
	return newTestMaterializerLimits(t, skillpkg.Limits{})
}

func newTestMaterializerLimits(t *testing.T, limits skillpkg.Limits) *Materializer {
	t.Helper()
	m, err := New(Config{CacheRoot: t.TempDir(), Limits: limits})
	if err != nil {
		t.Fatalf("new materializer: %v", err)
	}
	return m
}

// serve is a convenience server that answers with the given package bytes.
func servePackage(t *testing.T, pkg []byte, requests *int64) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if requests != nil {
			atomic.AddInt64(requests, 1)
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(pkg)
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestEnsureVerified_MaterializesTreeAtomically(t *testing.T) {
	bundle, sizeBytes := skillFixture(t)
	m := newTestMaterializer(t)
	srv := servePackage(t, bundle.Package, nil)

	vb, err := m.EnsureVerified(context.Background(), frozen(bundle, sizeBytes), capability(srv.URL))
	if err != nil {
		t.Fatalf("EnsureVerified: %v", err)
	}
	if vb.ContentDigest != bundle.TreeDigestHex() {
		t.Fatalf("content digest = %q, want %q", vb.ContentDigest, bundle.TreeDigestHex())
	}
	if vb.DigestAlgo != skillstore.AlgorithmSHA256 {
		t.Fatalf("digest algo = %q, want %q", vb.DigestAlgo, skillstore.AlgorithmSHA256)
	}

	// The published entry is the exact expanded canonical tree: every entry is a regular file (never a
	// symlink) whose bytes equal the verified per-file digest.
	for _, e := range bundle.Entries {
		dst := filepath.Join(vb.CacheDir, filepath.FromSlash(e.Path))
		fi, err := os.Lstat(dst)
		if err != nil {
			t.Fatalf("lstat %q: %v", e.Path, err)
		}
		if !fi.Mode().IsRegular() {
			t.Fatalf("entry %q is not a regular file (mode %v); symlinks/devices must never be created", e.Path, fi.Mode())
		}
		data, err := os.ReadFile(dst)
		if err != nil {
			t.Fatalf("read %q: %v", e.Path, err)
		}
		if sha256.Sum256(data) != e.SHA256 {
			t.Fatalf("on-disk bytes for %q do not match the verified digest", e.Path)
		}
	}

	// A verified marker attests the entry; it records format/digest identity and never a capability.
	if _, err := readMarker(vb.CacheDir); err != nil {
		t.Fatalf("read marker: %v", err)
	}
}

func TestEnsureVerified_CacheHitAvoidsObjectStorage(t *testing.T) {
	bundle, sizeBytes := skillFixture(t)
	m := newTestMaterializer(t)
	var requests int64
	srv := servePackage(t, bundle.Package, &requests)

	fb := frozen(bundle, sizeBytes)
	first, err := m.EnsureVerified(context.Background(), fb, capability(srv.URL))
	if err != nil {
		t.Fatalf("first EnsureVerified: %v", err)
	}
	if atomic.LoadInt64(&requests) != 1 {
		t.Fatalf("expected 1 download, got %d", requests)
	}

	// Close the server: a second call for the same content digest must be served from cache without
	// any network access.
	srv.Close()
	second, err := m.EnsureVerified(context.Background(), fb, capability(srv.URL))
	if err != nil {
		t.Fatalf("second EnsureVerified should have hit cache: %v", err)
	}
	if second.CacheDir != first.CacheDir {
		t.Fatalf("cache dir changed on hit: %q vs %q", second.CacheDir, first.CacheDir)
	}
	if atomic.LoadInt64(&requests) != 1 {
		t.Fatalf("cache hit triggered a download: %d requests", requests)
	}
}

func TestRetrieve_StatusMapping(t *testing.T) {
	cases := []struct {
		status int
		want   error
	}{
		{http.StatusUnauthorized, ErrRetrievalUnauthorized},
		{http.StatusForbidden, ErrRetrievalUnauthorized},
		{http.StatusNotFound, ErrRetrievalNotFound},
		{http.StatusFound, ErrRetrievalRedirect},
		{http.StatusTooManyRequests, ErrRetrievalTransient},
		{http.StatusInternalServerError, ErrRetrievalTransient},
		{http.StatusServiceUnavailable, ErrRetrievalTransient},
		{http.StatusTeapot, ErrRetrievalTransient}, // unlisted 4xx fails closed as transient, never success
	}
	for _, tc := range cases {
		t.Run(fmt.Sprintf("status_%d", tc.status), func(t *testing.T) {
			bundle, sizeBytes := skillFixture(t)
			m := newTestMaterializer(t)
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(tc.status)
			}))
			t.Cleanup(srv.Close)

			_, err := m.EnsureVerified(context.Background(), frozen(bundle, sizeBytes), capability(srv.URL))
			if !errors.Is(err, tc.want) {
				t.Fatalf("err = %v, want errors.Is(%v)", err, tc.want)
			}
		})
	}
}

func TestRetrieve_ExpiredCapability(t *testing.T) {
	bundle, sizeBytes := skillFixture(t)
	m := newTestMaterializer(t)
	var requests int64
	srv := servePackage(t, bundle.Package, &requests)

	expired := skillstore.RetrievalCapability{URL: srv.URL, Method: "GET", ExpiresAt: time.Now().Add(-time.Second)}
	_, err := m.EnsureVerified(context.Background(), frozen(bundle, sizeBytes), expired)
	if !errors.Is(err, ErrRetrievalUnauthorized) {
		t.Fatalf("err = %v, want ErrRetrievalUnauthorized", err)
	}
	if atomic.LoadInt64(&requests) != 0 {
		t.Fatalf("expired capability must not issue a request, got %d", requests)
	}
}

func TestRetrieve_Oversize(t *testing.T) {
	bundle, sizeBytes := skillFixture(t)
	m := newTestMaterializerLimits(t, skillpkg.Limits{MaxPackageBytes: 8})
	srv := servePackage(t, bundle.Package, nil)

	_, err := m.EnsureVerified(context.Background(), frozen(bundle, sizeBytes), capability(srv.URL))
	if !errors.Is(err, ErrSizeMismatch) {
		t.Fatalf("err = %v, want ErrSizeMismatch", err)
	}
}

func TestIntegrity_PackageDigestMismatch(t *testing.T) {
	bundle, sizeBytes := skillFixture(t)
	m := newTestMaterializer(t)

	tampered := append([]byte(nil), bundle.Package...)
	tampered[0] ^= 0xff
	srv := servePackage(t, tampered, nil)

	_, err := m.EnsureVerified(context.Background(), frozen(bundle, sizeBytes), capability(srv.URL))
	if !errors.Is(err, ErrPackageDigestMismatch) {
		t.Fatalf("err = %v, want ErrPackageDigestMismatch", err)
	}
}

func TestIntegrity_ContentDigestMismatch(t *testing.T) {
	bundle, sizeBytes := skillFixture(t)
	m := newTestMaterializer(t)
	srv := servePackage(t, bundle.Package, nil)

	// The bytes and package_digest are correct; the frozen content_digest is inconsistent. The runtime
	// must detect the mismatch only after decoding — never substitute the package_digest check for it.
	fb := frozen(bundle, sizeBytes)
	fb.ContentDigest = "0000000000000000000000000000000000000000000000000000000000000000"
	_, err := m.EnsureVerified(context.Background(), fb, capability(srv.URL))
	if !errors.Is(err, ErrContentDigestMismatch) {
		t.Fatalf("err = %v, want ErrContentDigestMismatch", err)
	}
}

func TestIntegrity_SizeMismatch(t *testing.T) {
	bundle, sizeBytes := skillFixture(t)
	m := newTestMaterializer(t)
	srv := servePackage(t, bundle.Package, nil)

	fb := frozen(bundle, sizeBytes)
	fb.SizeBytes = sizeBytes + 1
	_, err := m.EnsureVerified(context.Background(), fb, capability(srv.URL))
	if !errors.Is(err, ErrSizeMismatch) {
		t.Fatalf("err = %v, want ErrSizeMismatch", err)
	}
}

func TestIntegrity_DecodeFailed(t *testing.T) {
	m := newTestMaterializer(t)
	garbage := []byte("not a skill package at all")
	srv := servePackage(t, garbage, nil)

	// The frozen package_digest is self-consistent for the garbage bytes, so the byte-level check
	// passes; the canonical decode must then fail closed on the corrupt container.
	bundle, sizeBytes := skillFixture(t)
	fb := frozen(bundle, sizeBytes)
	fb.PackageDigest = skillstore.PackageDigestHex(garbage)

	_, err := m.EnsureVerified(context.Background(), fb, capability(srv.URL))
	if !errors.Is(err, ErrDecodeFailed) {
		t.Fatalf("err = %v, want ErrDecodeFailed", err)
	}
}

func TestConcurrent_CoalescesPopulation(t *testing.T) {
	bundle, sizeBytes := skillFixture(t)
	m := newTestMaterializer(t)
	var requests int64
	srv := servePackage(t, bundle.Package, &requests)

	fb := frozen(bundle, sizeBytes)
	const n = 16
	var wg sync.WaitGroup
	errs := make([]error, n)
	dirs := make([]string, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			vb, err := m.EnsureVerified(context.Background(), fb, capability(srv.URL))
			errs[i] = err
			if vb != nil {
				dirs[i] = vb.CacheDir
			}
		}(i)
	}
	wg.Wait()

	for i, err := range errs {
		if err != nil {
			t.Fatalf("goroutine %d: %v", i, err)
		}
		if dirs[i] != dirs[0] {
			t.Fatalf("goroutine %d cache dir %q != %q", i, dirs[i], dirs[0])
		}
	}
	if got := atomic.LoadInt64(&requests); got != 1 {
		t.Fatalf("single-flight failed: %d downloads for %d concurrent callers", got, n)
	}
}

func TestCorruptEntry_FailsClosed(t *testing.T) {
	bundle, sizeBytes := skillFixture(t)
	m := newTestMaterializer(t)
	var requests int64
	srv := servePackage(t, bundle.Package, &requests)

	// Pre-create a present-but-invalid entry: the directory exists but its marker attests a different
	// content digest. The runtime must fail closed and never re-download over it.
	entryDir := m.entryDirFor(bundle.TreeDigestHex())
	if err := os.MkdirAll(entryDir, 0o755); err != nil {
		t.Fatalf("mkdir entry: %v", err)
	}
	bad, _ := json.Marshal(entryMarker{
		Format:        skillpkg.FormatName,
		FormatVersion: skillpkg.FormatVersion,
		DigestAlgo:    skillstore.AlgorithmSHA256,
		ContentDigest: "actually-something-else",
	})
	if err := os.WriteFile(filepath.Join(entryDir, markerFileName), bad, 0o644); err != nil {
		t.Fatalf("write bad marker: %v", err)
	}

	_, err := m.EnsureVerified(context.Background(), frozen(bundle, sizeBytes), capability(srv.URL))
	if !errors.Is(err, ErrCacheCorrupt) {
		t.Fatalf("err = %v, want ErrCacheCorrupt", err)
	}
	if atomic.LoadInt64(&requests) != 0 {
		t.Fatalf("corrupt entry must fail closed without re-downloading, got %d requests", requests)
	}
}

func TestNoSecretInError(t *testing.T) {
	m := newTestMaterializer(t)
	secret := "X-Amz-Signature=deadbeef&X-Amz-Credential=secretkey"

	// Expired capability: the error must never leak the query string.
	expired := skillstore.RetrievalCapability{
		URL:       "https://example.invalid/obj?" + secret,
		Method:    "GET",
		ExpiresAt: time.Now().Add(-time.Second),
	}
	_, err := m.EnsureVerified(context.Background(), FrozenSkillBundle{
		SkillRevisionID:   "rev",
		ContentDigestAlgo: skillstore.AlgorithmSHA256,
		ContentDigest:     "0000000000000000000000000000000000000000000000000000000000000000",
		PackageDigestAlgo: skillstore.AlgorithmSHA256,
		PackageDigest:     "0000000000000000000000000000000000000000000000000000000000000000",
		PackageFormat:     skillpkg.FormatName,
		PackageFormatVer:  skillpkg.FormatVersion,
	}, expired)
	if err == nil {
		t.Fatal("expected an error")
	}
	if got := err.Error(); strings.Contains(got, "deadbeef") || strings.Contains(got, "secretkey") || strings.Contains(got, "X-Amz-Signature") {
		t.Fatalf("error leaked capability secret: %q", got)
	}
}

func TestValidateBundle_FailClosed(t *testing.T) {
	base := func() FrozenSkillBundle {
		bundle, sizeBytes := skillFixture(t)
		return frozen(bundle, sizeBytes)
	}
	cases := []struct {
		name   string
		mutate func(*FrozenSkillBundle)
		want   error
	}{
		{"bad content algo", func(b *FrozenSkillBundle) { b.ContentDigestAlgo = "md5" }, ErrUnsupportedDigestAlgorithm},
		{"bad package algo", func(b *FrozenSkillBundle) { b.PackageDigestAlgo = "sha1" }, ErrUnsupportedDigestAlgorithm},
		{"bad format", func(b *FrozenSkillBundle) { b.PackageFormat = "zip" }, ErrUnsupportedPackageFormat},
		{"bad format version", func(b *FrozenSkillBundle) { b.PackageFormatVer = 2 }, ErrUnsupportedPackageFormat},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			b := base()
			tc.mutate(&b)
			if err := validateBundle(&b); !errors.Is(err, tc.want) {
				t.Fatalf("err = %v, want errors.Is(%v)", err, tc.want)
			}
		})
	}
}

func TestReservedMarkerNameRejected(t *testing.T) {
	// A Skill whose canonical tree contains the root-level reserved marker name must fail closed
	// (expandStaging), not silently have its file overwritten by the verified marker.
	files := []skillpkg.SourceFile{
		{Path: markerFileName, Data: []byte("would collide with the marker\n")},
		{Path: "SKILL.md", Data: []byte("# Colliding\n")},
	}
	bundle, err := skillpkg.Build(files, skillpkg.DefaultLimits())
	if err != nil {
		t.Fatalf("build skill: %v", err)
	}
	var total uint64
	for _, e := range bundle.Entries {
		total += e.Size
	}

	m := newTestMaterializer(t)
	srv := servePackage(t, bundle.Package, nil)
	_, err = m.EnsureVerified(context.Background(), frozen(bundle, int64(total)), capability(srv.URL))
	if !errors.Is(err, ErrCachePublish) {
		t.Fatalf("err = %v, want ErrCachePublish", err)
	}
}
