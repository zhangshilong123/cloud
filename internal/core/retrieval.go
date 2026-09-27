package core

// RetrievalCapability issuance (Step 5B of specs/decisions/controller/skill-delivery/
// 0-skill-retrieval-capability.md).
//
// This file owns the Cloud-side mint path: it verifies the durable (execution, attempt,
// skill_revision) authority chain and mints one short-lived, GET-only, exact-object capability per
// requested frozen revision. It resolves the authority chain in a database-only transaction, then
// signs outside any transaction — no external effect runs inside transact, and nothing is persisted.
// It never re-resolves mutable Skill state (D10): targets come only from immutable
// execution_skill_bindings joined to skill_revisions. It implements no Node materialization, no
// Controller relay, and no byte proxying (those belong to Phase 7/8).

import (
	"context"
	"time"

	"github.com/wanglongan587/cloud/internal/skillstore"
)

// defaultRetrievalCapabilityTTL is the fallback lifetime when the Store is not wired with a TTL
// (ADR D24); cmd/server always wires the configured value.
const defaultRetrievalCapabilityTTL = 300 * time.Second

// RetrievedCapability is one minted capability bound to its frozen skill revision (Step 5B ADR D3).
// URL is a bearer credential: it exists only in this transient response and must never be persisted
// or logged (D4/D6/D7). Method is always "GET" (D26).
type RetrievedCapability struct {
	SkillRevisionID string
	Method          string
	URL             string
	ExpiresAt       time.Time
}

// MintSkillRetrievalCapabilities verifies the durable (execution, attempt, skill_revision) authority
// chain and mints one short-lived, GET-only, exact-object retrieval capability per requested frozen
// revision (Step 5B ADR D9/D12/D31). A refresh for the same triple returns a fresh capability over
// the same frozen revision; nothing is persisted and no new Execution/Attempt/Binding is created
// (D11/D13/D21). Terminal attempts never mint (D14).
func (s *Store) MintSkillRetrievalCapabilities(ctx context.Context, executionID, attemptID string, revisionIDs []string) ([]RetrievedCapability, error) {
	// Phase 1 (database-only): verify the authority chain and resolve each requested revision to its
	// frozen object_locator. Failures here are stable classes, never provider detail (D32).
	var targets []retrievalTarget
	if _, err := s.transact(ctx, func(t *transaction) Object {
		targets = resolveRetrievalTargets(t, executionID, attemptID, revisionIDs)
		return nil
	}); err != nil {
		return nil, err
	}

	// Phase 2 (external): mint one exact-object capability per target. No existence probe (D29).
	if s.RetrievalCapabilityIssuer == nil {
		return nil, &Fault{Code: "storage_not_configured", Status: 503, Params: Object{}}
	}
	ttl := s.RetrievalCapabilityTTL
	if ttl <= 0 {
		ttl = defaultRetrievalCapabilityTTL
	}

	out := make([]RetrievedCapability, 0, len(targets))
	for _, tg := range targets {
		locator, err := skillstore.ParseLocator(tg.objectLocator)
		if err != nil {
			return nil, &Fault{Code: "invalid_locator", Status: 500, Params: Object{}}
		}
		capability, err := s.RetrievalCapabilityIssuer.Issue(ctx, locator, ttl)
		if err != nil {
			return nil, &Fault{Code: "signing_failed", Status: 503, Params: Object{}}
		}
		out = append(out, RetrievedCapability{
			SkillRevisionID: tg.skillRevisionID,
			Method:          capability.Method,
			URL:             capability.URL,
			ExpiresAt:       capability.ExpiresAt,
		})
	}
	return out, nil
}

// retrievalTarget is the resolved issuance target for one frozen revision: its authoritative logical
// locator, never a caller-supplied locator or URL (D16).
type retrievalTarget struct {
	skillRevisionID string
	objectLocator   string
}

// resolveRetrievalTargets verifies the durable (execution, attempt, skill_revision) authority chain
// and returns, for each requested revision, its frozen object_locator. It never re-resolves mutable
// Skill state (D10): targets come only from immutable execution_skill_bindings joined to
// skill_revisions. Every requested revision must be a frozen binding of this very execution (D12).
func resolveRetrievalTargets(t *transaction, executionID, attemptID string, revisionIDs []string) []retrievalTarget {
	require(validID(executionID), 404, "authorization_failed")
	require(validID(attemptID), 404, "authorization_failed")

	// The attempt must belong to the execution (D9): a foreign or unknown attempt is an authorization
	// failure, indistinguishable from an unknown record.
	a := t.one("SELECT state FROM attempts WHERE attempt_id=$1 AND execution_id=$2", attemptID, executionID)
	require(a != nil, 404, "authorization_failed")
	// Terminal attempts never mint (D14): succeeded/failed/canceled/superseded.
	require(!attemptTerminal(a.S("state")), 409, "attempt_not_eligible")

	targets := make([]retrievalTarget, 0, len(revisionIDs))
	for _, revID := range revisionIDs {
		require(validID(revID), 404, "revision_not_bound")
		r := t.one(`SELECT sr.object_locator
			FROM execution_skill_bindings esb
			JOIN skill_revisions sr ON sr.id = esb.skill_revision_id AND sr.skill_id = esb.skill_id
			WHERE esb.execution_id=$1 AND esb.skill_revision_id=$2`, executionID, revID)
		require(r != nil, 404, "revision_not_bound")
		targets = append(targets, retrievalTarget{skillRevisionID: revID, objectLocator: r.S("objectLocator")})
	}
	return targets
}

// attemptTerminal reports whether an Attempt state is terminal per Step 5B ADR D14. Non-terminal
// states (eligible/dispatched/running) may still mint.
func attemptTerminal(state string) bool {
	switch state {
	case "succeeded", "failed", "canceled", "superseded":
		return true
	}
	return false
}
