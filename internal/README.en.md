# internal: Authoritative Cloud Subsystems

[中文](README.md) | [English](README.en.md)

`internal` hosts the private implementation packages for Ora Cloud. Per the repository architectural boundary rules documented in `AGENTS.md`, all core state, policy, translation, and infrastructure adapters remain private under `internal/`.

## Module map

- [core](core/README.en.md) is the authoritative domain core, owning business state machines, transaction boundaries, database advisory locks, cryptographic authentication, and the Cloud Skill ingestion saga (`Store.IngestSkill` / `Store.IngestSkills`).
  - [migrations](core/migrations/README.en.md) contains ordered, forward-only PostgreSQL schema migration scripts and checksum verification.
- [api](api/README.en.md) is the HTTP presentation layer.
  - [router](api/router/README.en.md) binds HTTP routes, verifies two-tier JWT credentials, parses JSON request bodies, and projects domain errors into stable contracts.
- [gateway](gateway/README.en.md) is the public authentication boundary: PostgreSQL-backed Login Attempts and Browser Sessions, provider-neutral login orchestration, internal JWT issuance, cookie/CSRF/redirect protection, and the `/api/v1` proxy.
  - [idaas](gateway/idaas/README.en.md) is the Huawei IDaaS 2.0 (`client_secret_post`) adapter that produces a `VerifiedIdentity` with `source=huawei-corp`.
  - [github](gateway/github/README.en.md) is the GitHub OAuth App adapter that produces a `VerifiedIdentity`.
  - [devlogin](gateway/devlogin/README.en.md) is the development-only provider: a local form where any typed identity signs in, registered solely on loopback development origins.
- [contract](contract/README.en.md) defines OpenAPI 3.0 schema models, DTO structures, and contract coverage tests.
- [skillpkg](skillpkg/README.en.md) is the pure content layer for Cloud Skill canonical package v1 — canonical path validation, ManifestV1, tree digest, and `ora-skill-package` v1 encode/decode/verify, with no database or storage dependency.
- [skillmeta](skillmeta/README.en.md) is the business metadata layer for Cloud Skills — it parses the `SKILL.md` YAML frontmatter, validates `name`/`description`, and derives `canonical_name = ASCII lowercase(name)`; pure CPU, deterministic, I/O-free, and strictly separate from `internal/skillpkg` (the identity layer, which never parses `name`).
- [skillstore](skillstore/README.en.md) is the provider-neutral Object Storage semantic layer for Cloud Skill canonical packages — the physical `package_digest`, the stable logical object key, a create-only `ObjectStore` port, the five-class result taxonomy, and the probe-by-`object_locator` reconciliation; no concrete provider / upload HTTP API — the `internal/core` ingestion saga drives `PutImmutable`/`Reconcile` through `Store.SkillsObjectStore`.
  - [fakestore](skillstore/fakestore/README.en.md) is the deterministic in-memory test double for `skillstore.ObjectStore`, scriptable to every failure mode.
- [skillsource](skillsource/README.en.md) is the Cloud Skill source intake layer — it ingests directory / ZIP / uncompressed TAR into normalized `sourceEntry`s, performs source-level safety validation and `SKILL.md` candidate discovery, and produces `PreparedCandidate`s that feed the `IngestSkills` saga; purely in-memory, it persists nothing and reuses `skillpkg.ValidatePath` and `skillmeta.Parse`.
- [repository](repository/README.en.md) manages PostgreSQL database connection pools and startup health checks via GORM.
- [config](config/README.en.md) loads and validates application configuration files and environment overrides.
- [logger](logger/README.en.md) provides structured, non-blocking JSON logging via Zap and Lumberjack.
- [simulator](simulator/README.en.md) implements in-process doubles for the Substrate execution engine, Controller, and Workspace Node.
- [controlpb](controlpb/README.en.md) holds the gRPC Go code (server stubs and messages) generated from the Controller internal control contract under [`proto/`](../proto/README.en.md); read-only.
- [controlgrpc](controlgrpc/README.en.md) serves that contract over gRPC: the caller-identity interceptor, `Fault` → status mapping, the lease service; translation only, no business rules.

## Layering and architectural rules

1. **Unidirectional dependencies**:
   - `cmd/*` $\rightarrow$ `internal/api/router`, `internal/gateway`, `internal/core`, `internal/config`, `internal/logger`, `internal/repository`.
   - `internal/gateway` $\rightarrow$ `internal/core` (the `Claims` type only), `internal/config`, `internal/logger`; `internal/gateway/idaas`, `internal/gateway/github` and `internal/gateway/devlogin` $\rightarrow$ `internal/gateway`. The Gateway never queries Cloud business tables.
   - `internal/api/router` $\rightarrow$ `internal/core`, `internal/contract`; `internal/controlgrpc` $\rightarrow$ `internal/core`, `internal/controlpb`.
   - `internal/core` $\rightarrow$ standard library, `gorm.io/gorm`, `internal/core/migrations`, `internal/skillpkg`, `internal/skillmeta`, `internal/skillstore`, `internal/skillsource` (only `IngestSource` consumes `PreparedSourceResult`).
   - `internal/repository` $\rightarrow$ `internal/config`, `gorm.io/gorm`.
- `internal/skillpkg` $\rightarrow$ standard library only (no internal dependencies).
   - `internal/skillmeta` $\rightarrow$ standard library, `gopkg.in/yaml.v3` (no internal dependencies).
   - `internal/skillstore` $\rightarrow$ standard library, `internal/skillpkg` (`internal/skillstore/fakestore` $\rightarrow$ `internal/skillstore`).
   - `internal/skillsource` $\rightarrow$ standard library, `internal/skillpkg`, `internal/skillmeta` (never imports `core` / `skillstore`).
   - Lower layers (`core`, `repository`) never import upper presentation layers (`api`, `router`).
2. **PostgreSQL is authoritative**:
   - All shared state is persisted in PostgreSQL. In-memory caching of authoritative domain state across requests is strictly prohibited.
3. **Transaction boundary**:
   - Database transactions and advisory locks are strictly localized to PostgreSQL operations inside `core.Store.transact`. Transactions must **never** be held across external HTTP requests, Git CLI commands, or filesystem I/O.
4. **Error handling**:
   - Public-facing errors use the stable `Fault` structure (`Code`, `Params`, `Status`). Internal SQL, stack traces, and database errors are logged internally and never returned to clients.

See [AGENTS.md](../AGENTS.md), [Core contract](../docs/core-contract.md), and [Authentication](../docs/authentication.md).
