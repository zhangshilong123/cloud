package s3store

import (
	"context"
	"errors"
	"net/url"
	"strings"
	"testing"
	"time"

	v4 "github.com/aws/aws-sdk-go-v2/aws/signer/v4"
	"github.com/aws/aws-sdk-go-v2/service/s3"
)

// fakePresigner is a scriptable presigner double. A nil func falls back to a benign signed URL.
type fakePresigner struct {
	get func(ctx context.Context, in *s3.GetObjectInput, optFns ...func(*s3.PresignOptions)) (*v4.PresignedHTTPRequest, error)
}

func (f *fakePresigner) PresignGetObject(ctx context.Context, in *s3.GetObjectInput, optFns ...func(*s3.PresignOptions)) (*v4.PresignedHTTPRequest, error) {
	if f.get != nil {
		return f.get(ctx, in, optFns...)
	}
	return &v4.PresignedHTTPRequest{URL: "https://s3.example/bkt/key?X-Amz-Signature=abc", Method: "GET"}, nil
}

func testIssuer(bucket string, p presigner) *issuer {
	if bucket == "" {
		bucket = "bkt"
	}
	return &issuer{presign: p, bucket: bucket}
}

// TestIssueSignsExactObjectGET pins the D5/D26 least-privilege contract: Issue signs exactly the
// configured bucket + logical locator key, with the requested TTL, GET-only, and no existence probe
// (there is no HeadObject anywhere in the path).
func TestIssueSignsExactObjectGET(t *testing.T) {
	loc := testLocator(t)
	ttl := 300 * time.Second
	var gotIn *s3.GetObjectInput
	var gotExpires time.Duration
	a := &fakePresigner{get: func(_ context.Context, in *s3.GetObjectInput, optFns ...func(*s3.PresignOptions)) (*v4.PresignedHTTPRequest, error) {
		gotIn = in
		opts := s3.PresignOptions{}
		for _, fn := range optFns {
			fn(&opts)
		}
		gotExpires = opts.Expires
		return &v4.PresignedHTTPRequest{URL: "https://s3.example/bkt/" + loc.String() + "?X-Amz-Signature=abc", Method: "GET"}, nil
	}}
	iss := testIssuer("bkt", a)

	cap, err := iss.Issue(context.Background(), loc, ttl)
	if err != nil {
		t.Fatalf("Issue: %v", err)
	}
	if cap.Method != "GET" {
		t.Errorf("method = %q, want GET", cap.Method)
	}
	if gotIn == nil {
		t.Fatal("PresignGetObject was not called")
	}
	if gotIn.Bucket == nil || *gotIn.Bucket != "bkt" {
		t.Errorf("bucket = %v, want bkt", gotIn.Bucket)
	}
	if gotIn.Key == nil || *gotIn.Key != loc.String() {
		t.Errorf("key = %v, want %q", gotIn.Key, loc.String())
	}
	if gotExpires != ttl {
		t.Errorf("presign expires = %s, want %s", gotExpires, ttl)
	}
	if !strings.Contains(cap.URL, loc.String()) {
		t.Errorf("url %q must address the exact object key %q", cap.URL, loc.String())
	}
	// ExpiresAt is a conservative lower bound: roughly now+ttl, never before now.
	if cap.ExpiresAt.Before(time.Now().Add(ttl-2*time.Second)) || cap.ExpiresAt.After(time.Now().Add(ttl+2*time.Second)) {
		t.Errorf("expires_at = %s, want ~now+%s", cap.ExpiresAt, ttl)
	}
}

// TestIssueSigningError pins that a provider failure surfaces as an error (the caller classifies it
// as signing_failed), never as a fake URL.
func TestIssueSigningError(t *testing.T) {
	loc := testLocator(t)
	a := &fakePresigner{get: func(_ context.Context, _ *s3.GetObjectInput, _ ...func(*s3.PresignOptions)) (*v4.PresignedHTTPRequest, error) {
		return nil, errors.New("signature compute failed")
	}}
	iss := testIssuer("bkt", a)

	if _, err := iss.Issue(context.Background(), loc, 300*time.Second); err == nil {
		t.Fatal("Issue must propagate a signing error")
	}
}

// TestIssueRejectsNonPositiveTTL pins that a non-positive TTL is refused before any signing call.
func TestIssueRejectsNonPositiveTTL(t *testing.T) {
	called := false
	a := &fakePresigner{get: func(_ context.Context, _ *s3.GetObjectInput, _ ...func(*s3.PresignOptions)) (*v4.PresignedHTTPRequest, error) {
		called = true
		return &v4.PresignedHTTPRequest{URL: "https://s3.example/bkt/key", Method: "GET"}, nil
	}}
	iss := testIssuer("bkt", a)

	for _, ttl := range []time.Duration{0, -time.Second} {
		if _, err := iss.Issue(context.Background(), testLocator(t), ttl); err == nil {
			t.Fatalf("Issue(ttl=%s) = nil error, want error", ttl)
		}
	}
	if called {
		t.Fatal("Issue must not sign when TTL is non-positive")
	}
}

// TestIssueRealClientProducesSignedURL proves, through the real aws-sdk-go-v2 S3 presigner, that
// Issue produces a real SigV4 signed GET URL: X-Amz-Signature present, X-Amz-Expires equal to the
// TTL, and the exact object key in the path — the exact-object, short-lived, read-only contract.
func TestIssueRealClientProducesSignedURL(t *testing.T) {
	t.Setenv("AWS_ACCESS_KEY_ID", "test")
	t.Setenv("AWS_SECRET_ACCESS_KEY", "test")

	cfg := Config{
		Region: "us-east-1", Bucket: "bkt", Endpoint: "https://s3.example.com", PathStyle: true,
		CredentialMode: CredentialModeEnvironment,
		ConnectTimeout: 5 * time.Second, RequestTimeout: 5 * time.Second,
	}
	client, err := newClient(context.Background(), cfg)
	if err != nil {
		t.Fatalf("newClient: %v", err)
	}
	iss := &issuer{presign: s3.NewPresignClient(client), bucket: "bkt"}

	loc := testLocator(t)
	ttl := 300 * time.Second
	cap, err := iss.Issue(context.Background(), loc, ttl)
	if err != nil {
		t.Fatalf("Issue: %v", err)
	}
	if cap.Method != "GET" {
		t.Errorf("method = %q, want GET", cap.Method)
	}
	u, err := url.Parse(cap.URL)
	if err != nil {
		t.Fatalf("signed URL not parseable: %v", err)
	}
	if u.Query().Get("X-Amz-Signature") == "" {
		t.Errorf("signed URL must carry X-Amz-Signature, got %q", cap.URL)
	}
	if u.Query().Get("X-Amz-Expires") != "300" {
		t.Errorf("X-Amz-Expires = %q, want 300", u.Query().Get("X-Amz-Expires"))
	}
	if !strings.Contains(u.Path, loc.String()) {
		t.Errorf("signed URL path %q must contain the exact object key %q", u.Path, loc.String())
	}
	if cap.ExpiresAt.Before(time.Now().Add(ttl-2*time.Second)) || cap.ExpiresAt.After(time.Now().Add(ttl+2*time.Second)) {
		t.Errorf("expires_at = %s, want ~now+%s", cap.ExpiresAt, ttl)
	}
}
