package skillsource

import (
	"archive/zip"
	"bytes"
	"os"
	"strings"
	"testing"
)

func TestZipSingleCandidate(t *testing.T) {
	data := buildZip(t, []tfile{
		txt("a/SKILL.md", skillMD("alpha", "desc", "a\n")),
		txt("a/foo.txt", "foo"),
	})
	res := zsrc(t, data)
	if len(res.Candidates) != 1 || res.Candidates[0].CandidateRoot != "a" || res.Candidates[0].SourceType != "archive" {
		t.Fatalf("unexpected: %+v", res.Candidates)
	}
}

func TestZipMultipleCandidates(t *testing.T) {
	data := buildZip(t, []tfile{
		txt("a/SKILL.md", skillMD("alpha", "", "a\n")),
		txt("a/a.txt", "a"),
		txt("b/SKILL.md", skillMD("beta", "", "b\n")),
		txt("b/b.txt", "b"),
	})
	res := zsrc(t, data)
	if len(res.Candidates) != 2 || res.Candidates[0].CandidateRoot != "a" || res.Candidates[1].CandidateRoot != "b" {
		t.Fatalf("unexpected: %+v", res.Candidates)
	}
}

func TestZipEntryOrderPermutation(t *testing.T) {
	a := buildZip(t, []tfile{
		txt("a/SKILL.md", skillMD("alpha", "", "a\n")),
		txt("a/x.txt", "x"),
		txt("b/SKILL.md", skillMD("beta", "", "b\n")),
	})
	b := buildZip(t, []tfile{
		txt("b/SKILL.md", skillMD("beta", "", "b\n")),
		txt("a/x.txt", "x"),
		txt("a/SKILL.md", skillMD("alpha", "", "a\n")),
	})
	ra := zsrc(t, a)
	rb := zsrc(t, b)
	if flatten(ra) != flatten(rb) {
		t.Fatalf("entry order must not change the result:\n%q\nvs\n%q", flatten(ra), flatten(rb))
	}
}

func TestZipSafeUnattachedIgnored(t *testing.T) {
	data := buildZip(t, []tfile{
		txt("a/SKILL.md", skillMD("alpha", "", "a\n")),
		txt("a/a.txt", "a"),
		txt("README.md", "readme"),
	})
	res := zsrc(t, data)
	if len(res.Candidates) != 1 || strings.Join(canonicalOf(t, res.Candidates[0]), ",") != "SKILL.md,a.txt" {
		t.Fatalf("unattached file leaked: %+v", res.Candidates)
	}
}

func TestZipTraversalRejected(t *testing.T) {
	data := buildZip(t, []tfile{
		txt("a/SKILL.md", skillMD("alpha", "", "a\n")),
		txt("a/../evil", "x"),
	})
	_, err := PrepareZip(data, DefaultLimits())
	wantErrIs(t, err, ErrUnsafePath)
}

func TestZipAbsolutePathRejected(t *testing.T) {
	data := buildZip(t, []tfile{
		txt("a/SKILL.md", skillMD("alpha", "", "a\n")),
		txt("/etc/passwd", "x"),
	})
	_, err := PrepareZip(data, DefaultLimits())
	wantErrIs(t, err, ErrUnsafePath)
}

func TestZipDotDotRejected(t *testing.T) {
	for _, name := range []string{"../x", "a/../../x", "./x"} {
		data := buildZip(t, []tfile{
			txt("a/SKILL.md", skillMD("alpha", "", "a\n")),
			txt(name, "x"),
		})
		if _, err := PrepareZip(data, DefaultLimits()); err == nil {
			t.Fatalf("path %q must be rejected", name)
		}
	}
}

func TestZipWindowsDriveRejected(t *testing.T) {
	data := buildZip(t, []tfile{
		txt("a/SKILL.md", skillMD("alpha", "", "a\n")),
		txt("C:/windows/system32/evil", "x"),
	})
	_, err := PrepareZip(data, DefaultLimits())
	wantErrIs(t, err, ErrUnsafePath)
}

func TestZipDuplicatePathRejected(t *testing.T) {
	data := buildZip(t, []tfile{
		txt("a/SKILL.md", skillMD("alpha", "", "a\n")),
		txt("a/dup.txt", "one"),
		txt("a/dup.txt", "two"),
	})
	_, err := PrepareZip(data, DefaultLimits())
	wantErrIs(t, err, ErrDuplicatePath)
}

func TestZipCaseCollisionRejected(t *testing.T) {
	data := buildZip(t, []tfile{
		txt("a/SKILL.md", skillMD("alpha", "", "a\n")),
		txt("a/x.txt", "x"),
		txt("A/x.txt", "y"),
	})
	_, err := PrepareZip(data, DefaultLimits())
	wantErrIs(t, err, ErrCaseCollision)
}

