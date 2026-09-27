// Package s3store implements the production skillstore.ObjectStore adapter over a
// single S3-compatible endpoint using aws-sdk-go-v2/service/s3, frozen in
// specs/decisions/cloud/skills/20260927-production-object-storage-provider.md.
//
// It is the sole production provider (ADR D17): create-only writes use the
// provider's native conditional PUT (If-None-Match: *), so there is no
// HEAD-then-PUT path and no multipart upload. It offers only PutImmutable/Stat/Get
// — no List/Delete/Presign — resolves credentials strictly from the deployment
// platform, and never persists or logs them.
package s3store

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"

	"github.com/wanglongan587/cloud/internal/skillstore"
)

// api is the minimal S3 surface the adapter needs. *s3.Client satisfies it; tests
// substitute a fake to assert the exact requests and error mapping without a live
// endpoint.
type api interface {
	PutObject(ctx context.Context, params *s3.PutObjectInput, optFns ...func(*s3.Options)) (*s3.PutObjectOutput, error)
	HeadObject(ctx context.Context, params *s3.HeadObjectInput, optFns ...func(*s3.Options)) (*s3.HeadObjectOutput, error)
	GetObject(ctx context.Context, params *s3.GetObjectInput, optFns ...func(*s3.Options)) (*s3.GetObjectOutput, error)
}

// store adapts a bounded S3 client to skillstore.ObjectStore. It is constructed by
// cmd/server at wiring time, never by the domain layer.
type store struct {
	client api
	bucket string
	// requestTimeout bounds each operation end-to-end; the caller's deadline still
	// wins because context.WithTimeout keeps the earlier deadline.
	requestTimeout time.Duration
}

// New validates cfg, resolves credentials from the deployment platform, builds a
// bounded HTTP client, and returns a skillstore.ObjectStore. A nil return error
// does not mean the bucket is reachable — reachability is classified on first use,
// never probed at startup (ADR D14).
func New(ctx context.Context, cfg Config) (skillstore.ObjectStore, error) {
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	client, err := newClient(ctx, cfg)
	if err != nil {
		return nil, err
	}
	return newStore(cfg, client), nil
}

func newStore(cfg Config, client api) *store {
	return &store{client: client, bucket: cfg.Bucket, requestTimeout: cfg.RequestTimeout}
}

// PutImmutable performs the single conditional PUT (If-None-Match: *). There is no
// HEAD-then-PUT: the provider's native create-only semantics make the write atomic
// and provable (ADR D3). A 412 becomes PutAlreadyExists; every other error is
// classified per D6/D7. The caller guarantees a validated request.
func (s *store) PutImmutable(ctx context.Context, req *skillstore.PutRequest) skillstore.PutResult {
	if req == nil {
		return skillstore.PutResult{Outcome: skillstore.PutDefiniteFailurePermanent}
	}
	ctx, cancel := context.WithTimeout(ctx, s.requestTimeout)
	defer cancel()

	_, err := s.client.PutObject(ctx, &s3.PutObjectInput{
		Bucket:      aws.String(s.bucket),
		Key:         aws.String(req.Locator.String()),
		Body:        bytes.NewReader(req.Bytes),
		IfNoneMatch: aws.String("*"),
	})
	if err != nil {
		return skillstore.PutResult{Outcome: putOutcome(classify(err))}
	}
	return skillstore.PutResult{Outcome: skillstore.PutCreated}
}

// Stat reports existence only via HEAD. Evidence is diagnostic and never identity.
func (s *store) Stat(ctx context.Context, locator skillstore.Locator) skillstore.StatResult {
	ctx, cancel := context.WithTimeout(ctx, s.requestTimeout)
	defer cancel()

	out, err := s.client.HeadObject(ctx, &s3.HeadObjectInput{
		Bucket: aws.String(s.bucket),
		Key:    aws.String(locator.String()),
	})
	if err != nil {
		return skillstore.StatResult{Outcome: statOutcome(classify(err))}
	}

	ev := skillstore.Evidence{Size: -1}
	if out.ContentLength != nil {
		ev.Size = *out.ContentLength
	}
	if out.ETag != nil {
		ev.ETag = *out.ETag
	}
	return skillstore.StatResult{Outcome: skillstore.StatPresent, Evidence: ev}
}

