package core

// Canonical Skill ingestion saga (Phase 2 of
// specs/decisions/cloud/skills/20260927-canonical-ingestion-saga.md).
//
// This file owns the write path that turns one candidate file tree into a durable,
// activated SkillRevision. It is journal-first and idempotent:
//
//	TX #1  (DB only)   authorize + bind the idempotency namespace + INSERT a "planned"
//	                   skill_ingestions row, or find an existing row (replay / resume /
//	                   conflict).
//	external (no DB)   probe by stable identity, PUT only when definitively absent, and
//	                   reconcile to CONFIRMED_PRESENT_MATCHING — never inside a DB
//	                   transaction (D28/D30).
//	TX #2  (DB only)   create/reuse the Skill, create/reuse the SkillRevision, run the
//	                   activation CAS, and finalize state→committed with the durable
//	                   activation_outcome.
//
// Recovery is client-driven continuation (D14): a request that strands in "storing"
// (or crashes between transactions) is resumed by re-submitting the same Idempotency-Key;
// the durable (workspace_id, idempotency_key, canonical_name) row is found again, the
// external phase re-probes (never a blind re-PUT), and the saga continues. No autonomous
// worker and no durable package payload are introduced here.

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"strings"

	"github.com/wanglongan587/cloud/internal/skillmeta"
	"github.com/wanglongan587/cloud/internal/skillpkg"
	"github.com/wanglongan587/cloud/internal/skillstore"
)

// SkillIngestRequest is the caller input for one candidate. A batch shares one
// IdempotencyKey and SourceType across its candidates (see IngestSkills); each candidate
// is its own Skill file tree and must contain exactly one root SKILL.md.
type SkillIngestRequest struct {
	// IdempotencyKey scopes the (workspace_id, idempotency_key, canonical_name) namespace.
	// The same key re-submitted with the same candidate replays/resumes; the same key with
	// a different candidate conflicts (D22).
	IdempotencyKey string
	// SourceType is "directory" or "archive" (the transport the candidate was read from).
	SourceType string
	// TargetSkillID, when set, is an explicit update of that Skill (its business identity
	// is authoritative, D8); when empty it is a name-based batch import (D7).
	TargetSkillID string
	// DisplayName and Summary are caller-provided business metadata written to a newly
	// created Skill; they are never derived from SKILL.md. DisplayName defaults to
	// canonical_name when empty. They participate in the request fingerprint verbatim
	// (empty participates as empty, D10), so they are never rewritten on an existing Skill.
	DisplayName string
	Summary     string
	// Files is the canonical candidate tree (the transport decode is a separate
	// source-adapter slice; this slice receives already-decoded SourceFiles).
	Files []skillpkg.SourceFile
	// Limits bounds canonicalization; a zero value uses the library defaults.
	Limits skillpkg.Limits
}

// SkillIngestResult is the durable outcome of one candidate. State is one of the five
// saga states; IngestionID is empty only when the candidate failed validation before TX #1
// (no journal row exists). Activation is non-empty only for a committed row.
type SkillIngestResult struct {
	IngestionID string
	SkillID     string
	RevisionID  string
	State       string // planned | storing | verified | committed | failed ("" when pre-journal)
	Activation  string // activated | activation_conflict ("" when not committed)
	Replayed    bool   // true when a terminal prior ingestion was returned without new work
	ErrorCode   string // stable error code, only meaningful for a failure/conflict
}

// skillFingerprint computes the semantic request fingerprint (D10): the SHA-256 hex of a
// domain-separated, length-prefixed encoding of (target_skill_id, display_name, summary,
// content_digest, package_digest) in that fixed order. The idempotency namespace
// (workspace_id, idempotency_key, canonical_name) is the lookup key and is deliberately not
// encoded here; the fingerprint is the replay-vs-conflict discriminator within that namespace.
func skillFingerprint(targetSkillID, displayName, summary, contentDigest, packageDigest string) string {
	h := sha256.New()
	_, _ = h.Write([]byte("ora-skill-request-v1\x00"))
	var lenBuf [8]byte
	for _, field := range [5]string{targetSkillID, displayName, summary, contentDigest, packageDigest} {
		binary.BigEndian.PutUint64(lenBuf[:], uint64(len(field)))
		_, _ = h.Write(lenBuf[:])
		_, _ = h.Write([]byte(field))
	}
	return hex.EncodeToString(h.Sum(nil))
}

