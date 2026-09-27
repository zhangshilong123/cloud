# internal/skillpkg: Canonical Skill Package v1 content layer

[中文](README.md) | [English](README.en.md)

`internal/skillpkg` is the pure content implementation of the Cloud Skill canonical package contract
(`ora-skill-package` v1): it deterministically maps a Skill's canonical virtual file tree into ManifestV1,
a tree content digest, and `ora-skill-package` v1 container bytes. Cloud ingestion and future Node
verification share this byte layout. The normative contract is
`specs/decisions/cloud/skills/20260924-canonical-skill-package-v1.md`.

## Responsibilities

- **Canonical path validation**: reject, don't clean — absolute paths, `.`/`..`, empty components, NUL,
  Windows drive prefixes, and trailing `/` are rejected; the `\` → `/` source-boundary mapping is followed
  by full re-validation, never silent normalization.
- **ManifestV1**: big-endian `manifest_version` + `file_count`, then per-file `path_length` / `path` /
  `file_size` / `file_sha256` (raw 32 bytes). No JSON/protobuf, padding, optional fields, or timestamps.
- **Digests**: per-file `SHA256(exact file bytes)`; tree digest =
  `SHA256("ora-skill-tree-v1" || 0x00 || manifest)`, raw `[32]byte` internally, lowercase hex as text.
- **Container**: `"ORASKILL"` + `package_version` + `manifest_length` + manifest + file bodies spliced in
  manifest order, with no footer/trailer/central directory.
- **Decode-as-verify**: magic, versions, every length/count bound, ordering, dedup, case-collision,
  per-file SHA-256, and exact EOF are all checked; the digest is recomputed from the manifest, never trusted.
- **Bounds**: `Limits` / `DefaultLimits()` (provisional library defaults, not a product quota).

## Invariants and boundaries

- Pure content library: it touches no database, Object Storage, Node filesystem, or process/network state —
  it only reads, validates, hashes, encodes, decodes, and verifies bytes.
- Same canonical tree (same paths, same bytes) ⇒ same manifest / digest / container (independent of order,
  separators, and archive metadata).
- It never executes any file content (including `.sh` / `.py` / `.js` / binaries).
- `SKILL.md` metadata parsing and directory/archive discovery (zip/tar decoding) are later slices, not
  implemented here.

See [AGENTS.md](../../AGENTS.md), the [internal module map](../README.en.md), and
`specs/decisions/cloud/skills/0-cloud-skills.md`.