package site

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

// ⛔ .well-known/polis MUST NOT CHANGE BY ONE BYTE when its Extra machinery
// moves onto the shared jsonextra package.
//
// The goldens under testdata/golden/ were written by the code BEFORE the move
// (POLIS_UPDATE_WELLKNOWN_GOLDEN=1), one per fixture: what SaveWellKnown and
// SaveWellKnownRaw emit after loading it. Every well-known on the fleet has
// been through one of those writers, so an unchanged output here means no
// tenant's file re-hashes and Judge's snapshot does not move.
func TestWellKnownWritersEmitTheBytesTheyAlwaysHave(t *testing.T) {
	fixtures := []string{
		"bash_cli_wellknown.json",
		"bash_rotated_wellknown.json",
		"every_member_wellknown.json",
		"legacy_webapp_wellknown.json",
		"minimal_wellknown.json",
	}
	update := os.Getenv("POLIS_UPDATE_WELLKNOWN_GOLDEN") == "1"
	for _, name := range fixtures {
		t.Run(name, func(t *testing.T) {
			for _, writer := range []string{"struct", "raw"} {
				dir := t.TempDir()
				if err := os.MkdirAll(filepath.Join(dir, ".well-known"), 0755); err != nil {
					t.Fatal(err)
				}
				wkPath := filepath.Join(dir, ".well-known", "polis")
				if err := os.WriteFile(wkPath, loadTestdata(t, name), 0644); err != nil {
					t.Fatal(err)
				}
				switch writer {
				case "struct":
					wk, err := LoadWellKnown(dir)
					if err != nil {
						t.Fatal(err)
					}
					if err := SaveWellKnown(dir, wk); err != nil {
						t.Fatal(err)
					}
				case "raw":
					raw, err := LoadWellKnownRaw(dir)
					if err != nil {
						t.Fatal(err)
					}
					if err := SaveWellKnownRaw(dir, raw); err != nil {
						t.Fatal(err)
					}
				}
				got, err := os.ReadFile(wkPath)
				if err != nil {
					t.Fatal(err)
				}
				golden := filepath.Join("testdata", "golden", writer+"."+name)
				if update {
					if err := os.MkdirAll(filepath.Dir(golden), 0755); err != nil {
						t.Fatal(err)
					}
					if err := os.WriteFile(golden, got, 0644); err != nil {
						t.Fatal(err)
					}
					continue
				}
				want, err := os.ReadFile(golden)
				if err != nil {
					t.Fatal(err)
				}
				if !bytes.Equal(got, want) {
					t.Fatalf("%s writer changed the bytes of %s:\n--- want\n%s\n--- got\n%s", writer, name, want, got)
				}
			}
		})
	}
}
