// Package fleet is the pure domain model of a Sextant fleet: the
// config-as-data document (fleet.json), the policy/assignment/filter model,
// and scope resolution with provenance. It performs no I/O; persistence and
// the nix gate live behind ports in the application layer.
package fleet

import (
	"encoding/json"
	"fmt"
)

// Version is the fleet.json schema version this domain reads and writes.
const Version = 3

// Fleet mirrors fleet.json, the group-first config-as-data of one
// organisation's overlay repo.
type Fleet struct {
	Version int    `json:"version"`
	Org     *Scope `json:"org,omitempty"`

	Groups  map[string]Group  `json:"groups,omitempty"`
	Devices map[string]Device `json:"devices,omitempty"`

	// Stations are the imaging stations (the inspoelstraat) that discover
	// devices over PXE and report them. Registering one here is what lets the
	// console offer it as a choice and mint its report credential - so an
	// operator never types a station tag from memory.
	Stations map[string]Station `json:"stations,omitempty"`

	// SecretRefs registers the NAMES of secrets that exist in the secret
	// store (agenix on devices, a Secret in the cluster). A secret-typed
	// setting stores one of these names, never the secret value; the console
	// offers them as a picker. Sextant only ever knows the names.
	SecretRefs map[string]SecretRef `json:"secretRefs,omitempty"`

	// Policies are reusable named setting bundles; Assignments bind them to
	// scopes; Filters narrow assignments to matching devices. See resolve.go
	// for the precedence rule.
	Policies    map[string]Policy `json:"policies,omitempty"`
	Assignments []Assignment      `json:"assignments,omitempty"`
	Filters     map[string]Filter `json:"filters,omitempty"`

	// Access grants roles at scopes to IdP groups (see domain/identity).
	// Config-as-data: every access change is a gated, audited git commit.
	Access []AccessBinding `json:"access,omitempty"`

	Rollout *RolloutPolicy `json:"rollout,omitempty"`

	// Assurance holds the organisation's control settings (ADR 0007).
	Assurance *Assurance `json:"assurance,omitempty"`
}

// Station is a registered imaging station. The map key is its tag (the
// station reports and authenticates as this); Description and Site are
// operator-facing labels shown in the console.
type Station struct {
	Description string `json:"description,omitempty"`
	Site        string `json:"site,omitempty"`
}

// Assurance configures audit controls. RequireFourEyes rejects merging a
// change by its own author (segregation of duties).
type Assurance struct {
	RequireFourEyes bool `json:"requireFourEyes,omitempty"`
}

// Scope carries the settings set directly at a scope plus the subset of keys
// it enforces (locks for more specific scopes). Setting keys are option paths
// from the catalog (e.g. "apps.office", "secureboot.enable"); values are
// typed per the catalog.
type Scope struct {
	Settings map[string]any `json:"settings,omitempty"`
	Enforced []string       `json:"enforced,omitempty"`
	// Packages, Flatpaks and Overlays are additive across the scope chain
	// (unioned org+groups+device), unlike settings. They name things to look
	// up (nixpkgs attrs, flathub ids, repo-defined overlays), never code.
	Packages []string `json:"packages,omitempty"`
	Flatpaks []string `json:"flatpaks,omitempty"`
	Overlays []string `json:"overlays,omitempty"`
	// Accepted is the risk-acceptance register at this scope: control key ->
	// documented justification (comply-or-explain).
	Accepted map[string]string `json:"accepted,omitempty"`
}

// Group is a node in the free group tree. Policy resolves org -> root group
// -> ... -> this group -> device.
type Group struct {
	// Parent is the optional parent group. Empty means a root group.
	Parent string `json:"parent,omitempty"`
	// IdpGroup maps this group to an identity-provider group claim.
	IdpGroup string `json:"idpGroup,omitempty"`
	// Pin holds a staged-rollout ring at a target revision; empty follows HEAD.
	Pin string `json:"pin,omitempty"`

	Settings map[string]any    `json:"settings,omitempty"`
	Enforced []string          `json:"enforced,omitempty"`
	Packages []string          `json:"packages,omitempty"`
	Flatpaks []string          `json:"flatpaks,omitempty"`
	Overlays []string          `json:"overlays,omitempty"`
	Accepted map[string]string `json:"accepted,omitempty"`
}

// DeviceState is the lifecycle position of a device.
const (
	// DeviceActive is the zero value: enrolled and converging.
	DeviceActive = ""
	// DeviceRetired keeps the record for audit but stops everything else:
	// no image builds, no check-ins, no rollout counting. Reactivation is
	// an explicit, audited step.
	DeviceRetired = "retired"
)

// Intent is a pending remote action a device reacts to. It is DATA, not a
// command channel: the device pulls it on check-in and acts locally, and
// every intent is an audited commit. No live RCE (ADR/design 0004).
const (
	// IntentNone is the zero value: no pending action.
	IntentNone = ""
	// IntentLock: lock all sessions and stay locked across reboot;
	// reversible by clearing the intent.
	IntentLock = "lock"
	// IntentWipe: cryptographically erase the device (destroy LUKS keys).
	// Irreversible; requires the device to be locked first (or force).
	IntentWipe = "wipe"
)

