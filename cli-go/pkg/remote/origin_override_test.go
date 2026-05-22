package remote

import (
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

func TestParseOriginOverrides_HappyPath(t *testing.T) {
	raw := "discover.polis.pub=http://localhost:9000/origin/discover.polis.pub,alice.polis.pub=http://localhost:9000/origin/alice.polis.pub"
	got, err := ParseOriginOverrides(raw)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("got %d entries, want 2", len(got))
	}
	if u := got["discover.polis.pub"]; u == nil || u.Host != "localhost:9000" || u.Path != "/origin/discover.polis.pub" {
		t.Errorf("discover override = %+v", u)
	}
}

func TestParseOriginOverrides_Errors(t *testing.T) {
	cases := map[string]string{
		"missing equals":  "discover.polis.pub",
		"empty host":      "=http://x",
		"empty url":       "discover.polis.pub=",
		"no scheme":       "discover.polis.pub=localhost:9000",
		"no host in url":  "discover.polis.pub=http://",
	}
	for name, raw := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := ParseOriginOverrides(raw)
			if err == nil {
				t.Errorf("expected error for %q", raw)
			}
		})
	}
}

func TestParseOriginOverrides_EmptyAndWhitespace(t *testing.T) {
	got, err := ParseOriginOverrides("")
	if err != nil {
		t.Fatalf("empty: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("empty raw should produce zero entries, got %d", len(got))
	}

	got, err = ParseOriginOverrides("   ,  , a.com=http://b ")
	if err != nil {
		t.Fatalf("with whitespace: %v", err)
	}
	if len(got) != 1 {
		t.Errorf("expected 1 entry after stripping empties, got %d", len(got))
	}
}

func TestMaybeWrapTransport_NoEnvIsNoop(t *testing.T) {
	t.Setenv(EnvOriginOverrides, "")
	inner := http.DefaultTransport
	got := MaybeWrapTransport(inner)
	if got != inner {
		t.Errorf("expected identity return when env unset, got %T", got)
	}
}

func TestMaybeWrapTransport_BadEnvDisables(t *testing.T) {
	t.Setenv(EnvOriginOverrides, "garbage")
	inner := http.DefaultTransport
	got := MaybeWrapTransport(inner)
	if got != inner {
		t.Errorf("expected identity return on bad env, got %T", got)
	}
}

// End-to-end: outbound request for discover.polis.pub gets routed to the
// override target; non-matching hosts pass through unchanged.
func TestOriginOverrideTransport_Routes(t *testing.T) {
	// Stand up a fake "psychic" that records what path it received.
	var receivedPath, receivedHost string
	psychic := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		receivedPath = r.URL.Path
		receivedHost = r.Host
		w.WriteHeader(200)
		_, _ = io.WriteString(w, "ok")
	}))
	defer psychic.Close()

	// Configure overrides: pretend discover.polis.pub routes through psychic.
	psychicURL, _ := url.Parse(psychic.URL + "/origin/discover.polis.pub")
	tr := &originOverrideTransport{
		overrides: map[string]*url.URL{"discover.polis.pub": psychicURL},
		inner:     http.DefaultTransport,
	}
	c := &http.Client{Transport: tr}

	// Outbound request to https://discover.polis.pub/posts/foo.html should
	// land at psychic at /origin/discover.polis.pub/posts/foo.html.
	resp, err := c.Get("https://discover.polis.pub/posts/foo.html")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Errorf("status %d", resp.StatusCode)
	}
	if want := "/origin/discover.polis.pub/posts/foo.html"; receivedPath != want {
		t.Errorf("psychic saw path %q, want %q", receivedPath, want)
	}
	if want := psychicURL.Host; receivedHost != want {
		t.Errorf("psychic saw Host %q, want %q", receivedHost, want)
	}
}

func TestOriginOverrideTransport_NonMatchPassesThrough(t *testing.T) {
	// A real-network passthrough is hard to test deterministically, so use
	// httptest to stand in for "real upstream."
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
	}))
	defer upstream.Close()

	psychicURL, _ := url.Parse("http://psychic:9000/origin/discover.polis.pub")
	tr := &originOverrideTransport{
		overrides: map[string]*url.URL{"discover.polis.pub": psychicURL},
		inner:     http.DefaultTransport,
	}
	c := &http.Client{Transport: tr}

	// alice.polis.pub is NOT in overrides — should hit upstream directly.
	resp, err := c.Get(upstream.URL + "/anything")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Errorf("status %d", resp.StatusCode)
	}
}

func TestNewClient_WrapsTransportWhenEnvSet(t *testing.T) {
	t.Setenv(EnvOriginOverrides, "discover.polis.pub=http://localhost:9000")
	c := NewClient()
	if c.HTTPClient.Transport == nil {
		t.Fatal("expected wrapped transport, got nil")
	}
	if _, ok := c.HTTPClient.Transport.(*originOverrideTransport); !ok {
		t.Errorf("expected *originOverrideTransport, got %T", c.HTTPClient.Transport)
	}
}

func TestNewClient_NoWrapWhenEnvUnset(t *testing.T) {
	t.Setenv(EnvOriginOverrides, "")
	c := NewClient()
	if _, ok := c.HTTPClient.Transport.(*originOverrideTransport); ok {
		t.Errorf("did not expect wrap when env unset")
	}
}

func TestNewClientWithHTTP_PreservesCallerClient(t *testing.T) {
	t.Setenv(EnvOriginOverrides, "discover.polis.pub=http://localhost:9000")
	hc := &http.Client{}
	c := NewClientWithHTTP(hc)
	if c.HTTPClient == hc {
		t.Errorf("caller's *http.Client should not be aliased when wrapping")
	}
	if hc.Transport != nil {
		t.Errorf("caller's transport must not be mutated")
	}
}

func TestJoinPath(t *testing.T) {
	cases := []struct{ a, b, want string }{
		{"", "/foo", "/foo"},
		{"/origin", "", "/origin"},
		{"/origin", "/foo", "/origin/foo"},
		{"/origin/", "/foo", "/origin/foo"},
		{"/origin", "foo", "/origin/foo"},
	}
	for _, c := range cases {
		if got := joinPath(c.a, c.b); got != c.want {
			t.Errorf("joinPath(%q,%q) = %q, want %q", c.a, c.b, got, c.want)
		}
	}
}

// Sanity: parse error message references the bad pair.
func TestParseOriginOverrides_ErrorMessages(t *testing.T) {
	_, err := ParseOriginOverrides("bad-pair-no-equals")
	if err == nil || !strings.Contains(err.Error(), "bad-pair-no-equals") {
		t.Errorf("error should reference bad pair: %v", err)
	}
}
