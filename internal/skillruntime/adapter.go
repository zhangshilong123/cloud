package skillruntime

// AgentRuntimeAdapter is the boundary between the runtime-owned READY state and a concrete Agent
// runtime's provider-specific layout (Node materialization ADR D12/D37, server-side). It turns a
// ReadyAttempt into the ordered provisions an Agent runtime exposes, WITHOUT spawning anything, WITHOUT
// touching mutable Skill/current-revision state (D13), and WITHOUT rewriting canonical package bytes
// (D14/D40). A provider adapter (Codex/Claude/…) implements this in a later slice; this package ships
// only the canonical server filesystem layout it composes.
type AgentRuntimeAdapter interface {
	// AttemptRoot returns the published projection root the Agent runtime should mount/expose for the
	// ready Attempt.
	AttemptRoot(a ReadyAttempt) string
	// Provisions returns the ordered, complete Skill materializations for a ready Attempt: each is one
	// required Skill's immutable runtime name mapped to its published projection directory.
	Provisions(a ReadyAttempt) []SkillProjection
}

// FSAdapter is the canonical, provider-neutral AgentRuntimeAdapter: it exposes the published projection
// exactly as materialized (attempt root plus the ordered skills). It performs no transformation, no
// filesystem mutation, and no lookup — it is the default layout a spawn consumes before a provider adds
// native discovery conventions.
type FSAdapter struct{}

// AttemptRoot returns the runtime-owned published projection root.
func (FSAdapter) AttemptRoot(a ReadyAttempt) string { return a.Root }

// Provisions returns the ordered complete Skill projections carried by the ReadyAttempt manifest.
func (FSAdapter) Provisions(a ReadyAttempt) []SkillProjection { return a.Skills }
