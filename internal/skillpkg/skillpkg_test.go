package skillpkg_test

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"testing"

	"github.com/wanglongan587/cloud/internal/skillpkg"
)

// The golden constants below were derived from an independent Python reference
// implementation of the byte-level contract (not from Build), so they guard
// against this implementation drifting from the spec.
const (
	// Vector 1 — minimal: a single SKILL.md.
	v1SkillMD          = "# Demo Skill\n\nA minimal golden-vector skill.\n"
	v1ManifestHex      = "00010000000100000008534b494c4c2e6d64000000000000002dbf96785083afbf16facb5dfeee03efd492f5e14a13c987f479c003badbb6851c"
	v1TreeDigestHex    = "a0bfb8117999b224c3527354cb3003d91f6876bed347667f68ba5ab2bcc39bbb"
	v1PackageSHA256Hex = "370825dd99f62cff9c5f825450ad8c69b2bd049a25ffed5ea58729f35500c657"

	// Vector 2 — multiple files: SKILL.md + assets/icon.bin (binary) + scripts/run.sh.
	v2SkillMD          = "# Multi-file Skill\n"
	v2RunSH            = "#!/bin/sh\necho ok\n"
	v2ManifestHex      = "00010000000300000008534b494c4c2e6d64000000000000001347bada80e8c09b135a640e759715b723fcb28b446735a3ea6517e2c5445a50150000000f6173736574732f69636f6e2e62696e000000000000000ca87edd2f00dfefd9e4f7d58017294e8ead89b64d21c9f66e38c29a6aac6772af0000000e736372697074732f72756e2e73680000000000000012b4d644d4279594903f1a9911956432d9473041f2984fc6014c14d7402c7d126c"
	v2TreeDigestHex    = "0022e06e2e1aa41d2dc00159327e16f0a662269b420c84a88e47f74ee2c0de6c"
	v2PackageSHA256Hex = "8c7393d135ec9124f8b3be5b78f721a3dcb04b4bb95fd4a9376af7f46696190b"
)

var v2Icon = []byte{0x00, 0xff, 0x89, 0x50, 0x4e, 0x47, 0x0d, 0x0a, 0x1a, 0x0a, 0xff, 0xfe}

func sha256Hex(b []byte) string {
	d := sha256.Sum256(b)
	return hex.EncodeToString(d[:])
}

func vector2Sources() []skillpkg.SourceFile {
	return []skillpkg.SourceFile{
		{Path: "SKILL.md", Data: []byte(v2SkillMD)},
		{Path: "assets/icon.bin", Data: v2Icon},
		{Path: "scripts/run.sh", Data: []byte(v2RunSH)},
	}
}

func TestGoldenVector1Minimal(t *testing.T) {
	b, err := skillpkg.Build(
		[]skillpkg.SourceFile{{Path: "SKILL.md", Data: []byte(v1SkillMD)}},
		skillpkg.Limits{},
	)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if got := hex.EncodeToString(b.Manifest); got != v1ManifestHex {
		t.Errorf("manifest:\n got %s\nwant %s", got, v1ManifestHex)
	}
	if got := b.TreeDigestHex(); got != v1TreeDigestHex {
		t.Errorf("tree digest:\n got %s\nwant %s", got, v1TreeDigestHex)
	}
	if got := sha256Hex(b.Package); got != v1PackageSHA256Hex {
		t.Errorf("package sha256:\n got %s\nwant %s", got, v1PackageSHA256Hex)
	}
	if len(b.Entries) != 1 || b.Entries[0].Path != "SKILL.md" || b.Entries[0].Size != uint64(len(v1SkillMD)) {
		t.Errorf("unexpected entries: %+v", b.Entries)
	}
}

func TestGoldenVector2MultipleFiles(t *testing.T) {
	b, err := skillpkg.Build(vector2Sources(), skillpkg.Limits{})
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if got := hex.EncodeToString(b.Manifest); got != v2ManifestHex {
		t.Errorf("manifest:\n got %s\nwant %s", got, v2ManifestHex)
	}
	if got := b.TreeDigestHex(); got != v2TreeDigestHex {
		t.Errorf("tree digest:\n got %s\nwant %s", got, v2TreeDigestHex)
	}
	if got := sha256Hex(b.Package); got != v2PackageSHA256Hex {
		t.Errorf("package sha256:\n got %s\nwant %s", got, v2PackageSHA256Hex)
	}
	want := []string{"SKILL.md", "assets/icon.bin", "scripts/run.sh"}
	if len(b.Entries) != len(want) {
		t.Fatalf("entries = %d, want %d", len(b.Entries), len(want))
	}
	for i, p := range want {
		if b.Entries[i].Path != p {
			t.Errorf("entries[%d].Path = %q, want %q", i, b.Entries[i].Path, p)
		}
	}
}

