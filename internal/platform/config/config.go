// Package config loads runtime configuration. Precedence: flags > environment
// > defaults. Secrets are accepted from the environment only, never from flags,
// so they cannot leak via argv or process listings.
package config

import (
	"encoding/base64"
	"flag"
	"fmt"
	"strings"
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
	// StateDir holds durable control-plane state (change requests, rollout
	// runs). Empty defaults to <repo>/.sextant-state.
	StateDir string

	// APIToken guards /api/v1 (bearer). Environment-only (SEXTANT_API_TOKEN):
	// secrets never appear on the command line. Empty disables the API.
	APIToken string
	// CheckinToken guards POST /api/checkin (device-facing, its own
	// credential). Environment-only. Empty disables check-in.
	CheckinToken string
	// PgDSN connects the observed plane to Postgres. Environment-only
	// (SEXTANT_PG_DSN, carries a password). Empty disables the observed
	// plane (check-in, status, rollout convergence).
	PgDSN string

	// OIDC console SSO. Empty issuer disables session auth (token-only).
	OIDCIssuer      string
	OIDCClientID    string
	OIDCRedirectURL string
	OIDCGroupsClaim string
	// OIDCScopes is a comma-separated scope list ("" = openid,profile,email).
	OIDCScopes []string
	// OIDCClientSecret and SessionKey are environment-only secrets.
	OIDCClientSecret string
	SessionKey       []byte
	// SecureCookies marks cookies Secure (set behind TLS).
	SecureCookies bool
	// DevAuth substitutes a synthetic owner session (no IdP). Loopback
	// only; the server refuses to start with dev auth on a public address.
	DevAuth bool
	// Baseline org-wide role groups (server config; the fleet document's
	// access list adds per-scope bindings on top).
	ViewerGroups []string
	EditorGroups []string
	OwnerGroups  []string

	// LDAP directory browse (group pickers). The IdP (e.g. Zitadel) stays
	// the login authority; LDAP is the read-only group source. Empty URL
	// disables the directory surface.
	LDAPURL         string
	LDAPBindDN      string
	LDAPBindPass    string // environment-only secret
	LDAPBaseDN      string
	LDAPGroupFilter string
	LDAPNameAttr    string

	// Organisation presentation defaults; per-user preferences override.
	DefaultLocale   string
	DefaultTimezone string
}

// Getenv is the environment lookup used by Load. Injected so tests can supply
// a fake environment without mutating the process.
type Getenv func(key string) string

