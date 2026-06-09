package render

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sync"
	"time"
)

// Author-avatar fetch + cache.
//
// This is the SINGLE SEAM for obtaining a remote author's avatar config. Today
// it reads the author's .well-known/polis "avatar" block; when avatars move
// into the content bundle, repoint fetchAuthorAvatar and nothing else changes.
//
// Cache is in-process with a positive TTL and a shorter negative TTL (so a
// dead/unreachable origin isn't re-hit on every render). Mirrors the intent of
// LoadOrFetchReplyContext (bounded by the caller's concurrency; see
// handlers_stream.go buildAuthorAvatars which fans out at most 5 at a time).

const (
	authorAvatarTTL          = 6 * time.Hour
	authorAvatarNegTTL       = 15 * time.Minute
	authorAvatarFetchTimeout = 3 * time.Second
	authorAvatarMaxBody      = 256 * 1024
)

type authorAvatarEntry struct {
	cfg       AvatarConfig
	ok        bool
	fetchedAt time.Time
}

var (
	authorAvatarMu     sync.Mutex
	authorAvatarCache  = map[string]authorAvatarEntry{}
	authorAvatarClient = &http.Client{Timeout: authorAvatarFetchTimeout}
)

// LoadOrFetchAuthorAvatar returns the avatar config for a remote author domain,
// fetching + caching on miss/expiry. ok=false means the fetch failed (the
// caller should fall back to FallbackAvatarConfig). A reachable site with no
// avatar block returns (zero AvatarConfig, true) — cached as a successful
// "no custom avatar" so we don't re-hit it.
func LoadOrFetchAuthorAvatar(domain string) (AvatarConfig, bool) {
	if domain == "" {
		return AvatarConfig{}, false
	}

	authorAvatarMu.Lock()
	e, present := authorAvatarCache[domain]
	authorAvatarMu.Unlock()
	if present {
		ttl := authorAvatarTTL
		if !e.ok {
			ttl = authorAvatarNegTTL
		}
		if time.Since(e.fetchedAt) < ttl {
			return e.cfg, e.ok
		}
	}

	cfg, ok := fetchAuthorAvatar(domain)

	authorAvatarMu.Lock()
	authorAvatarCache[domain] = authorAvatarEntry{cfg: cfg, ok: ok, fetchedAt: time.Now()}
	authorAvatarMu.Unlock()
	return cfg, ok
}

// fetchAuthorAvatar is a package var so tests can stub the network. Default
// implementation reads https://<domain>/.well-known/polis and parses the
// "avatar" block.
var fetchAuthorAvatar = func(domain string) (AvatarConfig, bool) {
	resp, err := authorAvatarClient.Get("https://" + domain + "/.well-known/polis")
	if err != nil {
		return AvatarConfig{}, false
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return AvatarConfig{}, false
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, authorAvatarMaxBody))
	if err != nil {
		return AvatarConfig{}, false
	}
	var wk struct {
		Avatar *AvatarConfig `json:"avatar"`
	}
	if err := json.Unmarshal(body, &wk); err != nil {
		return AvatarConfig{}, false
	}
	if wk.Avatar == nil {
		return AvatarConfig{}, true // reachable, no custom avatar
	}
	return *wk.Avatar, true
}

// FallbackAvatarConfig returns a deterministic hue-based config for an author
// with no custom avatar. The hue is SERVER-AUTHORITATIVE — it's sent to the
// client in author_avatar.bg, so the JS side renders it directly rather than
// recomputing (only the single initial letter is derived client-side). The
// hue formula matches stream.js avatarFallback / app.js domainToAvatar in
// spirit; exact byte-parity isn't required since the server is the source.
func FallbackAvatarConfig(domain string) AvatarConfig {
	return AvatarConfig{BG: fallbackAvatarHue(domain), FG: "#ffffff"}
}

func fallbackAvatarHue(domain string) string {
	var hash int32
	for _, r := range domain {
		hash = int32(r) + (hash << 5) - hash
	}
	hue := int(hash) % 360
	if hue < 0 {
		hue += 360
	}
	return fmt.Sprintf("hsl(%d, 35%%, 55%%)", hue)
}
