package skillstore_test

import (
	"context"
	"strings"
	"testing"

	"github.com/wanglongan587/cloud/internal/skillpkg"
	"github.com/wanglongan587/cloud/internal/skillstore"
	"github.com/wanglongan587/cloud/internal/skillstore/fakestore"
)

// matchingFixture builds a valid canonical package and returns both the durable
// Want it corresponds to and the exact package bytes to plant in a store.
func matchingFixture(t *testing.T) (skillstore.Want, []byte) {
	t.Helper()
	bundle, err := skillpkg.Build(
		[]skillpkg.SourceFile{{Path: "SKILL.md", Data: []byte("# Demo\n")}},
		skillpkg.Limits{},
	)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	digest := skillstore.PackageDigestHex(bundle.Package)
	loc, err := skillstore.NewLocator(skillpkg.FormatName, skillpkg.FormatVersion, skillstore.AlgorithmSHA256, digest)
	if err != nil {
		t.Fatalf("NewLocator: %v", err)
	}
	want := skillstore.Want{
		Locator:       loc,
		PackageDigest: digest,
		ContentDigest: bundle.TreeDigestHex(),
		Limits:        skillpkg.Limits{},
	}
	return want, bundle.Package
}

func TestReconcileAbsent(t *testing.T) {
	var store fakestore.Fake
	want, _ := matchingFixture(t)
	if got := skillstore.Reconcile(context.Background(), &store, &want); got != skillstore.VerdictConfirmedAbsent {
		t.Fatalf("Reconcile = %v, want VerdictConfirmedAbsent", got)
	}
}

func TestReconcileMatching(t *testing.T) {
	var store fakestore.Fake
	want, pkg := matchingFixture(t)
	if !store.Seed(want.Locator, pkg) {
		t.Fatal("Seed failed unexpectedly")
	}
	if got := skillstore.Reconcile(context.Background(), &store, &want); got != skillstore.VerdictConfirmedPresentMatching {
		t.Fatalf("Reconcile = %v, want VerdictConfirmedPresentMatching", got)
	}
}

func TestReconcileWrongPackageSHA(t *testing.T) {
	var store fakestore.Fake
	want, _ := matchingFixture(t)
	if !store.Seed(want.Locator, []byte("wrong bytes at this key")) {
		t.Fatal("Seed failed unexpectedly")
	}
	if got := skillstore.Reconcile(context.Background(), &store, &want); got != skillstore.VerdictMismatch {
		t.Fatalf("Reconcile = %v, want VerdictMismatch", got)
	}
}

func TestReconcileMalformedPackage(t *testing.T) {
	// Bytes whose SHA-256 equals the expected package digest but which are not a
	// decodable ora-skill-package: the byte check passes, the decode check fails.
	var store fakestore.Fake
	malformed := []byte("not an ora-skill-package")
	digest := skillstore.PackageDigestHex(malformed)
	loc, err := skillstore.NewLocator(skillpkg.FormatName, skillpkg.FormatVersion, skillstore.AlgorithmSHA256, digest)
	if err != nil {
		t.Fatalf("NewLocator: %v", err)
	}
	want := skillstore.Want{
		Locator:       loc,
		PackageDigest: digest,
		ContentDigest: strings.Repeat("0", 64),
		Limits:        skillpkg.Limits{},
	}
	if !store.Seed(loc, malformed) {
		t.Fatal("Seed failed unexpectedly")
	}
	if got := skillstore.Reconcile(context.Background(), &store, &want); got != skillstore.VerdictMismatch {
		t.Fatalf("Reconcile = %v, want VerdictMismatch", got)
	}
}

func TestReconcileWrongTreeDigest(t *testing.T) {
	// A valid, correctly-addressed package whose decoded tree digest differs from
	// the expected content digest: the byte and decode checks pass, the tree check
	// fails.
	var store fakestore.Fake
	want, pkg := matchingFixture(t)
	if !store.Seed(want.Locator, pkg) {
		t.Fatal("Seed failed unexpectedly")
	}
	want.ContentDigest = strings.Repeat("f", 64)
	if got := skillstore.Reconcile(context.Background(), &store, &want); got != skillstore.VerdictMismatch {
		t.Fatalf("Reconcile = %v, want VerdictMismatch", got)
	}
}

func TestReconcileStatIndeterminate(t *testing.T) {
	var store fakestore.Fake
	store.StatFn = func(context.Context, skillstore.Locator) skillstore.StatResult {
		return skillstore.StatResult{Outcome: skillstore.StatIndeterminate}
	}
	want, _ := matchingFixture(t)
	if got := skillstore.Reconcile(context.Background(), &store, &want); got != skillstore.VerdictIndeterminate {
		t.Fatalf("Reconcile = %v, want VerdictIndeterminate", got)
	}
}

