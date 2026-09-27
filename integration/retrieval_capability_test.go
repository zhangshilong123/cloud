package integration

// RetrievalCapability issuance (Step 5B of specs/decisions/controller/skill-delivery/
// 0-skill-retrieval-capability.md), pinned end-to-end against PostgreSQL:
//
//   - a mint verifies the durable (execution, attempt, skill_revision) authority chain and signs
//     exactly the frozen object_locator, GET-only, with the configured TTL;
//   - unbound revisions, foreign (execution, attempt) triples, and terminal attempts all fail closed
//     with the frozen D32 error classes;
//   - storage_not_configured / signing_failed / invalid_locator surface as stable classes, never
//     provider detail;
//   - nothing is persisted: no credential column exists, no row is added by mint/refresh, and a
//     refresh returns a fresh capability over the same frozen revision.

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/wanglongan587/cloud/internal/core"
	"github.com/wanglongan587/cloud/internal/skillstore"
)

// fakeIssuer is a deterministic in-memory RetrievalCapabilityIssuer. It records every locator it
// signs (for exact-object assertions) and stamps a per-call sequence into the URL so a refresh is
// observable as a fresh credential.
type fakeIssuer struct {
	signed []string
	fail   error
	seq    int
}

func (f *fakeIssuer) Issue(_ context.Context, locator skillstore.Locator, ttl time.Duration) (skillstore.RetrievalCapability, error) {
	if f.fail != nil {
		return skillstore.RetrievalCapability{}, f.fail
	}
	f.seq++
	f.signed = append(f.signed, locator.String())
	return skillstore.RetrievalCapability{
		URL:       fmt.Sprintf("https://s3.example/bkt/%s?X-Amz-Signature=sig%d", locator.String(), f.seq),
		Method:    "GET",
		ExpiresAt: time.Now().Add(ttl),
	}, nil
}

// retrievalFixture provisions one admitted Execution over a single bound Skill whose frozen revision
// carries a valid object_locator, ready for capability minting, and wires the fake issuer.
type retrievalFixture struct {
	t         *testing.T
	store     *core.Store
	ws        string
	owner     string
	agent     string
	skillID   string
	revision  string
	execution string
	attempt   string
	locator   string
	issuer    *fakeIssuer
}

func newRetrievalFixture(t *testing.T) *retrievalFixture {
	t.Helper()
	pool, _ := testSchema(t, "retr_")
	store := newStoreOnSchema(t, pool)
	must(t, store.Migrate(context.Background()))
	owner, _, ws, _ := seedSkillBase(t, pool)

	execOK(t, pool, `INSERT INTO user_identities(user_id,source,subject) VALUES($1,'corp','alice')`, owner)
	execOK(t, pool, `INSERT INTO collab_workspace_members(workspace_id,user_id,role,status,created_by) VALUES($1,$2,'owner','active',$2)`, ws, owner)

	agent := insertAgent(t, pool, ws, "runner", owner)
	skillID := insertSkill(t, pool, ws, "alpha", owner)
	revision := insertRevision(t, pool, skillID, strings.Repeat("a", 64), owner)
	locator := "skills/ora-skill-package/v1/sha256/" + strings.Repeat("1a", 32) // 64 lowercase hex
	execOK(t, pool, `UPDATE skill_revisions SET object_locator=$2 WHERE id=$1`, revision, locator)
	execOK(t, pool, `UPDATE skills SET current_revision_id=$2,version=version+1,updated_at=now() WHERE id=$1`, skillID, revision)
	insertBinding(t, pool, agent, skillID, true)

	out, err := store.AdmitExecution(context.Background(), "corp", "alice", "Caller", ws, agent, core.Object{"prompt": "x"})
	must(t, err)
	execution := out.O("execution").S("executionId")
	attempt := out["attempts"].([]core.Object)[0].S("attemptId")

	issuer := &fakeIssuer{}
	store.RetrievalCapabilityIssuer = issuer
	store.RetrievalCapabilityTTL = 300 * time.Second

	return &retrievalFixture{
		t: t, store: store, ws: ws, owner: owner, agent: agent, skillID: skillID, revision: revision,
		execution: execution, attempt: attempt, locator: locator, issuer: issuer,
	}
}

// mint runs one capability mint for the fixture's (execution, attempt, revision).
func (f *retrievalFixture) mint() ([]core.RetrievedCapability, error) {
	f.t.Helper()
	return f.store.MintSkillRetrievalCapabilities(context.Background(), f.execution, f.attempt, []string{f.revision})
}

