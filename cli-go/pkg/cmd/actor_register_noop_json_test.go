package cmd

import (
	"encoding/json"
	"io"
	"os"
	"strings"
	"testing"

	"github.com/vdibart/polis-cli/cli-go/pkg/actor"
	"github.com/vdibart/polis-cli/cli-go/pkg/site"
)

// TestRegisterNoop_JSONFollowsTheSuccessContract — in --json mode the no-op must
// use the same {"status","command","data"} envelope as a real registration.
//
// The first version emitted {"success":true,"noop":true}, copying the ERROR
// envelope's key. A script that checks status=="success" and reads data would
// have misread it, and an end-to-end check still passed because it looked only
// for the field it had invented. This test asserts conformance to the contract.
func TestRegisterNoop_JSONFollowsTheSuccessContract(t *testing.T) {
	dir := registerGateSite(t)

	oldDataDir, oldJSON := dataDir, jsonOutput
	t.Cleanup(func() { dataDir, jsonOutput = oldDataDir, oldJSON })
	dataDir, jsonOutput = dir, true

	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	oldStdout := os.Stdout
	os.Stdout = w
	handleActorRegister([]string{"judge.example", "--authority", "operator"})
	w.Close()
	os.Stdout = oldStdout
	out, err := io.ReadAll(r)
	if err != nil {
		t.Fatal(err)
	}

	var got struct {
		Status  string                 `json:"status"`
		Command string                 `json:"command"`
		Data    map[string]interface{} `json:"data"`
		Success *bool                  `json:"success"`
	}
	line := strings.TrimSpace(string(out))
	if err := json.Unmarshal([]byte(line), &got); err != nil {
		t.Fatalf("the no-op's JSON did not parse: %v\n%s", err, line)
	}
	if got.Status != "success" || got.Command != "actor" {
		t.Errorf("envelope is status %q, command %q; want the documented success contract (success, actor)", got.Status, got.Command)
	}
	if got.Success != nil {
		t.Error(`the no-op must not use the error envelope's top-level "success" key`)
	}
	if got.Data["noop"] != true {
		t.Errorf("data.noop = %v, want true", got.Data["noop"])
	}
	if got.Data["domain"] != "judge.example" {
		t.Errorf("data.domain = %v, want the domain that was not registered", got.Data["domain"])
	}
	if _, err := os.Stat(actor.RegistryPath(dir)); err == nil {
		t.Error("the no-op must not create a registry")
	}
	if site.ActorRegistryPointer(dir) != "" {
		t.Error("the no-op must not publish an actor_registry pointer")
	}
}
