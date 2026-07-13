package mail

import "testing"

func TestConfigValidate(t *testing.T) {
	base := Config{Host: "smtp.example.com", Port: 587, From: "no-reply@example.com", Security: StartTLS}
	if err := base.Validate(); err != nil {
		t.Fatalf("valid config rejected: %v", err)
	}

	bad := []Config{
		{Port: 587, From: "a@b.com", Security: StartTLS},                      // no host
		{Host: "h", Port: 0, From: "a@b.com", Security: StartTLS},             // bad port
		{Host: "h", Port: 70000, From: "a@b.com", Security: StartTLS},         // bad port
		{Host: "h", Port: 587, From: "not-an-email", Security: StartTLS},      // bad from
		{Host: "h", Port: 587, From: "a@b.com", Security: Security("weird")},  // unknown security
		{Host: "mail.example.com", Port: 25, From: "a@b.com", Security: None}, // none over network
	}
	for i, c := range bad {
		if err := c.Validate(); err == nil {
			t.Errorf("bad config %d accepted", i)
		}
	}

	// Unencrypted is allowed only for a local relay.
	local := Config{Host: "localhost", Port: 25, From: "a@b.com", Security: None}
	if err := local.Validate(); err != nil {
		t.Errorf("local none-security relay rejected: %v", err)
	}

	// Two credential sources at once is a misconfiguration.
	both := base
	both.Username = "u"
	both.PasswordRef = "smtp-pw"
	both.PasswordEnc = []byte("enc")
	if err := both.Validate(); err == nil {
		t.Error("config with both a ref and an entered password accepted")
	}
}

func TestMessageValidate(t *testing.T) {
	if err := (Message{To: []string{"a@b.com"}, Subject: "hi"}).Validate(); err != nil {
		t.Fatalf("valid message rejected: %v", err)
	}
	for _, m := range []Message{
		{Subject: "hi"},                          // no recipients
		{To: []string{"nope"}, Subject: "hi"},    // bad address
		{To: []string{"a@b.com"}, Subject: "  "}, // empty subject
	} {
		if err := m.Validate(); err == nil {
			t.Errorf("bad message accepted: %+v", m)
		}
	}
}
