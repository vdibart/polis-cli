package remote

import (
	"errors"
	"fmt"
	"testing"
)

func TestWellKnownErrorClassification(t *testing.T) {
	wrap := func(code int) error {
		return &WellKnownError{Err: fmt.Errorf("failed to fetch .well-known/polis: %w", &StatusError{Code: code, URL: "u"})}
	}
	cases := []struct {
		name              string
		err               error
		notPolis, unavail bool
	}{
		{"404", wrap(404), true, false},
		{"403", wrap(403), true, false},
		{"429 says nothing about the site", wrap(429), false, true},
		{"500", wrap(500), false, true},
		{"network", &WellKnownError{Err: errors.New("dial tcp: refused")}, false, true},
		{"not a polis document", &WellKnownError{Err: errors.New("parse"), NotPolis: true}, true, false},
		{"a local error is neither", errors.New("disk full"), false, false},
		{"a bare 404 elsewhere is not about .well-known", &StatusError{Code: 404}, false, false},
	}
	for _, c := range cases {
		if got := TargetIsNotPolis(c.err); got != c.notPolis {
			t.Errorf("%s: TargetIsNotPolis = %v", c.name, got)
		}
		if got := TargetUnavailable(c.err); got != c.unavail {
			t.Errorf("%s: TargetUnavailable = %v", c.name, got)
		}
	}
	// The message is unchanged by the wrapper.
	if got := wrap(404).Error(); got != "failed to fetch .well-known/polis: fetch failed with status 404 for u" {
		t.Errorf("message changed: %q", got)
	}
}
