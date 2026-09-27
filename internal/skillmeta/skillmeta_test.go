package skillmeta

import (
	"bytes"
	"strings"
	"testing"
)

// fm wraps a frontmatter body in the "---" fences and appends a Markdown body, matching the
// canonical SKILL.md layout exercised by the parser.
func fm(body string) []byte {
	return []byte("---\n" + body + "\n---\n# body\n")
}

func TestParseValid(t *testing.T) {
	cases := []struct {
		name      string
		input     []byte
		wantName  string
		wantCanon string
		wantDesc  string
	}{
		{"minimal", fm("name: foo"), "foo", "foo", ""},
		{"name and description", fm("name: foo\ndescription: does a thing"), "foo", "foo", "does a thing"},
		{"quoted name", fm("name: \"foo\""), "foo", "foo", ""},
		{"single-quoted name", fm("name: 'foo'"), "foo", "foo", ""},
		{"canonical lowercase", fm("name: Review"), "Review", "review", ""},
		{"digits and separators", fm("name: My.Skill-v2_alpha"), "My.Skill-v2_alpha", "my.skill-v2_alpha", ""},
		{"all uppercase", fm("name: HTTP_CLIENT"), "HTTP_CLIENT", "http_client", ""},
		{"description trimmed", fm("name: foo\ndescription: \"  hello world  \""), "foo", "foo", "hello world"},
		{"empty description string", fm("name: foo\ndescription: \"\""), "foo", "foo", ""},
		{"whitespace description", fm("name: foo\ndescription: \"   \""), "foo", "foo", ""},
		{"unknown fields opaque", fm("name: foo\nlicense: MIT\nversion: 1\ncategory: dev"), "foo", "foo", ""},
		{"description before name", fm("description: x\nname: foo"), "foo", "foo", "x"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := Parse(tc.input)
			if err != nil {
				t.Fatalf("Parse() err = %v", err)
			}
			if got.Name != tc.wantName {
				t.Errorf("Name = %q, want %q", got.Name, tc.wantName)
			}
			if got.CanonicalName() != tc.wantCanon {
				t.Errorf("CanonicalName() = %q, want %q", got.CanonicalName(), tc.wantCanon)
			}
			if got.Description != tc.wantDesc {
				t.Errorf("Description = %q, want %q", got.Description, tc.wantDesc)
			}
		})
	}
}

func TestParseCRLF(t *testing.T) {
	input := []byte("---\r\nname: foo\r\ndescription: bar\r\n---\r\nbody\r\n")
	got, err := Parse(input)
	if err != nil {
		t.Fatalf("Parse() err = %v", err)
	}
	if got.Name != "foo" || got.CanonicalName() != "foo" || got.Description != "bar" {
		t.Errorf("got %+v, want name=foo canonical=foo description=bar", got)
	}
}

func TestParseBodyIgnored(t *testing.T) {
	// The Markdown body may itself contain "---" lines (horizontal rules); once the frontmatter
	// closing fence is found, the body is opaque and must not be re-parsed.
	input := []byte("---\nname: foo\n---\n# heading\n---\nname: not-this\n")
	got, err := Parse(input)
	if err != nil {
		t.Fatalf("Parse() err = %v", err)
	}
	if got.Name != "foo" {
		t.Errorf("Name = %q, want %q (body must be opaque)", got.Name, "foo")
	}
}

func TestParseDoesNotMutateInput(t *testing.T) {
	input := []byte("---\nname: foo\n---\nbody")
	before := append([]byte(nil), input...)
	if _, err := Parse(input); err != nil {
		t.Fatalf("Parse() err = %v", err)
	}
	if !bytes.Equal(input, before) {
		t.Errorf("Parse mutated its input:\n got %q\nwant %q", input, before)
	}
}

