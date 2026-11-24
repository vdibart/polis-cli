// Package vercelentry is a polis instance served as a Vercel Go function.
//
// ⚠️ EXPERIMENTAL. Remote instances on Vercel are exploratory and not under
// active development. This package may change or be removed without notice.
//
// ⭐ It exists because of how Vercel builds a Go function: the file under api/
// is compiled as a package outside this module's tree, and Go's internal rule
// then refuses its import of webapp/internal/server. A public package inside
// the module may import internal/, so the function imports this one.
//
// ⛔ It holds nothing of its own: the instance is internal/server's network
// handler, built once per process from the environment the platform provides.
package vercelentry

import (
	"io/fs"
	"log"
	"net/http"
	"os"
	"sync"

	"github.com/vdibart/polis-cli/webapp/internal/server"
	"github.com/vdibart/polis-cli/webapp/internal/webui"
)

// DataDir names the site in logs and owner records. ⛔ It is a label, not a
// place: the site lives in the store POLIS_STORAGE names, and nothing on this
// path writes the local disk. It is not writable on Vercel either, so a write
// that slipped through would fail rather than be lost silently.
const DataDir = "/polis-site"

// Version is the release this build is, stamped at build time with
// -ldflags "-X github.com/vdibart/polis-cli/webapp/vercelentry.Version=X.Y.Z".
// ⭐ The instance fetches its own release's function manifest by it; an
// unstamped build ("dev") deploys only with POLIS_FUNCTION_MANIFEST set.
var Version = "dev"

var (
	once    sync.Once
	handler http.Handler
	failed  error
)

// Handler serves one request, building the instance on the first.
//
// ⛔ A failed build answers 500 on every request of this process and logs the
// cause once; the cause names a variable or a step, never a value, because the
// environment holds secrets.
func Handler(w http.ResponseWriter, r *http.Request) {
	once.Do(func() {
		server.OneLinePerLogEntry() // one log entry, one line: a value cannot forge a second
		webFS, err := fs.Sub(webui.Assets, "www")
		if err == nil {
			handler, err = server.NewNetworkHandler(webFS, DataDir, Version, os.Getenv)
		}
		if err != nil {
			failed = err
			log.Printf("[error] polis instance did not start: %v", err)
		}
	})
	if failed != nil {
		http.Error(w, "this instance could not start; see its logs", http.StatusInternalServerError)
		return
	}
	handler.ServeHTTP(w, r)
}
