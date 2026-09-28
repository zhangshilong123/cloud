package skillruntime

// Attempt-scoped Skill projection (6A.4): the derived, disposable, Agent-visible view of an Attempt's
// frozen + verified Skills. This file owns the projection contract frozen in
// specs/decisions/cloud/skills/20260928-attempt-projection-runtime-adapter-ready-spawn-gate.md, which
// inherits the behavioral contract of the Node materialization ADR (D10-D26, D33, D41) moved server-side:
//
//   - A projection belongs to exactly one Attempt and is keyed by attempt_id only (D10/D38).
//   - It is built from already-frozen Skill identity + already-verified cache results; it never
//     re-resolves mutable Skill/current-revision state (D13) and never executes package content (D28).
//   - It copies (never hardlinks) the verified tree, so the shared verified cache is never mutated by a
//     projection and a published projection survives cache eviction (D26 / cloud D36).
//   - Partial projection is never runtime-visible: stage the whole tree + READY marker, then one atomic
//     rename publishes them together (D15/D39).
//   - READY is an explicit marker + validation step, never inferred from files-on-disk (D19/D20/D41);
//     a failed/partial attempt is never resumed in place — a retry is a new attempt_id (D23/D24).

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// attemptMarkerName is the ownership + READY record written at the root of a published Attempt
// projection. Its presence and value are what make a projection READY and what let the runtime prove
// path ownership (Node materialization ADR D17). It is reserved: the runtime never treats a Skill file
// by this name as content.
const attemptMarkerName = ".ora-attempt-ready"

// projectionFormat / projectionFormatVersion identify the projection ownership/READY manifest layout.
const (
	projectionFormat        = "ora-skill-projection"
	projectionFormatVersion = 1
)

// ProjectionSkill is one ordered frozen entry of an Attempt projection: the frozen delivery identity
// plus an already-verified cache result. SkillID and CanonicalName are Cloud-side identities supplied at
// projection time purely to make ordering deterministic and the manifest auditable; the runtime resolves
// nothing mutable from them, and the runtime directory name is the immutable identity (skill_revision_id
// via the frozen Bundle), never a display/canonical name.
type ProjectionSkill struct {
	// SkillID is the frozen skill identity (execution_skill_bindings.skill_id); the stable ordering
	// tie-breaker when CanonicalName is equal, and part of the manifest identity. It never enters the
	// runtime directory name.
	SkillID string
	// CanonicalName is the stable Skill name used only for deterministic ordering; never for naming.
	CanonicalName string
	// Bundle is the frozen delivery metadata; its SkillRevisionID is the immutable runtime name.
	Bundle FrozenSkillBundle
	// Verified is the verified immutable cache entry the projection copies from (never mutates).
	Verified VerifiedSkillBundle
}

// SkillProjection is the published, manifest-attested materialization of one Skill within an Attempt:
// the immutable identity plus where the runtime placed it. It is what the AgentRuntimeAdapter exposes as
// a provision.
type SkillProjection struct {
	SkillID           string
	SkillRevisionID   string
	RuntimeName       string // directory name under the Attempt root, always skill_revision_id
	ContentDigest     string
	ContentDigestAlgo string
	Dir               string // absolute projected tree directory
}

// PreparedAttempt is the immutable capability proving one Attempt's projection was fully materialized
// and atomically published. It is produced only by Project, never by inspecting filesystem state.
type PreparedAttempt struct {
	AttemptID string
	Root      string // <attempt_root>/attempts/<attempt_id>
}

// ReadyAttempt is a PreparedAttempt that has passed the explicit READY barrier: its ownership/READY
// marker is present, consistent with the Attempt identity, and attests every required Skill. It is the
// only value the spawn gate accepts, so future spawn can never open on a partial/prepared-only attempt.
type ReadyAttempt struct {
	AttemptID string
	Root      string
	Skills    []SkillProjection // ordered, complete materialization manifest
}

// attemptManifest is the ownership + READY marker payload. It records the Attempt identity and the
// ordered, complete Skill materialization — never a capability, signed URL, package digest, or mutable
// Skill/Agent state.
type attemptManifest struct {
	Format        string                 `json:"format"`
	FormatVersion int                    `json:"format_version"`
	AttemptID     string                 `json:"attempt_id"`
	Skills        []attemptManifestSkill `json:"skills"`
}

type attemptManifestSkill struct {
	SkillID         string `json:"skill_id"`
	SkillRevisionID string `json:"skill_revision_id"`
	RuntimeName     string `json:"runtime_name"`
	ContentDigest   string `json:"content_digest"`
	DigestAlgo      string `json:"digest_algorithm"`
}

