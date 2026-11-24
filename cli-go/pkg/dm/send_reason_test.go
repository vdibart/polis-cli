package dm

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
)

// ⭐ A recipient whose messages key can't be had says why: its site did not
// answer, or it answered without a messages key this site can use. The
// sender's API turns each into plain words naming the site.
func TestAFailedKeyFetchSaysWhy(t *testing.T) {
	sender, _ := testSender(t)
	answer := func(code int, body string) string {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(code)
			_, _ = w.Write([]byte(body))
		}))
		t.Cleanup(srv.Close)
		return srv.URL
	}
	gone := httptest.NewServer(http.NotFoundHandler())
	goneURL := gone.URL
	gone.Close()

	for name, c := range map[string]struct {
		url  string
		want error
	}{
		"not there":        {goneURL, ErrRecipientUnreachable},
		"a server error":   {answer(http.StatusBadGateway, ""), ErrRecipientUnreachable},
		"throttled":        {answer(http.StatusTooManyRequests, ""), ErrRecipientUnreachable},
		"not a polis site": {answer(http.StatusNotFound, ""), ErrRecipientNoMessagesKey},
		"not JSON":         {answer(http.StatusOK, "<html>"), ErrRecipientNoMessagesKey},
		"no identity key":  {answer(http.StatusOK, `{}`), ErrRecipientNoMessagesKey},
	} {
		_, err := sender.SendMessage(c.url, "Hello", "")
		if !errors.Is(err, c.want) {
			t.Errorf("%s: %v, want it to be %v", name, err, c.want)
		}
	}
}
