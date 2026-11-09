package provision

import (
	"fmt"
	"regexp"
	"sort"

	"github.com/vdibart/polis-cli/cli-go/pkg/publicfile"
	"github.com/vdibart/polis-cli/cli-go/pkg/storage"
)

// ⭐⭐ THE SITE'S PUBLIC TREE IS THE DEPLOYMENT'S STATIC FILES. Every key in
// the private store that is not private becomes .vercel/output/static/<key>,
// and Vercel serves it before anything reaches the function. The deployment is
// the PROJECTION the public Blob store used to be: rebuilt from the private
// store, never read back for truth.
//
// ⛔ A private name never becomes a static file. A static file is served to
// anyone at the site's own domain, so this check is the whole of the privacy
// property for the public surface: it is the same declared list the serve
// handler refuses (pkg/privatepath, in any path segment) plus .env files by
// any spelling.

// MaxDeploymentFiles is Vercel's cap on files in one deployment
// (vercel.com/docs/limits, read 2026-10-13), the function's included.
const MaxDeploymentFiles = 15000

// StaticFile is one public file of the site, named by its digest.
type StaticFile struct {
	Key  string // site-relative, as the store names it
	SHA  string // hex SHA-1 of its bytes
	Size int64
}

// DeployFile is one entry of a deployment's file list.
type DeployFile struct {
	File string `json:"file"`
	SHA  string `json:"sha"`
	Size int64  `json:"size"`
	Mode int    `json:"mode"`
}

// IsPublicKey reports whether a site key may be deployed: a clean key that
// names nothing private, and that the self-hosted serve handler would answer.
// ⛔ publicfile.Servable is the same rule serve uses: a deploy publishes, so it
// is never wider than serving.
func IsPublicKey(key string) bool {
	clean, err := storage.CleanKey(key)
	if err != nil || clean != key || key == "" {
		return false
	}
	return !isPrivateBuildName(key) && publicfile.Servable(key)
}

var hexSHA1 = regexp.MustCompile(`^[0-9a-f]{40}$`)

// staticDeploymentFiles returns the deployment entries for a site's public
// tree, sorted by name.
//
// ⛔ It REFUSES rather than skips: a private name here means the caller
// selected wrongly, and dropping it quietly would hide that. ⛔ An error names
// the key, never the file's contents.
func staticDeploymentFiles(files []StaticFile) ([]DeployFile, error) {
	out := make([]DeployFile, 0, len(files))
	seen := map[string]bool{}
	for _, f := range files {
		if !IsPublicKey(f.Key) {
			return nil, fmt.Errorf("provision: %s is not a public key; refusing to deploy any of the site", f.Key)
		}
		if seen[f.Key] {
			return nil, fmt.Errorf("provision: %s is listed twice", f.Key)
		}
		seen[f.Key] = true
		if !hexSHA1.MatchString(f.SHA) || f.Size < 0 {
			return nil, fmt.Errorf("provision: %s has no valid digest", f.Key)
		}
		out = append(out, DeployFile{File: ".vercel/output/static/" + f.Key, SHA: f.SHA, Size: f.Size, Mode: modeFile})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].File < out[j].File })
	return out, nil
}