func TestGoldenVector3SourceConvergence(t *testing.T) {
	canonical, err := skillpkg.Build(vector2Sources(), skillpkg.Limits{})
	if err != nil {
		t.Fatalf("Build(canonical): %v", err)
	}
	// Same logical tree supplied in a different order and with Windows separators.
	converged, err := skillpkg.Build(
		[]skillpkg.SourceFile{
			{Path: "scripts\\run.sh", Data: []byte(v2RunSH)},
			{Path: "SKILL.md", Data: []byte(v2SkillMD)},
			{Path: "assets\\icon.bin", Data: v2Icon},
		},
		skillpkg.Limits{},
	)
	if err != nil {
		t.Fatalf("Build(converged): %v", err)
	}
	if !bytes.Equal(canonical.Manifest, converged.Manifest) {
		t.Errorf("manifest diverged:\n %s\n %s",
			hex.EncodeToString(canonical.Manifest), hex.EncodeToString(converged.Manifest))
	}
	if canonical.TreeDigest != converged.TreeDigest {
		t.Errorf("tree digest diverged")
	}
	if !bytes.Equal(canonical.Package, converged.Package) {
		t.Errorf("package diverged")
	}
}

// TestGoldenVector4MetadataIndependence asserts that canonical identity depends
// only on (canonical path, exact file bytes): the ManifestV1 record for a file is
// exactly path(4+len) + size(8) + sha256(32), with no mode/mtime/uid/gid/archive
// field, and binary bytes round-trip verbatim (no newline/NFC/rewrite).
func TestGoldenVector4MetadataIndependence(t *testing.T) {
	b, err := skillpkg.Build(
		[]skillpkg.SourceFile{{Path: "SKILL.md", Data: []byte(v1SkillMD)}},
		skillpkg.Limits{},
	)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	// manifest = version(2) + count(4) + [path_len(4)+path(8)+size(8)+sha256(32)] = 58.
	// Any mode/mtime/uid/gid/archive field would add bytes to this record.
	if got, want := len(b.Manifest), 2+4+4+len("SKILL.md")+8+32; got != want {
		t.Errorf("manifest length = %d, want %d (metadata fields would add bytes)", got, want)
	}

	// The binary asset's exact bytes are recoverable, unmodified.
	multi, err := skillpkg.Build(vector2Sources(), skillpkg.Limits{})
	if err != nil {
		t.Fatalf("Build(vector2): %v", err)
	}
	got, err := skillpkg.Decode(multi.Package, skillpkg.Limits{})
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	if files := decodeBodies(t, got); !bytes.Equal(files["assets/icon.bin"], v2Icon) {
		t.Errorf("icon.bin round-trip: %x, want %x", files["assets/icon.bin"], v2Icon)
	}
}

// decodeBodies reconstructs each file's exact body bytes by walking the package
// body in manifest order, independent of skillpkg internals.
func decodeBodies(t *testing.T, b *skillpkg.Bundle) map[string][]byte {
	t.Helper()
	manifestLen := binary.BigEndian.Uint64(b.Package[10:18])
	body := b.Package[18+int(manifestLen):]
	out := make(map[string][]byte, len(b.Entries))
	var cursor int
	for _, e := range b.Entries {
		out[e.Path] = body[cursor : cursor+int(e.Size)]
		cursor += int(e.Size)
	}
	return out
}

