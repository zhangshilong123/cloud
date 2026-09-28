// Package skillruntime implements the production server-side Skill materialization runtime frozen in
// specs/decisions/cloud/skills/20260928-web-runtime-skill-materialization-ownership.md: consume a
// short-lived RetrievalCapability, download the exact immutable package bytes, verify package_digest,
// decode through the single canonical codec internal/skillpkg, verify content_digest, and publish a
// verified immutable cache entry keyed by (digest_algorithm, content_digest).
//
// It is the smallest production-oriented boundary for the Server-side retrieval/cache effects. It owns
// only download + verify + cache: no database, no revision resolution, no execution admission, no
// capability minting, no projection, no READY barrier, no spawn, and no Controller byte-proxy — those
// belong to other slices. It never re-resolves mutable Skill state: its input is the already-frozen
// bundle metadata plus a caller-supplied bearer capability.
package skillruntime

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"path/filepath"
	"strings"
	"time"

	"github.com/wanglongan587/cloud/internal/skillpkg"
	"github.com/wanglongan587/cloud/internal/skillstore"
)

// defaultRetrievalConnectTimeout / defaultRetrievalRequestTimeout bound the download HTTP client when
// Config leaves the corresponding timeout unset; they mirror the storage section's defaults.
const (
	defaultRetrievalConnectTimeout = 5 * time.Second
	defaultRetrievalRequestTimeout = 30 * time.Second
)

// Config is the explicit, injection-friendly materializer configuration. Every value is concrete at
// construction: cmd/server translates the resolved `runtime` config section here, so this package never
// imports internal/config (mirroring the s3store.Config boundary).
type Config struct {
	// CacheRoot is the filesystem root under which verified immutable cache entries live
	// (<root>/sha256/<content_digest>/). It must be non-empty; there is no default because a shared
	// machine path must never be invented (plan §19).
	CacheRoot string

	// ConnectTimeout bounds establishing a TCP/TLS connection to the signed URL.
	ConnectTimeout time.Duration

	// RequestTimeout bounds each download end-to-end; the caller's context deadline still wins.
	RequestTimeout time.Duration

	// Limits bounds the download and decode. Zero fields fall back to skillpkg.DefaultLimits; the
	// download bound is Limits.MaxPackageBytes.
	Limits skillpkg.Limits
}

// normalize applies defaults so a partially populated Config behaves predictably.
func (c *Config) normalize() {
	if c.ConnectTimeout == 0 {
		c.ConnectTimeout = defaultRetrievalConnectTimeout
	}
	if c.RequestTimeout == 0 {
		c.RequestTimeout = defaultRetrievalRequestTimeout
	}
	c.Limits = normalizeLimits(c.Limits)
}

// normalizeLimits fills zero (unset) bound fields from skillpkg.DefaultLimits. The codec's own
// normalization is unexported, so the runtime mirrors it here; this matters because the download bound
// is Limits.MaxPackageBytes and must be non-zero even when Config leaves Limits fully zero-valued.
func normalizeLimits(l skillpkg.Limits) skillpkg.Limits {
	d := skillpkg.DefaultLimits()
	if l.MaxFileCount <= 0 {
		l.MaxFileCount = d.MaxFileCount
	}
	if l.MaxPathBytes == 0 {
		l.MaxPathBytes = d.MaxPathBytes
	}
	if l.MaxPathComponentBytes == 0 {
		l.MaxPathComponentBytes = d.MaxPathComponentBytes
	}
	if l.MaxSingleFileBytes == 0 {
		l.MaxSingleFileBytes = d.MaxSingleFileBytes
	}
	if l.MaxTotalBytes == 0 {
		l.MaxTotalBytes = d.MaxTotalBytes
	}
	if l.MaxManifestBytes == 0 {
		l.MaxManifestBytes = d.MaxManifestBytes
	}
	if l.MaxPackageBytes == 0 {
		l.MaxPackageBytes = d.MaxPackageBytes
	}
	if l.MaxSkillMDBytes == 0 {
		l.MaxSkillMDBytes = d.MaxSkillMDBytes
	}
	return l
}

