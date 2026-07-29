package app

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"code.overheid.nl/MinBZK/DAWO-Sextant/internal/domain/fleet"
	"code.overheid.nl/MinBZK/DAWO-Sextant/internal/ports"
)

// lockWith wraps a dawo node body in a minimal but realistic flake.lock.
func lockWith(nodes string) string {
	return `{"nodes":{` + nodes + `,"root":{"inputs":{"dawo":"dawo","nixpkgs":"nixpkgs"}}},"root":"root","version":7}`
}

func TestParseFlakeLock(t *testing.T) {
	cases := []struct {
		name     string
		raw      string
		wantOK   bool
		wantRev  string
		wantURL  string
		wantTime time.Time
	}{
		{
			name: "github pin",
			raw: lockWith(`"dawo":{"locked":{"lastModified":1750636800,"owner":"MinBZK","repo":"DAWO",` +
				`"rev":"0f1e2d3c4b5a69788796a5b4c3d2e1f001234567","type":"github"}}`),
			wantOK: true, wantRev: "0f1e2d3c4b5a69788796a5b4c3d2e1f001234567",
			wantURL: "github:MinBZK/DAWO", wantTime: time.Unix(1750636800, 0).UTC(),
		},
		{
			name: "git url pin keeps its url",
			raw: lockWith(`"dawo":{"locked":{"lastModified":1750636800,"type":"git",` +
				`"url":"https://code.overheid.nl/MinBZK/DAWO","rev":"abc123"}}`),
			wantOK: true, wantRev: "abc123", wantURL: "https://code.overheid.nl/MinBZK/DAWO",
			wantTime: time.Unix(1750636800, 0).UTC(),
		},
		{
			// The input name is taken, so nix stores the node as dawo_2 and the
			// root node's mapping is the only way to find it.
			name: "renamed node resolved through root inputs",
			raw: `{"nodes":{"dawo_2":{"locked":{"rev":"deadbeef","type":"git","url":"u"}},` +
				`"root":{"inputs":{"dawo":"dawo_2"}}},"root":"root","version":7}`,
			wantOK: true, wantRev: "deadbeef", wantURL: "u",
		},
		{
			// An overlay that pins no core: legal, simply has no version.
			name:   "no dawo node",
			raw:    `{"nodes":{"nixpkgs":{"locked":{"rev":"aaa"}},"root":{"inputs":{"nixpkgs":"nixpkgs"}}},"root":"root","version":7}`,
			wantOK: false,
		},
		{
			name:   "node without a rev",
			raw:    lockWith(`"dawo":{"locked":{"lastModified":1750636800,"type":"github","owner":"MinBZK","repo":"DAWO"}}`),
			wantOK: false,
		},
		{
			name:   "malformed json",
			raw:    `{"nodes":{"dawo":`,
			wantOK: false,
		},
		{
			name:   "empty file",
			raw:    "",
			wantOK: false,
		},
		{
			// No lastModified: the pin is still a pin, just undated.
			name:   "undated pin",
			raw:    lockWith(`"dawo":{"locked":{"rev":"cafe","type":"git","url":"u"}}`),
			wantOK: true, wantRev: "cafe", wantURL: "u",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := parseFlakeLock([]byte(tc.raw))
			if ok != tc.wantOK {
				t.Fatalf("ok = %v, want %v (got %+v)", ok, tc.wantOK, got)
			}
			if !ok {
				return
			}
			if got.Rev != tc.wantRev || got.URL != tc.wantURL || !got.Modified.Equal(tc.wantTime) {
				t.Errorf("got %+v, want rev %q url %q modified %v", got, tc.wantRev, tc.wantURL, tc.wantTime)
			}
		})
	}
}

// TestCoreVersionReadsOverlayAndFollowsTheSnapshot: a missing flake.lock is
// simply no version, and the answer is memoised until the working tree moves.
func TestCoreVersionReadsOverlayAndFollowsTheSnapshot(t *testing.T) {
	svc, dir := newService(t, nil)

	if cv, ok := svc.CoreVersion(); ok {
		t.Fatalf("overlay without flake.lock reported a core version: %+v", cv)
	}
	lock := lockWith(`"dawo":{"locked":{"lastModified":1750636800,"owner":"MinBZK","repo":"DAWO","rev":"feedface","type":"github"}}`)
	if err := os.WriteFile(filepath.Join(dir, FlakeLockFile), []byte(lock), 0o644); err != nil {
		t.Fatal(err)
	}
	// The read is cached against the snapshot, so the answer only changes once
	// a write republishes it - the point of not re-reading on every render.
	if _, ok := svc.CoreVersion(); ok {
		t.Error("cached answer ignored: flake.lock re-read without a snapshot change")
	}
	if err := svc.Apply(context.Background(),
		fleet.SetScopeSetting("group:pilot", "apps.office", true),
		"settings: office on for pilot", ports.Author{Name: "t"}, "lt-1"); err != nil {
		t.Fatal(err)
	}
	cv, ok := svc.CoreVersion()
	if !ok || cv.Rev != "feedface" || cv.URL != "github:MinBZK/DAWO" {
		t.Fatalf("core version after a write = %+v (ok %v)", cv, ok)
	}
}
