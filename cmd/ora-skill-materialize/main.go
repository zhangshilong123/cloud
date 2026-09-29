// Command ora-skill-materialize is the Node-local, one-shot Go materialization helper (Phase 6B.2B).
//
// It is the production Skill materialization runtime on the assigned sandbox Node, frozen in
// specs/decisions/cloud/skills/20260929-node-runtime-materialization-placement.md (Candidate B): the
// Rust ora-node host/guardian invokes this binary **by absolute path over a closed stdin/stdout
// channel** with a JSON request that carries the frozen Attempt + Skill bundle metadata and the
// ephemeral RetrievalCapabilities, and the helper runs the canonical materialization chain —
// EnsureVerified → Project → Ready → SpawnGate.Open — reusing cloud/internal/skillpkg +
// cloud/internal/skillruntime end-to-end (no Rust codec/materializer duplicate). It emits a narrow,
// non-secret JSON result; it never starts the real Agent process (Phase 6B.2C).
//
// Security: the signed successive URL is a bearer credential. It arrives only on stdin (the closed
// channel), never in argv/env, and this process never writes it to stdout, stderr, any file, or any
// error/result. See README.md for the exact wire contract.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/wanglongan587/cloud/internal/skillruntime"
	"github.com/wanglongan587/cloud/internal/skillstore"
)

// maxRequestBytes bounds the stdin request read. The signed-URL signatures in the capabilities are the
// dominant field; 4 MiB accommodates a large frozen binding set while still refusing a runaway stream.
const maxRequestBytes = 4 << 20

// request is the helper stdin wire shape: the frozen Attempt identity, the two filesystem roots the
// Node owns locally, the frozen Skill delivery metadata, and the ephemeral retrieval capabilities
// keyed by skill_revision_id. The capability URL is a bearer and exists only on this closed channel.
type request struct {
	AttemptID    string       `json:"attempt_id"`
	CacheRoot    string       `json:"cache_root"`
	AttemptRoot  string       `json:"attempt_root"`
	Skills       []skillEntry `json:"skills"`
	Capabilities []capability `json:"capabilities"`
}

// skillEntry is one frozen delivery descriptor (the execution_skill_bindings snapshot). It carries the
// exact immutable delivery identity; never a mutable current revision, object locator, or capability.
type skillEntry struct {
	SkillID           string `json:"skill_id"`
	CanonicalName     string `json:"canonical_name"`
	SkillRevisionID   string `json:"skill_revision_id"`
	ContentDigestAlgo string `json:"content_digest_algorithm"`
	ContentDigest     string `json:"content_digest"`
	PackageDigestAlgo string `json:"package_digest_algorithm"`
	PackageDigest     string `json:"package_digest"`
	PackageFormat     string `json:"package_format"`
	PackageFormatVer  int    `json:"package_format_version"`
	SizeBytes         int64  `json:"size_bytes"`
}

// capability is one ephemeral retrieval credential over a frozen revision. URL is a bearer: never
// logged, persisted, or echoed into the result.
type capability struct {
	SkillRevisionID string `json:"skill_revision_id"`
	Method          string `json:"method"`
	URL             string `json:"url"`
	ExpiresAt       string `json:"expires_at"` // RFC3339; empty means "no expiry pre-check"
}

// result is the helper stdout wire shape: a narrow, non-secret preparedness fact. On success it is
// {attempt_id, prepared:true, local_root}; on failure {attempt_id, prepared:false, stable_error_code}.
// It never carries a signed URL, a path beyond the local projection root, or a LaunchSpec (which stays
// runtime-local and is never relayed or persisted).
type result struct {
	AttemptID       string `json:"attempt_id"`
	Prepared        bool   `json:"prepared"`
	LocalRoot       string `json:"local_root,omitempty"`
	StableErrorCode string `json:"stable_error_code,omitempty"`
}

func main() {
	os.Exit(run(context.Background(), os.Stdin, os.Stdout))
}

// run reads the bounded request from in, materializes it, and writes the non-secret result to out. It
// returns the process exit code: 0 prepared, 1 preparation_failed (a result with a stable code was
// still written), 2 malformed request (stdin unreadable/decode failure — no materialization attempted).
func run(ctx context.Context, in io.Reader, out io.Writer) int {
	data, err := io.ReadAll(io.LimitReader(in, maxRequestBytes))
	if err != nil {
		writeResult(out, result{StableErrorCode: "unreadable_request"})
		return 2
	}
	if len(data) >= maxRequestBytes {
		writeResult(out, result{StableErrorCode: "request_too_large"})
		return 2
	}
	var req request
	if err := json.Unmarshal(data, &req); err != nil {
		writeResult(out, result{StableErrorCode: "invalid_request"})
		return 2
	}

	res, err := materialize(ctx, req)
	if err != nil {
		if !writeResult(out, result{AttemptID: req.AttemptID, Prepared: false, StableErrorCode: stableCode(err)}) {
			return 2
		}
		return 1
	}
	if !writeResult(out, res) {
		return 2
	}
	return 0
}

