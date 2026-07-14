package postgres

import (
	"strings"
	"testing"
	"time"
)

// fakeTokenRow is a minimal pgx.Row stand-in that fills scanToken's Scan
// destinations directly, letting the malformed-groups path be tested without
// a live database.
type fakeTokenRow struct {
	groups []byte
}

func (r fakeTokenRow) Scan(dest ...any) error {
	now := time.Now()
	*dest[0].(*string) = "tok-1"
	*dest[1].(*string) = "test token"
	*dest[2].(*string) = "personal"
	*dest[3].(*string) = "alice"
	*dest[4].(*[]byte) = r.groups
	*dest[5].(*string) = ""
	*dest[6].(*string) = "hash"
	*dest[7].(*time.Time) = now
	*dest[8].(*time.Time) = now.Add(time.Hour)
	*dest[9].(**time.Time) = nil
	return nil
}

// TestScanTokenRejectsMalformedGroups guards against a silently-empty group
// set: groups feed authorization decisions, so a corrupt groups jsonb column
// (manual edit, partial write, schema drift) must surface as an error, not
// load as "this token has no groups" with no signal anything is wrong.
func TestScanTokenRejectsMalformedGroups(t *testing.T) {
	_, err := scanToken(fakeTokenRow{groups: []byte(`not valid json`)})
	if err == nil {
		t.Fatal("malformed groups jsonb accepted silently")
	}
	if !strings.Contains(err.Error(), "tok-1") {
		t.Errorf("error = %v, want it to identify the token id", err)
	}
}

// TestScanTokenAcceptsValidGroups is the control case.
func TestScanTokenAcceptsValidGroups(t *testing.T) {
	tok, err := scanToken(fakeTokenRow{groups: []byte(`["owners","admins"]`)})
	if err != nil {
		t.Fatalf("valid groups rejected: %v", err)
	}
	if len(tok.Groups) != 2 || tok.Groups[0] != "owners" {
		t.Fatalf("groups = %+v", tok.Groups)
	}
}

// TestScanTokenAcceptsEmptyGroups: no groups column content at all (nil/empty
// bytes) is a legitimate "no groups snapshot", not an error.
func TestScanTokenAcceptsEmptyGroups(t *testing.T) {
	tok, err := scanToken(fakeTokenRow{groups: nil})
	if err != nil {
		t.Fatalf("empty groups rejected: %v", err)
	}
	if len(tok.Groups) != 0 {
		t.Fatalf("groups = %+v, want none", tok.Groups)
	}
}
