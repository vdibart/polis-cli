// Package clone provides functionality to clone remote polis sites.
package clone

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/vdibart/polis-cli/cli-go/pkg/attestation"
	"github.com/vdibart/polis-cli/cli-go/pkg/bundle"
	"github.com/vdibart/polis-cli/cli-go/pkg/remote"
)

// Content type names this clone knows how to fetch. Names, not paths: WHERE
// each type lives is read from the site's own bundle declaration, because `dir`
// is user-configurable and a hardcoded path would silently clone the wrong
// place on any site that moved one.
const (
	typeComment = "pub.polis.comment"
	typeFollow  = "pub.polis.follow"
)

// CloneState tracks the state of a cloned site.
type CloneState struct {
	SourceURL    string `json:"source_url"`
	ClonedAt     string `json:"cloned_at"`
	LastUpdated  string `json:"last_updated"`
	PostsCount   int    `json:"posts_count"`
	LastManifest string `json:"last_manifest_hash,omitempty"`
}

// CloneResult contains the results of a clone operation.
type CloneResult struct {
	TargetDir             string `json:"target_dir"`
	PostsDownloaded       int    `json:"posts_downloaded"`
	CommentsDownloaded    int    `json:"comments_downloaded"`
	BlessedCommentsSynced int    `json:"blessed_comments_synced"`

	// TagsDownloaded and AttestationsDownloaded count the signed records this
	// clone collected because the source ENUMERATED them in index.jsonl.
	// Before Signet epic 25 no polis site published such an enumeration, and
	// both were permanently unreachable — see NotEnumerable.
	TagsDownloaded         int `json:"tags_downloaded"`
	AttestationsDownloaded int `json:"attestations_downloaded"`

	Errors int `json:"errors"`

	// LicenseCloned reports whether the site's signed licence document was
	// found and copied. Discovered through the `license` POINTER in
	// .well-known/polis, never by assuming a path.
	LicenseCloned bool `json:"license_cloned"`
	// DIDDocumentCloned reports whether .well-known/did.json was found and
	// copied. Absent is an ordinary state — most sites have never published one.
	DIDDocumentCloned bool `json:"did_document_cloned"`

	// NotEnumerable names content types this clone could NOT collect because
	// the source's index carries no line of that type.
	//
	// ⚠️ This is the honest half of the answer and it must never go quiet: a
	// clone that omitted a type in silence would make `polis validate ./clone`
	// report "no problems" about artifacts nobody looked for. Same discipline
	// as the validator's not-applicable reporting.
	//
	// ⚠️ An empty slice of a type is INDISTINGUISHABLE from a site that
	// publishes no enumeration for it — a site with no tags and a site running
	// a CLI older than Signet epic 25 look identical over HTTP. So the wording
	// stays "the site publishes no index entries for these" rather than
	// claiming the site has none.
	NotEnumerable []string `json:"not_enumerable,omitempty"`

	// RejectedPaths lists remote-supplied paths this clone refused to write
	// because they would land outside the clone folder.
	RejectedPaths []string `json:"rejected_paths,omitempty"`
}

// CloneOptions configures the clone operation.
type CloneOptions struct {
	FullClone bool // If true, re-download everything; if false, only new content
}

