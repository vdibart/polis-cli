package owner

import (
	"testing"
	"time"
)

// ⛔ A session older than the window is not fresh: a stolen 12-hour session
// cannot enrol or remove a way to sign in. A fresh one can. ⭐ The issue time
// is inside the MAC, so another process computes the same answer.
func TestASessionIsFreshOnlyWithinTheWindow(t *testing.T) {
	now := time.Unix(1_800_000_000, 0)
	inst := remoteInstance(t)
	inst.Cache.now = func() time.Time { return now }
	tok, _, err := OpenSignIn(inst, "passkey")
	if err != nil {
		t.Fatal(err)
	}
	if !SignedInWithin(inst, tok, 10*time.Minute) {
		t.Fatal("a just-opened session is not fresh")
	}
	now = now.Add(11 * time.Minute)
	if !SignedIn(inst, tok) {
		t.Fatal("the session should still be live")
	}
	if SignedInWithin(inst, tok, 10*time.Minute) {
		t.Error("an 11-minute-old session counted as fresh")
	}
}
