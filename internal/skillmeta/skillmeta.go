// Package skillmeta parses the SKILL.md YAML frontmatter contract and derives the
// canonical_name identity. It is the business metadata layer: pure CPU, deterministic,
// no I/O, and deliberately distinct from internal/skillpkg (the identity layer), which
// stays frozen and never parses name.
//
// The frozen contract is
// specs/decisions/cloud/skills/20260927-skill-md-metadata-contract.md:
//
//   - metadata sits in a YAML frontmatter block whose first byte is a "---" line and whose
//     closing fence is the next line that is exactly "---" (no BOM, no leading content);
//   - name is required and must be a YAML string scalar; after trimming Unicode whitespace it
//     must be non-empty, valid UTF-8, contain no NUL/control characters, not start with ".",
//     and be at most 200 bytes. The name is bounded Unicode: CJK names such as 律师助手 are
//     accepted verbatim (no transliteration, no slugify, no ASCII fallback);
//   - canonical_name is the single transformation NFC-normalize + Unicode case-fold(name) — a
//     case-insensitive Unicode comparison key that separates the human-readable name from the
//     canonical identity. ASCII input still folds to lowercase (Lawyer-Assistant →
//     lawyer-assistant), so existing ASCII rows are unchanged;
//   - description is optional; when present it must be a YAML string scalar of at most 4096
//     bytes (after trimming), and is returned trimmed ("" when absent or whitespace-only);
//   - duplicate keys and invalid YAML fail closed; unknown fields and the Markdown body are
//     opaque and preserved (this package never rewrites the frontmatter).
//
// Parse reads the input without mutating it and returns a *ParseError carrying a stable
// machine-readable Code on every failure.
package skillmeta

import (
	"errors"
	"fmt"
	"strings"
	"unicode/utf8"

	"golang.org/x/text/cases"
	"golang.org/x/text/unicode/norm"

	"gopkg.in/yaml.v3"
)

// Stable failure codes. A caller maps these to skill_ingestions.error_code and to its own
// HTTP status. They are this package's implementation of the failure modes frozen by the
// metadata contract, which left the exact strings to the implementation slice and gave these
// names as the example codes.
const (
	CodeNoFrontmatter           = "skill_md_no_frontmatter"
	CodeInvalidYAML             = "skill_md_invalid_yaml"
	CodeMissingName             = "skill_md_missing_name"
	CodeNameNotString           = "skill_md_name_not_string"
	CodeNameInvalidChars        = "skill_md_name_invalid_chars"
	CodeNameInvalidControlChars = "skill_md_name_invalid_control_chars"
	CodeNameTooLong             = "skill_md_name_too_long"
	CodeDescriptionInvalid      = "skill_md_description_invalid"
	CodeDuplicateKey            = "skill_md_duplicate_key"
)

// Metadata is the extracted SKILL.md identity. Name is the required, trimmed, validated
// name; Description is the optional description, trimmed, or "" when absent or
// whitespace-only (the exact value a caller writes to SkillRevision.package_description,
// which is NOT NULL and never receives a NULL).
type Metadata struct {
	Name        string
	Description string
}

// CanonicalName returns the single deterministic transformation of Name — a case-insensitive
// Unicode comparison key: NFC-normalize then Unicode case-fold. Parse guarantees Name is
// already trimmed and valid (bounded Unicode), so this is a total function even for a
// hand-constructed Metadata value. ASCII input still folds to lowercase, so existing ASCII
// canonicals are unchanged; CJK text is invariant (no transliteration, no slugify, no pinyin).
func (m Metadata) CanonicalName() string {
	return canonicalKey(m.Name)
}

// canonicalKey computes the canonical comparison key for a validated name. Order matters:
// NFC first (so canonically-equivalent inputs collapse), then full case-fold (whose output is
// not guaranteed composed), then NFC again for a single canonical form the DB uniqueness index
// sees byte-for-byte identically.
func canonicalKey(name string) string {
	return norm.NFC.String(cases.Fold().String(norm.NFC.String(name)))
}

// ParseError is a deterministic metadata parse failure. Code is a stable machine-readable
// enum (see the Code* constants); Detail is a safe, human-readable diagnostic that never
// carries provider, credential, or internal-path information.
type ParseError struct {
	Code   string
	Detail string
}

func (e *ParseError) Error() string {
	if e.Detail == "" {
		return "skillmeta: " + e.Code
	}
	return "skillmeta: " + e.Code + ": " + e.Detail
}

// CodeOf returns the stable failure code carried by err, or "" when err is not a *ParseError.
func CodeOf(err error) string {
	var pe *ParseError
	if errors.As(err, &pe) {
		return pe.Code
	}
	return ""
}

func parseError(code, detail string) error {
	return &ParseError{Code: code, Detail: detail}
}