// Clone clones a remote polis site to a local directory.
//
// It fetches the CANONICAL content paths (content/…/post/…/slug.md), never the
// rendered mounts (/posts/…). That is deliberate: the canonical path holds the
// signed source, the mount serves a projection of it, and what you verify is
// the source. Whether a mount serves at all is a different question, asked by
// Judge.
func Clone(serverURL, targetDir string, opts CloneOptions) (*CloneResult, error) {
	client := remote.NewClient()
	// Both lists are emitted through a plain map in JSON mode, where `omitempty`
	// does not apply — a nil slice would publish `null` where the contract says
	// `[]` (docs/cli/user/json-mode.md), so start them empty.
	result := &CloneResult{TargetDir: targetDir, RejectedPaths: []string{}, NotEnumerable: []string{}}

	// Normalize server URL
	serverURL = normalizeURL(serverURL)

	// Create target directory
	if err := os.MkdirAll(targetDir, 0755); err != nil {
		return nil, fmt.Errorf("failed to create target directory: %w", err)
	}

	// Check for existing clone state
	statePath := filepath.Join(targetDir, ".polis-clone-state.json")
	var state *CloneState
	if !opts.FullClone {
		state, _ = loadState(statePath)
	}

	// Fetch .well-known/polis as RAW BYTES and store them verbatim.
	//
	// ⚠️ It used to be fetched into remote.WellKnown and re-serialized, which
	// silently dropped every field that struct does not model — `bundles`
	// (where all the content lives), `license` (the pointer to the signed
	// terms) and `public_key_messages` among them. A clone's identity document
	// is not ours to rewrite: copy the bytes the site published, then read them.
	wkRaw, err := client.FetchContent(serverURL + "/.well-known/polis")
	if err != nil {
		return nil, fmt.Errorf("failed to fetch .well-known/polis: %w", err)
	}

	wkDest, ok := safeDest(targetDir, filepath.Join(".well-known", "polis"))
	if !ok {
		return nil, fmt.Errorf("refusing to write .well-known/polis: it would land outside %s", targetDir)
	}
	if err := os.MkdirAll(filepath.Dir(wkDest), 0755); err != nil {
		return nil, err
	}
	if err := os.WriteFile(wkDest, []byte(wkRaw), 0644); err != nil {
		return nil, err
	}

	var wk wellKnown
	if err := json.Unmarshal([]byte(wkRaw), &wk); err != nil {
		return nil, fmt.Errorf("failed to parse .well-known/polis: %w", err)
	}

	// Fetch every bundle the site declares, at the path the site declares it
	// at, and resolve content directories from what comes back.
	bundles := fetchBundles(client, serverURL, targetDir, &wk, result)
	dirs := newDirResolver(bundles)

	// Create the core content directory so the tree shape matches a real site
	// even when the site is empty.
	bundleDir, ok := safeDest(targetDir, dirs.bundleRoot())
	if !ok {
		return nil, fmt.Errorf("refusing bundle root %s: it would land outside %s", dirs.bundleRoot(), targetDir)
	}
	if err := os.MkdirAll(bundleDir, 0755); err != nil {
		return nil, err
	}

	// Fetch public index
	entries, err := client.FetchPublicIndex(serverURL)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch index.jsonl: %w", err)
	}

	// Save index.jsonl
	indexFile, err := os.Create(filepath.Join(bundleDir, "index.jsonl"))
	if err != nil {
		return nil, err
	}
	for _, entry := range entries {
		data, err := json.Marshal(entry)
		if err != nil {
			indexFile.Close()
			return nil, fmt.Errorf("marshal index entry: %w", err)
		}
		if _, err := indexFile.WriteString(string(data) + "\n"); err != nil {
			indexFile.Close()
			return nil, fmt.Errorf("write index entry: %w", err)
		}
	}
	if err := indexFile.Close(); err != nil {
		return nil, fmt.Errorf("close index file: %w", err)
	}

	// Download indexed content. Modern public indexes (post-bundle-refactor)
	// emit path-based entries with the URL field left empty — the canonical
	// fetch URL is derived as serverURL + "/" + entry.Path. Older indexes that
	// populated URL directly are still supported via the URL fallback.
	//
	// ⚠️ COMMENTS ARE INDEXED TOO, and used to be skipped: the loop filtered
	// on type "post", so `comments_downloaded` was reported on every run and
	// incremented on none. A clone with no comment files cannot have its
	// comment signatures checked.
	// Which content types the source ENUMERATES, recorded before the
	// incremental skip so an up-to-date clone does not report a type as
	// missing merely because it downloaded nothing this run.
	enumerated := map[string]bool{}
	for _, entry := range entries {
		enumerated[entry.Type] = true
	}

	for _, entry := range entries {
		switch entry.Type {
		case "post", "comment", "tag", "attestation":
		default:
			// A type this build has never heard of. Skipping it is right —
			// we do not know where it belongs — and it is still counted as
			// enumerated above, so nothing is claimed about it either way.
			continue
		}

		// Check if we should skip (incremental mode)
		if state != nil && !opts.FullClone {
			if entry.Published <= state.LastUpdated {
				continue // Skip already-downloaded content
			}
		}

		relPath := entry.Path
		if relPath == "" {
			relPath = strings.TrimPrefix(entry.URL, serverURL+"/")
		}
		contentURL := entry.URL
		if contentURL == "" {
			contentURL = serverURL + "/" + strings.TrimPrefix(relPath, "/")
		}

		// Local path mirrors the remote's source-content layout under
		// targetDir so subsequent `polis render` finds the markdown where it
		// expects.
		//
		// ⛔ relPath is the REMOTE's bytes. A hostile index listing
		// "../../.bashrc" would otherwise write outside the clone folder.
		dest, ok := safeDest(targetDir, relPath)
		if !ok {
			result.RejectedPaths = append(result.RejectedPaths, relPath)
			continue
		}
		if !writeFetched(client, contentURL, dest) {
			result.Errors++
			continue
		}

		switch entry.Type {
		case "post":
			result.PostsDownloaded++
		case "comment":
			result.CommentsDownloaded++
		case "tag":
			result.TagsDownloaded++
		case "attestation":
			result.AttestationsDownloaded++
		}
	}

	// blessed.json — the signed record of blessings this site published.
	commentDir := dirs.contentDir(typeComment, "comment")
	blessedRel := filepath.Join(commentDir, "blessed.json")
	if blessedDest, ok := safeDest(targetDir, blessedRel); !ok {
		result.RejectedPaths = append(result.RejectedPaths, blessedRel)
	} else {
		os.MkdirAll(filepath.Dir(blessedDest), 0755)
		if content, err := client.FetchContent(serverURL + "/" + filepath.ToSlash(blessedRel)); err == nil {
			if err := os.WriteFile(blessedDest, []byte(content), 0644); err == nil {
				var bc struct {
					Comments []struct {
						Blessed []interface{} `json:"blessed"`
					} `json:"comments"`
				}
				if json.Unmarshal([]byte(content), &bc) == nil {
					for _, post := range bc.Comments {
						result.BlessedCommentsSynced += len(post.Blessed)
					}
				}
			}
		}
	}

	// following.json — the signed authored edge list.
	followDir := dirs.contentDir(typeFollow, "follow")
	followingRel := filepath.Join(followDir, "following.json")
	if followingDest, ok := safeDest(targetDir, followingRel); !ok {
		result.RejectedPaths = append(result.RejectedPaths, followingRel)
	} else {
		os.MkdirAll(filepath.Dir(followingDest), 0755)
		if content, err := client.FetchContent(serverURL + "/" + filepath.ToSlash(followingRel)); err == nil {
			os.WriteFile(followingDest, []byte(content), 0644)
		}
	}

	// license.json — found by FOLLOWING THE POINTER in .well-known/polis, which
	// is the only supported way. `dir` and `mount` are user-configurable, so a
	// hardcoded /license path would be wrong on any site that moved it, and an
	// absent pointer means the author has stated no terms rather than that we
	// failed to look.
	if rel := licenseRelPath(&wk); rel != "" {
		if dest, ok := safeDest(targetDir, rel); !ok {
			result.RejectedPaths = append(result.RejectedPaths, rel)
		} else if writeFetched(client, serverURL+"/"+filepath.ToSlash(rel), dest) {
			result.LicenseCloned = true
		}
	}

	// .well-known/did.json — the same identity key in a second encoding. Its
	// location is fixed by the did:web method, not by our config, so this one
	// path really is constant.
	if dest, ok := safeDest(targetDir, filepath.Join(".well-known", "did.json")); ok &&
		writeFetched(client, serverURL+"/.well-known/did.json", dest) {
		result.DIDDocumentCloned = true
	}

	// pub.polis.tag and pub.polis.attestation are CONTENT, fetched by the loop
	// above like any other indexed line — since Signet epic 25 gave each
	// content type a contributor to index.jsonl.
	//
	// ⛔ Before that they were unreachable, and not because the fetch was
	// missing: a polis site published NO INDEX of either one. index.jsonl
	// carried posts and comments; .well-known/polis carried bundles and
	// pointers; sitemap.xml carried rendered post pages. Both are flat files
	// under their content dir whose names are unknowable from outside, and
	// HTTP does not list directories. (The rendered /tags page enumerates tag
	// names, but it is a theme-overridable PROJECTION, and scraping it would
	// make clone depend on markup.)
	//
	// So a source whose index still carries no line of a type is either a site
	// with none of that type or a site running an older CLI, and the two are
	// indistinguishable from here. Reported rather than papered over.
	for _, t := range []struct{ entryType, typeName string }{
		{"tag", "pub.polis.tag"},
		{"attestation", attestation.TypeName},
	} {
		if !enumerated[t.entryType] {
			result.NotEnumerable = append(result.NotEnumerable, t.typeName)
		}
	}

	// Save clone state
	newState := &CloneState{
		SourceURL:   serverURL,
		ClonedAt:    time.Now().UTC().Format("2006-01-02T15:04:05Z"),
		LastUpdated: time.Now().UTC().Format("2006-01-02T15:04:05Z"),
		PostsCount:  result.PostsDownloaded,
	}
	if state != nil {
		newState.ClonedAt = state.ClonedAt
		newState.PostsCount = state.PostsCount + result.PostsDownloaded
	}
	saveState(statePath, newState)

	return result, nil
}

