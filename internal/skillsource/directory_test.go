package skillsource

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func symlinkOrSkip(t *testing.T, target, link string) {
	t.Helper()
	if err := os.Symlink(target, link); err != nil {
		t.Skipf("symlinks not supported here: %v", err)
	}
}

func TestDirectorySymlinkRejected(t *testing.T) {
	root := writeTree(t, map[string]string{
		"a/SKILL.md": skillMD("alpha", "", "a\n"),
		"a/real.txt": "real",
	})
	symlinkOrSkip(t, "real.txt", filepath.Join(root, "a", "link.txt"))
	_, err := PrepareDirectory(root, DefaultLimits())
	wantErrIs(t, err, ErrUnsupportedType)
}

func TestDirectorySymlinkedRootRejected(t *testing.T) {
	real := writeTree(t, map[string]string{"SKILL.md": skillMD("alpha", "", "a\n")})
	link := filepath.Join(filepath.Dir(real), "link-root")
	symlinkOrSkip(t, real, link)
	_, err := PrepareDirectory(link, DefaultLimits())
	wantErrIs(t, err, ErrUnsupportedType)
}

func TestDirectorySpecialFileRejected(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("special files (FIFO/device/socket) are not constructible on Windows")
	}
	// A FIFO inside the tree must be rejected as an unsupported entry type.
	root := writeTree(t, map[string]string{"SKILL.md": skillMD("alpha", "", "a\n")})
	fifo := filepath.Join(root, "pipe")
	if err := mkfifo(fifo); err != nil {
		t.Skipf("mkfifo not supported: %v", err)
	}
	_, err := PrepareDirectory(root, DefaultLimits())
	wantErrIs(t, err, ErrUnsupportedType)
}

func TestDirectoryPathLimit(t *testing.T) {
	root := writeTree(t, map[string]string{
		"a/SKILL.md":             skillMD("alpha", "", "a\n"),
		"a/a-very-long-name.txt": "x",
	})
	lim := DefaultLimits()
	lim.Package.MaxPathBytes = 12
	_, err := PrepareDirectory(root, lim)
	wantErrIs(t, err, ErrLimit)
}

func TestDirectoryFileCountLimit(t *testing.T) {
	root := writeTree(t, map[string]string{
		"a/SKILL.md": skillMD("alpha", "", "a\n"),
		"a/one.txt":  "1",
		"a/two.txt":  "2",
	})
	lim := DefaultLimits()
	lim.Package.MaxFileCount = 2
	_, err := PrepareDirectory(root, lim)
	wantErrIs(t, err, ErrLimit)
}

func TestDirectoryTotalLimit(t *testing.T) {
	root := writeTree(t, map[string]string{
		"a/SKILL.md": skillMD("alpha", "", "a\n"),
		"a/big.txt":  "0123456789",
	})
	lim := DefaultLimits()
	lim.Package.MaxTotalBytes = 4
	_, err := PrepareDirectory(root, lim)
	wantErrIs(t, err, ErrLimit)
}

func TestDirectoryExpandedTotalLimit(t *testing.T) {
	root := writeTree(t, map[string]string{
		"a/SKILL.md": skillMD("alpha", "", "a\n"),
		"a/a.txt":    "aaa",
		"b/SKILL.md": skillMD("beta", "", "b\n"),
		"b/b.txt":    "bbb",
	})
	lim := DefaultLimits()
	lim.MaxExpandedBytes = 10
	_, err := PrepareDirectory(root, lim)
	wantErrIs(t, err, ErrLimit)
}

func TestDirectoryEntryCountLimit(t *testing.T) {
	root := writeTree(t, map[string]string{
		"a/SKILL.md": skillMD("alpha", "", "a\n"),
		"a/a.txt":    "a",
		"a/b.txt":    "b",
	})
	lim := DefaultLimits()
	lim.MaxEntries = 2
	_, err := PrepareDirectory(root, lim)
	wantErrIs(t, err, ErrLimit)
}
