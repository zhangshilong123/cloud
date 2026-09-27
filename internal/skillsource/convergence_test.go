package skillsource

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/wanglongan587/cloud/internal/skillpkg"
	"github.com/wanglongan587/cloud/internal/skillstore"
)

// writeTreeFiles writes ordered entries to a fresh temp directory and returns its root.
func writeTreeFiles(t *testing.T, files []tfile) string {
	t.Helper()
	root := t.TempDir()
	for _, f := range files {
		p := filepath.Join(root, filepath.FromSlash(f.name))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", f.name, err)
		}
		if err := os.WriteFile(p, f.data, 0o644); err != nil {
			t.Fatalf("write %s: %v", f.name, err)
		}
	}
	return root
}

// TestConvergenceGolden asserts that the same logical Skill tree read through directory,
// ZIP, and TAR transports produces byte-identical canonical identity: the same
// candidate-relative paths, exact bytes, canonical_name, skillpkg manifest, content
// digest, package bytes, and package digest (ADR D3 / §25).
func TestConvergenceGolden(t *testing.T) {
	fixture := []tfile{
		txt("a/SKILL.md", skillMD("Converge", "a converged skill", "body\n")),
		txt("a/plain.txt", "hello world\n"),
		raw("a/nested/blob.bin", []byte{0x00, 0x01, 0x02, 0xFE, 0xFF, 0x0A, 0x0D, 0x80}),
	}

	dirRes := dir(t, writeTreeFiles(t, fixture))
	zipRes := zsrc(t, buildZip(t, fixture))
	tarRes := tsrc(t, buildTar(t, fixture))

	if len(dirRes.Candidates) != 1 || len(zipRes.Candidates) != 1 || len(tarRes.Candidates) != 1 {
		t.Fatalf("want 1 candidate per transport, got %d/%d/%d",
			len(dirRes.Candidates), len(zipRes.Candidates), len(tarRes.Candidates))
	}

	dir := convergeOf(t, dirRes.Candidates[0])
	zip := convergeOf(t, zipRes.Candidates[0])
	tar := convergeOf(t, tarRes.Candidates[0])

	if dir.CanonicalName != "converge" || zip.CanonicalName != "converge" || tar.CanonicalName != "converge" {
		t.Fatalf("canonical_name diverged: %q/%q/%q", dir.CanonicalName, zip.CanonicalName, tar.CanonicalName)
	}
	if dir.ContentDigest != zip.ContentDigest || zip.ContentDigest != tar.ContentDigest {
		t.Fatalf("content_digest diverged: %q/%q/%q", dir.ContentDigest, zip.ContentDigest, tar.ContentDigest)
	}
	if dir.PackageDigest != zip.PackageDigest || zip.PackageDigest != tar.PackageDigest {
		t.Fatalf("package_digest diverged: %q/%q/%q", dir.PackageDigest, zip.PackageDigest, tar.PackageDigest)
	}
	if !equalBytes(dir.Manifest, zip.Manifest) || !equalBytes(zip.Manifest, tar.Manifest) {
		t.Fatalf("manifest diverged across transports")
	}
	if !equalBytes(dir.Package, zip.Package) || !equalBytes(zip.Package, tar.Package) {
		t.Fatalf("package bytes diverged across transports")
	}
	if flatten(dirRes) != flatten(zipRes) || flatten(zipRes) != flatten(tarRes) {
		t.Fatalf("candidate tree diverged across transports")
	}
}

// convergence is one transport's canonical identity for a candidate.
type convergence struct {
	CanonicalName string
	Manifest      []byte
	ContentDigest string
	Package       []byte
	PackageDigest string
}

func convergeOf(t *testing.T, c PreparedCandidate) convergence {
	t.Helper()
	b, err := skillpkg.Build(c.Files, skillpkg.DefaultLimits())
	if err != nil {
		t.Fatalf("build candidate %q: %v", c.CandidateRoot, err)
	}
	return convergence{
		CanonicalName: c.CanonicalName,
		Manifest:      b.Manifest,
		ContentDigest: b.TreeDigestHex(),
		Package:       b.Package,
		PackageDigest: skillstore.PackageDigestHex(b.Package),
	}
}

func equalBytes(a, b []byte) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
