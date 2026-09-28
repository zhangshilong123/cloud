package skillruntime

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/wanglongan587/cloud/internal/skillpkg"
	"github.com/wanglongan587/cloud/internal/skillstore"
)

// markerFileName is the reserved commit record written into a published entry directory. It is written
// into the staging tree before the atomic rename, so the tree and its marker become visible together;
// its presence and value are what make a cache entry serveable. The canonical tree is asserted never to
// contain this exact root-level name (expandStaging fails closed otherwise), because it is reserved for
// this runtime's marker, not a skill file.
const markerFileName = ".ora-skill-complete"

// errNoEntry reports a cache miss: no entry (or no marker) exists for the requested content digest. It
// is distinct from ErrCacheCorrupt, which reports a present-but-invalid entry that must fail closed.
var errNoEntry = errors.New("skillruntime: no cache entry")

// entryMarker is the verified-marker payload (plan §22). It records format, digest algorithm, and
// content digest — never a capability, signed URL, package digest, or business identity.
type entryMarker struct {
	Format        string `json:"format"`
	FormatVersion int    `json:"format_version"`
	DigestAlgo    string `json:"digest_algorithm"`
	ContentDigest string `json:"content_digest"`
}

// flightGroup coalesces concurrent materializations of the same content digest into a single
// population within this process. Correctness over optimization: cross-process races are resolved by
// the atomic rename publish, not by this map.
type flightGroup struct {
	mu sync.Mutex
	m  map[string]*flight
}

type flight struct {
	done chan struct{}
}

func newFlightGroup() *flightGroup {
	return &flightGroup{m: make(map[string]*flight)}
}

// begin returns a finish func. The first caller for key becomes the population leader and receives the
// only real closer (which signals waiters and clears the map); every concurrent waiter blocks until the
// leader finishes and receives a no-op closer. A waiter then re-checks the cache and, if the leader did
// not publish, will itself become the leader of a fresh flight.
func (g *flightGroup) begin(key string) (finish func()) {
	g.mu.Lock()
	if f, ok := g.m[key]; ok {
		g.mu.Unlock()
		<-f.done
		return func() {}
	}
	f := &flight{done: make(chan struct{})}
	g.m[key] = f
	g.mu.Unlock()
	return func() {
		close(f.done)
		g.mu.Lock()
		delete(g.m, key)
		g.mu.Unlock()
	}
}

// validateHit returns the verified entry if the cache already holds a fully-valid entry for the frozen
// bundle. It validates the commit marker (format, version, digest algorithm, content digest) against the
// request; it never trusts an entry directory's mere presence. A missing entry is a miss (errNoEntry);
// a present-but-invalid marker is corruption (ErrCacheCorrupt).
func (m *Materializer) validateHit(b FrozenSkillBundle, entryDir string) (*VerifiedSkillBundle, error) {
	marker, err := readMarker(entryDir)
	if err != nil {
		return nil, err
	}
	if marker.Format != skillpkg.FormatName || marker.FormatVersion != skillpkg.FormatVersion {
		return nil, wrapMsg(ErrCacheCorrupt, "marker format %q v%d", marker.Format, marker.FormatVersion)
	}
	if marker.DigestAlgo != skillstore.AlgorithmSHA256 || marker.ContentDigest != b.ContentDigest {
		return nil, wrapMsg(ErrCacheCorrupt, "marker identity mismatch")
	}
	return &VerifiedSkillBundle{
		ContentDigest: b.ContentDigest,
		DigestAlgo:    b.ContentDigestAlgo,
		CacheDir:      entryDir,
	}, nil
}

// readMarker reads and decodes the marker for an entry directory. A missing marker/entry is a miss
// (errNoEntry); an unreadable or unparsable marker is corruption (ErrCacheCorrupt).
func readMarker(entryDir string) (entryMarker, error) {
	data, err := os.ReadFile(filepath.Join(entryDir, markerFileName))
	if err != nil {
		if os.IsNotExist(err) {
			return entryMarker{}, errNoEntry
		}
		return entryMarker{}, wrapMsg(ErrCacheCorrupt, "read marker: %v", err)
	}
	var m entryMarker
	if err := json.Unmarshal(data, &m); err != nil {
		return entryMarker{}, wrapMsg(ErrCacheCorrupt, "decode marker: %v", err)
	}
	return m, nil
}