// Load parses args (without the program name) against env and returns the
// resolved configuration. It returns an error for unknown flags or invalid
// values; it never calls os.Exit.
func Load(args []string, getenv Getenv) (*Config, error) {
	cfg := &Config{
		Addr:             envOr(getenv, "ADDR", "127.0.0.1:8080"),
		LogLevel:         envOr(getenv, "LOG_LEVEL", "info"),
		LogFormat:        envOr(getenv, "LOG_FORMAT", "text"),
		ShutdownGrace:    15 * time.Second,
		RepoDir:          envOr(getenv, "REPO", ""),
		GateMode:         envOr(getenv, "GATE", "eval"),
		GitRemote:        envOr(getenv, "GIT_REMOTE", ""),
		APIToken:         getenv(EnvPrefix + "API_TOKEN"),     // env-only secret
		CheckinToken:     getenv(EnvPrefix + "CHECKIN_TOKEN"), // env-only secret
		PgDSN:            getenv(EnvPrefix + "PG_DSN"),        // env-only secret
		OIDCIssuer:       envOr(getenv, "OIDC_ISSUER", ""),
		OIDCClientID:     envOr(getenv, "OIDC_CLIENT_ID", ""),
		OIDCRedirectURL:  envOr(getenv, "OIDC_REDIRECT_URL", ""),
		OIDCGroupsClaim:  envOr(getenv, "OIDC_GROUPS_CLAIM", ""),
		OIDCClientSecret: getenv(EnvPrefix + "OIDC_CLIENT_SECRET"), // env-only secret
		LDAPURL:          envOr(getenv, "LDAP_URL", ""),
		LDAPBindDN:       envOr(getenv, "LDAP_BIND_DN", ""),
		LDAPBindPass:     getenv(EnvPrefix + "LDAP_BIND_PASSWORD"), // env-only secret
		LDAPBaseDN:       envOr(getenv, "LDAP_BASE_DN", ""),
		LDAPGroupFilter:  envOr(getenv, "LDAP_GROUP_FILTER", ""),
		LDAPNameAttr:     envOr(getenv, "LDAP_NAME_ATTR", ""),
		DefaultLocale:    envOr(getenv, "DEFAULT_LOCALE", "en"),
		DefaultTimezone:  envOr(getenv, "DEFAULT_TIMEZONE", "UTC"),
	}
	if v := getenv(EnvPrefix + "SHUTDOWN_GRACE"); v != "" {
		d, err := time.ParseDuration(v)
		if err != nil {
			return nil, fmt.Errorf("%sSHUTDOWN_GRACE: %w", EnvPrefix, err)
		}
		cfg.ShutdownGrace = d
	}
	// Session key: base64 of exactly 32 random bytes. Environment-only.
	if v := getenv(EnvPrefix + "SESSION_KEY"); v != "" {
		key, err := base64.StdEncoding.DecodeString(v)
		if err != nil {
			return nil, fmt.Errorf("%sSESSION_KEY: not valid base64: %w", EnvPrefix, err)
		}
		if len(key) != 32 {
			return nil, fmt.Errorf("%sSESSION_KEY: must decode to exactly 32 bytes, got %d", EnvPrefix, len(key))
		}
		cfg.SessionKey = key
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
	fs.StringVar(&cfg.StateDir, "state-dir", cfg.StateDir, "durable control-plane state dir (default <repo>/.sextant-state)")
	fs.StringVar(&cfg.OIDCIssuer, "oidc-issuer", cfg.OIDCIssuer, "OIDC issuer URL (empty disables session auth)")
	fs.StringVar(&cfg.OIDCClientID, "oidc-client-id", cfg.OIDCClientID, "OIDC client id")
	fs.StringVar(&cfg.OIDCRedirectURL, "oidc-redirect-url", cfg.OIDCRedirectURL, "OIDC redirect URL (https://host/callback)")
	fs.StringVar(&cfg.OIDCGroupsClaim, "oidc-groups-claim", cfg.OIDCGroupsClaim, "ID-token claim carrying groups (default groups)")
	scopes := fs.String("oidc-scopes", envOr(getenv, "OIDC_SCOPES", ""), "comma-separated OIDC scopes (default openid,profile,email)")
	fs.BoolVar(&cfg.SecureCookies, "secure-cookies", cfg.SecureCookies, "mark cookies Secure (set behind TLS)")
	fs.BoolVar(&cfg.DevAuth, "dev-auth", false, "synthetic owner session without an IdP (loopback only)")
	viewers := fs.String("viewer-groups", "", "comma-separated IdP groups with org-wide viewer role")
	editors := fs.String("editor-groups", "", "comma-separated IdP groups with org-wide editor role")
	owners := fs.String("owner-groups", "", "comma-separated IdP groups with org-wide owner role")
	if err := fs.Parse(args); err != nil {
		return nil, err
	}
	cfg.OIDCScopes = splitList(*scopes)
	cfg.ViewerGroups = splitList(*viewers)
	cfg.EditorGroups = splitList(*editors)
	cfg.OwnerGroups = splitList(*owners)
	if err := cfg.validate(); err != nil {
		return nil, err
	}
	return cfg, nil
}

// splitList parses a comma-separated flag into trimmed non-empty items.
func splitList(s string) []string {
	var out []string
	for _, p := range strings.Split(s, ",") {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
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
	if c.OIDCIssuer != "" {
		if c.OIDCClientID == "" || c.OIDCClientSecret == "" || c.OIDCRedirectURL == "" {
			return fmt.Errorf("oidc needs --oidc-client-id, --oidc-redirect-url and SEXTANT_OIDC_CLIENT_SECRET")
		}
		if len(c.SessionKey) != 32 {
			return fmt.Errorf("oidc needs SEXTANT_SESSION_KEY (base64 of 32 random bytes)")
		}
	}
	if c.DevAuth {
		host := c.Addr
		if i := strings.LastIndex(host, ":"); i >= 0 {
			host = host[:i]
		}
		switch host {
		case "127.0.0.1", "localhost", "::1", "[::1]":
		default:
			return fmt.Errorf("--dev-auth requires a loopback --addr, got %s", c.Addr)
		}
		if c.OIDCIssuer != "" {
			return fmt.Errorf("--dev-auth and --oidc-issuer are mutually exclusive")
		}
	}
	return nil
}

func envOr(getenv Getenv, key, def string) string {
	if v := getenv(EnvPrefix + key); v != "" {
		return v
	}
	return def
}
