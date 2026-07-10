package fleet

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

// catalog.go: the settings vocabulary (ADR 0005). catalog.json is generated
// from the overlay's documented dawo.* options (nix/export-catalog.nix) and
// committed to the overlay repo, so it versions with the configuration it
// describes. The console renders its settings surface entirely from these
// entries: no hand-built toggles, no UI code per option.

// CatalogFile is the catalog's path inside the overlay repo.
const CatalogFile = "catalog.json"

// CatalogEntry describes one settable option.
type CatalogEntry struct {
	// Name is the dotted option path under dawo. (e.g. "apps.office").
	Name string `json:"name"`
	// Type is the nix type description ("boolean", "string", "one of ...",
	// "positive integer", ...). Widget derives it.
	Type string `json:"type"`
	// Description is the human explanation shown next to the control.
	Description string `json:"description"`
	// Default is the option's declared default, when JSON-representable.
	// Shown as the inherited value hint; nil means "no renderable default".
	Default any `json:"default,omitempty"`
	// RiskClass flags options whose change deserves extra care ("high" by
	// convention). The console renders it as a warning badge; approval
	// flows may key off it later.
	RiskClass string `json:"riskClass,omitempty"`
}

// DefaultString renders the declared default for display; empty when none.
func (e CatalogEntry) DefaultString() string {
	if e.Default == nil {
		return ""
	}
	b, err := json.Marshal(e.Default)
	if err != nil {
		return ""
	}
	return string(b)
}

// Widget classifies the input control an entry needs.
type Widget string

const (
	WidgetToggle Widget = "toggle" // booleans
	WidgetNumber Widget = "number" // integers
	WidgetSelect Widget = "select" // enums ("one of ...")
	WidgetText   Widget = "text"   // everything else
)

// Widget derives the control from the nix type description.
func (e CatalogEntry) Widget() Widget {
	t := strings.ToLower(e.Type)
	switch {
	case t == "boolean":
		return WidgetToggle
	case strings.Contains(t, "integer"):
		return WidgetNumber
	case strings.HasPrefix(t, "one of"):
		return WidgetSelect
	default:
		return WidgetText
	}
}

// Options lists the allowed values for a select entry. Nix renders enums
// as `one of "a", "b", "c"`.
func (e CatalogEntry) Options() []string {
	t := strings.TrimPrefix(e.Type, "one of ")
	if t == e.Type {
		return nil
	}
	var out []string
	for _, part := range strings.Split(t, ",") {
		v := strings.Trim(strings.TrimSpace(part), `"`)
		if v != "" {
			out = append(out, v)
		}
	}
	return out
}

// Category groups entries by their first path segment ("apps", "ssh"),
// which becomes a section in the UI. Top-level options group under
// "general".
func (e CatalogEntry) Category() string {
	if i := strings.IndexByte(e.Name, '.'); i > 0 {
		return e.Name[:i]
	}
	return "general"
}

// ParseValue converts a submitted string to the entry's typed value; the
// gate remains the final validator, this keeps obvious mistakes local.
func (e CatalogEntry) ParseValue(s string) (any, error) {
	switch e.Widget() {
	case WidgetToggle:
		switch s {
		case "true", "on":
			return true, nil
		case "false", "off", "":
			return false, nil
		}
		return nil, fmt.Errorf("%s expects true or false", e.Name)
	case WidgetNumber:
		var n int
		if _, err := fmt.Sscanf(s, "%d", &n); err != nil || fmt.Sprint(n) != s {
			return nil, fmt.Errorf("%s expects an integer", e.Name)
		}
		return n, nil
	case WidgetSelect:
		for _, opt := range e.Options() {
			if s == opt {
				return s, nil
			}
		}
		return nil, fmt.Errorf("%s must be one of %v", e.Name, e.Options())
	default:
		return s, nil
	}
}

// Catalog is the parsed settings vocabulary, lookup-ready.
type Catalog struct {
	Entries []CatalogEntry
	byName  map[string]CatalogEntry
}

// ParseCatalog reads catalog.json bytes. An empty or missing catalog is
// valid (the UI shows a hint instead of a settings surface).
func ParseCatalog(b []byte) (*Catalog, error) {
	var entries []CatalogEntry
	if len(b) > 0 {
		if err := json.Unmarshal(b, &entries); err != nil {
			return nil, fmt.Errorf("parse %s: %w", CatalogFile, err)
		}
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].Name < entries[j].Name })
	c := &Catalog{Entries: entries, byName: make(map[string]CatalogEntry, len(entries))}
	for _, e := range entries {
		c.byName[e.Name] = e
	}
	return c, nil
}

// Lookup returns the entry for a setting key.
func (c *Catalog) Lookup(name string) (CatalogEntry, bool) {
	e, ok := c.byName[name]
	return e, ok
}

// Categories returns the section names in display order.
func (c *Catalog) Categories() []string {
	seen := map[string]bool{}
	var out []string
	for _, e := range c.Entries {
		if cat := e.Category(); !seen[cat] {
			seen[cat] = true
			out = append(out, cat)
		}
	}
	sort.Strings(out)
	return out
}

// ByCategory returns the entries of one section, name-sorted.
func (c *Catalog) ByCategory(cat string) []CatalogEntry {
	var out []CatalogEntry
	for _, e := range c.Entries {
		if e.Category() == cat {
			out = append(out, e)
		}
	}
	return out
}
