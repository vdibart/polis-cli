package ops

import (
	"fmt"

	"github.com/vdibart/polis-cli/cli-go/pkg/bundle"
	"github.com/vdibart/polis-cli/cli-go/pkg/render"
	"github.com/vdibart/polis-cli/cli-go/pkg/resolve"
)

// RenderSite renders all content for a site using the bundle configuration to
// determine source and mount directories.
func RenderSite(resolver *resolve.Resolver, coreBundle *bundle.Bundle, baseURL, cliThemesDir string) (*render.RenderStats, error) {
	postsSource, _ := coreBundle.ContentDir("pub.polis.post")
	postsMountDir, _ := coreBundle.MountDir("pub.polis.post")
	commentsSource, _ := coreBundle.ContentDir("pub.polis.comment")
	commentsMountDir, _ := coreBundle.MountDir("pub.polis.comment")

	renderer, err := render.NewPageRenderer(render.PageConfig{
		DataDir:           resolver.SiteDir(),
		CLIThemesDir:      cliThemesDir,
		BaseURL:           baseURL,
		RenderMarkers:     false,
		PostsSourceDir:    postsSource,
		PostsMountDir:     postsMountDir,
		CommentsSourceDir: commentsSource,
		CommentsMountDir:  commentsMountDir,
	})
	if err != nil {
		return nil, fmt.Errorf("create renderer: %w", err)
	}

	stats, err := renderer.RenderAll(true)
	if err != nil {
		return stats, fmt.Errorf("render: %w", err)
	}

	return stats, nil
}
