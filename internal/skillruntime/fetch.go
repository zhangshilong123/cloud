package skillruntime

import (
	"context"
	"crypto/tls"
	"fmt"
	"io"
	"net"
	"net/http"
	"time"

	"github.com/wanglongan587/cloud/internal/skillstore"
)

// newDownloadClient builds a bounded HTTP client that clones http.DefaultTransport (never replaces it —
// AGENTS) and refuses to follow redirects: the signed URL is a bearer credential and must never be
// re-targeted by a hostile storage endpoint (plan §8). The returned client carries the connect timeout
// on its dialer; per-request end-to-end bounds are applied by the caller's context.
func newDownloadClient(connectTimeout time.Duration) (*http.Client, error) {
	base, ok := http.DefaultTransport.(*http.Transport)
	if !ok {
		return nil, fmt.Errorf("skillruntime: http.DefaultTransport is not *http.Transport")
	}
	transport := base.Clone()
	transport.DialContext = (&net.Dialer{
		Timeout:   connectTimeout,
		KeepAlive: 30 * time.Second,
	}).DialContext
	transport.TLSClientConfig = &tls.Config{MinVersion: tls.VersionTLS12}
	return &http.Client{
		Transport: transport,
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}, nil
}

// retrieve performs the external HTTPS GET for the exact object bytes, bounded by maxBytes. It honors
// capability.ExpiresAt with a strict pre-check, performs a GET only (the capability's Method is
// informational and always "GET" — ADR D26), and maps every outcome to a credential-free sentinel.
//
// The signed URL is a bearer credential: it is never persisted, logged, or embedded in any error.
// Errors from client.Do may echo the request URL (and therefore its signature query), so the raw
// transport error is deliberately discarded and only the safe classification is returned.
func (m *Materializer) retrieve(ctx context.Context, capability skillstore.RetrievalCapability, maxBytes uint64) ([]byte, error) {
	if capability.URL == "" {
		return nil, wrapMsg(ErrRetrievalUnauthorized, "empty retrieval URL")
	}
	if !capability.ExpiresAt.IsZero() && time.Now().After(capability.ExpiresAt) {
		return nil, wrapMsg(ErrRetrievalUnauthorized, "capability expired at %s", capability.ExpiresAt.Format(time.RFC3339Nano))
	}

	ctx, cancel := context.WithTimeout(ctx, m.cfg.RequestTimeout)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, capability.URL, nil)
	if err != nil {
		// NewRequest can only fail on an unparsable URL; do not echo the URL (it carries the signature).
		return nil, wrapMsg(ErrRetrievalTransient, "cannot build request")
	}

	resp, err := m.client.Do(req)
	if err != nil {
		// err may embed the signed URL and its signature query; return the safe classification only.
		return nil, ErrRetrievalTransient
	}
	defer resp.Body.Close()

	if err := classifyStatus(resp.StatusCode); err != nil {
		return nil, err
	}
	data, oversize, err := readBounded(resp.Body, maxBytes)
	if err != nil {
		return nil, ErrRetrievalTransient
	}
	if oversize {
		return nil, wrapMsg(ErrSizeMismatch, "object exceeds %d bytes", maxBytes)
	}
	return data, nil
}

// classifyStatus maps a response code to a retrieval sentinel. Only 200 is success; every other code
// fails closed into a credential-free class. 401/403 are unauthorized, 404 is not-found, 3xx means a
// redirect was refused, and 429/5xx are transient. Any unlisted code fails closed as transient so it is
// never mistaken for authoritative content.
func classifyStatus(code int) error {
	switch {
	case code == http.StatusOK:
		return nil
	case code == http.StatusUnauthorized || code == http.StatusForbidden:
		return wrapMsg(ErrRetrievalUnauthorized, "http status %d", code)
	case code == http.StatusNotFound:
		return wrapMsg(ErrRetrievalNotFound, "http status %d", code)
	case code >= 300 && code < 400:
		return wrapMsg(ErrRetrievalRedirect, "http status %d", code)
	case code == http.StatusTooManyRequests || code >= 500:
		return wrapMsg(ErrRetrievalTransient, "http status %d", code)
	default:
		return wrapMsg(ErrRetrievalTransient, "http status %d", code)
	}
}

// readBounded reads at most maxBytes from r and reports whether the stream continues past that bound,
// without ever reading unboundedly. maxBytes is bounded in practice by MaxPackageBytes (~1 GiB). It
// mirrors the s3store adapter's read boundary so both retrieval paths share the same oversize semantics.
func readBounded(r io.Reader, maxBytes uint64) (data []byte, oversize bool, err error) {
	const maxInt64 = int64(^uint64(0) >> 1)
	limit := int64(maxBytes) // #nosec G115 -- clamped to MaxInt64 below; maxBytes is bounded by package limits in practice
	if maxBytes > uint64(maxInt64) {
		limit = maxInt64
	}

	data, err = io.ReadAll(io.LimitReader(r, limit))
	if err != nil {
		return nil, false, err
	}
	if int64(len(data)) < limit {
		// The stream ended inside the bound: we hold the complete object.
		return data, false, nil
	}
	// We buffered exactly `limit` bytes; the object is oversize iff one more exists.
	var one [1]byte
	n, err := r.Read(one[:])
	if err != nil && err != io.EOF {
		return nil, false, err
	}
	return data, n > 0, nil
}
