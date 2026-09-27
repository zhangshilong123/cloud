package skillsource

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"strings"
	"testing"
)

// buildTarTyped builds a tar with one custom-typed entry appended after regular entries.
func buildTarTyped(t *testing.T, entries []tfile, extra tar.Header) []byte {
	t.Helper()
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	for _, e := range entries {
		if err := tw.WriteHeader(&tar.Header{Name: e.name, Mode: 0o644, Size: int64(len(e.data)), Typeflag: tar.TypeReg}); err != nil {
			t.Fatal(err)
		}
		if _, err := tw.Write(e.data); err != nil {
			t.Fatal(err)
		}
	}
	if err := tw.WriteHeader(&extra); err != nil {
		t.Fatal(err)
	}
	if extra.Size > 0 {
		if _, err := tw.Write(make([]byte, extra.Size)); err != nil {
			t.Fatal(err)
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func TestTarSingleCandidate(t *testing.T) {
	data := buildTar(t, []tfile{
		txt("a/SKILL.md", skillMD("alpha", "desc", "a\n")),
		txt("a/foo.txt", "foo"),
	})
	res := tsrc(t, data)
	if len(res.Candidates) != 1 || res.Candidates[0].CandidateRoot != "a" || res.Candidates[0].SourceType != "archive" {
		t.Fatalf("unexpected: %+v", res.Candidates)
	}
}

func TestTarMultipleCandidates(t *testing.T) {
	data := buildTar(t, []tfile{
		txt("a/SKILL.md", skillMD("alpha", "", "a\n")),
		txt("a/a.txt", "a"),
		txt("b/SKILL.md", skillMD("beta", "", "b\n")),
		txt("b/b.txt", "b"),
	})
	res := tsrc(t, data)
	if len(res.Candidates) != 2 || res.Candidates[0].CandidateRoot != "a" || res.Candidates[1].CandidateRoot != "b" {
		t.Fatalf("unexpected: %+v", res.Candidates)
	}
}

func TestTarEntryOrderPermutation(t *testing.T) {
	a := buildTar(t, []tfile{
		txt("a/SKILL.md", skillMD("alpha", "", "a\n")),
		txt("a/x.txt", "x"),
		txt("b/SKILL.md", skillMD("beta", "", "b\n")),
	})
	b := buildTar(t, []tfile{
		txt("b/SKILL.md", skillMD("beta", "", "b\n")),
		txt("a/x.txt", "x"),
		txt("a/SKILL.md", skillMD("alpha", "", "a\n")),
	})
	ra := tsrc(t, a)
	rb := tsrc(t, b)
	if flatten(ra) != flatten(rb) {
		t.Fatalf("entry order must not change the result:\n%q\nvs\n%q", flatten(ra), flatten(rb))
	}
}

func TestTarSafeUnattachedIgnored(t *testing.T) {
	data := buildTar(t, []tfile{
		txt("a/SKILL.md", skillMD("alpha", "", "a\n")),
		txt("a/a.txt", "a"),
		txt("README.md", "readme"),
	})
	res := tsrc(t, data)
	if len(res.Candidates) != 1 || strings.Join(canonicalOf(t, res.Candidates[0]), ",") != "SKILL.md,a.txt" {
		t.Fatalf("unattached file leaked: %+v", res.Candidates)
	}
}

func TestTarTraversalRejected(t *testing.T) {
	data := buildTar(t, []tfile{
		txt("a/SKILL.md", skillMD("alpha", "", "a\n")),
		txt("a/../evil", "x"),
	})
	_, err := PrepareTar(data, DefaultLimits())
	wantErrIs(t, err, ErrUnsafePath)
}

func TestTarAbsolutePathRejected(t *testing.T) {
	data := buildTar(t, []tfile{
		txt("a/SKILL.md", skillMD("alpha", "", "a\n")),
		txt("/etc/passwd", "x"),
	})
	_, err := PrepareTar(data, DefaultLimits())
	wantErrIs(t, err, ErrUnsafePath)
}

func TestTarDuplicateAndCaseCollisionRejected(t *testing.T) {
	data := buildTar(t, []tfile{
		txt("a/SKILL.md", skillMD("alpha", "", "a\n")),
		txt("a/dup.txt", "one"),
		txt("a/dup.txt", "two"),
	})
	_, err := PrepareTar(data, DefaultLimits())
	wantErrIs(t, err, ErrDuplicatePath)

	data = buildTar(t, []tfile{
		txt("a/SKILL.md", skillMD("alpha", "", "a\n")),
		txt("a/x.txt", "x"),
		txt("A/x.txt", "y"),
	})
	_, err = PrepareTar(data, DefaultLimits())
	wantErrIs(t, err, ErrCaseCollision)
}

func TestTarSpecialTypesRejected(t *testing.T) {
	base := []tfile{txt("a/SKILL.md", skillMD("alpha", "", "a\n"))}
	for name, hdr := range map[string]tar.Header{
		"symlink":  {Name: "a/link", Typeflag: tar.TypeSymlink, Linkname: "SKILL.md"},
		"hardlink": {Name: "a/hard", Typeflag: tar.TypeLink, Linkname: "SKILL.md"},
		"char":     {Name: "a/dev", Typeflag: tar.TypeChar},
		"block":    {Name: "a/dev", Typeflag: tar.TypeBlock},
		"fifo":     {Name: "a/fifo", Typeflag: tar.TypeFifo},
	} {
		data := buildTarTyped(t, base, hdr)
		if _, err := PrepareTar(data, DefaultLimits()); err == nil {
			t.Fatalf("%s entry must be rejected", name)
		}
	}
}

func TestTarOversizedFileRejected(t *testing.T) {
	data := buildTar(t, []tfile{
		txt("a/SKILL.md", skillMD("alpha", "", "a\n")),
		txt("a/big.txt", "0123456789"),
	})
	lim := DefaultLimits()
	lim.Package.MaxSingleFileBytes = 4
	_, err := PrepareTar(data, lim)
	wantErrIs(t, err, ErrLimit)
}

func TestTarExpandedTotalAndEntryCountLimits(t *testing.T) {
	data := buildTar(t, []tfile{
		txt("a/SKILL.md", skillMD("alpha", "", "a\n")),
		txt("a/a.txt", "aaa"),
		txt("b/SKILL.md", skillMD("beta", "", "b\n")),
		txt("b/b.txt", "bbb"),
	})
	lim := DefaultLimits()
	lim.MaxExpandedBytes = 10
	_, err := PrepareTar(data, lim)
	wantErrIs(t, err, ErrLimit)

	lim = DefaultLimits()
	lim.MaxEntries = 1
	data = buildTar(t, []tfile{
		txt("a/SKILL.md", skillMD("alpha", "", "a\n")),
		txt("a/a.txt", "a"),
	})
	_, err = PrepareTar(data, lim)
	wantErrIs(t, err, ErrLimit)
}

func TestTarTruncatedRejected(t *testing.T) {
	data := buildTar(t, []tfile{
		txt("a/SKILL.md", skillMD("alpha", "", "a\n")),
		txt("a/foo.txt", "some content here"),
	})
	for _, cut := range []int{1, 10, 100} {
		if _, err := PrepareTar(data[:len(data)-cut], DefaultLimits()); err == nil {
			t.Fatalf("truncated tar (cut %d) must be rejected", cut)
		}
	}
}

func TestTarNestedRootsRejected(t *testing.T) {
	data := buildTar(t, []tfile{
		txt("a/SKILL.md", skillMD("outer", "", "o\n")),
		txt("a/b/SKILL.md", skillMD("inner", "", "i\n")),
	})
	_, err := PrepareTar(data, DefaultLimits())
	wantErrIs(t, err, ErrNestedRoot)
}

func TestTarDuplicateCanonicalNameRejected(t *testing.T) {
	data := buildTar(t, []tfile{
		txt("a/SKILL.md", skillMD("My-Skill", "", "a\n")),
		txt("b/SKILL.md", skillMD("my-skill", "", "b\n")),
	})
	_, err := PrepareTar(data, DefaultLimits())
	wantErrIs(t, err, ErrDuplicateName)
}

func TestGzipTarRejected(t *testing.T) {
	raw := buildTar(t, []tfile{txt("a/SKILL.md", skillMD("alpha", "", "a\n"))})
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	gz.Write(raw)
	gz.Close()
	// PrepareTar must not silently gunzip; gzip-tar is an unsupported encoding.
	_, err := PrepareTar(buf.Bytes(), DefaultLimits())
	wantErrIs(t, err, ErrInvalidArchive)
}

func TestNoArchiveFallback(t *testing.T) {
	// A zip handed to the tar decoder and a tar handed to the zip decoder must each
	// fail — there is no sniffing or parser fallback.
	zipBytes := buildZip(t, []tfile{txt("a/SKILL.md", skillMD("alpha", "", "a\n"))})
	tarBytes := buildTar(t, []tfile{txt("a/SKILL.md", skillMD("alpha", "", "a\n"))})

	if _, err := PrepareTar(zipBytes, DefaultLimits()); err == nil {
		t.Fatal("zip bytes must not be accepted as tar")
	}
	if _, err := PrepareZip(tarBytes, DefaultLimits()); err == nil {
		t.Fatal("tar bytes must not be accepted as zip")
	}
}
