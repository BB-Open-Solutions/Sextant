package fleet

import "testing"

func TestAddStation(t *testing.T) {
	f := &Fleet{Version: Version}
	if err := AddStation("dawo-inspoelstraat", Station{Description: "meterkast NUC"})(f); err != nil {
		t.Fatal(err)
	}
	if f.Stations["dawo-inspoelstraat"].Description != "meterkast NUC" {
		t.Fatal("station not registered")
	}
	// Duplicate rejected.
	if err := AddStation("dawo-inspoelstraat", Station{})(f); err == nil {
		t.Fatal("duplicate station accepted")
	}
	// A non-slug tag is rejected (it becomes an auth subject and a URL path).
	if err := AddStation("Not A Slug", Station{})(f); err == nil {
		t.Fatal("invalid station tag accepted")
	}
}

func TestRemoveStation(t *testing.T) {
	f := &Fleet{Version: Version, Stations: map[string]Station{"nuc-1": {}}}
	if err := RemoveStation("nuc-1")(f); err != nil {
		t.Fatal(err)
	}
	if _, ok := f.Stations["nuc-1"]; ok {
		t.Fatal("station not removed")
	}
	if err := RemoveStation("nuc-1")(f); err == nil {
		t.Fatal("removing an unknown station accepted")
	}
}
