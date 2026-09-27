package router

import (
	"errors"
	"io"
	"mime/multipart"
	"net/http"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"

	"github.com/wanglongan587/cloud/internal/core"
	"github.com/wanglongan587/cloud/internal/skillsource"
)

// skillsUploadPath is the single public route that accepts a multipart Skill source. It must stay
// in lock-step with Routes() and with the Gateway's route-aware body-limit exemption
// (internal/gateway/handler.go requestBodyLimit).
const skillsUploadPath = "/api/v1/tenants/:tid/spaces/:spaceId/skills/imports"

// skillsUploadBudget is the route-local request-body ceiling for the public Skill source upload
// (public upload ADR D14). Every other route keeps the router's 64 KiB JSON ceiling; only this
// route may accept up to 256 MiB, and it is still ephemeral-only: the archive is decoded in
// memory and never durably staged.
const skillsUploadBudget = 256 << 20

// uploadSkillSource handles the dedicated multipart Skill source upload route. It is registered
// separately from the generic JSON loop because it carries a multipart body under its own body
// budget; authorization, idempotency, storage, and activation all stay in the saga.
func uploadSkillSource(store *core.Store, auth *core.Authenticator, log *zap.Logger, c *gin.Context) {
	_, user, fault := verifyPublicCredentials(c, auth)
	if fault != nil {
		failure(c, fault)
		return
	}

	// Idempotency-Key is required (D6): missing → idempotency_key_required; empty/oversized →
	// invalid_idempotency_key. Checked before parsing so a malformed request fails fast without
	// buffering the 256 MiB body.
	key := c.GetHeader("Idempotency-Key")
	if key == "" {
		if _, present := c.Request.Header["Idempotency-Key"]; !present {
			failure(c, &core.Fault{Code: "idempotency_key_required", Status: 400, Params: core.Object{}})
			return
		}
		failure(c, &core.Fault{Code: "invalid_idempotency_key", Status: 400, Params: core.Object{}})
		return
	}
	if len(key) > 200 {
		failure(c, &core.Fault{Code: "invalid_idempotency_key", Status: 400, Params: core.Object{}})
		return
	}

	// Route-local body ceiling. The cheap known-length check rejects before any buffering; the
	// MaxBytesReader bounds a streamed body while the multipart parser reads it.
	if c.Request.ContentLength > skillsUploadBudget {
		failure(c, &core.Fault{Code: "upload_too_large", Status: 413, Params: core.Object{}})
		return
	}
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, skillsUploadBudget)
	if err := c.Request.ParseMultipartForm(32 << 20); err != nil {
		var maxErr *http.MaxBytesError
		if errors.As(err, &maxErr) {
			failure(c, &core.Fault{Code: "upload_too_large", Status: 413, Params: core.Object{}})
			return
		}
		failure(c, &core.Fault{Code: "invalid_multipart", Status: 400, Params: core.Object{}})
		return
	}
	form := c.Request.MultipartForm

	// Strict field set: text fields are source_kind/target_skill_id/display_name/summary, the one
	// file field is source. Unknown parts — including the forbidden canonical_name/content_digest/
	// package_digest/object_locator/revision_id — and duplicate parts are rejected (D5).
	for name, values := range form.Value {
		switch name {
		case "source_kind", "target_skill_id", "display_name", "summary":
		default:
			failure(c, &core.Fault{Code: "unknown_field", Status: 400, Params: core.Object{"field": name}})
			return
		}
		if len(values) > 1 {
			failure(c, &core.Fault{Code: "duplicate_field", Status: 400, Params: core.Object{"field": name}})
			return
		}
	}
	for name, files := range form.File {
		if name != "source" {
			failure(c, &core.Fault{Code: "unknown_field", Status: 400, Params: core.Object{"field": name}})
			return
		}
		if len(files) > 1 {
			failure(c, &core.Fault{Code: "duplicate_field", Status: 400, Params: core.Object{"field": name}})
			return
		}
	}

	sourceKindValues := form.Value["source_kind"]
	if len(sourceKindValues) == 0 {
		failure(c, &core.Fault{Code: "missing_field", Status: 400, Params: core.Object{"field": "source_kind"}})
		return
	}
	sourceKind := skillsource.SourceKind(sourceKindValues[0])
	if sourceKind != skillsource.KindZip && sourceKind != skillsource.KindTar {
		failure(c, &core.Fault{Code: "source_unsupported_source_kind", Status: 400, Params: core.Object{}})
		return
	}

	sourceFiles := form.File["source"]
	if len(sourceFiles) == 0 {
		failure(c, &core.Fault{Code: "missing_field", Status: 400, Params: core.Object{"field": "source"}})
		return
	}

	targetSkillID := firstValue(form.Value, "target_skill_id")
	displayName := firstValue(form.Value, "display_name")
	summary := firstValue(form.Value, "summary")
	if len(displayName) > 200 {
		failure(c, &core.Fault{Code: "invalid_field_type", Status: 400, Params: core.Object{"field": "display_name"}})
		return
	}
	if len(summary) > 4096 {
		failure(c, &core.Fault{Code: "invalid_field_type", Status: 400, Params: core.Object{"field": "summary"}})
		return
	}

	data, err := readSourcePart(sourceFiles[0])
	if err != nil {
		log.Error("skill upload source read failed", zap.String("requestId", c.GetString("requestId")), zap.Error(err))
		failure(c, &core.Fault{Code: "internal_error", Status: 500, Params: core.Object{}})
		return
	}

	var prepared *skillsource.PreparedSourceResult
	switch sourceKind {
	case skillsource.KindZip:
		prepared, err = skillsource.PrepareZip(data, skillsource.DefaultLimits())
	default:
		prepared, err = skillsource.PrepareTar(data, skillsource.DefaultLimits())
	}
	if err != nil {
		// Source-level structural failure: zero candidates were formed, so it is a 400 + source_*
		// (D7), never a 200 envelope.
		failure(c, &core.Fault{Code: skillsource.CodeOf(err), Status: 400, Params: core.Object{}})
		return
	}

	uid, err := store.ResolveIdentity(c.Request.Context(), user.Source, user.Subject, user.DisplayName)
	if err != nil {
		failure(c, core.ErrorCode(err))
		return
	}

	result, err := store.IngestSource(c.Request.Context(), c.Param("spaceId"), uid, key, prepared, core.SourceIngestParams{
		TargetSkillID: targetSkillID,
		DisplayName:   displayName,
		Summary:       summary,
	})
	if err != nil {
		f := core.ErrorCode(err)
		if f.Status == 500 {
			log.Error("skill upload failed", zap.String("requestId", c.GetString("requestId")), zap.Error(err))
		}
		failure(c, f)
		return
	}
	c.JSON(http.StatusOK, sourceUploadResponse(sourceKind, prepared, result))
}

