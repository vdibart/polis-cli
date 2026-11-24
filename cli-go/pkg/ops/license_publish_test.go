package ops

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vdibart/polis-cli/cli-go/pkg/bundle"
	"github.com/vdibart/polis-cli/cli-go/pkg/license"
	"github.com/vdibart/polis-cli/cli-go/pkg/publish"
	"github.com/vdibart/polis-cli/cli-go/pkg/resolve"
	"github.com/vdibart/polis-cli/cli-go/pkg/signing"
	"github.com/vdibart/polis-cli/cli-go/pkg/site"
	"github.com/vdibart/polis-cli/cli-go/pkg/storage"
)

// ⛔ A work's own licence is a signed, outbound statement, and terms are not
// retroactive: a version published under terms the author did not write
// cannot be taken back. The engine once stripped the frontmatter before
// reading the author's `license:` line, so every post published through /v1
// carried the site default instead. These tests hold create and update to the
// licence the author wrote, inside the signature.

const licensedBase = "https://maya.example"

// licensedEngine is an engine over a real initialised site whose stated terms
// are `choice`.
func licensedEngine(t *testing.T, choice string) (*Engine, string, []byte, []byte) {
	t.Helper()
	dir := t.TempDir()
	if _, err := site.Init(storage.At(dir), site.InitOptions{License: choice, BaseURL: licensedBase}); err != nil {
		t.Fatalf("init: %v", err)
	}
	priv, err := os.ReadFile(filepath.Join(dir, ".polis", "keys", "id_ed25519"))
	if err != nil {
		t.Fatal(err)
	}
	pub, err := os.ReadFile(filepath.Join(dir, ".polis", "keys", "id_ed25519.pub"))
	if err != nil {
		t.Fatal(err)
	}
	bundles := map[string]*bundle.Bundle{"pub.polis.core": bundle.DefaultCoreBundle()}
	e, err := NewEngine(EngineConfig{
		Resolver:   resolve.New(storage.At(dir), bundles),
		Bundles:    bundles,
		PrivateKey: priv,
		PublicKey:  pub,
		BaseURL:    licensedBase,
	})
	if err != nil {
		t.Fatal(err)
	}
	return e, dir, priv, pub
}

// signedTerms reads a published post's licence block and checks the post
// verifies over the bytes every verifier reconstructs.
func signedTerms(t *testing.T, path string, pub []byte) *license.Terms {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	sig := publish.ParseFrontmatter(string(raw))["signature"]
	if sig == "" {
		t.Fatal("post carries no signature")
	}
	var lines []string
	for len(sig) > 76 {
		lines, sig = append(lines, sig[:76]), sig[76:]
	}
	armour := "-----BEGIN SSH SIGNATURE-----\n" + strings.Join(append(lines, sig), "\n") + "\n-----END SSH SIGNATURE-----"
	base := signing.MarkdownSigningBase(string(raw), signing.TypePost)
	if ok, err := signing.VerifySignature([]byte(base), pub, armour); err != nil || !ok {
		t.Fatalf("the post does not verify (ok=%v, err=%v)", ok, err)
	}
	// ⛔ The block must be inside the signed bytes, not beside them.
	if !strings.Contains(base, "\nlicense:\n") {
		t.Error("the licence block is not in the signing base")
	}
	terms := license.ParseBlock(string(raw))
	if terms == nil {
		t.Fatal("no licence block")
	}
	return terms
}

func dispatchPost(t *testing.T, e *Engine, action string, payload map[string]any) map[string]any {
	t.Helper()
	res, err := e.Dispatch(context.Background(), ActionRequest{Action: action, ContentType: "pub.polis.post", Payload: payload})
	if err != nil {
		t.Fatalf("%s: %v", action, err)
	}
	return res.Data
}

