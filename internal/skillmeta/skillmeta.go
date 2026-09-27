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
//   - name is required and must be a YAML string scalar; after trimming ASCII whitespace it
//     must match ASCII [A-Za-z0-9._-]+, not start with ".", and be at most 200 bytes;
//   - canonical_name is the single transformation ASCII-lowercase(name);
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

	"gopkg.in/yaml.v3"
)

// Stable failure codes. A caller maps these to skill_ingestions.error_code and to its own
// HTTP status. They are this package's implementation of the failure modes frozen by the
// metadata contract, which left the exact strings to the implementation slice and gave these
// names as the example codes.
const (
	CodeNoFrontmatter      = "skill_md_no_frontmatter"
	CodeInvalidYAML        = "skill_md_invalid_yaml"
	CodeMissingName        = "skill_md_missing_name"
	CodeNameNotString      = "skill_md_name_not_string"
	CodeNameInvalidChars   = "skill_md_name_invalid_chars"
	CodeNameTooLong        = "skill_md_name_too_long"
	CodeDescriptionInvalid = "skill_md_description_invalid"
	CodeDuplicateKey       = "skill_md_duplicate_key"
)

// Metadata is the extracted SKILL.md identity. Name is the required, trimmed, validated
// name; Description is the optional description, trimmed, or "" when absent or
// whitespace-only (the exact value a caller writes to SkillRevision.package_description,
// which is NOT NULL and never receives a NULL).
type Metadata struct {
	Name        string
	Description string
}

// CanonicalName returns the single deterministic transformation of Name: ASCII lowercase.
// Parse guarantees Name is already trimmed and ASCII-only, so this is a total function;
// folding ASCII (not Unicode) keeps it faithful to the contract even for a hand-constructed
// Metadata value.
func (m Metadata) CanonicalName() string {
	b := []byte(m.Name)
	for i, c := range b {
		if c >= 'A' && c <= 'Z' {
			b[i] = c + ('a' - 'A')
		}
	}
	return string(b)
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
	trimmed := trimASCII(name.Value)
	if !validNameBytes(trimmed) {
		return Metadata{}, parseError(CodeNameInvalidChars, fmt.Sprintf("frontmatter \"name\" %q is not ASCII [A-Za-z0-9._-]+ or starts with \".\"", trimmed))
	}
	if len(trimmed) > 200 {
		return Metadata{}, parseError(CodeNameTooLong, fmt.Sprintf("frontmatter \"name\" is %d bytes (max 200)", len(trimmed)))
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

// validNameBytes reports whether s is a non-empty ASCII [A-Za-z0-9._-]+ value that does not
// start with ".". The charset is checked byte-wise (the name is ASCII-only by contract).
func validNameBytes(s string) bool {
	if s == "" || s[0] == '.' {
		return false
	}
	for i := 0; i < len(s); i++ {
		switch c := s[i]; {
		case c >= 'a' && c <= 'z':
		case c >= 'A' && c <= 'Z':
		case c >= '0' && c <= '9':
		case c == '.' || c == '_' || c == '-':
		default:
			return false
		}
	}
	return true
}

// trimASCII trims only ASCII whitespace (0x09-0x0D and 0x20). The contract specifies ASCII
// whitespace for name because name's charset is ASCII-only; a Unicode space must survive the
// trim and fail the charset check rather than be silently dropped.
func trimASCII(s string) string {
	return strings.TrimFunc(s, func(r rune) bool {
		switch r {
		case ' ', '\t', '\n', '\v', '\f', '\r':
			return true
		}
		return false
	})
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