func TestParseFailures(t *testing.T) {
	long200 := strings.Repeat("a", 200)
	long201 := strings.Repeat("a", 201)
	long4096 := strings.Repeat("d", 4096)
	long4097 := strings.Repeat("d", 4097)

	cases := []struct {
		name  string
		input []byte
		want  string
	}{
		{"no frontmatter at all", []byte("name: foo\n"), CodeNoFrontmatter},
		{"BOM before fence", []byte("\xef\xbb\xbf---\nname: foo\n---\n"), CodeNoFrontmatter},
		{"leading content", []byte("# title\n---\nname: foo\n---\n"), CodeNoFrontmatter},
		{"leading whitespace", []byte("\n---\nname: foo\n---\n"), CodeNoFrontmatter},
		{"missing closing fence", []byte("---\nname: foo\n"), CodeNoFrontmatter},
		{"invalid YAML", fm("name: [unclosed"), CodeInvalidYAML},
		{"sequence root", fm("- a\n- b"), CodeInvalidYAML},
		{"scalar root", fm("just a string"), CodeInvalidYAML},
		{"empty body", []byte("---\n---\nbody\n"), CodeInvalidYAML},
		{"missing name", fm("description: x"), CodeMissingName},
		{"name not top-level", fm("metadata:\n  name: x"), CodeMissingName},
		{"empty name string", fm("name: \"\""), CodeNameInvalidChars},
		{"whitespace name", fm("name: \"   \""), CodeNameInvalidChars},
		{"name with space", fm("name: \"foo bar\""), CodeNameInvalidChars},
		{"name with slash", fm("name: \"foo/bar\""), CodeNameInvalidChars},
		{"dot-prefixed name", fm("name: \".hidden\""), CodeNameInvalidChars},
		{"name too long", fm("name: \"" + long201 + "\""), CodeNameTooLong},
		{"name is int", fm("name: 123"), CodeNameNotString},
		{"name is bool", fm("name: true"), CodeNameNotString},
		{"name is null", fm("name: null"), CodeNameNotString},
		{"name is empty scalar", fm("name:"), CodeNameNotString},
		{"name is sequence", fm("name: [a]"), CodeNameNotString},
		{"name is mapping", fm("name: {a: b}"), CodeNameNotString},
		{"description is int", fm("name: foo\ndescription: 123"), CodeDescriptionInvalid},
		{"description is null", fm("name: foo\ndescription: null"), CodeDescriptionInvalid},
		{"description is empty scalar", fm("name: foo\ndescription:"), CodeDescriptionInvalid},
		{"description too long", fm("name: foo\ndescription: \"" + long4097 + "\""), CodeDescriptionInvalid},
		{"duplicate name key", fm("name: foo\nname: bar"), CodeDuplicateKey},
		{"duplicate description key", fm("name: foo\ndescription: a\ndescription: b"), CodeDuplicateKey},
		{"duplicate unknown key", fm("name: foo\nlicense: A\nlicense: B"), CodeDuplicateKey},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := Parse(tc.input)
			if got := CodeOf(err); got != tc.want {
				t.Fatalf("CodeOf(Parse()) = %q (err %v), want %q", got, err, tc.want)
			}
		})
	}

	// name at exactly the 200-byte bound is valid; 201 fails (covered above).
	t.Run("name exactly 200 bytes is valid", func(t *testing.T) {
		got, err := Parse(fm("name: \"" + long200 + "\""))
		if err != nil {
			t.Fatalf("Parse() err = %v", err)
		}
		if len(got.Name) != 200 || got.CanonicalName() != long200 {
			t.Errorf("got name length %d, want 200", len(got.Name))
		}
	})
	t.Run("description exactly 4096 bytes is valid", func(t *testing.T) {
		got, err := Parse(fm("name: foo\ndescription: \"" + long4096 + "\""))
		if err != nil {
			t.Fatalf("Parse() err = %v", err)
		}
		if len(got.Description) != 4096 {
			t.Errorf("description length = %d, want 4096", len(got.Description))
		}
	})
}

// TestParseCompatibilityFixtures pins the metadata contract against the Desktop and multica
// SKILL.md content audited in the metadata ADR: every name is already lowercase kebab-case, so
// canonical_name equals name with no slugify and no directory-name fallback.
func TestParseCompatibilityFixtures(t *testing.T) {
	names := []string{
		"multica-onboarding",
		"multica-platform",
		"multica-working-on-issues",
		"web-design-guidelines",
		"incident-response",
	}
	for _, n := range names {
		t.Run(n, func(t *testing.T) {
			got, err := Parse(fm("name: " + n + "\ndescription: audited content"))
			if err != nil {
				t.Fatalf("Parse() err = %v", err)
			}
			if got.CanonicalName() != n {
				t.Errorf("CanonicalName() = %q, want %q", got.CanonicalName(), n)
			}
		})
	}
}

// TestParseNoSlugify pins the ADR's rejection of multica's lossy slugify: "." and "_" survive
// canonicalization verbatim and never collapse into "-".
func TestParseNoSlugify(t *testing.T) {
	for name, want := range map[string]string{
		"My.Skill": "my.skill",
		"Foo_Bar":  "foo_bar",
		"a.b_c-d":  "a.b_c-d",
	} {
		got, err := Parse(fm("name: " + name))
		if err != nil {
			t.Fatalf("Parse(%q) err = %v", name, err)
		}
		if got.CanonicalName() != want {
			t.Errorf("CanonicalName(%q) = %q, want %q", name, got.CanonicalName(), want)
		}
	}
}

// TestParseErrorIsStable asserts the ParseError carries the exported stable code and that a
// non-ParseError yields "" from CodeOf.
func TestParseErrorIsStable(t *testing.T) {
	_, err := Parse(fm("name: 123"))
	if err == nil {
		t.Fatal("Parse() err = nil, want error")
	}
	pe, ok := err.(*ParseError)
	if !ok {
		t.Fatalf("err = %T, want *ParseError", err)
	}
	if pe.Code != CodeNameNotString {
		t.Errorf("Code = %q, want %q", pe.Code, CodeNameNotString)
	}
	if CodeOf(nil) != "" {
		t.Errorf("CodeOf(nil) = %q, want empty", CodeOf(nil))
	}
}