// Projector creates and owns Attempt-scoped Skill projections from verified cache entries. It performs
// no download, no verification, no database work, and no process execution: it only copies already
// verified trees and writes the READY marker. It is safe for concurrent use; each Attempt has its own
// published directory and a retry is a new Attempt identity.
type Projector struct {
	attemptRoot string
}

// NewProjector validates attemptRoot and returns a ready-to-use Projector. The root must be non-empty:
// a shared-machine path is never invented (mirroring the materializer's cache-root rule).
func NewProjector(attemptRoot string) (*Projector, error) {
	if strings.TrimSpace(attemptRoot) == "" {
		return nil, fmt.Errorf("skillruntime: attempt root is required")
	}
	return &Projector{attemptRoot: attemptRoot}, nil
}

// Project materializes the full ordered projection for one Attempt: it sorts the frozen Skills
// deterministically, copies every verified tree (never hardlink), writes the READY marker, and publishes
// the whole tree with one atomic rename. It returns a PreparedAttempt only when every required Skill was
// projected; any failure leaves no published attempt and discards the staging tree, so a partial
// projection never becomes runtime-visible. It never mutates the shared verified cache.
func (p *Projector) Project(ctx context.Context, attemptID string, skills []ProjectionSkill) (*PreparedAttempt, error) {
	if err := safeComponent(attemptID); err != nil {
		return nil, fmt.Errorf("skillruntime: invalid attempt id %q: %v", attemptID, err)
	}
	projDir := p.projectionDir(attemptID)

	// Idempotent reuse: an existing valid projection for the same attempt is returned unchanged, never
	// rebuilt in place (a retry is a new attempt_id). A present-but-invalid attempt root fails closed
	// and is never overwritten (D17/D23).
	if _, err := os.Stat(projDir); err == nil {
		if m, rerr := readAttemptMarker(projDir); rerr == nil && m.AttemptID == attemptID && m.Format == projectionFormat && m.FormatVersion == projectionFormatVersion {
			return &PreparedAttempt{AttemptID: attemptID, Root: projDir}, nil
		}
		return nil, wrapMsg(ErrAttemptCorrupt, "attempt %s already published inconsistently", attemptID)
	}

	ordered := append([]ProjectionSkill(nil), skills...)
	sortProjectionSkills(ordered)
	if err := validateProjectionSkills(ordered); err != nil {
		return nil, err
	}

	staging, cleanup, err := p.newStaging(attemptID)
	if err != nil {
		return nil, wrapMsg(ErrProjectionPublish, "create staging: %v", err)
	}
	defer cleanup()

	manifest := attemptManifest{Format: projectionFormat, FormatVersion: projectionFormatVersion, AttemptID: attemptID}
	seen := make(map[string]string, len(ordered))
	for _, sk := range ordered {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		name := sk.Bundle.SkillRevisionID
		if prior, ok := seen[name]; ok {
			return nil, wrapMsg(ErrProjectionCollision, "skills %s and %s share runtime name %s", prior, sk.SkillID, name)
		}
		seen[name] = sk.SkillID

		dst := filepath.Join(staging, name)
		if err := os.MkdirAll(dst, 0o755); err != nil {
			return nil, wrapMsg(ErrProjectionPublish, "mkdir %s: %v", name, err)
		}
		if err := copyTree(sk.Verified.CacheDir, dst); err != nil {
			return nil, wrapMsg(ErrProjectionPublish, "project skill %s: %v", sk.Bundle.SkillRevisionID, err)
		}
		manifest.Skills = append(manifest.Skills, attemptManifestSkill{
			SkillID:         sk.SkillID,
			SkillRevisionID: sk.Bundle.SkillRevisionID,
			RuntimeName:     name,
			ContentDigest:   sk.Bundle.ContentDigest,
			DigestAlgo:      sk.Bundle.ContentDigestAlgo,
		})
	}

	if err := writeAttemptMarker(staging, manifest); err != nil {
		return nil, wrapMsg(ErrProjectionPublish, "write marker: %v", err)
	}
	if err := publishAttempt(staging, projDir, attemptID, p); err != nil {
		return nil, err
	}
	return &PreparedAttempt{AttemptID: attemptID, Root: projDir}, nil
}

