// Package skillstore implements the provider-neutral Object Storage semantics for
// canonical Skill packages, frozen in
// specs/decisions/cloud/skills/20260927-object-storage-abstraction.md.
//
// It owns only the write-and-verify side of the boundary: the physical package
// identity (package_digest), the stable logical object locator, a create-only
// ObjectStore port, and a provider-neutral reconciliation core. It never touches
// a concrete storage provider, the database, an HTTP handler, or the ingestion
// saga — those are later steps. The port is defined here at its consuming
// boundary and is implemented by a future provider adapter or by the in-memory
// test double in package fakestore.
package skillstore

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
)

// AlgorithmSHA256 is the digest algorithm produced by the canonical package for
// every file digest, tree digest, and package digest today.
const AlgorithmSHA256 = "sha256"

// PackageDigestHex returns the physical package identity: SHA-256 over the exact
// ora-skill-package bytes, as lowercase 64-hex. It is the package_digest of ADR
// D4 — distinct from the Step 2A tree content digest and never a business,
// revision, or cache identity.
func PackageDigestHex(b []byte) string {
	d := sha256.Sum256(b)
	return hex.EncodeToString(d[:])
}

// Error sentinels. Classify validation failures with errors.Is.
var (
	// ErrLocator reports an invalid object locator or locator component.
	ErrLocator = errors.New("skillstore: invalid object locator")
	// ErrRequest reports an internally inconsistent PutRequest.
	ErrRequest = errors.New("skillstore: inconsistent put request")
)

// ObjectStore is the provider-neutral, create-only object storage port consumed by
// the Skill ingestion saga. It is the semantic contract, not a provider SDK: a
// real adapter (future) or the in-memory Fake implements it. It deliberately has
// no List, Delete, Copy, Move, or Presign operation, and no provider-specific
// naming or credentials. Every operation returns a typed result whose outcome
// expresses the full failure taxonomy; a caller must branch on that outcome and
// never decide whether to retry from a Go error alone.
type ObjectStore interface {
	// PutImmutable creates the object at req.Locator exactly once. It never
	// overwrites an existing object; a provider without conditional-create support
	// must refuse to serve rather than degrade to an unconditional write.
	PutImmutable(ctx context.Context, req *PutRequest) PutResult

	// Stat reports existence only. It is the cheapest probe filter; only Get can
	// prove content correctness, so Stat's evidence is never identity.
	Stat(ctx context.Context, locator Locator) StatResult

	// Get returns the exact object bytes, bounded by maxBytes. It is the only
	// source of authoritative content for verification.
	Get(ctx context.Context, locator Locator, maxBytes uint64) GetResult
}

// PutOutcome classifies the result of an immutable create-only write.
type PutOutcome uint8

const (
	// PutCreated means the object did not exist and was written. It is not a
	// verification: the caller must still reconcile (ADR D7).
	PutCreated PutOutcome = iota
	// PutAlreadyExists means the locator is already occupied. It says nothing
	// about the existing object's content; it is a needs-verification outcome.
	PutAlreadyExists
	// PutDefiniteFailureTransient means the request provably had no effect for a
	// reason that may clear (DNS, connection refused, 429). A bounded retry is
	// admissible.
	PutDefiniteFailureTransient
	// PutDefiniteFailurePermanent means the request provably had no effect for a
	// reason that will not clear (auth/validation/size rejection). Terminal.
	PutDefiniteFailurePermanent
	// PutAmbiguous means the request may have been accepted; the outcome is
	// unknown and must be resolved by probe, never by a blind re-PUT.
	PutAmbiguous
)

// PutResult is the structured outcome of PutImmutable. The Outcome field is the
// authoritative classification; a bare Go error must not substitute for it.
type PutResult struct {
	Outcome PutOutcome
}

// StatOutcome classifies the result of a Stat probe.
type StatOutcome uint8

const (
	// StatPresent means the provider reports an object at the locator. The
	// accompanying Evidence is optional and never identity.
	StatPresent StatOutcome = iota
	// StatAbsent means the provider definitively reported not-found. A timeout or
	// "could not check" is never StatAbsent.
	StatAbsent
	// StatDefiniteFailureTransient means the probe provably could not run for a
	// reason that may clear.
	StatDefiniteFailureTransient
	// StatDefiniteFailurePermanent means the probe provably could not run for a
	// reason that will not clear (e.g. auth denied).
	StatDefiniteFailurePermanent
	// StatIndeterminate means the probe's outcome is unknown.
	StatIndeterminate
)

