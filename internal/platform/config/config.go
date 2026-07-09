// Package config loads runtime configuration. Precedence: flags > environment
// > defaults. Secrets are accepted from the environment only, never from flags,
// so they cannot leak via argv or process listings.
package config

import (
	"flag"
	"fmt"
	"time"
)

// EnvPrefix is the prefix for all Sextant environment variables.
const EnvPrefix = "SEXTANT_"

// Config is the fully resolved runtime configuration.
type Config struct {
	// Addr is the HTTP listen address, e.g. "127.0.0.1:8080".
	Addr string
	// LogLevel is one of debug, info, warn, error.
	LogLevel string
	// LogFormat is one of text, json.
	LogFormat string
	// ShutdownGrace bounds how long a graceful shutdown may take before
	// in-flight requests are cut off.
	ShutdownGrace time.Duration

	// RepoDir is the organisation's overlay working tree (the config plane).
	// Empty runs the server without a configuration plane (health/metrics
	// only), useful for probes and smoke tests.
	RepoDir string
	// Write enables the write path (mutations, commits). Off by default:
	// a read-only console can never change the fleet.
	Write bool
	// GateMode selects the validation gate: "eval" (nix eval, the default)
	// or "none" (no gate; for tests and repos without a flake).
	GateMode string
	// GitRemote names the push remote for the HA write path ("" = local
	// commits only).
	GitRemote string

	// APIToken guards /api/v1 (bearer). Environment-only (SEXTANT_API_TOKEN):
	// secrets never appear on the command line. Empty disables the API.
	APIToken string
}

// Getenv is the environment lookup used by Load. Injected so tests can supply
// a fake environment without mutating the process.
type Getenv func(key string) string

// Load parses args (without the program name) against env and returns the
// resolved configuration. It returns an error for unknown flags or invalid
// values; it never calls os.Exit.
func Load(args []string, getenv Getenv) (*Config, error) {
	cfg := &Config{
		Addr:          envOr(getenv, "ADDR", "127.0.0.1:8080"),
		LogLevel:      envOr(getenv, "LOG_LEVEL", "info"),
		LogFormat:     envOr(getenv, "LOG_FORMAT", "text"),
		ShutdownGrace: 15 * time.Second,
		RepoDir:       envOr(getenv, "REPO", ""),
		GateMode:      envOr(getenv, "GATE", "eval"),
		GitRemote:     envOr(getenv, "GIT_REMOTE", ""),
		APIToken:      getenv(EnvPrefix + "API_TOKEN"), // env-only secret
	}
	if v := getenv(EnvPrefix + "SHUTDOWN_GRACE"); v != "" {
		d, err := time.ParseDuration(v)
		if err != nil {
			return nil, fmt.Errorf("%sSHUTDOWN_GRACE: %w", EnvPrefix, err)
		}
		cfg.ShutdownGrace = d
	}

	fs := flag.NewFlagSet("sextant", flag.ContinueOnError)
	fs.StringVar(&cfg.Addr, "addr", cfg.Addr, "HTTP listen address")
	fs.StringVar(&cfg.LogLevel, "log-level", cfg.LogLevel, "log level: debug|info|warn|error")
	fs.StringVar(&cfg.LogFormat, "log-format", cfg.LogFormat, "log format: text|json")
	fs.DurationVar(&cfg.ShutdownGrace, "shutdown-grace", cfg.ShutdownGrace, "graceful shutdown timeout")
	fs.StringVar(&cfg.RepoDir, "repo", cfg.RepoDir, "overlay working tree (the config plane)")
	fs.BoolVar(&cfg.Write, "write", cfg.Write, "enable the write path (mutations, commits)")
	fs.StringVar(&cfg.GateMode, "gate", cfg.GateMode, "validation gate: eval|none")
	fs.StringVar(&cfg.GitRemote, "git-remote", cfg.GitRemote, "push remote for the HA write path")
	if err := fs.Parse(args); err != nil {
		return nil, err
	}
	if err := cfg.validate(); err != nil {
		return nil, err
	}
	return cfg, nil
}

func (c *Config) validate() error {
	switch c.LogLevel {
	case "debug", "info", "warn", "error":
	default:
		return fmt.Errorf("log-level %q: must be debug|info|warn|error", c.LogLevel)
	}
	switch c.LogFormat {
	case "text", "json":
	default:
		return fmt.Errorf("log-format %q: must be text|json", c.LogFormat)
	}
	if c.Addr == "" {
		return fmt.Errorf("addr must not be empty")
	}
	if c.ShutdownGrace <= 0 {
		return fmt.Errorf("shutdown-grace must be positive")
	}
	switch c.GateMode {
	case "eval", "none":
	default:
		return fmt.Errorf("gate %q: must be eval|none", c.GateMode)
	}
	if c.Write && c.RepoDir == "" {
		return fmt.Errorf("--write needs --repo")
	}
	return nil
}

func envOr(getenv Getenv, key, def string) string {
	if v := getenv(EnvPrefix + key); v != "" {
		return v
	}
	return def
}
