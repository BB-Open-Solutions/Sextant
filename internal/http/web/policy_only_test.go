package web_test

import (
	"io"
	"net/url"
	"strings"
	"testing"
)

// ADR 0017: some controls may be set through a policy and nowhere else. The
// two properties that matter are that the editor stops offering them, and that
// hiding them is not the whole enforcement - a form post must be refused too,
// or the rule is decoration that anybody with curl can step around.

func TestPolicyOnlyKeysAreNotOfferedInTheEditor(t *testing.T) {
	ts, _ := newConsole(t)
	resp, err := client().Get(ts.URL + "/settings")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	page := string(body)

	// The seed catalog carries usbDevices.enable and its allowlist. Neither
	// may appear as an input on the general editor.
	for _, key := range []string{"v:usbDevices.enable", "v:usbDevices.allowlist"} {
		if strings.Contains(page, key) {
			t.Errorf("%q is still offered in the general settings editor", key)
		}
	}
	// An ordinary setting must still be there - a rule that hid everything
	// would pass the check above and be useless.
	if !strings.Contains(page, "v:desktop") {
		t.Fatal("ordinary settings vanished from the editor; the exclusion is too broad")
	}
}

// Hiding a field is not enforcement. Somebody posting the key directly has to
// be refused, and refused with a 403 rather than a quiet no-op, so an
// integration that tries it learns why instead of believing it worked.
func TestPolicyOnlyKeysAreRefusedOnPost(t *testing.T) {
	ts, cfg := newConsole(t)
	resp, err := client().PostForm(ts.URL+"/settings", url.Values{
		"csrf":                {"dev-csrf"},
		"scope":               {"org"},
		"v:usbDevices.enable": {"true"},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 403 {
		t.Fatalf("posting a policy-only key = %d, want 403", resp.StatusCode)
	}
	if got, ok := cfg.Fleet().Org.Settings["usbDevices.enable"]; ok {
		t.Fatalf("the value was stored anyway (%v); the refusal has to actually refuse", got)
	}
}

// Clearing one must stay possible, and this is the row that makes the feature
// usable rather than a trap: moving a control into a policy means setting it
// there and removing it here. Refusing the clear would strand every inline
// value that already exists.
func TestAPolicyOnlyKeyCanStillBeCleared(t *testing.T) {
	ts, _ := newConsole(t)
	resp, err := client().PostForm(ts.URL+"/settings", url.Values{
		"csrf":                {"dev-csrf"},
		"scope":               {"org"},
		"v:usbDevices.enable": {""},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode == 403 {
		t.Fatal("clearing a policy-only key was refused; an inline value could never be moved into a policy")
	}
}
