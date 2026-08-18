package web_test

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"code.overheid.nl/MinBZK/DAWO-Sextant/internal/adapters/git"
	"code.overheid.nl/MinBZK/DAWO-Sextant/internal/app"
	"code.overheid.nl/MinBZK/DAWO-Sextant/internal/domain/identity"
	"code.overheid.nl/MinBZK/DAWO-Sextant/internal/http/web"
	"code.overheid.nl/MinBZK/DAWO-Sextant/internal/ports"
)

const seedFleet = `{
  "version": 3,
  "org": {"settings": {"desktop": "plasma"}},
  "groups": {"pilot": {}},
  "devices": {"lt-1": {"groups": ["pilot"], "hardware": "hw"}}
}`

const seedCatalog = `[
  {"name":"apps.office","type":"boolean","description":"Office suite","default":false,"riskClass":"high"},
  {"name":"apps.retries","type":"positive integer","description":"Retries","default":0},
  {"name":"desktop","type":"string","description":"Desktop environment. Which one a device gets is a fleet decision, not a per-user one.","default":"kde","label":"Bureaublad"},
  {"name":"apps.licenseRef","type":"string","description":"App license key","secret":true},
  {"name":"netbird.setupKey","type":"string","description":"NetBird join key","secret":true},
  {"name":"usbDevices.enable","type":"boolean","description":"Block USB devices plugged in after boot","default":false,"riskClass":"high"},
  {"name":"usbDevices.allowlist","type":"list of string","description":"USBGuard rules for devices that must keep working","default":[]},
  {"name":"timesync.enable","type":"boolean","description":"Time sync","default":false,"label":"Time synchronisation"},
  {"name":"netbird.enable","type":"boolean","description":"Join the mesh","default":false},
  {"name":"netbird.managementUrl","type":"string","description":"Management server URL"},
  {"name":"timesync.servers","type":"string","description":"NTP servers"}
]`

const seedProfiles = `[
  {"name":"laptop","label":"Laptop workplace","class":"laptop",
   "settings":{"desktop":"gnome","apps.office":true}}
]`

const seedBundles = `[
  {"name":"secure-workplace","label":"Secure workplace","enable":"apps.office",
   "settings":{"apps.office":true,"desktop":"gnome","apps.retries":3},
   "exposed":["apps.retries"]}
]`

// newConsole builds the console over a real temp git repo seeded with
// fleet.json + catalog.json, dev sessions, allow-all gate.
func newConsole(t *testing.T) (*httptest.Server, *app.ConfigService) {
	t.Helper()
	allow := ports.GateFunc(func(context.Context, string, []string) error { return nil })
	return newConsoleWithGate(t, allow)
}

// newConsoleWithGate builds the console over the seed fleet with a caller-chosen
// gate, so a test can prove how a handler behaves when the nix gate rejects
// (e.g. metadata handlers that skip the gate must still succeed).
func newConsoleWithGate(t *testing.T, gate ports.Gate) (*httptest.Server, *app.ConfigService) {
	t.Helper()
	dir := t.TempDir()
	run := func(args ...string) {
		out, err := exec.Command("git", append([]string{"-C", dir}, args...)...).CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	run("init", "-q", "-b", "main")
	for name, body := range map[string]string{"fleet.json": seedFleet,
		"catalog.json": seedCatalog, "profiles.json": seedProfiles,
		"bundles.json": seedBundles} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	run("add", ".")
	run("-c", "user.name=t", "-c", "user.email=t@t", "commit", "-q", "-m", "seed")

	repo, err := git.Open(dir, "")
	if err != nil {
		t.Fatal(err)
	}
	cfg, err := app.NewConfigService(repo, gate)
	if err != nil {
		t.Fatal(err)
	}
	srv, err := web.New(web.Services{Config: cfg}, web.DevSessions{}, true,
		nil, nil, nil, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	srv.Routes(mux)
	ts := httptest.NewServer(mux)
	t.Cleanup(ts.Close)
	return ts, cfg
}

// noRedirect keeps 3xx visible so tests can assert on them.
func client() *http.Client {
	return &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	}}
}

