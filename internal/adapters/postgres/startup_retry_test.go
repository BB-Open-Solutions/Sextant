package postgres

import (
	"errors"
	"net"
	"testing"
)

// The distinction this rests on: "the database is not there YET" is worth
// waiting for, "the database said no" is not. Getting it wrong in the second
// direction is the dangerous one - a bad migration retried for two minutes
// buries the real error under noise and delays the failure.

type timeoutErr struct{}

func (timeoutErr) Error() string   { return "i/o timeout" }
func (timeoutErr) Timeout() bool   { return true }
func (timeoutErr) Temporary() bool { return true }

func TestUnreachableCoversTheWaysACellComesUpSlowly(t *testing.T) {
	for _, e := range []error{
		errors.New("failed to connect to `user=app database=app`: dial error: connection refused"),
		// A Cilium policy not yet programmed. This is the one that made a
		// fresh cell look like a broken deployment on 2026-07-30.
		errors.New("dial tcp 10.43.80.174:5432: connect: operation not permitted"),
		errors.New("dial tcp 10.43.80.174:5432: connect: no route to host"),
		errors.New("the database system is starting up"),
		errors.New("server is not accepting connections"),
		net.Error(timeoutErr{}),
	} {
		if !isUnreachable(e) {
			t.Errorf("treated as fatal, should be retried: %v", e)
		}
	}
}

func TestARealFailureIsNotRetried(t *testing.T) {
	for _, e := range []error{
		// A migration that ran and failed. Retrying it changes nothing and
		// hides the reason behind two minutes of warnings.
		errors.New(`migration 0014_diagnostics.sql: ERROR: column "foo" does not exist (SQLSTATE 42703)`),
		errors.New("advisory lock: permission denied for database app"),
		errors.New(`failed to connect: password authentication failed for user "app"`),
	} {
		if isUnreachable(e) {
			t.Errorf("would be retried, but the database answered: %v", e)
		}
	}
}