// TestRetrievalMintsBoundRevision pins the happy path: a bound frozen revision mints exactly one
// GET-only capability signed over the exact logical locator, with a non-zero expiry.
func TestRetrievalMintsBoundRevision(t *testing.T) {
	f := newRetrievalFixture(t)
	caps, err := f.mint()
	must(t, err)
	if len(caps) != 1 {
		t.Fatalf("want 1 capability, got %d", len(caps))
	}
	c := caps[0]
	if c.SkillRevisionID != f.revision {
		t.Errorf("skill_revision_id = %q, want %q", c.SkillRevisionID, f.revision)
	}
	if c.Method != "GET" {
		t.Errorf("method = %q, want GET", c.Method)
	}
	if c.URL == "" {
		t.Error("capability URL must be set")
	}
	if c.ExpiresAt.IsZero() {
		t.Error("capability expiry must be set")
	}
	if len(f.issuer.signed) != 1 || f.issuer.signed[0] != f.locator {
		t.Fatalf("issuer signed %v, want exactly [%s]", f.issuer.signed, f.locator)
	}
}

// TestRetrievalRejectsUnboundRevision pins D12: a valid revision id that is not among this
// execution's frozen bindings fails closed with revision_not_bound.
func TestRetrievalRejectsUnboundRevision(t *testing.T) {
	f := newRetrievalFixture(t)
	_, err := f.store.MintSkillRetrievalCapabilities(context.Background(), f.execution, f.attempt, []string{uuid.NewString()})
	if fault := core.ErrorCode(err); fault.Status != 404 || fault.Code != "revision_not_bound" {
		t.Fatalf("want 404 revision_not_bound, got %d %s", fault.Status, fault.Code)
	}
	if len(f.issuer.signed) != 0 {
		t.Fatalf("unbound revision must not mint, signed %v", f.issuer.signed)
	}
}

// TestRetrievalAuthorizationFailsOnForeignTriple pins D9: a (execution, attempt) pair that does not
// verify fails closed as authorization_failed, indistinguishable from an unknown record.
func TestRetrievalAuthorizationFailsOnForeignTriple(t *testing.T) {
	f := newRetrievalFixture(t)
	cases := []struct {
		name      string
		execution string
		attempt   string
	}{
		{"foreign attempt", f.execution, uuid.NewString()},
		{"foreign execution", uuid.NewString(), f.attempt},
		{"both foreign", uuid.NewString(), uuid.NewString()},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := f.store.MintSkillRetrievalCapabilities(context.Background(), c.execution, c.attempt, []string{f.revision})
			if fault := core.ErrorCode(err); fault.Status != 404 || fault.Code != "authorization_failed" {
				t.Fatalf("want 404 authorization_failed, got %d %s", fault.Status, fault.Code)
			}
		})
	}
	if len(f.issuer.signed) != 0 {
		t.Fatalf("foreign triple must not mint, signed %v", f.issuer.signed)
	}
}

// TestRetrievalTerminalAttemptNotEligible pins D14: a terminal attempt never mints.
func TestRetrievalTerminalAttemptNotEligible(t *testing.T) {
	f := newRetrievalFixture(t)
	for _, terminal := range []string{"succeeded", "failed", "canceled", "superseded"} {
		t.Run(terminal, func(t *testing.T) {
			execOK(t, f.store.Pool, `UPDATE attempts SET state=$2, updated_at=now() WHERE attempt_id=$1`, f.attempt, terminal)
			_, err := f.store.MintSkillRetrievalCapabilities(context.Background(), f.execution, f.attempt, []string{f.revision})
			if fault := core.ErrorCode(err); fault.Status != 409 || fault.Code != "attempt_not_eligible" {
				t.Fatalf("want 409 attempt_not_eligible, got %d %s", fault.Status, fault.Code)
			}
		})
	}
	if len(f.issuer.signed) != 0 {
		t.Fatalf("terminal attempt must not mint, signed %v", f.issuer.signed)
	}
}

// TestRetrievalStorageNotConfigured pins D16: with no issuer wired, a valid triple reports
// storage_not_configured and never fabricates a URL.
func TestRetrievalStorageNotConfigured(t *testing.T) {
	f := newRetrievalFixture(t)
	f.store.RetrievalCapabilityIssuer = nil
	_, err := f.mint()
	if fault := core.ErrorCode(err); fault.Status != 503 || fault.Code != "storage_not_configured" {
		t.Fatalf("want 503 storage_not_configured, got %d %s", fault.Status, fault.Code)
	}
}