// writeMarker writes the verified-marker payload into a staging directory, before the atomic rename, so
// the marker is committed atomically with the tree it attests.
func writeMarker(dir string, b FrozenSkillBundle) error {
	m := entryMarker{
		Format:        skillpkg.FormatName,
		FormatVersion: skillpkg.FormatVersion,
		DigestAlgo:    b.ContentDigestAlgo,
		ContentDigest: b.ContentDigest,
	}
	data, err := json.Marshal(m)
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, markerFileName), append(data, '\n'), 0o644)
}

// newStaging creates a fresh staging directory on the same filesystem as the cache root (always under
// <root>/.staging), so the final rename is atomic and never a cross-device copy. The returned cleanup
// removes the staging directory best-effort; after a successful publish the directory was renamed away,
// so cleanup is a no-op.
func (m *Materializer) newStaging(contentDigest string) (dir string, cleanup func(), err error) {
	stagingRoot := filepath.Join(m.cfg.CacheRoot, ".staging")
	if err := os.MkdirAll(stagingRoot, 0o755); err != nil {
		return "", func() {}, err
	}
	dir, err = os.MkdirTemp(stagingRoot, contentDigest+"-*")
	if err != nil {
		return "", func() {}, err
	}
	return dir, func() { _ = os.RemoveAll(dir) }, nil
}

// expandStaging writes the decoded canonical entries under the staging root as directories and regular
// files only. It never creates symlinks/hardlinks/devices/FIFOs, and it rejects any canonical tree that
// would collide with the reserved marker name. Every path was already validated by skillpkg.Decode (no
// absolute, "..", drive, separator escape, NUL, or case collision), so the join stays under the staging
// root; withinRoot guards the mapping a second time against filesystem drift.
func (m *Materializer) expandStaging(root string, bundle *skillpkg.Bundle) error {
	body := packageBody(bundle.Package, bundle.Manifest)
	var offset uint64
	for _, e := range bundle.Entries {
		if e.Path == markerFileName {
			return fmt.Errorf("skillruntime: reserved marker name %q in canonical tree", e.Path)
		}
		end := offset + e.Size
		if end > uint64(len(body)) {
			return fmt.Errorf("skillruntime: file %q exceeds package body", e.Path)
		}
		data := body[offset:end]
		offset = end

		rel := filepath.FromSlash(e.Path)
		dst := filepath.Join(root, rel)
		if !withinRoot(root, dst) {
			return fmt.Errorf("skillruntime: path %q escapes staging root", e.Path)
		}
		if dir := filepath.Dir(dst); dir != root {
			if err := os.MkdirAll(dir, 0o755); err != nil {
				return err
			}
		}
		if err := os.WriteFile(dst, data, 0o644); err != nil {
			return err
		}
	}
	return nil
}

// packageBody returns the concatenated file-body region of an ora-skill-package: everything after the
// 18-byte header (magic + version + manifest length) and the manifest. It relies on the canonical
// layout the single codec already verified, so it never re-parses any length field.
func packageBody(pkg, manifest []byte) []byte {
	const headerSize = len(skillpkg.Magic) + 2 + 8
	return pkg[headerSize+len(manifest):]
}

// withinRoot reports whether dst is the root or lies strictly beneath it, guarding the mapping against
// any unexpected separator or traversal drift after FromSlash.
func withinRoot(root, dst string) bool {
	rel, err := filepath.Rel(root, dst)
	if err != nil {
		return false
	}
	return rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

// publishEntry atomically commits the fully-verified staged tree (with its marker) into the final entry
// directory. The rename is same-filesystem because staging lives under the same root; it publishes the
// tree and marker together, so a reader sees both or neither. If the rename fails because another
// process already published a valid entry, that entry is reused; anything else fails closed with no
// in-place mutation.
func publishEntry(staging, entryDir string, b FrozenSkillBundle, m *Materializer) error {
	if err := os.MkdirAll(filepath.Dir(entryDir), 0o755); err != nil {
		return wrapMsg(ErrCachePublish, "create cache parent: %v", err)
	}
	if err := os.Rename(staging, entryDir); err == nil {
		return nil
	}
	// The rename did not take effect. Reuse a pre-existing valid entry if one is there; otherwise this
	// is a genuine publication failure (fail closed — the pre-existing state is never mutated).
	if _, verr := m.validateHit(b, entryDir); verr == nil {
		return nil
	}
	return wrapMsg(ErrCachePublish, "atomic rename of verified entry failed")
}
