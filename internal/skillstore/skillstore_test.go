package skillstore_test

import (
	"strings"
	"testing"

	"github.com/wanglongan587/cloud/internal/skillstore"
)

func TestPackageDigestHexIsLowercase(t *testing.T) {
	got := skillstore.PackageDigestHex([]byte("hello"))
	const want = "2cf24dba5fb0a30e26e83b2ac5b9e29e1b161e5c1fa7425e73043362938b9824"
	if got != want {
		t.Errorf("PackageDigestHex = %q, want %q", got, want)
	}
	if strings.ToLower(got) != got {
		t.Errorf("PackageDigestHex(%q) is not lowercase", got)
	}
}

// validPutRequest builds an internally consistent PutRequest over a fixed byte
// slice, so a test can mutate exactly one field and assert Validate rejects it.
func validPutRequest(t *testing.T) skillstore.PutRequest {
	t.Helper()
	body := []byte("package bytes")
	digest := skillstore.PackageDigestHex(body)
	loc, err := skillstore.NewLocator("ora-skill-package", 1, "sha256", digest)
	if err != nil {
		t.Fatalf("NewLocator: %v", err)
	}
	return skillstore.PutRequest{
		Locator:               loc,
		Bytes:                 body,
		ExpectedPackageDigest: digest,
		ExpectedPackageSize:   uint64(len(body)),
		ExpectedContentDigest: strings.Repeat("0", 64),
		PackageFormat:         "ora-skill-package",
		PackageFormatVersion:  1,
		DigestAlgorithm:       "sha256",
	}
}

func TestPutRequestValidate(t *testing.T) {
	t.Run("consistent request passes", func(t *testing.T) {
		req := validPutRequest(t)
		if err := req.Validate(); err != nil {
			t.Fatalf("Validate: %v", err)
		}
	})

	t.Run("size mismatch", func(t *testing.T) {
		req := validPutRequest(t)
		req.ExpectedPackageSize++
		if err := req.Validate(); err == nil {
			t.Fatal("Validate succeeded, want size error")
		}
	})

	t.Run("package digest mismatch", func(t *testing.T) {
		req := validPutRequest(t)
		req.ExpectedPackageDigest = strings.Repeat("a", 64)
		if err := req.Validate(); err == nil {
			t.Fatal("Validate succeeded, want digest error")
		}
	})

	t.Run("locator digest mismatch", func(t *testing.T) {
		req := validPutRequest(t)
		loc, err := skillstore.NewLocator("ora-skill-package", 1, "sha256", strings.Repeat("b", 64))
		if err != nil {
			t.Fatalf("NewLocator: %v", err)
		}
		req.Locator = loc
		if err := req.Validate(); err == nil {
			t.Fatal("Validate succeeded, want locator digest error")
		}
	})

	t.Run("format mismatch", func(t *testing.T) {
		req := validPutRequest(t)
		req.PackageFormat = "zip"
		if err := req.Validate(); err == nil {
			t.Fatal("Validate succeeded, want format error")
		}
	})

	t.Run("version mismatch", func(t *testing.T) {
		req := validPutRequest(t)
		req.PackageFormatVersion = 2
		if err := req.Validate(); err == nil {
			t.Fatal("Validate succeeded, want version error")
		}
	})

	t.Run("algorithm mismatch", func(t *testing.T) {
		req := validPutRequest(t)
		req.DigestAlgorithm = "md5"
		if err := req.Validate(); err == nil {
			t.Fatal("Validate succeeded, want algorithm error")
		}
	})
}
