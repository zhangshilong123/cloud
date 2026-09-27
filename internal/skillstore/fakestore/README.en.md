# internal/skillstore/fakestore: in-memory test double

[中文](README.md) | [English](README.en.md)

`internal/skillstore/fakestore` is a **deterministic in-memory test double** for `skillstore.ObjectStore`,
not a production provider. Its default behavior is honest: `PutImmutable` is an atomic create-only write
under a mutex, `Stat` reports present/absent with a size hint, and `Get` returns the exact bytes or bounds
the read at `maxBytes`. Tests may override `PutFn` / `StatFn` / `GetFn` to inject scripted outcomes covering
every failure mode the reconciliation contract requires (ADR D25).

## Uses

- **Create-only assertions**: a second `PutImmutable` / `Seed` at the same key reports already-exists and
  never overwrites the existing bytes.
- **Bounded Get**: `maxBytes` smaller than the object length → `GetOversize`.
- **Scripted outcomes**: `StatFn`/`GetFn` returning `StatIndeterminate`/`GetIndeterminate` verify `Reconcile`
  classification passthrough.
- **`Seed`**: places an object directly, bypassing `PutFn`, to simulate "an earlier ambiguous PUT actually
  did (or did not) land" or to plant a corrupt object at a key.

## Boundaries

- Test-only: no network, no real storage, no semantics beyond mutual exclusion; production providers
  (S3/GCS/Azure/MinIO SDKs) are later steps, not implemented here.

See [AGENTS.md](../../../AGENTS.md), the [skillstore notes](../README.en.md), and
`specs/decisions/cloud/skills/20260927-object-storage-abstraction.md`.
