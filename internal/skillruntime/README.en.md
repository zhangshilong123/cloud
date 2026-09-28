# internal/skillruntime: server-side Skill retrieval / verification / verified cache / Attempt projection / READY / spawn gate

[中文](README.md) | [English](README.en.md)

`internal/skillruntime` is the production server-side Skill materialization **runtime slice (6A.3 + 6A.4)**:
it expresses the ADR-frozen «consume a `RetrievalCapability` → bounded download → `package_digest`
verification → single-codec decode → `content_digest` verification → verified immutable cache →
**Attempt-scoped projection → runtime/provider adapter → READY barrier → spawn-gate abstraction →
cleanup/bounded GC**» as directly testable code. It is the seam the 6B dispatch consumes through
`Store.SkillMaterializer` + `Store.SkillProjector`. The normative contracts are
`specs/decisions/cloud/skills/20260928-server-side-skill-retrieval-verification-cache.md` and
`specs/decisions/cloud/skills/20260928-attempt-projection-runtime-adapter-ready-spawn-gate.md`.

## Responsibilities (6A.3 — verified immutable cache)

- **Input**: a `FrozenSkillBundle` (immutable delivery metadata: `skill_revision_id`, `content_digest`,
  `package_digest`, `package_format`, `size_bytes`) plus a caller-supplied `RetrievalCapability` (bearer).
- **Retrieval**: bounded fail-closed HTTPS GET (cloned transport, never replaced; `CheckRedirect` refuses
  3xx; `readBounded`; expiry pre-check; status → typed sentinel).
- **Verification (fixed order)**: `package_digest` (`skillstore.PackageDigestHex` byte identity) →
  `skillpkg.Decode` (the single codec, a verifier) → `content_digest` (`TreeDigest`) → `size_bytes`
  (content total) consistency.
- **Cache**: identity `sha256/<content_digest>`; same-fs `<root>/.staging/` staging + atomic rename publish +
  a `.ora-skill-complete` marker (no capability); hits issue zero object-storage requests; corruption fails
  closed.
- **Output**: a `VerifiedSkillBundle` (`content_digest` + `digest_algorithm` + `CacheDir`), a read-only
  reference — the copy source for the 6A.4 projection.

## Responsibilities (6A.4 — Attempt projection / READY / spawn gate)

- **Projection** (`projection.go`): `Projector.Project(ctx, attemptID, []ProjectionSkill)` **copies** (never
  writable-hardlinks) the verified canonical tree to
  `<attempt_root>/attempts/<attempt_id>/<skill_revision_id>/`; deterministic ordering
  `canonical_name→skill_id→skill_revision_id`, directory name always `skill_revision_id` (immutable frozen
  identity); stage first, then one atomic `os.Rename`, so a partial projection is never runtime-visible.
  Produces a `PreparedAttempt`.
- **READY barrier** (`projection.go`): `Ready(ctx, PreparedAttempt)` re-reads the `.ora-attempt-ready`
  ownership/READY marker and confirms every required Skill dir — never inferred from files-on-disk; missing
  marker → `ErrNotReady`, corrupt/identity-mismatch → `ErrAttemptCorrupt`. Produces a `ReadyAttempt`.
- **Runtime adapter** (`adapter.go`): `AgentRuntimeAdapter` (`AttemptRoot` / `Provisions`) is the read-only
  consumption boundary from READY state to provider layout; the default `FSAdapter` is provider-neutral and
  executes nothing.
- **Spawn gate** (`gate.go`): `SpawnGate.Open(ReadyAttempt) → LaunchSpec` is the type-level preparation→spawn
  gate, accepting only a `ReadyAttempt`; this slice never execs, never proxies bytes, never starts a daemon.
- **Cleanup / GC** (`cleanup.go`): `CleanupAttempt` is idempotent and deletes only the owned projection;
  `CollectStaging` sweeps staging residue; `Materializer.Collect` bounds the cache by age / oldest-first
  count, independent of projections.

## Invariants and boundaries

- Fail closed end to end: any failed step discards the bytes; no partial cache or partial projection ever
  becomes visible.
- `package_digest` (byte layer) and `content_digest` (content tree) are two independent checks, never
  substitutable.
- Cache key = `digest_algorithm + content_digest`, never `package_digest` or any business identity.
- Only fully-verified trees become visible via an atomic publish; corruption is never re-downloaded-over or
  mutated in place.
- Verified cache and Attempt projection are distinct layers: the cache is never mutated by a projection
  (copy, never hardlink).
- A projection belongs to exactly one Attempt (key is `attempt_id` only); a retry is a new Attempt that
  reuses the cache.
- READY is an explicit barrier, never inferred from partial filesystem state; spawn opens only on a
  `ReadyAttempt`.
- Capability URL/signature is never persisted/logged/returned/embedded in an error (transport errors are
  discarded).
- Package content is never executed; external IO runs outside any DB transaction; no mutable Skill lookup;
  no `os/exec` / `net/http` / DB work in this package.
- The package imports only `internal/skillpkg` + `internal/skillstore` (never `internal/config`); wiring is
  done by `cmd/server` from the `runtime` config section (`skill_cache_root` + `skill_attempt_root`).

See [AGENTS.md](../../AGENTS.md), the [internal module map](../README.en.md),
`specs/decisions/cloud/skills/20260928-web-runtime-skill-materialization-ownership.md`,
`specs/decisions/cloud/skills/20260928-server-side-skill-retrieval-verification-cache.md`, and
`specs/decisions/cloud/skills/20260928-attempt-projection-runtime-adapter-ready-spawn-gate.md`.