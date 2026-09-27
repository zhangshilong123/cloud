package skillstore

import (
	"strings"
	"testing"
	"time"
)

// TestRetrievalCapabilityStringRedacted pins ADR D7 "Redaction": the debug/log String form must
// never contain the bearer URL, its query, or any signature material — only the non-secret method and
// expiry. The URL field exists for delivery, but formatting it must not leak it.
func TestRetrievalCapabilityStringRedacted(t *testing.T) {
	const secret = "https://s3.example/bkt/skills/ora-skill-package/v1/sha256/abcd?X-Amz-Signature=deadbeef&X-Amz-Credential=AKIA"
	c := RetrievalCapability{
		URL:       secret,
		Method:    "GET",
		ExpiresAt: time.Now().Add(300 * time.Second),
	}

	s := c.String()
	for _, forbidden := range []string{
		secret,
		"X-Amz-Signature",
		"deadbeef",
		"X-Amz-Credential",
		"AKIA",
		"s3.example",
	} {
		if strings.Contains(s, forbidden) {
			t.Fatalf("String() leaked credential material %q in %q", forbidden, s)
		}
	}
	if !strings.Contains(s, "GET") {
		t.Errorf("String() must report the non-secret method, got %q", s)
	}
	if !strings.Contains(s, "expires_at") {
		t.Errorf("String() must report the non-secret expiry, got %q", s)
	}
}

// TestRetrievalCapabilityStringIsSafeForErrorWrapping guards the habit of embedding a capability in
// an error via %v: since String() is redacted, the wrapped error cannot leak the URL either.
func TestRetrievalCapabilityStringIsSafeForErrorWrapping(t *testing.T) {
	c := RetrievalCapability{URL: "https://s3.example/bkt/k?X-Amz-Signature=secret", Method: "GET", ExpiresAt: time.Now()}
	if strings.Contains(c.String(), "secret") || strings.Contains(c.String(), "s3.example") {
		t.Fatalf("String() leaked URL material: %q", c.String())
	}
}
