package dm

import "testing"

// TestDefaultArgon2idParamsLocked pins the default Argon2id cost. These values are
// recorded per-epoch in keyring.json (so old blobs always decrypt with their own
// params), documented in FORMAT.md, and baked into the cross-impl vector
// (vectors_test.go). Changing them is a conscious decision — update FORMAT.md and
// regenerate the cross-impl vector deliberately, and remember the browser WASM must
// match. This guard exists so a casual tweak can't silently diverge the two
// implementations. (Task 1.9: lock the deferred parameters.)
func TestDefaultArgon2idParamsLocked(t *testing.T) {
	if defaultArgon2Time != 3 || defaultArgon2Memory != 64*1024 || defaultArgon2Threads != 4 {
		t.Fatalf("Argon2id defaults changed to t=%d m=%dKiB p=%d — if intentional, update FORMAT.md, the cross-impl vector consts, and confirm the browser WASM matches",
			defaultArgon2Time, defaultArgon2Memory, defaultArgon2Threads)
	}
	// 12-word recovery (128-bit) — the locked starting size (upgradeable to 24).
	if recoveryEntropyBits != 128 {
		t.Fatalf("recovery entropy = %d bits, want 128 (12-word BIP39)", recoveryEntropyBits)
	}
}
