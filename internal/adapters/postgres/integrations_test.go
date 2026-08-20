package postgres

import (
	"context"
	"testing"

	"code.overheid.nl/MinBZK/DAWO-Sextant/internal/domain/observed"
)

// The whole point of the nullable column: a beat that says nothing about
// integrations must not erase what the last beat did say. An older agent
// checks in every minute, so getting this wrong would blank the panel within
// a minute of the first device that has not been updated yet.
func TestIntegrationsSurviveASilentBeat(t *testing.T) {
	s := openStore(t)
	ctx := context.Background()

	reporting := observed.CheckIn{
		Tag:      "lt-1",
		Revision: "rev-1",
		Integrations: observed.Integrations{
			"netbird": {State: "up"},
			"wazuh":   {State: "down", Detail: "wazuh-agent.service failed"},
		},
	}
	if _, err := s.Upsert(ctx, "default", reporting, t0); err != nil {
		t.Fatal(err)
	}

	// An older agent: same device, no integration field at all.
	silent := observed.CheckIn{Tag: "lt-1", Revision: "rev-2"}
	if _, err := s.Upsert(ctx, "default", silent, t0); err != nil {
		t.Fatal(err)
	}

	st, ok, err := s.Get(ctx, "default", "lt-1")
	if err != nil || !ok {
		t.Fatalf("get: %v ok=%v", err, ok)
	}
	if st.Revision != "rev-2" {
		t.Fatalf("revision = %q, want the silent beat to have landed", st.Revision)
	}
	if !st.Integrations.Up("netbird") {
		t.Fatalf("integrations = %+v, want netbird still up after a silent beat", st.Integrations)
	}
	if got := st.Integrations["wazuh"]; got.State != "down" || got.Detail != "wazuh-agent.service failed" {
		t.Fatalf("wazuh = %+v, want the reported failure kept", got)
	}
}

// An agent that reports an EMPTY set is saying something: nothing is on any
// more. That has to clear the row, or an integration switched off keeps its
// last state on screen for good.
func TestEmptyIntegrationsClearTheRow(t *testing.T) {
	s := openStore(t)
	ctx := context.Background()

	if _, err := s.Upsert(ctx, "default", observed.CheckIn{
		Tag:          "lt-2",
		Integrations: observed.Integrations{"netbird": {State: "up"}},
	}, t0); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Upsert(ctx, "default", observed.CheckIn{
		Tag:          "lt-2",
		Integrations: observed.Integrations{},
	}, t0); err != nil {
		t.Fatal(err)
	}
	st, _, err := s.Get(ctx, "default", "lt-2")
	if err != nil {
		t.Fatal(err)
	}
	if len(st.Integrations) != 0 {
		t.Fatalf("integrations = %+v, want cleared by an empty report", st.Integrations)
	}
	if st.Integrations == nil {
		t.Fatal("an empty report reads back as never-reported; the two must stay apart")
	}
}

// List carries the same column as Get. It is a separate query, and a column
// added to one and forgotten in the other is exactly the kind of drift that
// leaves the device list and the device page disagreeing.
func TestListCarriesIntegrations(t *testing.T) {
	s := openStore(t)
	ctx := context.Background()
	if _, err := s.Upsert(ctx, "default", observed.CheckIn{
		Tag:          "lt-3",
		Integrations: observed.Integrations{"openbao": {State: "up"}},
	}, t0); err != nil {
		t.Fatal(err)
	}
	sts, err := s.List(ctx, "default")
	if err != nil {
		t.Fatal(err)
	}
	var found bool
	for _, st := range sts {
		if st.Tag == "lt-3" {
			found = st.Integrations.Up("openbao")
		}
	}
	if !found {
		t.Fatalf("list did not carry the integration reading: %+v", sts)
	}
}
