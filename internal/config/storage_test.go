package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func loadStorage(t *testing.T, storageYAML string) (*Config, error) {
	t.Helper()
	base := "server:\n  read_timeout: 10s\n  write_timeout: 15s\ndatabase:\n  conn_max_lifetime: 1h\ncontrol:\n  grpc_addr: 127.0.0.1:8082\n"
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte(base+storageYAML), 0o600); err != nil {
		t.Fatal(err)
	}
	return Load(path)
}

func TestLoadStorageAbsentIsNil(t *testing.T) {
	cfg, err := loadStorage(t, "")
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Storage != nil {
		t.Fatalf("storage absent must decode to nil, got %+v", cfg.Storage)
	}
}

func TestLoadStorageAppliesDefaults(t *testing.T) {
	cfg, err := loadStorage(t, "storage:\n  provider: s3\n  bucket: bkt\n  region: us-east-1\n")
	if err != nil {
		t.Fatal(err)
	}
	s := cfg.Storage
	if s.CredentialMode != "environment" {
		t.Errorf("credential_mode default = %q, want environment", s.CredentialMode)
	}
	if s.TLS.Verify == nil || !*s.TLS.Verify {
		t.Errorf("tls.verify default = %v, want true", s.TLS.Verify)
	}
	if s.Timeouts.Connect != 5*time.Second || s.Timeouts.Request != 30*time.Second {
		t.Errorf("timeouts default = %v/%v, want 5s/30s", s.Timeouts.Connect, s.Timeouts.Request)
	}
}

func TestLoadStorageFull(t *testing.T) {
	yaml := "storage:\n" +
		"  provider: s3\n" +
		"  bucket: bkt\n" +
		"  region: us-east-1\n" +
		"  endpoint: https://s3.example.com\n" +
		"  path_style: true\n" +
		"  credential_mode: shared_credentials_file\n" +
		"  credentials_file: /etc/creds\n" +
		"  tls:\n    verify: false\n" +
		"  allow_insecure_http: false\n" +
		"  timeouts:\n    connect: 2s\n    request: 10s\n"
	cfg, err := loadStorage(t, yaml)
	if err != nil {
		t.Fatal(err)
	}
	s := cfg.Storage
	if s.Bucket != "bkt" || s.Region != "us-east-1" || s.Endpoint != "https://s3.example.com" {
		t.Fatalf("storage not parsed: %+v", s)
	}
	if !s.PathStyle || s.CredentialMode != "shared_credentials_file" || s.CredentialsFile != "/etc/creds" {
		t.Fatalf("storage credential/path fields not parsed: %+v", s)
	}
	if s.TLS.Verify == nil || *s.TLS.Verify {
		t.Errorf("tls.verify = %v, want false", s.TLS.Verify)
	}
	if s.Timeouts.Connect != 2*time.Second || s.Timeouts.Request != 10*time.Second {
		t.Errorf("timeouts = %v/%v, want 2s/10s", s.Timeouts.Connect, s.Timeouts.Request)
	}
}

func TestLoadStorageRejectsInvalid(t *testing.T) {
	cases := []struct {
		name string
		yaml string
		want string // substring of the error
	}{
		{"unknown provider", "storage:\n  provider: gcs\n  bucket: bkt\n  region: r\n", "storage.provider"},
		{"missing bucket", "storage:\n  provider: s3\n  region: r\n", "storage.bucket"},
		{"missing region", "storage:\n  provider: s3\n  bucket: bkt\n", "storage.region"},
		{"unknown credential_mode", "storage:\n  provider: s3\n  bucket: bkt\n  region: r\n  credential_mode: bogus\n", "storage.credential_mode"},
		{"shared file without path", "storage:\n  provider: s3\n  bucket: bkt\n  region: r\n  credential_mode: shared_credentials_file\n", "storage.credentials_file"},
		{"http without allow", "storage:\n  provider: s3\n  bucket: bkt\n  region: r\n  endpoint: http://s3.example.com\n", "allow_insecure_http"},
		{"verify false with ca", "storage:\n  provider: s3\n  bucket: bkt\n  region: r\n  tls:\n    verify: false\n    ca_file: /ca.pem\n", "tls.verify=false"},
		{"negative connect", "storage:\n  provider: s3\n  bucket: bkt\n  region: r\n  timeouts:\n    connect: -1s\n", "timeouts.connect"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := loadStorage(t, c.yaml)
			if err == nil {
				t.Fatalf("expected error containing %q, got nil", c.want)
			}
			if !strings.Contains(err.Error(), c.want) {
				t.Fatalf("error = %q, want it to contain %q", err, c.want)
			}
		})
	}
}

func TestLoadStorageHTTPEndpointWithAllow(t *testing.T) {
	yaml := "storage:\n  provider: s3\n  bucket: bkt\n  region: r\n  endpoint: http://127.0.0.1:9000\n  allow_insecure_http: true\n"
	if _, err := loadStorage(t, yaml); err != nil {
		t.Fatalf("http endpoint with allow_insecure_http should load, got %v", err)
	}
}

func TestLoadStorageBucketFromEnv(t *testing.T) {
	yaml := "storage:\n  provider: s3\n  region: us-east-1\n"
	t.Setenv("CLOUD_STORAGE_BUCKET", "env-bkt")
	cfg, err := loadStorage(t, yaml)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Storage == nil || cfg.Storage.Bucket != "env-bkt" {
		t.Fatalf("CLOUD_STORAGE_BUCKET must fill storage.bucket, got %+v", cfg.Storage)
	}
}