func TestAPostPublishedThroughTheEngineKeepsTheLicenceItsAuthorWrote(t *testing.T) {
	e, dir, priv, pub := licensedEngine(t, "reserved")

	data := dispatchPost(t, e, "create", map[string]any{
		"markdown": "---\nlicense: open\n---\n# Take it\n\nUse this freely.\n",
	})
	got := signedTerms(t, filepath.Join(dir, data["path"].(string)), pub)
	if got.Profile != license.ProfileOpen {
		t.Fatalf("profile = %q, want the work's own %q (the site default is reserved)", got.Profile, license.ProfileOpen)
	}

	// ⭐ The same input through the publish path the CLI and the old browser
	// app use yields the same terms (only the timestamp differs).
	want, err := publish.PublishPost(storage.At(dir), "# Take it too\n\nUse this freely.\n", "", priv,
		&publish.DiscoveryConfig{BaseURL: licensedBase, LicenseProfile: "open"})
	if err != nil {
		t.Fatal(err)
	}
	ref := signedTerms(t, filepath.Join(dir, want.Path), pub)
	got.Asserted, ref.Asserted = "", ""
	if *got != *ref {
		t.Errorf("engine terms %+v differ from the publish path's %+v", *got, *ref)
	}
}

func TestAPostRepublishedThroughTheEngineCarriesTheLicenceWrittenOnTheNewVersion(t *testing.T) {
	e, dir, _, pub := licensedEngine(t, "reserved")

	data := dispatchPost(t, e, "create", map[string]any{"markdown": "# Notes\n\nFirst take.\n"})
	path := data["path"].(string)
	if got := signedTerms(t, filepath.Join(dir, path), pub); got.Profile != license.ProfileReserved {
		t.Fatalf("first version profile = %q, want the site default %q", got.Profile, license.ProfileReserved)
	}

	dispatchPost(t, e, "update", map[string]any{
		"path":     path,
		"markdown": "---\nlicense: open\n---\n# Notes\n\nSecond take, now open.\n",
	})
	if got := signedTerms(t, filepath.Join(dir, path), pub); got.Profile != license.ProfileOpen {
		t.Errorf("new version profile = %q, want the %q written on it", got.Profile, license.ProfileOpen)
	}
}

func TestAPostWithNoLicenceLineStillTakesTheSiteDefault(t *testing.T) {
	e, dir, _, pub := licensedEngine(t, "reserved")

	data := dispatchPost(t, e, "create", map[string]any{"markdown": "# Plain\n\nNothing said about terms.\n"})
	if got := signedTerms(t, filepath.Join(dir, data["path"].(string)), pub); got.Profile != license.ProfileReserved {
		t.Errorf("profile = %q, want the site default %q", got.Profile, license.ProfileReserved)
	}

	// ⚠️ A materialised block from an earlier version is what publish stamped,
	// not what the author wrote: it is not read as an override.
	stamped := "---\nlicense:\n  v: pub.polis.license.v1\n  profile: pub.polis.license.open/1\n---\n# Echo\n\nPasted back.\n"
	data = dispatchPost(t, e, "create", map[string]any{"markdown": stamped})
	if got := signedTerms(t, filepath.Join(dir, data["path"].(string)), pub); got.Profile != license.ProfileReserved {
		t.Errorf("profile = %q, want the site default %q (a stamped block is not an override)", got.Profile, license.ProfileReserved)
	}
}

func TestTheLicenceSurvivesAnEngineWithNoDiscoveryService(t *testing.T) {
	if cfg := postDiscoveryConfig(HandlerEnv{}, ""); cfg != nil {
		t.Errorf("no licence and no discovery service: cfg = %+v, want nil (publish's defaults)", cfg)
	}
	cfg := postDiscoveryConfig(HandlerEnv{BaseURL: licensedBase}, "open")
	if cfg == nil || cfg.LicenseProfile != "open" || cfg.BaseURL != licensedBase {
		t.Errorf("cfg = %+v, want the licence carried with the site's address", cfg)
	}
	cfg = postDiscoveryConfig(HandlerEnv{DiscoveryURL: "https://ds.example", BaseURL: licensedBase}, "open")
	if cfg == nil || cfg.DiscoveryURL != "https://ds.example" || cfg.LicenseProfile != "open" {
		t.Errorf("cfg = %+v, want the discovery service and the licence", cfg)
	}
}