func TestSettingsPageRendersCatalog(t *testing.T) {
	ts, _ := newConsole(t)
	resp, err := client().Get(ts.URL + "/settings")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != 200 {
		t.Fatalf("status = %d\n%s", resp.StatusCode, body)
	}
	page := string(body)
	for _, want := range []string{"apps.office", "Office suite", "high risk", "desktop",
		`&#34;kde&#34;`} {
		if !strings.Contains(page, want) {
			t.Errorf("page missing %q", want)
		}
	}
	// Integration options live on the Integrations page, never in the general
	// settings editor.
	if strings.Contains(page, "netbird.setupKey") {
		t.Error("integration setting leaked into the settings editor")
	}
	// Unknown scope 404s instead of rendering an empty editor.
	resp2, _ := client().Get(ts.URL + "/settings?scope=group:ghost")
	resp2.Body.Close()
	if resp2.StatusCode != 404 {
		t.Fatalf("ghost scope status = %d", resp2.StatusCode)
	}
}

func TestSettingsPostSetEnforceClear(t *testing.T) {
	ts, cfg := newConsole(t)
	post := func(form url.Values) *http.Response {
		t.Helper()
		form.Set("csrf", "dev-csrf")
		resp, err := client().PostForm(ts.URL+"/settings", form)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		return resp
	}
	// The batch handler diffs every catalog key against the scope's current
	// settings in one Save: an omitted/empty row clears a key that is currently
	// set. Booleans post as i:<key> (inherit) / b:<key> (the slider); every
	// other widget posts v:<key>. The seed fleet already sets org "desktop", so
	// every org post below echoes v:desktop to keep it - exactly as the settings
	// page always resubmits every row, touched or not.

	// Set + enforce apps.office at org (slider On = v:true).
	resp := post(url.Values{"scope": {"org"}, "v:desktop": {"plasma"},
		"v:apps.office": {"true"}, "e:apps.office": {"on"}})
	if resp.StatusCode != 303 {
		t.Fatalf("set status = %d", resp.StatusCode)
	}
	own, enforced, _ := cfg.Fleet().ScopeSettings("org")
	if own["apps.office"] != true || len(enforced) != 1 || enforced[0] != "apps.office" {
		t.Fatalf("after set: own=%v enforced=%v", own, enforced)
	}

	// The organisation root has no inherit control: sliding the boolean off
	// (an explicit empty v:) means default, so it clears and unenforces - org
	// never writes an explicit false. The field must be PRESENT: an absent
	// key belongs to another form (the integrations cards) and is untouched.
	post(url.Values{"scope": {"org"}, "v:desktop": {"plasma"}, "v:apps.office": {""}})
	own, enforced, _ = cfg.Fleet().ScopeSettings("org")
	if _, has := own["apps.office"]; has || len(enforced) != 0 {
		t.Fatalf("after off at org: own=%v enforced=%v", own, enforced)
	}

	// Group and device scopes take writes too (apps.office left at inherit = v: "").
	post(url.Values{"scope": {"group:pilot"}, "v:desktop": {"gnome"}, "v:apps.office": {""}})
	own, _, _ = cfg.Fleet().ScopeSettings("group:pilot")
	if own["desktop"] != "gnome" {
		t.Fatalf("group set: own=%v", own)
	}
	post(url.Values{"scope": {"device:lt-1"}, "v:desktop": {"cosmic"}, "v:apps.office": {""}})
	if res := cfg.Fleet().Resolve("lt-1"); res["desktop"].Value != "cosmic" {
		t.Fatalf("device set not resolved: %+v", res["desktop"])
	}

	// Guard rail: a value the catalog type rejects fails the whole batch.
	if resp := post(url.Values{"scope": {"org"}, "v:desktop": {"plasma"}, "v:apps.retries": {"notanumber"}}); resp.StatusCode != 400 {
		t.Errorf("bad value: status = %d, want 400", resp.StatusCode)
	}
	respCSRF, _ := client().PostForm(ts.URL+"/settings", url.Values{
		"scope": {"org"}, "v:desktop": {"plasma"}, "csrf": {"wrong"}})
	respCSRF.Body.Close()
	if respCSRF.StatusCode != 403 {
		t.Fatalf("bad csrf status = %d", respCSRF.StatusCode)
	}
}

