package s3store

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	smithyhttp "github.com/aws/smithy-go/transport/http"

	"github.com/wanglongan587/cloud/internal/skillpkg"
	"github.com/wanglongan587/cloud/internal/skillstore"
)

// fakeAPI is a scriptable api double for unit tests, mirroring the seam the
// production adapter depends on. A nil func falls back to a benign success.
type fakeAPI struct {
	put  func(ctx context.Context, in *s3.PutObjectInput, optFns ...func(*s3.Options)) (*s3.PutObjectOutput, error)
	head func(ctx context.Context, in *s3.HeadObjectInput, optFns ...func(*s3.Options)) (*s3.HeadObjectOutput, error)
	get  func(ctx context.Context, in *s3.GetObjectInput, optFns ...func(*s3.Options)) (*s3.GetObjectOutput, error)
}

func (f *fakeAPI) PutObject(ctx context.Context, in *s3.PutObjectInput, optFns ...func(*s3.Options)) (*s3.PutObjectOutput, error) {
	if f.put != nil {
		return f.put(ctx, in, optFns...)
	}
	return &s3.PutObjectOutput{}, nil
}

func (f *fakeAPI) HeadObject(ctx context.Context, in *s3.HeadObjectInput, optFns ...func(*s3.Options)) (*s3.HeadObjectOutput, error) {
	if f.head != nil {
		return f.head(ctx, in, optFns...)
	}
	return &s3.HeadObjectOutput{}, nil
}

func (f *fakeAPI) GetObject(ctx context.Context, in *s3.GetObjectInput, optFns ...func(*s3.Options)) (*s3.GetObjectOutput, error) {
	if f.get != nil {
		return f.get(ctx, in, optFns...)
	}
	return &s3.GetObjectOutput{}, nil
}

func testStore(cfg Config, a api) *store {
	if cfg.Bucket == "" {
		cfg.Bucket = "bkt"
	}
	if cfg.RequestTimeout == 0 {
		cfg.RequestTimeout = time.Second
	}
	return newStore(cfg, a)
}

func testLocator(t *testing.T) skillstore.Locator {
	t.Helper()
	digest := skillstore.PackageDigestHex([]byte("payload"))
	loc, err := skillstore.NewLocator(skillpkg.FormatName, skillpkg.FormatVersion, skillstore.AlgorithmSHA256, digest)
	if err != nil {
		t.Fatalf("NewLocator: %v", err)
	}
	return loc
}

func httpError(status int) error {
	return &smithyhttp.ResponseError{Response: &smithyhttp.Response{Response: &http.Response{StatusCode: status}}}
}

func TestClassify(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want class
	}{
		{"404", httpError(404), classNotFound},
		{"412", httpError(412), classAlreadyExists},
		{"429", httpError(429), classTemporary},
		{"400", httpError(400), classPermanent},
		{"401", httpError(401), classPermanent},
		{"403", httpError(403), classPermanent},
		{"500", httpError(500), classAmbiguous},
		{"503", httpError(503), classAmbiguous},
		{"dns", &net.DNSError{Name: "x", Err: "no such host"}, classTemporary},
		{"dial refused", &net.OpError{Op: "dial", Err: errors.New("refused")}, classTemporary},
		{"read broken", &net.OpError{Op: "read", Err: errors.New("reset")}, classAmbiguous},
		{"deadline", context.DeadlineExceeded, classAmbiguous},
		{"canceled", context.Canceled, classAmbiguous},
		{"unknown", errors.New("something else"), classAmbiguous},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := classify(c.err); got != c.want {
				t.Fatalf("classify(%v) = %d, want %d", c.err, got, c.want)
			}
		})
	}
}

