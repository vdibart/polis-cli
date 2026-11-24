package ops

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	pathpkg "path"
	"strings"
	"time"

	"github.com/vdibart/polis-cli/cli-go/pkg/comment"
	"github.com/vdibart/polis-cli/cli-go/pkg/contentpath"
	"github.com/vdibart/polis-cli/cli-go/pkg/discovery"
	"github.com/vdibart/polis-cli/cli-go/pkg/draft"
	"github.com/vdibart/polis-cli/cli-go/pkg/metadata"
	"github.com/vdibart/polis-cli/cli-go/pkg/publish"
	"github.com/vdibart/polis-cli/cli-go/pkg/render"
	"github.com/vdibart/polis-cli/cli-go/pkg/signing"
	"github.com/vdibart/polis-cli/cli-go/pkg/storage"
)

// postSourcePrefix is builtin_core.go's.
const (
	commentSourcePrefix = "content/pub.polis.core/comment/"
	commentDraftsDir    = ".polis/bundles/pub.polis.core/comments/drafts"
)

// UnpublishResult is what taking an item down did.
type UnpublishResult struct {
	Kind       string // "post" or "comment"
	Path       string // the source path it had
	ContentURL string // its address, as the discovery service knew it
	DraftKey   string // where its text now waits, as a draft
	DraftID    string // a post's draft id (the v5 app's drafts); "" for a comment
	// Discovery is what happened at the discovery service: "unpublished",
	// "skipped" (no discovery service, or this site is not registered with it)
	// or "failed: <why>". A failure there never undoes the local unpublish:
	// the site is the source, and the DS can be told again (polis unpublish --url).
	Discovery string
}

// Unpublish takes one of the site's published posts or comments down: the
// discovery service is told (signed with the site's key, when the site is
// registered there), the text goes back to drafts without its signature and
// versions, and the published file, its version history, its rendered pages
// and its index entry are removed, and the manifest is updated. Rendering the
// site afterwards is the caller's (the after-write module, or the CLI's own).
//
// ⭐ THE ONE IMPLEMENTATION: polis unpublish and /v1's post.unpublish both
// call it. It cannot be undone: publishing the draft again is a new
// publication, and blessed comments on a post are orphaned.
func Unpublish(site storage.Site, contentPath, baseURL, discoveryURL string, privateKey []byte) (*UnpublishResult, error) {
	isPost := strings.HasPrefix(contentPath, postSourcePrefix)
	isComment := strings.HasPrefix(contentPath, commentSourcePrefix)
	if !isPost && !isComment {
		return nil, fmt.Errorf("invalid path: it must start with %s or %s: %s", postSourcePrefix, commentSourcePrefix, contentPath)
	}
	if err := contentpath.Validate(contentPath); err != nil {
		return nil, fmt.Errorf("invalid path: %w", err)
	}
	if !strings.HasSuffix(contentPath, ".md") {
		return nil, fmt.Errorf("invalid path: it must name a published .md file: %s", contentPath)
	}
	ctx := context.Background()
	st := site.Store()
	data, err := st.Get(ctx, contentPath)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, fmt.Errorf("content %s: %w", contentPath, ErrNotFound)
	}
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", contentPath, err)
	}
	published := string(data)

	res := &UnpublishResult{Kind: "post", Path: contentPath, Discovery: "skipped"}
	dsType := "pub.polis.post"
	if isComment {
		res.Kind, dsType = "comment", "pub.polis.comment"
	}
	res.ContentURL = strings.TrimRight(baseURL, "/") + "/" + contentPath

	// The discovery service first, as polis unpublish always has: a reader's
	// feed stops offering it even if a local step below fails.
	if discoveryURL != "" && baseURL != "" && len(privateKey) > 0 && discovery.IsRegisteredLocally(site, discoveryURL) {
		ts := time.Now().UTC().Format(time.RFC3339)
		sig, serr := signing.SignContent([]byte(discovery.MakeContentUnpublishCanonicalJSON(dsType, res.ContentURL, ts)), privateKey)
		if serr != nil {
			return nil, fmt.Errorf("sign the unpublish request: %w", serr)
		}
		if derr := discovery.NewClient(discoveryURL).UnpublishContent(dsType, res.ContentURL, ts, sig); derr != nil {
			res.Discovery = "failed: " + derr.Error()
		} else {
			res.Discovery = "unpublished"
		}
	}

	// Back to drafts, stripped of its signature and versions.
	fm := publish.ParseFrontmatter(published)
	title := fm["title"]
	if title == "" {
		title = pathpkg.Base(contentPath)
	}
	body := publish.StripFrontmatter(published)
	base := pathpkg.Base(contentPath)
	if isPost {
		title = publish.UnquoteYAMLString(title)
		text := "# " + title + "\n\n" + render.StripLeadingTitleHeading(body, title)
		id, derr := draft.Save(site, strings.TrimSuffix(base, ".md"), text)
		if derr != nil {
			return nil, fmt.Errorf("write the draft: %w", derr)
		}
		res.DraftID, res.DraftKey = id, draft.Key(id)
	} else {
		res.DraftKey = commentDraftsDir + "/" + base
		if perr := st.Put(ctx, res.DraftKey, []byte(comment.UnpublishedDraftContent(published, title, body))); perr != nil {
			return nil, fmt.Errorf("write the draft: %w", perr)
		}
	}

	// The published file and what was made from it.
	dateDir := pathpkg.Base(pathpkg.Dir(contentPath))
	if isPost {
		_ = st.Delete(ctx, postSourcePrefix+dateDir+"/.versions/"+base, storage.IgnoreMissing())
	}
	if derr := st.Delete(ctx, contentPath); derr != nil {
		return nil, fmt.Errorf("delete the published file: %w", derr)
	}
	html := strings.TrimSuffix(contentPath, ".md") + ".html"
	_ = st.Delete(ctx, html, storage.IgnoreMissing())
	if isPost {
		_ = st.Delete(ctx, "posts/"+dateDir+"/"+strings.TrimSuffix(base, ".md")+".html", storage.IgnoreMissing())
	}
	if ierr := metadata.RemoveIndexEntry(site, contentPath); ierr != nil {
		return nil, fmt.Errorf("remove the index entry: %w", ierr)
	}
	if merr := publish.UpdateManifest(site); merr != nil {
		return nil, fmt.Errorf("update the manifest: %w", merr)
	}
	return res, nil
}
