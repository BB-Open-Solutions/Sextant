package fleet

import "fmt"

// station.go: mutations for the imaging-station registry (the inspoelstraat).
// Registering a station is config-as-data - an audited git commit - so the
// console can offer it as a choice and mint its report credential.

// AddStation registers an imaging station under tag.
func AddStation(tag string, s Station) Mutation {
	return func(f *Fleet) error {
		if !ValidateSlug(tag) {
			return fmt.Errorf("invalid station tag %q (lowercase slug required)", tag)
		}
		if _, exists := f.Stations[tag]; exists {
			return fmt.Errorf("station %q already exists", tag)
		}
		if f.Stations == nil {
			f.Stations = map[string]Station{}
		}
		f.Stations[tag] = s
		return nil
	}
}

// RemoveStation unregisters an imaging station. Discovered devices live in the
// observed store, not the config, so nothing in the fleet dangles; the caller
// revokes the station's credential separately.
func RemoveStation(tag string) Mutation {
	return func(f *Fleet) error {
		if _, ok := f.Stations[tag]; !ok {
			return fmt.Errorf("unknown station %q", tag)
		}
		delete(f.Stations, tag)
		return nil
	}
}
