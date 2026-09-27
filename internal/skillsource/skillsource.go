// Package skillsource implements V1 Skill source intake and candidate discovery:
// it turns a directory or an archive (ZIP or uncompressed TAR) into a deterministic
// PreparedSourceResult — a validated set of PreparedCandidate trees plus any
// candidate-local PreparationFailures. The semantics are frozen by
// specs/decisions/cloud/skills/20260927-source-intake-candidate-discovery.md.
//
// Ownership and boundaries:
//
//	transport decode (directory / zip / tar)     → validated canonical virtual tree
//	→ candidate discovery (exact "SKILL.md")      → reject nested roots
//	→ candidate preparation (skillmeta.Parse)     → derive canonical_name
//	→ reject duplicate canonical_name             → PreparedSourceResult
//
// The package performs no I/O beyond reading the source itself and never touches the
// database, Object Storage, or the ingestion saga. It reuses internal/skillpkg for the
// canonical path rules (skillpkg.ValidatePath) and internal/skillmeta for the SKILL.md
// metadata contract; it does not reimplement either. It deliberately does not build the
// canonical package (skillpkg.Build) — that remains the ingestion saga's single
// authoritative path — and it does not own any business effect (authz, idempotency,
// storage, activation). A caller maps PreparedCandidates into the existing
// IngestSkills saga at a separate seam (internal/core).
package skillsource

import (
	"errors"
	"fmt"

	"github.com/wanglongan587/cloud/internal/skillpkg"
)

// SourceKind names the V1 intake transport encoding the caller provides explicitly
// (ADR D2). It is orthogonal to the durable skill_ingestions.source_type: KindZip and
// KindTar both map to source_type "archive", KindDirectory to "directory".
type SourceKind string

const (
	KindDirectory SourceKind = "directory"
	KindZip       SourceKind = "zip"
	KindTar       SourceKind = "tar"
)

// SourceType is the durable skill_ingestions.source_type for a transport kind (ADR D2).
// A zip or tar source is one "archive" transport; SourceKind is its encoding.
func (k SourceKind) SourceType() string {
	if k == KindDirectory {
		return "directory"
	}
	return "archive"
}

// PreparedCandidate is one deterministic candidate the source layer produced for the
// ingestion saga. It carries exactly what the saga needs: the candidate root path, the
// durable source_type, the candidate-relative canonical file tree (exact bytes), and the
// parsed metadata identity. It never carries transport bytes or archive metadata.
type PreparedCandidate struct {
	// CandidateRoot is the canonical parent directory of the candidate's SKILL.md
	// ("" when the source root itself holds the SKILL.md). It is a locator only,
	// never a Skill identity (ADR D7).
	CandidateRoot string
	// SourceType is "directory" or "archive" (ADR D2).
	SourceType string
	// Files is the candidate-relative canonical tree: paths are relative to
	// CandidateRoot, with the transport prefix removed, in exact original bytes.
	Files []skillpkg.SourceFile
	// CanonicalName is ASCII-lowercase(name) from the SKILL.md metadata.
	CanonicalName string
	// PackageName / PackageDescription are the validated SKILL.md metadata fields.
	PackageName        string
	PackageDescription string
}

// PreparationFailure is a candidate-local failure (ADR D7): a candidate root whose
// SKILL.md metadata could not be parsed. It is not persisted, not an idempotency
// identity, and not a SkillIngestion row; it only locates the failed candidate within
// the batch outcome and carries the stable metadata error code.
type PreparationFailure struct {
	// CandidateRoot locates the failed candidate (never used as a Skill identity).
	CandidateRoot string
	// ErrorCode is the stable skillmeta failure code (e.g. skill_md_missing_name).
	ErrorCode string
	// Detail is a safe, human-readable diagnostic with no internal-path or secret data.
	Detail string
}

// PreparedSourceResult is the deterministic batch outcome of source preparation: the
// valid candidates plus any candidate-local preparation failures (ADR D8). It is a
// conceptual model only — no database resource, no migration, no idempotency identity.
type PreparedSourceResult struct {
	Candidates          []PreparedCandidate
	PreparationFailures []PreparationFailure
}