func TestPutImmutableSendsIfNoneMatchAndBody(t *testing.T) {
	loc := testLocator(t)
	payload := []byte("the exact package bytes")
	var got *s3.PutObjectInput
	a := &fakeAPI{put: func(_ context.Context, in *s3.PutObjectInput, _ ...func(*s3.Options)) (*s3.PutObjectOutput, error) {
		got = in
		return &s3.PutObjectOutput{}, nil
	}}
	st := testStore(Config{}, a)

	res := st.PutImmutable(context.Background(), &skillstore.PutRequest{Locator: loc, Bytes: payload})
	if res.Outcome != skillstore.PutCreated {
		t.Fatalf("outcome = %v, want PutCreated", res.Outcome)
	}
	if got == nil {
		t.Fatal("PutObject was not called")
	}
	if aws.ToString(got.Bucket) != "bkt" {
		t.Errorf("bucket = %q, want bkt", aws.ToString(got.Bucket))
	}
	if aws.ToString(got.Key) != loc.String() {
		t.Errorf("key = %q, want %q", aws.ToString(got.Key), loc.String())
	}
	if aws.ToString(got.IfNoneMatch) != "*" {
		t.Errorf("IfNoneMatch = %q, want *", aws.ToString(got.IfNoneMatch))
	}
	body, err := io.ReadAll(got.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	if !bytes.Equal(body, payload) {
		t.Errorf("body = %q, want %q", body, payload)
	}
}

func TestPutImmutableOutcomes(t *testing.T) {
	loc := testLocator(t)
	cases := []struct {
		name string
		err  error
		want skillstore.PutOutcome
	}{
		{"already exists", httpError(412), skillstore.PutAlreadyExists},
		{"transient", &net.DNSError{Name: "x"}, skillstore.PutDefiniteFailureTransient},
		{"permanent", httpError(403), skillstore.PutDefiniteFailurePermanent},
		{"ambiguous", httpError(500), skillstore.PutAmbiguous},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			a := &fakeAPI{put: func(_ context.Context, _ *s3.PutObjectInput, _ ...func(*s3.Options)) (*s3.PutObjectOutput, error) {
				return nil, c.err
			}}
			st := testStore(Config{}, a)
			res := st.PutImmutable(context.Background(), &skillstore.PutRequest{Locator: loc, Bytes: []byte("x")})
			if res.Outcome != c.want {
				t.Fatalf("outcome = %v, want %v", res.Outcome, c.want)
			}
		})
	}
}

func TestPutImmutableNilRequest(t *testing.T) {
	st := testStore(Config{}, &fakeAPI{})
	if res := st.PutImmutable(context.Background(), nil); res.Outcome != skillstore.PutDefiniteFailurePermanent {
		t.Fatalf("nil request outcome = %v, want permanent", res.Outcome)
	}
}

func TestStat(t *testing.T) {
	loc := testLocator(t)
	size := int64(1234)
	etag := "etag-1"

	t.Run("present", func(t *testing.T) {
		a := &fakeAPI{head: func(_ context.Context, _ *s3.HeadObjectInput, _ ...func(*s3.Options)) (*s3.HeadObjectOutput, error) {
			return &s3.HeadObjectOutput{ContentLength: &size, ETag: &etag}, nil
		}}
		st := testStore(Config{}, a)
		res := st.Stat(context.Background(), loc)
		if res.Outcome != skillstore.StatPresent {
			t.Fatalf("outcome = %v, want StatPresent", res.Outcome)
		}
		if res.Evidence.Size != size || res.Evidence.ETag != etag {
			t.Errorf("evidence = %+v, want size=%d etag=%q", res.Evidence, size, etag)
		}
	})

	t.Run("absent", func(t *testing.T) {
		a := &fakeAPI{head: func(_ context.Context, _ *s3.HeadObjectInput, _ ...func(*s3.Options)) (*s3.HeadObjectOutput, error) {
			return nil, httpError(404)
		}}
		st := testStore(Config{}, a)
		if res := st.Stat(context.Background(), loc); res.Outcome != skillstore.StatAbsent {
			t.Fatalf("outcome = %v, want StatAbsent", res.Outcome)
		}
	})

	t.Run("indeterminate", func(t *testing.T) {
		a := &fakeAPI{head: func(_ context.Context, _ *s3.HeadObjectInput, _ ...func(*s3.Options)) (*s3.HeadObjectOutput, error) {
			return nil, httpError(503)
		}}
		st := testStore(Config{}, a)
		if res := st.Stat(context.Background(), loc); res.Outcome != skillstore.StatIndeterminate {
			t.Fatalf("outcome = %v, want StatIndeterminate", res.Outcome)
		}
	})
}

