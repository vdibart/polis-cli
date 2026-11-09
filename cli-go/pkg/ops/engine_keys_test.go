package ops

import (
	"context"
	"strings"
	"testing"

	"github.com/vdibart/polis-cli/cli-go/pkg/bundle"
	"github.com/vdibart/polis-cli/cli-go/pkg/resolve"
	"github.com/vdibart/polis-cli/cli-go/pkg/storage"
)

// ⭐ The engine reads its key at CALL time when given a source: a host that
// loads its key after building the engine (a deferred start), or reloads it
// after a rotation, must not leave the engine refusing or signing with the
// retired key.
func TestTheEngineReadsItsKeyAtCallTime(t *testing.T) {
	var key []byte
	bundles := map[string]*bundle.Bundle{"pub.polis.core": bundle.DefaultCoreBundle()}
	e, err := NewEngine(EngineConfig{
		Resolver: resolve.New(storage.At(t.TempDir()), bundles),
		Bundles:  bundles,
		Keys:     func() ([]byte, []byte) { return key, nil },
	})
	if err != nil {
		t.Fatal(err)
	}
	req := ActionRequest{ContentType: "post", Action: "create"}
	_, err = e.Dispatch(context.Background(), req)
	if err == nil || !strings.Contains(err.Error(), "no private key") {
		t.Fatalf("with no key loaded yet: %v; want the no-private-key refusal", err)
	}
	key = []byte("loaded later")
	_, err = e.Dispatch(context.Background(), req)
	if err != nil && strings.Contains(err.Error(), "no private key") {
		t.Error("a key loaded after the engine was built was not seen")
	}
}
