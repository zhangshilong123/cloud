// Package skillpkg implements the canonical Ora Skill package v1
// ("ora-skill-package") shared by Cloud ingestion and future Node materialization.
//
// A canonical Skill package is a deterministic byte-level container over the
// canonical virtual file tree of a single Skill. For any identical canonical
// tree (same relative POSIX paths, same exact file bytes), every conforming
// implementation must produce byte-identical output:
//
//	manifest    — ManifestV1: a length-prefixed, ordered list of file entries
//	tree digest — SHA256("ora-skill-tree-v1" || 0x00 || manifest)
//	package     — "ORASKILL" header + manifest + concatenated file bodies
//
// The normative byte-level contract lives in
// specs/decisions/cloud/skills/20260924-canonical-skill-package-v1.md; this
// package is the single implementation of that contract. It never touches the
// database, Object Storage, a Node's filesystem layout, or any process/network
// state: it only reads, validates, hashes, encodes, decodes, and verifies bytes.
package skillpkg

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"sort"
	"strings"
	"unicode/utf8"
)

// Stable identity constants. FormatName/FormatVersion are also what later steps
// persist as SkillRevision package_format / package_format_version.
const (
	// Magic is the fixed 8-byte header of every ora-skill-package v1 object.
	Magic = "ORASKILL"

	// FormatName / FormatVersion identify the immutable package family.
	FormatName    = "ora-skill-package"
	FormatVersion = 1

	packageVersion  uint16 = 1                  // container format version, written to the package header
	manifestVersion uint16 = 1                  // canonical ManifestV1 version
	headerSize             = len(Magic) + 2 + 8 // magic + package version + manifest length

	treeDigestDomain = "ora-skill-tree-v1" // domain-separates the tree digest from a bare SHA-256
)

// Error sentinels. Classify package failures with errors.Is.
var (
	ErrPath           = errors.New("skill package: invalid canonical path")
	ErrMissingSkillMD = errors.New("skill package: missing SKILL.md")
	ErrInvalidSkillMD = errors.New("skill package: SKILL.md is not valid UTF-8")
	ErrDuplicatePath  = errors.New("skill package: duplicate canonical path")
	ErrCaseCollision  = errors.New("skill package: case-insensitive path collision")
	ErrOrdering       = errors.New("skill package: entries out of canonical order")
	ErrLimit          = errors.New("skill package: limit exceeded")
	ErrBadMagic       = errors.New("skill package: bad magic")
	ErrVersion        = errors.New("skill package: unsupported version")
	ErrTruncated      = errors.New("skill package: truncated input")
	ErrDigest         = errors.New("skill package: file digest mismatch")
	ErrTrailing       = errors.New("skill package: trailing bytes after final file")
)

// SourceFile is one source-neutral regular file entering canonicalization: its
// exact original bytes plus a path that may still use a native separator ("\" on
// Windows). Canonicalization maps the separator to "/" and then fully validates
// the canonical form; it never rewrites the bytes or silently cleans the path.
type SourceFile struct {
	Path string
	Data []byte
}

// Entry is one canonical file in the manifest: canonical path, byte size, and the
// SHA-256 of the exact file bytes.
type Entry struct {
	Path   string
	Size   uint64
	SHA256 [32]byte
}

// Bundle is the deterministic result of one Skill file tree: the ordered entries,
// the canonical ManifestV1 bytes, the tree content digest over that manifest, and
// the complete ora-skill-package v1 bytes.
type Bundle struct {
	Entries    []Entry
	Manifest   []byte
	TreeDigest [32]byte
	Package    []byte
}

// TreeDigestHex returns the lowercase 64-character hexadecimal form of the tree
// content digest — the textual representation persisted with a SkillRevision.
func (b *Bundle) TreeDigestHex() string { return hex.EncodeToString(b.TreeDigest[:]) }

// pending is an internal Build-stage entry that still carries exact file bytes so
// the package body can be written without re-slicing the manifest.
type pending struct {
	path string
	data []byte
	size uint64
}

