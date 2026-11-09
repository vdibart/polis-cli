package server

import (
	"context"
	"log"
	"net/http"
	"sync"
	"time"

	"github.com/vdibart/polis-cli/cli-go/pkg/site"
)

// Key reload when the key file changes on disk.
//
// The server reads .polis/keys/ once, at startup (LoadKeys), and signs with
// that in-memory copy. A rotation run by the CLI against the same data
// directory — on hosted, `polis rotate-key` over `fly ssh` — changes the files
// but not the copy, so the server kept signing with the RETIRED key until it
// was restarted or evicted, publishing signatures the site's own key history
// says are out of date.
//
// Every signing act happens inside an HTTP request or a background cycle
// (sync, Rosie's catch-up), so the key file is checked at the start of each:
// one stat, and a reload only when its size or modification time changed.
// (The webapp's own rotation handler already reloads; this is for everyone
// else who writes the key.)

// keyStamp identifies the key file's content as last loaded.
type keyStamp struct {
	size    int64
	modTime time.Time
}

type keyReloadState struct {
	mu     sync.Mutex
	loaded keyStamp

	// lastCheck and warned apply to a site on a remote store only (see
	// keyCheckDue).
	lastCheck time.Time
	warned    bool
}

// remoteKeyCheckInterval is how often a site on a remote store looks for a
// changed key. ⭐ On disk a stat is a system call, so every request checks; on
// an object store it is a network round trip, and one per request would add
// that latency to every page. Such a site is rotated through its own server,
// whose rotation handler reloads at once, so the check only catches a write
// made from elsewhere.
const remoteKeyCheckInterval = 30 * time.Second

// stampKeyFile records the key file's current size and mtime as loaded.
func (s *Server) stampKeyFile() {
	st := s.keyStamp()
	s.keyReload.mu.Lock()
	s.keyReload.loaded = st
	s.keyReload.mu.Unlock()
}

// keyStamp reads the size and modification time of the private key, through
// the site's own store.
//
// ⛔ THE SITE'S STORE, NEVER A LOCAL PATH. It used to stat the file under
// DataDir directly, which on a remote backend is a directory the site does not
// live in: the stamp was always zero and a changed key was never seen.
//
// ⚠️ This is one of the few callers that needs Stat METADATA rather than mere
// existence, and it depends on ModTimes: a backend that cannot report one
// makes "has the key changed?" unanswerable, and this would silently decide
// the key never changes. Such a backend gets no reload, and says so once
// (keyCheckDue); a different change signal would be needed, not a zero
// timestamp.
func (s *Server) keyStamp() keyStamp {
	info, err := s.Site().Store().Stat(context.Background(), site.PrivateKeyKey)
	if err != nil {
		return keyStamp{}
	}
	return keyStamp{size: info.Size, modTime: info.ModTime}
}

// keyCheckDue reports whether this request should look for a changed key.
func (s *Server) keyCheckDue() bool {
	caps := s.Site().Store().Capabilities()
	if caps.RealPath {
		return true // on disk: every request, as always
	}
	s.keyReload.mu.Lock()
	defer s.keyReload.mu.Unlock()
	if !caps.ModTimes {
		if !s.keyReload.warned {
			s.keyReload.warned = true
			log.Printf("[warning] key reload is off: this site's store reports no modification times, so a key changed from outside this server is not picked up until restart")
		}
		return false
	}
	now := time.Now()
	if !s.keyReload.lastCheck.IsZero() && now.Sub(s.keyReload.lastCheck) < remoteKeyCheckInterval {
		return false
	}
	s.keyReload.lastCheck = now
	return true
}

// ReloadKeysIfChanged reloads the key pair when the private key file differs
// from what was last loaded, and reports whether it did. Cheap when nothing
// changed: one stat (on a remote store, at most one per
// remoteKeyCheckInterval).
func (s *Server) ReloadKeysIfChanged() bool {
	if !s.keyCheckDue() {
		return false
	}
	now := s.keyStamp()
	if now == (keyStamp{}) {
		return false // no key file: nothing to sign with, nothing to reload
	}
	s.keyReload.mu.Lock()
	changed := now != s.keyReload.loaded
	s.keyReload.mu.Unlock()
	if !changed {
		return false
	}
	s.LoadKeys()
	s.LogEvent("pub.polis.site.keys_reloaded", map[string]interface{}{
		"reason": "key file changed on disk",
	})
	return true
}

// keyReloadMiddleware checks the key file before every request, so a handler
// never signs with a key the site no longer holds.
func (s *Server) keyReloadMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.ReloadKeysIfChanged()
		next.ServeHTTP(w, r)
	})
}