// deviceScopeSeed sets a device's own setting (overriding org), enforces it,
// registers a secret reference and records a risk acceptance directly at the
// device - enough to exercise settingsPage's device-only rendering branches
// (Resolved/Source, SecretRefs, Acceptances) that the org-scope tests above
// never touch.
const deviceScopeSeed = `{
  "version": 3,
  "org": {"settings": {"desktop": "plasma"}},
  "groups": {"pilot": {}, "other": {}},
  "devices": {
    "lt-1": {"groups": ["pilot"], "hardware": "hw",
      "settings": {"desktop": "cosmic"}, "enforced": ["desktop"],
      "accepted": {"apps.office": "approved by CISO"}},
    "lt-2": {"groups": ["other"], "hardware": "hw"}
  },
  "secretRefs": {"vpn-key": {"description": "NetBird join key"}}
}`

// newConsoleWithFleet is newConsole with a caller-supplied fleet document, for
// scenarios the shared seedFleet does not cover.
func newConsoleWithFleet(t *testing.T, fleetDoc string) *httptest.Server {
	t.Helper()
	dir := t.TempDir()
	run := func(args ...string) {
		out, err := exec.Command("git", append([]string{"-C", dir}, args...)...).CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	run("init", "-q", "-b", "main")
	for name, body := range map[string]string{"fleet.json": fleetDoc, "catalog.json": seedCatalog} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	run("add", ".")
	run("-c", "user.name=t", "-c", "user.email=t@t", "commit", "-q", "-m", "seed")

	repo, err := git.Open(dir, "")
	if err != nil {
		t.Fatal(err)
	}
	gate := ports.GateFunc(func(context.Context, string, []string) error { return nil })
	cfg, err := app.NewConfigService(repo, gate)
	if err != nil {
		t.Fatal(err)
	}
	srv, err := web.New(web.Services{Config: cfg}, web.DevSessions{}, true,
		nil, nil, nil, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	srv.Routes(mux)
	ts := httptest.NewServer(mux)
	t.Cleanup(ts.Close)
	return ts
}

// TestSettingsPageDeviceScopeRendersResolvedAndRegistry exercises the
// device-scope-only rendering path settingsPage takes: the resolved-value
// column (own setting beats the inherited org value), the enforced lock, the
// secret-reference picker list and a risk acceptance recorded at the device.
func TestSettingsPageDeviceScopeRendersResolvedAndRegistry(t *testing.T) {
	ts := newConsoleWithFleet(t, deviceScopeSeed)
	resp, err := client().Get(ts.URL + "/settings?scope=device:lt-1")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != 200 {
		t.Fatalf("status = %d\n%s", resp.StatusCode, body)
	}
	page := string(body)
	for _, want := range []string{"cosmic", "vpn-key"} {
		if !strings.Contains(page, want) {
			t.Errorf("device-scope page missing %q", want)
		}
	}
	// Risk acceptances moved to the compliance page (Bram, 17 jul): the
	// settings editor must NOT render them anymore.
	if strings.Contains(page, "approved by CISO") {
		t.Error("settings page still renders risk acceptances")
	}
}

// TestSettingsPageGroupScopeFiltersDeviceDrilldown exercises settingsPage's
// group-scope-only branches: the scope selector's group cascade, the
// group-scoped app lists, and the device drill-down narrowed to direct group
// members (a device in a sibling group must not appear).
func TestSettingsPageGroupScopeFiltersDeviceDrilldown(t *testing.T) {
	ts := newConsoleWithFleet(t, deviceScopeSeed)
	resp, err := client().Get(ts.URL + "/settings?scope=group:pilot")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != 200 {
		t.Fatalf("status = %d\n%s", resp.StatusCode, body)
	}
	page := string(body)
	// The scope selector IS the scope indicator (the "you are editing" line
	// was consolidated away): the group option must render selected.
	if !strings.Contains(page, `value="group:pilot" selected`) {
		t.Error("group-scope page's selector does not show pilot as the edited scope")
	}
	if !strings.Contains(page, "lt-1") {
		t.Error("group-scope page missing its own member lt-1")
	}
	if strings.Contains(page, "lt-2") {
		t.Error("group-scope page leaked lt-2, a member of the sibling group")
	}
}

// TestPostSettingRejectsMalformedScope: a scope string in neither the
// org/group:/device: shape reaches ScopeSettings, which reports it as a bad
// scope - a plain 400, not a panic or a silent no-op.
func TestPostSettingRejectsMalformedScope(t *testing.T) {
	ts, _ := newConsole(t)
	resp, err := client().PostForm(ts.URL+"/settings", url.Values{
		"csrf": {"dev-csrf"}, "scope": {"not-a-scope"}, "v:desktop": {"gnome"}})
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("malformed scope status = %d, want 400\n%s", resp.StatusCode, body)
	}
}

// TestPostSettingRequiresEditorRole: a viewer-only binding may read a scope
// but not save to it - requireWeb must refuse with 403 before ApplySettings
// ever runs.
func TestPostSettingRequiresEditorRole(t *testing.T) {
	ts := newScopedConsole(t, identity.User{Subject: "u", Groups: []string{"alpha-team"}})
	resp, err := http.PostForm(ts.URL+"/settings", url.Values{
		"csrf": {"csrf"}, "scope": {"group:alpha"}, "v:desktop": {"gnome"}})
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("viewer post status = %d, want 403\n%s", resp.StatusCode, body)
	}
}