// Build canonicalizes files into a Bundle. It maps native separators, validates
// canonical paths and the single-root SKILL.md invariant, enforces limits, hashes
// every file, and emits the deterministic manifest, tree content digest, and
// ora-skill-package v1 bytes. Files must be distinct canonical paths; discovery
// (which selects a Skill root) is a separate, later step.
func Build(files []SourceFile, limits Limits) (*Bundle, error) {
	l := limits.normalized()
	if len(files) > l.MaxFileCount {
		return nil, fmt.Errorf("%w: %d files (max %d)", ErrLimit, len(files), l.MaxFileCount)
	}

	ps := make([]pending, 0, len(files))
	for _, f := range files {
		p := mapSeparators(f.Path)
		if err := validatePath(p); err != nil {
			return nil, err
		}
		if uint64(len(p)) > l.MaxPathBytes {
			return nil, fmt.Errorf("%w: path %q is %d bytes (max %d)", ErrLimit, p, len(p), l.MaxPathBytes)
		}
		for _, c := range strings.Split(p, "/") {
			if uint64(len(c)) > l.MaxPathComponentBytes {
				return nil, fmt.Errorf("%w: path component in %q exceeds %d bytes", ErrLimit, p, l.MaxPathComponentBytes)
			}
		}
		if uint64(len(f.Data)) > l.MaxSingleFileBytes {
			return nil, fmt.Errorf("%w: file %q is %d bytes (max %d)", ErrLimit, p, len(f.Data), l.MaxSingleFileBytes)
		}
		ps = append(ps, pending{path: p, data: f.Data, size: uint64(len(f.Data))})
	}

	sort.Slice(ps, func(i, j int) bool { return ps[i].path < ps[j].path })

	paths := make([]string, len(ps))
	for i := range ps {
		paths[i] = ps[i].path
	}
	if err := checkPathSet(paths); err != nil {
		return nil, err
	}

	// The exact bytes of the root metadata file must be valid UTF-8 and bounded.
	// checkPathSet already guarantees exactly one canonical path "SKILL.md".
	var skillData []byte
	for i := range ps {
		if ps[i].path == "SKILL.md" {
			skillData = ps[i].data
			break
		}
	}
	if !utf8.Valid(skillData) {
		return nil, fmt.Errorf("%w", ErrInvalidSkillMD)
	}
	if uint64(len(skillData)) > l.MaxSkillMDBytes {
		return nil, fmt.Errorf("%w: SKILL.md is %d bytes (max %d)", ErrLimit, len(skillData), l.MaxSkillMDBytes)
	}

	entries := make([]Entry, len(ps))
	var total uint64
	for i := range ps {
		entries[i] = Entry{Path: ps[i].path, Size: ps[i].size, SHA256: sha256.Sum256(ps[i].data)}
		total += ps[i].size
	}
	if total > l.MaxTotalBytes {
		return nil, fmt.Errorf("%w: total %d bytes (max %d)", ErrLimit, total, l.MaxTotalBytes)
	}

	manifest := encodeManifest(entries)
	if uint64(len(manifest)) > l.MaxManifestBytes {
		return nil, fmt.Errorf("%w: manifest is %d bytes (max %d)", ErrLimit, len(manifest), l.MaxManifestBytes)
	}
	digest := treeDigest(manifest)
	pkg := encodePackage(manifest, ps)
	if uint64(len(pkg)) > l.MaxPackageBytes {
		return nil, fmt.Errorf("%w: package is %d bytes (max %d)", ErrLimit, len(pkg), l.MaxPackageBytes)
	}
	return &Bundle{Entries: entries, Manifest: manifest, TreeDigest: digest, Package: pkg}, nil
}

// Decode verifies and decodes ora-skill-package v1 bytes. It is a verifier, not
// merely a parser: it checks the magic, versions, manifest structure, canonical
// path validity, ordering, uniqueness, per-file SHA-256 over the exact body
// bytes, size bounds, and exact EOF (no trailing bytes). On success the returned
// Bundle.TreeDigest is the derived content digest for the caller to compare
// against an expected value — the digest is recomputed from the manifest, never
// stored in the package. Package aliases the input buffer (no copy).
func Decode(data []byte, limits Limits) (*Bundle, error) {
	l := limits.normalized()
	if uint64(len(data)) > l.MaxPackageBytes {
		return nil, fmt.Errorf("%w: %d bytes (max %d)", ErrLimit, len(data), l.MaxPackageBytes)
	}
	if len(data) < headerSize {
		return nil, fmt.Errorf("%w: %d bytes, need %d-byte header", ErrTruncated, len(data), headerSize)
	}
	if string(data[:len(Magic)]) != Magic {
		return nil, fmt.Errorf("%w: %q", ErrBadMagic, data[:len(Magic)])
	}
	if v := binary.BigEndian.Uint16(data[8:10]); v != packageVersion {
		return nil, fmt.Errorf("%w: package version %d, want %d", ErrVersion, v, packageVersion)
	}
	manifestLen := binary.BigEndian.Uint64(data[10:18])
	if manifestLen > l.MaxManifestBytes {
		return nil, fmt.Errorf("%w: manifest length %d (max %d)", ErrLimit, manifestLen, l.MaxManifestBytes)
	}
	if manifestLen > uint64(len(data))-uint64(headerSize) {
		return nil, fmt.Errorf("%w: manifest length %d exceeds %d input bytes", ErrTruncated, manifestLen, len(data)-headerSize)
	}
	mlen := int(manifestLen) // #nosec G115 -- manifest length is bounded above by the remaining input bytes
	manifest := data[headerSize : headerSize+mlen]
	entries, err := decodeManifest(manifest, l)
	if err != nil {
		return nil, err
	}

	body := data[headerSize+mlen:]
	bodyLen := uint64(len(body))
	var total uint64
	for i := range entries {
		e := &entries[i]
		if e.Size > bodyLen-total {
			return nil, fmt.Errorf("%w: file %q needs %d bytes, %d remain", ErrTruncated, e.Path, e.Size, bodyLen-total)
		}
		chunk := body[total : total+e.Size]
		if sha256.Sum256(chunk) != e.SHA256 {
			return nil, fmt.Errorf("%w: file %q", ErrDigest, e.Path)
		}
		total += e.Size
		if total > l.MaxTotalBytes {
			return nil, fmt.Errorf("%w: total exceeds %d bytes", ErrLimit, l.MaxTotalBytes)
		}
	}
	if total != bodyLen {
		return nil, fmt.Errorf("%w: %d byte(s) after final file", ErrTrailing, bodyLen-total)
	}
	return &Bundle{Entries: entries, Manifest: manifest, TreeDigest: treeDigest(manifest), Package: data}, nil
}

// treeDigest computes the domain-separated Skill tree content digest over the
// canonical manifest: SHA256("ora-skill-tree-v1" || 0x00 || manifest).
func treeDigest(manifest []byte) [32]byte {
	h := sha256.New()
	_, _ = h.Write([]byte(treeDigestDomain))
	_, _ = h.Write([]byte{0x00})
	_, _ = h.Write(manifest)
	var d [32]byte
	copy(d[:], h.Sum(nil))
	return d
}
