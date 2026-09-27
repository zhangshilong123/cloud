package fakestore_test

import (
	"context"
	"testing"

	"github.com/wanglongan587/cloud/internal/skillstore"
	"github.com/wanglongan587/cloud/internal/skillstore/fakestore"
)

func TestFakePutIsCreateOnly(t *testing.T) {
	ctx := context.Background()
	var f fakestore.Fake

	bytesA := []byte("first content")
	digestA := skillstore.PackageDigestHex(bytesA)
	loc, err := skillstore.NewLocator("ora-skill-package", 1, "sha256", digestA)
	if err != nil {
		t.Fatalf("NewLocator: %v", err)
	}
	req := skillstore.PutRequest{Locator: loc, Bytes: bytesA}

	if got := f.PutImmutable(ctx, &req).Outcome; got != skillstore.PutCreated {
		t.Fatalf("first PutImmutable = %v, want PutCreated", got)
	}
	if got := f.PutImmutable(ctx, &req).Outcome; got != skillstore.PutAlreadyExists {
		t.Fatalf("second PutImmutable = %v, want PutAlreadyExists", got)
	}

	// Different bytes at the same key still report already-exists...
	reqOther := req
	reqOther.Bytes = []byte("different content at the same key")
	if got := f.PutImmutable(ctx, &reqOther).Outcome; got != skillstore.PutAlreadyExists {
		t.Fatalf("different-bytes PutImmutable = %v, want PutAlreadyExists", got)
	}

	// ...and the existing bytes are never overwritten.
	got := f.Get(ctx, loc, uint64(len(bytesA)))
	if got.Outcome != skillstore.GetPresent {
		t.Fatalf("Get = %v, want GetPresent", got.Outcome)
	}
	if string(got.Bytes) != string(bytesA) {
		t.Fatalf("Get bytes = %q, want %q (existing object must not be overwritten)", got.Bytes, bytesA)
	}
}

func TestFakeGetIsBounded(t *testing.T) {
	ctx := context.Background()
	var f fakestore.Fake
	loc, err := skillstore.NewLocator("ora-skill-package", 1, "sha256", skillstore.PackageDigestHex([]byte("seed")))
	if err != nil {
		t.Fatalf("NewLocator: %v", err)
	}
	if !f.Seed(loc, []byte("0123456789")) {
		t.Fatal("Seed failed unexpectedly")
	}
	if got := f.Get(ctx, loc, 5); got.Outcome != skillstore.GetOversize {
		t.Fatalf("Get with small bound = %v, want GetOversize", got.Outcome)
	}
	if got := f.Get(ctx, loc, 10); got.Outcome != skillstore.GetPresent {
		t.Fatalf("Get with adequate bound = %v, want GetPresent", got.Outcome)
	}
}

func TestFakeSeedIsCreateOnly(t *testing.T) {
	var f fakestore.Fake
	loc, err := skillstore.NewLocator("ora-skill-package", 1, "sha256", skillstore.PackageDigestHex([]byte("seed")))
	if err != nil {
		t.Fatalf("NewLocator: %v", err)
	}
	if !f.Seed(loc, []byte("first")) {
		t.Fatal("first Seed should report a new write")
	}
	if f.Seed(loc, []byte("second")) {
		t.Fatal("second Seed should report no overwrite")
	}
	got := f.Get(context.Background(), loc, 10)
	if string(got.Bytes) != "first" {
		t.Fatalf("Get bytes = %q, want %q (Seed must not overwrite)", got.Bytes, "first")
	}
}

func TestPutOutcomeClassesAreDistinct(t *testing.T) {
	seen := map[skillstore.PutOutcome]bool{}
	for _, o := range []skillstore.PutOutcome{
		skillstore.PutCreated,
		skillstore.PutAlreadyExists,
		skillstore.PutDefiniteFailureTransient,
		skillstore.PutDefiniteFailurePermanent,
		skillstore.PutAmbiguous,
	} {
		if seen[o] {
			t.Fatalf("duplicate PutOutcome value %v", o)
		}
		seen[o] = true
	}
	if skillstore.PutDefiniteFailureTransient == skillstore.PutAmbiguous {
		t.Fatal("transient definite failure must be distinct from ambiguous")
	}
	if skillstore.PutDefiniteFailurePermanent == skillstore.PutDefiniteFailureTransient {
		t.Fatal("permanent definite failure must be distinct from transient")
	}
}