// skillCandidate is one Skill's fully prepared identity, computed entirely outside any
// database transaction from the in-memory candidate tree (pure CPU; D6/D12).
type skillCandidate struct {
	canonicalName      string
	displayName        string
	summary            string
	contentDigest      string
	packageDigest      string
	locator            skillstore.Locator
	packageBytes       []byte
	limits             skillpkg.Limits
	sizeBytes          int64
	fileCount          int
	packageName        string
	packageDescription string
	fingerprint        string
}

// prepareSkillCandidate canonicalizes one candidate tree into its stable identity before any
// journal or external mutation: it builds the ora-skill-package bytes, parses SKILL.md, and
// derives canonical_name, the content digest, the package digest, the object locator, and the
// request fingerprint. It performs no I/O and never enters a database transaction.
func prepareSkillCandidate(req *SkillIngestRequest) (*skillCandidate, error) {
	bundle, err := skillpkg.Build(req.Files, req.Limits)
	if err != nil {
		return nil, err
	}
	meta, err := skillmeta.Parse(skillMDBytes(req.Files))
	if err != nil {
		return nil, err
	}
	contentDigest := bundle.TreeDigestHex()
	packageDigest := skillstore.PackageDigestHex(bundle.Package)
	locator, err := skillstore.NewLocator(skillpkg.FormatName, skillpkg.FormatVersion, skillstore.AlgorithmSHA256, packageDigest)
	if err != nil {
		return nil, err
	}
	var total uint64
	for _, e := range bundle.Entries {
		total += e.Size
	}
	return &skillCandidate{
		canonicalName:      meta.CanonicalName(),
		displayName:        req.DisplayName,
		summary:            req.Summary,
		contentDigest:      contentDigest,
		packageDigest:      packageDigest,
		locator:            locator,
		packageBytes:       bundle.Package,
		limits:             req.Limits,
		sizeBytes:          int64(total),
		fileCount:          len(bundle.Entries),
		packageName:        meta.Name,
		packageDescription: meta.Description,
		fingerprint:        skillFingerprint(req.TargetSkillID, req.DisplayName, req.Summary, contentDigest, packageDigest),
	}, nil
}

// skillMDBytes returns the exact SKILL.md bytes from the candidate tree, normalizing the
// native separator the same way skillpkg does. skillpkg.Build has already guaranteed exactly
// one canonical "SKILL.md", so this returns its bytes (or nil only defensively).
func skillMDBytes(files []skillpkg.SourceFile) []byte {
	for _, f := range files {
		if strings.ReplaceAll(f.Path, "\\", "/") == "SKILL.md" {
			return f.Data
		}
	}
	return nil
}

// ingestPlanned is the outcome of TX #1: the journal row plus whether it already reached a
// terminal state (replay) or still needs the external phase (fresh or resume).
type ingestPlanned struct {
	row      Object
	terminal bool // true when the existing row is committed/failed (replay, no new work)
}

