package ops

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vdibart/polis-cli/cli-go/pkg/bundle"
	"github.com/vdibart/polis-cli/cli-go/pkg/dm"
	"github.com/vdibart/polis-cli/cli-go/pkg/resolve"
)

// engineAt builds a second engine over an existing site dir, the way a hosted
// tenant handler is rebuilt after cache eviction.
func engineAt(t *testing.T, siteDir string) *Engine {
	t.Helper()
	bundles := map[string]*bundle.Bundle{"pub.polis.core": bundle.DefaultCoreBundle()}
	priv, _ := os.ReadFile(filepath.Join(siteDir, ".polis/keys/id_ed25519"))
	pub, _ := os.ReadFile(filepath.Join(siteDir, ".polis/keys/id_ed25519.pub"))
	engine, err := NewEngine(EngineConfig{
		Resolver:   resolve.New(siteDir, bundles),
		Bundles:    bundles,
		PrivateKey: priv,
		PublicKey:  pub,
		BaseURL:    "https://test.example.com",
	})
	if err != nil {
		t.Fatal(err)
	}
	return engine
}

// The self-hosted DM rate limit did not exist: deliverDM built a fresh
// dm.NewRateLimiter on every call, so both counters were reset before each
// check and never reached their limit — while the security model publishes
// 10/sender/hr and 100/hr global for every deployment.
//
// The limiter must outlive the request. Deliveries fail here for other reasons
// (an unparseable envelope), which is after both rate checks, so the counters
// still advance exactly as a real delivery's would.
func TestDMRateLimitCountsAcrossRequests(t *testing.T) {
	engine, siteDir := newTestEngine(t)
	t.Cleanup(func() { forgetDMRateLimiter(siteDir) })
	forgetDMRateLimiter(siteDir)

	deliver := func() error {
		_, err := engine.Dispatch(context.Background(), ActionRequest{
			Action:      "deliver",
			ContentType: "pub.polis.dm",
			Payload: map[string]any{
				"sender_domain": "sender.example",
				"envelope":      `{"not":"a valid envelope"}`,
			},
		})
		return err
	}

	for i := 1; i <= dm.DefaultPerSenderRate; i++ {
		err := deliver()
		if err != nil && strings.Contains(err.Error(), "rate limit") {
			t.Fatalf("delivery %d of %d was rate limited: %v", i, dm.DefaultPerSenderRate, err)
		}
	}
	err := deliver()
	if err == nil || !strings.Contains(err.Error(), "per-sender rate limit exceeded") {
		t.Fatalf("delivery %d = %v, want a per-sender rate limit refusal", dm.DefaultPerSenderRate+1, err)
	}

	// A second engine over the same site dir is the same recipient: rebuilding
	// the tenant handler (hosted evicts and reloads them) must not hand a
	// flooding sender a fresh window.
	engine2 := engineAt(t, siteDir)
	_, err = engine2.Dispatch(context.Background(), ActionRequest{
		Action:      "deliver",
		ContentType: "pub.polis.dm",
		Payload:     map[string]any{"sender_domain": "sender.example", "envelope": `{}`},
	})
	if err == nil || !strings.Contains(err.Error(), "per-sender rate limit exceeded") {
		t.Errorf("after a handler rebuild: %v, want the limit still in force", err)
	}

	// A different sender is unaffected by the first one's flood.
	_, err = engine.Dispatch(context.Background(), ActionRequest{
		Action:      "deliver",
		ContentType: "pub.polis.dm",
		Payload:     map[string]any{"sender_domain": "other.example", "envelope": `{}`},
	})
	if err != nil && strings.Contains(err.Error(), "rate limit") {
		t.Errorf("a second sender was rate limited by the first: %v", err)
	}
}
