package cmd

import (
	"context"

	"github.com/vdibart/polis-cli/cli-go/pkg/bundle"
	"github.com/vdibart/polis-cli/cli-go/pkg/capability"
	"github.com/vdibart/polis-cli/cli-go/pkg/mode"
	"github.com/vdibart/polis-cli/cli-go/pkg/ops"
	"github.com/vdibart/polis-cli/cli-go/pkg/resolve"
	"github.com/vdibart/polis-cli/cli-go/pkg/storage"
)

// ⭐⭐ THE CLI RUNS THE SAME OPERATIONS THE API SERVES, IN-PROCESS.
//
// polis publish and polis republish dispatch the action layer's post.create
// and post.update against the local site, with no server running: the same
// code /v1 runs for the v5 app and for agents, so the CLI is not a third
// implementation that drifts (it once read the author's licence where the API
// did not). What a write causes runs through the shared after-write module,
// with the CLI's own parts only:
//   - hooks OFF: the CLI runs no publish hook, by decision, even when the
//     site has a hook script (docs/cli/implementation-parity.md);
//   - no render for a v3 site, as polis publish never rendered one;
//   - events to POLIS_LOG_FILE (source cli), with the command's request id.

// cliEngine is the action layer over the local site at dir.
func cliEngine(dir string, privKey, pubKey []byte) (*ops.Engine, error) {
	site := storage.At(dir)
	bundles := map[string]*bundle.Bundle{"pub.polis.core": loadOrDefaultBundle(dir)}
	base := baseURL
	if base == "" {
		base = getBaseURLFromSite(dir)
	}
	emit := func(action string, fields map[string]any) { logCLIAction(action, fields) }
	after := ops.NewAfterWrite(ops.AfterWriteConfig{
		Site: site,
		NewRenderer: func() (ops.SiteRenderer, error) {
			return newCascadeRenderer(dir)
		},
		HooksOn:          false,
		Emit:             emit,
		SkipRenderShapes: map[string]bool{"v3": true},
	})
	return ops.NewEngine(ops.EngineConfig{
		Resolver:                 resolve.New(site, bundles),
		Bundles:                  bundles,
		PrivateKey:               privKey,
		PublicKey:                pubKey,
		BaseURL:                  base,
		DiscoveryURL:             discoveryURL,
		CLIThemesDir:             findCLIThemesDir(),
		Capabilities:             capability.For(mode.Local),
		EmitEvent:                emit,
		AfterWrite:               after.Apply,
		AllowPrivateDMRecipients: true, // the owner's own machine
	})
}

// cliContext is the context a command's operation runs in: its request id
// and the cli surface, which every event the write causes carries.
func cliContext() context.Context {
	return ops.WithRequestMeta(context.Background(), &ops.RequestMeta{RequestID: requestID, Surface: ops.SurfaceCLI})
}
