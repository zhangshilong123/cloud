package skillruntime

import (
	"errors"
	"fmt"
)

// Error sentinels (classify failures with errors.Is). Each transports a safe, credential-free
// class; the signed RetrievalCapability URL, signature query, and storage credentials must never
// appear in any error text or structured field (ADR D6/D7, plan §6/§37).
var (
	// ErrUnsupportedDigestAlgorithm reports a content/package digest algorithm other than "sha256"
	// (the only algorithm the V1 codec approves). Fail closed.
	ErrUnsupportedDigestAlgorithm = errors.New("skillruntime: unsupported digest algorithm")

	// ErrUnsupportedPackageFormat reports a package_format or package_format_version other than the
	// exact current canonical family ("ora-skill-package" v1). No best-effort decode of future formats.
	ErrUnsupportedPackageFormat = errors.New("skillruntime: unsupported package format")

	// ErrRetrievalUnauthorized reports a 401/403 or an already-expired capability. It is the
	// "capability invalid/expired" class and is terminal for this capability (refresh belongs to 6B).
	ErrRetrievalUnauthorized = errors.New("skillruntime: retrieval unauthorized or expired")

	// ErrRetrievalNotFound reports a 404 exact-object-unavailable outcome.
	ErrRetrievalNotFound = errors.New("skillruntime: retrieval object not found")

	// ErrRetrievalRedirect reports a 3xx response. Redirects are never followed: the signed URL is a
	// bearer credential and must not be re-targeted by a hostile storage endpoint (plan §8).
	ErrRetrievalRedirect = errors.New("skillruntime: retrieval redirect not followed")

	// ErrRetrievalTransient reports a 5xx, network, timeout, or cancellation outcome that may clear.
	ErrRetrievalTransient = errors.New("skillruntime: retrieval transient failure")

	// ErrSizeMismatch reports an over-bound body or a decoded content total that does not equal the
	// frozen size_bytes (plan §9).
	ErrSizeMismatch = errors.New("skillruntime: package size mismatch")

	// ErrPackageDigestMismatch reports SHA256(downloaded bytes) != package_digest. The bytes are
	// rejected before any decode or cache publish.
	ErrPackageDigestMismatch = errors.New("skillruntime: package digest mismatch")

	// ErrDecodeFailed reports a structurally invalid package (skillpkg.Decode error). It wraps the
	// underlying skillpkg sentinel.
	ErrDecodeFailed = errors.New("skillruntime: package decode failed")

	// ErrContentDigestMismatch reports decoded TreeDigest != content_digest. Independent of, and never
	// a substitute for, ErrPackageDigestMismatch.
	ErrContentDigestMismatch = errors.New("skillruntime: content digest mismatch")

	// ErrCacheCorrupt reports a present-but-invalid cache entry (missing/invalid marker, or a structural
	// read failure). The entry is never returned, mutated, or quarantined in place.
	ErrCacheCorrupt = errors.New("skillruntime: cache entry corrupt")

	// ErrCachePublish reports a failure to atomically move a fully verified staged tree into the final
	// cache entry (rename/io failure). No partially verified tree is ever visible.
	ErrCachePublish = errors.New("skillruntime: cache publish failed")

	// ErrProjectionMismatch reports a verified bundle that does not correspond to the frozen bundle it
	// was paired with (ContentDigest / digest algorithm disagreement), or a frozen bundle that fails the
	// same fail-closed validation the materializer applies. A projection never substitutes content for a
	// Skill whose identity it does not carry.
	ErrProjectionMismatch = errors.New("skillruntime: projection input mismatch")

	// ErrProjectionCollision reports two required Skills resolving to the same runtime directory name
	// (same skill_revision_id). Duplicate content identity cannot be projected twice under one name;
	// it is a fail-closed collision, never a silently dropped or overwritten Skill.
	ErrProjectionCollision = errors.New("skillruntime: projection name collision")

	// ErrAttemptCorrupt reports an already-published Attempt projection whose ownership/READY marker is
	// missing, unparsable, or inconsistent with the Attempt identity. It is never overwritten in place
	// and never re-projected over (a retry is a new Attempt identity).
	ErrAttemptCorrupt = errors.New("skillruntime: attempt projection corrupt")

	// ErrNotReady reports that the READY barrier has not been passed: the Attempt has no published
	// projection, or its marker does not attest every required Skill. READY is explicit and is never
	// inferred from partial filesystem state.
	ErrNotReady = errors.New("skillruntime: attempt not ready")

	// ErrProjectionPublish reports a failure to atomically move a fully staged Attempt projection into
	// its final published location. No partially prepared projection is ever visible to a runtime.
	ErrProjectionPublish = errors.New("skillruntime: attempt projection publish failed")
)

// wrapMsg attaches a credential-free detail to a sentinel while preserving errors.Is via %w.
func wrapMsg(sentinel error, format string, args ...any) error {
	return fmt.Errorf("%w: %s", sentinel, fmt.Sprintf(format, args...))
}
