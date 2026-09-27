# internal/skillsource: Source intake & candidate discovery

[中文](README.md) | [English](README.en.md)

`internal/skillsource` is the Cloud Skills **source intake layer**: it ingests directory / ZIP / uncompressed
TAR transports into normalized `sourceEntry` streams, performs source-level safety validation and `SKILL.md`
candidate discovery, and produces `PreparedCandidate`s that feed the existing `IngestSkills` saga. It persists
nothing itself — identity (`skillpkg`), metadata (`skillmeta`), and persistence (`skillstore` / `core`) stay
in their own packages. The normative contract is
`specs/decisions/cloud/skills/20260927-source-intake-candidate-discovery.md`.

## Responsibilities

- **Transport adapters**: `PrepareDirectory(root)`, `PrepareZip(data)`, and `PrepareTar(data)` read three
  inputs into one `sourceEntry{path, data}` stream; archives support ZIP and uncompressed TAR only
  (`.zip` / `.tar`). `.tar.gz` / `.tgz` / gzip are unsupported — no sniffing, no ZIP↔TAR fallback.
- **Source-level safety (fail-whole-source)**: canonical path validation (reusing `skillpkg.ValidatePath`),
  exact duplicate paths, case collisions, archive symlink/special entries, traversal / absolute / Windows
  drive prefixes, and every limit (archive bytes / entries / expanded bytes / per-file bytes / path bytes).
- **Candidate discovery**: exact root `SKILL.md` (root candidate owning the whole tree) or `*/SKILL.md`
  (nested root); candidates are ordered by canonical root bytes, and `partition` slices per-candidate files
  while enforcing per-candidate `skillpkg.Limits`.
- **Metadata derivation**: each candidate is derived via `skillmeta.Parse` into `canonical_name` /
  `package_name` / `package_description`; a parse failure yields a `PreparationFailure` (non-durable, does not
  block sibling candidates).
- **SourceType**: directory → `"directory"`, zip/tar → `"archive"` (decided by the caller-supplied
  `SourceKind`, never by filename/MIME inference).

## Result model

```go
type PreparedSourceResult struct {
    Candidates          []PreparedCandidate   // valid candidates, fed to the IngestSkills saga
    PreparationFailures []PreparationFailure  // candidate-level metadata failures only (non-durable)
}
```

`Prepare*` returns either a `PreparedSourceResult` or a source-level structural error (`ErrUnsupportedKind` /
`ErrInvalidArchive` / `ErrUnsupportedType` / `ErrUnsafePath` / `ErrDuplicatePath` / `ErrCaseCollision` /
`ErrNestedRoot` / `ErrDuplicateName` / `ErrNoCandidates` / `ErrLimit`). A structural error rejects the **whole
source**: no `PreparedCandidate` is produced and no `skills` / `skill_revisions` / `skill_ingestions` rows are
written (`CodeOf(err)` returns a stable code).

## Failure layers

- **A source structural** (invalid archive / unsafe path / unsupported type / duplicate path / case collision /
  nested roots / duplicate canonical_name / no candidates / limits) → whole source rejected, zero rows.
- **B candidate preparation** (invalid `SKILL.md` metadata) → `PreparationFailure{CandidateRoot, ErrorCode,
  Detail}`, non-durable, siblings continue.
- **C candidate business** (authz / idempotency / storage / CAS) → existing `IngestSkills` partial-success,
  outside this package.

## Dependency direction

`skillsource → skillpkg (paths/limits) + skillmeta (metadata)`, never the reverse; `core` consumes
`PreparedSourceResult` via `IngestSource`. `skillsource` does not import `core` / `skillstore`.

## Non-goals

- Never executes any file content; never infers SourceKind / MIME / filenames; never gunzips; no NFC/NFD
  normalization.
- No name-vs-parent-directory consistency check; hardlinks are read as regular files (a portable API cannot
  distinguish them).
- No public upload HTTP API, production Object Storage provider, Node delivery, or Agent-Execution binding.

See [AGENTS.md](../../AGENTS.md), the [internal module map](../README.en.md), and
`specs/decisions/cloud/skills/0-cloud-skills.md`.
