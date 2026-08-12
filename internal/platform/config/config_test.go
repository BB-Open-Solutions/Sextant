package config

import (
	"strings"
	"testing"
	"time"
)

func env(m map[string]string) Getenv {
	return func(k string) string { return m[k] }
}

func TestDefaults(t *testing.T) {
	cfg, err := Load(nil, env(nil))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Addr != "127.0.0.1:8080" {
		t.Errorf("Addr = %q, want default", cfg.Addr)
	}
	if cfg.LogLevel != "info" || cfg.LogFormat != "text" {
		t.Errorf("log defaults = %q/%q", cfg.LogLevel, cfg.LogFormat)
	}
	if cfg.ShutdownGrace != 15*time.Second {
		t.Errorf("ShutdownGrace = %v", cfg.ShutdownGrace)
	}
}

func TestEnvOverridesDefaults(t *testing.T) {
	cfg, err := Load(nil, env(map[string]string{
		"SEXTANT_ADDR":           "0.0.0.0:9090",
		"SEXTANT_LOG_LEVEL":      "debug",
		"SEXTANT_LOG_FORMAT":     "json",
		"SEXTANT_SHUTDOWN_GRACE": "30s",
	}))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Addr != "0.0.0.0:9090" || cfg.LogLevel != "debug" ||
		cfg.LogFormat != "json" || cfg.ShutdownGrace != 30*time.Second {
		t.Errorf("env not applied: %+v", cfg)
	}
}

func TestFlagsOverrideEnv(t *testing.T) {
	cfg, err := Load(
		[]string{"--addr", "127.0.0.1:1234", "--log-level", "warn"},
		env(map[string]string{"SEXTANT_ADDR": "0.0.0.0:9090", "SEXTANT_LOG_LEVEL": "debug"}),
	)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Addr != "127.0.0.1:1234" || cfg.LogLevel != "warn" {
		t.Errorf("flags did not win: %+v", cfg)
	}
}

func TestInvalid(t *testing.T) {
	cases := []struct {
		name string
		args []string
		env  map[string]string
		want string
	}{
		{"bad level", []string{"--log-level", "loud"}, nil, "log-level"},
		{"bad format", []string{"--log-format", "xml"}, nil, "log-format"},
		{"empty addr", []string{"--addr", ""}, nil, "addr"},
		{"bad grace flag", []string{"--shutdown-grace", "-1s"}, nil, "shutdown-grace"},
		{"bad grace env", nil, map[string]string{"SEXTANT_SHUTDOWN_GRACE": "soon"}, "SHUTDOWN_GRACE"},
		{"unknown flag", []string{"--bogus"}, nil, "bogus"},
		{
			"session auth on non-loopback without secure cookies",
			[]string{
				"--addr", "0.0.0.0:8080",
				"--oidc-issuer", "https://idp.example.com",
				"--oidc-client-id", "sextant",
				"--oidc-redirect-url", "https://console.example.com/callback",
			},
			map[string]string{
				"SEXTANT_OIDC_CLIENT_SECRET": "secret",
				"SEXTANT_SESSION_KEY":        validSessionKey,
			},
			"requires --secure-cookies",
		},
		{
			"dev-auth on non-loopback",
			[]string{"--dev-auth", "--addr", "0.0.0.0:8080"},
			nil,
			"--dev-auth requires a loopback",
		},
		{
			"dev-auth and oidc mutually exclusive",
			[]string{
				"--dev-auth",
				"--oidc-issuer", "https://idp.example.com",
				"--oidc-client-id", "sextant",
				"--oidc-redirect-url", "https://console.example.com/callback",
			},
			map[string]string{
				"SEXTANT_OIDC_CLIENT_SECRET": "secret",
				"SEXTANT_SESSION_KEY":        validSessionKey,
			},
			"mutually exclusive",
		},
		{
			"oidc missing session key",
			[]string{
				"--addr", "127.0.0.1:8080",
				"--oidc-issuer", "https://idp.example.com",
				"--oidc-client-id", "sextant",
				"--oidc-redirect-url", "https://console.example.com/callback",
			},
			map[string]string{"SEXTANT_OIDC_CLIENT_SECRET": "secret"},
			"SEXTANT_SESSION_KEY",
		},
		{
			"oidc missing client secret",
			[]string{
				"--addr", "127.0.0.1:8080",
				"--oidc-issuer", "https://idp.example.com",
				"--oidc-client-id", "sextant",
				"--oidc-redirect-url", "https://console.example.com/callback",
			},
			map[string]string{"SEXTANT_SESSION_KEY": validSessionKey},
			"oidc needs",
		},
		{
			"gate remote without gate-url",
			[]string{"--gate", "remote"},
			nil,
			"gate remote requires --gate-url",
		},
		{
			"write with gate none unacknowledged",
			[]string{"--write", "--repo", "/tmp/x", "--gate", "none"},
			nil,
			"refusing --write with --gate none",
		},
		{
			"bad secure-cookies env",
			nil,
			map[string]string{"SEXTANT_SECURE_COOKIES": "maybe"},
			"SECURE_COOKIES",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := Load(tc.args, env(tc.env))
			if err == nil {
				t.Fatal("want error, got nil")
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("error %q does not mention %q", err, tc.want)
			}
		})
	}
}