func TestRequireChangeRequestBlocksDirectSetting(t *testing.T) {
	ts, cfg := newConsole(t)
	c := client()

	// Turn on require-change-request (dev session is org owner).
	resp, _ := c.PostForm(ts.URL+"/assurance", url.Values{"csrf": {"dev-csrf"}, "requireChangeRequest": {"on"}})
	resp.Body.Close()
	if !cfg.Fleet().Assurance.RequireChangeRequest {
		t.Fatal("require-change-request not saved")
	}

	// A direct setting edit is now refused (must go through a change).
	resp, _ = c.PostForm(ts.URL+"/settings", url.Values{
		"csrf": {"dev-csrf"}, "scope": {"org"}, "v:desktop": {"gnome"}})
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode == 303 {
		t.Fatal("direct setting accepted while change-request is required")
	}
	if !strings.Contains(string(body), "change-request required") {
		t.Fatalf("expected change-request message, got %d\n%s", resp.StatusCode, body)
	}
}

// TestDependentFieldGreysWhileEnableOff proves the NTP-servers idiom end to
// end: with timesync.enable off (its default) the servers control renders
// disabled inside a greyed value column carrying the data-requires hook;
// turning the enable on at this scope frees it on the next render.
func TestDependentFieldGreysWhileEnableOff(t *testing.T) {
	ts, _ := newConsole(t)
	get := func() string {
		t.Helper()
		resp, err := client().Get(ts.URL + "/settings?scope=org")
		if err != nil {
			t.Fatal(err)
		}
		b, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		return string(b)
	}
	page := get()
	col := regexp.MustCompile(`(?s)<div class="[^"]*opacity-50"[^>]*data-requires="v:timesync\.enable"[^>]*>.*?name="v:timesync\.servers"[^>]*>`).FindString(page)
	if col == "" {
		t.Fatalf("no greyed data-requires column around timesync.servers")
	}
	if !strings.Contains(col, `name="v:timesync.servers" disabled`) {
		t.Errorf("servers control not disabled while enable is off: %s", col)
	}

	// Turn the enable on; the dependent field frees up.
	form := url.Values{"scope": {"org"}, "csrf": {"dev-csrf"},
		"v:timesync.enable": {"true"}}
	resp, err := client().PostForm(ts.URL+"/settings", form)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != 303 {
		t.Fatalf("enable save = %d", resp.StatusCode)
	}
	page = get()
	if regexp.MustCompile(`name="v:timesync\.servers" disabled`).MatchString(page) {
		t.Error("servers control still disabled after enabling timesync")
	}
}

// TestSettingsShowsHumanLabel: a labelled option leads with its human name,
// and the dotted path it hides from the row is still reachable in the row's
// hint panel. Losing the key entirely would be a regression of its own - it is
// what an operator writes into fleet.json or quotes in an issue.
func TestSettingsShowsHumanLabel(t *testing.T) {
	ts, _ := newConsole(t)
	_, page := getPage(t, ts, "/settings?scope=org")
	if !strings.Contains(page, "Bureaublad") {
		t.Error("label missing")
	}
	panel := regexp.MustCompile(`(?s)<span class="hint-panel">.*?desktop.*?</span>`).FindString(page)
	if panel == "" {
		t.Error("dotted path not reachable in the hint panel")
	}
}

