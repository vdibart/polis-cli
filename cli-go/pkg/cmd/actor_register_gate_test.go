package cmd

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/vdibart/polis-cli/cli-go/pkg/actor"
	"github.com/vdibart/polis-cli/cli-go/pkg/site"
)

// registerGateSite is a site with a .well-known/polis and no actor registry —
// the state most sites are in, because most sites run no actors.
func registerGateSite(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	wk := filepath.Join(dir, ".well-known")
	if err := os.MkdirAll(wk, 0755); err != nil {
		t.Fatal(err)
	}
	body := `{"version":"2.0","public_key":"ssh-ed25519 AAAA","author_name":"Operator","created":"2026-09-14T00:00:00Z"}`
	if err := os.WriteFile(filepath.Join(wk, "polis"), []byte(body), 0644); err != nil {
		t.Fatal(err)
	}
	return dir
}

// registerGateRegistryFile writes a registry file at the path the gate checks.
func registerGateRegistryFile(t *testing.T, dir string) {
	t.Helper()
	p := actor.RegistryPath(dir)
	if err := os.MkdirAll(filepath.Dir(p), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(`{"v":1,"operator":"example.com","actors":[]}`), 0644); err != nil {
		t.Fatal(err)
	}
}

func registerGatePointer(t *testing.T, dir string) {
	t.Helper()
	if err := site.SetActorRegistryPointer(dir, "/content/pub.polis.core/actor/registry.json"); err != nil {
		t.Fatal(err)
	}
}

// An ordinary site — no pointer, no registry file — must no-op. Before this gate
// a single run created a registry and made the site an operator.
func TestRegisterGate_OrdinarySiteNoops(t *testing.T) {
	dir := registerGateSite(t)
	if got := registerGate(dir); got != registerNoop {
		t.Fatalf("an ordinary site must no-op (registerNoop=%d), got %d", registerNoop, got)
	}
}

// An operator — pointer published, registry present — proceeds.
func TestRegisterGate_OperatorProceeds(t *testing.T) {
	dir := registerGateSite(t)
	registerGateRegistryFile(t, dir)
	registerGatePointer(t, dir)
	if got := registerGate(dir); got != registerProceed {
		t.Fatalf("an operator must proceed (registerProceed=%d), got %d", registerProceed, got)
	}
}

// The documented recovery: the registry file was deleted but the pointer
// survives, and re-running register must rebuild it. This is why the ALLOW
// decision keys on the pointer and never on the file.
func TestRegisterGate_RecoveryPointerWithoutFileProceeds(t *testing.T) {
	dir := registerGateSite(t)
	registerGatePointer(t, dir)
	if _, err := os.Stat(actor.RegistryPath(dir)); err == nil {
		t.Fatal("fixture error: the registry file must be absent for this case")
	}
	if got := registerGate(dir); got != registerProceed {
		t.Fatalf("pointer present with the file missing is the recovery case and must proceed (registerProceed=%d), got %d", registerProceed, got)
	}
}

// A registry file with no pointer is a broken operator. A silent no-op here would
// let an operator believe an update landed when it was dropped.
func TestRegisterGate_FileWithoutPointerIsBroken(t *testing.T) {
	dir := registerGateSite(t)
	registerGateRegistryFile(t, dir)
	if got := registerGate(dir); got != registerBroken {
		t.Fatalf("a registry file with no pointer must be reported broken (registerBroken=%d), got %d", registerBroken, got)
	}
}