// Ready is the explicit READY barrier: it re-reads the published ownership/READY marker and confirms it
// is consistent with the Attempt identity and that every required Skill's projection directory exists.
// READY is never inferred from the mere presence of files. It returns a ReadyAttempt (the only value the
// spawn gate accepts) or fails closed with ErrNotReady / ErrAttemptCorrupt.
func (p *Projector) Ready(ctx context.Context, a PreparedAttempt) (*ReadyAttempt, error) {
	if err := safeComponent(a.AttemptID); err != nil {
		return nil, fmt.Errorf("skillruntime: invalid attempt id %q: %v", a.AttemptID, err)
	}
	// Ownership proof (D17): the projection root must be the runtime-owned path for this Attempt, never
	// an arbitrary caller-supplied directory.
	if a.Root != p.projectionDir(a.AttemptID) {
		return nil, wrapMsg(ErrNotReady, "attempt %s root is not the runtime-owned projection path", a.AttemptID)
	}
	m, err := readAttemptMarker(a.Root)
	if errors.Is(err, errNoAttemptMarker) {
		return nil, wrapMsg(ErrNotReady, "attempt %s has no READY marker", a.AttemptID)
	}
	if err != nil {
		return nil, err // ErrAttemptCorrupt
	}
	if m.AttemptID != a.AttemptID || m.Format != projectionFormat || m.FormatVersion != projectionFormatVersion {
		return nil, wrapMsg(ErrAttemptCorrupt, "attempt %s marker identity mismatch", a.AttemptID)
	}
	out := &ReadyAttempt{AttemptID: a.AttemptID, Root: a.Root, Skills: make([]SkillProjection, 0, len(m.Skills))}
	for _, ms := range m.Skills {
		dir := filepath.Join(a.Root, ms.RuntimeName)
		info, err := os.Stat(dir)
		if err != nil || !info.IsDir() {
			return nil, wrapMsg(ErrNotReady, "attempt %s skill %s projection missing", a.AttemptID, ms.SkillRevisionID)
		}
		out.Skills = append(out.Skills, SkillProjection{
			SkillID:           ms.SkillID,
			SkillRevisionID:   ms.SkillRevisionID,
			RuntimeName:       ms.RuntimeName,
			ContentDigest:     ms.ContentDigest,
			ContentDigestAlgo: ms.DigestAlgo,
			Dir:               dir,
		})
	}
	return out, nil
}

// projectionDir is the runtime-owned published projection path for an Attempt.
func (p *Projector) projectionDir(attemptID string) string {
	return filepath.Join(p.attemptRoot, "attempts", attemptID)
}

// newStaging creates a fresh staging directory on the same filesystem as the attempt root (always under
// <root>/.staging), so the final rename is atomic and never a cross-device copy. The returned cleanup
// removes the staging directory best-effort; after a successful publish it was renamed away.
func (p *Projector) newStaging(attemptID string) (string, func(), error) {
	stagingRoot := filepath.Join(p.attemptRoot, ".staging")
	if err := os.MkdirAll(stagingRoot, 0o755); err != nil {
		return "", func() {}, err
	}
	dir, err := os.MkdirTemp(stagingRoot, attemptID+"-*")
	if err != nil {
		return "", func() {}, err
	}
	return dir, func() { _ = os.RemoveAll(dir) }, nil
}

// sortProjectionSkills orders the frozen Skills deterministically by (canonical_name, skill_id,
// skill_revision_id) — the same key core uses at admission (ORDER BY s.canonical_name, esb.skill_id), so
// projection order is independent of input order and stable across retries (Node materialization D33).
func sortProjectionSkills(skills []ProjectionSkill) {
	sort.SliceStable(skills, func(i, j int) bool {
		if skills[i].CanonicalName != skills[j].CanonicalName {
			return skills[i].CanonicalName < skills[j].CanonicalName
		}
		if skills[i].SkillID != skills[j].SkillID {
			return skills[i].SkillID < skills[j].SkillID
		}
		return skills[i].Bundle.SkillRevisionID < skills[j].Bundle.SkillRevisionID
	})
}

// validateProjectionSkills fail-closes every frozen entry before any filesystem work: the same
// fail-closed materialization validation (validateBundle), a safe immutable runtime name, and a verified
// result whose identity matches the frozen bundle (never a substituted tree).
func validateProjectionSkills(skills []ProjectionSkill) error {
	for i := range skills {
		sk := &skills[i]
		if err := safeComponent(sk.SkillID); err != nil {
			return wrapMsg(ErrProjectionMismatch, "skill_id %q: %v", sk.SkillID, err)
		}
		if err := validateBundle(&sk.Bundle); err != nil {
			return wrapMsg(ErrProjectionMismatch, "skill %s: %v", sk.SkillID, err)
		}
		if err := safeComponent(sk.Bundle.SkillRevisionID); err != nil {
			return wrapMsg(ErrProjectionMismatch, "skill_revision_id %q: %v", sk.Bundle.SkillRevisionID, err)
		}
		if sk.Verified.ContentDigest != sk.Bundle.ContentDigest || sk.Verified.DigestAlgo != sk.Bundle.ContentDigestAlgo {
			return wrapMsg(ErrProjectionMismatch, "skill %s verified bundle does not match frozen content identity", sk.SkillID)
		}
		info, err := os.Stat(sk.Verified.CacheDir)
		if err != nil || !info.IsDir() {
			return wrapMsg(ErrProjectionMismatch, "skill %s verified cache dir %q is missing", sk.SkillID, sk.Verified.CacheDir)
		}
	}
	return nil
}