func firstValue(values map[string][]string, name string) string {
	if v := values[name]; len(v) > 0 {
		return v[0]
	}
	return ""
}

func readSourcePart(fh *multipart.FileHeader) ([]byte, error) {
	f, err := fh.Open()
	if err != nil {
		return nil, err
	}
	defer f.Close()
	return io.ReadAll(f)
}

// sourceUploadResponse projects the saga result plus the prepared candidates into the public 200
// envelope { sourceKind, preparationFailures[], ingestions[] } (D7/D8/D9). Identity fields that
// only resolve after the saga (skillId/revisionId/ingestionId) are null when absent; candidateRoot
// and canonicalName come from the prepared candidates, which are in the same order as the saga's
// per-candidate results.
func sourceUploadResponse(kind skillsource.SourceKind, prepared *skillsource.PreparedSourceResult, result core.SourceIngestResult) gin.H {
	failures := make([]gin.H, 0, len(result.PreparationFailures))
	for _, pf := range result.PreparationFailures {
		failures = append(failures, gin.H{"candidateRoot": pf.CandidateRoot, "errorCode": pf.ErrorCode, "detail": pf.Detail})
	}
	ingestions := make([]gin.H, 0, len(result.Ingestions))
	for i, ing := range result.Ingestions {
		item := gin.H{
			"candidateRoot": "",
			"canonicalName": "",
			"skillId":       optionalID(ing.SkillID),
			"revisionId":    optionalID(ing.RevisionID),
			"ingestionId":   optionalID(ing.IngestionID),
			"state":         ing.State,
			"activation":    ing.Activation,
			"replayed":      ing.Replayed,
			"errorCode":     ing.ErrorCode,
		}
		if prepared != nil && i < len(prepared.Candidates) {
			item["candidateRoot"] = prepared.Candidates[i].CandidateRoot
			item["canonicalName"] = prepared.Candidates[i].CanonicalName
		}
		ingestions = append(ingestions, item)
	}
	return gin.H{"sourceKind": string(kind), "preparationFailures": failures, "ingestions": ingestions}
}

func optionalID(s string) any {
	if s == "" {
		return nil
	}
	return s
}