// ingestSkillPlanned runs TX #1 inside a transaction: it authorizes the write (D25), validates
// the request, and either inserts a "planned" row or finds the existing row for the
// (workspace_id, idempotency_key, canonical_name) namespace. A same-namespace row with a
// different fingerprint is a deterministic conflict (D22); a same-fingerprint row either
// replays its terminal result or resumes the saga.
func ingestSkillPlanned(t *transaction, workspaceID, actorID string, req *SkillIngestRequest, cand *skillCandidate) ingestPlanned {
	role := workspaceRole(t, workspaceID, actorID)
	require(role != "", 404, "not_found")
	switch role {
	case "owner", "admin":
	default:
		reject(403, "workspace_admin_required")
	}
	require(req.IdempotencyKey != "" && len(req.IdempotencyKey) <= 200, 400, "invalid_idempotency_key")
	require(req.SourceType == "directory" || req.SourceType == "archive", 400, "invalid_source_type")
	if req.TargetSkillID != "" {
		require(validID(req.TargetSkillID), 404, "not_found")
	}

	if existing := t.one(`SELECT * FROM skill_ingestions
		WHERE workspace_id=$1 AND idempotency_key=$2 AND canonical_name=$3`,
		workspaceID, req.IdempotencyKey, cand.canonicalName); existing != nil {
		require(existing.S("requestFingerprint") == cand.fingerprint, 409, "idempotency_conflict")
		terminal := existing.S("state") == "committed" || existing.S("state") == "failed"
		return ingestPlanned{row: existing, terminal: terminal}
	}

	id := newID()
	var targetSkill any
	if req.TargetSkillID != "" {
		targetSkill = req.TargetSkillID
	}
	t.exec(`INSERT INTO skill_ingestions(
		id, workspace_id, target_skill_id, idempotency_key, source_type,
		canonical_name, request_fingerprint, expected_digest, digest_algorithm, object_locator, state, created_by)
		VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,'planned',$11)`,
		id, workspaceID, targetSkill, req.IdempotencyKey, req.SourceType,
		cand.canonicalName, cand.fingerprint, cand.contentDigest, skillstore.AlgorithmSHA256, cand.locator.String(), actorID)
	return ingestPlanned{row: t.one("SELECT * FROM skill_ingestions WHERE id=$1", id)}
}

// externalKind classifies the external (Object Storage) phase result, which then decides the
// single short DB-only finalize transition.
type externalKind uint8

const (
	externalVerified externalKind = iota // CONFIRMED_PRESENT_MATCHING → TX #2
	externalStoring                      // reconcile-required → state 'storing'
	externalFailed                       // deterministic permanent failure → state 'failed'
)

type externalOutcome struct {
	kind   externalKind
	code   string
	detail string
}

// storeSkillPackage runs the external phase with no database access (D28/D30). It probes by
// the stable identity first; only a definitively absent object is written, and an ambiguous
// PUT is resolved by re-probing, never a blind re-PUT (D14). It is bounded: at most one PUT and
// two Reconciles, converging to verified/storing/failed; a strand converges on the next
// client-driven resubmission.
func storeSkillPackage(ctx context.Context, store skillstore.ObjectStore, cand *skillCandidate) externalOutcome {
	want := &skillstore.Want{
		Locator:       cand.locator,
		PackageDigest: cand.packageDigest,
		ContentDigest: cand.contentDigest,
		Limits:        cand.limits,
	}
	verdict := skillstore.Reconcile(ctx, store, want)
	if verdict == skillstore.VerdictConfirmedAbsent {
		put := store.PutImmutable(ctx, &skillstore.PutRequest{
			Locator:               cand.locator,
			Bytes:                 cand.packageBytes,
			ExpectedPackageDigest: cand.packageDigest,
			ExpectedPackageSize:   uint64(len(cand.packageBytes)),
			ExpectedContentDigest: cand.contentDigest,
			PackageFormat:         skillpkg.FormatName,
			PackageFormatVersion:  skillpkg.FormatVersion,
			DigestAlgorithm:       skillstore.AlgorithmSHA256,
		})
		switch put.Outcome {
		case skillstore.PutDefiniteFailureTransient:
			return externalOutcome{kind: externalStoring}
		case skillstore.PutDefiniteFailurePermanent:
			return externalOutcome{kind: externalFailed, code: "object_store_unavailable", detail: "Object Storage rejected the write"}
		default:
			// PutCreated / PutAlreadyExists / PutAmbiguous: converge by re-probing.
			verdict = skillstore.Reconcile(ctx, store, want)
		}
	}
	switch verdict {
	case skillstore.VerdictConfirmedPresentMatching:
		return externalOutcome{kind: externalVerified}
	case skillstore.VerdictMismatch:
		return externalOutcome{kind: externalFailed, code: "object_identity_mismatch", detail: "stored object does not match the durable identity"}
	case skillstore.VerdictDefiniteFailurePermanent:
		return externalOutcome{kind: externalFailed, code: "object_store_unavailable", detail: "Object Storage is unavailable"}
	default:
		// ConfirmedAbsent (an ambiguous PUT left it absent), DefiniteFailureTransient, or
		// Indeterminate: reconcile-required, never a blind write or an in-process loop.
		return externalOutcome{kind: externalStoring}
	}
}