// Error sentinels. Classify source-level structural failures with errors.Is.
var (
	// ErrUnsupportedKind reports a SourceKind outside {directory, zip, tar}.
	ErrUnsupportedKind = errors.New("skillsource: unsupported source kind")
	// ErrInvalidArchive reports a corrupt, truncated, or inconsistent archive.
	ErrInvalidArchive = errors.New("skillsource: invalid or corrupt archive")
	// ErrUnsupportedType reports a non-regular entry (symlink, device, FIFO, …).
	ErrUnsupportedType = errors.New("skillsource: unsupported entry type")
	// ErrUnsafePath reports a non-canonical or unsafe entry path.
	ErrUnsafePath = errors.New("skillsource: unsafe canonical path")
	// ErrDuplicatePath reports two entries with the same canonical path.
	ErrDuplicatePath = errors.New("skillsource: duplicate canonical path")
	// ErrCaseCollision reports two entries differing only by case.
	ErrCaseCollision = errors.New("skillsource: case-insensitive path collision")
	// ErrNestedRoot reports a candidate root nested inside another (ADR D4).
	ErrNestedRoot = errors.New("skillsource: nested candidate root")
	// ErrDuplicateName reports two candidates with the same canonical_name (ADR D5).
	ErrDuplicateName = errors.New("skillsource: duplicate canonical_name")
	// ErrNoCandidates reports a source with no SKILL.md at all (ADR D6).
	ErrNoCandidates = errors.New("skillsource: no SKILL.md candidates")
	// ErrLimit reports a source/expansion limit breach (ADR D6).
	ErrLimit = errors.New("skillsource: limit exceeded")
)

// CodeOf returns the stable machine-readable code carried by a source-level structural
// error, or "source_invalid" for an unclassified error.
func CodeOf(err error) string {
	switch {
	case errors.Is(err, ErrUnsupportedKind):
		return "source_unsupported_source_kind"
	case errors.Is(err, ErrInvalidArchive):
		return "source_invalid_archive"
	case errors.Is(err, ErrUnsupportedType):
		return "source_unsupported_entry_type"
	case errors.Is(err, ErrUnsafePath):
		return "source_unsafe_path"
	case errors.Is(err, ErrDuplicatePath):
		return "source_duplicate_path"
	case errors.Is(err, ErrCaseCollision):
		return "source_case_collision"
	case errors.Is(err, ErrNestedRoot):
		return "source_nested_candidate_roots"
	case errors.Is(err, ErrDuplicateName):
		return "source_duplicate_canonical_name"
	case errors.Is(err, ErrNoCandidates):
		return "source_no_skill_candidates"
	case errors.Is(err, ErrLimit):
		return "source_limit_exceeded"
	default:
		return "source_invalid"
	}
}

// Limits bounds source processing. Byte limits are uint64; a zero field means "use the
// default" (DefaultLimits). Package is the per-candidate skillpkg bound reused as the
// authoritative package-level upper bound so a canonical candidate can never exceed it
// and only be discovered later at Build time (ADR D6, source/expansion limits).
type Limits struct {
	// Package bounds each candidate's canonicalization (skillpkg.Limits).
	Package skillpkg.Limits
	// MaxArchiveBytes caps a zip/tar source's raw byte length (directory: not applied).
	MaxArchiveBytes uint64
	// MaxEntries caps the number of regular-file entries in one source.
	MaxEntries int
	// MaxExpandedBytes caps the sum of all regular-file entry bytes in one source.
	MaxExpandedBytes uint64
	// MaxCandidates caps the number of candidate roots in one source.
	MaxCandidates int
}

// DefaultLimits returns conservative library defaults. They are provisional library
// defaults, not a product quota and not a database schema; a product config must not
// silently widen them.
func DefaultLimits() Limits {
	return Limits{
		Package:          skillpkg.DefaultLimits(),
		MaxArchiveBytes:  2 << 30, // 2 GiB
		MaxEntries:       100_000,
		MaxExpandedBytes: 2 << 30, // 2 GiB
		MaxCandidates:    256,
	}
}

// normalized fills zero (unset) fields from DefaultLimits so a zero-value Limits behaves
// as the default configuration, and normalizes the nested Package limits the same way.
//
//nolint:gocritic // value-typed receiver returns the normalized copy; the Limits config mirrors skillpkg.Build's by-value signature and intake is not a hot path.
func (l Limits) normalized() Limits {
	d := DefaultLimits()
	if l.MaxArchiveBytes == 0 {
		l.MaxArchiveBytes = d.MaxArchiveBytes
	}
	if l.MaxEntries <= 0 {
		l.MaxEntries = d.MaxEntries
	}
	if l.MaxExpandedBytes == 0 {
		l.MaxExpandedBytes = d.MaxExpandedBytes
	}
	if l.MaxCandidates <= 0 {
		l.MaxCandidates = d.MaxCandidates
	}
	l.Package = normalizePackage(l.Package)
	return l
}