// wellKnown is the subset of .well-known/polis clone reads. Deliberately
// separate from remote.WellKnown, which models the display fields and not the
// pointers — and deliberately used only for READING, never for writing the file
// back out.
type wellKnown struct {
	Bundles map[string]struct {
		Path string `json:"path"`
	} `json:"bundles"`
	License string `json:"license"`
}

// fetchBundles downloads every bundle.json the site declares, writes it at the
// declared path, and returns the ones that parsed. A site with no bundles block
// (pre-refactor) yields none, and the caller falls back to the default layout.
func fetchBundles(client *remote.Client, serverURL, targetDir string, wk *wellKnown, result *CloneResult) []*bundle.Bundle {
	var out []*bundle.Bundle
	for _, entry := range wk.Bundles {
		if entry.Path == "" {
			continue
		}
		rel := filepath.FromSlash(strings.TrimPrefix(entry.Path, "/"))
		dest, ok := safeDest(targetDir, rel)
		if !ok {
			result.RejectedPaths = append(result.RejectedPaths, rel)
			continue
		}
		content, err := client.FetchContent(serverURL + "/" + strings.TrimPrefix(entry.Path, "/"))
		if err != nil {
			result.Errors++
			continue
		}
		if err := os.MkdirAll(filepath.Dir(dest), 0755); err != nil {
			result.Errors++
			continue
		}
		if err := os.WriteFile(dest, []byte(content), 0644); err != nil {
			result.Errors++
			continue
		}
		var b bundle.Bundle
		if err := json.Unmarshal([]byte(content), &b); err != nil {
			result.Errors++
			continue
		}
		// The bundle's name and every type's `dir` become write paths below.
		// A declaration naming any directory outside the clone is not used at
		// all — the clone falls back to the default layout for it.
		if unsafe := unsafeBundleDirs(targetDir, &b); len(unsafe) > 0 {
			result.RejectedPaths = append(result.RejectedPaths, unsafe...)
			continue
		}
		out = append(out, &b)
	}
	return out
}