// Device is one managed machine, keyed by its asset tag.
type Device struct {
	// State is the lifecycle position ("" = active, "retired").
	State string `json:"state,omitempty"`
	// Intent is a pending remote action ("" | "lock" | "wipe").
	Intent       string   `json:"intent,omitempty"`
	Groups       []string `json:"groups,omitempty"`
	AssignedUser string   `json:"assignedUser,omitempty"`
	// Class categorises the device (laptop, server, station); filterable.
	Class    string `json:"class,omitempty"`
	Hardware string `json:"hardware"`
	// Labels are free-form key/value pairs for filter targeting.
	Labels map[string]string `json:"labels,omitempty"`

	Settings   map[string]any    `json:"settings,omitempty"`
	Enforced   []string          `json:"enforced,omitempty"`
	Packages   []string          `json:"packages,omitempty"`
	Flatpaks   []string          `json:"flatpaks,omitempty"`
	Overlays   []string          `json:"overlays,omitempty"`
	Accepted   map[string]string `json:"accepted,omitempty"`
	Exceptions map[string]string `json:"exceptions,omitempty"`
	ITAM       ITAM              `json:"itam,omitempty"`
}

// ITAM is the asset record carried on a device (asset tag is the key).
type ITAM struct {
	Serial    string `json:"serial,omitempty"`
	Model     string `json:"model,omitempty"`
	Owner     string `json:"owner,omitempty"`
	Site      string `json:"site,omitempty"`
	Lifecycle string `json:"lifecycle,omitempty"`
	HostKeyID string `json:"hostKeyId,omitempty"`
}

// Policy is a reusable named bundle of settings. Enforced lists which of the
// policy's own keys are locks: at the scope the policy is assigned to, those
// keys resolve with enforce semantics (most general wins), the rest with
// default semantics (most specific wins).
type Policy struct {
	Name        string         `json:"name,omitempty"`
	Description string         `json:"description,omitempty"`
	Settings    map[string]any `json:"settings"`
	Enforced    []string       `json:"enforced,omitempty"`
}

// Assignment binds one policy to one scope target, optionally narrowed by a
// filter. Priority breaks ties between assignments contributing the same key
// at the same scope specificity; higher wins.
type Assignment struct {
	Policy string `json:"policy"`
	// Target is a scope ref: "org", "group:<name>" or "device:<tag>".
	Target string `json:"target"`
	// Filter names an entry in Fleet.Filters; empty matches every device.
	Filter   string `json:"filter,omitempty"`
	Priority int    `json:"priority,omitempty"`
}

// Filter is a pure predicate over device attributes.
type Filter struct {
	Name string `json:"name,omitempty"`
	// Match is "all" (every rule must hold, the default) or "any".
	Match string       `json:"match,omitempty"`
	Rules []FilterRule `json:"rules"`
}

// FilterRule tests one device attribute. Attr is one of: tag, class,
// hardware, assignedUser, group (membership incl. ancestry), or
// "label:<key>". Op is one of: eq, ne, prefix, in.
type FilterRule struct {
	Attr   string   `json:"attr"`
	Op     string   `json:"op"`
	Value  string   `json:"value,omitempty"`
	Values []string `json:"values,omitempty"` // for op "in"
}

// RolloutPolicy is the organisation's staged-rollout plan: rings promote in
// order, each gated on convergence, health and soak (see domain/rollout).
// Ring definitions live in the rollout package; they are pure data here.
type RolloutPolicy struct {
	// Rings lists group names with their gates, in promotion order.
	Rings []RolloutRing `json:"rings,omitempty"`
	// ApproveRole is the minimum role required to start a rollout.
	ApproveRole string `json:"approveRole,omitempty"`
}

// RolloutRing mirrors rollout.Ring in the config document.
type RolloutRing struct {
	Group             string `json:"group"`
	Name              string `json:"name,omitempty"` // wave label (Canary, Test, Phase 1)
	SoakMinutes       int    `json:"soakMinutes,omitempty"`
	MinHealthyPercent int    `json:"minHealthyPercent,omitempty"`
	RequireApproval   bool   `json:"requireApproval,omitempty"` // manual promotion gate
}

// Label is the wave's display name (its Name, or the group if unnamed).
func (r RolloutRing) Label() string {
	if r.Name != "" {
		return r.Name
	}
	return r.Group
}

// AccessBinding mirrors identity.Binding in the config document.
type AccessBinding struct {
	Group string `json:"group"`
	Role  string `json:"role"`
	Scope string `json:"scope"`
}

// Decode parses a fleet document. It rejects unknown schema versions so an
// old binary never silently misreads a newer document.
func Decode(b []byte) (*Fleet, error) {
	var f Fleet
	if err := json.Unmarshal(b, &f); err != nil {
		return nil, fmt.Errorf("parse fleet document: %w", err)
	}
	if f.Version != Version {
		return nil, fmt.Errorf("fleet document version %d, this build supports %d", f.Version, Version)
	}
	return &f, nil
}

// Encode serializes the fleet document deterministically (indented, stable
// key order via encoding/json map sorting) for clean git diffs.
func (f *Fleet) Encode() ([]byte, error) {
	b, err := json.MarshalIndent(f, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(b, '\n'), nil
}
