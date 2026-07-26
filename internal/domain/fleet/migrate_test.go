package fleet

import (
	"strings"
	"testing"
)

// TestDecodeCurrentVersionRoundTrips: a v3 document decodes unchanged through
// the migration path (no migration runs).
func TestDecodeCurrentVersionRoundTrips(t *testing.T) {
	f, err := Decode([]byte(`{"version":3,"org":{"settings":{"desktop":"gnome"}},
	  "groups":{"pilot":{}},"devices":{"lt-1":{"groups":["pilot"]}}}`))
	if err != nil {
		t.Fatal(err)
	}
	if f.Version != Version || f.Org.Settings["desktop"] != "gnome" {
		t.Fatalf("round-trip mangled: %+v", f)
	}
}

// TestDecodeRejectsNewerVersion: a document from a future build is refused,
// not misread.
func TestDecodeRejectsNewerVersion(t *testing.T) {
	_, err := Decode([]byte(`{"version":999,"groups":{},"devices":{}}`))
	if err == nil || !strings.Contains(err.Error(), "newer than this build") {
		t.Fatalf("want newer-version rejection, got %v", err)
	}
}

// TestDecodeRejectsBadVersion: version 0 / missing is unsupported.
func TestDecodeRejectsBadVersion(t *testing.T) {
	if _, err := Decode([]byte(`{"groups":{},"devices":{}}`)); err == nil {
		t.Fatal("want rejection of version 0")
	}
}

// TestMigrationPathUpgradesForward exercises the mechanism with a synthetic
// registry: a v2-shaped document is migrated to the current version, proving
// a future v3->v4 step will upgrade existing documents on read. The synthetic
// migration renames an old field into the current schema.
func TestMigrationPathUpgradesForward(t *testing.T) {
	// Pretend v2 stored the org desktop under a legacy key; the migration
	// moves it into org.settings.desktop.
	migs := map[int]migration{
		2: func(doc map[string]any) (map[string]any, error) {
			org, _ := doc["org"].(map[string]any)
			if org == nil {
				org = map[string]any{}
			}
			settings, _ := org["settings"].(map[string]any)
			if settings == nil {
				settings = map[string]any{}
			}
			if legacy, ok := doc["legacyDesktop"]; ok {
				settings["desktop"] = legacy
				delete(doc, "legacyDesktop")
			}
			org["settings"] = settings
			doc["org"] = org
			return doc, nil
		},
	}
	v2 := []byte(`{"version":2,"legacyDesktop":"plasma","groups":{"g":{}},"devices":{}}`)
	f, err := decode(v2, migs)
	if err != nil {
		t.Fatal(err)
	}
	if f.Version != Version {
		t.Fatalf("version after migration = %d, want %d", f.Version, Version)
	}
	if f.Org.Settings["desktop"] != "plasma" {
		t.Fatalf("migration did not carry the value forward: %+v", f.Org.Settings)
	}
}

// TestMigrationMissingStepFails: a gap in the registry is a loud error, not a
// silent half-migration.
func TestMigrationMissingStepFails(t *testing.T) {
	v1 := []byte(`{"version":1,"groups":{},"devices":{}}`)
	_, err := decode(v1, map[int]migration{}) // no 1->2 step
	if err == nil || !strings.Contains(err.Error(), "no migration from fleet version 1") {
		t.Fatalf("want missing-step error, got %v", err)
	}
}
