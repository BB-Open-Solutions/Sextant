// Package config loads runtime configuration. Precedence: flags > environment
// > defaults. Secrets are accepted from the environment only, never from flags,
// so they cannot leak via argv or process listings.
package config

import (
	"encoding/base64"
	"flag"
	"fmt"
	"strconv"
	"strings"
	"time"
)

// EnvPrefix is the prefix for all Sextant environment variables.
const EnvPrefix = "SEXTANT_"

// Config is the fully resolved runtime configuration.
type Config struct {
	// Addr is the HTTP listen address, e.g. "127.0.0.1:8080".
	Addr string

	// MetricsAddr, when set, moves /metrics onto its own listener and takes
	// it OFF the main one. The main address is the one behind the ingress, so
	// anything served there is served to whoever can reach the console -
	// which for a public console means the internet.
	//
	// /metrics discloses the exact build, the route inventory and the traffic
	// volume per page. No fleet data, but a version number is the first thing
	// somebody matches against a vulnerability list, and none of it needs a
	// public path: Prometheus scrapes inside the cluster.
	//
	// Empty keeps /metrics on the main listener, which is right for local
	// development where there is no second port to scrape and no ingress in
	// front. Deployments set it.
	MetricsAddr string
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
	// GateMode selects the validation gate: "eval" (in-process nix eval,
	// the default), "remote" (delegate to a nix-capable gate-runner; the
	// console image ships no nix) or "none" (no gate; tests / no flake).
	GateMode string
	// GateURL is the gate-runner base URL when GateMode is "remote".
	GateURL string
	// GateToken is the bearer secret the console presents to a gate-runner
	// that requires authentication (GATE_TOKEN on the runner). Empty sends no
	// Authorization header, for a runner that allows unauthenticated calls.
	GateToken string
	// AllowUnvalidated acknowledges running the WRITE path with no validation
	// gate (GateMode "none"). Without it, --write + --gate none refuses to
	// start: committing configuration unvalidated is a foot-gun in a real
	// deployment (a bad fleet.json reaches devices). Local dev / tests set it
	// explicitly (SEXTANT_ALLOW_UNVALIDATED=1); a read-only console never
	// needs it.
	AllowUnvalidated bool
	// DisableDiagnostics is the deployment-level kill switch for the
	// diagnostics-on-demand feature (design 0010): a tenant that must forbid
	// log collection outright sets SEXTANT_DISABLE_DIAGNOSTICS=true and the
	// console can neither request nor accept bundles.
	DisableDiagnostics bool
	// ReleaseCache enables build-before-promote (GateMode remote only): a
	// ring's release is built into the runner's signed binary cache before its
	// branch moves. Requires the runner's cache to be configured
	// (GATE_CACHE_DIR + signing key) and devices to list it as a substituter.
	ReleaseCache bool
	// GitRemote names the push remote for the HA write path ("" = local
	// commits only).
	GitRemote string
	// StateDir holds durable control-plane state (change requests, rollout
	// runs). Empty defaults to <repo>/.sextant-state.
	StateDir string

	// APIToken guards /api/v1 (bearer). Environment-only (SEXTANT_API_TOKEN):
	// secrets never appear on the command line. Empty disables the API.
	APIToken string
	// CheckinToken is the OPTIONAL shared bridge token for POST /api/checkin
	// (device-facing). Environment-only. Per-device credentials authorize
	// check-in on their own (api/checkin.go authorized); empty disables only
	// the shared-token path, which is the hardened shape - a cell without
	// this token has no fleet-wide check-in secret to steal.
	CheckinToken string
	// PgDSN connects the observed plane to Postgres. Environment-only
	// (SEXTANT_PG_DSN, carries a password). Empty disables the observed
	// plane (check-in, status, rollout convergence).
	PgDSN string

	// SecretKey seals operator-entered secrets at rest (an SMTP password
	// typed in the console). Environment-only (SEXTANT_SECRET_KEY,
	// base64 32 bytes). Empty disables the entered-secret path; secret
	// references still work.
	SecretKey string
	// SecretDir is where mounted secret references are read from (agenix, a
	// cluster Secret projected to files). A reference name maps to a file
	// here. Empty falls back to /run/secrets.
	SecretDir string

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
	// TrustProxy tells the rate limiter to key clients on the rightmost
	// X-Forwarded-For entry (the address the trusted ingress observed) instead
	// of RemoteAddr, which behind a proxy is the proxy itself. Enable ONLY when
	// the service actually sits behind a trusted reverse proxy.
	TrustProxy bool
	// SecureCookies marks cookies Secure (set behind TLS). Settable by
	// --secure-cookies or SEXTANT_SECURE_COOKIES (flag wins).
	SecureCookies bool
	// UpstreamRepo is the core image repository (DAWO-NixOS) the upstream
	// watcher polls; a new HEAD stages a core-update change request. Empty
	// disables the watcher.
	UpstreamRepo string

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
	// OrgName is the organisation's display name - the scope tree's root as
	// shown in the console (group parents, scope selectors).
	OrgName string

	// ConsoleURL is the console's public base (e.g. https://console.example.com),
	// used to make notification e-mails clickable. Empty omits the link.
	ConsoleURL string
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
		MetricsAddr:      envOr(getenv, "METRICS_ADDR", ""),
		LogLevel:         envOr(getenv, "LOG_LEVEL", "info"),
		LogFormat:        envOr(getenv, "LOG_FORMAT", "text"),
		ShutdownGrace:    15 * time.Second,
		RepoDir:          envOr(getenv, "REPO", ""),
		GateMode:         envOr(getenv, "GATE", "eval"),
		GateURL:          envOr(getenv, "GATE_URL", ""),
		GateToken:        envOr(getenv, "GATE_TOKEN", ""),
		GitRemote:        envOr(getenv, "GIT_REMOTE", ""),
		APIToken:         getenv(EnvPrefix + "API_TOKEN"),     // env-only secret
		CheckinToken:     getenv(EnvPrefix + "CHECKIN_TOKEN"), // env-only secret
		PgDSN:            getenv(EnvPrefix + "PG_DSN"),        // env-only secret
		SecretKey:        getenv(EnvPrefix + "SECRET_KEY"),    // env-only secret
		SecretDir:        envOr(getenv, "SECRET_DIR", "/run/secrets"),
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
		OrgName:          envOr(getenv, "ORG_NAME", ""),
		UpstreamRepo:     envOr(getenv, "UPSTREAM_REPO", ""),
		ConsoleURL:       envOr(getenv, "CONSOLE_URL", ""),
	}
	// Environment-only bools. Parsed with ParseBool rather than compared to
	// the literal "true": the refusal message for --gate none tells operators
	// to set SEXTANT_ALLOW_UNVALIDATED=1, and a string compare made that
	// instruction silently wrong. Strict, so a typo cannot read as false and
	// turn an acknowledgement into a refusal nobody can explain.
	for _, b := range []struct {
		key string
		dst *bool
	}{
		{"ALLOW_UNVALIDATED", &cfg.AllowUnvalidated},
		{"DISABLE_DIAGNOSTICS", &cfg.DisableDiagnostics},
		{"RELEASE_CACHE", &cfg.ReleaseCache},
	} {
		v := getenv(EnvPrefix + b.key)
		if v == "" {
			continue
		}
		parsed, err := strconv.ParseBool(v)
		if err != nil {
			return nil, fmt.Errorf("%s%s: %w", EnvPrefix, b.key, err)
		}
		*b.dst = parsed
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
	// Secure cookies: environment-only bool, parsed strictly so a typo
	// doesn't silently leave cookies insecure. Seeded before flag
	// registration so --secure-cookies (which defaults to cfg.SecureCookies)
	// still wins per the package's flags > environment > defaults precedence.
	if v := getenv(EnvPrefix + "SECURE_COOKIES"); v != "" {
		b, err := strconv.ParseBool(v)
		if err != nil {
			return nil, fmt.Errorf("%sSECURE_COOKIES: %w", EnvPrefix, err)
		}
		cfg.SecureCookies = b
	}
	if v := getenv(EnvPrefix + "TRUST_PROXY"); v != "" {
		b, err := strconv.ParseBool(v)
		if err != nil {
			return nil, fmt.Errorf("%sTRUST_PROXY: %w", EnvPrefix, err)
		}
		cfg.TrustProxy = b
	}

	fs := flag.NewFlagSet("sextant", flag.ContinueOnError)
	fs.StringVar(&cfg.Addr, "addr", cfg.Addr, "HTTP listen address")
	fs.StringVar(&cfg.MetricsAddr, "metrics-addr", cfg.MetricsAddr,
		"serve /metrics on this address instead of the main one (empty: on the main listener)")
	fs.StringVar(&cfg.LogLevel, "log-level", cfg.LogLevel, "log level: debug|info|warn|error")
	fs.StringVar(&cfg.LogFormat, "log-format", cfg.LogFormat, "log format: text|json")
	fs.DurationVar(&cfg.ShutdownGrace, "shutdown-grace", cfg.ShutdownGrace, "graceful shutdown timeout")
	fs.StringVar(&cfg.RepoDir, "repo", cfg.RepoDir, "overlay working tree (the config plane)")
	fs.BoolVar(&cfg.Write, "write", cfg.Write, "enable the write path (mutations, commits)")
	fs.StringVar(&cfg.GateMode, "gate", cfg.GateMode, "validation gate: eval|remote|none")
	fs.StringVar(&cfg.GateURL, "gate-url", cfg.GateURL, "gate-runner base URL (when --gate=remote)")
	fs.StringVar(&cfg.GateToken, "gate-token", cfg.GateToken, "bearer token for the gate-runner (when --gate=remote)")
	fs.BoolVar(&cfg.AllowUnvalidated, "allow-unvalidated", cfg.AllowUnvalidated, "acknowledge running the write path with --gate none (local dev only)")
	fs.BoolVar(&cfg.ReleaseCache, "release-cache", cfg.ReleaseCache, "build releases into the runner's binary cache before ring promotion (when --gate=remote)")
	fs.StringVar(&cfg.GitRemote, "git-remote", cfg.GitRemote, "push remote for the HA write path")
	fs.StringVar(&cfg.StateDir, "state-dir", cfg.StateDir, "durable control-plane state dir (default <repo>/.sextant-state)")
	fs.StringVar(&cfg.OIDCIssuer, "oidc-issuer", cfg.OIDCIssuer, "OIDC issuer URL (empty disables session auth)")
	fs.StringVar(&cfg.OIDCClientID, "oidc-client-id", cfg.OIDCClientID, "OIDC client id")
	fs.StringVar(&cfg.OIDCRedirectURL, "oidc-redirect-url", cfg.OIDCRedirectURL, "OIDC redirect URL (https://host/callback)")
	fs.StringVar(&cfg.OIDCGroupsClaim, "oidc-groups-claim", cfg.OIDCGroupsClaim, "ID-token claim carrying groups (default groups)")
	scopes := fs.String("oidc-scopes", envOr(getenv, "OIDC_SCOPES", ""), "comma-separated OIDC scopes (default openid,profile,email)")
	fs.BoolVar(&cfg.SecureCookies, "secure-cookies", cfg.SecureCookies, "mark cookies Secure (set behind TLS)")
	fs.BoolVar(&cfg.TrustProxy, "trust-proxy", cfg.TrustProxy, "key rate limits on X-Forwarded-For (only behind a trusted proxy)")
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
	case "eval":
	case "none":
		// Fail-safe: a write-enabled console with no validation gate would
		// commit configuration nothing checked, and a bad fleet.json reaches
		// devices. Refuse unless explicitly acknowledged (local dev / tests).
		if c.Write && !c.AllowUnvalidated {
			return fmt.Errorf("refusing --write with --gate none: unvalidated commits reach devices; use --gate eval|remote, or acknowledge with --allow-unvalidated (SEXTANT_ALLOW_UNVALIDATED=1) for local dev")
		}
	case "remote":
		if c.GateURL == "" {
			return fmt.Errorf("gate remote requires --gate-url")
		}
	default:
		return fmt.Errorf("gate %q: must be eval|remote|none", c.GateMode)
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
		// Session auth over anything but loopback must mark cookies Secure -
		// otherwise a session cookie can be sent in the clear on a
		// misconfigured (non-TLS, or TLS-terminating-proxy) deployment. Fail
		// fast rather than ship an insecure default.
		if !c.SecureCookies && !isLoopback(c.Addr) {
			return fmt.Errorf("session auth on non-loopback --addr %q requires --secure-cookies (or SEXTANT_SECURE_COOKIES=true); refusing to ship session cookies without the Secure flag", c.Addr)
		}
	}
	if c.DevAuth {
		if !isLoopback(c.Addr) {
			return fmt.Errorf("--dev-auth requires a loopback --addr, got %s", c.Addr)
		}
		if c.OIDCIssuer != "" {
			return fmt.Errorf("--dev-auth and --oidc-issuer are mutually exclusive")
		}
	}
	return nil
}

// isLoopback reports whether addr's host part is a loopback address.
func isLoopback(addr string) bool {
	host := addr
	if i := strings.LastIndex(host, ":"); i >= 0 {
		host = host[:i]
	}
	switch host {
	case "127.0.0.1", "localhost", "::1", "[::1]":
		return true
	}
	return false
}

func envOr(getenv Getenv, key, def string) string {
	if v := getenv(EnvPrefix + key); v != "" {
		return v
	}
	return def
}
