package web

import (
	"net/http"
	"sort"
	"strings"

	"code.overheid.nl/MinBZK/DAWO-Sextant/internal/domain/fleet"
	"code.overheid.nl/MinBZK/DAWO-Sextant/internal/domain/identity"
)

// integrations.go: the Integrations surface. Integrations (NetBird, directory
// login, Wazuh) are device-side dawo.* options the overlay publishes in the
// catalog; this page groups those options per integration and configures them
// at the org scope, with secret values delivered as secret references. When an
// overlay has not published an integration's options yet, the card says so
// instead of pretending it is available.

// integration is one known integration and the catalog prefix its options use.
type integration struct {
	Key, Name, Icon, Desc, Prefix string
}

// knownIntegrations are the device-side integrations the console surfaces.
// (Console SSO via Zitadel is configured at deploy time, not per-fleet, so it
// is not listed here.)
var knownIntegrations = []integration{
	{"netbird", "NetBird VPN", "vpn_lock",
		"Zero-config mesh VPN. Devices join with a setup key delivered as a secret reference.", "netbird."},
	{"identity", "Directory login (LDAP)", "badge",
		"Device login against your directory (SSSD/LDAP): domain, server and a bind secret.", "identity."},
	{"wazuh", "Wazuh SIEM", "security",
		"Endpoint security agent reporting to a Wazuh manager.", "wazuh."},
	{"openbao", "OpenBao vault", "encrypted",
		"Central secret store (open-source Vault). Devices fetch named secrets over the API; rotation needs no configuration change.", "openbao."},
	{"osquery", "Fleet inventory (osquery)", "inventory_2",
		"Reports this device to a Fleet server so one console covers the whole estate, whatever it runs. The device enrols itself and answers queries; nothing is pushed back at it, and configuration stays here. Note that osquery cannot see the Nix store, so software inventory is empty for now.", "osquery."},
	{"localAdmin", "Local admin account", "admin_panel_settings",
		"An administrator who can sign in when the directory or the network is unreachable. Choose the name; the password is delivered as a secret reference, so no credential is shared between fleets. Off locks the account on every device.", "localAdmin."},
}

// isIntegrationSetting reports whether a catalog key belongs to an
// integration. Those options live on the Integrations page (a card each);
// the general Settings editor hides them so a key is configured in exactly
// one place, never both.
func isIntegrationSetting(key string) bool {
	for _, ig := range knownIntegrations {
		if strings.HasPrefix(key, ig.Prefix) {
			return true
		}
	}
	return false
}

// integrationsPage renders one card per integration, at a chosen scope
// (?scope=org|group:<name>, default org - an integration is often wanted for
// one group first, then widened). Viewer at the scope to see; the card forms
// post to the settings editor, which enforces Editor at the scope.
func (s *Server) integrationsPage(w http.ResponseWriter, r *http.Request, v view) {
	scope := r.URL.Query().Get("scope")
	if scope == "" {
		scope = "org"
	}
	if err := s.requireWeb(v, scope, identity.Viewer); err != nil {
		http.Error(w, err.Error(), http.StatusForbidden)
		return
	}
	f, cat := s.svc.Config.Snapshot()
	own, _, err := f.ScopeSettings(scope)
	if err != nil {
		http.NotFound(w, r)
		return
	}

	type optRow struct {
		Entry fleet.CatalogEntry
		Value string
		// Lines is a list value rendered one item per line, for the same
		// textarea the settings editor uses. Without it a list fell through to
		// this page's single-line input showing `["a","b"]` (renderValue's JSON),
		// and saving the card - which submits every row at once - reparsed that
		// as a ONE-element list holding the literal text. One of the three
		// list-typed settings is usbDevices.allowlist, so the silent case was a
		// security control, and this is the same defect RawFromValue records
		// having fixed on the API side.
		Lines string
		Set   bool
	}
	type card struct {
		integration
		Published bool
		Rows      []optRow
	}
	cards := make([]card, 0, len(knownIntegrations))
	for _, ig := range knownIntegrations {
		c := card{integration: ig}
		for _, e := range cat.Entries {
			if !strings.HasPrefix(e.Name, ig.Prefix) {
				continue
			}
			row := optRow{Entry: e}
			if val, has := own[e.Name]; has {
				row.Set, row.Value = true, renderValue(val)
				row.Lines = valueLines(val)
			}
			c.Rows = append(c.Rows, row)
		}
		// Gate first, then alphabetical within its family: a card that led with
		// options reading "takes effect once X.enable is on" and put X.enable
		// last is asking the operator to read bottom-up.
		sort.SliceStable(c.Rows, func(i, j int) bool {
			gi, gj := cat.Requires(c.Rows[i].Entry.Name), cat.Requires(c.Rows[j].Entry.Name)
			if gi != gj {
				return gi < gj
			}
			return c.Rows[i].Entry.Name < c.Rows[j].Entry.Name
		})
		c.Published = len(c.Rows) > 0
		cards = append(cards, c)
	}

	secretRefs := make([]string, 0, len(f.SecretRefs))
	for name := range f.SecretRefs {
		secretRefs = append(secretRefs, name)
	}
	sort.Strings(secretRefs)

	// Scope drill-down: the groups this user may view, for the selector.
	groups := make([]string, 0, len(f.Groups))
	for g := range f.Groups {
		if v.canView("group:" + g) {
			groups = append(groups, g)
		}
	}
	sort.Strings(groups)
	selGroup, _ := strings.CutPrefix(scope, "group:")
	if scope == "org" {
		selGroup = ""
	}

	s.render(w, "integrations", map[string]any{
		"Title": "Integrations", "Nav": "integrations",
		"PickerBase": "/integrations",
		"Cards":      cards,
		"SecretRefs": secretRefs,
		// Pending: a just-saved card detached to background validation; the
		// page says so and refreshes once instead of showing stale values.
		"Pending":  r.URL.Query().Get("pending") == "1",
		"Scope":    scope,
		"SelGroup": selGroup,
		"Groups":   groups,
		"IsOrg":    scope == "org",
		"CanEdit":  v.roleAt(scope).Meets(identity.Editor),
	}, v)
}
