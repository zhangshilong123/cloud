// Package config handles application configuration loading and parsing.
package config

import (
	"fmt"
	"strings"
	"time"

	"github.com/spf13/viper"

	"github.com/wanglongan587/cloud/internal/core"
	"github.com/wanglongan587/cloud/internal/logger"
)

// Config holds all configuration of the application.
type Config struct {
	Server        ServerConfig        `mapstructure:"server"`
	Logger        logger.Config       `mapstructure:"logger"`
	Database      DatabaseConfig      `mapstructure:"database"`
	Auth          AuthConfig          `mapstructure:"auth"`
	Collaboration CollaborationConfig `mapstructure:"collaboration"`
	Control       ControlConfig       `mapstructure:"control"`
	Storage       *StorageConfig      `mapstructure:"storage"`
	Runtime       *RuntimeConfig      `mapstructure:"runtime"`
}

// CollaborationConfig gates optional collaboration-capability wiring on the Store.
type CollaborationConfig struct {
	// DevelopmentFixtures installs the in-memory dev/demo Agent/Team/Workflow fixtures
	// (internal/collab) onto the Store's collaboration ports. Development-only and explicitly
	// enabled: production must leave it false (default), in which case only human targets are
	// served. It must never be coupled to whether an external login provider is enabled.
	DevelopmentFixtures bool `mapstructure:"development_fixtures"`
}

// ControlConfig binds the Controller-facing gRPC listener. Until the authentication ADR adds TLS,
// the address must stay on a loopback or private network; the contract carries bearer credentials
// only.
type ControlConfig struct {
	GRPCAddr string `mapstructure:"grpc_addr"`
}

// AuthConfig contains only internal verification keys, never an external login SDK.
type AuthConfig struct {
	Audience string            `mapstructure:"audience"`
	Keys     []core.TrustedKey `mapstructure:"keys"`
}

// ServerConfig holds HTTP server configuration.
type ServerConfig struct {
	Port         int           `mapstructure:"port"`
	Mode         string        `mapstructure:"mode"`
	ReadTimeout  time.Duration `mapstructure:"read_timeout"`
	WriteTimeout time.Duration `mapstructure:"write_timeout"`
}

// DatabaseConfig holds database connection parameters.
type DatabaseConfig struct {
	Driver          string        `mapstructure:"driver"`
	DSN             string        `mapstructure:"dsn"`
	MaxIdleConns    int           `mapstructure:"max_idle_conns"`
	MaxOpenConns    int           `mapstructure:"max_open_conns"`
	ConnMaxLifetime time.Duration `mapstructure:"conn_max_lifetime"`
}

// Load reads configuration from file and environment variables.
func Load(configPath string) (*Config, error) {
	v := viper.New()

	if configPath != "" {
		v.SetConfigFile(configPath)
	} else {
		v.SetConfigName("config")
		v.SetConfigType("yaml")
		v.AddConfigPath("./configs")
		v.AddConfigPath("../configs")
		v.AddConfigPath(".")
	}

	// Read environment variables (e.g., CLOUD_SERVER_PORT=8080)
	v.SetEnvPrefix("CLOUD")
	v.SetEnvKeyReplacer(strings.NewReplacer(".", "_"))
	v.AutomaticEnv()
	if err := BindEnvKeys(v, Config{}); err != nil {
		return nil, err
	}

	if err := v.ReadInConfig(); err != nil {
		return nil, err
	}

	var cfg Config
	if err := v.Unmarshal(&cfg); err != nil {
		return nil, err
	}
	if cfg.Server.ReadTimeout <= 0 || cfg.Server.WriteTimeout <= 0 || cfg.Database.ConnMaxLifetime <= 0 {
		return nil, fmt.Errorf("server timeouts and database.conn_max_lifetime must be positive durations")
	}
	if cfg.Control.GRPCAddr == "" {
		return nil, fmt.Errorf("control.grpc_addr is required")
	}
	if cfg.Storage != nil {
		cfg.Storage.applyDefaults()
		if err := cfg.Storage.Validate(); err != nil {
			return nil, err
		}
	}
	if cfg.Runtime != nil {
		cfg.Runtime.applyDefaults()
		if err := cfg.Runtime.Validate(); err != nil {
			return nil, err
		}
	}

	return &cfg, nil
}
