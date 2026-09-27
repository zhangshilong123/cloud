package s3store

import (
	"fmt"
	"net/url"
	"time"
)

// CredentialMode values accepted by Config.CredentialMode. They mirror the frozen
// `storage.credential_mode` contract (ADR D10): environment reads static keys from
// AWS_ACCESS_KEY_ID / AWS_SECRET_ACCESS_KEY / AWS_SESSION_TOKEN;
// shared_credentials_file reads a mounted credentials file; workload_identity
// relies on the platform-assigned identity (IMDS / ECS task role / IRSA).
const (
	CredentialModeEnvironment           = "environment"
	CredentialModeSharedCredentialsFile = "shared_credentials_file"
	CredentialModeWorkloadIdentity      = "workload_identity"
)

// Config is the S3-compatible adapter configuration. It is deliberately a separate
// type from internal/config: internal/config imports internal/core, and a
// skillstore subpackage that imported internal/config would close the import cycle
// core → skillstore → s3store → config → core. cmd/server translates the resolved
// `storage` section into this type at wiring time, so every value here is already
// concrete — timeouts have defaults applied, TLS verification is a resolved bool,
// and credential_mode has been normalized.
type Config struct {
	Region   string
	Bucket   string
	Endpoint string

	// PathStyle selects path-style addressing (http(s)://host/bucket/key) instead of
	// virtual-hosted style. Self-hosted S3-compatible endpoints typically require it.
	PathStyle bool

	// CredentialMode is one of the CredentialMode* constants; "" is treated as
	// environment. CredentialsFile is the path used when the mode is
	// shared_credentials_file.
	CredentialMode  string
	CredentialsFile string

	// InsecureSkipVerify disables TLS certificate verification. It must be false in
	// production; it is only ever true when the deployment explicitly set
	// storage.tls.verify=false (with its dev/test-only meaning).
	InsecureSkipVerify bool

	// CAFile is an optional PEM CA bundle used to verify the endpoint's certificate.
	CAFile string

	// ConnectTimeout bounds establishing a connection; RequestTimeout bounds each
	// operation end-to-end (ADR D8). Both must be positive.
	ConnectTimeout time.Duration
	RequestTimeout time.Duration
}

// Validate rejects an unusable configuration before any client is built. It never
// reaches the network: bucket reachability is classified on first use, not probed
// at startup (ADR D14).
func (c Config) Validate() error {
	if c.Region == "" {
		return fmt.Errorf("s3store: region is required")
	}
	if c.Bucket == "" {
		return fmt.Errorf("s3store: bucket is required")
	}
	switch c.CredentialMode {
	case "", CredentialModeEnvironment, CredentialModeSharedCredentialsFile, CredentialModeWorkloadIdentity:
	default:
		return fmt.Errorf("s3store: unknown credential_mode %q", c.CredentialMode)
	}
	if c.CredentialMode == CredentialModeSharedCredentialsFile && c.CredentialsFile == "" {
		return fmt.Errorf("s3store: credential_mode=%s requires credentials_file", CredentialModeSharedCredentialsFile)
	}
	if c.Endpoint != "" {
		u, err := url.Parse(c.Endpoint)
		if err != nil || u.Host == "" {
			return fmt.Errorf("s3store: endpoint %q is not a valid URL", c.Endpoint)
		}
		if u.Scheme != "http" && u.Scheme != "https" {
			return fmt.Errorf("s3store: endpoint scheme must be http or https, got %q", u.Scheme)
		}
	}
	if c.ConnectTimeout <= 0 {
		return fmt.Errorf("s3store: connect timeout must be positive")
	}
	if c.RequestTimeout <= 0 {
		return fmt.Errorf("s3store: request timeout must be positive")
	}
	return nil
}