func TestZipSymlinkEntryRejected(t *testing.T) {
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	h := &zip.FileHeader{Name: "a/SKILL.md", Method: zip.Store}
	w, err := zw.CreateHeader(h)
	if err != nil {
		t.Fatal(err)
	}
	w.Write([]byte(skillMD("alpha", "", "a\n")))
	// A symlink entry: mode bit set, no regular content.
	sh := &zip.FileHeader{Name: "a/link", Method: zip.Store}
	sh.SetMode(os.ModeSymlink | 0o777)
	if _, err := zw.CreateHeader(sh); err != nil {
		t.Fatal(err)
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	_, err = PrepareZip(buf.Bytes(), DefaultLimits())
	wantErrIs(t, err, ErrUnsupportedType)
}

func TestZipOversizedFileRejected(t *testing.T) {
	data := buildZip(t, []tfile{
		txt("a/SKILL.md", skillMD("alpha", "", "a\n")),
		txt("a/big.txt", "0123456789"),
	})
	lim := DefaultLimits()
	lim.Package.MaxSingleFileBytes = 4
	_, err := PrepareZip(data, lim)
	wantErrIs(t, err, ErrLimit)
}

func TestZipExpandedTotalLimit(t *testing.T) {
	data := buildZip(t, []tfile{
		txt("a/SKILL.md", skillMD("alpha", "", "a\n")),
		txt("a/a.txt", "aaa"),
		txt("b/SKILL.md", skillMD("beta", "", "b\n")),
		txt("b/b.txt", "bbb"),
	})
	lim := DefaultLimits()
	lim.MaxExpandedBytes = 10
	_, err := PrepareZip(data, lim)
	wantErrIs(t, err, ErrLimit)
}

func TestZipEntryCountLimit(t *testing.T) {
	data := buildZip(t, []tfile{
		txt("a/SKILL.md", skillMD("alpha", "", "a\n")),
		txt("a/a.txt", "a"),
	})
	lim := DefaultLimits()
	lim.MaxEntries = 1
	_, err := PrepareZip(data, lim)
	wantErrIs(t, err, ErrLimit)
}

func TestZipArchiveBytesLimit(t *testing.T) {
	data := buildZip(t, []tfile{
		txt("a/SKILL.md", skillMD("alpha", "", "a\n")),
	})
	lim := DefaultLimits()
	lim.MaxArchiveBytes = 4
	_, err := PrepareZip(data, lim)
	wantErrIs(t, err, ErrLimit)
}

func TestZipTruncatedRejected(t *testing.T) {
	data := buildZip(t, []tfile{
		txt("a/SKILL.md", skillMD("alpha", "", "a\n")),
		txt("a/foo.txt", "some content here"),
	})
	for _, cut := range []int{1, 5, 20} {
		if _, err := PrepareZip(data[:len(data)-cut], DefaultLimits()); err == nil {
			t.Fatalf("truncated zip (cut %d) must be rejected", cut)
		}
	}
}

func TestZipCorruptRejected(t *testing.T) {
	data := buildZip(t, []tfile{
		txt("a/SKILL.md", skillMD("alpha", "", "a\n")),
		txt("a/foo.txt", "01234567890123456789"),
	})
	corrupt := append([]byte(nil), data...)
	corrupt[len(corrupt)/2] ^= 0xFF
	_, err := PrepareZip(corrupt, DefaultLimits())
	wantErrIs(t, err, ErrInvalidArchive)
}

func TestZipDuplicateCanonicalNameRejected(t *testing.T) {
	data := buildZip(t, []tfile{
		txt("a/SKILL.md", skillMD("My-Skill", "", "a\n")),
		txt("b/SKILL.md", skillMD("my-skill", "", "b\n")),
	})
	_, err := PrepareZip(data, DefaultLimits())
	wantErrIs(t, err, ErrDuplicateName)
}

func TestZipDirectoryEntriesSkipped(t *testing.T) {
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	// Explicit directory entry (trailing slash) must be skipped, not rejected.
	if _, err := zw.Create("a/"); err != nil {
		t.Fatal(err)
	}
	w, err := zw.Create("a/SKILL.md")
	if err != nil {
		t.Fatal(err)
	}
	w.Write([]byte(skillMD("alpha", "", "a\n")))
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	res := zsrc(t, buf.Bytes())
	if len(res.Candidates) != 1 {
		t.Fatalf("directory entry must be skipped, got %+v", res)
	}
}