// Parse extracts Metadata from the exact SKILL.md bytes.
func Parse(skillMD []byte) (Metadata, error) {
	body, ok := splitFrontmatter(skillMD)
	if !ok {
		return Metadata{}, parseError(CodeNoFrontmatter, "SKILL.md does not begin with a \"---\"-fenced YAML frontmatter block")
	}

	var doc yaml.Node
	if err := yaml.Unmarshal(body, &doc); err != nil {
		return Metadata{}, parseError(CodeInvalidYAML, err.Error())
	}
	root := frontmatterRoot(&doc)
	if root == nil || root.Kind != yaml.MappingNode {
		return Metadata{}, parseError(CodeInvalidYAML, "frontmatter body must be a YAML mapping")
	}

	var name, description *yaml.Node
	seen := make(map[string]struct{}, len(root.Content)/2)
	for i := 0; i+1 < len(root.Content); i += 2 {
		key := root.Content[i]
		if _, dup := seen[key.Value]; dup {
			return Metadata{}, parseError(CodeDuplicateKey, fmt.Sprintf("duplicate frontmatter key %q", key.Value))
		}
		seen[key.Value] = struct{}{}
		switch key.Value {
		case "name":
			name = root.Content[i+1]
		case "description":
			description = root.Content[i+1]
		}
	}

	if name == nil {
		return Metadata{}, parseError(CodeMissingName, "frontmatter has no top-level \"name\"")
	}
	if !isStringScalar(name) {
		return Metadata{}, parseError(CodeNameNotString, "frontmatter \"name\" must be a YAML string")
	}
	trimmed := strings.TrimSpace(name.Value)
	if err := validateName(trimmed); err != nil {
		return Metadata{}, err
	}
	// The canonical key must fit the DB's CHECK(length BETWEEN 1 AND 200) (PostgreSQL length()
	// counts characters, not bytes); NFC/case-fold never shrinks, but folding can grow the rune
	// count (e.g. İ → i̇), so guard the derived key too.
	if utf8.RuneCountInString(canonicalKey(trimmed)) > 200 {
		return Metadata{}, parseError(CodeNameTooLong, fmt.Sprintf("frontmatter \"name\" canonicalizes longer than 200 characters (max 200)"))
	}

	meta := Metadata{Name: trimmed}
	if description == nil {
		return meta, nil
	}
	if !isStringScalar(description) {
		return Metadata{}, parseError(CodeDescriptionInvalid, "frontmatter \"description\" must be a YAML string")
	}
	desc := strings.TrimSpace(description.Value)
	if len(desc) > 4096 {
		return Metadata{}, parseError(CodeDescriptionInvalid, fmt.Sprintf("frontmatter \"description\" is %d bytes (max 4096)", len(desc)))
	}
	meta.Description = desc
	return meta, nil
}

// isStringScalar reports whether n is a YAML string scalar ("!!str"). Bool/int/float/null,
// sequences, mappings, and aliases all fail this check, per the contract's strict string
// typing for name and description.
func isStringScalar(n *yaml.Node) bool {
	return n.Kind == yaml.ScalarNode && n.Tag == "!!str"
}

// validateName enforces the bounded-Unicode name rules on the already Unicode-trimmed value:
// non-empty, valid UTF-8, no NUL/control characters, no leading ".", and at most 200 bytes.
// Every failure is a distinct ParseError code (they are never collapsed into one invalid_name).
func validateName(s string) error {
	if s == "" {
		return parseError(CodeMissingName, "frontmatter \"name\" is empty after trimming whitespace")
	}
	if s[0] == '.' {
		return parseError(CodeNameInvalidChars, `frontmatter "name" must not start with "."`)
	}
	if !utf8.ValidString(s) {
		return parseError(CodeNameInvalidChars, `frontmatter "name" is not valid UTF-8`)
	}
	if hasControlChar(s) {
		return parseError(CodeNameInvalidControlChars, `frontmatter "name" contains control characters`)
	}
	if len(s) > 200 {
		return parseError(CodeNameTooLong, fmt.Sprintf("frontmatter \"name\" is %d bytes (max 200)", len(s)))
	}
	return nil
}

// hasControlChar reports whether s contains a NUL, C0 (0x00–0x1F), DEL (0x7F), or C1
// (0x80–0x9F) control character. Valid CJK, punctuation, and whitespace runes pass.
func hasControlChar(s string) bool {
	for _, r := range s {
		if r < 0x20 || r == 0x7f || (r >= 0x80 && r <= 0x9f) {
			return true
		}
	}
	return false
}

// frontmatterRoot returns the root node of a parsed document, or nil for an empty document.
func frontmatterRoot(doc *yaml.Node) *yaml.Node {
	if doc.Kind != yaml.DocumentNode || len(doc.Content) == 0 {
		return nil
	}
	return doc.Content[0]
}

// splitFrontmatter returns the YAML body between the opening "---" fence at byte 0 and the
// next line whose content (after a trailing CR) is exactly "---", or !ok when there is no such
// fence. It never mutates data. A BOM, leading whitespace, a non-"---" first line, or a
// missing closing fence all yield !ok (the contract fails closed on each).
func splitFrontmatter(data []byte) (body []byte, ok bool) {
	if len(data) < 4 || data[0] != '-' || data[1] != '-' || data[2] != '-' {
		return nil, false
	}
	start := 0
	switch data[3] {
	case '\n':
		start = 4
	case '\r':
		if len(data) < 5 || data[4] != '\n' {
			return nil, false
		}
		start = 5
	default:
		return nil, false
	}

	for i := start; i < len(data); {
		lineStart := i
		for i < len(data) && data[i] != '\n' {
			i++
		}
		line := data[lineStart:i]
		if len(line) > 0 && line[len(line)-1] == '\r' {
			line = line[:len(line)-1]
		}
		if string(line) == "---" {
			return data[start:lineStart], true
		}
		if i >= len(data) {
			break
		}
		i++ // skip the newline
	}
	return nil, false
}
