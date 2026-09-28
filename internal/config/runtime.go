package config

import (
	"fmt"
	"strings"
	"time"
)

// Default Server-side Skill runtime timeouts, applied when the deployer omits them. They bound the
// capability download (connect and end-to-end), mirroring the storage section's shape.
const (
	defaultRuntimeConnectTimeout = 5 * time.Second
	defaultRuntimeRequestTimeout = 30 * time.Second
)

// RuntimeConfig is the optional `runtime` section (6A.3: server-side Skill retrieval, verification,
// and verified cache). It is a pointer field on Config: a nil pointer means the section is absent, the
// process starts normally, and the materializer stays unwired (Store.SkillMaterializer == nil). A
// non-nil pointer with invalid values fails startup. skill_cache_root has no default — a shared-machine
// path must never be invented (plan §19) — so it is required whenever the section is present.
type RuntimeConfig struct {
	// SkillCacheRoot is the filesystem root under which verified immutable cache entries live
	// (<root>/sha256/<content_digest>/). Required when `runtime` is present.
	SkillCacheRoot string `mapstructure:"skill_cache_root"`

	// SkillAttemptRoot is the filesystem root under which Attempt-scoped Skill projections live
	// (<root>/attempts/<attempt_id>/). Required when `runtime` is present (6A.4).
	SkillAttemptRoot string `mapstructure:"skill_attempt_root"`

	// Retrieval bounds the signed-URL download HTTP client.
	Retrieval RuntimeRetrievalConfig `mapstructure:"retrieval"`
}

// RuntimeRetrievalConfig bounds the capability download (connect and end-to-end).
type RuntimeRetrievalConfig struct {
	ConnectTimeout time.Duration `mapstructure:"connect_timeout"`
	RequestTimeout time.Duration `mapstructure:"request_timeout"`
}

// applyDefaults fills the frozen defaults for fields the deployer omitted. It runs after unmarshalling
// and before Validate.
func (r *RuntimeConfig) applyDefaults() {
	if r.Retrieval.ConnectTimeout == 0 {
		r.Retrieval.ConnectTimeout = defaultRuntimeConnectTimeout
	}
	if r.Retrieval.RequestTimeout == 0 {
		r.Retrieval.RequestTimeout = defaultRuntimeRequestTimeout
	}
}

// Validate rejects an unusable `runtime` section before the process serves. It never probes the
// network or the filesystem.
func (r *RuntimeConfig) Validate() error {
	if strings.TrimSpace(r.SkillCacheRoot) == "" {
		return fmt.Errorf("runtime.skill_cache_root is required")
	}
	if strings.TrimSpace(r.SkillAttemptRoot) == "" {
		return fmt.Errorf("runtime.skill_attempt_root is required")
	}
	if r.Retrieval.ConnectTimeout <= 0 {
		return fmt.Errorf("runtime.retrieval.connect_timeout must be positive")
	}
	if r.Retrieval.RequestTimeout <= 0 {
		return fmt.Errorf("runtime.retrieval.request_timeout must be positive")
	}
	return nil
}
