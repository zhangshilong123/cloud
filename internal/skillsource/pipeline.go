package skillsource

import (
	"fmt"
	"sort"
	"strings"

	"github.com/wanglongan587/cloud/internal/skillmeta"
	"github.com/wanglongan587/cloud/internal/skillpkg"
)

// skillMDFile is the exact candidate-root marker discovered by the source layer.
// Discovery is case-sensitive: skill.md / Skill.md / SKILL.MD are not candidates.
const skillMDFile = "SKILL.md"

// mapSeparators normalizes a transport path to POSIX form by replacing the Windows
// separator with "/", exactly as skillpkg does before validating. No cleaning or
// rewriting is performed; the path is then validated in its canonical form.
func mapSeparators(p string) string { return strings.ReplaceAll(p, "\\", "/") }

// prepare runs the shared, transport-independent pipeline over a decoded, per-entry
// validated entry set (ADR D5 timing): sort deterministically, validate the source-wide
// structure (duplicate path / case collision), discover candidate roots, reject nested
// roots, parse each candidate's SKILL.md, derive canonical_name, reject duplicates among
// valid candidates, and partition each candidate into a candidate-relative tree.
func prepare(entries []sourceEntry, sourceType string, l *Limits) (*PreparedSourceResult, error) {
	sort.Slice(entries, func(i, j int) bool { return entries[i].path < entries[j].path })
	if err := checkDuplicates(entries); err != nil {
		return nil, err
	}

	roots := discoverRoots(entries)
	if len(roots) == 0 {
		return nil, fmt.Errorf("%w", ErrNoCandidates)
	}
	if len(roots) > l.MaxCandidates {
		return nil, fmt.Errorf("%w: %d candidates (max %d)", ErrLimit, len(roots), l.MaxCandidates)
	}
	if a, b, ok := firstNestedRoot(roots); ok {
		return nil, fmt.Errorf("%w: %q nested inside %q", ErrNestedRoot, b, a)
	}

	var (
		candidates []PreparedCandidate
		failures   []PreparationFailure
		byName     = make(map[string]string, len(roots))
	)
	for _, root := range roots {
		meta, err := skillmeta.Parse(skillMDBytes(entries, root))
		if err != nil {
			failures = append(failures, PreparationFailure{
				CandidateRoot: root,
				ErrorCode:     skillmeta.CodeOf(err),
				Detail:        err.Error(),
			})
			continue
		}
		canonical := meta.CanonicalName()
		if other, dup := byName[canonical]; dup {
			return nil, fmt.Errorf("%w: %q (candidates %q and %q)", ErrDuplicateName, canonical, other, root)
		}
		byName[canonical] = root

		files, err := partition(entries, root, l)
		if err != nil {
			return nil, err
		}
		candidates = append(candidates, PreparedCandidate{
			CandidateRoot:      root,
			SourceType:         sourceType,
			Files:              files,
			CanonicalName:      canonical,
			PackageName:        meta.Name,
			PackageDescription: meta.Description,
		})
	}
	return &PreparedSourceResult{Candidates: candidates, PreparationFailures: failures}, nil
}

// validateEntryPath applies the single shared canonical path contract (skillpkg) plus the
// package path-length bounds, so a hostile or oversized path fails during decode rather
// than at Build time. It is used for every entry, including ones that will later be
// ignored as unattached.
func validateEntryPath(p string, l *Limits) error {
	if err := skillpkg.ValidatePath(p); err != nil {
		return fmt.Errorf("%w: %v", ErrUnsafePath, err)
	}
	if uint64(len(p)) > l.Package.MaxPathBytes {
		return fmt.Errorf("%w: path %q is %d bytes (max %d)", ErrLimit, p, len(p), l.Package.MaxPathBytes)
	}
	for _, c := range strings.Split(p, "/") {
		if uint64(len(c)) > l.Package.MaxPathComponentBytes {
			return fmt.Errorf("%w: path component in %q exceeds %d bytes", ErrLimit, p, l.Package.MaxPathComponentBytes)
		}
	}
	return nil
}

// checkDuplicates verifies the source-wide canonical-tree invariants over a sorted entry
// set: no duplicate exact path and no case-insensitive collision. The fold is Unicode
// simple lowercase, matching skillpkg's collision key.
func checkDuplicates(entries []sourceEntry) error {
	prev := ""
	seen := make(map[string]string, len(entries))
	for _, e := range entries {
		if prev != "" && e.path == prev {
			return fmt.Errorf("%w: %q", ErrDuplicatePath, e.path)
		}
		if key := strings.ToLower(e.path); seen[key] != "" && seen[key] != e.path {
			return fmt.Errorf("%w: %q vs %q", ErrCaseCollision, seen[key], e.path)
		}
		seen[strings.ToLower(e.path)] = e.path
		prev = e.path
	}
	return nil
}

// discoverRoots returns the distinct candidate roots (parent directories of exact
// "SKILL.md" entries) in canonical sorted order. A top-level SKILL.md yields root "".
func discoverRoots(entries []sourceEntry) []string {
	roots := make([]string, 0, 1)
	for _, e := range entries {
		if e.path == skillMDFile {
			roots = append(roots, "")
		} else if rest, ok := strings.CutSuffix(e.path, "/"+skillMDFile); ok {
			roots = append(roots, rest)
		}
	}
	sort.Strings(roots)
	return roots
}

// isAncestor reports whether outer is a component-ancestor of inner in canonical path
// components: "a" is an ancestor of "a/b" but not of "ab" (ADR D4). The source root ("")
// owns the whole tree, so it is an ancestor of every other root.
func isAncestor(outer, inner string) bool {
	if outer == "" {
		return true
	}
	return strings.HasPrefix(inner, outer+"/")
}

// firstNestedRoot returns the first ancestor/descendant pair among sorted roots, if any.
func firstNestedRoot(roots []string) (outer, inner string, ok bool) {
	for i := range roots {
		for j := i + 1; j < len(roots); j++ {
			if isAncestor(roots[i], roots[j]) {
				return roots[i], roots[j], true
			}
		}
	}
	return "", "", false
}

// skillMDBytes returns the exact SKILL.md bytes for a candidate root ("" meaning the
// source root).
func skillMDBytes(entries []sourceEntry, root string) []byte {
	path := skillMDFile
	if root != "" {
		path = root + "/" + skillMDFile
	}
	for _, e := range entries {
		if e.path == path {
			return e.data
		}
	}
	return nil
}

// partition collects the candidate subtree for root, rewriting each path relative to the
// root and enforcing the per-candidate file-count and total-byte package bounds so they
// are detected here (structural) rather than only at Build time.
func partition(entries []sourceEntry, root string, l *Limits) ([]skillpkg.SourceFile, error) {
	prefix := ""
	if root != "" {
		prefix = root + "/"
	}
	files := make([]skillpkg.SourceFile, 0, len(entries))
	var total uint64
	for _, e := range entries {
		if prefix != "" && !strings.HasPrefix(e.path, prefix) {
			continue
		}
		files = append(files, skillpkg.SourceFile{Path: strings.TrimPrefix(e.path, prefix), Data: e.data})
		total += uint64(len(e.data))
	}
	if len(files) > l.Package.MaxFileCount {
		return nil, fmt.Errorf("%w: candidate %q has %d files (max %d)", ErrLimit, root, len(files), l.Package.MaxFileCount)
	}
	if total > l.Package.MaxTotalBytes {
		return nil, fmt.Errorf("%w: candidate %q is %d bytes (max %d)", ErrLimit, root, total, l.Package.MaxTotalBytes)
	}
	return files, nil
}
