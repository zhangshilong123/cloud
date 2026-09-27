# internal/config: Configuration Loader & Validation

[中文](README.md) | [English](README.en.md)

`internal/config` manages configuration parsing, schema validation, and environment variable overrides for Ora Cloud.

## Responsibilities

- **Structured configuration**: Defines strongly-typed Go structs mapping the entire system configuration:
  - `ServerConfig`: Port, Gin mode, read/write timeouts.
  - `LoggerConfig`: Log level, file paths, rotation thresholds (max size, age, backups, gzip compression).
  - `DatabaseConfig`: Driver (must be `postgres`), DSN, and connection pool limits (`max_open_conns`, `max_idle_conns`, `conn_max_lifetime`).
  - `AuthConfig`: Expected token audience and list of `TrustedKey` verification parameters.
  - `StorageConfig` (optional pointer): the `storage` section for the production Object Storage. Fields are
    documented in the commented sample in `configs/config.yaml` and in
    `specs/decisions/cloud/skills/20260927-production-object-storage-provider.md`.
- **Hierarchical loading via Viper**:
  - Searches for `config.yaml` in `./configs`, `../configs`, and `.`.
  - Supports explicit file path overriding via `-config <path>`.
  - Maps environment variables with the `CLOUD_` prefix, replacing dots with underscores (e.g., `CLOUD_DATABASE_DSN` overrides `database.dsn`).
- **Startup sanity validation**: Rejects invalid configurations with explicit errors, requiring positive durations for `read_timeout`, `write_timeout`, and `conn_max_lifetime`.
  A present-but-invalid `storage` section (unknown provider, missing bucket/region, conflicting TLS/credential
  combination, invalid timeout) is also rejected; when the section is absent, the process starts normally and
  `SkillsObjectStore` stays nil.

## Boundaries and invariants

- **No secret storage**: Configuration files store only public verification keys and infrastructure references. Plaintext deployment secrets and private keys must never appear in configuration files.
- **Immutable runtime**: Configurations are loaded once at command startup and passed as ready-to-use values. There is no global mutable configuration singleton.

See [config.yaml](../../configs/config.yaml), [cmd/server](../../cmd/server/README.en.md), and [Authentication](../../docs/authentication.md).
