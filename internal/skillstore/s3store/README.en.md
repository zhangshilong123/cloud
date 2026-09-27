# internal/skillstore/s3store: production S3-compatible Object Storage adapter

[中文](README.md) | [English](README.en.md)

`internal/skillstore/s3store` is the **sole production implementation** of `skillstore.ObjectStore`. It talks
to a single S3-compatible endpoint (AWS S3 / MinIO / Ceph RGW / Alibaba OSS, …) via
`aws-sdk-go-v2/service/s3`, frozen in
`specs/decisions/cloud/skills/20260927-production-object-storage-provider.md`.

Key semantics:

- **Create-only = one conditional write**: `PutImmutable` issues a single `PutObject` with
  `If-None-Match: *`; a `412` maps to `PutAlreadyExists`. There is **no** HEAD-then-PUT TOCTOU path and no
  multipart upload (ADR D3/D4).
- **Three operations only**: `PutImmutable` / `Stat` (HEAD) / `Get` (bounded read); no List / Delete /
  Presign / GC.
- **Conservative error classification**: SDK errors are mapped into the frozen taxonomy (ADR D6/D7) —
  not_found / already_exists / temporary / permanent / ambiguous — then into the five-value outcomes. Raw
  SDK errors never cross the port. Anything where the remote *may* have accepted the request is ambiguous and
  is resolved by the saga's `Reconcile` probe, never a blind re-PUT.
- **Bounded timeouts and transport**: connect 5s / request 30s (configurable); each operation derives its
  deadline from the caller's context and never runs unbounded; the HTTP transport is derived from
  `http.DefaultTransport.Clone()`, never mutating any global default.
- **Credentials come from the deployment platform**: `credential_mode` = environment (`AWS_ACCESS_KEY_ID`
  etc.) / shared_credentials_file / workload_identity (IMDS / ECS / IRSA); credentials never enter the DB,
  logs, requests, or responses.

## Configuration

This package accepts only an already-resolved `Config`, deliberately decoupled from `internal/config` to avoid
the `core → skillstore → s3store → config → core` import cycle. `cmd/server` translates the `storage` section
into it at wiring time:

| Field | Meaning |
| --- | --- |
| `Region` / `Bucket` | region placeholder and the single bucket (required) |
| `Endpoint` / `PathStyle` | optional custom endpoint; `PathStyle=true` selects path-style addressing |
| `CredentialMode` / `CredentialsFile` | credential mode and the shared_credentials_file path |
| `InsecureSkipVerify` / `CAFile` | TLS verification switch (resolved from `tls.verify`) and a custom CA |
| `ConnectTimeout` / `RequestTimeout` | connect / per-request timeouts (defaults 5s / 30s already applied) |

`New(ctx, cfg)` performs only local validation and client construction; it never probes bucket reachability
(ADR D14), which is classified on first use. When the section is absent, `cmd/server` never constructs this
package (`SkillsObjectStore` stays nil).

## Tests

`s3store_test.go` injects a fake through a minimal `api` interface to assert: the `If-None-Match: *` and body
are passed through verbatim, the error-matrix maps to the five-value outcomes, and bounded `Get` (present /
oversize / exactly maxBytes / absent). A separate `httptest.Server` acts as a fake S3 endpoint to prove,
**through the real SDK**, end-to-end conditional write and 412 → `PutAlreadyExists`.

See [AGENTS.md](../../../AGENTS.md), the [skillstore notes](../README.en.md), and
`specs/decisions/cloud/skills/20260927-production-object-storage-provider.md`.