// materialize runs the canonical chain on the Node-local filesystem. The prepared result is the
// published Attempt projection root; the LaunchSpec it also produces is a pure runtime-local handoff
// and is deliberately not returned (it must never be relayed or persisted).
func materialize(ctx context.Context, req request) (result, error) {
	mat, err := skillruntime.New(skillruntime.Config{CacheRoot: req.CacheRoot})
	if err != nil {
		return result{}, err
	}
	proj, err := skillruntime.NewProjector(req.AttemptRoot)
	if err != nil {
		return result{}, err
	}
	gate := skillruntime.NewSpawnGate(nil)

	caps := make(map[string]skillstore.RetrievalCapability, len(req.Capabilities))
	for _, c := range req.Capabilities {
		exp, perr := parseExpiry(c.ExpiresAt)
		if perr != nil {
			return result{}, perr
		}
		caps[c.SkillRevisionID] = skillstore.RetrievalCapability{URL: c.URL, Method: c.Method, ExpiresAt: exp}
	}

	projections := make([]skillruntime.ProjectionSkill, 0, len(req.Skills))
	for _, e := range req.Skills {
		cap, ok := caps[e.SkillRevisionID]
		if !ok {
			return result{}, fmt.Errorf("request missing retrieval capability for revision %s", e.SkillRevisionID)
		}
		bundle := skillruntime.FrozenSkillBundle{
			SkillRevisionID:   e.SkillRevisionID,
			ContentDigestAlgo: e.ContentDigestAlgo,
			ContentDigest:     e.ContentDigest,
			PackageDigestAlgo: e.PackageDigestAlgo,
			PackageDigest:     e.PackageDigest,
			PackageFormat:     e.PackageFormat,
			PackageFormatVer:  e.PackageFormatVer,
			SizeBytes:         e.SizeBytes,
		}
		verified, err := mat.EnsureVerified(ctx, bundle, cap)
		if err != nil {
			return result{}, err
		}
		projections = append(projections, skillruntime.ProjectionSkill{
			SkillID:       e.SkillID,
			CanonicalName: e.CanonicalName,
			Bundle:        bundle,
			Verified:      *verified,
		})
	}

	prepared, err := proj.Project(ctx, req.AttemptID, projections)
	if err != nil {
		return result{}, err
	}
	ready, err := proj.Ready(ctx, *prepared)
	if err != nil {
		return result{}, err
	}
	_ = gate.Open(*ready) // runtime-local LaunchSpec; only the projection root is relayed
	return result{AttemptID: req.AttemptID, Prepared: true, LocalRoot: prepared.Root}, nil
}

// parseExpiry converts the capability expiry to a time.Time; an empty value means "no pre-check".
func parseExpiry(s string) (time.Time, error) {
	if s == "" {
		return time.Time{}, nil
	}
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		return time.Time{}, fmt.Errorf("invalid capability expires_at %q", s)
	}
	return t, nil
}

// stableCode maps a materialization failure to a stable, non-secret §41 error code. The signed URL,
// signature, path, or provider detail never appears in the returned code or in the underlying error's
// classification; the raw error text is discarded.
func stableCode(err error) string {
	switch {
	case errors.Is(err, skillruntime.ErrRetrievalUnauthorized):
		return "retrieval_unauthorized"
	case errors.Is(err, skillruntime.ErrRetrievalNotFound):
		return "retrieval_not_found"
	case errors.Is(err, skillruntime.ErrRetrievalRedirect):
		return "retrieval_redirect"
	case errors.Is(err, skillruntime.ErrRetrievalTransient):
		return "retrieval_transient"
	case errors.Is(err, skillruntime.ErrSizeMismatch):
		return "size_mismatch"
	case errors.Is(err, skillruntime.ErrPackageDigestMismatch):
		return "package_digest_mismatch"
	case errors.Is(err, skillruntime.ErrDecodeFailed):
		return "decode_failed"
	case errors.Is(err, skillruntime.ErrContentDigestMismatch):
		return "content_digest_mismatch"
	case errors.Is(err, skillruntime.ErrCacheCorrupt):
		return "cache_corrupt"
	case errors.Is(err, skillruntime.ErrCachePublish):
		return "cache_publish"
	case errors.Is(err, skillruntime.ErrProjectionMismatch):
		return "projection_mismatch"
	case errors.Is(err, skillruntime.ErrProjectionCollision):
		return "projection_collision"
	case errors.Is(err, skillruntime.ErrAttemptCorrupt):
		return "attempt_corrupt"
	case errors.Is(err, skillruntime.ErrNotReady):
		return "not_ready"
	case errors.Is(err, skillruntime.ErrProjectionPublish):
		return "projection_publish"
	case errors.Is(err, skillruntime.ErrUnsupportedDigestAlgorithm):
		return "unsupported_digest_algorithm"
	case errors.Is(err, skillruntime.ErrUnsupportedPackageFormat):
		return "unsupported_package_format"
	default:
		return "internal"
	}
}

// writeResult encodes res as a single JSON line to out and reports success.
func writeResult(out io.Writer, res result) bool {
	enc := json.NewEncoder(out)
	enc.SetEscapeHTML(false)
	return enc.Encode(res) == nil
}