// IngestSkill runs the full saga for one candidate. It returns a per-candidate failure (a
// validation error_code with no journal row) for invalid content, a *Fault for authorization /
// idempotency conflicts, or a durable result for a journaled outcome.
func (s *Store) IngestSkill(ctx context.Context, workspaceID, actorID string, req *SkillIngestRequest) (SkillIngestResult, error) {
	if req == nil {
		return SkillIngestResult{}, &Fault{Code: "invalid_input", Status: 400, Params: Object{}}
	}
	cand, err := prepareSkillCandidate(req)
	if err != nil {
		// Validation/quota/metadata failure happens before TX #1: no journal row, no revision.
		return SkillIngestResult{ErrorCode: candidateErrorCode(err)}, nil
	}

	var planned ingestPlanned
	_, err = s.transact(ctx, func(t *transaction) Object {
		planned = ingestSkillPlanned(t, workspaceID, actorID, req, cand)
		return nil
	})
	if err != nil {
		return SkillIngestResult{}, err
	}
	if planned.terminal {
		res := skillIngestionResult(planned.row)
		res.Replayed = true
		return res, nil
	}

	id := planned.row.S("id")
	if s.SkillsObjectStore == nil {
		return s.markSkillIngestionFailed(ctx, id, "object_store_unavailable", "Object Storage is not configured")
	}

	switch oc := storeSkillPackage(ctx, s.SkillsObjectStore, cand); oc.kind {
	case externalVerified:
		if err := s.markSkillIngestionVerified(ctx, id); err != nil {
			return SkillIngestResult{}, err
		}
		return s.commitSkillIngestion(ctx, id, actorID, cand)
	case externalStoring:
		return s.markSkillIngestionStoring(ctx, id)
	default:
		return s.markSkillIngestionFailed(ctx, id, oc.code, oc.detail)
	}
}

// IngestSkills runs a batch of candidates under one IdempotencyKey and SourceType with
// per-candidate independence (D4/D5): one candidate's business failure never rolls back a
// sibling. It returns one result per candidate in input order. A *Fault — an authorization
// rejection, an idempotency conflict, a cross-workspace target, or a lost database — is a
// request-level rejection and propagates as a non-nil error so the caller can surface it as an
// HTTP 403/404/409 rather than a per-candidate errorCode inside a 200 envelope (public upload
// ADR D2/D6/D16).
func (s *Store) IngestSkills(ctx context.Context, workspaceID, actorID, key, sourceType string, reqs []SkillIngestRequest) ([]SkillIngestResult, error) {
	results := make([]SkillIngestResult, 0, len(reqs))
	for _, r := range reqs {
		r.IdempotencyKey = key
		r.SourceType = sourceType
		res, err := s.IngestSkill(ctx, workspaceID, actorID, &r)
		if err != nil {
			return results, err
		}
		results = append(results, res)
	}
	return results, nil
}

// markSkillIngestionVerified records the durable 'verified' state (D15: ⇔
// CONFIRMED_PRESENT_MATCHING) in a short DB-only transaction between the external phase and
// TX #2, so a crash between the two resumes from 'verified' rather than re-putting.
func (s *Store) markSkillIngestionVerified(ctx context.Context, id string) error {
	_, err := s.transact(ctx, func(t *transaction) Object {
		t.exec(`UPDATE skill_ingestions SET state='verified', updated_at=now()
			WHERE id=$1 AND state IN ('planned','storing')`, id)
		return nil
	})
	return err
}

// markSkillIngestionStoring records a reconcile-required strand. The client resumes by
// re-submitting the same Idempotency-Key; the row is never failed on ambiguity.
func (s *Store) markSkillIngestionStoring(ctx context.Context, id string) (SkillIngestResult, error) {
	out, err := s.transact(ctx, func(t *transaction) Object {
		t.exec(`UPDATE skill_ingestions SET state='storing', updated_at=now()
			WHERE id=$1 AND state IN ('planned','storing','verified')`, id)
		return t.one("SELECT * FROM skill_ingestions WHERE id=$1", id)
	})
	if err != nil {
		return SkillIngestResult{}, err
	}
	return skillIngestionResult(out), nil
}

