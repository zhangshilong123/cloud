package controlgrpc

import (
	"context"

	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/wanglongan587/cloud/internal/controlpb"
)

// MintSkillRetrieval mints one short-lived, GET-only, exact-object retrieval capability per frozen
// skill revision, after Cloud verifies the durable (execution, attempt, skill_revision) authority
// chain (Step 5B ADR). The Controller only requests/refreshes; it never selects a revision, supplies
// a locator, or persists the capability (D8/D9). The URL is a bearer credential and is never logged.
func (s *executionService) MintSkillRetrieval(ctx context.Context, req *controlpb.MintSkillRetrievalRequest) (*controlpb.MintSkillRetrievalResponse, error) {
	caps, e := s.store.MintSkillRetrievalCapabilities(ctx, req.GetExecutionId(), req.GetAttemptId(), req.GetSkillRevisionIds())
	if e != nil {
		return nil, toStatus(e)
	}
	out := make([]*controlpb.SkillRetrievalCapability, 0, len(caps))
	for _, c := range caps {
		out = append(out, &controlpb.SkillRetrievalCapability{
			SkillRevisionId: c.SkillRevisionID,
			Method:          c.Method,
			Url:             c.URL,
			ExpiresAt:       timestamppb.New(c.ExpiresAt),
		})
	}
	return &controlpb.MintSkillRetrievalResponse{Capabilities: out}, nil
}
