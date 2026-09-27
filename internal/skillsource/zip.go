package skillsource

import (
	"archive/zip"
	"bytes"
	"fmt"
	"io"
	"os"
	"strings"
)

// readZip decodes ZIP bytes into validated entries without extracting anything to disk
// and without trusting header sizes alone. Every entry is checked at processing time:
// canonical path, entry type (regular files only; symlink/special entries are rejected,
// directory entries are skipped as content-free metadata), declared size vs actual bytes
// read, and the entry-count and expanded-total bounds. The ZIP reader verifies each entry
// CRC on read, so a corrupt body surfaces as a read error.
func readZip(data []byte, l *Limits) ([]sourceEntry, error) {
	if err := checkArchiveBytes(uint64(len(data)), l); err != nil {
		return nil, err
	}
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidArchive, err)
	}

	entries := make([]sourceEntry, 0, len(zr.File))
	var total uint64
	for _, f := range zr.File {
		if f.FileInfo().IsDir() || strings.HasSuffix(f.Name, "/") {
			continue
		}
		if mode := f.Mode(); mode&(os.ModeSymlink|os.ModeDevice|os.ModeCharDevice|os.ModeNamedPipe|os.ModeSocket|os.ModeIrregular) != 0 {
			return nil, fmt.Errorf("%w: %q", ErrUnsupportedType, f.Name)
		}

		name := mapSeparators(f.Name)
		if err := validateEntryPath(name, l); err != nil {
			return nil, err
		}
		if f.UncompressedSize64 > l.Package.MaxSingleFileBytes {
			return nil, fmt.Errorf("%w: file %q is %d bytes (max %d)", ErrLimit, name, f.UncompressedSize64, l.Package.MaxSingleFileBytes)
		}
		rc, err := f.Open()
		if err != nil {
			return nil, fmt.Errorf("%w: %v", ErrInvalidArchive, err)
		}
		body, err := readEntryBounded(rc, l.Package.MaxSingleFileBytes)
		closeErr := rc.Close()
		if err != nil {
			return nil, err
		}
		if closeErr != nil {
			return nil, fmt.Errorf("%w: %v", ErrInvalidArchive, closeErr)
		}
		if uint64(len(body)) != f.UncompressedSize64 {
			return nil, fmt.Errorf("%w: file %q declared %d bytes, read %d", ErrInvalidArchive, name, f.UncompressedSize64, len(body))
		}
		total, err = addChecked(total, uint64(len(body)), l.MaxExpandedBytes)
		if err != nil {
			return nil, err
		}
		if len(entries) >= l.MaxEntries {
			return nil, fmt.Errorf("%w: %d entries (max %d)", ErrLimit, len(entries)+1, l.MaxEntries)
		}
		entries = append(entries, sourceEntry{path: name, data: body})
	}
	return entries, nil
}

// readEntryBounded reads an archive entry body with an enforced byte bound, failing
// closed when the actual bytes exceed limit.
func readEntryBounded(r io.Reader, limit uint64) ([]byte, error) {
	data, err := io.ReadAll(io.LimitReader(r, int64(limit)+1)) //nolint:gosec // limit ≤ 1 GiB (DefaultLimits), fits int64.
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidArchive, err)
	}
	if uint64(len(data)) > limit {
		return nil, fmt.Errorf("%w: entry exceeds %d bytes", ErrLimit, limit)
	}
	return data, nil
}
