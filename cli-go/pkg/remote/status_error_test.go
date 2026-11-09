package remote_test

import (
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/vdibart/polis-cli/cli-go/pkg/remote"
)

// The message is relied on by anything that was matching the fmt.Errorf this
// type replaced, so it is pinned rather than merely assumed.
func TestStatusErrorMessageIsUnchanged(t *testing.T) {
	err := &remote.StatusError{Code: 404, URL: "https://example.com/a.md"}
	want := "fetch failed with status 404 for https://example.com/a.md"
	if got := err.Error(); got != want {
		t.Errorf("Error() = %q, want %q", got, want)
	}
}

func TestIsThrottledOnlyMatches429(t *testing.T) {
	cases := []struct {
		code int
		want bool
	}{{429, true}, {404, false}, {403, false}, {500, false}, {503, false}}
	for _, c := range cases {
		err := error(&remote.StatusError{Code: c.code, URL: "https://example.com/x"})
		if got := remote.IsThrottled(err); got != c.want {
			t.Errorf("IsThrottled(status %d) = %v, want %v", c.code, got, c.want)
		}
		// It must survive wrapping: callers add context before the check.
		wrapped := fmt.Errorf("while checking the fleet: %w", err)
		if got := remote.IsThrottled(wrapped); got != c.want {
			t.Errorf("IsThrottled(wrapped status %d) = %v, want %v", c.code, got, c.want)
		}
	}
	if remote.IsThrottled(errors.New("connection refused")) {
		t.Error("a non-status error must not read as throttled")
	}
	if remote.IsThrottled(nil) {
		t.Error("nil must not read as throttled")
	}
}

// The distinction this type exists for: 404 says something about the site,
// 429 says something about us. A caller that cannot tell them apart records a
// throttled sweep as a fleet full of empty sites — which is how a rate-limited
// canary run came out green.
func TestFetchContentCarriesTheStatusAndRetryAfter(t *testing.T) {
	for _, tc := range []struct {
		name       string
		code       int
		retryAfter string
		throttled  bool
	}{
		{"not found", http.StatusNotFound, "", false},
		{"throttled", http.StatusTooManyRequests, "3179", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if tc.retryAfter != "" {
					w.Header().Set("Retry-After", tc.retryAfter)
				}
				w.WriteHeader(tc.code)
			}))
			defer srv.Close()

			_, err := remote.NewClient().FetchContent(srv.URL + "/.well-known/polis")
			if err == nil {
				t.Fatal("expected an error")
			}
			var se *remote.StatusError
			if !errors.As(err, &se) {
				t.Fatalf("error is not a *StatusError: %T %v", err, err)
			}
			if se.Code != tc.code {
				t.Errorf("Code = %d, want %d", se.Code, tc.code)
			}
			if se.RetryAfter != tc.retryAfter {
				t.Errorf("RetryAfter = %q, want %q", se.RetryAfter, tc.retryAfter)
			}
			if got := remote.IsThrottled(err); got != tc.throttled {
				t.Errorf("IsThrottled = %v, want %v", got, tc.throttled)
			}
		})
	}
}