// normalizePackage merges unset skillpkg limits with their defaults, mirroring the
// zero-value behavior of skillpkg itself (whose own normalization is not exported).
func normalizePackage(p skillpkg.Limits) skillpkg.Limits {
	d := skillpkg.DefaultLimits()
	if p.MaxFileCount <= 0 {
		p.MaxFileCount = d.MaxFileCount
	}
	if p.MaxPathBytes == 0 {
		p.MaxPathBytes = d.MaxPathBytes
	}
	if p.MaxPathComponentBytes == 0 {
		p.MaxPathComponentBytes = d.MaxPathComponentBytes
	}
	if p.MaxSingleFileBytes == 0 {
		p.MaxSingleFileBytes = d.MaxSingleFileBytes
	}
	if p.MaxTotalBytes == 0 {
		p.MaxTotalBytes = d.MaxTotalBytes
	}
	if p.MaxManifestBytes == 0 {
		p.MaxManifestBytes = d.MaxManifestBytes
	}
	if p.MaxPackageBytes == 0 {
		p.MaxPackageBytes = d.MaxPackageBytes
	}
	if p.MaxSkillMDBytes == 0 {
		p.MaxSkillMDBytes = d.MaxSkillMDBytes
	}
	return p
}

// PrepareDirectory reads a directory source rooted at root and returns its prepared
// result. A non-nil error is a source-level structural failure (zero candidates were
// formed); PreparationFailures are candidate-local and never abort the source.
//
//nolint:gocritic // value-typed Limits mirrors skillpkg.Build's by-value signature; source intake is not a hot path.
func PrepareDirectory(root string, limits Limits) (*PreparedSourceResult, error) {
	l := limits.normalized()
	entries, err := readDirectory(root, &l)
	if err != nil {
		return nil, err
	}
	return prepare(entries, KindDirectory.SourceType(), &l)
}

// PrepareZip decodes a ZIP archive's exact bytes and returns its prepared result.
//
//nolint:gocritic // value-typed Limits mirrors skillpkg.Build's by-value signature; source intake is not a hot path.
func PrepareZip(data []byte, limits Limits) (*PreparedSourceResult, error) {
	l := limits.normalized()
	entries, err := readZip(data, &l)
	if err != nil {
		return nil, err
	}
	return prepare(entries, KindZip.SourceType(), &l)
}

// PrepareTar decodes an uncompressed TAR archive's exact bytes and returns its prepared
// result. gzip-compressed tar is unsupported and rejected by the caller (ADR D1/D2).
//
//nolint:gocritic // value-typed Limits mirrors skillpkg.Build's by-value signature; source intake is not a hot path.
func PrepareTar(data []byte, limits Limits) (*PreparedSourceResult, error) {
	l := limits.normalized()
	entries, err := readTar(data, &l)
	if err != nil {
		return nil, err
	}
	return prepare(entries, KindTar.SourceType(), &l)
}

// sourceEntry is one validated regular file entering the shared pipeline: a canonical
// relative POSIX path plus its exact bytes. Decode stages produce it; prepare consumes it.
type sourceEntry struct {
	path string
	data []byte
}

// checkArchiveBytes enforces the raw archive byte bound before any parse.
func checkArchiveBytes(n uint64, l *Limits) error {
	if n > l.MaxArchiveBytes {
		return fmt.Errorf("%w: archive is %d bytes (max %d)", ErrLimit, n, l.MaxArchiveBytes)
	}
	return nil
}

// addChecked accumulates a size with overflow-safe bound checking: it fails when the
// increment exceeds limit or when total+inc would exceed limit (and therefore can never
// overflow uint64).
func addChecked(total, inc, limit uint64) (uint64, error) {
	if inc > limit || total > limit-inc {
		return 0, fmt.Errorf("%w: expanded total %d bytes (max %d)", ErrLimit, total+inc, limit)
	}
	return total + inc, nil
}
