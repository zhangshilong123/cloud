package skillruntime

// Cleanup and bounded GC for the derived, disposable runtime state (6A.4). Projection cleanup and cache
// GC are independent (Node materialization ADR D26): ending an Attempt removes only that Attempt's owned
// projection, never the shared verified cache, and cache GC evicts by its own bound, never relying on or
// touching projections (projections are copies, so they survive cache eviction).

import (
	"os"
	"path/filepath"
	"sort"
	"time"
)

// CleanupAttempt removes the runtime-owned projection for one Attempt. It is idempotent (a missing
// Attempt is success) and never deletes business authority or any other resource: it removes only
// <attempt_root>/attempts/<attempt_id>. Unknown/user files outside that owned path are preserved
// (D17/D38).
func (p *Projector) CleanupAttempt(attemptID string) error {
	if err := safeComponent(attemptID); err != nil {
		return err
	}
	return os.RemoveAll(p.projectionDir(attemptID))
}

// CollectStaging sweeps abandoned projection staging directories (partial-preparation hygiene state)
// older than maxAge from <attempt_root>/.staging. It is safe to run anytime and never touches published
// projections or the verified cache.
func (p *Projector) CollectStaging(maxAge time.Duration) (int, error) {
	return sweepStaging(filepath.Join(p.attemptRoot, ".staging"), maxAge)
}

// Collect bounds the verified immutable cache and its staging: it removes stale staging trees and
// verified entries beyond the retention bound (oldest-first). The cache is derived/disposable, so
// eviction never deletes business authority, and published projections are copies that do not reference
// cache bytes (D26). The digest-addressed identity is preserved: this only removes whole entries by
// age/count, never mutates an entry's content and never re-downloads-over a corrupt entry (which remains
// fail-closed from 6A.3).
//
// maxAge bounds age; maxEntries < 0 disables the count bound, and maxAge <= 0 disables the age bound
// (eviction then keyed only by the entry count). It is idempotent and safe for concurrent use.
func (m *Materializer) Collect(maxAge time.Duration, maxEntries int) (int, error) {
	removed := 0
	n, err := sweepStaging(filepath.Join(m.cfg.CacheRoot, ".staging"), maxAge)
	if err != nil {
		return removed, err
	}
	removed += n

	entriesRoot := filepath.Join(m.cfg.CacheRoot, m.contentAlg)
	dirs, err := os.ReadDir(entriesRoot)
	if err != nil {
		if os.IsNotExist(err) {
			return removed, nil
		}
		return removed, err
	}

	type entry struct {
		name  string
		mtime time.Time
	}
	var retained []entry
	for _, d := range dirs {
		if !d.IsDir() {
			continue
		}
		info, err := d.Info()
		if err != nil {
			// Best-effort on a vanished/unreadable child; it is neither retained nor counted.
			continue
		}
		if maxAge > 0 && time.Since(info.ModTime()) > maxAge {
			if err := os.RemoveAll(filepath.Join(entriesRoot, d.Name())); err != nil {
				return removed, err
			}
			removed++
			continue
		}
		retained = append(retained, entry{name: d.Name(), mtime: info.ModTime()})
	}

	if maxEntries >= 0 && len(retained) > maxEntries {
		sort.Slice(retained, func(i, j int) bool { return retained[i].mtime.Before(retained[j].mtime) })
		for _, e := range retained[:len(retained)-maxEntries] {
			if err := os.RemoveAll(filepath.Join(entriesRoot, e.name)); err != nil {
				return removed, err
			}
			removed++
		}
	}
	return removed, nil
}

// sweepStaging removes subdirectories of root (staging hygiene state) whose modification time is older
// than maxAge. A maxAge <= 0 removes every child (used when the bound is disabled). It never recurses
// outside root and never removes published state (staging lives under a dedicated .staging directory).
func sweepStaging(root string, maxAge time.Duration) (int, error) {
	dirs, err := os.ReadDir(root)
	if err != nil {
		if os.IsNotExist(err) {
			return 0, nil
		}
		return 0, err
	}
	removed := 0
	for _, d := range dirs {
		if !d.IsDir() {
			continue
		}
		info, err := d.Info()
		if err != nil {
			continue
		}
		if maxAge <= 0 || time.Since(info.ModTime()) > maxAge {
			if err := os.RemoveAll(filepath.Join(root, d.Name())); err != nil {
				return removed, err
			}
			removed++
		}
	}
	return removed, nil
}
