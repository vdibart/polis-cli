package server

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/vdibart/polis-cli/cli-go/pkg/remote"
)

// ⭐ A follow or unfollow that fails says so in plain words naming the site;
// ⛔ never the resolver's or the transport's words (they carried the
// machine's own DNS resolver's address to the owner and to any agent).
func TestAFailedFollowSaysWhyInPlainWords(t *testing.T) {
	notPolis := &remote.WellKnownError{Err: errors.New(`fetch https://x.example/.well-known/polis: 404`), NotPolis: true}
	unreachable := &remote.WellKnownError{Err: errors.New(`dial tcp: lookup bob.example on 127.0.0.53:53: no such host`)}
	for name, c := range map[string]struct {
		err  error
		want string
	}{
		"not polis":   {notPolis, "bob.example isn't a polis site, so it can't be followed"},
		"unreachable": {unreachable, "bob.example could not be reached just now (its /.well-known/polis did not answer), so it wasn't followed"},
		"anything":    {errors.New("discovery: 500 internal"), "bob.example could not be followed just now"},
	} {
		got := followFailure("follow", "bob.example", c.err).Error()
		if !strings.HasPrefix(got, c.want) {
			t.Errorf("%s: %q, want it to start %q", name, got, c.want)
		}
		for _, leak := range []string{"dial", "lookup", "127.0.0.53", "404", "discovery:", "fetch ", "x.example"} {
			if strings.Contains(got, leak) {
				t.Errorf("%s: %q carries %q", name, got, leak)
			}
		}
	}
	if got := followFailure("unfollow", "bob.example", unreachable).Error(); !strings.Contains(got, "wasn't unfollowed") {
		t.Errorf("unfollow: %q", got)
	}
}

// ⭐ Through the API: following a site that can't be reached answers 502
// with the plain words.
func TestFollowingAnUnreachableSiteAnswersInPlainWords(t *testing.T) {
	s, h := queryRoutes(t)
	w := sendBody(t, h, "POST", "/v1/content/follow", mint(t, s), `{"url":"https://nowhere.invalid"}`)
	var body struct {
		Error struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &body)
	msg := body.Error.Message
	if w.Code == http.StatusOK || !strings.Contains(msg, "nowhere.invalid") {
		t.Fatalf("follow of an unreachable site = %d %q, want a refusal naming it", w.Code, msg)
	}
	for _, leak := range []string{"dial", "lookup", "no such host", "Get \""} {
		if strings.Contains(msg, leak) {
			t.Errorf("answer %q carries %q", msg, leak)
		}
	}
}

func sendBody(t *testing.T, h http.Handler, method, path, token, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	return w
}

// ⭐ Through the API: a message to a site that can't be reached answers 502
// in plain words naming it, not "An internal error occurred".
func TestAMessageToAnUnreachableSiteSaysSoPlainly(t *testing.T) {
	s, h := queryRoutes(t)
	w := sendBody(t, h, "POST", "/v1/content/dm/actions/send", mint(t, s), `{"recipient_url":"https://nowhere.invalid","content":"hello"}`)
	if w.Code != http.StatusBadGateway || !strings.Contains(w.Body.String(), "nowhere.invalid could not be reached") {
		t.Fatalf("send to an unreachable site = %d %s", w.Code, w.Body.String())
	}
	for _, leak := range []string{"dial", "lookup", "no such host", "internal error"} {
		if strings.Contains(w.Body.String(), leak) {
			t.Errorf("answer carries %q: %s", leak, w.Body.String())
		}
	}
}
