package server

import (
	"net/http"
	"path/filepath"
	"strings"

	"github.com/vdibart/polis-cli/cli-go/pkg/site"
	"github.com/vdibart/polis-cli/webapp/internal/serve"
)

// isSiteFilePath reports whether a request path names one of the site's own
// published files that the editor surface must serve from the data directory
// rather than answer with the SPA shell: the discovery documents under
// .well-known/, the root robots.txt and rsl.xml, and the licence pages under
// the licence type's mount.
//
// The mount is read per request, never hardcoded: dir/mount are
// user-configurable, and a changed bundle.json must not leave the old path
// served and the new one falling through to the shell.
func isSiteFilePath(dataDir, urlPath string) bool {
	clean := filepath.ToSlash(filepath.Clean(strings.TrimPrefix(urlPath, "/")))
	switch {
	case clean == "robots.txt", clean == "rsl.xml":
		return true
	case clean == ".well-known" || strings.HasPrefix(clean, ".well-known/"):
		return true
	}
	mount := strings.Trim(site.LicenseMountDir(dataDir), "/")
	return mount != "" && (clean == mount || strings.HasPrefix(clean, mount+"/"))
}

// serveSiteFile serves a path isSiteFilePath accepted through the shared public
// handler, so the editor surface gets the same content types, CORS, licence
// headers and dot-path rules as --reader and hosted.
func serveSiteFile(w http.ResponseWriter, r *http.Request, dataDir string) {
	serve.ServeTenantPublic(w, r, NewDataDirStorage(dataDir), "", nil)
}