// markSkillIngestionFailed records a deterministic permanent failure with its stable
// error_code. error_code is set only here, on a terminal 'failed' row.
func (s *Store) markSkillIngestionFailed(ctx context.Context, id, code, detail string) (SkillIngestResult, error) {
	out, err := s.transact(ctx, func(t *transaction) Object {
		t.exec(`UPDATE skill_ingestions SET state='failed', error_code=$2, error_detail=$3, updated_at=now()
			WHERE id=$1 AND state IN ('planned','storing','verified')`, id, code, detail)
		return t.one("SELECT * FROM skill_ingestions WHERE id=$1", id)
	})
	if err != nil {
		return SkillIngestResult{}, err
	}
	return skillIngestionResult(out), nil
}

// commitSkillIngestion runs TX #2 (DB only, after CONFIRMED_PRESENT_MATCHING): resolve the
// Skill, resolve the SkillRevision, run the activation CAS, and finalize the journal row.
func (s *Store) commitSkillIngestion(ctx context.Context, id, actorID string, cand *skillCandidate) (SkillIngestResult, error) {
	var res SkillIngestResult
	_, err := s.transact(ctx, func(t *transaction) Object {
		res = commitSkill(t, id, actorID, cand)
		return nil
	})
	if err != nil {
		return SkillIngestResult{}, err
	}
	return res, nil
}

// commitSkill is TX #2's body. It fences on state='verified' (D15/D18) so a previously
// finalized row is never re-committed, and it always writes activation_outcome and backfills
// target_skill_id for batch imports (D16).
func commitSkill(t *transaction, id, actorID string, cand *skillCandidate) SkillIngestResult {
	ing := t.one("SELECT * FROM skill_ingestions WHERE id=$1", id)
	require(ing != nil, 404, "not_found")
	require(ing.S("state") == "verified", 409, "ingestion_state_conflict")

	skill, _ := resolveSkill(t, ing.S("workspaceId"), ing.S("targetSkillId"), cand, actorID)
	rev := resolveRevision(t, skill.S("id"), actorID, cand)
	outcome := "activated"
	if !casActivate(t, skill.S("id"), rev.S("id")) {
		outcome = "activation_conflict"
	}
	t.exec(`UPDATE skill_ingestions SET state='committed', activation_outcome=$2, target_skill_id=$3, updated_at=now()
		WHERE id=$1 AND state='verified'`, id, outcome, skill.S("id"))
	return SkillIngestResult{
		IngestionID: id,
		SkillID:     skill.S("id"),
		RevisionID:  rev.S("id"),
		State:       "committed",
		Activation:  outcome,
	}
}

// resolveSkill returns the business Skill a candidate converges to (D7/D17): an explicit
// update reuses target_skill_id (authoritative, D8), a batch import matches an active Skill by
// (workspace_id, canonical_name) or creates one. Existing Skill metadata is never overwritten;
// only a newly created Skill receives display_name/summary. The second result reports creation.
func resolveSkill(t *transaction, workspaceID, targetSkillID string, cand *skillCandidate, actorID string) (Object, bool) {
	if targetSkillID != "" {
		skill := t.one("SELECT * FROM skills WHERE id=$1 AND deleted_at IS NULL", targetSkillID)
		require(skill != nil, 404, "not_found")
		require(skill.S("workspaceId") == workspaceID, 404, "not_found")
		return skill, false
	}
	if skill := t.one("SELECT * FROM skills WHERE workspace_id=$1 AND canonical_name=$2 AND deleted_at IS NULL",
		workspaceID, cand.canonicalName); skill != nil {
		return skill, false
	}
	displayName := cand.displayName
	if displayName == "" {
		displayName = cand.canonicalName
	}
	id := newID()
	t.exec("INSERT INTO skills(id, workspace_id, canonical_name, display_name, summary, created_by) VALUES($1,$2,$3,$4,$5,$6)",
		id, workspaceID, cand.canonicalName, displayName, cand.summary, actorID)
	return t.one("SELECT * FROM skills WHERE id=$1", id), true
}

