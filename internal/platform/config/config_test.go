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
