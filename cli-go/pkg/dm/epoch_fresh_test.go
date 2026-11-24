package dm

import (
	"encoding/base64"
	"strings"
	"testing"
)

func passwordKeyring(t *testing.T) *Keyring {
	t.Helper()
	pub := base64.StdEncoding.EncodeToString(make([]byte, 32))
	k := &Keyring{}
	if _, err := k.AddBootstrapEpoch(); err != nil {
		t.Fatal(err)
	}
	if _, err := k.AddPasswordEpoch(pub, "wrap-1", "rec-1", KDFParams{Algo: "argon2id", Salt: "AAA="}, KDFParams{Algo: "hkdf-sha256", Salt: "BBB="}); err != nil {
		t.Fatal(err)
	}
	return k
}

// ⭐ A fresh key (spec 2.3, "Lost both"): a new epoch becomes current, and the
// earlier one keeps both its wraps, so its messages wait for the old password
// or phrase. Only wrapped blobs arrive.
func TestAFreshKeyLeavesTheEarlierEpochSealedNotGone(t *testing.T) {
	k := passwordKeyring(t)
	prev := k.Current
	pub := base64.StdEncoding.EncodeToString(make([]byte, 32))
	id, err := k.AddFreshPasswordEpoch(pub, "wrap-2", "rec-2", KDFParams{Algo: "argon2id", Salt: "CCC="}, KDFParams{Algo: "hkdf-sha256", Salt: "DDD="})
	if err != nil {
		t.Fatal(err)
	}
	if k.Current != id || id == prev {
		t.Fatalf("current = %d, want the new epoch %d", k.Current, id)
	}
	old, _ := k.EpochByID(prev)
	if old.WrappedDEK != "wrap-1" || old.WrappedDEKRecovery != "rec-1" {
		t.Errorf("the earlier epoch's wraps changed: %+v", old)
	}
	// Refused: before any password, a bad key, a missing wrap, a wrong KDF.
	boot := &Keyring{}
	_, _ = boot.AddBootstrapEpoch()
	if _, err := boot.AddFreshPasswordEpoch(pub, "w", "r", KDFParams{Algo: "argon2id"}, KDFParams{Algo: "hkdf-sha256"}); err == nil || !strings.Contains(err.Error(), "set a password first") {
		t.Errorf("a fresh key with no password set = %v, want refused", err)
	}
	for name, try := range map[string]func() error{
		"bad key": func() error {
			_, e := k.AddFreshPasswordEpoch("x", "w", "r", KDFParams{Algo: "argon2id"}, KDFParams{Algo: "hkdf-sha256"})
			return e
		},
		"no recovery": func() error {
			_, e := k.AddFreshPasswordEpoch(pub, "w", "", KDFParams{Algo: "argon2id"}, KDFParams{Algo: "hkdf-sha256"})
			return e
		},
		"wrong kdf": func() error {
			_, e := k.AddFreshPasswordEpoch(pub, "w", "r", KDFParams{Algo: "pbkdf2"}, KDFParams{Algo: "hkdf-sha256"})
			return e
		},
	} {
		if try() == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}

// ⭐ "Keep unlocked": an earlier epoch's password wrap is replaced; its
// recovery wrap stays; the current epoch and the bootstrap one are refused.
func TestAnEarlierEpochIsWrappedAgainUnderTheCurrentPassword(t *testing.T) {
	k := passwordKeyring(t)
	earlier := k.Current
	pub := base64.StdEncoding.EncodeToString(make([]byte, 32))
	if _, err := k.AddFreshPasswordEpoch(pub, "wrap-2", "rec-2", KDFParams{Algo: "argon2id", Salt: "CCC="}, KDFParams{Algo: "hkdf-sha256", Salt: "DDD="}); err != nil {
		t.Fatal(err)
	}
	rev := k.Revision
	if err := k.RewrapEarlierEpoch(earlier, "wrap-1-again", &KDFParams{Algo: "argon2id", Salt: "EEE="}); err != nil {
		t.Fatal(err)
	}
	ep, _ := k.EpochByID(earlier)
	if ep.WrappedDEK != "wrap-1-again" || ep.KDF.Salt != "EEE=" || ep.WrappedDEKRecovery != "rec-1" {
		t.Errorf("earlier epoch after re-wrap = %+v", ep)
	}
	if k.Revision != rev+1 {
		t.Errorf("revision = %d, want %d", k.Revision, rev+1)
	}
	if err := k.RewrapEarlierEpoch(k.Current, "w", &KDFParams{Algo: "argon2id"}); err == nil {
		t.Error("re-wrapping the current epoch this way was accepted")
	}
	if err := k.RewrapEarlierEpoch(0, "w", &KDFParams{Algo: "argon2id"}); err == nil {
		t.Error("re-wrapping the bootstrap epoch was accepted")
	}
	if err := k.RewrapEarlierEpoch(earlier, "", &KDFParams{Algo: "argon2id"}); err == nil {
		t.Error("an empty wrap was accepted")
	}
}
