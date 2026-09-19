// Package index rebuilds a site's projections — the files that could be
// regenerated from the content if they were lost.
//
// ⭐ The rule the package exists to keep (Law 1's corollary, stated as
// ownership): a projection may have MANY sources, and any regenerator that
// knows one source must not own the whole file. See contributors.go.
package index

import (
	"errors"
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/vdibart/polis-cli/cli-go/pkg/discovery"
	"github.com/vdibart/polis-cli/cli-go/pkg/metadata"
	"github.com/vdibart/polis-cli/cli-go/pkg/notification"
)

// Version is set at init time by cmd package.
var Version = "dev"

// GetGenerator returns the generator identifier for metadata files.
func GetGenerator() string {
	return "polis-cli-go/" + Version
}

// RebuildOptions configures what to rebuild.
//
// The content-index flags select CONTRIBUTORS (contributors.go); everything
// they do not name is preserved byte-identically.
type RebuildOptions struct {
	Posts        bool
	Comments     bool
	Tags         bool
	Attestations bool
	All          bool

	// Notifications is a DEPRECATED alias kept so no one's script breaks. It
	// clears state; it reconstructs nothing, so it is not a rebuild. The verb
	// is `polis notifications clear`.
	Notifications bool

	// Discovery service params for blessed comments recovery
	DiscoveryURL string
	DiscoveryKey string
	BaseURL      string // Site base URL (e.g., https://alice.polis.pub)
	Generator    string // e.g. "polis-cli-go/0.59.0" — used in metadata
	// NewDSClient is a hook for tests to inject a discovery client (e.g. one
	// with DSKeyCache disabled so a fake DS need not sign its responses).
	// Production leaves it nil and a normal verified client is constructed.
	// Same seam Clerk uses (webapp/internal/hosted/clerk.go).
	NewDSClient func(url, key string) *discovery.Client
}

// contentTypes returns the entry types this run rebuilds, and whether the
// content index is being touched at all.
//
// `--all` means every contributor, expressed as an empty selection.
func (o RebuildOptions) contentTypes() (only []string, touched bool) {
	if o.All {
		return nil, true
	}
	for _, sel := range []struct {
		on        bool
		entryType string
	}{
		{o.Posts, EntryTypePost},
		{o.Comments, EntryTypeComment},
		{o.Tags, EntryTypeTag},
		{o.Attestations, EntryTypeAttestation},
	} {
		if sel.on {
			only = append(only, sel.entryType)
		}
	}
	return only, len(only) > 0
}

// RebuildResult contains the results of a rebuild operation.
type RebuildResult struct {
	PostsRebuilt         int `json:"posts_rebuilt"`
	CommentsRebuilt      int `json:"comments_rebuilt"`
	NotificationsCleared int `json:"notifications_cleared"`

	// ContentIndex reports per-type what was rebuilt, what was PRESERVED, and
	// what was skipped. Nil when this run did not touch index.jsonl.
	ContentIndex *ContentIndexResult `json:"content_index,omitempty"`
}

// RebuildIndex regenerates the projections named by opts.
func RebuildIndex(dataDir string, opts RebuildOptions) (*RebuildResult, error) {
	result := &RebuildResult{}

	if only, touched := opts.contentTypes(); touched {
		ci, err := RebuildContentIndex(dataDir, only)
		if err != nil {
			return nil, fmt.Errorf("failed to rebuild content index: %w", err)
		}
		result.ContentIndex = ci
		result.PostsRebuilt = ci.PostsRebuilt()
	}

	// ⚠️ `--comments` DOES TWO JOBS, on two files, by two mechanisms, and
	// always has: the comment slice of index.jsonl is
	// REBUILT above, from the comment files on disk, like every other type;
	// blessed.json is RECONCILED here, which means preserved untouched when it
	// is readable and recovered from the DS only when it is missing. They share
	// a flag only because both are called "comments". Documented rather than
	// split: a user rebuilding comments wants both, and splitting the flag
	// changes what an existing script does.
	if opts.All || opts.Comments {
		count, err := rebuildCommentsIndex(dataDir, opts)
		if err != nil {
			return nil, fmt.Errorf("failed to rebuild comments index: %w", err)
		}
		result.CommentsRebuilt = count
	}

	// ⚠️ NOT part of `--all`. Clearing notifications reconstructs nothing —
	// it is a delete — so it is reached only by naming the deprecated flag.
	if opts.Notifications {
		count, err := notification.ClearAll(dataDir)
		if err != nil {
			return nil, fmt.Errorf("failed to clear notifications: %w", err)
		}
		result.NotificationsCleared = count
	}

	return result, nil
}

