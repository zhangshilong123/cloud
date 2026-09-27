# internal/skillstore: Object Storage abstraction (port / semantic types / reconciliation core)

[中文](README.md) | [English](README.en.md)

`internal/skillstore` is the **provider-neutral Object Storage semantic layer** for Cloud Skill canonical
packages: it expresses the ADR-frozen immutable create-only write, the stable physical identity, the
ambiguous-result classification, and the probe-by-`object_locator` reconciliation as directly testable code.
It does **not** implement any concrete cloud vendor SDK, HTTP upload, ingestion pipeline, `SKILL.md`
discovery, or `RetrievalCapability`. The normative contract is
`specs/decisions/cloud/skills/20260927-object-storage-abstraction.md`.

## Responsibilities

- **Physical identity**: `PackageDigestHex(b) = SHA256(exact ora-skill-package bytes)` (lowercase 64-hex),
  i.e. `package_digest` (D4), strictly distinct from the Step 2A tree content digest.
- **Stable logical key**: `Locator` encodes
  `skills/<package_format>/v<package_format_version>/<digest_algorithm>/<package_digest_hex>`
  (provider-independent, business-identity-free, ≤ 277 bytes < the 1024 schema bound); `NewLocator` /
  `ParseLocator` reject path traversal, non-lowercase hex, and unknown format/algorithm.
- **ObjectStore port**: `PutImmutable` (create-only, never overwrite), `Stat` (existence only, evidence is
  never identity), `Get` (bounded, `maxBytes`); no List/Delete/Copy/Move/Presign.
- **Result classification**: each operation returns a typed outcome (`PutCreated`/`PutAlreadyExists`/
  `PutDefiniteFailureTransient`/`PutDefiniteFailurePermanent`/`PutAmbiguous`; `Stat…`; `Get…`) — a caller
  must never decide whether to retry from `err != nil` alone.
- **Reconcile**: probes at `want.Locator` (Stat → bounded Get → local verification) and returns a unified
  `Verdict` (`ConfirmedPresentMatching`/`ConfirmedAbsent`/`Mismatch`/`DefiniteFailure{Transient,Permanent}`/
  `Indeterminate`); the three checks are `SHA256(fetched)==package_digest` → `skillpkg.Decode` succeeds →
  `TreeDigest==content_digest`, and any failure is `Mismatch` (fail closed).

## Invariants and boundaries

- **Create-only**: no overwrite/upsert/replace; no `Stat(); if absent: Put()` (TOCTOU); a provider without
  conditional-create support is unsupported, never a correctness degradation.
- **Three-layer identity separation**: `SkillRevision.id` (business), `digest_algorithm+content_digest`
  (tree content), `package_digest` (physical bytes); `content_digest != package_digest`.
- **Timeout is not absent**: a `Stat` timeout / could-not-check is never mapped to `StatAbsent`.
- **`MISMATCH` fails closed**: never overwrite/delete/rewrite/adopt/change-key/retry the PUT.
- **No retry/scheduling in the provider-neutral layer**: no retry loop / worker / scheduler / DB orchestration.
- Pure semantics: it touches no database, concrete provider, HTTP handler, ingestion saga, or execution path.

See [AGENTS.md](../../AGENTS.md), the [internal module map](../README.en.md),
`specs/decisions/cloud/skills/20260927-object-storage-abstraction.md`, and the test double
[fakestore](fakestore/README.en.md).