func TestGoldenVector5PathRejection(t *testing.T) {
	ok := []byte("ok")
	cases := []struct {
		name  string
		files []skillpkg.SourceFile
		want  error
	}{
		{"absolute", []skillpkg.SourceFile{{"/etc/x", ok}}, skillpkg.ErrPath},
		{"parent traversal", []skillpkg.SourceFile{{"a/../b", ok}}, skillpkg.ErrPath},
		{"dot component", []skillpkg.SourceFile{{"./a", ok}}, skillpkg.ErrPath},
		{"empty component", []skillpkg.SourceFile{{"a//b", ok}}, skillpkg.ErrPath},
		{"trailing separator", []skillpkg.SourceFile{{"a/", ok}}, skillpkg.ErrPath},
		{"nul byte", []skillpkg.SourceFile{{"a\x00b", ok}}, skillpkg.ErrPath},
		{"drive prefix", []skillpkg.SourceFile{{"C:\\foo", ok}}, skillpkg.ErrPath},
		{"drive prefix bare", []skillpkg.SourceFile{{"C:foo", ok}}, skillpkg.ErrPath},
		{"empty path", []skillpkg.SourceFile{{"", ok}}, skillpkg.ErrPath},
		{"duplicate path", []skillpkg.SourceFile{{"SKILL.md", ok}, {"SKILL.md", ok}}, skillpkg.ErrDuplicatePath},
		{"case collision", []skillpkg.SourceFile{{"SKILL.md", ok}, {"skill.md", ok}}, skillpkg.ErrCaseCollision},
		{"missing skill md", []skillpkg.SourceFile{{"readme.md", ok}}, skillpkg.ErrMissingSkillMD},
		{"wrong-case skill md", []skillpkg.SourceFile{{"skill.md", ok}}, skillpkg.ErrMissingSkillMD},
		{"non-utf8 skill md", []skillpkg.SourceFile{{"SKILL.md", []byte{0xff}}}, skillpkg.ErrInvalidSkillMD},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := skillpkg.Build(tc.files, skillpkg.Limits{})
			if !errors.Is(err, tc.want) {
				t.Fatalf("Build() err = %v, want %v", err, tc.want)
			}
		})
	}
}

func TestGoldenVector6PackageCorruption(t *testing.T) {
	valid, err := skillpkg.Build(vector2Sources(), skillpkg.Limits{})
	if err != nil {
		t.Fatalf("Build: %v", err)
	}

	t.Run("bad magic", func(t *testing.T) {
		p := bytes.Clone(valid.Package)
		p[0] ^= 0xff
		_, err := skillpkg.Decode(p, skillpkg.Limits{})
		assertErr(t, err, skillpkg.ErrBadMagic)
	})
	t.Run("bad package version", func(t *testing.T) {
		p := bytes.Clone(valid.Package)
		binary.BigEndian.PutUint16(p[8:10], 2)
		_, err := skillpkg.Decode(p, skillpkg.Limits{})
		assertErr(t, err, skillpkg.ErrVersion)
	})
	t.Run("bad manifest version", func(t *testing.T) {
		p := bytes.Clone(valid.Package)
		binary.BigEndian.PutUint16(p[18:20], 2)
		_, err := skillpkg.Decode(p, skillpkg.Limits{})
		assertErr(t, err, skillpkg.ErrVersion)
	})
	t.Run("manifest too large", func(t *testing.T) {
		p := bytes.Clone(valid.Package)
		binary.BigEndian.PutUint64(p[10:18], 1<<40)
		_, err := skillpkg.Decode(p, skillpkg.Limits{})
		assertErr(t, err, skillpkg.ErrLimit)
	})
	t.Run("manifest truncated", func(t *testing.T) {
		p := bytes.Clone(valid.Package)
		binary.BigEndian.PutUint64(p[10:18], uint64(len(p)))
		_, err := skillpkg.Decode(p, skillpkg.Limits{})
		assertErr(t, err, skillpkg.ErrTruncated)
	})
	t.Run("file digest mismatch", func(t *testing.T) {
		p := bytes.Clone(valid.Package)
		p[len(p)-1] ^= 0xff
		_, err := skillpkg.Decode(p, skillpkg.Limits{})
		assertErr(t, err, skillpkg.ErrDigest)
	})
	t.Run("trailing bytes", func(t *testing.T) {
		p := append(bytes.Clone(valid.Package), 0x00)
		_, err := skillpkg.Decode(p, skillpkg.Limits{})
		assertErr(t, err, skillpkg.ErrTrailing)
	})
	t.Run("out of order", func(t *testing.T) {
		_, err := skillpkg.Decode(outOfOrderPackage(t), skillpkg.Limits{})
		assertErr(t, err, skillpkg.ErrOrdering)
	})
	t.Run("duplicate path", func(t *testing.T) {
		_, err := skillpkg.Decode(duplicatePathPackage(t), skillpkg.Limits{})
		assertErr(t, err, skillpkg.ErrDuplicatePath)
	})
}

