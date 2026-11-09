package server

import (
	"context"
	"testing"

	"github.com/vdibart/polis-cli/cli-go/pkg/provision"
	"github.com/vdibart/polis-cli/cli-go/pkg/storage"
)

// ⭐ A released function build is stamped with its version, and the network
// handler carries it to the server, which fetches its OWN release's function
// manifest by it when no manifest is set inline. Without the stamp a released
// instance could not redeploy its function from its release.
func TestAStampedNetworkHandlerFetchesItsOwnReleasesManifest(t *testing.T) {
	b := requestStartedBackend(storage.NewObjectStore(""))
	s, _, err := newNetworkHandler(testWebFS, instanceOptions{DataDir: t.TempDir(), CLIVersion: "1.2.3", Getenv: networkEnv(nil), Backend: &b})
	if err != nil {
		t.Fatalf("network handler: %v", err)
	}
	t.Cleanup(s.Close)
	if s.CLIVersion != "1.2.3" {
		t.Fatalf("CLIVersion = %q; want the stamped version", s.CLIVersion)
	}
	var asked []string
	s.releaseFunctionFor = func(_ context.Context, v string) (provision.FunctionManifest, func(context.Context, provision.DeployFile) ([]byte, error), error) {
		asked = append(asked, v)
		return provision.FunctionManifest{}, nil, nil
	}
	if _, _, err := s.functionManifest(context.Background(), storage.NewObjectStore(""), func(string) string { return "" }); err != nil {
		t.Fatal(err)
	}
	if len(asked) != 1 || asked[0] != "1.2.3" {
		t.Fatalf("fetched the manifest for %v; want [1.2.3]", asked)
	}
}