// rebuildCommentsIndex reconciles blessed.json — by PRESERVING it.
//
// ⚠️ R24-2 / SIGNET epic 14 D1. This function used to rebuild blessed.json from
// DS relationship records, and the DS does not carry a blessed comment's VERSION
// PIN. So every rebuild silently dropped it, and with it the canonical
// "edited since blessing" signal: comment.IsEditedSinceBlessing reads an empty
// pin as "can't tell" and returns false, which is indistinguishable from "not
// edited". A comment edited after being blessed simply stopped being detectable.
//
// The missing `Version` was the symptom. The defect was the category error:
// blessed.json is AUTHORED — the blesser decided, the local record was written,
// and the DS learned afterward — and rebuild treated it as a projection of DS
// state. Under Law 1 you never rebuild a source from something derived from it,
// and the pin is the proof: it never existed on the DS, so it could not come
// back from there no matter how carefully the mapping was written.
//
// So: if the file is there, it is the truth and rebuild does not touch it. The
// DS is consulted only to RECOVER a file that is missing entirely, where the
// choice is between partial evidence and none.
func rebuildCommentsIndex(dataDir string, opts RebuildOptions) (int, error) {
	gen := opts.Generator
	if gen == "" {
		gen = GetGenerator()
	}
	d := newSiteDirs(dataDir)
	commentDir := d.contentAbs("pub.polis.comment", "comment")
	if err := os.MkdirAll(commentDir, 0755); err != nil {
		return 0, err
	}

	existing, err := metadata.LoadBlessedComments(dataDir)
	switch {
	case err == nil:
		// PRESERVED, untouched — pins, signature and all. Rewriting it even
		// byte-identically would clear the signature (the unsigned write path),
		// so the right number of writes here is zero.
		return countBlessedEntries(existing), nil

	case !errors.Is(err, os.ErrNotExist):
		// Present but unreadable. Rebuilding OVER it would destroy pins a human
		// could still recover by hand, so refuse and say so. An honest failure
		// beats a silent overwrite of authored data.
		return 0, fmt.Errorf("blessed.json exists but could not be parsed (%w) — "+
			"it is authored, not derived, so rebuild will not overwrite it; "+
			"move it aside to rebuild from the discovery service", err)
	}

	// From here: the file is genuinely absent.
	return recoverCommentsIndex(dataDir, commentDir, gen, opts)
}

