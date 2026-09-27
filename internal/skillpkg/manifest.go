package skillpkg

import (
	"encoding/binary"
	"fmt"
)

// encodeManifest serializes entries into canonical ManifestV1 bytes:
//
//	manifest_version uint16 big-endian
//	file_count       uint32 big-endian
//	per file: path_length uint32, path bytes, file_size uint64, file_sha256 (32 raw bytes)
//
// This is the canonical wire format, not JSON/protobuf; the Go structs are only an
// internal representation.
func encodeManifest(entries []Entry) []byte {
	buf := make([]byte, 0, 6)
	buf = binary.BigEndian.AppendUint16(buf, manifestVersion)
	buf = binary.BigEndian.AppendUint32(buf, uint32(len(entries))) // #nosec G115 -- file count is bounded above by MaxFileCount
	for _, e := range entries {
		buf = binary.BigEndian.AppendUint32(buf, uint32(len(e.Path))) // #nosec G115 -- path length is bounded above by MaxPathBytes
		buf = append(buf, e.Path...)
		buf = binary.BigEndian.AppendUint64(buf, e.Size)
		buf = append(buf, e.SHA256[:]...)
	}
	return buf
}

// decodeManifest parses and defensively validates a ManifestV1 byte slice. Every
// length field is checked against the configured limits and the remaining input
// before any indexing, so a hostile manifest cannot trigger an oversized or
// out-of-bounds read.
func decodeManifest(b []byte, l Limits) ([]Entry, error) {
	if len(b) < 6 {
		return nil, fmt.Errorf("%w: manifest header", ErrTruncated)
	}
	if v := binary.BigEndian.Uint16(b[0:2]); v != manifestVersion {
		return nil, fmt.Errorf("%w: manifest version %d, want %d", ErrVersion, v, manifestVersion)
	}
	count := binary.BigEndian.Uint32(b[2:6])
	if int64(count) > int64(l.MaxFileCount) {
		return nil, fmt.Errorf("%w: %d files (max %d)", ErrLimit, count, l.MaxFileCount)
	}

	entries := make([]Entry, 0, count)
	pos := 6
	for i := uint32(0); i < count; i++ {
		if pos+4 > len(b) {
			return nil, fmt.Errorf("%w: entry %d path length", ErrTruncated, i)
		}
		plen := binary.BigEndian.Uint32(b[pos : pos+4])
		pos += 4
		if uint64(plen) > l.MaxPathBytes {
			return nil, fmt.Errorf("%w: path length %d (max %d)", ErrLimit, plen, l.MaxPathBytes)
		}
		if int64(plen) > int64(len(b)-pos) {
			return nil, fmt.Errorf("%w: entry %d path", ErrTruncated, i)
		}
		path := string(b[pos : pos+int(plen)])
		pos += int(plen)
		if pos+8+32 > len(b) {
			return nil, fmt.Errorf("%w: entry %d size and digest", ErrTruncated, i)
		}
		size := binary.BigEndian.Uint64(b[pos : pos+8])
		pos += 8
		var dg [32]byte
		copy(dg[:], b[pos:pos+32])
		pos += 32

		if err := validatePath(path); err != nil {
			return nil, err
		}
		if size > l.MaxSingleFileBytes {
			return nil, fmt.Errorf("%w: file %q is %d bytes (max %d)", ErrLimit, path, size, l.MaxSingleFileBytes)
		}
		entries = append(entries, Entry{Path: path, Size: size, SHA256: dg})
	}

	paths := make([]string, len(entries))
	for i := range entries {
		paths[i] = entries[i].Path
	}
	if err := checkPathSet(paths); err != nil {
		return nil, err
	}
	return entries, nil
}

// checkPathSet verifies the canonical-tree invariants over paths in the order they
// appear: strict ascending byte order (no out-of-order and no duplicate entry), no
// case-insensitive collision, and exactly one root "SKILL.md". Build sorts before
// calling; Decode requires the already-encoded order to be canonical.
func checkPathSet(paths []string) error {
	prev := ""
	seen := make(map[string]string, len(paths))
	skill := 0
	for _, p := range paths {
		if prev != "" && p <= prev {
			if p == prev {
				return fmt.Errorf("%w: %q", ErrDuplicatePath, p)
			}
			return fmt.Errorf("%w: %q after %q", ErrOrdering, p, prev)
		}
		key := foldKey(p)
		if other, ok := seen[key]; ok {
			return fmt.Errorf("%w: %q vs %q", ErrCaseCollision, other, p)
		}
		seen[key] = p
		if p == "SKILL.md" {
			skill++
		}
		prev = p
	}
	if skill != 1 {
		return fmt.Errorf("%w", ErrMissingSkillMD)
	}
	return nil
}

// encodePackage serializes a Bundle's manifest and file bodies into
// ora-skill-package v1 bytes:
//
//	magic 8 bytes, package_version uint16, manifest_length uint64,
//	manifest, then each file's exact bytes in manifest order.
func encodePackage(manifest []byte, ps []pending) []byte {
	n := headerSize + len(manifest)
	for i := range ps {
		n += len(ps[i].data)
	}
	buf := make([]byte, 0, n)
	buf = append(buf, Magic...)
	buf = binary.BigEndian.AppendUint16(buf, packageVersion)
	buf = binary.BigEndian.AppendUint64(buf, uint64(len(manifest)))
	buf = append(buf, manifest...)
	for i := range ps {
		buf = append(buf, ps[i].data...)
	}
	return buf
}
