package skillsource

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// skillMD builds a SKILL.md frontmatter with the given name/description and body.
func skillMD(name, description, body string) string {
	md := "---\nname: " + name + "\n"
	if description != "" {
		md += "description: " + description + "\n"
	}
	md += "---\n" + body
	return md
}

// writeTree writes path→content entries under a fresh temp directory and returns its root.
func writeTree(t *testing.T, files map[string]string) string {
	t.Helper()
	root := t.TempDir()
	for name, content := range files {
		p := filepath.Join(root, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", name, err)
		}
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	return root
}

// tfile is one ordered archive entry for building deterministic zip/tar fixtures.
type tfile struct {
	name string
	data []byte
}

func txt(name, data string) tfile        { return tfile{name: name, data: []byte(data)} }
func raw(name string, data []byte) tfile { return tfile{name: name, data: data} }

// buildZip builds a ZIP archive from ordered entries.
func buildZip(t *testing.T, entries []tfile) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for _, e := range entries {
		w, err := zw.Create(e.name)
		if err != nil {
			t.Fatalf("zip create %s: %v", e.name, err)
		}
		if _, err := w.Write(e.data); err != nil {
			t.Fatalf("zip write %s: %v", e.name, err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatalf("zip close: %v", err)
	}
	return buf.Bytes()
}

// buildTar builds an uncompressed TAR archive from ordered entries.
func buildTar(t *testing.T, entries []tfile) []byte {
	t.Helper()
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	for _, e := range entries {
		if err := tw.WriteHeader(&tar.Header{Name: e.name, Mode: 0o644, Size: int64(len(e.data)), Typeflag: tar.TypeReg}); err != nil {
			t.Fatalf("tar header %s: %v", e.name, err)
		}
		if _, err := tw.Write(e.data); err != nil {
			t.Fatalf("tar write %s: %v", e.name, err)
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatalf("tar close: %v", err)
	}
	return buf.Bytes()
}

func wantErrIs(t *testing.T, err, target error) {
	t.Helper()
	if err == nil {
		t.Fatalf("expected error wrapping %v, got nil", target)
	}
	if !errors.Is(err, target) {
		t.Fatalf("expected error wrapping %v, got: %v", target, err)
	}
}

// dir/zsrc/tsrc prepare a source through one transport and fail the test on any
// structural error, keeping the call sites short.
func dir(t *testing.T, root string) *PreparedSourceResult {
	t.Helper()
	res, err := PrepareDirectory(root, DefaultLimits())
	if err != nil {
		t.Fatalf("PrepareDirectory: %v", err)
	}
	return res
}

func zsrc(t *testing.T, data []byte) *PreparedSourceResult {
	t.Helper()
	res, err := PrepareZip(data, DefaultLimits())
	if err != nil {
		t.Fatalf("PrepareZip: %v", err)
	}
	return res
}

func tsrc(t *testing.T, data []byte) *PreparedSourceResult {
	t.Helper()
	res, err := PrepareTar(data, DefaultLimits())
	if err != nil {
		t.Fatalf("PrepareTar: %v", err)
	}
	return res
}

// canonicalOf returns the candidate-relative paths of one candidate's file tree, in order.
func canonicalOf(t *testing.T, c PreparedCandidate) []string {
	t.Helper()
	paths := make([]string, 0, len(c.Files))
	for _, f := range c.Files {
		paths = append(paths, f.Path)
	}
	return paths
}

// flatten renders a PreparedSourceResult into a deterministic comparable string.
func flatten(res *PreparedSourceResult) string {
	var b strings.Builder
	for _, c := range res.Candidates {
		b.WriteString("CAND:" + c.CandidateRoot + ":" + c.CanonicalName + "\n")
		for _, f := range c.Files {
			b.WriteString(f.Path + ":" + string(f.Data) + "\n")
		}
	}
	for _, f := range res.PreparationFailures {
		b.WriteString("FAIL:" + f.CandidateRoot + ":" + f.ErrorCode + "\n")
	}
	return b.String()
}

func TestDirectorySingleRootCandidate(t *testing.T) {
	root := writeTree(t, map[string]string{
		"a/SKILL.md": skillMD("alpha", "desc", "body\n"),
		"a/foo.txt":  "read me",
	})
	res := dir(t, root)
	if len(res.Candidates) != 1 || len(res.PreparationFailures) != 0 {
		t.Fatalf("want 1 candidate 0 failures, got %d/%d", len(res.Candidates), len(res.PreparationFailures))
	}
	c := res.Candidates[0]
	if c.CandidateRoot != "a" || c.SourceType != "directory" || c.CanonicalName != "alpha" || c.PackageName != "alpha" || c.PackageDescription != "desc" {
		t.Fatalf("unexpected candidate: %+v", c)
	}
	if got := canonicalOf(t, c); strings.Join(got, ",") != "SKILL.md,foo.txt" {
		t.Fatalf("unexpected paths: %v", got)
	}
}

func TestDirectorySiblingCandidates(t *testing.T) {
	root := writeTree(t, map[string]string{
		"a/SKILL.md": skillMD("alpha", "", "a\n"),
		"a/a.txt":    "a",
		"b/SKILL.md": skillMD("beta", "", "b\n"),
		"b/b.txt":    "b",
	})
	res := dir(t, root)
	if len(res.Candidates) != 2 {
		t.Fatalf("want 2 candidates, got %d", len(res.Candidates))
	}
	if res.Candidates[0].CandidateRoot != "a" || res.Candidates[1].CandidateRoot != "b" {
		t.Fatalf("unexpected roots: %q %q", res.Candidates[0].CandidateRoot, res.Candidates[1].CandidateRoot)
	}
}

func TestDirectoryDeepCandidate(t *testing.T) {
	root := writeTree(t, map[string]string{
		"x/y/z/SKILL.md":   skillMD("deep", "", "d\n"),
		"x/y/z/nested/f.g": "f",
	})
	res := dir(t, root)
	if len(res.Candidates) != 1 || res.Candidates[0].CandidateRoot != "x/y/z" {
		t.Fatalf("unexpected: %+v", res.Candidates)
	}
	got := canonicalOf(t, res.Candidates[0])
	if strings.Join(got, ",") != "SKILL.md,nested/f.g" {
		t.Fatalf("unexpected candidate-relative paths: %v", got)
	}
}

func TestDirectorySafeUnattachedIgnored(t *testing.T) {
	root := writeTree(t, map[string]string{
		"a/SKILL.md": skillMD("alpha", "", "a\n"),
		"a/a.txt":    "a",
		"README.md":  "top-level readme",
		"LICENSE":    "MIT",
	})
	res := dir(t, root)
	if len(res.Candidates) != 1 {
		t.Fatalf("want 1 candidate, got %d", len(res.Candidates))
	}
	if got := canonicalOf(t, res.Candidates[0]); strings.Join(got, ",") != "SKILL.md,a.txt" {
		t.Fatalf("unattached files leaked into identity: %v", got)
	}
}

func TestDirectoryRootCandidate(t *testing.T) {
	root := writeTree(t, map[string]string{
		"SKILL.md":  skillMD("root", "", "r\n"),
		"guide.md":  "g",
		"README.md": "readme", // inside the root candidate subtree: NOT unattached
		"sub/x.txt": "x",
	})
	res := dir(t, root)
	if len(res.Candidates) != 1 || res.Candidates[0].CandidateRoot != "" {
		t.Fatalf("unexpected: %+v", res.Candidates)
	}
	got := canonicalOf(t, res.Candidates[0])
	if strings.Join(got, ",") != "README.md,SKILL.md,guide.md,sub/x.txt" {
		t.Fatalf("root candidate must own the whole tree: %v", got)
	}
}

func TestNestedRootsRejectSource(t *testing.T) {
	root := writeTree(t, map[string]string{
		"a/SKILL.md":   skillMD("outer", "", "o\n"),
		"a/b/SKILL.md": skillMD("inner", "", "i\n"),
	})
	_, err := PrepareDirectory(root, DefaultLimits())
	wantErrIs(t, err, ErrNestedRoot)
}

func TestNestedRootsRejectZip(t *testing.T) {
	data := buildZip(t, []tfile{
		txt("a/SKILL.md", skillMD("outer", "", "o\n")),
		txt("a/b/SKILL.md", skillMD("inner", "", "i\n")),
	})
	_, err := PrepareZip(data, DefaultLimits())
	wantErrIs(t, err, ErrNestedRoot)
}

func TestNoSkillMD(t *testing.T) {
	root := writeTree(t, map[string]string{
		"a.txt": "x",
		"b.txt": "y",
	})
	_, err := PrepareDirectory(root, DefaultLimits())
	wantErrIs(t, err, ErrNoCandidates)
}

func TestDeterministicCandidateOrdering(t *testing.T) {
	root := writeTree(t, map[string]string{
		"z/SKILL.md": skillMD("z", "", "z\n"),
		"a/SKILL.md": skillMD("a", "", "a\n"),
		"m/SKILL.md": skillMD("m", "", "m\n"),
	})
	res := dir(t, root)
	var roots []string
	for _, c := range res.Candidates {
		roots = append(roots, c.CandidateRoot)
	}
	if strings.Join(roots, ",") != "a,m,z" {
		t.Fatalf("candidate roots must be sorted by canonical bytes, got %v", roots)
	}
}

func TestDuplicateCanonicalNameRejectsSource(t *testing.T) {
	root := writeTree(t, map[string]string{
		"a/SKILL.md": skillMD("My-Skill", "", "a\n"),
		"b/SKILL.md": skillMD("my-skill", "", "b\n"),
	})
	_, err := PrepareDirectory(root, DefaultLimits())
	wantErrIs(t, err, ErrDuplicateName)
}

func TestInvalidMetadataBecomesPreparationFailure(t *testing.T) {
	root := writeTree(t, map[string]string{
		"a/SKILL.md": "---\ndescription: no name\n---\nbody\n",
	})
	res := dir(t, root)
	if len(res.Candidates) != 0 || len(res.PreparationFailures) != 1 {
		t.Fatalf("want 0 candidates + 1 failure, got %d/%d", len(res.Candidates), len(res.PreparationFailures))
	}
	f := res.PreparationFailures[0]
	if f.CandidateRoot != "a" || f.ErrorCode != "skill_md_missing_name" {
		t.Fatalf("unexpected failure: %+v", f)
	}
}

func TestInvalidMetadataDoesNotBlockSibling(t *testing.T) {
	root := writeTree(t, map[string]string{
		"a/SKILL.md": skillMD("alpha", "", "a\n"),
		"b/SKILL.md": "---\ndescription: broken\n---\nbody\n",
		"c/SKILL.md": skillMD("gamma", "", "c\n"),
	})
	res := dir(t, root)
	if len(res.Candidates) != 2 || len(res.PreparationFailures) != 1 {
		t.Fatalf("want 2 candidates + 1 failure, got %d/%d", len(res.Candidates), len(res.PreparationFailures))
	}
	if res.Candidates[0].CanonicalName != "alpha" || res.Candidates[1].CanonicalName != "gamma" {
		t.Fatalf("valid siblings must survive: %v", res.Candidates)
	}
	if res.PreparationFailures[0].CandidateRoot != "b" {
		t.Fatalf("failure root: %+v", res.PreparationFailures[0])
	}
}

func TestAllInvalidMetadataIsPreparationResult(t *testing.T) {
	// SKILL.md exists but every candidate's metadata is invalid: a normal preparation
	// result (zero candidates, N failures), never "no candidate" and never a durable error.
	root := writeTree(t, map[string]string{
		"a/SKILL.md": "---\ndescription: no name\n---\n",
		"b/SKILL.md": "---\ndescription: also broken\n---\n",
	})
	res := dir(t, root)
	if len(res.Candidates) != 0 || len(res.PreparationFailures) != 2 {
		t.Fatalf("want 0 candidates + 2 failures, got %d/%d", len(res.Candidates), len(res.PreparationFailures))
	}
}

func TestUnsafeUnattachedFileRejectsSource(t *testing.T) {
	// A file outside every candidate root with a traversal path must reject the whole
	// source even though it would be ignored for candidate construction.
	data := buildZip(t, []tfile{
		txt("a/SKILL.md", skillMD("alpha", "", "a\n")),
		txt("../evil.txt", "escape"),
	})
	_, err := PrepareZip(data, DefaultLimits())
	wantErrIs(t, err, ErrUnsafePath)
}

func TestUnsupportedSourceKind(t *testing.T) {
	if KindTar.SourceType() != "archive" || KindZip.SourceType() != "archive" || KindDirectory.SourceType() != "directory" {
		t.Fatalf("source_type mapping is wrong")
	}
}

func TestCandidateSourceFilesPreserveBytes(t *testing.T) {
	bin := []byte{0x00, 0x01, 0xFE, 0xFF, 0x0A, 0x0D, 0x80}
	root := writeTree(t, map[string]string{
		"a/SKILL.md": skillMD("alpha", "", "b\n"),
	})
	// write a binary file directly (writeTree uses string; write bytes here).
	p := filepath.Join(root, "a", "blob.bin")
	if err := os.WriteFile(p, bin, 0o644); err != nil {
		t.Fatal(err)
	}
	res := dir(t, root)
	for _, f := range res.Candidates[0].Files {
		if f.Path == "blob.bin" && !bytes.Equal(f.Data, bin) {
			t.Fatalf("binary bytes not preserved")
		}
	}
}

func TestCandidateCountLimit(t *testing.T) {
	files := map[string]string{}
	for _, r := range []string{"a", "b", "c"} {
		files[r+"/SKILL.md"] = skillMD(r, "", r+"\n")
	}
	root := writeTree(t, files)
	lim := DefaultLimits()
	lim.MaxCandidates = 2
	_, err := PrepareDirectory(root, lim)
	wantErrIs(t, err, ErrLimit)
}