// recoverCommentsIndex reconstructs an ABSENT blessed.json from DS relationship
// records, or creates an empty one when there is nothing to recover.
//
// ⚠️ Recovery is lossy and the loss is structural, not a bug to fix later: the
// DS stores which comment was blessed, never which VERSION of it was blessed.
// Recovered entries therefore have no pin, so "edited since blessing" cannot be
// answered for them until their author blesses again. That is worth saying out
// loud rather than leaving a reader to infer it from an empty field.
//
// Written UNSIGNED, deliberately. A reconstruction is not the author asserting
// anything — nobody was asked — and epic 01's rule holds: never assert that
// someone said something they did not.
func recoverCommentsIndex(dataDir, commentDir, gen string, opts RebuildOptions) (int, error) {
	if opts.DiscoveryURL != "" && opts.DiscoveryKey != "" && opts.BaseURL != "" {
		makeClient := opts.NewDSClient
		if makeClient == nil {
			makeClient = discovery.NewClient
		}
		client := makeClient(opts.DiscoveryURL, opts.DiscoveryKey)

		domain := opts.BaseURL
		domain = strings.TrimPrefix(domain, "https://")
		domain = strings.TrimPrefix(domain, "http://")
		domain = strings.TrimSuffix(domain, "/")

		resp, err := client.QueryRelationships("pub.polis.comment.blessing", map[string]string{
			"actor":  domain,
			"status": "granted",
		})
		if err == nil && len(resp.Records) > 0 {
			bc := &metadata.BlessedComments{
				Version:  gen,
				Comments: []metadata.PostComments{},
			}

			// Group by post (target_url)
			postMap := make(map[string][]metadata.BlessedComment)
			order := []string{}
			for _, rel := range resp.Records {
				postPath := rel.TargetURL
				if idx := strings.Index(postPath, "/posts/"); idx >= 0 {
					postPath = postPath[idx+1:]
				}
				if _, seen := postMap[postPath]; !seen {
					order = append(order, postPath)
				}
				// Version is deliberately left empty: the DS never had it. Do not
				// invent one from the comment's CURRENT version — that would claim
				// the author blessed whatever it says today, which is the exact
				// edit the pin exists to reveal.
				postMap[postPath] = append(postMap[postPath], metadata.BlessedComment{
					URL:       rel.SourceURL,
					BlessedAt: rel.UpdatedAt,
				})
			}

			// Deterministic output: map iteration order is randomised, and this
			// file is now covered by a signature whose bytes must be reproducible.
			sort.Strings(order)
			for _, post := range order {
				bc.Comments = append(bc.Comments, metadata.PostComments{
					Post:    post,
					Blessed: postMap[post],
				})
			}

			if err := metadata.SaveBlessedComments(dataDir, bc); err != nil {
				return 0, err
			}

			return len(resp.Records), nil
		}
		// Fetch failed or found nothing — fall through to the empty file.
	}

	bc := &metadata.BlessedComments{
		Version:  gen,
		Comments: []metadata.PostComments{},
	}
	if err := metadata.SaveBlessedComments(dataDir, bc); err != nil {
		return 0, err
	}
	return 0, nil
}

// countBlessedEntries totals the blessed comments across all posts, so a
// preserving rebuild still reports a truthful count.
func countBlessedEntries(bc *metadata.BlessedComments) int {
	n := 0
	for _, pc := range bc.Comments {
		n += len(pc.Blessed)
	}
	return n
}

// parseFrontmatter extracts frontmatter fields from content.
//
// ⚠️ FLAT ONLY. It trims keys, so an indented child of a nested block
// (`in-reply-to:`, `license:`) lands in the same map as a top-level field.
// Read nested blocks with a parser that knows about indentation —
// parseInReplyTo does.
func parseFrontmatter(content string) (map[string]string, string) {
	fm := make(map[string]string)
	lines := strings.Split(content, "\n")

	if len(lines) == 0 || strings.TrimSpace(lines[0]) != "---" {
		return fm, content
	}

	var bodyStart int
	for i := 1; i < len(lines); i++ {
		if strings.TrimSpace(lines[i]) == "---" {
			bodyStart = i + 1
			break
		}
		if idx := strings.Index(lines[i], ":"); idx > 0 {
			key := strings.TrimSpace(lines[i][:idx])
			value := strings.TrimSpace(lines[i][idx+1:])
			fm[key] = value
		}
	}

	body := ""
	if bodyStart < len(lines) {
		body = strings.Join(lines[bodyStart:], "\n")
	}

	return fm, body
}

// canonicalizeContent normalizes content for hashing.
func canonicalizeContent(content string) string {
	content = strings.TrimLeft(content, "\n")
	lines := strings.Split(content, "\n")
	for i := range lines {
		lines[i] = strings.TrimRight(lines[i], " \t")
	}
	for len(lines) > 0 && lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	return strings.Join(lines, "\n") + "\n"
}
