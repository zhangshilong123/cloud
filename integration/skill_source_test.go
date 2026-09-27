package integration

// Source intake → candidate discovery → ingestion saga integration tests. These drive
// skillsource.Prepare* then core.Store.IngestSource against an isolated PostgreSQL schema
// with an in-memory fakestore.ObjectStore, pinning the source-intake boundary
// (specs/decisions/cloud/skills/20260927-source-intake-candidate-discovery.md):
//
//   - a prepared directory source maps to the existing IngestSkills saga and commits;
//   - an archive source (ZIP) commits identically through the same saga;
//   - source-level structural failures (nested roots, duplicate canonical_name, unsafe
//     archive path) produce zero skills / skill_revisions / skill_ingestions rows;
//   - an invalid-metadata candidate becomes a PreparationFailure with no ingestion row
//     while valid siblings still commit.

import (
	"archive/zip"
	"bytes"
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/wanglongan587/cloud/internal/core"
	"github.com/wanglongan587/cloud/internal/skillsource"
)

// srcMD builds a SKILL.md frontmatter for a source fixture.
func srcMD(name, description, body string) string {
	md := "---\nname: " + name + "\n"
	if description != "" {
		md += "description: " + description + "\n"
	}
	md += "---\n" + body
	return md
}

// writeSourceDir writes path→content entries under a fresh temp directory.
func writeSourceDir(t *testing.T, files map[string]string) string {
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

// buildSourceZip builds a ZIP archive from path→content entries.
func buildSourceZip(t *testing.T, files map[string]string) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for name, content := range files {
		w, err := zw.Create(name)
		if err != nil {
			t.Fatalf("zip create %s: %v", name, err)
		}
		if _, err := w.Write([]byte(content)); err != nil {
			t.Fatalf("zip write %s: %v", name, err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatalf("zip close: %v", err)
	}
	return buf.Bytes()
}

func assertZeroSkillRows(t *testing.T, store *core.Store) {
	t.Helper()
	var skills, revisions, ingestions int
	must(t, store.Pool.QueryRow(`SELECT (SELECT count(*) FROM skills), (SELECT count(*) FROM skill_revisions), (SELECT count(*) FROM skill_ingestions)`).Scan(&skills, &revisions, &ingestions))
	if skills != 0 || revisions != 0 || ingestions != 0 {
		t.Fatalf("structural failure must leave zero rows, got skills=%d revisions=%d ingestions=%d", skills, revisions, ingestions)
	}
}

func TestSkillSourceDirectoryIngest(t *testing.T) {
	store, _, user, ws := skillIngestFixture(t)
	root := writeSourceDir(t, map[string]string{
		"a/SKILL.md": srcMD("alpha", "desc", "body\n"),
		"a/guide.md": "read me",
	})

	prepared, err := skillsource.PrepareDirectory(root, skillsource.DefaultLimits())
	if err != nil {
		t.Fatalf("prepare: %v", err)
	}
	if len(prepared.Candidates) != 1 {
		t.Fatalf("want 1 candidate, got %d", len(prepared.Candidates))
	}

	out, err := store.IngestSource(context.Background(), ws, user, "src-key", prepared, core.SourceIngestParams{})
	if err != nil {
		t.Fatalf("ingest: %v", err)
	}
	if len(out.Ingestions) != 1 || out.Ingestions[0].State != "committed" || out.Ingestions[0].Activation != "activated" {
		t.Fatalf("unexpected ingestion results: %+v", out.Ingestions)
	}

	var skills, revisions, ingestions int
	var sourceType string
	must(t, store.Pool.QueryRow(`SELECT (SELECT count(*) FROM skills), (SELECT count(*) FROM skill_revisions), (SELECT count(*) FROM skill_ingestions), (SELECT source_type FROM skill_ingestions LIMIT 1)`).Scan(&skills, &revisions, &ingestions, &sourceType))
	if skills != 1 || revisions != 1 || ingestions != 1 {
		t.Fatalf("want 1/1/1 rows, got %d/%d/%d", skills, revisions, ingestions)
	}
	if sourceType != "directory" {
		t.Fatalf("source_type must be directory, got %q", sourceType)
	}
}

func TestSkillSourceArchiveIngest(t *testing.T) {
	store, _, user, ws := skillIngestFixture(t)
	data := buildSourceZip(t, map[string]string{
		"a/SKILL.md": srcMD("alpha", "desc", "body\n"),
		"a/guide.md": "read me",
	})

	prepared, err := skillsource.PrepareZip(data, skillsource.DefaultLimits())
	if err != nil {
		t.Fatalf("prepare zip: %v", err)
	}
	out, err := store.IngestSource(context.Background(), ws, user, "zip-key", prepared, core.SourceIngestParams{})
	if err != nil {
		t.Fatalf("ingest: %v", err)
	}
	if len(out.Ingestions) != 1 || out.Ingestions[0].State != "committed" || out.Ingestions[0].Activation != "activated" {
		t.Fatalf("unexpected: %+v", out.Ingestions)
	}

	var sourceType string
	must(t, store.Pool.QueryRow(`SELECT source_type FROM skill_ingestions LIMIT 1`).Scan(&sourceType))
	if sourceType != "archive" {
		t.Fatalf("source_type must be archive, got %q", sourceType)
	}
}

func TestSkillSourceNestedRootsZeroRows(t *testing.T) {
	store, _, _, _ := skillIngestFixture(t)
	root := writeSourceDir(t, map[string]string{
		"a/SKILL.md":   srcMD("outer", "", "o\n"),
		"a/b/SKILL.md": srcMD("inner", "", "i\n"),
	})
	if _, err := skillsource.PrepareDirectory(root, skillsource.DefaultLimits()); err == nil {
		t.Fatal("nested roots must fail preparation")
	}
	assertZeroSkillRows(t, store)
}

func TestSkillSourceDuplicateNameZeroRows(t *testing.T) {
	store, _, _, _ := skillIngestFixture(t)
	root := writeSourceDir(t, map[string]string{
		"a/SKILL.md": srcMD("My-Skill", "", "a\n"),
		"b/SKILL.md": srcMD("my-skill", "", "b\n"),
	})
	if _, err := skillsource.PrepareDirectory(root, skillsource.DefaultLimits()); err == nil {
		t.Fatal("duplicate canonical_name must fail preparation")
	}
	assertZeroSkillRows(t, store)
}

func TestSkillSourceUnsafeArchiveZeroRows(t *testing.T) {
	store, _, _, _ := skillIngestFixture(t)
	data := buildSourceZip(t, map[string]string{
		"a/SKILL.md":  srcMD("alpha", "", "a\n"),
		"../evil.txt": "escape",
	})
	if _, err := skillsource.PrepareZip(data, skillsource.DefaultLimits()); err == nil {
		t.Fatal("unsafe archive path must fail preparation")
	}
	assertZeroSkillRows(t, store)
}

func TestSkillSourcePartialMetadataPreparation(t *testing.T) {
	store, _, user, ws := skillIngestFixture(t)
	root := writeSourceDir(t, map[string]string{
		"a/SKILL.md": srcMD("alpha", "", "a\n"),
		"a/a.txt":    "a",
		"b/SKILL.md": "---\ndescription: no name\n---\nbody\n",
		"c/SKILL.md": srcMD("gamma", "", "c\n"),
		"c/c.txt":    "c",
	})

	prepared, err := skillsource.PrepareDirectory(root, skillsource.DefaultLimits())
	if err != nil {
		t.Fatalf("prepare: %v", err)
	}
	if len(prepared.Candidates) != 2 || len(prepared.PreparationFailures) != 1 {
		t.Fatalf("want 2 candidates + 1 failure, got %d/%d", len(prepared.Candidates), len(prepared.PreparationFailures))
	}
	if prepared.PreparationFailures[0].CandidateRoot != "b" {
		t.Fatalf("failure root: %+v", prepared.PreparationFailures[0])
	}

	out, err := store.IngestSource(context.Background(), ws, user, "batch-key", prepared, core.SourceIngestParams{})
	if err != nil {
		t.Fatalf("ingest: %v", err)
	}
	if len(out.PreparationFailures) != 1 || len(out.Ingestions) != 2 {
		t.Fatalf("unexpected result: failures=%d ingestions=%d", len(out.PreparationFailures), len(out.Ingestions))
	}

	var skills, revisions, ingestions int
	must(t, store.Pool.QueryRow(`SELECT (SELECT count(*) FROM skills), (SELECT count(*) FROM skill_revisions), (SELECT count(*) FROM skill_ingestions)`).Scan(&skills, &revisions, &ingestions))
	if skills != 2 || revisions != 2 || ingestions != 2 {
		t.Fatalf("only valid candidates create rows: want 2/2/2, got %d/%d/%d", skills, revisions, ingestions)
	}
}
