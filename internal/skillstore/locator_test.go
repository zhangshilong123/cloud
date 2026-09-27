package skillstore_test

import (
	"strings"
	"testing"

	"github.com/wanglongan587/cloud/internal/skillstore"
)

// validDigest is a well-formed lowercase 64-hex digest used where only the shape
// matters, not the content.
const validDigest = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"

func TestNewLocatorCanonicalKey(t *testing.T) {
	loc, err := skillstore.NewLocator("ora-skill-package", 1, "sha256", validDigest)
	if err != nil {
		t.Fatalf("NewLocator: %v", err)
	}
	want := "skills/ora-skill-package/v1/sha256/" + validDigest
	if got := loc.String(); got != want {
		t.Errorf("String() = %q, want %q", got, want)
	}
	if got := len(loc.String()); got != 99 {
		t.Errorf("v1 locator length = %d, want 99", got)
	}
	if got := len(loc.String()); got >= 1024 {
		t.Errorf("v1 locator length %d exceeds the 1024 schema bound", got)
	}
}

func TestNewLocatorRejects(t *testing.T) {
	cases := []struct {
		name      string
		format    string
		version   int
		algorithm string
		digest    string
	}{
		{"unknown format", "zip", 1, "sha256", validDigest},
		{"zero version", "ora-skill-package", 0, "sha256", validDigest},
		{"negative version", "ora-skill-package", -1, "sha256", validDigest},
		{"unknown algorithm", "ora-skill-package", 1, "md5", validDigest},
		{"uppercase digest", "ora-skill-package", 1, "sha256", strings.ToUpper(validDigest)},
		{"short digest", "ora-skill-package", 1, "sha256", "abc"},
		{"non-hex digest", "ora-skill-package", 1, "sha256", strings.Repeat("g", 64)},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := skillstore.NewLocator(tc.format, tc.version, tc.algorithm, tc.digest); err == nil {
				t.Fatalf("NewLocator(%q, %d, %q, %q) succeeded, want error", tc.format, tc.version, tc.algorithm, tc.digest)
			}
		})
	}
}

func TestParseLocatorRoundTrip(t *testing.T) {
	loc, err := skillstore.NewLocator("ora-skill-package", 1, "sha256", validDigest)
	if err != nil {
		t.Fatalf("NewLocator: %v", err)
	}
	got, err := skillstore.ParseLocator(loc.String())
	if err != nil {
		t.Fatalf("ParseLocator(%q): %v", loc.String(), err)
	}
	if got != loc {
		t.Errorf("ParseLocator round trip = %q, want %q", got.String(), loc.String())
	}
}

func TestParseLocatorRejects(t *testing.T) {
	cases := []struct{ name, in string }{
		{"empty", ""},
		{"wrong prefix", "files/ora-skill-package/v1/sha256/" + validDigest},
		{"too few segments", "skills/ora-skill-package/v1/" + validDigest},
		{"too many segments", "skills/ora-skill-package/v1/sha256/" + validDigest + "/extra"},
		{"leading slash", "/skills/ora-skill-package/v1/sha256/" + validDigest},
		{"trailing slash", "skills/ora-skill-package/v1/sha256/" + validDigest + "/"},
		{"empty format", "skills//v1/sha256/" + validDigest},
		{"unknown format", "skills/zip/v1/sha256/" + validDigest},
		{"version zero", "skills/ora-skill-package/v0/sha256/" + validDigest},
		{"version missing", "skills/ora-skill-package/sha256/" + validDigest},
		{"unknown algorithm", "skills/ora-skill-package/v1/md5/" + validDigest},
		{"bad digest", "skills/ora-skill-package/v1/sha256/nothex"},
		{"traversal", "skills/../v1/sha256/" + validDigest},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := skillstore.ParseLocator(tc.in); err == nil {
				t.Fatalf("ParseLocator(%q) succeeded, want error", tc.in)
			}
		})
	}
}

func TestLocatorIsContentDerived(t *testing.T) {
	a := []byte("package content A")
	b := []byte("package content B")

	da := skillstore.PackageDigestHex(a)
	db := skillstore.PackageDigestHex(b)
	if da == db {
		t.Fatalf("distinct byte slices produced equal digests")
	}

	la, err := skillstore.NewLocator("ora-skill-package", 1, "sha256", da)
	if err != nil {
		t.Fatalf("NewLocator: %v", err)
	}
	lb, err := skillstore.NewLocator("ora-skill-package", 1, "sha256", db)
	if err != nil {
		t.Fatalf("NewLocator: %v", err)
	}
	if la.String() == lb.String() {
		t.Fatalf("distinct digests produced equal locators")
	}

	// Identical content must always yield the identical locator.
	la2, err := skillstore.NewLocator("ora-skill-package", 1, "sha256", skillstore.PackageDigestHex(a))
	if err != nil {
		t.Fatalf("NewLocator: %v", err)
	}
	if la2.String() != la.String() {
		t.Errorf("same content produced different locators: %q vs %q", la2.String(), la.String())
	}
}

func TestLocatorComponentsAreContentOnly(t *testing.T) {
	loc, err := skillstore.NewLocator("ora-skill-package", 1, "sha256", validDigest)
	if err != nil {
		t.Fatalf("NewLocator: %v", err)
	}
	if loc.Format() != "ora-skill-package" || loc.Version() != 1 || loc.Algorithm() != "sha256" || loc.DigestHex() != validDigest {
		t.Errorf("unexpected locator components: format=%q version=%d algorithm=%q digest=%q",
			loc.Format(), loc.Version(), loc.Algorithm(), loc.DigestHex())
	}
	if got := loc.String(); got != "skills/ora-skill-package/v1/sha256/"+validDigest {
		t.Errorf("locator = %q, want canonical content-only form", got)
	}
}
