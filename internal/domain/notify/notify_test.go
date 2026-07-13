package notify

import "testing"

func TestValidate(t *testing.T) {
	base := Notification{ID: "n1", Kind: ApprovalNeeded, Title: "Review needed", Recipient: "sub-1"}
	if err := base.Validate(); err != nil {
		t.Fatalf("valid notification rejected: %v", err)
	}
	cases := map[string]Notification{
		"empty id":         {Kind: ApprovalNeeded, Title: "t", Recipient: "s"},
		"unknown kind":     {ID: "n", Kind: "made-up", Title: "t", Recipient: "s"},
		"empty title":      {ID: "n", Kind: RolloutDone, Recipient: "s"},
		"neither audience": {ID: "n", Kind: RolloutDone, Title: "t"},
		"both audiences":   {ID: "n", Kind: RolloutDone, Title: "t", Recipient: "s", Audience: "g"},
	}
	for name, n := range cases {
		if err := n.Validate(); err == nil {
			t.Errorf("%s: expected a validation error", name)
		}
	}
}

func TestForReader(t *testing.T) {
	direct := Notification{Recipient: "sub-1"}
	if !direct.ForReader("sub-1", nil) {
		t.Error("direct notification not delivered to its subject")
	}
	if direct.ForReader("sub-2", []string{"owner"}) {
		t.Error("direct notification leaked to another subject")
	}
	broadcast := Notification{Audience: "owner"}
	if !broadcast.ForReader("sub-9", []string{"editor", "owner"}) {
		t.Error("broadcast not delivered to a member of its audience")
	}
	if broadcast.ForReader("sub-9", []string{"viewer"}) {
		t.Error("broadcast delivered to a non-member")
	}
}
