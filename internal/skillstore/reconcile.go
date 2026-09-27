package skillstore

import (
	"context"

	"github.com/wanglongan587/cloud/internal/skillpkg"
)

// Verdict is the reconciled, durable conclusion of probing a stored object. It is
// the unified outcome classification of ADR D10, with the transient/permanent
// refinement carried through from the port.
type Verdict uint8

const (
	// VerdictConfirmedPresentMatching means the object exists and passed all three
	// verification checks. It is the only verdict that licenses DB TX #2.
	VerdictConfirmedPresentMatching Verdict = iota
	// VerdictConfirmedAbsent means the provider definitively reported not-found.
	VerdictConfirmedAbsent
	// VerdictMismatch means the object exists but is wrong. It is deterministic
	// and fails closed: never overwrite, delete, adopt, or retry the write.
	VerdictMismatch
	// VerdictDefiniteFailureTransient means the probe provably could not run for a
	// reason that may clear; existence is still unknown but a bounded retry is
	// admissible.
	VerdictDefiniteFailureTransient
	// VerdictDefiniteFailurePermanent means the probe provably could not run for a
	// reason that will not clear. Terminal.
	VerdictDefiniteFailurePermanent
	// VerdictIndeterminate means the probe's outcome is unknown; only a further
	// probe (never a blind write) can converge it.
	VerdictIndeterminate
)

// Want is the durable expectation a reconciliation is checking: the persisted
// logical locator plus the expected package and content identity derived from the
// canonical tree before any external call.
type Want struct {
	// Locator is the persisted object_locator (the probe target). It is used
	// exactly as given; reconciliation never re-derives a key from content.
	Locator Locator
	// PackageDigest is the expected SHA-256 of the exact package bytes (lowercase
	// 64-hex).
	PackageDigest string
	// ContentDigest is the expected Step 2A tree digest (lowercase 64-hex).
	ContentDigest string
	// Limits bounds the Get and the package decode.
	Limits skillpkg.Limits
}

// Reconcile probes the stored object at want.Locator and returns the reconciled
// verdict. It is provider-neutral: it uses only Stat and a bounded Get plus local
// verification, never a provider checksum, List, or inferred state.
func Reconcile(ctx context.Context, store ObjectStore, want *Want) Verdict {
	st := store.Stat(ctx, want.Locator)
	switch st.Outcome {
	case StatAbsent:
		return VerdictConfirmedAbsent
	case StatPresent:
		return reconcilePresent(ctx, store, want)
	case StatDefiniteFailureTransient:
		return VerdictDefiniteFailureTransient
	case StatDefiniteFailurePermanent:
		return VerdictDefiniteFailurePermanent
	default:
		return VerdictIndeterminate
	}
}

// reconcilePresent continues a reconciliation after Stat reported presence: it
// reads the object within the package bound and verifies it against the durable
// expectation.
func reconcilePresent(ctx context.Context, store ObjectStore, want *Want) Verdict {
	got := store.Get(ctx, want.Locator, maxPackageBytes(want.Limits))
	switch got.Outcome {
	case GetPresent:
		return verify(got.Bytes, want)
	case GetAbsent:
		// The object vanished between Stat and Get: existence is now unknown.
		return VerdictIndeterminate
	case GetOversize:
		// Deterministically larger than any valid package: wrong, not transient.
		return VerdictMismatch
	case GetDefiniteFailureTransient:
		return VerdictDefiniteFailureTransient
	case GetDefiniteFailurePermanent:
		return VerdictDefiniteFailurePermanent
	default:
		return VerdictIndeterminate
	}
}

// verify applies the three ADR D9 checks in order: byte digest, structural decode,
// and tree content digest. Any failure is a deterministic mismatch, never a retry.
func verify(data []byte, want *Want) Verdict {
	if PackageDigestHex(data) != want.PackageDigest {
		return VerdictMismatch
	}
	bundle, err := skillpkg.Decode(data, want.Limits)
	if err != nil {
		return VerdictMismatch
	}
	if bundle.TreeDigestHex() != want.ContentDigest {
		return VerdictMismatch
	}
	return VerdictConfirmedPresentMatching
}

// maxPackageBytes returns the byte bound a reconciliation Get must honor, falling
// back to the library default when the caller left the limit unset.
func maxPackageBytes(l skillpkg.Limits) uint64 {
	if l.MaxPackageBytes != 0 {
		return l.MaxPackageBytes
	}
	return skillpkg.DefaultLimits().MaxPackageBytes
}