// resolveRevision returns the immutable SkillRevision for this candidate, reusing an identical
// (skill_id, digest_algorithm, content_digest) row or inserting a new one (D18). Since Phase 6A.1 it
// also persists and audits the trusted physical package_digest (§5/§6).
//
// §6 (revision reuse): content_digest alone does not prove the exact physical container bytes —
// two package encodings can decode to the same logical tree — so the durable package_digest is the
// byte-equivalence proof. Reuse is therefore allowed only when the existing row's package_digest is
// populated and equals the candidate's; a NULL legacy digest (pre-0019) or any mismatch stops the
// ingestion rather than silently writing a potentially different physical digest into the immutable
// historical row, re-serving unverifiable bytes, or freezing stale metadata.
func resolveRevision(t *transaction, skillID, actorID string, cand *skillCandidate) Object {
	if existing := t.one("SELECT * FROM skill_revisions WHERE skill_id=$1 AND digest_algorithm=$2 AND content_digest=$3",
		skillID, skillstore.AlgorithmSHA256, cand.contentDigest); existing != nil {
		require(existing.S("packageDigest") != "" && existing.S("packageDigest") == cand.packageDigest,
			409, "revision_delivery_conflict")
		return existing
	}
	id := newID()
	t.exec(`INSERT INTO skill_revisions(id, skill_id, content_digest, digest_algorithm, size_bytes, file_count,
		package_format, package_format_version, package_digest, package_digest_algorithm,
		object_locator, package_name, package_description, created_by)
		VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14)`,
		id, skillID, cand.contentDigest, skillstore.AlgorithmSHA256, cand.sizeBytes, cand.fileCount,
		skillpkg.FormatName, skillpkg.FormatVersion, cand.packageDigest, skillstore.AlgorithmSHA256,
		cand.locator.String(), cand.packageName, cand.packageDescription, actorID)
	return t.one("SELECT * FROM skill_revisions WHERE id=$1", id)
}

// casActivate advances a Skill's current_revision_id with a compare-and-set on its version
// (D19), returning whether the pointer advanced. Under the single-writer advisory lock it always
// succeeds, but the CAS (and the durable activation_outcome it feeds) stays correct if that lock
// model ever changes.
func casActivate(t *transaction, skillID, revisionID string) bool {
	skill := t.one("SELECT version FROM skills WHERE id=$1", skillID)
	require(skill != nil, 404, "not_found")
	version := skill.N("version")
	return t.execRows("UPDATE skills SET current_revision_id=$2, version=version+1, updated_at=now() WHERE id=$1 AND version=$3",
		skillID, revisionID, version) == 1
}

// skillIngestionResult projects a journal row into its public result.
func skillIngestionResult(row Object) SkillIngestResult {
	return SkillIngestResult{
		IngestionID: row.S("id"),
		SkillID:     row.S("targetSkillId"),
		State:       row.S("state"),
		Activation:  row.S("activationOutcome"),
		ErrorCode:   row.S("errorCode"),
	}
}

// candidateErrorCode maps a pre-journal candidate failure to its stable error_code: the
// SKILL.md metadata codes pass through, a limit breach is skill_limit_exceeded, and any other
// package construction failure is skill_package_invalid.
func candidateErrorCode(err error) string {
	if code := skillmeta.CodeOf(err); code != "" {
		return code
	}
	if errors.Is(err, skillpkg.ErrLimit) {
		return "skill_limit_exceeded"
	}
	return "skill_package_invalid"
}

// skillReadSelect is the shared public read projection for a live Skill. It joins the exact current
// SkillRevision through the same composite identity (current_revision_id, id) as the write path and
// exposes only public fields — never object_locator/bucket/storage key/provider or any signed URL.
const skillReadSelect = `SELECT s.id, s.workspace_id, s.canonical_name, s.display_name, s.summary,
	s.version, s.created_at, s.updated_at,
	sr.id AS current_revision_id, sr.content_digest, sr.size_bytes, sr.package_format, sr.package_format_version,
	sr.package_description
	FROM skills s
	LEFT JOIN skill_revisions sr ON sr.id = s.current_revision_id AND sr.skill_id = s.id`

