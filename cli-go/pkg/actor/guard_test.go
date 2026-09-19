package actor

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vdibart/polis-cli/cli-go/pkg/site"
)

// operatorSite writes a minimal site directory publishing pub, and returns its
// path. It deliberately does not go through site.Init — the Guard must work off
// nothing but .well-known/polis and the registry file.
func operatorSite(t *testing.T, tenantsDir, handle string, pub []byte) string {
	t.Helper()
	siteDir := filepath.Join(tenantsDir, handle)
	if err := os.MkdirAll(filepath.Join(siteDir, ".well-known"), 0755); err != nil {
		t.Fatal(err)
	}
	wk := map[string]string{
		"version":     "2.0",
		"public_key":  string(pub),
		"author_name": handle,
		"created":     "2026-09-05T00:00:00Z",
	}
	data, _ := json.Marshal(wk)
	if err := os.WriteFile(filepath.Join(siteDir, ".well-known", "polis"), data, 0644); err != nil {
		t.Fatal(err)
	}
	return siteDir
}

func plainTenant(t *testing.T, tenantsDir, handle string) {
	t.Helper()
	_, pub := keypair(t)
	operatorSite(t, tenantsDir, handle, pub)
}

func registryFor(operator string, domains ...string) *Registry {
	r := &Registry{
		V:         SchemaVersion,
		Operator:  operator,
		Asserted:  "2026-09-05T14:02:00Z",
		Generator: "polis-cli-go/test",
	}
	for _, d := range domains {
		r.Actors = append(r.Actors, Entry{
			Domain:          d,
			Authority:       AuthorityOperator,
			ExpectedActions: []string{"pub.polis.attestation.integrity"},
		})
	}
	return r
}

func TestLoadGuard_FindsOperatorByPointerAndVerifies(t *testing.T) {
	tenantsDir := t.TempDir()
	priv, pub := keypair(t)
	opDir := operatorSite(t, tenantsDir, "polis", pub)
	plainTenant(t, tenantsDir, "alice")

	r := registryFor("polis.polis.pub", "judge.polis.pub", "polis.polis.pub")
	if err := StateRegistry(opDir, r, priv); err != nil {
		t.Fatalf("StateRegistry: %v", err)
	}

	g := LoadGuard(tenantsDir)
	if g.Bootstrap() {
		t.Fatalf("guard bootstrapped despite a valid registry: %v", g.Problems)
	}
	if len(g.Problems) != 0 {
		t.Errorf("unexpected problems: %v", g.Problems)
	}
	if !g.IsActorDomain("judge.polis.pub") {
		t.Error("judge.polis.pub should be a known actor domain")
	}
	if !g.IsActorDomain("polis.polis.pub") {
		t.Error("an operator that lists itself should be a known actor domain")
	}
	if g.IsActorDomain("alice.polis.pub") {
		t.Error("an ordinary tenant must never read as an actor site")
	}
	if e := g.EntryFor("judge.polis.pub"); e == nil || e.Authority != AuthorityOperator {
		t.Errorf("entry lookup returned %+v", e)
	}
	if ops := g.Operators(); len(ops) != 1 || ops[0] != "polis" {
		t.Errorf("operators = %v, want [polis]", ops)
	}
}

// TestLoadGuard_BootstrapExcludesNothing — no registry anywhere is exactly the
// state the fleet is in today, so it must degrade to current behaviour rather
// than stopping a sweep.
func TestLoadGuard_BootstrapExcludesNothing(t *testing.T) {
	tenantsDir := t.TempDir()
	plainTenant(t, tenantsDir, "alice")
	plainTenant(t, tenantsDir, "bob")

	g := LoadGuard(tenantsDir)
	if !g.Bootstrap() {
		t.Error("a fleet with no registry must report bootstrap")
	}
	if g.IsActorDomain("alice.polis.pub") {
		t.Error("bootstrap must exclude nothing")
	}
	found, trusted := g.Registries()
	if found != 0 || trusted != 0 {
		t.Errorf("found=%d trusted=%d, want 0/0", found, trusted)
	}
}

// TestLoadGuard_TamperedRegistryIsNotTrusted is the reason the signature check
// is not optional. The registry sits in a tenant-shaped directory that Medic
// and Patrol write to, so an actor must not trust the bytes merely because they
// are on our own disk.
//
// ⛔ And note the direction of the failure: an untrusted registry excludes
// NOTHING. Trusting it would let anyone who can write one file remove any
// tenant from Rosie's or Reaper's reach by naming it an actor.
func TestLoadGuard_TamperedRegistryIsNotTrusted(t *testing.T) {
	tenantsDir := t.TempDir()
	priv, pub := keypair(t)
	opDir := operatorSite(t, tenantsDir, "polis", pub)

	r := registryFor("polis.polis.pub", "judge.polis.pub")
	if err := StateRegistry(opDir, r, priv); err != nil {
		t.Fatal(err)
	}

	// Someone adds an entry after signing.
	loaded, _ := Load(RegistryPath(opDir))
	loaded.Actors = append(loaded.Actors, Entry{Domain: "alice.polis.pub", Authority: AuthorityOperator})
	data, _ := json.MarshalIndent(loaded, "", "  ")
	os.WriteFile(RegistryPath(opDir), data, 0644)

	g := LoadGuard(tenantsDir)
	if g.IsActorDomain("alice.polis.pub") {
		t.Fatal("a tampered registry was trusted — a tenant could be hidden from Rosie and Reaper")
	}
	if g.IsActorDomain("judge.polis.pub") {
		t.Error("nothing in an untrusted registry may be trusted, including the parts that were genuine")
	}
	if !g.Bootstrap() {
		t.Error("with no trusted registry the guard must report bootstrap, so the caller says so out loud")
	}
	if len(g.Problems) == 0 {
		t.Error("a tampered registry must be reported, not silently ignored")
	}
	if !strings.Contains(strings.Join(g.Problems, " "), "invalid") {
		t.Errorf("expected an 'invalid' problem, got: %v", g.Problems)
	}
}

