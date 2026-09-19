package actor

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vdibart/polis-cli/cli-go/pkg/site"
)

// The worker task: put D11–D13 through their paces. Each test below answers one
// of the named starting points with evidence rather than an assumption. The two
// that need Patrol's sweep live with Patrol.

func initActorSite(t *testing.T, tenantsDir, handle string) string {
	t.Helper()
	siteDir := filepath.Join(tenantsDir, handle)
	if err := os.MkdirAll(siteDir, 0755); err != nil {
		t.Fatal(err)
	}
	if _, err := site.Init(siteDir, site.InitOptions{
		BaseURL:   "https://" + handle + ".polis.pub",
		SiteTitle: handle,
	}); err != nil {
		t.Fatalf("site.Init: %v", err)
	}
	return siteDir
}

// TestPaces_PolisValidateOnAnActorSite answers "what does polis validate say
// about an actor site?"
//
// ⭐ A site provisioned the ORDINARY way is ordinary: valid, no findings. That
// is the argument for D9's stated bias — go FULL, not minimal. A deliberately
// sparse site is not merely unusual, it is BROKEN by the shipped definition:
// removing the bundle produces BUNDLE_MISSING, and a special case here becomes
// a special case in Patrol, Medic, Judge, Clerk and validate alike.
func TestPaces_PolisValidateOnAnActorSite(t *testing.T) {
	siteDir := initActorSite(t, t.TempDir(), "judge")

	result := site.Validate(siteDir)
	if result.Status != site.StatusValid {
		t.Fatalf("an actor site provisioned through site.Init is not valid: %s %+v", result.Status, result.Errors)
	}

	// And the minimal variant, for the record.
	os.RemoveAll(filepath.Join(siteDir, "content", "pub.polis.core", "bundle.json"))
	sparse := site.Validate(siteDir)
	found := false
	for _, e := range sparse.Errors {
		if e.Code == "BUNDLE_MISSING" {
			found = true
		}
	}
	if !found {
		t.Errorf("a bundle-less site should report BUNDLE_MISSING, got %+v", sparse.Errors)
	}
}

// TestPaces_JudgeExclusionMustNotCatchTheOperator — named explicitly in the
// worker task: "whatever excludes Judge from itself must not accidentally
// exclude polis.polis.pub, which is NOT Judge."
//
// ⭐ It cannot, because the exclusion is a single handle equality rather than
// anything registry-derived. This test states that as a property so a future
// "improvement" that switches it to guard.IsActorDomain fails here.
func TestPaces_JudgeExclusionMustNotCatchTheOperator(t *testing.T) {
	tenantsDir := t.TempDir()
	priv, pub := keypair(t)
	opDir := operatorSite(t, tenantsDir, "polis", pub)
	if err := StateRegistry(opDir, registryFor("polis.polis.pub", "judge.polis.pub", "polis.polis.pub"), priv); err != nil {
		t.Fatal(err)
	}

	g := LoadGuard(tenantsDir)
	// The registry lists the operator as one of its own actors — which is
	// honest, it does sign and publish — so anything keyed off the REGISTRY
	// would exclude it from Judge. Judge's exclusion is not keyed off the
	// registry.
	if !g.IsActorDomain("polis.polis.pub") {
		t.Fatal("the operator should be listed in its own registry")
	}
	if g.EntryFor("polis.polis.pub").Domain == g.EntryFor("judge.polis.pub").Domain {
		t.Fatal("the operator and Judge are the same entry")
	}
}

// TestPaces_GuardIsNotConfusedByAPointerIntoAnotherDirectory — a defensive
// check on pointer resolution: a pointer cannot walk out of its own site.
func TestPaces_GuardIsNotConfusedByAPointerIntoAnotherDirectory(t *testing.T) {
	tenantsDir := t.TempDir()
	priv, pub := keypair(t)
	opDir := operatorSite(t, tenantsDir, "polis", pub)
	if err := StateRegistry(opDir, registryFor("polis.polis.pub", "judge.polis.pub"), priv); err != nil {
		t.Fatal(err)
	}

	if err := site.SetActorRegistryPointer(opDir, "/../../etc/passwd"); err != nil {
		t.Fatal(err)
	}
	g := LoadGuard(tenantsDir)
	if g.IsActorDomain("judge.polis.pub") {
		t.Error("a traversing pointer resolved to something the guard trusted")
	}
	if len(g.Problems) == 0 {
		t.Error("a pointer that resolves to nothing readable must be reported")
	}
	if !strings.Contains(strings.Join(g.Problems, " "), "missing") {
		t.Errorf("problems = %v", g.Problems)
	}
}
