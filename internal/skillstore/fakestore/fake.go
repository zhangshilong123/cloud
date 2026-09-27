// Package fakestore provides a deterministic in-memory skillstore.ObjectStore for
// tests. It is a test double, not a production provider: it enforces create-only
// semantics under a mutex and can be scripted to return every failure mode the
// reconciliation contract requires (ADR D25).
package fakestore

import (
	"context"
	"sync"

	"github.com/wanglongan587/cloud/internal/skillstore"
)

// Fake is a deterministic in-memory skillstore.ObjectStore. Its zero value is
// ready to use. By default it behaves honestly: PutImmutable is an atomic
// create-only write, Stat reports present/absent with a size hint, and Get returns
// the exact stored bytes or absent. Tests override PutFn, StatFn, or GetFn to
// inject scripted outcomes.
type Fake struct {
	mu      sync.Mutex
	objects map[string][]byte

	// PutFn, when non-nil, replaces the default PutImmutable behavior. It receives
	// the request so a test can assert the immutable expectation is passed through,
	// and may return any PutOutcome.
	PutFn func(ctx context.Context, req *skillstore.PutRequest) skillstore.PutResult
	// StatFn, when non-nil, replaces the default Stat behavior.
	StatFn func(ctx context.Context, locator skillstore.Locator) skillstore.StatResult
	// GetFn, when non-nil, replaces the default Get behavior.
	GetFn func(ctx context.Context, locator skillstore.Locator, maxBytes uint64) skillstore.GetResult
}

// PutImmutable implements skillstore.ObjectStore with create-only semantics.
func (f *Fake) PutImmutable(ctx context.Context, req *skillstore.PutRequest) skillstore.PutResult {
	if f.PutFn != nil {
		return f.PutFn(ctx, req)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	key := req.Locator.String()
	if _, exists := f.objects[key]; exists {
		return skillstore.PutResult{Outcome: skillstore.PutAlreadyExists}
	}
	if f.objects == nil {
		f.objects = make(map[string][]byte)
	}
	// Store a copy so a later mutation of req.Bytes cannot corrupt the store.
	f.objects[key] = append([]byte(nil), req.Bytes...)
	return skillstore.PutResult{Outcome: skillstore.PutCreated}
}

// Stat implements skillstore.ObjectStore.
func (f *Fake) Stat(ctx context.Context, locator skillstore.Locator) skillstore.StatResult {
	if f.StatFn != nil {
		return f.StatFn(ctx, locator)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	data, exists := f.objects[locator.String()]
	if !exists {
		return skillstore.StatResult{Outcome: skillstore.StatAbsent}
	}
	return skillstore.StatResult{
		Outcome:  skillstore.StatPresent,
		Evidence: skillstore.Evidence{Size: int64(len(data))},
	}
}

// Get implements skillstore.ObjectStore with an enforced byte bound.
func (f *Fake) Get(ctx context.Context, locator skillstore.Locator, maxBytes uint64) skillstore.GetResult {
	if f.GetFn != nil {
		return f.GetFn(ctx, locator, maxBytes)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	data, exists := f.objects[locator.String()]
	if !exists {
		return skillstore.GetResult{Outcome: skillstore.GetAbsent}
	}
	if uint64(len(data)) > maxBytes {
		return skillstore.GetResult{Outcome: skillstore.GetOversize}
	}
	return skillstore.GetResult{
		Outcome: skillstore.GetPresent,
		Bytes:   append([]byte(nil), data...),
	}
}

// Seed stores an object directly, as if a prior write had already landed, without
// going through PutFn. It enforces create-only: seeding an occupied key reports
// false and changes nothing. A test uses it to simulate "an earlier ambiguous PUT
// actually did (or did not) land" and to plant a corrupt object at a key.
func (f *Fake) Seed(locator skillstore.Locator, data []byte) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	key := locator.String()
	if _, exists := f.objects[key]; exists {
		return false
	}
	if f.objects == nil {
		f.objects = make(map[string][]byte)
	}
	f.objects[key] = append([]byte(nil), data...)
	return true
}