// Evidence is optional provider-supplied metadata about a stored object. It is
// diagnostic only and never identity: nothing in this package may conclude from
// Evidence that an object is the expected one (that requires Get plus the full
// verification in Reconcile). In particular an ETag must never be assumed to
// equal SHA-256.
type Evidence struct {
	// Size is the object byte length; -1 when unknown.
	Size int64
	// ETag is the provider's entity tag; "" when absent.
	ETag string
}

// StatResult is the structured outcome of Stat. Evidence is meaningful only when
// Outcome is StatPresent.
type StatResult struct {
	Outcome  StatOutcome
	Evidence Evidence
}

// GetOutcome classifies the result of a bounded Get.
type GetOutcome uint8

const (
	// GetPresent means the object was returned in full, within maxBytes.
	GetPresent GetOutcome = iota
	// GetAbsent means the provider definitively reported not-found.
	GetAbsent
	// GetOversize means the object exists but exceeds maxBytes. It is a
	// deterministic finding, not a transient error: reconciliation fails closed.
	GetOversize
	// GetDefiniteFailureTransient means the read provably could not run for a
	// reason that may clear.
	GetDefiniteFailureTransient
	// GetDefiniteFailurePermanent means the read provably could not run for a
	// reason that will not clear.
	GetDefiniteFailurePermanent
	// GetIndeterminate means the read's outcome is unknown.
	GetIndeterminate
)

// GetResult is the structured outcome of Get. Bytes is non-nil only when Outcome
// is GetPresent.
type GetResult struct {
	Outcome GetOutcome
	Bytes   []byte
}

// PutRequest is the complete immutable expectation for a create-only write. The
// adapter must not parse these fields back out of the locator string; the caller
// passes them explicitly (ADR D5).
type PutRequest struct {
	// Locator is the stable logical object key.
	Locator Locator
	// Bytes is the exact ora-skill-package v1 bytes to write. Step 2B writes the
	// in-memory bytes produced by skillpkg.Build; no streaming source is
	// introduced yet.
	Bytes []byte
	// ExpectedPackageDigest is SHA-256 of Bytes (lowercase 64-hex).
	ExpectedPackageDigest string
	// ExpectedPackageSize is len(Bytes).
	ExpectedPackageSize uint64
	// ExpectedContentDigest is the Step 2A tree digest (lowercase 64-hex).
	ExpectedContentDigest string
	// PackageFormat, PackageFormatVersion, and DigestAlgorithm name the package
	// family the key already encodes; they are carried explicitly so the adapter
	// never parses the key text.
	PackageFormat        string
	PackageFormatVersion int
	DigestAlgorithm      string
}

// Validate checks the request's internal consistency. It must succeed before any
// external call: the byte length, the byte digest, the locator's embedded digest,
// and the format/version/algorithm fields must all agree. A mismatch here is a
// programming error, not a storage outcome.
func (r *PutRequest) Validate() error {
	if uint64(len(r.Bytes)) != r.ExpectedPackageSize {
		return fmt.Errorf("%w: expected package size %d != len(bytes) %d", ErrRequest, r.ExpectedPackageSize, len(r.Bytes))
	}
	if PackageDigestHex(r.Bytes) != r.ExpectedPackageDigest {
		return fmt.Errorf("%w: package digest mismatch", ErrRequest)
	}
	if r.Locator.DigestHex() != r.ExpectedPackageDigest {
		return fmt.Errorf("%w: locator digest != expected package digest", ErrRequest)
	}
	if r.Locator.Format() != r.PackageFormat {
		return fmt.Errorf("%w: locator format %q != request format %q", ErrRequest, r.Locator.Format(), r.PackageFormat)
	}
	if r.Locator.Version() != r.PackageFormatVersion {
		return fmt.Errorf("%w: locator version %d != request version %d", ErrRequest, r.Locator.Version(), r.PackageFormatVersion)
	}
	if r.Locator.Algorithm() != r.DigestAlgorithm {
		return fmt.Errorf("%w: locator algorithm %q != request algorithm %q", ErrRequest, r.Locator.Algorithm(), r.DigestAlgorithm)
	}
	return nil
}
