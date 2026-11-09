package cmd

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/vdibart/polis-cli/cli-go/pkg/site"
	"github.com/vdibart/polis-cli/cli-go/pkg/storage"
)

// `polis unregister` used to remove the site from the discovery service with no
// question asked, where bash asks and takes --force. The two CLIs now agree, and
// --json, which cannot ask, refuses without --force instead of going ahead.
func TestConfirmUnregister(t *testing.T) {
	oldJSON := jsonOutput
	defer func() { jsonOutput = oldJSON }()

	cases := []struct {
		name    string
		json    bool
		force   bool
		input   string
		want    bool
		wantErr bool
	}{
		{"interactive yes", false, false, "yes\n", true, false},
		{"interactive y is not yes", false, false, "y\n", false, false},
		{"interactive no answer", false, false, "", false, false},
		{"interactive force", false, true, "", true, false},
		{"json without force refuses", true, false, "yes\n", false, true},
		{"json with force", true, true, "", true, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			jsonOutput = c.json
			got, err := confirmUnregister("example.com", c.force, strings.NewReader(c.input))
			if (err != nil) != c.wantErr {
				t.Fatalf("err = %v, wantErr %v", err, c.wantErr)
			}
			if err != nil && !strings.Contains(err.Error(), "--force") {
				t.Errorf("refusal does not name --force: %v", err)
			}
			if got != c.want {
				t.Errorf("proceed = %v, want %v", got, c.want)
			}
		})
	}
}

// End to end: under --json without --force the command exits non-zero and the
// discovery service is never asked to unregister anything.
func TestUnregister_JSONWithoutForceRemovesNothing(t *testing.T) {
	if os.Getenv("POLIS_TEST_UNREGISTER_HELPER") == "1" {
		dataDir = os.Getenv("POLIS_TEST_DIR")
		jsonOutput = true
		handleUnregister(strings.Fields(os.Getenv("POLIS_TEST_ARGS")))
		os.Exit(0)
	}

	dir := t.TempDir()
	if _, err := site.Init(storage.At(dir), site.InitOptions{BaseURL: "https://alice.polis.pub", SiteTitle: "alice"}); err != nil {
		t.Fatal(err)
	}
	var hits atomic.Int32
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		w.Write([]byte(`{"success": true}`))
	}))
	defer ts.Close()

	run := func(args string) (string, error) {
		cmd := exec.Command(os.Args[0], "-test.run=^TestUnregister_JSONWithoutForceRemovesNothing$")
		cmd.Env = append(os.Environ(),
			"POLIS_TEST_UNREGISTER_HELPER=1",
			"POLIS_TEST_DIR="+dir,
			"POLIS_TEST_ARGS="+args,
			"POLIS_BASE_URL=https://alice.polis.pub",
			"DISCOVERY_SERVICE_URL="+ts.URL,
		)
		out, err := cmd.CombinedOutput()
		return string(out), err
	}

	out, err := run("")
	var exitErr *exec.ExitError
	if !errors.As(err, &exitErr) || exitErr.ExitCode() == 0 {
		t.Fatalf("unregister --json without --force exited 0; want non-zero\n%s", out)
	}
	if !strings.Contains(out, `"success":false`) || !strings.Contains(out, "--force") {
		t.Errorf("want a JSON error naming --force, got:\n%s", out)
	}
	if n := hits.Load(); n != 0 {
		t.Errorf("the discovery service was called %d time(s) by a refused unregister", n)
	}

	for _, flag := range []string{"--force", "-f"} {
		hits.Store(0)
		if out, err := run(flag); err != nil {
			t.Fatalf("unregister --json %s: %v\n%s", flag, err, out)
		}
		if hits.Load() == 0 {
			t.Errorf("unregister --json %s never reached the discovery service", flag)
		}
	}
}
