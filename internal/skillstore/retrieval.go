package skillstore

import (
	"context"
	"fmt"
	"time"
)

// RetrievalCapability is a short-lived, GET-only, exact-object retrieval credential (Step 5B ADR
// D3/D5), minted for a single logical Locator. It is a bearer credential (D4): it is transient
// delivery state, never persisted, never used as a cache key or recovery identity. Method is always
// "GET" (D26).
type RetrievalCapability struct {
	// URL is the short-lived signed URL. It is a bearer credential and must never be logged,
	// persisted, or embedded in any error, trace, or dump (D6/D7).
	URL string
	// Method is the HTTP method the URL is bound to; always "GET" (D26).
	Method string
	// ExpiresAt is the authoritative expiry. It is never later than the URL's true signing validity,
	// so a consumer that honors ExpiresAt never uses an already-expired URL (D24).
	ExpiresAt time.Time
}

// String returns a redacted description suitable for logs and debug formatting: it reports the
// method and expiry but never the URL, query, signature, or any other credential material (ADR D7
// "Redaction"). Do not add the URL here.
func (c RetrievalCapability) String() string {
	return fmt.Sprintf("RetrievalCapability{method=%q expires_at=%s}", c.Method, c.ExpiresAt.Format(time.RFC3339Nano))
}

// RetrievalCapabilityIssuer mints a short-lived, GET-only, exact-object retrieval capability for a
// single logical Locator (Step 5B ADR D28). It is a deliberately separate port from ObjectStore:
// ObjectStore owns the durable put/stat/get side, the issuer owns ephemeral credential issuance.
// It performs no existence probe (D29): the capability is minted and the eventual GET discovers
// absence.
type RetrievalCapabilityIssuer interface {
	// Issue returns a fresh capability over locator, valid for ttl. A non-nil error means the
	// provider could not sign; the caller classifies it as signing_failed (D32). It must not persist
	// the capability, and must not log the URL it returns.
	Issue(ctx context.Context, locator Locator, ttl time.Duration) (RetrievalCapability, error)
}
