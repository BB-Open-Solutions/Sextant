package fleet

import (
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
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
	// Label is the optional human name the console shows instead of the raw
	// dotted path ("LUKS mapper" for diskUnlock.tpm2.device); the path stays
	// visible as the technical identity. Authored in the overlay via the
	// `// { label = "..."; }` annotation - Name remains the API key.
	Label string `json:"label,omitempty"`
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
	// Secret marks an option whose value is a SECRET (a NetBird setup key, a
	// bind password). Sextant never stores the secret itself: the setting
	// holds a reference to a named secret in the store (agenix on the device,
	// a Secret in the cluster), which the generator resolves to a path. The
	// console renders a reference picker, never a text box, so a secret can
	// never be pasted into git.
	Secret bool `json:"secret,omitempty"`
	// Classes lists the device classes whose image defines this option
	// (derived by the per-class catalog export - never hand-maintained, so it
	// cannot drift from the images). Empty means universal: every class has
	// it. The generator skips a setting for a device whose class is absent
	// here, so a workplace-only option (a desktop environment) set at a scope
	// that also covers headless machines configures the laptops and visibly
	// skips the servers instead of failing their evaluation.
	Classes []string `json:"classes,omitempty"`
}

// AppliesTo reports whether the entry applies to a device of the given
// class. An empty Classes list is universal. An empty class applies
// everything: filtering only happens on a known class, so a device without
// one fails loudly at the gate (image lacks the option) rather than being
// silently under-configured. Mirrors the generator's rule (generator.nix,
// applicableTo) - the parity the mixed-class tests pin down.
func (e CatalogEntry) AppliesTo(class string) bool {
	if len(e.Classes) == 0 || class == "" {
		return true
	}
	for _, c := range e.Classes {
		if c == class {
			return true
		}
	}
	return false
}

// DisplayName is what the console leads with: the human label when the
// overlay authored one, else the dotted path.
func (e CatalogEntry) DisplayName() string {
	if e.Label != "" {
		return e.Label
	}
	return e.Name
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

// The catalog-entry widget kinds.
const (
	WidgetToggle Widget = "toggle" // booleans
	WidgetNumber Widget = "number" // integers
	WidgetSelect Widget = "select" // enums ("one of ...")
	WidgetText   Widget = "text"   // everything else
	WidgetSecret Widget = "secret" // secret-reference picker (never a value)
	WidgetCode   Widget = "code"   // list-valued: a multi-line editor, one item per line
)

// Widget derives the control from the nix type description. A secret option
// always renders as a reference picker, whatever its underlying type, so its
// value is never typed into the console.
func (e CatalogEntry) Widget() Widget {
	if e.Secret {
		return WidgetSecret
	}
	t := strings.ToLower(e.Type)
	switch {
	case t == "boolean":
		return WidgetToggle
	case strings.Contains(t, "integer"):
		return WidgetNumber
	case strings.HasPrefix(t, "one of"):
		return WidgetSelect
	case strings.HasPrefix(t, "list of"):
		// A list needs a multi-line editor so it stores a real array, not a
		// single string the gate would reject on type.
		return WidgetCode
	default:
		return WidgetText
	}
}

// Options lists the allowed values for a select entry. Nix renders enums
// as `one of "a", "b", "c"`. Prefix detection matches Widget's
// (case-insensitive), so a select never renders with zero options.
func (e CatalogEntry) Options() []string {
	const prefix = "one of "
	if !strings.HasPrefix(strings.ToLower(e.Type), prefix) {
		return nil
	}
	t := e.Type[len(prefix):]
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
	case WidgetSecret:
		// A secret setting stores a REFERENCE to a named secret, never the
		// secret itself. Accept only a reference slug, so a raw secret can
		// never be pasted in and committed to git.
		if s != "" && !ValidateSlug(s) {
			return nil, fmt.Errorf("%s takes a secret reference name, not a value", e.Name)
		}
		return s, nil
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
		// Honor the range the nix type states, so the obvious mistake fails
		// here instead of minutes later at the gate.
		t := strings.ToLower(e.Type)
		if strings.Contains(t, "positive") && n <= 0 {
			return nil, fmt.Errorf("%s expects a positive integer", e.Name)
		}
		if (strings.Contains(t, "unsigned") || strings.Contains(t, "non-negative")) && n < 0 {
			return nil, fmt.Errorf("%s expects a non-negative integer", e.Name)
		}
		return n, nil
	case WidgetSelect:
		for _, opt := range e.Options() {
			if s == opt {
				return s, nil
			}
		}
		return nil, fmt.Errorf("%s must be one of %v", e.Name, e.Options())
	case WidgetCode:
		// A list, one item per line (blank lines dropped). Stored as a real
		// array so the generator emits a nix list; the gate still type-checks
		// the elements.
		out := []string{}
		for _, line := range strings.Split(s, "\n") {
			if v := strings.TrimSpace(line); v != "" {
				out = append(out, v)
			}
		}
		if len(out) == 0 {
			return nil, fmt.Errorf("%s expects at least one item, one per line", e.Name)
		}
		return out, nil
	default:
		return s, nil
	}
}

// RawFromValue renders an already-typed value - one the API decoded from JSON -
// into the string form ParseValue expects, so both transports land in the same
// validator instead of each having its own idea of what a value is.
//
// WHY IT EXISTS. The API used fmt.Sprint for this, which looked like it worked
// and did not. A JSON list arrived as Go's "[a b]" and ParseValue, splitting on
// newlines, turned it into a ONE-element list holding that text. The result is
// a valid list of string, so the gate passed it and the device was configured
// with nonsense. Three settings are list-typed and one of them is
// usbDevices.allowlist, so the silent case was a security control.
//
// Numbers failed more loudly but no more correctly: JSON has a single number
// type, so 1000000 decodes to float64 and fmt.Sprint renders it "1e+06", which
// the integer check then rejects as not an integer.
func (e CatalogEntry) RawFromValue(v any) (string, error) {
	switch t := v.(type) {
	case nil:
		return "", nil
	case string:
		return t, nil
	case bool:
		return strconv.FormatBool(t), nil
	case float64:
		// 'f' with -1 precision: never an exponent, and no trailing zeros, so
		// an integer stays spelled as one and a genuine fraction survives to be
		// rejected by the integer check rather than silently truncated here.
		return strconv.FormatFloat(t, 'f', -1, 64), nil
	case []any:
		out := make([]string, 0, len(t))
		for i, el := range t {
			s, ok := el.(string)
			if !ok {
				return "", fmt.Errorf("%s: item %d is %T, expected text", e.Name, i+1, el)
			}
			// The list form is one item per line, so an item containing a line
			// break cannot round-trip. Say so instead of silently splitting it
			// into two items.
			if strings.ContainsAny(s, "\n\r") {
				return "", fmt.Errorf("%s: item %d contains a line break, which a list value cannot hold", e.Name, i+1)
			}
			out = append(out, s)
		}
		return strings.Join(out, "\n"), nil
	default:
		return "", fmt.Errorf("%s: cannot take a value of type %T", e.Name, v)
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
		// A duplicate name means the rendered row and the POST-time lookup
		// could disagree on type and options; refuse the whole catalog
		// rather than serve a UI that lies.
		if _, dup := c.byName[e.Name]; dup {
			return nil, fmt.Errorf("parse %s: duplicate entry %q", CatalogFile, e.Name)
		}
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
