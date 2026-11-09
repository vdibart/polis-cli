package mode

import "testing"

func TestAllCoversEveryDeclaredMode(t *testing.T) {
	// ⭐ The point of All() is that a table ranging over it fails when a fifth
	// mode appears, rather than silently acquiring the zero value. So the count
	// is asserted: adding a mode without adding it to All() breaks here.
	if n := len(All()); n != 4 {
		t.Fatalf("All() has %d modes, want 4 — add the new one to All() so every "+
			"table that ranges over it is forced to account for it", n)
	}
	seen := map[Mode]bool{}
	for _, m := range All() {
		if seen[m] {
			t.Errorf("%q appears twice in All()", m)
		}
		seen[m] = true
		if !Valid(m) {
			t.Errorf("%q is in All() but Valid() rejects it", m)
		}
	}
}

// ⛔ The bug this package removes: a remote instance with no mode configured
// concluded it was a laptop. Defaulting is the failure, so Parse refuses.
func TestParseRefusesRatherThanDefaulting(t *testing.T) {
	if _, err := Parse(""); err == nil {
		t.Error("Parse(\"\") succeeded; an unset mode must be a configuration error, not a default")
	}
	for _, bad := range []string{"hosted", "remote", "LOCAL", "custodial"} {
		if _, err := Parse(bad); err == nil {
			t.Errorf("Parse(%q) succeeded, and it is not one of the four", bad)
		}
	}
	for _, m := range All() {
		got, err := Parse(string(m))
		if err != nil || got != m {
			t.Errorf("Parse(%q) = %q, %v", m, got, err)
		}
	}
}

// ⚠️ "remote" collapses two flavours that differ on the one question the
// secrets seam cares about, so the helper must not be mistaken for a mode.
func TestIsRemoteCoversBothFlavoursAndNothingElse(t *testing.T) {
	if !IsRemote(RemoteClientSigned) || !IsRemote(RemoteServerSigned) {
		t.Error("IsRemote missed a remote flavour")
	}
	if IsRemote(Local) || IsRemote(Custodied) {
		t.Error("IsRemote accepted a non-remote mode")
	}
	if Valid(Mode("remote")) {
		t.Error(`"remote" is valid as a mode; it must not be — the flavours differ on ` +
			`whether the instance can sign unattended, which is what decides the identity key`)
	}
}
