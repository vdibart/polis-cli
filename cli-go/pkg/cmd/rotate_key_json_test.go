package cmd

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/vdibart/polis-cli/cli-go/pkg/discovery"
	"github.com/vdibart/polis-cli/cli-go/pkg/site"
)

// runRotateKeyJSON runs the real handler in --json mode and returns data.
func runRotateKeyJSON(t *testing.T, dir, dsURL string) map[string]interface{} {
	t.Helper()
	t.Setenv("POLIS_BASE_URL", "https://alice.polis.pub")
	t.Setenv("DISCOVERY_SERVICE_URL", dsURL)

	prevDataDir, prevJSON := dataDir, jsonOutput
	t.Cleanup(func() { dataDir, jsonOutput = prevDataDir, prevJSON })
	dataDir, jsonOutput = dir, true

	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	oldStdout := os.Stdout
	os.Stdout = w
	handleRotateKey(nil)
	w.Close()
	os.Stdout = oldStdout
	out, _ := io.ReadAll(r)

	var got struct {
		Status string                 `json:"status"`
		Data   map[string]interface{} `json:"data"`
	}
	line := strings.TrimSpace(string(out))
	if err := json.Unmarshal([]byte(line), &got); err != nil {
		t.Fatalf("rotate-key JSON did not parse: %v\n%s", err, line)
	}
	if got.Status != "success" {
		t.Fatalf("status = %q", got.Status)
	}
	return got.Data
}

// ds_rotation used to be the constant "success" — including on a site that is
// not registered, where nothing was sent. It now says what happened.
func TestRotateKeyJSON_DSRotationSkippedWhenUnregistered(t *testing.T) {
	dir := t.TempDir()
	if _, err := site.Init(dir, site.InitOptions{BaseURL: "https://alice.polis.pub", SiteTitle: "alice"}); err != nil {
		t.Fatal(err)
	}
	contacted := false
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		contacted = true
		w.Write([]byte(`{}`))
	}))
	defer ts.Close()

	data := runRotateKeyJSON(t, dir, ts.URL)
	if data["ds_rotation"] != "skipped" {
		t.Errorf("ds_rotation = %v on an unregistered site, want \"skipped\"", data["ds_rotation"])
	}
	if contacted {
		t.Error("an unregistered site contacted the discovery service")
	}
}

func TestRotateKeyJSON_DSRotationSuccessWhenNotified(t *testing.T) {
	dir := t.TempDir()
	if _, err := site.Init(dir, site.InitOptions{BaseURL: "https://alice.polis.pub", SiteTitle: "alice"}); err != nil {
		t.Fatal(err)
	}
	contacted := false
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/sites/keys/rotate" {
			contacted = true
		}
		w.Write([]byte(`{}`))
	}))
	defer ts.Close()
	if err := discovery.WriteRegistrationMarker(dir, ts.URL, "alice.polis.pub", ""); err != nil {
		t.Fatal(err)
	}

	data := runRotateKeyJSON(t, dir, ts.URL)
	if !contacted {
		t.Fatal("a registered site did not notify the discovery service")
	}
	if data["ds_rotation"] != "success" {
		t.Errorf("ds_rotation = %v, want \"success\"", data["ds_rotation"])
	}
}
