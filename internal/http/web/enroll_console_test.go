package web_test

import (
	"net/http"
	"net/url"
	"testing"
)

// enroll_console_test.go covers the console's enrolment handler: the path a
// device takes into the fleet document. It was at 0%.
//
// The failure worth guarding is not a crash. It is a 303 for a device that
// is registered but cannot check in - which happens if the credential is not
// issued and nobody notices. The handler is written to let enrolment succeed
// anyway (the credential can be re-issued), so what must never break is that
// the record itself is complete and correct.

func enrollPost(t *testing.T, base string, form url.Values) *http.Response {
	t.Helper()
	form.Set("csrf", "dev-csrf")
	resp, err := client().PostForm(base+"/devices", form)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	return resp
}

func TestEnrollADeviceFromTheConsole(t *testing.T) {
	ts, cfg := newConsole(t)

	resp := enrollPost(t, ts.URL, url.Values{
		"tag": {"lt-new"}, "hardware": {"hp-g4"}, "class": {"laptop"}, "group": {"pilot"},
	})
	if resp.StatusCode != 303 {
		t.Fatalf("enroll = %d", resp.StatusCode)
	}
	d, ok := cfg.Fleet().Devices["lt-new"]
	if !ok {
		t.Fatal("the device is not in the fleet document")
	}
	if d.Hardware != "hp-g4" || d.Class != "laptop" {
		t.Errorf("device = %+v", d)
	}
	// The group has to arrive, or the device inherits from org and gets a
	// configuration nobody chose for it.
	if len(d.Groups) != 1 || d.Groups[0] != "pilot" {
		t.Errorf("groups = %v, want [pilot]", d.Groups)
	}
	// And enrolment is dated: a device with a zero enrolment date reads as
	// provisional forever and never counts toward a rollout.
	if d.Enrolled.IsZero() {
		t.Error("the enrolment date is zero")
	}
}

func TestEnrollWithoutAGroupFallsBackToOrgScope(t *testing.T) {
	ts, cfg := newConsole(t)
	if resp := enrollPost(t, ts.URL, url.Values{
		"tag": {"lt-orphan"}, "hardware": {"hp-g4"}, "class": {"laptop"},
	}); resp.StatusCode != 303 {
		t.Fatalf("enroll = %d", resp.StatusCode)
	}
	d, ok := cfg.Fleet().Devices["lt-orphan"]
	if !ok {
		t.Fatal("the device was not enrolled")
	}
	if len(d.Groups) != 0 {
		t.Errorf("groups = %v, want none", d.Groups)
	}
}

func TestEnrollRefusesWhatTheFleetCannotHold(t *testing.T) {
	ts, cfg := newConsole(t)
	before := len(cfg.Fleet().Devices)
	cases := []struct {
		name string
		form url.Values
	}{
		{"no tag", url.Values{"hardware": {"hp-g4"}, "class": {"laptop"}}},
		{"tag that is not a slug", url.Values{"tag": {"LT NEW!"}, "hardware": {"hp-g4"}, "class": {"laptop"}}},
		{"no hardware profile", url.Values{"tag": {"lt-x"}, "class": {"laptop"}}},
		{"class outside the vocabulary", url.Values{"tag": {"lt-x"}, "hardware": {"hp-g4"}, "class": {"kiosk"}}},
		{"group that does not exist", url.Values{"tag": {"lt-x"}, "hardware": {"hp-g4"}, "class": {"laptop"}, "group": {"ghosts"}}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			resp := enrollPost(t, ts.URL, c.form)
			if resp.StatusCode == 303 {
				t.Errorf("accepted %v", c.form)
			}
			// Refused for the RIGHT reason. An earlier version of this test
			// posted to the wrong path and passed on 405 for every case,
			// proving nothing at all.
			if resp.StatusCode == http.StatusMethodNotAllowed {
				t.Fatalf("405: the test is posting to the wrong route, not exercising validation")
			}
		})
	}
	if got := len(cfg.Fleet().Devices); got != before {
		t.Errorf("the fleet grew from %d to %d devices on refused enrolments", before, got)
	}
}

// TestEnrollingAnExistingTagIsRefused: a second enrolment would reset the
// enrolment date and re-issue a credential, silently invalidating the one
// already running on the machine.
func TestEnrollingAnExistingTagIsRefused(t *testing.T) {
	ts, _ := newConsole(t)
	if resp := enrollPost(t, ts.URL, url.Values{
		"tag": {"lt-twice"}, "hardware": {"hp-g4"}, "class": {"laptop"},
	}); resp.StatusCode != 303 {
		t.Fatal("first enrolment failed")
	}
	if resp := enrollPost(t, ts.URL, url.Values{
		"tag": {"lt-twice"}, "hardware": {"hp-g4"}, "class": {"laptop"},
	}); resp.StatusCode == 303 {
		t.Error("enrolling an existing tag succeeded; the running credential would be invalidated")
	}
}
