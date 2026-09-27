# internal/skillmeta: SKILL.md metadata parsing and canonical_name derivation

[中文](README.md) | [English](README.en.md)

`internal/skillmeta` is Cloud Skills' **business metadata layer**: it parses the `SKILL.md` YAML
frontmatter and deterministically derives `canonical_name`. It is pure CPU, deterministic, and
I/O-free, and is strictly separate from the identity layer `internal/skillpkg` (frozen at Step 2A,
which only validates that `SKILL.md` exists and is UTF-8 — it never parses `name`). The contract is
`specs/decisions/cloud/skills/20260927-skill-md-metadata-contract.md`.

## Responsibilities

- **Envelope**: the frontmatter must start with a `---` line at byte 0; the closing fence is the next
  line whose content is exactly `---`. A BOM, leading content, or a missing closing fence fails
  closed (`skill_md_no_frontmatter`).
- **name (the single identity field)**: required and a YAML string scalar; after trimming ASCII
  whitespace it must match `[A-Za-z0-9._-]+`, not start with `.`, and be ≤ 200 bytes. Missing /
  non-string / invalid chars / too long each yield a stable `error_code`, with **no fallback** to the
  directory name, archive filename, or `display_name`.
- **canonical_name**: `ASCII lowercase(name)` is the single transformation (no NFC, no slugify, no
  extra trim).
- **description**: optional, a string, ≤ 4096 bytes after a Unicode trim; absent or trimmed-empty
  returns `""` (matching `SkillRevision.package_description NOT NULL DEFAULT ''` — never `NULL`).
- **Duplicate keys / invalid YAML fail closed**: the body is parsed into a `yaml.Node` AST and
  duplicate top-level keys are detected here (`skill_md_duplicate_key`), independent of `yaml.v3`'s
  map-decode behavior; unknown fields and the body are opaque and preserved (read-only extraction,
  never a rewrite).

## API

```go
meta, err := skillmeta.Parse(skillMDBytes)   // (Metadata, error); err carries a stable Code via *ParseError
meta.Name                                     // required, trimmed, validated name
meta.Description                              // optional description, trimmed; "" when absent
meta.CanonicalName()                          // ASCII lowercase(name)
skillmeta.CodeOf(err)                         // stable error code ("" when not a *ParseError)
```

Failure codes: `skill_md_no_frontmatter` / `skill_md_invalid_yaml` / `skill_md_missing_name` /
`skill_md_name_not_string` / `skill_md_name_invalid_chars` / `skill_md_name_too_long` /
`skill_md_description_invalid` / `skill_md_duplicate_key`.

## Boundaries

- Read-only extraction: never mutates the input bytes, never rewrites the frontmatter, no NFC/NFD
  normalization, no slugify.
- The failure-code strings are this implementation's realization of the contract's failure *modes*
  (the contract freezes the conditions, not the strings).
- No `name == parent directory` consistency check: `canonical_name` must derive from content bytes
  alone (see the contract's open questions).

See [AGENTS.md](../../AGENTS.md), the [internal module overview](../README.md), and
`specs/decisions/cloud/skills/20260927-skill-md-metadata-contract.md`.
