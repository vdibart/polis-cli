package server

import (
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/vdibart/polis-cli/cli-go/pkg/branch"
	"github.com/vdibart/polis-cli/cli-go/pkg/mode"
	"github.com/vdibart/polis-cli/cli-go/pkg/owner"
	"github.com/vdibart/polis-cli/cli-go/pkg/provision"
	"github.com/vdibart/polis-cli/cli-go/pkg/site"
	"github.com/vdibart/polis-cli/cli-go/pkg/storage"
)

// branchTo branches origin to the address, writing the owner's recovery key at
// keyPath, and returns the branched site.
func branchTo(t *testing.T, origin, to, keyPath string) storage.Site {
	t.Helper()
	out := filepath.Join(t.TempDir(), "branched")
	if _, err := branch.Run(branch.Options{
		Source: origin, To: to, Out: out, FromDomain: "alice.example.test",
		WithoutPrivate: true, RecoveryKey: keyPath,
	}); err != nil {
		t.Fatalf("branch to %s: %v", to, err)
	}
	return storage.At(out)
}

// serverFor is a server-signed instance configured the way the deploy step
// configures one: POLIS_RECOVERY_KEY from the site's provision.OwnerConfig.
func serverFor(t *testing.T, s storage.Site, baseURL string) *Server {
	t.Helper()
	cfg, err := provision.OwnerConfig(s)
	if err != nil {
		t.Fatalf("owner config: %v", err)
	}
	rec, err := owner.NewRecovery(cfg.Values[owner.RecoveryKeyEnv], baseURL)
	if err != nil {
		t.Fatalf("the instance refuses the configured key: %v", err)
	}
	dir := t.TempDir()
	srv := &Server{Mode: mode.RemoteServerSigned, Recovery: rec, DataDir: dir, BaseURL: baseURL}
	srv.ownerStoreOnce.Do(func() {})
	srv.ownerStoreV = storage.NewSite(dir)
	return srv
}

// ⭐⭐ The whole first sign-in to a branched site, with real crypto: the branch
// makes the owner's key, the deploy step's config sets its public half, the
// instance issues a challenge, the owner signs it exactly as `polis recover
// sign` does, and /api/recover/verify accepts it. A key from a different branch
// is refused.
func TestABranchedSitesRecoveryKeySignsItsFirstSignIn(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	origin := filepath.Join(t.TempDir(), "origin")
	if _, err := site.Init(storage.At(origin), site.InitOptions{BaseURL: "https://alice.example.test", SiteTitle: "alice"}); err != nil {
		t.Fatal(err)
	}
	// A key's window starts AT its handover's valid_from, so the rotation must
	// land in a later second than anything the origin signed.
	time.Sleep(1100 * time.Millisecond)

	const target = "https://alice-two.example.test"
	keyPath := filepath.Join(t.TempDir(), "recovery")
	srv := serverFor(t, branchTo(t, origin, target, keyPath), target)

	sign := func(path, message string) string {
		t.Helper()
		priv, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		sig, err := owner.SignChallenge([]byte(message), priv)
		if err != nil {
			t.Fatalf("sign: %v", err)
		}
		return sig
	}

	ch := issue(t, srv)
	if w := postProof(t, srv, ch.Nonce, sign(keyPath, ch.Message)); w.Code != http.StatusOK {
		t.Fatalf("⛔ the instance refused the owner's recovery proof: %d %s", w.Code, w.Body.String())
	}

	// Another branch's key, for a different site, proves nothing here.
	otherKey := filepath.Join(t.TempDir(), "other")
	branchTo(t, origin, "https://alice-three.example.test", otherKey)
	ch = issue(t, srv)
	if w := postProof(t, srv, ch.Nonce, sign(otherKey, ch.Message)); w.Code != http.StatusUnauthorized {
		t.Errorf("⛔ a different branch's key was not refused: %d %s", w.Code, w.Body.String())
	}
}