// validSessionKey is base64 of exactly 32 bytes, satisfying SEXTANT_SESSION_KEY's
// format check so tests can focus on the guard under exercise.
const validSessionKey = "YWFhYWFhYWFhYWFhYWFhYWFhYWFhYWFhYWFhYWFhYWE="

// TestValid exercises the happy paths adjacent to the security-critical
// guards in TestInvalid, so a regression that makes them reject valid
// configuration is caught alongside the negative cases.
func TestValid(t *testing.T) {
	cases := []struct {
		name string
		args []string
		env  map[string]string
	}{
		{
			"oidc on non-loopback with --secure-cookies",
			[]string{
				"--addr", "0.0.0.0:8080",
				"--secure-cookies",
				"--oidc-issuer", "https://idp.example.com",
				"--oidc-client-id", "sextant",
				"--oidc-redirect-url", "https://console.example.com/callback",
			},
			map[string]string{
				"SEXTANT_OIDC_CLIENT_SECRET": "secret",
				"SEXTANT_SESSION_KEY":        validSessionKey,
			},
		},
		{
			"oidc on non-loopback with SEXTANT_SECURE_COOKIES=true",
			[]string{
				"--addr", "0.0.0.0:8080",
				"--oidc-issuer", "https://idp.example.com",
				"--oidc-client-id", "sextant",
				"--oidc-redirect-url", "https://console.example.com/callback",
			},
			map[string]string{
				"SEXTANT_OIDC_CLIENT_SECRET": "secret",
				"SEXTANT_SESSION_KEY":        validSessionKey,
				"SEXTANT_SECURE_COOKIES":     "true",
			},
		},
		{
			"dev-auth on loopback",
			[]string{"--dev-auth", "--addr", "127.0.0.1:8080"},
			nil,
		},
		{
			"write + gate none acknowledged",
			[]string{"--write", "--repo", "/tmp/x", "--gate", "none", "--allow-unvalidated"},
			nil,
		},
		{
			"read-only + gate none needs no acknowledgement",
			[]string{"--gate", "none"},
			nil,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := Load(tc.args, env(tc.env)); err != nil {
				t.Fatalf("want no error, got %v", err)
			}
		})
	}
}

// TestIsLoopback pins the host-matching semantics validate() relies on to
// gate --dev-auth and the secure-cookies guard, including the bracketed
// IPv6 and bare-port forms.
func TestIsLoopback(t *testing.T) {
	cases := []struct {
		addr string
		want bool
	}{
		{"127.0.0.1:8080", true},
		{"[::1]:8080", true},
		{"localhost:8080", true},
		{"localhost", true},
		{"0.0.0.0:8080", false},
		{":8080", false},
		{"example.com:8080", false},
	}
	for _, tc := range cases {
		if got := isLoopback(tc.addr); got != tc.want {
			t.Errorf("isLoopback(%q) = %v, want %v", tc.addr, got, tc.want)
		}
	}
}

// TestEnvBools pins the spelling the refusal message advertises. The message
// for --write with --gate none tells operators to set
// SEXTANT_ALLOW_UNVALIDATED=1; a string compare against "true" made that
// instruction silently wrong, so the console refused to start with exactly
// the environment it asked for.
func TestEnvBools(t *testing.T) {
	for _, v := range []string{"1", "true", "TRUE", "t"} {
		cfg, err := Load([]string{"--write", "--repo", "/tmp/x", "--gate", "none"},
			env(map[string]string{"SEXTANT_ALLOW_UNVALIDATED": v}))
		if err != nil {
			t.Fatalf("ALLOW_UNVALIDATED=%q: %v", v, err)
		}
		if !cfg.AllowUnvalidated {
			t.Errorf("ALLOW_UNVALIDATED=%q did not set the acknowledgement", v)
		}
	}
	for _, v := range []string{"0", "false", "f"} {
		cfg, err := Load(nil, env(map[string]string{"SEXTANT_RELEASE_CACHE": v}))
		if err != nil {
			t.Fatalf("RELEASE_CACHE=%q: %v", v, err)
		}
		if cfg.ReleaseCache {
			t.Errorf("RELEASE_CACHE=%q read as true", v)
		}
	}
	// A typo must be loud: reading it as false would turn an acknowledgement
	// into a refusal, or a kill switch into a no-op.
	if _, err := Load(nil, env(map[string]string{"SEXTANT_DISABLE_DIAGNOSTICS": "yes"})); err == nil {
		t.Error("DISABLE_DIAGNOSTICS=yes: want an error, got none")
	}
}
