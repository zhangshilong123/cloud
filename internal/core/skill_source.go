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

// IngestSource is the single seam between source preparation and the ingestion saga: it maps
// each PreparedCandidate to a SkillIngestRequest and runs the existing IngestSkills
// partial-success saga under one idempotency key. It owns no business effect of its own —
// authz, idempotency, storage, and activation remain in the saga. PreparationFailures pass
// through unchanged (they never reach the saga and never create rows).
func (s *Store) IngestSource(ctx context.Context, workspaceID, actorID, key string, prepared *skillsource.PreparedSourceResult) (SourceIngestResult, error) {
	if prepared == nil {
		return SourceIngestResult{}, nil
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
			Files:          c.Files,
		})
	}
	results, err := s.IngestSkills(ctx, workspaceID, actorID, key, sourceType, reqs)
	if err != nil {
		return SourceIngestResult{}, err
	}
	return SourceIngestResult{PreparationFailures: prepared.PreparationFailures, Ingestions: results}, nil
}
