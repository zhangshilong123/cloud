package skillsource

import (
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
)

// readDirectory walks root deterministically (filepath.WalkDir reads entries in lexical
// order), keeping only regular files, rejecting symlinks and platform-recognizable special
// files, and enforcing the per-entry path, single-file, entry-count, and expanded-total
// bounds during the walk. Hardlinks are indistinguishable from regular files through the
// portable os/fs API and are therefore read as regular files (documented non-goal); the
// source-intake contract does not define hardlink handling.
func readDirectory(root string, l *Limits) ([]sourceEntry, error) {
	fi, err := os.Lstat(root)
	if err != nil {
		return nil, fmt.Errorf("skillsource: stat source root: %w", err)
	}
	if fi.Mode()&os.ModeSymlink != 0 || !fi.IsDir() {
		return nil, fmt.Errorf("%w: source root %q is not a directory", ErrUnsupportedType, root)
	}

	entries := make([]sourceEntry, 0, 16)
	var total uint64
	err = filepath.WalkDir(root, func(p string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if p == root {
			return nil
		}
		rel, rerr := filepath.Rel(root, p)
		if rerr != nil {
			return rerr
		}
		rel = filepath.ToSlash(rel)

		if d.IsDir() {
			return nil
		}
		typ := d.Type()
		if typ&(fs.ModeSymlink|fs.ModeNamedPipe|fs.ModeSocket|fs.ModeDevice|fs.ModeCharDevice|fs.ModeIrregular) != 0 {
			return fmt.Errorf("%w: %q", ErrUnsupportedType, rel)
		}
		if !typ.IsRegular() {
			return fmt.Errorf("%w: %q", ErrUnsupportedType, rel)
		}

		if err := validateEntryPath(rel, l); err != nil {
			return err
		}
		info, ierr := d.Info()
		if ierr != nil {
			return ierr
		}
		if uint64(info.Size()) > l.Package.MaxSingleFileBytes { //nolint:gosec // Size() ≥ 0 for a regular file (guarded above); this is an early-exit, actual bytes are re-checked by readFileBounded.
			return fmt.Errorf("%w: file %q is %d bytes (max %d)", ErrLimit, rel, info.Size(), l.Package.MaxSingleFileBytes)
		}
		data, rerr := readFileBounded(p, l.Package.MaxSingleFileBytes)
		if rerr != nil {
			return rerr
		}
		total, err = addChecked(total, uint64(len(data)), l.MaxExpandedBytes)
		if err != nil {
			return err
		}
		if len(entries) >= l.MaxEntries {
			return fmt.Errorf("%w: %d entries (max %d)", ErrLimit, len(entries)+1, l.MaxEntries)
		}
		entries = append(entries, sourceEntry{path: rel, data: data})
		return nil
	})
	if err != nil {
		return nil, err
	}
	return entries, nil
}

// readFileBounded reads a regular file with an enforced byte bound, failing closed when
// the file exceeds limit. It reads at most limit+1 bytes and never trusts a size hint alone.
func readFileBounded(path string, limit uint64) ([]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("skillsource: open %q: %w", path, err)
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, int64(limit)+1)) //nolint:gosec // limit ≤ 1 GiB (DefaultLimits), fits int64.
	if err != nil {
		return nil, fmt.Errorf("skillsource: read %q: %w", path, err)
	}
	if uint64(len(data)) > limit {
		return nil, fmt.Errorf("%w: file %q exceeds %d bytes", ErrLimit, path, limit)
	}
	return data, nil
}
