package fleet

import (
	"slices"
	"strings"
	"testing"
	"time"
)

func TestValidClass(t *testing.T) {
	for _, c := range Classes {
		if !ValidClass(c) {
			t.Errorf("ValidClass(%q) = false, want true", c)
		}
	}
	for _, c := range []string{"", "kiosk", "workstation", "Laptop"} {
		if ValidClass(c) {
			t.Errorf("ValidClass(%q) = true, want false", c)
		}
	}
}

func TestSetGroupAllowedClasses(t *testing.T) {
	f := policyFleet(t)

	// A set that every existing member satisfies is accepted (frontoffice
	// holds only laptops).
	apply(t, f, SetGroupAllowedClasses("frontoffice", []string{"laptop"}))
	if got := f.Groups["frontoffice"].AllowedClasses; !slices.Equal(got, []string{"laptop"}) {
		t.Fatalf("AllowedClasses = %v", got)
	}

	// An unknown class name is rejected.
	if err := SetGroupAllowedClasses("frontoffice", []string{"laptop", "kiosk"})(f); err == nil {
		t.Fatal("accepted unknown class")
	}

	// An unknown group is rejected.
	if err := SetGroupAllowedClasses("nope", []string{"laptop"})(f); err == nil {
		t.Fatal("accepted unknown group")
	}

	// The empty set clears the guardrail (any class allowed).
	apply(t, f, SetGroupAllowedClasses("frontoffice", nil))
	if got := f.Groups["frontoffice"].AllowedClasses; got != nil {
		t.Fatalf("clear left %v", got)
	}
}

func TestSetGroupAllowedClasses_NarrowRefusedForActiveMember(t *testing.T) {
	f := policyFleet(t)
	// zaanstad directly holds srv-1 (a server); narrowing it to laptops only
	// would strand that member, so it must be refused and the error must name
	// the device and its class.
	err := SetGroupAllowedClasses("zaanstad", []string{"laptop"})(f)
	if err == nil {
		t.Fatal("narrowing that strands srv-1 was accepted")
	}
	if !strings.Contains(err.Error(), "srv-1") || !strings.Contains(err.Error(), "server") {
		t.Fatalf("error does not name device+class: %v", err)
	}

	// A retired member does not block a narrowing: it neither builds nor
	// counts. Retire srv-1, then the narrow succeeds.
	apply(t, f, RetireDevice("srv-1"))
	if err := SetGroupAllowedClasses("zaanstad", []string{"laptop"})(f); err != nil {
		t.Fatalf("narrow refused despite only a retired member outside the set: %v", err)
	}
}

func TestMembershipRefusedByAllowedClasses(t *testing.T) {
	f := policyFleet(t)
	apply(t, f, SetGroupAllowedClasses("frontoffice", []string{"laptop"}))

	// AddDevice: a server may not enroll into a laptop-only group.
	if err := AddDevice("srv-new", Device{Hardware: "msi", Class: "server", Groups: []string{"frontoffice"}}, time.Now())(f); err == nil {
		t.Fatal("AddDevice put a server in a laptop-only group")
	}
	// A laptop still enrolls fine.
	if err := AddDevice("lt-new", Device{Hardware: "hp-g4", Class: "laptop", Groups: []string{"frontoffice"}}, time.Now())(f); err != nil {
		t.Fatalf("AddDevice refused an allowed class: %v", err)
	}

	// UpdateDevice: moving the server (srv-1) into the laptop-only group fails.
	groups := []string{"frontoffice"}
	if err := UpdateDevice("srv-1", DevicePatch{Groups: &groups})(f); err == nil {
		t.Fatal("UpdateDevice moved a server into a laptop-only group")
	}
	// Changing an in-group laptop's class to server also trips the guardrail.
	server := "server"
	if err := UpdateDevice("lt-1", DevicePatch{Class: &server})(f); err == nil {
		t.Fatal("UpdateDevice reclassified an in-group laptop to server")
	}

	// CreateGroupWithDevices: a new laptop-only group refuses a server.
	if err := CreateGroupWithDevices("kiosks", Group{AllowedClasses: []string{"laptop"}}, []string{"srv-1"})(f); err == nil {
		t.Fatal("CreateGroupWithDevices added a server to a laptop-only group")
	}
}

func TestMembershipAllowedWhenGuardrailEmpty(t *testing.T) {
	f := policyFleet(t)
	// frontoffice has no guardrail: any class may join.
	if err := AddDevice("srv-new", Device{Hardware: "msi", Class: "server", Groups: []string{"frontoffice"}}, time.Now())(f); err != nil {
		t.Fatalf("empty guardrail refused a server: %v", err)
	}
}