// FrozenSkillBundle is the already-frozen input a materialization runs from: the immutable delivery
// metadata plus the exact physical package identity. It carries no capability and no mutable Skill
// state. It is the runtime-boundary mirror of SkillBundleRef; the runtime resolves nothing from it.
type FrozenSkillBundle struct {
	SkillRevisionID   string
	ContentDigestAlgo string
	ContentDigest     string
	PackageDigestAlgo string
	PackageDigest     string
	PackageFormat     string
	PackageFormatVer  int
	SizeBytes         int64
}

// VerifiedSkillBundle is the immutable output of a successful materialization: the content identity and
// the published cache entry location. It is a read-only reference to the verified immutable tree, never
// an Attempt projection. CacheDir contains the expanded canonical tree exactly as materialized.
type VerifiedSkillBundle struct {
	// ContentDigest is the lowercase 64-hex tree content digest — the cache identity.
	ContentDigest string
	// DigestAlgo is the content digest algorithm (always "sha256" after validation).
	DigestAlgo string
	// CacheDir is the published final cache entry directory; its files are the verified canonical tree.
	CacheDir string
}

// Materializer downloads, verifies, and caches canonical Skill packages. It is safe for concurrent use:
// a per-digest single-flight coalesces simultaneous requests, and the atomic rename publish plus the
// content-complete marker make cross-process publication idempotent.
type Materializer struct {
	cfg        Config
	client     *http.Client
	flights    *flightGroup
	contentAlg string
}

// New validates cfg and returns a ready-to-use Materializer. Validation never reaches the network.
func New(cfg Config) (*Materializer, error) {
	cfg.normalize()
	if strings.TrimSpace(cfg.CacheRoot) == "" {
		return nil, fmt.Errorf("skillruntime: cache root is required")
	}
	if cfg.ConnectTimeout <= 0 {
		return nil, fmt.Errorf("skillruntime: connect timeout must be positive")
	}
	if cfg.RequestTimeout <= 0 {
		return nil, fmt.Errorf("skillruntime: request timeout must be positive")
	}
	client, err := newDownloadClient(cfg.ConnectTimeout)
	if err != nil {
		return nil, err
	}
	return &Materializer{
		cfg:        cfg,
		client:     client,
		flights:    newFlightGroup(),
		contentAlg: skillstore.AlgorithmSHA256,
	}, nil
}

// EnsureVerified returns a verified immutable cache entry for the frozen bundle, downloading through
// the supplied bearer capability only on a cache miss. The full verification chain runs before any
// final cache-visible state exists: exact bytes → package_digest → canonical decode → content_digest →
// content total. No external effect of any kind runs while a database transaction is held (the caller
// owns that boundary; this package performs no database work).
func (m *Materializer) EnsureVerified(ctx context.Context, b FrozenSkillBundle, capability skillstore.RetrievalCapability) (*VerifiedSkillBundle, error) {
	if err := validateBundle(&b); err != nil {
		return nil, err
	}
	entryDir := m.entryDirFor(b.ContentDigest)

	// Single-flight per content digest: concurrent requests for the same digest coalesce so at most one
	// population runs; every waiter re-checks the published entry. finish is the leader's closer and a
	// no-op for waiters.
	finish := m.flights.begin(b.ContentDigest)
	defer finish()

	vb, err := m.validateHit(b, entryDir)
	if err == nil {
		return vb, nil
	}
	if errors.Is(err, ErrCacheCorrupt) {
		// A present-but-invalid entry is corruption: fail closed and never re-download over it.
		return nil, err
	}

	pkg, err := m.retrieve(ctx, capability, m.cfg.Limits.MaxPackageBytes)
	if err != nil {
		return nil, err
	}
	return m.verifyAndStage(b, pkg, entryDir)
}