// projectSkill reshapes a raw skillReadSelect row into the public Skill object: the flattened
// revision columns become a nested currentRevision, and storage/locator fields are already absent
// from the select. currentRevision is omitted when the Skill has no activated revision yet.
func projectSkill(o Object) Object {
	out := Object{
		"id":            o.S("id"),
		"workspaceId":   o.S("workspaceId"),
		"canonicalName": o.S("canonicalName"),
		"displayName":   o.S("displayName"),
		"summary":       o.S("summary"),
		"version":       o.N("version"),
		"createdAt":     o.S("createdAt"),
		"updatedAt":     o.S("updatedAt"),
	}
	if revID := o.S("currentRevisionId"); revID != "" {
		out["currentRevision"] = Object{
			"id":                   revID,
			"description":          o.S("packageDescription"),
			"contentDigest":        o.S("contentDigest"),
			"sizeBytes":            o.N("sizeBytes"),
			"packageFormat":        o.S("packageFormat"),
			"packageFormatVersion": o.N("packageFormatVersion"),
		}
	}
	return out
}

// listSkills pages the live Skills of a Collaboration Workspace (member+), ordered by
// (canonical_name, skill id) for a deterministic walk. The cursor stays an opaque skill id, and the
// projection never exposes object storage fields (D8).
func listSkills(t *transaction, r *PublicRequest, uid string) Object {
	agentSpace(t, r.SpaceID, uid, false)
	out := pageSkillList(t, skillReadSelect+` WHERE s.workspace_id=$1 AND s.deleted_at IS NULL`, []any{r.SpaceID}, r)
	items := out["items"].([]Object)
	projected := make([]Object, 0, len(items))
	for _, it := range items {
		projected = append(projected, projectSkill(it))
	}
	out["items"] = projected
	return out
}

// getSkill reads one live Skill (member+). A soft-deleted Skill, a foreign-workspace Skill, or a
// missing Skill is uniformly 404 (no existence leak, D11).
func getSkill(t *transaction, r *PublicRequest, uid string) Object {
	agentSpace(t, r.SpaceID, uid, false)
	require(validID(r.SkillID), 404, "not_found")
	o := t.one(skillReadSelect+` WHERE s.id=$1 AND s.workspace_id=$2 AND s.deleted_at IS NULL`, r.SkillID, r.SpaceID)
	require(o != nil, 404, "not_found")
	return projectSkill(o)
}

// archiveSkill soft-deletes a Skill under the unified workspace delete rule (D11): the creator may
// always delete their own Skill; otherwise the actor must be a workspace owner/admin. It only sets
// deleted_at — never hard-deletes the SkillRevision, its object storage package, or any historical
// ExecutionSkillBinding/SkillRevision snapshot — so a frozen Execution bound to this Skill's
// revision stays readable while the Skill disappears from the live list and future admission (D11).
func archiveSkill(t *transaction, r *PublicRequest, uid string) Object {
	require(validID(r.SkillID), 404, "not_found")
	skill := t.one("SELECT * FROM skills WHERE id=$1 AND workspace_id=$2 AND deleted_at IS NULL", r.SkillID, r.SpaceID)
	require(skill != nil, 404, "not_found")
	agentSpace(t, r.SpaceID, uid, false)
	require(workspaceCanDelete(t, r.SpaceID, uid, skill.S("createdBy")), 403, "space_role_required")
	version(skill, r.Body.N("version"))
	t.exec("UPDATE skills SET deleted_at=now(), version=version+1, updated_at=now() WHERE id=$1", r.SkillID)
	// Re-project through the public read shape so the delete response still exposes the frozen
	// currentRevision — the soft-delete never clears the current_revision_id pointer (D11).
	return projectSkill(t.one(skillReadSelect+` WHERE s.id=$1`, r.SkillID))
}

// pageSkillList pages the Skill list in ascending (canonical_name, id) order. canonical_name is not
// unique, so the cursor predicate resolves the anchor row's canonical_name from the skills table
// before comparing the pair — the same composite-cursor pattern as pageByCreation, keeping the
// public cursor an opaque skill id.
func pageSkillList(t *transaction, q string, args []any, r *PublicRequest) Object {
	if r.After != "" {
		require(validID(r.After), 400, "invalid_cursor")
		args = append(args, r.After)
		q += " AND (s.canonical_name, s.id) > ((SELECT a.canonical_name FROM skills a WHERE a.id=$" + itoa(len(args)) + "::uuid), $" + itoa(len(args)) + "::uuid)"
	}
	return window(t, q+" ORDER BY s.canonical_name, s.id", args, r)
}
