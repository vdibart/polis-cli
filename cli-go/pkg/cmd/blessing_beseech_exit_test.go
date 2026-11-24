package cmd

import (
	"encoding/json"
	"errors"
	"github.com/vdibart/polis-cli/cli-go/pkg/storage"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/vdibart/polis-cli/cli-go/pkg/site"
)

// `blessing beseech` used to look the comment up at the discovery service, do
// nothing, and report "status": "success". It now sends a PENDING comment
// signed here again, by its version; any other version is refused, before the
// discovery service is asked anything.
func TestBlessingBeseech_RefusesAVersionNotPendingHere(t *testing.T) {
	if os.Getenv("POLIS_TEST_BESEECH_HELPER") == "1" {
		dataDir = os.Getenv("POLIS_TEST_DIR")
		jsonOutput = os.Getenv("POLIS_TEST_JSON") == "1"
		handleBlessingBeseech([]string{"sha256:abc123"})
		os.Exit(0)
	}

	dir := t.TempDir()
	if _, err := site.Init(storage.At(dir), site.InitOptions{BaseURL: "https://alice.polis.pub", SiteTitle: "alice"}); err != nil {
		t.Fatal(err)
	}
	var hits atomic.Int32
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		json.NewEncoder(w).Encode(map[string]interface{}{"exists": true})
	}))
	defer ts.Close()

	for _, mode := range []string{"1", "0"} {
		cmd := exec.Command(os.Args[0], "-test.run=^TestBlessingBeseech_RefusesAVersionNotPendingHere$")
		cmd.Env = append(os.Environ(),
			"POLIS_TEST_BESEECH_HELPER=1",
			"POLIS_TEST_DIR="+dir,
			"POLIS_TEST_JSON="+mode,
			"POLIS_BASE_URL=https://alice.polis.pub",
			"DISCOVERY_SERVICE_URL="+ts.URL,
		)
		var stdout, stderr strings.Builder
		cmd.Stdout, cmd.Stderr = &stdout, &stderr
		err := cmd.Run()
		var exitErr *exec.ExitError
		if !errors.As(err, &exitErr) || exitErr.ExitCode() == 0 {
			t.Errorf("json=%s: beseech exited 0; want non-zero\n%s%s", mode, stdout.String(), stderr.String())
		}
		all := stdout.String() + stderr.String()
		if strings.Contains(all, "comment sync") {
			t.Errorf("json=%s: points at `comment sync`, which does not re-request a blessing:\n%s", mode, all)
		}
		if mode != "1" {
			continue
		}
		var got struct {
			Status  string `json:"status"`
			Command string `json:"command"`
			Error   struct {
				Code    string `json:"code"`
				Message string `json:"message"`
			} `json:"error"`
		}
		if err := json.Unmarshal([]byte(stdout.String()), &got); err != nil {
			t.Fatalf("stdout is not one JSON object: %v\n%s", err, stdout.String())
		}
		if got.Status != "error" || got.Command != "blessing-beseech" || got.Error.Code != "NOT_FOUND" {
			t.Errorf("want status error, command blessing-beseech, code NOT_FOUND; got %+v", got)
		}
		if !strings.Contains(got.Error.Message, "polis comment send") || !strings.Contains(got.Error.Message, "polis republish") {
			t.Errorf("message does not say what can be done: %q", got.Error.Message)
		}
		// Only a pending comment is sent again: the discovery service returns
		// an earlier denial unchanged.
		if !strings.Contains(got.Error.Message, "PENDING") || !strings.Contains(got.Error.Message, "denied comment cannot be re-requested") {
			t.Errorf("message does not say only a pending comment can be re-requested: %q", got.Error.Message)
		}
	}
	if n := hits.Load(); n != 0 {
		t.Errorf("beseech made %d discovery-service request(s); it should refuse without one", n)
	}
}
