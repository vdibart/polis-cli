package server

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/vdibart/polis-cli/cli-go/pkg/signing"
)

// A rotation the CLI runs against the same data directory changes the key
// files but not the running server's in-memory copy. The server now notices
// before the next request (or background cycle) and reloads, instead of
// signing with the retired key until it restarts.
func TestServerReloadsKeysWhenTheKeyFileChanges(t *testing.T) {
	s := newConfiguredServer(t)
	s.LoadKeys()
	old := append([]byte(nil), s.PrivateKey...)

	// Nothing changed: no reload.
	if s.ReloadKeysIfChanged() {
		t.Fatal("reloaded with the key file unchanged")
	}

	// The CLI rotates underneath.
	newPriv, newPub, err := signing.GenerateKeypair()
	if err != nil {
		t.Fatal(err)
	}
	keys := filepath.Join(s.DataDir, ".polis", "keys")
	if err := os.WriteFile(filepath.Join(keys, "id_ed25519"), newPriv, 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(keys, "id_ed25519.pub"), newPub, 0644); err != nil {
		t.Fatal(err)
	}
	// Same size is possible for two keys, so make the change visible by mtime.
	later := time.Now().Add(2 * time.Second)
	os.Chtimes(filepath.Join(keys, "id_ed25519"), later, later)

	// The next request, through the real handler, picks it up.
	s.Handler().ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/api/capabilities", nil))
	if bytes.Equal(s.PrivateKey, old) || !bytes.Equal(s.PrivateKey, newPriv) {
		t.Fatal("the server still holds the retired key after a request")
	}
	if !bytes.Equal(s.PublicKey, newPub) {
		t.Error("the public key was not reloaded with the private key")
	}
	if s.ReloadKeysIfChanged() {
		t.Error("reloaded again with nothing new on disk")
	}
}