// Get returns the exact object bytes, bounded by maxBytes. Reading past maxBytes
// is detected without buffering the whole object (ADR D8).
func (s *store) Get(ctx context.Context, locator skillstore.Locator, maxBytes uint64) skillstore.GetResult {
	ctx, cancel := context.WithTimeout(ctx, s.requestTimeout)
	defer cancel()

	out, err := s.client.GetObject(ctx, &s3.GetObjectInput{
		Bucket: aws.String(s.bucket),
		Key:    aws.String(locator.String()),
	})
	if err != nil {
		return skillstore.GetResult{Outcome: getOutcome(classify(err))}
	}
	defer out.Body.Close()

	data, oversize, err := readBounded(out.Body, maxBytes)
	if err != nil {
		return skillstore.GetResult{Outcome: skillstore.GetIndeterminate}
	}
	if oversize {
		return skillstore.GetResult{Outcome: skillstore.GetOversize}
	}
	return skillstore.GetResult{Outcome: skillstore.GetPresent, Bytes: data}
}

// readBounded reads at most maxBytes from r and reports whether the stream
// continues past that bound, without ever reading unboundedly. maxBytes is bounded
// in practice by the package limits (~1 GiB), so the +1 byte peek below never
// overflows; the MaxInt64 clamp only guards a caller passing a pathological value.
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
	var n int
	n, err = r.Read(one[:])
	if err != nil && err != io.EOF {
		return nil, false, err
	}
	return data, n > 0, nil
}

// newClient builds a bounded S3 client without mutating any global default
// (http.DefaultTransport is cloned, never replaced — ADR/AGENTS).
func newClient(ctx context.Context, cfg Config) (*s3.Client, error) {
	creds, err := resolveCredentials(ctx, cfg)
	if err != nil {
		return nil, err
	}
	httpClient, err := newHTTPClient(cfg)
	if err != nil {
		return nil, err
	}

	opts := s3.Options{
		Region:           cfg.Region,
		Credentials:      creds,
		HTTPClient:       httpClient,
		RetryMaxAttempts: 3, // bounded transport retry only (ADR D7); business retry belongs to the saga
	}
	if cfg.Endpoint != "" {
		opts.BaseEndpoint = aws.String(cfg.Endpoint)
		opts.UsePathStyle = cfg.PathStyle
	}
	return s3.New(opts), nil
}

// newHTTPClient returns a client whose transport clones http.DefaultTransport with
// a bounded connect timeout and a TLS config (verify / custom CA). It never sets
// http.Client.Timeout: each operation carries its own context deadline, which is
// the correct and caller-respecting bound.
func newHTTPClient(cfg Config) (*http.Client, error) {
	base, ok := http.DefaultTransport.(*http.Transport)
	if !ok {
		return nil, fmt.Errorf("s3store: http.DefaultTransport is not *http.Transport")
	}
	transport := base.Clone()
	transport.DialContext = (&net.Dialer{
		Timeout:   cfg.ConnectTimeout,
		KeepAlive: 30 * time.Second,
	}).DialContext
	transport.TLSClientConfig = &tls.Config{
		MinVersion:         tls.VersionTLS12,
		InsecureSkipVerify: cfg.InsecureSkipVerify, // resolved from storage.tls.verify, never a hard-coded default
	}

	if cfg.CAFile != "" {
		pem, err := os.ReadFile(cfg.CAFile)
		if err != nil {
			return nil, fmt.Errorf("s3store: read tls ca_file: %w", err)
		}
		pool := x509.NewCertPool()
		if !pool.AppendCertsFromPEM(pem) {
			return nil, fmt.Errorf("s3store: tls ca_file %q contains no usable certificates", cfg.CAFile)
		}
		transport.TLSClientConfig.RootCAs = pool
	}
	return &http.Client{Transport: transport}, nil
}

// resolveCredentials builds the credential provider for the declared mode. It
// never persists or logs credentials (ADR D10/D23).
func resolveCredentials(ctx context.Context, cfg Config) (aws.CredentialsProvider, error) {
	switch cfg.CredentialMode {
	case "", CredentialModeEnvironment:
		return credentials.NewStaticCredentialsProvider(
			os.Getenv("AWS_ACCESS_KEY_ID"),
			os.Getenv("AWS_SECRET_ACCESS_KEY"),
			os.Getenv("AWS_SESSION_TOKEN"),
		), nil

	case CredentialModeSharedCredentialsFile:
		awsCfg, err := awsconfig.LoadDefaultConfig(ctx,
			awsconfig.WithRegion(cfg.Region),
			awsconfig.WithSharedCredentialsFiles([]string{cfg.CredentialsFile}),
		)
		if err != nil {
			return nil, fmt.Errorf("s3store: load shared credentials file: %w", err)
		}
		return awsCfg.Credentials, nil

	case CredentialModeWorkloadIdentity:
		awsCfg, err := awsconfig.LoadDefaultConfig(ctx, awsconfig.WithRegion(cfg.Region))
		if err != nil {
			return nil, fmt.Errorf("s3store: load workload identity credentials: %w", err)
		}
		return awsCfg.Credentials, nil

	default:
		return nil, fmt.Errorf("s3store: unknown credential_mode %q", cfg.CredentialMode)
	}
}
