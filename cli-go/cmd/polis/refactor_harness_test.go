package main

import (
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
)

// TestRefactorHarnessCompiles type-checks the refactor differential's two Go
// drivers against THIS cli-go, so a signature change that breaks them fails here
// instead of in the hands of the next person who needs the harness mid-refactor.
//
// ⛔ The drivers live outside both modules on purpose (in the maintainers' operations
// scripts, which copy them into a throwaway tree at run time), so `go build ./...`
// never reaches them, and they rotted on the first refactor after they were
// written. Nothing else builds them: this test is the guard.
//
// ⭐ It builds through -overlay, mapping the two absent cmd/ paths to the script
// files, so nothing is copied into the module and the harness stays out of it.
// No differential runs: this is a build, a fraction of a second warm.
//
// Only the working tree is checked. setup.sh still tolerates probetool failing to
// compile at an OLD ref; at HEAD both drivers must build, and a cli-go change that
// breaks one updates it in the same commit.
//
// Skips where the harness is absent: the public repo carries cli-go, not scripts/.
func TestRefactorHarnessCompiles(t *testing.T) {
	harness := filepath.Join(repoRoot, "scripts", "refactor-differential")
	if _, err := os.Stat(harness); errors.Is(err, fs.ErrNotExist) {
		t.Skip("no refactor differential in this tree (the public repo carries cli-go but not scripts/)")
	} else if err != nil {
		t.Fatalf("cannot look for the harness: %v", err)
	}

	cliGo, err := filepath.Abs(filepath.Join(repoRoot, "cli-go"))
	if err != nil {
		t.Fatal(err)
	}
	harnessAbs, err := filepath.Abs(harness)
	if err != nil {
		t.Fatal(err)
	}
	drivers := map[string]string{
		"difftool":  filepath.Join(harnessAbs, "actors", "difftool.go"),
		"probetool": filepath.Join(harnessAbs, "probes", "probetool.go"),
	}
	replace := map[string]string{}
	for name, src := range drivers {
		if _, err := os.Stat(src); err != nil {
			t.Fatalf("the harness has no %s driver at %s: %v", name, src, err)
		}
		replace[filepath.Join(cliGo, "cmd", name, "main.go")] = src
	}
	tmp := t.TempDir()
	overlay := filepath.Join(tmp, "overlay.json")
	b, err := json.Marshal(map[string]any{"Replace": replace})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(overlay, b, 0o600); err != nil {
		t.Fatal(err)
	}

	goBin := filepath.Join(runtime.GOROOT(), "bin", "go")
	if _, err := os.Stat(goBin); err != nil {
		if goBin, err = exec.LookPath("go"); err != nil {
			t.Skip("no go toolchain to build with")
		}
	}
	for name := range drivers {
		cmd := exec.Command(goBin, "build", "-overlay", overlay, "-o", filepath.Join(tmp, name), "./cmd/"+name)
		cmd.Dir = cliGo
		cmd.Env = append(os.Environ(), "GOWORK=off") // as setup.sh builds it
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Errorf("the refactor differential's %s driver no longer compiles against this cli-go.\n"+
				"Update %s in the same commit as the change that broke it.\n\n%s",
				name, drivers[name], out)
		}
	}
}
