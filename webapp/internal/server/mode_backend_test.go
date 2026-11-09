package server

import (
	"strings"
	"testing"

	"github.com/vdibart/polis-cli/cli-go/pkg/capability"
	"github.com/vdibart/polis-cli/cli-go/pkg/mode"
	"github.com/vdibart/polis-cli/cli-go/pkg/owner"
	"github.com/vdibart/polis-cli/cli-go/pkg/storage"
)

// ⛔ An unusable POLIS_MODE stops the boot on every backend — the scan's case
// was POSIX storage on a VPS behind a proxy. An unset mode still runs as local
// on a laptop; a local mode stops a deployed backend.
func TestAnUnusableModeStopsEveryBootAndLocalStopsADeployedOne(t *testing.T) {
	blob := storage.BackendVercelBlob
	for _, c := range []struct {
		name    string
		m       mode.Mode
		note    string
		unset   bool
		dev     bool
		kind    string
		refuses bool
	}{
		{"POSIX, typo", mode.Local, "ignoring POLIS_MODE=remote-server-sined", false, false, storage.BackendPOSIX, true},
		{"no POLIS_STORAGE, typo", mode.Local, "ignoring POLIS_MODE=remote-server-sined", false, false, "", true},
		{"POSIX, custodied claimed", mode.Local, "ignoring POLIS_MODE=custodied", false, false, storage.BackendPOSIX, true},
		{"laptop, unset", mode.Local, "", true, false, "", false},
		{"POSIX, unset", mode.Local, "", true, false, storage.BackendPOSIX, false},
		{"POSIX, server-signed", mode.RemoteServerSigned, "", false, false, storage.BackendPOSIX, false},
		{"deployed, typo", mode.Local, "ignoring POLIS_MODE=remote-sever", false, false, blob, true},
		{"deployed, unset (local)", mode.Local, "", true, false, blob, true},
		{"deployed, server-signed", mode.RemoteServerSigned, "", false, false, blob, false},
		{"deployed, dev server", mode.RemoteClientSigned, "", false, true, blob, false},
	} {
		err := checkModeForBackend(c.m, c.note, c.unset, c.dev, c.kind, nil)
		if (err != nil) != c.refuses {
			t.Errorf("%s: err=%v, want refuses=%v", c.name, err, c.refuses)
		}
	}
}

func envOf(kv map[string]string) func(string) string {
	return func(k string) string { return kv[k] }
}

// bootModeError runs what Run does with the mode: resolve it from the
// environment, then check it against the backend and the remote-only signals.
func bootModeError(kv map[string]string, dev bool) error {
	getenv := envOf(kv)
	m, note, unset := capability.ResolveSelfHosted(getenv)
	return checkModeForBackend(m, note, unset, dev, getenv(storage.EnvStorageKind), remoteOnlySignals(getenv))
}

// ⛔ A deployer who set a recovery key but forgot POLIS_MODE would otherwise run
// as local, with loopback owner-trust, behind their proxy.
func TestAnUnsetModeWithARecoveryKeyStopsTheBoot(t *testing.T) {
	err := bootModeError(map[string]string{owner.RecoveryKeyEnv: "anything"}, false)
	if err == nil {
		t.Fatal("an unset mode with a recovery key set started")
	}
	for _, want := range []string{capability.ModeEnv, owner.RecoveryKeyEnv, "refusing to start"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("refusal %q does not name %q", err, want)
		}
	}
	if strings.Contains(err.Error(), "anything") {
		t.Errorf("refusal %q echoes the recovery key's value", err)
	}
}

// ⛔ Vercel's runtime sets VERCEL on every deployment: an unset mode there is
// never a laptop.
func TestAnUnsetModeOnVercelStopsTheBoot(t *testing.T) {
	err := bootModeError(map[string]string{"VERCEL": "1"}, false)
	if err == nil || !strings.Contains(err.Error(), capability.ModeEnv) || !strings.Contains(err.Error(), "VERCEL") {
		t.Fatalf("an unset mode on Vercel: err=%v, want a refusal naming POLIS_MODE and VERCEL", err)
	}
}

// ⭐ The laptop: nothing set, nothing to configure.
func TestAnUnsetModeWithNothingRemoteRunsAsLocal(t *testing.T) {
	getenv := envOf(nil)
	m, _, _ := capability.ResolveSelfHosted(getenv)
	if m != mode.Local {
		t.Fatalf("unset resolved to %q, want local", m)
	}
	if err := bootModeError(nil, false); err != nil {
		t.Errorf("a laptop with nothing set refused to start: %v", err)
	}
}

// ⭐ An explicit local is a choice, so it runs even beside a remote-only signal.
func TestAnExplicitLocalModeWithARecoveryKeyRuns(t *testing.T) {
	if err := bootModeError(map[string]string{capability.ModeEnv: "local", owner.RecoveryKeyEnv: "k", "VERCEL": "1"}, false); err != nil {
		t.Errorf("POLIS_MODE=local with a recovery key refused: %v", err)
	}
}

// ⛔ --dev does not exempt the refusal: a dev server with a remote-only signal
// and no mode is the confused case, not the developer's laptop.
func TestDevDoesNotExemptAnUnsetModeBesideARemoteSignal(t *testing.T) {
	for _, kv := range []map[string]string{{owner.RecoveryKeyEnv: "k"}, {"VERCEL": "1"}} {
		if err := bootModeError(kv, true); err == nil {
			t.Errorf("--dev with %v and no mode started", kv)
		}
	}
	if err := bootModeError(nil, true); err != nil {
		t.Errorf("--dev on a laptop with nothing set refused: %v", err)
	}
}

// The refusal used to read "…. Running as local — refusing to start", which
// says two contradictory things.
func TestTheModeRefusalNoLongerSaysRunningAsLocal(t *testing.T) {
	for _, raw := range []string{"remote-server-sined", "custodied"} {
		err := bootModeError(map[string]string{capability.ModeEnv: raw}, false)
		if err == nil {
			t.Fatalf("POLIS_MODE=%s started", raw)
		}
		if strings.Contains(err.Error(), "Running as local") {
			t.Errorf("refusal still says it is running as local: %q", err)
		}
	}
}
