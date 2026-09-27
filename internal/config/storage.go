package config

import (
	"fmt"
	"net/url"
	"time"
)

// Default storage timeouts (ADR D8), applied when the deployer omits them.
const (
	defaultStorageConnectTimeout = 5 * time.Second
	defaultStorageRequestTimeout = 30 * time.Second
)

// StorageConfig is the optional `storage` section. It is a pointer field on Config:
// a nil pointer means the section is absent, the process starts normally, and the
// Object Store stays unconfigured (Store.SkillsObjectStore == nil, the saga returns
// object_store_unavailable). A non-nil pointer with invalid values fails startup
// (ADR D14).
type StorageConfig struct {
	// Provider is the only supported value today: "s3".
	Provider string `mapstructure:"provider"`
	// Bucket is the single configured bucket (ADR D11); the name never enters
	// durable state.
	Bucket string `mapstructure:"bucket"`
	// Region is the protocol-required region placeholder for the endpoint.
	Region string `mapstructure:"region"`
	// Endpoint is optional; empty means the AWS default endpoint, non-empty points
	// at a self-hosted S3-compatible endpoint.
	Endpoint string `mapstructure:"endpoint"`
	// PathStyle selects path-style addressing for self-hosted endpoints.
	PathStyle bool `mapstructure:"path_style"`
	// CredentialMode is environment | shared_credentials_file | workload_identity.
	CredentialMode string `mapstructure:"credential_mode"`
	// CredentialsFile is required when CredentialMode is shared_credentials_file.
	CredentialsFile string `mapstructure:"credentials_file"`

	TLS               StorageTLSConfig      `mapstructure:"tls"`
	AllowInsecureHTTP bool                  `mapstructure:"allow_insecure_http"`
	Timeouts          StorageTimeoutsConfig `mapstructure:"timeouts"`
}

// StorageTLSConfig controls TLS verification. Verify is a pointer so that an
// absent `tls` block defaults to true (verify), never to the bool zero value,
// which would silently disable certificate verification.
type StorageTLSConfig struct {
	Verify *bool  `mapstructure:"verify"`
	CAFile string `mapstructure:"ca_file"`
}

// StorageTimeoutsConfig bounds the connection and the whole request (ADR D8).
type StorageTimeoutsConfig struct {
	Connect time.Duration `mapstructure:"connect"`
	Request time.Duration `mapstructure:"request"`
}

// applyDefaults fills the frozen defaults for fields the deployer omitted. It runs
// after unmarshalling and before Validate.
func (s *StorageConfig) applyDefaults() {
	if s.CredentialMode == "" {
		s.CredentialMode = "environment"
	}
	if s.TLS.Verify == nil {
		t := true
		s.TLS.Verify = &t
	}
	if s.Timeouts.Connect == 0 {
		s.Timeouts.Connect = defaultStorageConnectTimeout
	}
	if s.Timeouts.Request == 0 {
		s.Timeouts.Request = defaultStorageRequestTimeout
	}
}

// Validate rejects an unusable `storage` section before the process serves. It
// never probes the network (ADR D14).
func (s *StorageConfig) Validate() error {
	if s.Provider != "s3" {
		return fmt.Errorf("storage.provider must be %q, got %q", "s3", s.Provider)
	}
	if s.Bucket == "" {
		return fmt.Errorf("storage.bucket is required")
	}
	if s.Region == "" {
		return fmt.Errorf("storage.region is required")
	}
	switch s.CredentialMode {
	case "environment", "shared_credentials_file", "workload_identity":
	default:
		return fmt.Errorf("storage.credential_mode must be environment|shared_credentials_file|workload_identity, got %q", s.CredentialMode)
	}
	if s.CredentialMode == "shared_credentials_file" && s.CredentialsFile == "" {
		return fmt.Errorf("storage.credentials_file is required when credential_mode=shared_credentials_file")
	}
	if s.Endpoint != "" {
		u, err := url.Parse(s.Endpoint)
		if err != nil || u.Host == "" {
			return fmt.Errorf("storage.endpoint %q is not a valid URL", s.Endpoint)
		}
		if u.Scheme != "http" && u.Scheme != "https" {
			return fmt.Errorf("storage.endpoint scheme must be http or https, got %q", u.Scheme)
		}
		if u.Scheme == "http" && !s.AllowInsecureHTTP {
			return fmt.Errorf("storage.endpoint uses http:// but storage.allow_insecure_http is false")
		}
	}
	if s.TLS.Verify != nil && !*s.TLS.Verify && s.TLS.CAFile != "" {
		return fmt.Errorf("storage.tls.verify=false conflicts with storage.tls.ca_file (a CA is only used when verifying)")
	}
	if s.Timeouts.Connect < 0 {
		return fmt.Errorf("storage.timeouts.connect must be >= 0")
	}
	if s.Timeouts.Request < 0 {
		return fmt.Errorf("storage.timeouts.request must be >= 0")
	}
	return nil
}
