package skillsource

import (
	"archive/tar"
	"bytes"
	"fmt"
	"io"
)

// readTar decodes an uncompressed TAR byte stream into validated entries. It deliberately
// never wraps the input in gzip.NewReader (ADR D1: gzip-tar is unsupported). Symlink,
// hardlink, device, FIFO, sparse, and other non-regular entry types are rejected
// explicitly; directory entries are skipped as content-free metadata. Declared size is
// checked against the actual bytes read, and all per-entry and source-wide bounds are
// enforced during the read.
func readTar(data []byte, l *Limits) ([]sourceEntry, error) {
	if err := checkArchiveBytes(uint64(len(data)), l); err != nil {
		return nil, err
	}
	tr := tar.NewReader(bytes.NewReader(data))

	entries := make([]sourceEntry, 0, 16)
	var total uint64
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("%w: %v", ErrInvalidArchive, err)
		}
		switch hdr.Typeflag {
		case tar.TypeDir:
			continue
		case tar.TypeReg:
			// regular file; the tar Reader normalizes the legacy '\x00' form to TypeReg
		case tar.TypeSymlink, tar.TypeLink, tar.TypeChar, tar.TypeBlock, tar.TypeFifo, tar.TypeGNUSparse:
			return nil, fmt.Errorf("%w: %q", ErrUnsupportedType, hdr.Name)
		default:
			return nil, fmt.Errorf("%w: %q", ErrUnsupportedType, hdr.Name)
		}

		name := mapSeparators(hdr.Name)
		if err := validateEntryPath(name, l); err != nil {
			return nil, err
		}
		if hdr.Size < 0 || uint64(hdr.Size) > l.Package.MaxSingleFileBytes {
			return nil, fmt.Errorf("%w: file %q is %d bytes (max %d)", ErrLimit, name, hdr.Size, l.Package.MaxSingleFileBytes)
		}
		body, err := readEntryBounded(tr, l.Package.MaxSingleFileBytes)
		if err != nil {
			return nil, err
		}
		if uint64(len(body)) != uint64(hdr.Size) {
			return nil, fmt.Errorf("%w: file %q declared %d bytes, read %d", ErrInvalidArchive, name, hdr.Size, len(body))
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
