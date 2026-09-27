package s3store

import (
	"context"
	"fmt"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	v4 "github.com/aws/aws-sdk-go-v2/aws/signer/v4"
	"github.com/aws/aws-sdk-go-v2/service/s3"

	"github.com/wanglongan587/cloud/internal/skillstore"
)

// presigner is the minimal S3 presign surface the issuer needs. *s3.PresignClient satisfies it;
// tests substitute a fake to assert the exact request and TTL without a live endpoint.
type presigner interface {
	PresignGetObject(ctx context.Context, params *s3.GetObjectInput, optFns ...func(*s3.PresignOptions)) (*v4.PresignedHTTPRequest, error)
}

// issuer adapts an S3 presign client to skillstore.RetrievalCapabilityIssuer (Step 5B ADR D28). It
// is the ephemeral-credential side of the S3 adapter, deliberately separate from the durable `store`:
// the store owns create-only writes and reads, the issuer mints short-lived GET-only signed URLs. It
// is constructed by cmd/server at wiring time and only Cloud (which holds the Object Storage
// credentials) builds one.
type issuer struct {
	presign presigner
	bucket  string
}

// NewIssuer validates cfg, resolves credentials from the deployment platform, builds the bounded
// client, and returns a RetrievalCapabilityIssuer. Like New, a nil return error does not mean the
// bucket is reachable — reachability is classified on first use, never probed at startup (ADR D14).
func NewIssuer(ctx context.Context, cfg Config) (skillstore.RetrievalCapabilityIssuer, error) {
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	client, err := newClient(ctx, cfg)
	if err != nil {
		return nil, err
	}
	return &issuer{presign: s3.NewPresignClient(client), bucket: cfg.Bucket}, nil
}

// Issue mints a GET-only, exact-object signed URL over locator, valid for ttl. It performs no
// existence probe (ADR D29): the URL is signed for the exact bucket+key and the eventual GET
// discovers absence. The returned URL is a bearer credential and is never logged or persisted by
// this package. ExpiresAt is a conservative lower bound on the URL's true validity — captured
// before the presign call, so it is never later than the actual signing expiry.
func (i *issuer) Issue(ctx context.Context, locator skillstore.Locator, ttl time.Duration) (skillstore.RetrievalCapability, error) {
	if ttl <= 0 {
		return skillstore.RetrievalCapability{}, fmt.Errorf("s3store: retrieval capability ttl must be positive")
	}
	now := time.Now()
	req, err := i.presign.PresignGetObject(ctx, &s3.GetObjectInput{
		Bucket: aws.String(i.bucket),
		Key:    aws.String(locator.String()),
	}, func(opts *s3.PresignOptions) {
		opts.Expires = ttl
	})
	if err != nil {
		return skillstore.RetrievalCapability{}, fmt.Errorf("s3store: presign get object: %w", err)
	}
	return skillstore.RetrievalCapability{
		URL:       req.URL,
		Method:    req.Method,
		ExpiresAt: now.Add(ttl),
	}, nil
}