// TestRetrievalSigningFailed pins that a provider failure surfaces as signing_failed.
func TestRetrievalSigningFailed(t *testing.T) {
	f := newRetrievalFixture(t)
	f.issuer.fail = errors.New("presign boom")
	_, err := f.mint()
	if fault := core.ErrorCode(err); fault.Status != 503 || fault.Code != "signing_failed" {
		t.Fatalf("want 503 signing_failed, got %d %s", fault.Status, fault.Code)
	}
}

// TestRetrievalInvalidLocator pins that a frozen revision whose object_locator cannot be parsed into
// a logical key fails closed with invalid_locator, never a provider error.
func TestRetrievalInvalidLocator(t *testing.T) {
	f := newRetrievalFixture(t)
	execOK(t, f.store.Pool, `UPDATE skill_revisions SET object_locator='not-a-locator' WHERE id=$1`, f.revision)
	_, err := f.mint()
	if fault := core.ErrorCode(err); fault.Status != 500 || fault.Code != "invalid_locator" {
		t.Fatalf("want 500 invalid_locator, got %d %s", fault.Status, fault.Code)
	}
}

// TestRetrievalNeverPersistsCapability pins D6/D7/D28: mint writes nothing durable and no schema
// field can hold a credential.
func TestRetrievalNeverPersistsCapability(t *testing.T) {
	f := newRetrievalFixture(t)
	caps, err := f.mint()
	must(t, err)
	if len(caps) != 1 || caps[0].URL == "" {
		t.Fatalf("mint must return a capability, got %+v", caps)
	}

	// No credential field exists anywhere in the skill/execution schema.
	for _, table := range []string{"executions", "attempts", "execution_skill_bindings", "skill_revisions", "skill_ingestions"} {
		for _, col := range []string{"url", "signed_url", "token", "credential", "retrieval_capability", "presign", "authorization_header"} {
			if columnExists(t, f.store.Pool, table, col) {
				t.Fatalf("%s must not carry a credential field %s", table, col)
			}
		}
	}

	// Mint adds no durable row: exactly one execution, one attempt, one binding remain.
	for _, q := range []string{
		"SELECT count(*) FROM executions",
		"SELECT count(*) FROM attempts",
		"SELECT count(*) FROM execution_skill_bindings",
	} {
		var n int
		must(t, f.store.Pool.QueryRow(q).Scan(&n))
		if n != 1 {
			t.Fatalf("%s → want 1 row after mint, got %d", q, n)
		}
	}

	// The bearer URL never lands in the database: no column holds it (proven above), and no row
	// carries the signature material.
	var leaked int
	must(t, f.store.Pool.QueryRow(`SELECT count(*) FROM skill_revisions WHERE object_locator LIKE '%X-Amz%' OR object_locator LIKE '%sig%'`).Scan(&leaked))
	if leaked != 0 {
		t.Fatalf("bearer credential material leaked into durable state: %d rows", leaked)
	}
}

// TestRetrievalRefreshMintsFreshCapabilitySameRevision pins D11/D13/D21: a refresh over the same
// triple returns a fresh capability for the same frozen revision and mints no new durable identity.
func TestRetrievalRefreshMintsFreshCapabilitySameRevision(t *testing.T) {
	f := newRetrievalFixture(t)
	first, err := f.mint()
	must(t, err)
	second, err := f.mint()
	must(t, err)
	if len(first) != 1 || len(second) != 1 {
		t.Fatalf("want 1 capability each, got %d and %d", len(first), len(second))
	}
	if first[0].SkillRevisionID != f.revision || second[0].SkillRevisionID != f.revision {
		t.Fatalf("refresh must preserve the frozen revision identity")
	}
	if first[0].URL == second[0].URL {
		t.Fatal("refresh must mint a fresh capability (new URL)")
	}
	if len(f.issuer.signed) != 2 || f.issuer.signed[0] != f.locator || f.issuer.signed[1] != f.locator {
		t.Fatalf("issuer signed %v, want two signatures of the same %s", f.issuer.signed, f.locator)
	}

	// No new attempt or binding is created by refresh.
	for _, q := range []string{
		"SELECT count(*) FROM attempts WHERE execution_id=$1",
		"SELECT count(*) FROM execution_skill_bindings WHERE execution_id=$1",
	} {
		var n int
		must(t, f.store.Pool.QueryRow(q, f.execution).Scan(&n))
		if n != 1 {
			t.Fatalf("%s → want 1 after refresh, got %d", q, n)
		}
	}
}
