# cmd/ora-skill-materialize: Node-local one-shot Skill materialization helper

[中文](README.md) | [English](README.en.md)

`ora-skill-materialize` is the sandbox-local, **one-shot Go materialization helper** for the assigned Node
(6B.2B). It reuses Cloud's canonical `internal/skillpkg` codec and `internal/skillruntime` materializer on
the Node-local filesystem end-to-end — never a second Rust codec/materializer. The frozen boundary is
`specs/decisions/cloud/skills/20260929-node-runtime-materialization-placement.md` (Candidate B).

## Purpose

The Rust `ora-node` host/guardian spawns this helper **inside the assigned Node sandbox**, by absolute path,
over a **closed stdin/stdout channel**. The helper consumes a JSON request carrying the frozen Attempt +
frozen Skill bundle metadata + the ephemeral `RetrievalCapability`, runs the canonical chain:

```
EnsureVerified → Project → Ready → SpawnGate.Open
```

and emits a narrow, non-secret preparedness fact. **It never starts the real Agent process** (that is 6B.2C).

## Invocation contract

- argv/env: **no credential**. Capabilities (signed URLs) arrive only via the stdin JSON.
- stdin: one JSON request (below), bounded read (`maxRequestBytes = 4 MiB`; over → `request_too_large`).
- stdout: one JSON result. stderr carries no URL/signature.
- Exit code: `0` prepared; `1` preparation_failed (a result with `stable_error_code` was still written to
  stdout); `2` malformed request (unreadable stdin / invalid JSON / oversized — no materialization attempted).

## Request (stdin)

```json
{
  "attempt_id": "<uuid>",
  "cache_root": "/var/lib/ora/cache",
  "attempt_root": "/var/lib/ora/runtime",
  "skills": [
    {
      "skill_id": "<uuid>",
      "canonical_name": "alpha",
      "skill_revision_id": "<uuid>",
      "content_digest_algorithm": "sha256",
      "content_digest": "<64 lowercase hex>",
      "package_digest_algorithm": "sha256",
      "package_digest": "<64 lowercase hex>",
      "package_format": "ora-skill-package",
      "package_format_version": 1,
      "size_bytes": 1234
    }
  ],
  "capabilities": [
    {
      "skill_revision_id": "<uuid>",
      "method": "GET",
      "url": "https://… (bearer signed URL)",
      "expires_at": "2026-09-29T12:00:00Z"
    }
  ]
}
```

`skills` is the `execution_skill_bindings` frozen snapshot (immutable delivery identity — never a mutable
current revision or object locator); `capabilities` are keyed exactly by `skill_revision_id`, and `url` is a
bearer that exists only on this closed channel.

## Result (stdout)

Success:

```json
{"attempt_id":"<uuid>","prepared":true,"local_root":"/var/lib/ora/runtime/attempts/<attempt_id>"}
```

Failure (`prepared:false` + stable non-secret code; no URL/path):

```json
{"attempt_id":"<uuid>","prepared":false,"stable_error_code":"package_digest_mismatch"}
```

`local_root` is the Node-local published Attempt projection root — the same filesystem host as the future
6B.2C executor. The `LaunchSpec` is a pure runtime-local handoff: the helper never returns it, never persists
it, and never writes it into the result.

## Secret handling (redline)

- The signed URL appears only on stdin; never in argv, env, stdout, stderr, any file, marker, projection,
  result, or `attempt_result` durable row.
- A materialization failure returns only a `stable_error_code` (`errors.Is` classification to a `skillruntime`
  sentinel); the raw transport error — which may embed the signed URL/signature — is discarded.
- `RetrievalCapability.String()` prints only `method`/`expires_at`, never the URL.

## Stable error codes (§41)

| Code | Trigger |
| --- | --- |
| `retrieval_unauthorized` / `retrieval_not_found` / `retrieval_redirect` / `retrieval_transient` | the 4 bounded-download outcomes |
| `size_mismatch` | over-bound body, or decoded content total ≠ frozen `size_bytes` |
| `package_digest_mismatch` | `SHA256(bytes) ≠ package_digest` |
| `decode_failed` / `content_digest_mismatch` | package decode failure / tree digest ≠ `content_digest` |
| `cache_corrupt` / `cache_publish` | pre-existing corrupt cache entry / atomic publish failure |
| `projection_mismatch` / `projection_collision` | projection identity mismatch / same-name runtime collision |
| `attempt_corrupt` / `not_ready` / `projection_publish` | inconsistent published projection / READY not passed / projection publish failure |
| `unsupported_digest_algorithm` / `unsupported_package_format` | non-sha256 / non-`ora-skill-package` v1 |
| `internal` | everything else (including an internally inconsistent request: a missing capability) |

## What it does NOT do

- **Not** start the Agent process/container (6B.2C); no heartbeat/exit/cancel/recovery (6B.3).
- **Not** write durable business state: Attempt state advances only through the Controller's fenced
  `attempt_result`.
- **Not** produce a Rust codec/materializer duplicate: this helper is the single Node-side consumer of the
  canonical Go implementation.

See the [cmd module map](../README.en.md), [internal/skillruntime](../../internal/skillruntime/README.en.md),
and [AGENTS.md](../../AGENTS.md).