// dirResolver answers "where does this content type live on this site", using
// the site's own declaration and falling back to the default layout when it
// declares nothing.
type dirResolver struct {
	bundles []*bundle.Bundle
}

func newDirResolver(bundles []*bundle.Bundle) *dirResolver {
	return &dirResolver{bundles: bundles}
}

// bundleRoot returns the directory holding the core bundle's content, which is
// also where index.jsonl lives.
func (d *dirResolver) bundleRoot() string {
	for _, b := range d.bundles {
		if b.Name == "pub.polis.core" {
			return filepath.Join("content", b.Name)
		}
	}
	return filepath.Join("content", "pub.polis.core")
}

// contentDir returns the site-relative source directory for a content type.
// fallbackDir is the default-layout leaf used when the site declares nothing.
func (d *dirResolver) contentDir(typeName, fallbackDir string) string {
	for _, b := range d.bundles {
		if dir, err := b.ContentDir(typeName); err == nil {
			return dir
		}
	}
	return filepath.Join("content", "pub.polis.core", fallbackDir)
}

// licenseRelPath resolves where the licence document lives by FOLLOWING THE
// POINTER, which is the only supported way — `dir` and `mount` are
// user-configurable, so a conventional path would be wrong on any site that
// moved one.
//
// ⛔ There is deliberately NO fallback to the bundle's licence directory. An
// absent pointer means the author has stated no terms; guessing a path and
// finding a stray file would clone a statement the site does not make.
func licenseRelPath(wk *wellKnown) string {
	if wk.License == "" {
		return ""
	}
	return filepath.FromSlash(strings.TrimPrefix(wk.License, "/"))
}

