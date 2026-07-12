package fleet

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

// hardware.go: the hardware-profile vocabulary and the specs an imaging
// station captures. A hardware profile is a named build target the generator
// knows (a device's `hardware` field references a key in the overlay's
// hardwareProfiles attrset). hardware-profiles.json - authored in the overlay
// next to catalog.json - adds the operator-facing METADATA the console needs:
// the brand, the models that map to the profile (so a discovered device
// suggests its own profile), and the brand-specific steps to walk an operator
// through imaging (firmware differs per make). Sextant only reads this; the
// nix modules themselves stay in the overlay.

// HardwareProfilesFile is the metadata file's path inside the overlay repo.
const HardwareProfilesFile = "hardware-profiles.json"

// ImagingStep is one operator-facing step in the guided imaging flow. Key is
// an optional firmware-key hint (e.g. "F12", "Enter") shown as a badge, since
// entering firmware/boot-menu differs per make.
type ImagingStep struct {
	Title  string `json:"title"`
	Detail string `json:"detail,omitempty"`
	Key    string `json:"key,omitempty"`
}

// HardwareProfile is one imaging target plus the guidance to image a machine
// of this kind. Name matches a key in the generator's hardwareProfiles.
type HardwareProfile struct {
	Name   string        `json:"name"`
	Vendor string        `json:"vendor,omitempty"` // brand, e.g. "Lenovo", "HP"
	Models []string      `json:"models,omitempty"` // model substrings mapping here
	Notes  string        `json:"notes,omitempty"`
	Disko  string        `json:"disko,omitempty"` // human note of the disk layout
	Steps  []ImagingStep `json:"steps,omitempty"` // ordered, brand-specific
}

// HardwareProfiles is the parsed, indexed hardware-profile catalog.
type HardwareProfiles struct {
	byName map[string]HardwareProfile
	order  []string // profile names, sorted, for stable rendering
}

// ParseHardwareProfiles reads hardware-profiles.json. Empty input yields an
// empty (never nil) set: an overlay that predates the export is valid, the
// console simply offers no profile suggestions. A malformed or duplicate
// document is rejected so the imaging surface never silently drops a profile.
func ParseHardwareProfiles(raw []byte) (*HardwareProfiles, error) {
	hp := &HardwareProfiles{byName: map[string]HardwareProfile{}}
	if len(raw) == 0 {
		return hp, nil
	}
	var list []HardwareProfile
	if err := json.Unmarshal(raw, &list); err != nil {
		return nil, fmt.Errorf("parse %s: %w", HardwareProfilesFile, err)
	}
	for _, p := range list {
		if p.Name == "" {
			return nil, fmt.Errorf("%s: a profile has no name", HardwareProfilesFile)
		}
		if _, dup := hp.byName[p.Name]; dup {
			return nil, fmt.Errorf("%s: duplicate profile %q", HardwareProfilesFile, p.Name)
		}
		hp.byName[p.Name] = p
		hp.order = append(hp.order, p.Name)
	}
	sort.Strings(hp.order)
	return hp, nil
}

// All returns the profiles in stable name order.
func (h *HardwareProfiles) All() []HardwareProfile {
	out := make([]HardwareProfile, 0, len(h.order))
	for _, n := range h.order {
		out = append(out, h.byName[n])
	}
	return out
}

// Names returns the profile names in stable order (the enroll dropdown).
func (h *HardwareProfiles) Names() []string {
	out := make([]string, len(h.order))
	copy(out, h.order)
	return out
}

// Get returns one profile by name.
func (h *HardwareProfiles) Get(name string) (HardwareProfile, bool) {
	p, ok := h.byName[name]
	return p, ok
}

// Has reports whether a profile name is known.
func (h *HardwareProfiles) Has(name string) bool {
	_, ok := h.byName[name]
	return ok
}

// Len is the number of known profiles.
func (h *HardwareProfiles) Len() int { return len(h.order) }

// Suggest maps a discovered device's vendor/model onto a profile name, so the
// station pre-selects the right hardware profile instead of making the
// operator know it. It matches a profile whose Models list contains a
// case-insensitive substring of the reported model (most specific - longest
// match - wins); vendor is a tiebreaker. Returns "" when nothing matches.
func (h *HardwareProfiles) Suggest(vendor, model string) string {
	model = strings.ToLower(strings.TrimSpace(model))
	vendor = strings.ToLower(strings.TrimSpace(vendor))
	best, bestLen := "", 0
	for _, name := range h.order {
		p := h.byName[name]
		for _, m := range p.Models {
			m = strings.ToLower(strings.TrimSpace(m))
			if m == "" || model == "" || !strings.Contains(model, m) {
				continue
			}
			score := len(m)
			if vendor != "" && strings.EqualFold(p.Vendor, vendor) {
				score++ // vendor agreement breaks ties toward the right brand
			}
			if score > bestLen {
				best, bestLen = name, score
			}
		}
	}
	return best
}

// HardwareSpec is the hardware fingerprint an imaging station captured for a
// device (nixos-facter-derived), stored on the device record for inventory
// and audit. It is data, not config: the generator never reads it.
type HardwareSpec struct {
	Vendor   string `json:"vendor,omitempty"`
	Model    string `json:"model,omitempty"`
	Serial   string `json:"serial,omitempty"`
	CPU      string `json:"cpu,omitempty"`
	Cores    int    `json:"cores,omitempty"`
	MemGB    int    `json:"memGB,omitempty"`
	DiskGB   int    `json:"diskGB,omitempty"`
	Firmware string `json:"firmware,omitempty"`
}

// Empty reports whether the spec carries nothing worth storing.
func (s HardwareSpec) Empty() bool {
	return s == HardwareSpec{}
}