func digestOf(s string) []byte {
	d := sha256.Sum256([]byte(s))
	return d[:]
}

func outOfOrderPackage(t *testing.T) []byte {
	t.Helper()
	var m []byte
	m = binary.BigEndian.AppendUint16(m, 1)
	m = binary.BigEndian.AppendUint32(m, 2)
	m = binary.BigEndian.AppendUint32(m, 4)
	m = append(m, "b.md"...)
	m = binary.BigEndian.AppendUint64(m, 1)
	m = append(m, digestOf("B")...)
	m = binary.BigEndian.AppendUint32(m, 4)
	m = append(m, "a.md"...)
	m = binary.BigEndian.AppendUint64(m, 1)
	m = append(m, digestOf("A")...)

	var p []byte
	p = append(p, skillpkg.Magic...)
	p = binary.BigEndian.AppendUint16(p, 1)
	p = binary.BigEndian.AppendUint64(p, uint64(len(m)))
	p = append(p, m...)
	p = append(p, "B"...)
	p = append(p, "A"...)
	return p
}

func duplicatePathPackage(t *testing.T) []byte {
	t.Helper()
	var m []byte
	m = binary.BigEndian.AppendUint16(m, 1)
	m = binary.BigEndian.AppendUint32(m, 2)
	m = binary.BigEndian.AppendUint32(m, 4)
	m = append(m, "b.md"...)
	m = binary.BigEndian.AppendUint64(m, 1)
	m = append(m, digestOf("B")...)
	m = binary.BigEndian.AppendUint32(m, 4)
	m = append(m, "b.md"...)
	m = binary.BigEndian.AppendUint64(m, 1)
	m = append(m, digestOf("B")...)

	var p []byte
	p = append(p, skillpkg.Magic...)
	p = binary.BigEndian.AppendUint16(p, 1)
	p = binary.BigEndian.AppendUint64(p, uint64(len(m)))
	p = append(p, m...)
	p = append(p, "B"...)
	p = append(p, "B"...)
	return p
}

func assertErr(t *testing.T, err, want error) {
	t.Helper()
	if !errors.Is(err, want) {
		t.Fatalf("Decode() err = %v, want %v", err, want)
	}
}

func TestRoundTrip(t *testing.T) {
	b, err := skillpkg.Build(vector2Sources(), skillpkg.Limits{})
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	got, err := skillpkg.Decode(b.Package, skillpkg.Limits{})
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	if got.TreeDigest != b.TreeDigest {
		t.Errorf("tree digest: got %s want %s", got.TreeDigestHex(), b.TreeDigestHex())
	}
	if !bytes.Equal(got.Package, b.Package) {
		t.Errorf("package bytes differ")
	}
	if len(got.Entries) != len(b.Entries) {
		t.Fatalf("entries = %d, want %d", len(got.Entries), len(b.Entries))
	}
	for i := range b.Entries {
		if got.Entries[i] != b.Entries[i] {
			t.Errorf("entries[%d] = %+v, want %+v", i, got.Entries[i], b.Entries[i])
		}
	}
}

func TestLimits(t *testing.T) {
	cases := []struct {
		name   string
		limits func() skillpkg.Limits
	}{
		{"file count", func() skillpkg.Limits {
			l := skillpkg.DefaultLimits()
			l.MaxFileCount = 1
			return l
		}},
		{"path bytes", func() skillpkg.Limits {
			l := skillpkg.DefaultLimits()
			l.MaxPathBytes = 4
			return l
		}},
		{"single file bytes", func() skillpkg.Limits {
			l := skillpkg.DefaultLimits()
			l.MaxSingleFileBytes = 3
			return l
		}},
		{"total bytes", func() skillpkg.Limits {
			l := skillpkg.DefaultLimits()
			l.MaxTotalBytes = 1
			return l
		}},
		{"skill md bytes", func() skillpkg.Limits {
			l := skillpkg.DefaultLimits()
			l.MaxSkillMDBytes = 5
			return l
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := skillpkg.Build(vector2Sources(), tc.limits())
			if !errors.Is(err, skillpkg.ErrLimit) {
				t.Fatalf("Build() err = %v, want ErrLimit", err)
			}
		})
	}
}
