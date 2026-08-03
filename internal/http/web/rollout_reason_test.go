package web

import (
	"os"
	"strings"
	"testing"
)

// The rollout page reads Waiting, WaitingFor and Behind from its handler's
// data map. Go templates resolve a MISSING map key to the zero value without
// complaining, so a field the handler forgets renders as nothing at all and
// every test still passes - which is exactly what happened: the waiting fields
// were added to the updates handler while the markup went into rollout.html,
// and the line an operator most needs never appeared.
func TestRolloutPageFieldsAreActuallySupplied(t *testing.T) {
	tpl, err := os.ReadFile("templates/rollout.html")
	if err != nil {
		t.Fatal(err)
	}
	handler, err := os.ReadFile("rollout_ops.go")
	if err != nil {
		t.Fatal(err)
	}
	for _, field := range []string{"Waiting", "WaitingFor", "Behind"} {
		if !strings.Contains(string(tpl), "."+field) {
			t.Errorf("rollout.html no longer reads %s; drop it from the handler too", field)
			continue
		}
		if !strings.Contains(string(handler), `"`+field+`"`) {
			t.Errorf("rollout.html reads .%s but rolloutPage never puts it in the data map, "+
				"so it renders as empty and nobody is told", field)
		}
	}
}
