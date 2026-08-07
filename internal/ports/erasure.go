package ports

import "context"

// ErasureStore finds and removes one person's records (GDPR art. 17).
//
// TWO IDENTIFIERS, ON PURPOSE. A person appears in this system under at
// least two names that do not have to match: the OIDC subject for anything
// they did in the console, and their OS username for anything their device
// reported. Measured on 2026-08-07: seen_users.subject is a numeric IdP id
// while elevation_requests.user is a login name. An erasure that takes only
// one of them leaves the other behind and reports success, which is the
// worst possible outcome for this particular operation.
//
// So both travel, always, and the counts come back per identifier so the
// caller can see which one matched nothing and ask why.
type ErasureStore interface {
	// CountPersonalData reports what is held, without removing anything.
	CountPersonalData(ctx context.Context, tenant, subject, username string) (PersonalDataCounts, error)
	// ErasePersonalData removes it and reports what went.
	ErasePersonalData(ctx context.Context, tenant, subject, username string) (PersonalDataCounts, error)
}

// PersonalDataCounts is what one person has in the observed plane.
type PersonalDataCounts struct {
	// SeenUser is the cached console identity: subject, e-mail, display
	// name, group memberships.
	SeenUser int
	// Prefs is their console preferences.
	Prefs int
	// Notifications addressed to them personally. Notifications addressed to
	// a GROUP are not personal data about them and are not counted.
	Notifications int
	// Elevation requests they raised - matched on the username, and the
	// reason field is free text they wrote themselves.
	Elevation int
	// ElevationDecided is requests they DECIDED rather than raised. Removing
	// those would erase somebody else's record of who approved their access,
	// so they are counted separately and never erased by this path.
	ElevationDecided int
}

// Total is what would actually be removed.
func (c PersonalDataCounts) Total() int {
	return c.SeenUser + c.Prefs + c.Notifications + c.Elevation
}
