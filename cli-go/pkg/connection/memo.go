package connection

import (
	"context"
	"sync"

	"github.com/vdibart/polis-cli/cli-go/pkg/attestation"
	"github.com/vdibart/polis-cli/cli-go/pkg/storage"
)

// A per-request memo of grant resolutions. One request to a hosted site's
// /mcp checks the same grant several times (the token, the description, the
// call's own /v1 request, the marker); with a memo in its context each check
// after the first reads the first one's answer.
//
// ⚠️ Scope it to ONE request. A memo that outlived its request would keep a
// withdrawn grant live, which is exactly what the live check is for.

type memoKey struct{}

type resolveMemo struct {
	mu   sync.Mutex
	seen map[string]resolved
}

type resolved struct {
	live *attestation.LiveGrant
	err  error
}

// WithResolveMemo returns ctx carrying a fresh memo, for one request.
func WithResolveMemo(ctx context.Context) context.Context {
	if _, ok := ctx.Value(memoKey{}).(*resolveMemo); ok {
		return ctx
	}
	return context.WithValue(ctx, memoKey{}, &resolveMemo{seen: map[string]resolved{}})
}

// ResolveIn is Resolve, answered from ctx's memo when it has one.
func ResolveIn(ctx context.Context, site storage.Site, grantURL string) (*attestation.LiveGrant, error) {
	m, ok := ctx.Value(memoKey{}).(*resolveMemo)
	if !ok {
		return Resolve(site, grantURL)
	}
	key := site.ID() + "\x00" + grantURL
	m.mu.Lock()
	defer m.mu.Unlock()
	if r, ok := m.seen[key]; ok {
		return r.live, r.err
	}
	live, err := Resolve(site, grantURL)
	m.seen[key] = resolved{live, err}
	return live, err
}