// TestLoadGuard_DeletedRegistryIsLoud — D12's deletion case. The pointer stays
// and the file goes, and without an explicit report the checks would quietly
// stop and look exactly like a fleet that has no actors.
func TestLoadGuard_DeletedRegistryIsLoud(t *testing.T) {
	tenantsDir := t.TempDir()
	priv, pub := keypair(t)
	opDir := operatorSite(t, tenantsDir, "polis", pub)
	if err := StateRegistry(opDir, registryFor("polis.polis.pub", "judge.polis.pub"), priv); err != nil {
		t.Fatal(err)
	}
	os.Remove(RegistryPath(opDir))

	g := LoadGuard(tenantsDir)
	if len(g.Problems) == 0 {
		t.Fatal("a pointer to a missing registry must be reported")
	}
	if !strings.Contains(strings.Join(g.Problems, " "), "missing") {
		t.Errorf("expected a 'missing' problem, got: %v", g.Problems)
	}
	if found, trusted := g.Registries(); found != 1 || trusted != 0 {
		t.Errorf("found=%d trusted=%d, want 1/0 — the pointer is evidence a registry should exist", found, trusted)
	}
}

// TestLoadGuard_WrongKeyIsNotTrusted covers the operator whose key rotated
// without the registry being re-signed. It is the same outcome as tampering,
// deliberately: the guard cannot tell the two apart and must not guess.
func TestLoadGuard_WrongKeyIsNotTrusted(t *testing.T) {
	tenantsDir := t.TempDir()
	priv, _ := keypair(t)
	_, otherPub := keypair(t)
	opDir := operatorSite(t, tenantsDir, "polis", otherPub)
	if err := StateRegistry(opDir, registryFor("polis.polis.pub", "judge.polis.pub"), priv); err != nil {
		t.Fatal(err)
	}

	g := LoadGuard(tenantsDir)
	if g.IsActorDomain("judge.polis.pub") {
		t.Error("a registry that does not verify against the operator's published key must not be trusted")
	}
	if len(g.Problems) == 0 {
		t.Error("expected a reported problem")
	}
}

// TestLoadGuard_NoHardcodedOperatorHandle — the check must work identically
// self-hosted, where the operator's handle is whatever that person chose. If
// anything here hardcodes "polis", this fails.
func TestLoadGuard_NoHardcodedOperatorHandle(t *testing.T) {
	tenantsDir := t.TempDir()
	priv, pub := keypair(t)
	opDir := operatorSite(t, tenantsDir, "selfhoster", pub)
	if err := StateRegistry(opDir, registryFor("selfhoster.example", "myjudge.example"), priv); err != nil {
		t.Fatal(err)
	}

	g := LoadGuard(tenantsDir)
	if !g.IsActorDomain("myjudge.example") {
		t.Fatal("a self-hoster's own registry was not honoured — something hardcodes our operator")
	}
	if ops := g.Operators(); len(ops) != 1 || ops[0] != "selfhoster" {
		t.Errorf("operators = %v", ops)
	}
}

// TestLoadGuard_UnreadableTenantsDirDoesNotPanic — a sweep must not be
// stoppable by the guard.
func TestLoadGuard_UnreadableTenantsDirDoesNotPanic(t *testing.T) {
	g := LoadGuard(filepath.Join(t.TempDir(), "does-not-exist"))
	if !g.Bootstrap() {
		t.Error("a missing tenants directory must bootstrap, not claim knowledge")
	}
	if len(g.Problems) == 0 {
		t.Error("it must still be reported")
	}
}

// TestWithdrawRegistry_ClearsPointerAndFile — a stale pointer to a deleted file
// publishes a 404 where a fact used to be.
func TestWithdrawRegistry_ClearsPointerAndFile(t *testing.T) {
	tenantsDir := t.TempDir()
	priv, pub := keypair(t)
	opDir := operatorSite(t, tenantsDir, "polis", pub)
	if err := StateRegistry(opDir, registryFor("polis.polis.pub", "judge.polis.pub"), priv); err != nil {
		t.Fatal(err)
	}

	if err := WithdrawRegistry(opDir); err != nil {
		t.Fatalf("WithdrawRegistry: %v", err)
	}
	if p := site.ActorRegistryPointer(opDir); p != "" {
		t.Errorf("pointer survived withdrawal: %q", p)
	}
	if _, err := os.Stat(RegistryPath(opDir)); !os.IsNotExist(err) {
		t.Error("registry file survived withdrawal")
	}
	if g := LoadGuard(tenantsDir); !g.Bootstrap() || len(g.Problems) != 0 {
		t.Errorf("a clean withdrawal must leave no problems: bootstrap=%v problems=%v", g.Bootstrap(), g.Problems)
	}
}

// TestRegistryURLPath_PointsAtTheSignedSource — Law 1's corollary. A pointer
// into the rendered mount would address a projection, which can be re-rendered,
// re-themed or moved; the content path is what the signature covers.
func TestRegistryURLPath_PointsAtTheSignedSource(t *testing.T) {
	got := RegistryURLPath("/data/tenants/polis")
	want := "/content/pub.polis.core/actor/registry.json"
	if got != want {
		t.Errorf("RegistryURLPath = %q, want %q", got, want)
	}
	if strings.HasPrefix(got, "/actors/") {
		t.Error("the pointer addresses the mount — that is a projection, not the signed source")
	}
}