// unsafeBundleDirs returns the site-relative directories a bundle declaration
// names that would land outside targetDir.
func unsafeBundleDirs(targetDir string, b *bundle.Bundle) []string {
	var unsafe []string
	root := filepath.Join("content", b.Name)
	if _, ok := safeDest(targetDir, root); !ok || b.Name == "" {
		unsafe = append(unsafe, root)
	}
	for name := range b.Types {
		dir, err := b.ContentDir(name)
		if err != nil {
			continue
		}
		if _, ok := safeDest(targetDir, dir); !ok {
			unsafe = append(unsafe, dir)
		}
	}
	sort.Strings(unsafe)
	return unsafe
}

// safeDest resolves a REMOTE-SUPPLIED relative path under targetDir, and
// reports false when the write would land anywhere else.
//
// ⛔ Every path a clone writes that came from the source site goes through
// here. A clone copies a stranger's site, so an index entry, a bundle
// declaration or a pointer is attacker-controlled bytes.
//
// Rejected: absolute paths, paths that climb out after cleaning, and paths
// that leave through a symlink already present inside the target — the check
// is on where the write LANDS, resolved on disk, not on how the string looks.
func safeDest(targetDir, rel string) (string, bool) {
	if rel == "" || strings.HasPrefix(filepath.ToSlash(rel), "/") || filepath.IsAbs(rel) {
		return "", false
	}
	if !filepath.IsLocal(rel) {
		return "", false
	}
	dest := filepath.Join(targetDir, rel)

	realTarget, err := filepath.EvalSymlinks(targetDir)
	if err != nil {
		return "", false
	}
	// Resolve the deepest part of dest that already exists — including dest
	// itself, since writing to an existing symlink follows it.
	existing := dest
	for {
		if _, err := os.Lstat(existing); err == nil {
			break
		}
		parent := filepath.Dir(existing)
		if parent == existing {
			return "", false
		}
		existing = parent
	}
	realExisting, err := filepath.EvalSymlinks(existing)
	if err != nil {
		return "", false // a dangling symlink: where it leads is unknowable
	}
	within, err := filepath.Rel(realTarget, realExisting)
	if err != nil || within == ".." || strings.HasPrefix(within, ".."+string(filepath.Separator)) || filepath.IsAbs(within) {
		return "", false
	}
	return dest, true
}

// writeFetched downloads url and writes it to dest, creating parents. Returns
// false when the resource is absent or unwritable — absence is expected for
// most optional artifacts and is never an error here.
func writeFetched(client *remote.Client, url, dest string) bool {
	content, err := client.FetchContent(url)
	if err != nil {
		return false
	}
	if err := os.MkdirAll(filepath.Dir(dest), 0755); err != nil {
		return false
	}
	return os.WriteFile(dest, []byte(content), 0644) == nil
}

// normalizeURL ensures URL has https:// prefix and no trailing slash.
func normalizeURL(url string) string {
	if !strings.HasPrefix(url, "http://") && !strings.HasPrefix(url, "https://") {
		url = "https://" + url
	}
	url = strings.TrimSuffix(url, "/")
	return url
}

// loadState loads the clone state from disk.
func loadState(path string) (*CloneState, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}

	var state CloneState
	if err := json.Unmarshal(data, &state); err != nil {
		return nil, err
	}

	return &state, nil
}

// saveState saves the clone state to disk.
func saveState(path string, state *CloneState) error {
	data, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(data, '\n'), 0644)
}

// ExtractDomainForDir extracts a directory name from a URL.
func ExtractDomainForDir(url string) string {
	url = strings.TrimPrefix(url, "https://")
	url = strings.TrimPrefix(url, "http://")
	url = strings.TrimSuffix(url, "/")
	if idx := strings.Index(url, "/"); idx > 0 {
		url = url[:idx]
	}
	return url
}
