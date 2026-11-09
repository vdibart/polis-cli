package cmd

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"strings"
	"testing"

	"github.com/vdibart/polis-cli/cli-go/pkg/signing"
	"github.com/vdibart/polis-cli/cli-go/pkg/site"
	"github.com/vdibart/polis-cli/cli-go/pkg/storage"
)

func initRotationSite(t *testing.T) (string, storage.Site) {
	t.Helper()
	dir := t.TempDir()
	if _, err := site.Init(storage.At(dir), site.InitOptions{BaseURL: "https://alice.polis.pub", SiteTitle: "alice"}); err != nil {
		t.Fatal(err)
	}
	return dir, storage.At(dir)
}

func silentDS(t *testing.T) string {
	t.Helper()
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Write([]byte(`{}`)) }))
	t.Cleanup(ts.Close)
	return ts.URL
}

// ⛔⛔ A crash after the new key is installed and before the key history is
// written: the next `polis rotate-key` finishes that rotation, signing the
// handover with the retired key kept on disk, and the chain verifies.
func TestRotateKeyFinishesARotationCrashedBeforeTheHistoryWrite(t *testing.T) {
	dir, s := initRotationSite(t)
	ctx := context.Background()
	newPriv, newPubSSH, err := signing.GenerateKeypair()
	if err != nil {
		t.Fatal(err)
	}
	if err := site.StageRotationKey(ctx, s, newPriv); err != nil {
		t.Fatal(err)
	}
	if err := site.InstallRotationKey(ctx, s, newPriv, newPubSSH); err != nil {
		t.Fatal(err)
	}
	// ✂ crash: RecordKeyRotation never ran.

	runRotateKeyJSON(t, dir, silentDS(t))

	block, err := site.LoadKeyHistory(s)
	if err != nil || block == nil {
		t.Fatalf("load key history: %v", err)
	}
	if err := site.VerifyChain(block, "alice.polis.pub"); err != nil {
		t.Fatalf("⛔ the published key history does not verify: %v", err)
	}
	if !site.SameSSHKey(block.Current.Key, string(newPubSSH)) {
		t.Errorf("the chain's head is %s; want the installed key", block.Current.Key)
	}
	for _, k := range []string{site.StagedKeyKey, site.PreviousKeyKey} {
		if ok, _ := s.Store().Exists(ctx, k); ok {
			t.Errorf("%s was left after the rotation finished", k)
		}
	}
}

// ⛔ The state an older build left — installed, staged copy gone, nothing
// recorded — is refused, naming it, and the chain is not touched.
func TestRotateKeyRefusesWhenTheKeyIsNotThePublishedOne(t *testing.T) {
	if os.Getenv("POLIS_ROTATE_REFUSAL_CHILD") != "" {
		dataDir, jsonOutput = os.Getenv("POLIS_ROTATE_REFUSAL_CHILD"), false
		handleRotateKey(nil)
		return
	}
	dir, s := initRotationSite(t)
	ctx := context.Background()
	newPriv, newPubSSH, err := signing.GenerateKeypair()
	if err != nil {
		t.Fatal(err)
	}
	_ = s.Store().Put(ctx, site.PrivateKeyKey, newPriv, storage.WithMode(0o600))
	_ = s.Store().Put(ctx, site.PublicKeyKey, newPubSSH, storage.WithMode(0o644))
	before, _ := s.Store().Get(ctx, site.WellKnownKey)

	cmd := exec.Command(os.Args[0], "-test.run=^TestRotateKeyRefusesWhenTheKeyIsNotThePublishedOne$")
	cmd.Env = append(os.Environ(), "POLIS_ROTATE_REFUSAL_CHILD="+dir,
		"POLIS_BASE_URL=https://alice.polis.pub", "DISCOVERY_SERVICE_URL="+silentDS(t))
	out, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatalf("rotate-key succeeded from a key that is not the published one:\n%s", out)
	}
	if !strings.Contains(string(out), "does not match the public_key .well-known/polis publishes") {
		t.Errorf("the refusal should name the state:\n%s", out)
	}
	if after, _ := s.Store().Get(ctx, site.WellKnownKey); string(after) != string(before) {
		t.Error("⛔ a refused rotation changed .well-known/polis")
	}
	if ok, _ := s.Store().Exists(ctx, site.StagedKeyKey); ok {
		t.Error("a refused rotation staged a key")
	}
}