func TestReconcileStatDefiniteFailure(t *testing.T) {
	var store fakestore.Fake
	store.StatFn = func(context.Context, skillstore.Locator) skillstore.StatResult {
		return skillstore.StatResult{Outcome: skillstore.StatDefiniteFailurePermanent}
	}
	want, _ := matchingFixture(t)
	if got := skillstore.Reconcile(context.Background(), &store, &want); got != skillstore.VerdictDefiniteFailurePermanent {
		t.Fatalf("Reconcile = %v, want VerdictDefiniteFailurePermanent", got)
	}
}

func TestReconcileGetIndeterminate(t *testing.T) {
	var store fakestore.Fake
	want, pkg := matchingFixture(t)
	if !store.Seed(want.Locator, pkg) {
		t.Fatal("Seed failed unexpectedly")
	}
	store.GetFn = func(context.Context, skillstore.Locator, uint64) skillstore.GetResult {
		return skillstore.GetResult{Outcome: skillstore.GetIndeterminate}
	}
	if got := skillstore.Reconcile(context.Background(), &store, &want); got != skillstore.VerdictIndeterminate {
		t.Fatalf("Reconcile = %v, want VerdictIndeterminate", got)
	}
}

func TestReconcileOversized(t *testing.T) {
	var store fakestore.Fake
	want, _ := matchingFixture(t)
	if !store.Seed(want.Locator, make([]byte, 128)) {
		t.Fatal("Seed failed unexpectedly")
	}
	want.Limits = skillpkg.Limits{MaxPackageBytes: 16}
	if got := skillstore.Reconcile(context.Background(), &store, &want); got != skillstore.VerdictMismatch {
		t.Fatalf("Reconcile = %v, want VerdictMismatch", got)
	}
}

func TestRecoveryAmbiguousThenReconcile(t *testing.T) {
	ctx := context.Background()

	t.Run("landed correctly", func(t *testing.T) {
		var store fakestore.Fake
		want, pkg := matchingFixture(t)
		store.PutFn = func(context.Context, *skillstore.PutRequest) skillstore.PutResult {
			return skillstore.PutResult{Outcome: skillstore.PutAmbiguous}
		}
		if got := store.PutImmutable(ctx, &skillstore.PutRequest{Locator: want.Locator}).Outcome; got != skillstore.PutAmbiguous {
			t.Fatalf("PutImmutable = %v, want PutAmbiguous", got)
		}
		if !store.Seed(want.Locator, pkg) {
			t.Fatal("Seed failed unexpectedly")
		}
		if got := skillstore.Reconcile(ctx, &store, &want); got != skillstore.VerdictConfirmedPresentMatching {
			t.Fatalf("Reconcile = %v, want VerdictConfirmedPresentMatching", got)
		}
	})

	t.Run("never landed", func(t *testing.T) {
		var store fakestore.Fake
		want, _ := matchingFixture(t)
		store.PutFn = func(context.Context, *skillstore.PutRequest) skillstore.PutResult {
			return skillstore.PutResult{Outcome: skillstore.PutAmbiguous}
		}
		if got := store.PutImmutable(ctx, &skillstore.PutRequest{Locator: want.Locator}).Outcome; got != skillstore.PutAmbiguous {
			t.Fatalf("PutImmutable = %v, want PutAmbiguous", got)
		}
		if got := skillstore.Reconcile(ctx, &store, &want); got != skillstore.VerdictConfirmedAbsent {
			t.Fatalf("Reconcile = %v, want VerdictConfirmedAbsent", got)
		}
	})

	t.Run("landed wrong bytes", func(t *testing.T) {
		var store fakestore.Fake
		want, _ := matchingFixture(t)
		store.PutFn = func(context.Context, *skillstore.PutRequest) skillstore.PutResult {
			return skillstore.PutResult{Outcome: skillstore.PutAmbiguous}
		}
		if got := store.PutImmutable(ctx, &skillstore.PutRequest{Locator: want.Locator}).Outcome; got != skillstore.PutAmbiguous {
			t.Fatalf("PutImmutable = %v, want PutAmbiguous", got)
		}
		if !store.Seed(want.Locator, []byte("corrupt")) {
			t.Fatal("Seed failed unexpectedly")
		}
		if got := skillstore.Reconcile(ctx, &store, &want); got != skillstore.VerdictMismatch {
			t.Fatalf("Reconcile = %v, want VerdictMismatch", got)
		}
	})
}