// TestSettingsRowKeepsOneSentence: the row states WHAT a setting is and the
// rest of the catalog's prose moves into the hint panel. Before this split
// every row carried its full rationale paragraph, which is the reason the page
// could not be scanned.
func TestSettingsRowKeepsOneSentence(t *testing.T) {
	ts, _ := newConsole(t)
	_, page := getPage(t, ts, "/settings?scope=org")
	if !strings.Contains(page, ">Desktop environment.</p>") {
		t.Error("row paragraph is not the opening sentence on its own")
	}
	rest := "Which one a device gets is a fleet decision, not a per-user one."
	if !strings.Contains(page, rest) {
		t.Fatal("the rest of the description was dropped instead of moved")
	}
	// It must live in the panel, not beside the label: a page that merely
	// repeats the paragraph in both places has not solved anything.
	panel := regexp.MustCompile(`(?s)<span class="hint-panel">.*?` + regexp.QuoteMeta(rest) + `.*?</span>\s*</span>`).FindString(page)
	if panel == "" {
		t.Error("rationale is not inside the hint panel")
	}
}

// TestDependentHintFollowsEnable: the "takes effect once <enable> is on" line
// carries the same data-requires hook as the control beside it. Without it the
// control ungreyed on the toggle while the warning kept claiming the setting
// was still waiting - the state the console showed on 2026-08-13.
func TestDependentHintFollowsEnable(t *testing.T) {
	ts, _ := newConsole(t)
	_, page := getPage(t, ts, "/settings?scope=org")
	if !strings.Contains(page, `data-requires-hint="v:timesync.enable"`) {
		t.Error("dependency warning has no hook for the enable it names")
	}
}

// TestDependentHintNamesTheSetting: the sentence names the enable by the label
// the reader can find on the page, not only by its dotted key. Reported against
// 0.88.0: the row "Office suite" said "takes effect once apps.office.enable is
// on" while the switch it means is three rows up and labelled "Office apps".
// Three strings for two things, and the reader does the mapping.
func TestDependentHintNamesTheSetting(t *testing.T) {
	ts, _ := newConsole(t)
	_, page := getPage(t, ts, "/settings?scope=org")
	re := regexp.MustCompile(`data-requires-hint="v:timesync\.enable"[^>]*>(.*?)</p>`)
	m := re.FindStringSubmatch(page)
	if m == nil {
		t.Fatal("no dependency warning paragraph to check")
	}
	hint := m[1]
	if !strings.Contains(hint, "timesync.enable") {
		t.Error("the key is gone; somebody working in the config file needs it")
	}
	if !strings.Contains(hint, "Time synchronisation") {
		t.Errorf("hint names only the key, not the label a reader can find: %s", hint)
	}
}

// TestDependentHintIsASentence: the "takes effect once <enable> is on" line is
// a sentence with a key inside it, so it must NOT be a flex container. Under
// `flex items-center` each word becomes a flex item and the key is aligned as
// a box against the tallest one instead of sitting on the text baseline, which
// reads as the key floating a few pixels above the words beside it. Reported
// against 0.87.0 on 2026-08-18, on cacheAuth.enable.
//
// The icon lines around it are genuinely icon-plus-label and may stay flex;
// this asserts only the paragraph that carries a key mid-sentence.
func TestDependentHintIsASentence(t *testing.T) {
	ts, _ := newConsole(t)
	_, page := getPage(t, ts, "/settings?scope=org")
	re := regexp.MustCompile(`<p class="([^"]*)" data-requires-hint="v:timesync\.enable"`)
	m := re.FindStringSubmatch(page)
	if m == nil {
		t.Fatal("no dependency warning paragraph to check")
	}
	for _, cls := range strings.Fields(m[1]) {
		if cls == "flex" || cls == "inline-flex" {
			t.Errorf("the dependency sentence is a flex container (%q); its key will not sit on the baseline", m[1])
		}
	}
}
