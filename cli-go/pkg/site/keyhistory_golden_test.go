package site

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/vdibart/polis-cli/cli-go/pkg/discovery"
)

// ⛔ THE CHAIN'S WIRE SHAPE IS FROZEN. The goldens under testdata/golden/ named
// genesis.* and rotate.* were written by the code BEFORE key history learned to
// preserve unmodelled members (POLIS_UPDATE_KEYHISTORY_GOLDEN=1). Writing genesis, and recording a rotation,
// must still produce exactly those bytes: a never-rotated site's document does
// not move, and a rotated one gains only its new entry.

const (
	goldenRotationKey = "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIGoldenRotationKeyGoldenRotationKeyGold polis@golden"
	goldenRotationSig = "-----BEGIN SSH SIGNATURE-----\nR09MREVO\n-----END SSH SIGNATURE-----\n"
	goldenValidFrom   = "2026-09-18T12:00:00.123Z"
)

func goldenRotationWitness() discovery.Witness {
	return discovery.Witness{
		Action: discovery.WitnessActionKeyRotation, Domain: "golden.example", OldKey: "old", NewKey: goldenRotationKey,
		Timestamp: goldenValidFrom, TransitionSig: goldenRotationSig, DS: "https://ds.example", DSKeyID: "k1",
		WitnessedAt: "2026-09-18T12:00:01.000Z", Signature: "c2ln",
	}
}

func TestKeyHistoryWritersEmitTheBytesTheyAlwaysHave(t *testing.T) {
	update := os.Getenv("POLIS_UPDATE_KEYHISTORY_GOLDEN") == "1"
	cases := []struct {
		golden, fixture string
		write           func(t *testing.T, dir string)
	}{
		{"genesis.bash_cli_wellknown.json", "bash_cli_wellknown.json", writeGenesis},
		{"genesis.minimal_wellknown.json", "minimal_wellknown.json", writeGenesis},
		{"rotate.bash_cli_wellknown.json", "bash_cli_wellknown.json", rotateOnce},
		{"rotate.bash_rotated_wellknown.json", "bash_rotated_wellknown.json", rotateOnce},
		{"rotate.every_member_wellknown.json", "every_member_wellknown.json", rotateOnce},
		{"genesis-then-rotate.minimal_wellknown.json", "minimal_wellknown.json", func(t *testing.T, dir string) {
			writeGenesis(t, dir)
			rotateOnce(t, dir)
		}},
	}
	for _, c := range cases {
		t.Run(c.golden, func(t *testing.T) {
			dir := t.TempDir()
			wkPath := filepath.Join(dir, ".well-known", "polis")
			if err := os.MkdirAll(filepath.Dir(wkPath), 0755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(wkPath, loadTestdata(t, c.fixture), 0644); err != nil {
				t.Fatal(err)
			}
			c.write(t, dir)
			got, err := os.ReadFile(wkPath)
			if err != nil {
				t.Fatal(err)
			}
			golden := filepath.Join("testdata", "golden", c.golden)
			if update {
				if err := os.WriteFile(golden, got, 0644); err != nil {
					t.Fatal(err)
				}
				return
			}
			want, err := os.ReadFile(golden)
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(got, want) {
				t.Fatalf("the chain's bytes changed:\n--- want\n%s\n--- got\n%s", want, got)
			}
		})
	}
}

func writeGenesis(t *testing.T, dir string) {
	t.Helper()
	if wrote, err := WriteGenesisKeyHistory(dir); err != nil || !wrote {
		t.Fatalf("genesis: wrote=%v err=%v", wrote, err)
	}
}

func rotateOnce(t *testing.T, dir string) {
	t.Helper()
	if err := RecordKeyRotation(dir, goldenRotationKey, goldenRotationSig, goldenValidFrom, goldenRotationWitness()); err != nil {
		t.Fatal(err)
	}
}