// copyTree copies the verified cache entry tree under dst as directories and regular files only. It
// skips the cache-internal commit marker (never Skill content) and rejects anything that is not a
// regular file or directory, so a projection can never introduce symlinks/hardlinks/devices. It never
// modifies the source tree (copy, not hardlink).
func copyTree(srcRoot, dstRoot string) error {
	return filepath.WalkDir(srcRoot, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(srcRoot, path)
		if err != nil {
			return err
		}
		if rel == "." {
			return nil
		}
		if !d.IsDir() && rel == markerFileName {
			return nil // the verified cache's own commit marker is cache-internal state, not Skill content
		}
		dst := filepath.Join(dstRoot, rel)
		if !withinRoot(dstRoot, dst) {
			return fmt.Errorf("skillruntime: path %q escapes projection root", rel)
		}
		if d.IsDir() {
			return os.MkdirAll(dst, 0o755)
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() {
			return fmt.Errorf("skillruntime: projection source %q is not a regular file", rel)
		}
		return copyFile(filepath.Join(srcRoot, rel), dst)
	})
}

// copyFile copies a single regular file byte-for-byte, preserving a data-file mode. Content is copied as
// data only; nothing is ever executed.
func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o644)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		_ = out.Close()
		return err
	}
	return out.Close()
}

// writeAttemptMarker writes the ownership/READY marker into the staging root before the atomic rename,
// so the marker is committed atomically with the tree it attests.
func writeAttemptMarker(dir string, m attemptManifest) error {
	data, err := json.Marshal(m)
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, attemptMarkerName), append(data, '\n'), 0o644)
}

// errNoAttemptMarker reports an attempt root with no (or an unreadable) READY marker.
var errNoAttemptMarker = errors.New("skillruntime: no attempt ready marker")

// readAttemptMarker reads and decodes the ownership/READY marker for a published attempt. A missing
// marker is errNoAttemptMarker; an unparsable one is ErrAttemptCorrupt.
func readAttemptMarker(root string) (attemptManifest, error) {
	data, err := os.ReadFile(filepath.Join(root, attemptMarkerName))
	if err != nil {
		if os.IsNotExist(err) {
			return attemptManifest{}, errNoAttemptMarker
		}
		return attemptManifest{}, wrapMsg(ErrAttemptCorrupt, "read marker: %v", err)
	}
	var m attemptManifest
	if err := json.Unmarshal(data, &m); err != nil {
		return attemptManifest{}, wrapMsg(ErrAttemptCorrupt, "decode marker: %v", err)
	}
	return m, nil
}

// publishAttempt atomically commits the fully staged (tree + READY marker) projection into the final
// attempt directory. If another process already published a valid projection for the same attempt it is
// reused; anything else fails closed with no in-place mutation.
func publishAttempt(staging, projDir, attemptID string, p *Projector) error {
	if err := os.MkdirAll(filepath.Dir(projDir), 0o755); err != nil {
		return wrapMsg(ErrProjectionPublish, "create attempts parent: %v", err)
	}
	if err := os.Rename(staging, projDir); err == nil {
		return nil
	}
	if m, rerr := readAttemptMarker(projDir); rerr == nil && m.AttemptID == attemptID && m.Format == projectionFormat && m.FormatVersion == projectionFormatVersion {
		return nil
	}
	return wrapMsg(ErrProjectionPublish, "atomic rename of attempt projection failed")
}

// safeComponent reports whether s is a safe single filesystem path component: non-empty, bounded, and
// free of separators, NUL, drive prefixes, and the reserved "." / ".." forms. Every identity the runtime
// maps into a path (attempt id, skill ids, runtime names) is treated as untrusted at this boundary.
func safeComponent(s string) error {
	if s == "" || len(s) > 64 {
		return errors.New("empty or too long")
	}
	if s == "." || s == ".." {
		return errors.New("reserved component")
	}
	for i := 0; i < len(s); i++ {
		switch c := s[i]; {
		case c == 0:
			return errors.New("NUL byte")
		case c == '/' || c == '\\':
			return errors.New("path separator")
		case c == ':':
			return errors.New("drive/colon")
		}
	}
	return nil
}
