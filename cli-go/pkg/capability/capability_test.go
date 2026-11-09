package capability

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/vdibart/polis-cli/cli-go/pkg/mode"
)

// ⭐ The table from RD6, written out independently of the implementation so the
// test is a second statement of the decision rather than a restatement of the
// code. ⛔ If these disagree, the README's table is right.
func TestTheTableIsRD6s(t *testing.T) {
	want := map[mode.Mode]Set{
		mode.Local: {
			CanRunHooks: true,
		},
		mode.RemoteClientSigned: {
			HasPublicApi:        true,
			ActorsRunOnSchedule: true,
		},
		mode.RemoteServerSigned: {
			HasPublicApi:              true,
			ActorsRunOnSchedule:       true,
			InstanceCanSignUnattended: true,
		},
		mode.Custodied: {
			HasPublicApi:              true,
			HasOperator:               true,
			ActorsRunOnSchedule:       true,
			InstanceCanSignUnattended: true,
		},
	}
	for _, m := range mode.All() {
		expected := want[m]
		expected.V = Version
		if got := For(m); !reflect.DeepEqual(got, expected) {
			t.Errorf("For(%s):\n  got  %+v\n  want %+v", m, got, expected)
		}
	}
	// ⛔ Non-vacuity: every mode must be covered, so a fifth mode fails here
	// rather than quietly deriving an all-false set.
	if len(want) != len(mode.All()) {
		t.Fatalf("the table covers %d modes but there are %d", len(want), len(mode.All()))
	}
}

// ⭐⭐ The capability that earns the flavour split. That it is the ONLY
// difference between the two remote modes is evidence the split is
// load-bearing and narrow, so the test states it directly.
func TestOnlyUnattendedSigningSeparatesTheRemoteFlavours(t *testing.T) {
	self, server := For(mode.RemoteClientSigned), For(mode.RemoteServerSigned)
	if self.InstanceCanSignUnattended || !server.InstanceCanSignUnattended {
		t.Fatal("instanceCanSignUnattended does not split the remote flavours")
	}
	self.InstanceCanSignUnattended = true
	if !reflect.DeepEqual(self, server) {
		t.Errorf("the remote flavours differ in more than unattended signing:\n  %+v\n  %+v", self, server)
	}
}

// ⛔ A name that leaks the topology defeats the reason capabilities cross the
// boundary at all: telling a third party `mode = "remote-server-signed"`
// publishes our deployment shape as a contract.
func TestNoCapabilityNameLeaksTheTopology(t *testing.T) {
	b, err := json.Marshal(For(mode.Custodied))
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var fields map[string]interface{}
	if err := json.Unmarshal(b, &fields); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	banned := []string{"hosted", "remote", "local", "custodied"}
	for name := range fields {
		if name == "v" {
			continue
		}
		lower := strings.ToLower(name)
		for _, bad := range banned {
			if strings.Contains(lower, bad) {
				t.Errorf("capability %q contains %q; a name must not publish the topology", name, bad)
			}
		}
	}
	if len(fields) < 6 {
		t.Errorf("expected the version plus five capabilities, got %d fields", len(fields))
	}
}

// ⭐ Versioned from day one, while only our own surface reads it.
func TestDescriptorCarriesItsVersion(t *testing.T) {
	for _, m := range mode.All() {
		if got := For(m).V; got != Version {
			t.Errorf("For(%s).V = %q, want %q", m, got, Version)
		}
	}
	b, _ := json.Marshal(For(mode.Local))
	if !strings.Contains(string(b), `"v":"`+Version+`"`) {
		t.Errorf("the serialised descriptor has no version: %s", b)
	}
}

// ⛔ No backend property may appear here. Those vary by storage backend, not by
// mode, and folding them in reopens a closed audit.
func TestNoBackendPropertyLeakedIn(t *testing.T) {
	b, _ := json.Marshal(For(mode.Custodied))
	for _, bad := range []string{"posix", "path", "mode", "stat", "blob", "filesystem"} {
		if strings.Contains(strings.ToLower(string(b)), `"`+bad) {
			t.Errorf("the descriptor carries %q, which is a storage-backend property", bad)
		}
	}
}

// ⭐ The dev flag is gone; a developer says which mode to be.
func TestSelfHostedDefaultsToLocalAndTakesAnExplicitMode(t *testing.T) {
	env := func(v string) func(string) string {
		return func(k string) string {
			if k == ModeEnv {
				return v
			}
			return ""
		}
	}

	if m, note, unset := ResolveSelfHosted(env("")); m != mode.Local || note != "" || !unset {
		t.Errorf("unset should be local with no note and report unset, got %q %q %v", m, note, unset)
	}
	if m, _, unset := ResolveSelfHosted(env("remote-client-signed")); m != mode.RemoteClientSigned || unset {
		t.Errorf("an explicit mode was not honoured, got %q", m)
	}
	// ⚠️ A typo falls back to local, and SAYS SO. Not fatal — a developer's
	// shell typo must not stop their site serving — but not silent either.
	m, note, _ := ResolveSelfHosted(env("remote"))
	if m != mode.Local {
		t.Errorf("an invalid mode should fall back to local, got %q", m)
	}
	if note == "" {
		t.Error("an invalid mode fell back silently; the developer must be told")
	}
	// ⛔ A self-hosted instance may not claim to be custodied: it has no
	// accounts, no provisioning and no login redirect.
	m, note, _ = ResolveSelfHosted(env("custodied"))
	if m != mode.Local {
		t.Errorf("self-hosted claimed custodied, got %q", m)
	}
	if !strings.Contains(note, "no operator") {
		t.Errorf("the refusal should say why: %q", note)
	}
}

// ⛔ An unset mode and an explicit local both run as local, but only the first
// is unset: the server refuses an unset mode beside a remote-only setting and
// honours an explicit local.
func TestResolveSelfHostedReportsAnUnsetMode(t *testing.T) {
	for _, c := range []struct {
		raw   string
		unset bool
	}{{"", true}, {"   ", true}, {"local", false}, {"remote-server-signed", false}, {"remote", false}} {
		m, _, unset := ResolveSelfHosted(func(k string) string {
			if k == ModeEnv {
				return c.raw
			}
			return ""
		})
		if unset != c.unset {
			t.Errorf("POLIS_MODE=%q: unset=%v, want %v", c.raw, unset, c.unset)
		}
		if c.raw == "local" && m != mode.Local {
			t.Errorf("explicit local resolved to %q", m)
		}
	}
}

// ⛔ A constant, not a lookup: polis-hosted IS the custodied case, and reading
// it from the environment would let a misconfiguration publish a set the
// deployment does not match.
func TestHostedModeIsNotConfigurable(t *testing.T) {
	if ForHosted() != mode.Custodied {
		t.Errorf("ForHosted() = %q, want custodied", ForHosted())
	}
	if !For(ForHosted()).HasOperator {
		t.Error("the hosted descriptor must report an operator")
	}
}
