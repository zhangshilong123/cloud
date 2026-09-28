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
- **name (the single identity field)**: required and a YAML string scalar; after a Unicode trim it
  must be non-empty, valid UTF-8, free of NUL/control characters, not start with `.`, and be ≤ 200
  bytes. The name is **bounded Unicode**: CJK names (`律师助手`), and mixed `legal-助手` are accepted
  verbatim — no transliteration, no pinyin, no ASCII slug. Missing / non-string / empty / control
  chars / leading-dot / too long each yield a stable `error_code`, with **no fallback** to the
  directory name, archive filename, or `display_name`.
- **canonical_name (the comparison key)**: `NFC(normalize + Unicode case-fold(name))` is the single
  transformation (no slugify, no pinyin, no extra trim, no transliteration). It is a case-insensitive
  Unicode comparison key; for ASCII input it is byte-identical to the old `ASCII lowercase(name)`
  (`Lawyer-Assistant` → `lawyer-assistant`).
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
meta.CanonicalName()                          // NFC + Unicode case-fold comparison key
skillmeta.CodeOf(err)                         // stable error code ("" when not a *ParseError)
```

Failure codes: `skill_md_no_frontmatter` / `skill_md_invalid_yaml` / `skill_md_missing_name` /
`skill_md_name_not_string` / `skill_md_name_invalid_chars` / `skill_md_name_invalid_control_chars` /
`skill_md_name_too_long` / `skill_md_description_invalid` / `skill_md_duplicate_key`.

## Boundaries

- Read-only extraction: never mutates the input bytes, never rewrites the frontmatter, no
  slugify/transliteration, no pinyin/ASCII suffix.
- Canonical normalization uses `golang.org/x/text` (NFC + `cases.Fold`), not a hand-rolled Unicode
  lowercase.
- The failure-code strings are this implementation's realization of the contract's failure *modes*
  (the contract freezes the conditions, not the strings).
- No `name == parent directory` consistency check: `canonical_name` must derive from content bytes
  alone (see the contract's open questions).

See [AGENTS.md](../../AGENTS.md), the [internal module overview](../README.md), and
`specs/decisions/cloud/skills/20260927-skill-md-metadata-contract.md`.