// verifyAndStage verifies the downloaded bytes and, on success, atomically publishes them into the
// final cache entry. On any verification failure it leaves no final entry and cleans the staging tree.
func (m *Materializer) verifyAndStage(b FrozenSkillBundle, pkg []byte, entryDir string) (*VerifiedSkillBundle, error) {
	// package_digest: the physical byte identity over the exact downloaded bytes, distinct from the tree
	// content digest below.
	if skillstore.PackageDigestHex(pkg) != b.PackageDigest {
		return nil, wrapMsg(ErrPackageDigestMismatch, "package bytes digest mismatch")
	}
	bundle, err := skillpkg.Decode(pkg, m.cfg.Limits)
	if err != nil {
		return nil, wrapMsg(ErrDecodeFailed, "%v", err)
	}
	if bundle.TreeDigestHex() != b.ContentDigest {
		return nil, wrapMsg(ErrContentDigestMismatch, "decoded tree content digest mismatch")
	}
	var total uint64
	for _, e := range bundle.Entries {
		total += e.Size
	}
	if int64(total) != b.SizeBytes {
		return nil, wrapMsg(ErrSizeMismatch, "content total %d != frozen size_bytes %d", total, b.SizeBytes)
	}

	staging, cleanup, err := m.newStaging(b.ContentDigest)
	if err != nil {
		return nil, wrapMsg(ErrCachePublish, "create staging: %v", err)
	}
	defer cleanup()

	if err := m.expandStaging(staging, bundle); err != nil {
		return nil, wrapMsg(ErrCachePublish, "expand staging: %v", err)
	}
	if err := writeMarker(staging, b); err != nil {
		return nil, wrapMsg(ErrCachePublish, "write marker: %v", err)
	}
	if err := publishEntry(staging, entryDir, b, m); err != nil {
		return nil, err
	}
	return &VerifiedSkillBundle{
		ContentDigest: b.ContentDigest,
		DigestAlgo:    b.ContentDigestAlgo,
		CacheDir:      entryDir,
	}, nil
}

// validateBundle rejects an unusable frozen input before any network or filesystem work: unknown digest
// algorithms, unknown package format/version, missing identities, and a non-positive size are all
// fail-closed. It never reaches mutable Skill state.
func validateBundle(b *FrozenSkillBundle) error {
	if b == nil {
		return fmt.Errorf("skillruntime: nil frozen bundle")
	}
	if b.ContentDigestAlgo != skillstore.AlgorithmSHA256 || b.PackageDigestAlgo != skillstore.AlgorithmSHA256 {
		return wrapMsg(ErrUnsupportedDigestAlgorithm, "content %q / package %q", b.ContentDigestAlgo, b.PackageDigestAlgo)
	}
	if b.PackageFormat != skillpkg.FormatName || b.PackageFormatVer != skillpkg.FormatVersion {
		return wrapMsg(ErrUnsupportedPackageFormat, "%q v%d", b.PackageFormat, b.PackageFormatVer)
	}
	if !isLowerHex64(b.ContentDigest) || !isLowerHex64(b.PackageDigest) {
		return fmt.Errorf("skillruntime: content/package digest must be 64 lowercase hex chars")
	}
	if b.SizeBytes < 0 {
		return fmt.Errorf("skillruntime: size_bytes must be >= 0")
	}
	return nil
}

// entryDirFor is the final cache entry path for a content digest: <root>/sha256/<content_digest>. The
// digest is the single identity; no business/revision/attempt/package identity ever enters the key.
func (m *Materializer) entryDirFor(contentDigest string) string {
	return filepath.Join(m.cfg.CacheRoot, m.contentAlg, contentDigest)
}

// isLowerHex64 reports whether s is exactly 64 lowercase hexadecimal characters.
func isLowerHex64(s string) bool {
	if len(s) != 64 {
		return false
	}
	for i := 0; i < len(s); i++ {
		switch c := s[i]; {
		case c >= '0' && c <= '9':
		case c >= 'a' && c <= 'f':
		default:
			return false
		}
	}
	return true
}
