package cmd

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"testing"

	"github.com/vdibart/polis-cli/cli-go/pkg/site"
)

// `blessing beseech` for a comment the discovery service does not know used to
// print an error and exit 0 — so a script checking the exit status, as the
// JSON contract tells it to, read a failure as success.
func TestBlessingBeseech_NotFoundExitsNonZero(t *testing.T) {
	if os.Getenv("POLIS_TEST_BESEECH_HELPER") == "1" {
		dataDir = os.Getenv("POLIS_TEST_DIR")
		jsonOutput = os.Getenv("POLIS_TEST_JSON") == "1"
		handleBlessingBeseech([]string{"sha256:unknown"})
		os.Exit(0)
	}

	dir := t.TempDir()
	if _, err := site.Init(dir, site.InitOptions{BaseURL: "https://alice.polis.pub", SiteTitle: "alice"}); err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]interface{}{"exists": false})
	}))
	defer ts.Close()

	for _, mode := range []string{"1", "0"} {
		cmd := exec.Command(os.Args[0], "-test.run=^TestBlessingBeseech_NotFoundExitsNonZero$")
		cmd.Env = append(os.Environ(),
			"POLIS_TEST_BESEECH_HELPER=1",
			"POLIS_TEST_DIR="+dir,
			"POLIS_TEST_JSON="+mode,
			"DISCOVERY_SERVICE_URL="+ts.URL,
		)
		out, err := cmd.CombinedOutput()
		var exitErr *exec.ExitError
		if !errors.As(err, &exitErr) || exitErr.ExitCode() == 0 {
			t.Errorf("json=%s: beseech of an unknown comment exited 0; want non-zero\n%s", mode, out)
		}
	}
}