func TestGet(t *testing.T) {
	loc := testLocator(t)

	t.Run("present", func(t *testing.T) {
		data := []byte("hello world")
		a := &fakeAPI{get: func(_ context.Context, _ *s3.GetObjectInput, _ ...func(*s3.Options)) (*s3.GetObjectOutput, error) {
			return &s3.GetObjectOutput{Body: io.NopCloser(bytes.NewReader(data))}, nil
		}}
		st := testStore(Config{}, a)
		res := st.Get(context.Background(), loc, 100)
		if res.Outcome != skillstore.GetPresent {
			t.Fatalf("outcome = %v, want GetPresent", res.Outcome)
		}
		if !bytes.Equal(res.Bytes, data) {
			t.Errorf("bytes = %q, want %q", res.Bytes, data)
		}
	})

	t.Run("oversize", func(t *testing.T) {
		data := []byte("hello world") // 11 bytes
		a := &fakeAPI{get: func(_ context.Context, _ *s3.GetObjectInput, _ ...func(*s3.Options)) (*s3.GetObjectOutput, error) {
			return &s3.GetObjectOutput{Body: io.NopCloser(bytes.NewReader(data))}, nil
		}}
		st := testStore(Config{}, a)
		if res := st.Get(context.Background(), loc, 5); res.Outcome != skillstore.GetOversize {
			t.Fatalf("outcome = %v, want GetOversize", res.Outcome)
		}
	})

	t.Run("exactly max", func(t *testing.T) {
		data := []byte("hello world") // 11 bytes
		a := &fakeAPI{get: func(_ context.Context, _ *s3.GetObjectInput, _ ...func(*s3.Options)) (*s3.GetObjectOutput, error) {
			return &s3.GetObjectOutput{Body: io.NopCloser(bytes.NewReader(data))}, nil
		}}
		st := testStore(Config{}, a)
		if res := st.Get(context.Background(), loc, 11); res.Outcome != skillstore.GetPresent {
			t.Fatalf("outcome = %v, want GetPresent", res.Outcome)
		}
	})

	t.Run("absent", func(t *testing.T) {
		a := &fakeAPI{get: func(_ context.Context, _ *s3.GetObjectInput, _ ...func(*s3.Options)) (*s3.GetObjectOutput, error) {
			return nil, httpError(404)
		}}
		st := testStore(Config{}, a)
		if res := st.Get(context.Background(), loc, 100); res.Outcome != skillstore.GetAbsent {
			t.Fatalf("outcome = %v, want GetAbsent", res.Outcome)
		}
	})
}

func TestNewValidatesConfig(t *testing.T) {
	invalid := []Config{
		{},
		{Region: "us-east-1"},
		{Region: "us-east-1", Bucket: "bkt", CredentialMode: "bogus"},
		{Region: "us-east-1", Bucket: "bkt", CredentialMode: CredentialModeSharedCredentialsFile},
		{Region: "us-east-1", Bucket: "bkt", Endpoint: "://bad"},
		{Region: "us-east-1", Bucket: "bkt", ConnectTimeout: time.Second},
	}
	for i, cfg := range invalid {
		if _, err := New(context.Background(), cfg); err == nil {
			t.Errorf("case %d: New(%+v) = nil error, want error", i, cfg)
		}
	}

	valid := Config{
		Region: "us-east-1", Bucket: "bkt", CredentialMode: CredentialModeEnvironment,
		ConnectTimeout: time.Second, RequestTimeout: time.Second,
	}
	if st, err := New(context.Background(), valid); err != nil || st == nil {
		t.Fatalf("New(valid) = (%v, %v), want non-nil store and nil error", st, err)
	}
}

// TestPutImmutableRealClientIfNoneMatch proves, through the real aws-sdk-go-v2 S3
// client against an httptest endpoint, that PutImmutable issues exactly one
// conditional PUT with If-None-Match: * and maps the 412 response to
// PutAlreadyExists — the create-only contract of ADR D3.
func TestPutImmutableRealClientIfNoneMatch(t *testing.T) {
	var gotIfNoneMatch string
	var calls int
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		gotIfNoneMatch = r.Header.Get("If-None-Match")
		w.Header().Set("Content-Type", "application/xml")
		w.WriteHeader(http.StatusPreconditionFailed)
		fmt.Fprint(w, `<Error><Code>PreconditionFailed</Code><Message>precondition failed</Message></Error>`)
	}))
	defer ts.Close()

	t.Setenv("AWS_ACCESS_KEY_ID", "test")
	t.Setenv("AWS_SECRET_ACCESS_KEY", "test")

	cfg := Config{
		Region: "us-east-1", Bucket: "bkt", Endpoint: ts.URL, PathStyle: true,
		CredentialMode: CredentialModeEnvironment,
		ConnectTimeout: 5 * time.Second, RequestTimeout: 5 * time.Second,
	}
	client, err := newClient(context.Background(), cfg)
	if err != nil {
		t.Fatalf("newClient: %v", err)
	}
	st := newStore(cfg, client)

	loc := testLocator(t)
	res := st.PutImmutable(context.Background(), &skillstore.PutRequest{Locator: loc, Bytes: []byte("payload")})
	if res.Outcome != skillstore.PutAlreadyExists {
		t.Fatalf("outcome = %v, want PutAlreadyExists", res.Outcome)
	}
	if calls != 1 {
		t.Errorf("PutObject called %d times, want exactly 1", calls)
	}
	if gotIfNoneMatch != "*" {
		t.Errorf("If-None-Match = %q, want *", gotIfNoneMatch)
	}
}
