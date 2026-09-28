package skillruntime

// LaunchSpec is the provider-neutral launch input a future Agent runtime consumes to open the spawn
// barrier. It is produced only from a ReadyAttempt, so it can never describe a partial or un-ready
// Attempt. New: it is a pure DTO — the actual process/container spawn is a 6B/Phase 2 Substrate concern.
type LaunchSpec struct {
	AttemptID  string
	Root       string
	Provisions []SkillProjection
}

// SpawnGate is the preparation→spawn barrier (Node materialization ADR D21/D41). It is the only seam
// through which a future dispatch slice may obtain spawn inputs, and it accepts only a ReadyAttempt: a
// raw attempt id, a PreparedAttempt that has not passed the READY barrier, or any partial filesystem
// state cannot open it. This slice implements the gate itself (type-level readiness); it performs no
// process spawn and no byte-proxy.
type SpawnGate struct {
	adapter AgentRuntimeAdapter
}

// NewSpawnGate returns a SpawnGate over the supplied layout adapter; a nil adapter falls back to the
// canonical FSAdapter.
func NewSpawnGate(adapter AgentRuntimeAdapter) *SpawnGate {
	if adapter == nil {
		adapter = FSAdapter{}
	}
	return &SpawnGate{adapter: adapter}
}

// Open assembles the provider-neutral launch inputs for a ReadyAttempt. It never blocks, performs no IO
// beyond what the adapter needs, and never spawns a process.
func (g *SpawnGate) Open(a ReadyAttempt) LaunchSpec {
	return LaunchSpec{
		AttemptID:  a.AttemptID,
		Root:       g.adapter.AttemptRoot(a),
		Provisions: g.adapter.Provisions(a),
	}
}
