package core

import (
	"context"

	"github.com/wanglongan587/cloud/internal/skillsource"
)

// SourceIngestResult is the combined outcome of ingesting one prepared source: the
// candidate-local preparation failures (non-durable, never journaled) plus the per-candidate
// ingestion results from the existing saga (ADR D8 / saga D4). It is an internal composition
// only — no durable batch resource, no migration.
type SourceIngestResult struct {
	// PreparationFailures are the candidate-local metadata failures produced during source
	// preparation. They are reported verbatim and never mapped to a SkillIngestion row.
	PreparationFailures []skillsource.PreparationFailure
	// Ingestions are the saga results for the prepared candidates, in candidate order.
	Ingestions []SkillIngestResult
}

// SourceIngestParams carries the caller-controlled business fields that map verbatim onto each
// candidate's SkillIngestRequest (public upload ADR D5). TargetSkillID is an explicit update and,
// when set, the source must contain exactly one candidate (otherwise single_candidate_required).
// DisplayName and Summary are written only to newly created Skills; they participate in the saga
// request fingerprint verbatim (empty participates as empty), never rewritten on an existing Skill.
type SourceIngestParams struct {
	TargetSkillID string
	DisplayName   string
	Summary       string
}

// IngestSource is the single seam between source preparation and the ingestion saga: it maps
// each PreparedCandidate to a SkillIngestRequest and runs the existing IngestSkills
// partial-success saga under one idempotency key. It owns no business effect of its own —
// idempotency, storage, and activation remain in the saga. PreparationFailures pass through
// unchanged (they never reach the saga and never create rows). The caller-controlled
// TargetSkillID/DisplayName/Summary are projected verbatim onto every candidate (an additive
// projection, not a saga semantic change).
//
// Authorization and target scoping are whole-request obligations of the public upload boundary
// (public upload ADR D2/D16): a non-member, a non-owner/admin member, or a target_skill_id that
// resolves outside this workspace is an HTTP 403/404, never a per-candidate errorCode inside a
// 200 envelope. They are checked up front so the request fails fast with zero side effects; the
// saga keeps its own check as defense in depth.
func (s *Store) IngestSource(ctx context.Context, workspaceID, actorID, key string, prepared *skillsource.PreparedSourceResult, params SourceIngestParams) (SourceIngestResult, error) {
	if prepared == nil {
		return SourceIngestResult{}, nil
	}

	_, err := s.transact(ctx, func(t *transaction) Object {
		role := workspaceRole(t, workspaceID, actorID)
		require(role != "", 404, "not_found")
		switch role {
		case "owner", "admin":
		default:
			reject(403, "workspace_admin_required")
		}
		return nil
	})
	if err != nil {
		return SourceIngestResult{}, err
	}

	if params.TargetSkillID != "" {
		if len(prepared.Candidates) > 1 {
			return SourceIngestResult{}, &Fault{Code: "single_candidate_required", Status: 400, Params: Object{}}
		}
		// An explicit update must resolve to a live Skill inside this workspace; a cross-workspace
		// or absent target is 404 not_found (no existence leak, public upload ADR D2).
		_, err = s.transact(ctx, func(t *transaction) Object {
			require(validID(params.TargetSkillID), 404, "not_found")
			skill := t.one("SELECT * FROM skills WHERE id=$1 AND deleted_at IS NULL", params.TargetSkillID)
			require(skill != nil, 404, "not_found")
			require(skill.S("workspaceId") == workspaceID, 404, "not_found")
			return nil
		})
		if err != nil {
			return SourceIngestResult{}, err
		}
	}

	reqs := make([]SkillIngestRequest, 0, len(prepared.Candidates))
	sourceType := ""
	for _, c := range prepared.Candidates {
		if sourceType == "" {
			sourceType = c.SourceType
		}
		reqs = append(reqs, SkillIngestRequest{
			IdempotencyKey: key,
			SourceType:     c.SourceType,
			TargetSkillID:  params.TargetSkillID,
			DisplayName:    params.DisplayName,
			Summary:        params.Summary,
			Files:          c.Files,
		})
	}
	results, err := s.IngestSkills(ctx, workspaceID, actorID, key, sourceType, reqs)
	if err != nil {
		return SourceIngestResult{}, err
	}
	return SourceIngestResult{PreparationFailures: prepared.PreparationFailures, Ingestions: results}, nil
}
