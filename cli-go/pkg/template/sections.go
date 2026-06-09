package template

import (
	"fmt"
	"regexp"
	"strings"
)

// sectionOpenPattern matches {{#name}} opening tags.
var sectionOpenPattern = regexp.MustCompile(`\{\{#(\w+)\}\}`)

// loopVarPattern matches {{name}} variable references inside loop bodies.
// Hoisted to package scope so substituteLoopVariables doesn't recompile it
// once per call (the function runs in the per-template-substitution hot path).
var loopVarPattern = regexp.MustCompile(`\{\{(\w+)\}\}`)

// commentCountClass returns "is-empty" when count <= 0 and "" otherwise.
// Used by sibling-iteration rendering and stream.html top-level focus
// substitution (engine.go) so the badge-hide grammar is identical for
// SSR'd siblings + the SSR'd focus + JS-rendered entries.
func commentCountClass(count int) string {
	if count > 0 {
		return ""
	}
	return "is-empty"
}

// commentCountDisplay returns "" for count <= 0 (no "0" badge) and the
// stringified count otherwise. Pairs with commentCountClass.
func commentCountDisplay(count int) string {
	if count > 0 {
		return fmt.Sprintf("%d", count)
	}
	return ""
}

// processSections expands all {{#section}}...{{/section}} loops in the template.
// Supported sections:
// - {{#posts}}...{{/posts}} - Loop over posts
// - {{#comments}}...{{/comments}} - Loop over comments
// - {{#blessed_comments}}...{{/blessed_comments}} - Loop over blessed comments on a post
// - {{#recent_posts}}...{{/recent_posts}} - Loop over 10 most recent posts
// - {{#recent_comments}}...{{/recent_comments}} - Loop over 10 most recent comments
// - {{#following}}...{{/following}} - Loop over followed authors
func (e *Engine) processSections(template string, ctx *RenderContext, depth int) (string, error) {
	// Process sections iteratively since Go regex doesn't support backreferences
	result := template
	var lastErr error

	// Keep processing until no more sections are found
	for {
		match := sectionOpenPattern.FindStringSubmatchIndex(result)
		if match == nil {
			break
		}

		// Extract the section name
		sectionName := result[match[2]:match[3]]
		openTag := result[match[0]:match[1]]
		closeTag := "{{/" + sectionName + "}}"

		// Find the matching close tag
		openTagEnd := match[1]
		closeTagStart := strings.Index(result[openTagEnd:], closeTag)
		if closeTagStart == -1 {
			// No matching close tag, skip this opening tag
			break
		}
		closeTagStart += openTagEnd

		// Extract section content
		sectionContent := result[openTagEnd:closeTagStart]

		var output string
		var err error

		switch sectionName {
		case "posts":
			output, err = e.renderPostsSection(sectionContent, ctx, depth)
		case "comments":
			output, err = e.renderCommentsSection(sectionContent, ctx, depth)
		case "blessed_comments":
			output, err = e.renderBlessedCommentsSection(sectionContent, ctx, depth)
		case "recent_posts":
			output, err = e.renderRecentPostsSection(sectionContent, ctx, depth)
		case "recent_comments":
			output, err = e.renderRecentCommentsSection(sectionContent, ctx, depth)
		case "siblings":
			output, err = e.renderSiblingsSection(sectionContent, ctx, depth)
		case "siblings_above":
			output, err = e.renderSiblingsAboveSection(sectionContent, ctx, depth)
		case "following":
			output, err = e.renderFollowingSection(sectionContent, ctx, depth)
		case "targets":
			output, err = e.renderTargetsSection(sectionContent, ctx, depth)
		case "tags":
			output, err = e.renderTagsSection(sectionContent, ctx, depth)
		default:
			// Unknown section - leave as-is and continue
			break
		}

		if err != nil {
			lastErr = err
			break
		}

		// Replace the section with its rendered output
		result = result[:match[0]] + output + result[closeTagStart+len(closeTag):]

		// Avoid checking unsupported section names again
		if sectionName != "posts" && sectionName != "comments" && sectionName != "blessed_comments" && sectionName != "recent_posts" && sectionName != "recent_comments" && sectionName != "siblings" && sectionName != "siblings_above" && sectionName != "following" && sectionName != "targets" && sectionName != "tags" {
			// Skip to after this section to avoid infinite loop on unknown sections
			result = result[:match[0]] + openTag + sectionContent + closeTag + result[match[0]:]
			break
		}
	}

	return result, lastErr
}

// renderPostsSection renders the {{#posts}} section for each post.
func (e *Engine) renderPostsSection(content string, ctx *RenderContext, depth int) (string, error) {
	var builder strings.Builder

	for _, post := range ctx.Posts {
		// Create a temporary context for this iteration
		iterCtx := &RenderContext{
			URL:            post.URL,
			Title:          post.Title,
			Published:      post.Published,
			PublishedHuman: post.PublishedHuman,
			CommentCount:   post.CommentCount,

			// Copy site-level variables
			SiteURL:   ctx.SiteURL,
			SiteTitle: ctx.SiteTitle,
			Year:      ctx.Year,
		}

		// Process partials first (before variable substitution) to prevent
		// user data containing {{> partial}} from being interpreted as includes.
		processed, err := e.processPartials(content, iterCtx, depth+1)
		if err != nil {
			return "", err
		}

		// Substitute loop-specific variables
		rendered := e.substituteLoopVariables(processed, map[string]string{
			"url":                   post.URL,
			"title":                 post.Title,
			"excerpt":               post.Excerpt,
			"published":             post.Published,
			"published_human":       post.PublishedHuman,
			"comment_count":         fmt.Sprintf("%d", post.CommentCount),
			"comment_count_display": commentCountDisplay(post.CommentCount),
		})

		builder.WriteString(rendered)
	}

	return builder.String(), nil
}

// renderCommentsSection renders the {{#comments}} section for each comment.
func (e *Engine) renderCommentsSection(content string, ctx *RenderContext, depth int) (string, error) {
	var builder strings.Builder

	for _, comment := range ctx.Comments {
		// Create a temporary context for this iteration
		iterCtx := &RenderContext{
			URL:            comment.URL,
			Published:      comment.Published,
			PublishedHuman: comment.PublishedHuman,
			TargetAuthor:   comment.TargetAuthor,
			Preview:        comment.Preview,

			// Copy site-level variables
			SiteURL:   ctx.SiteURL,
			SiteTitle: ctx.SiteTitle,
			Year:      ctx.Year,
		}

		// Process partials first (before variable substitution)
		processed, err := e.processPartials(content, iterCtx, depth+1)
		if err != nil {
			return "", err
		}

		// Substitute loop-specific variables
		rendered := e.substituteLoopVariables(processed, map[string]string{
			"url":             comment.URL,
			"target_author":   comment.TargetAuthor,
			"published":       comment.Published,
			"published_human": comment.PublishedHuman,
			"preview":         comment.Preview,
		})

		builder.WriteString(rendered)
	}

	return builder.String(), nil
}

// renderBlessedCommentsSection renders the {{#blessed_comments}} section for each blessed comment.
func (e *Engine) renderBlessedCommentsSection(content string, ctx *RenderContext, depth int) (string, error) {
	var builder strings.Builder

	for _, bc := range ctx.BlessedComments {
		// Create a temporary context for this iteration
		iterCtx := &RenderContext{
			URL:        bc.URL,
			AuthorName: bc.AuthorName,
			// AuthorDomain MUST be set here, not only in substituteLoopVariables
			// below: processPartials (called next) runs ctx-variable substitution
			// with this iterCtx, and the engine maps {{author_domain}} ->
			// ctx.AuthorDomain. An unset field blanks {{author_domain}} in the
			// expanded partial BEFORE the loop-variable pass can fill it — which
			// rendered an empty <a class="entry-handle"> (no commenter byline) on
			// the canonical per-post comment card.
			AuthorDomain:   bc.AuthorDomain,
			Published:      bc.Published,
			PublishedHuman: bc.PublishedHuman,
			Content:        bc.Content,

			// Copy site-level variables
			SiteURL:   ctx.SiteURL,
			SiteTitle: ctx.SiteTitle,
			Year:      ctx.Year,
		}

		// Process partials first (before variable substitution)
		processed, err := e.processPartials(content, iterCtx, depth+1)
		if err != nil {
			return "", err
		}

		// Substitute loop-specific variables
		rendered := e.substituteLoopVariables(processed, map[string]string{
			"url":                bc.URL,
			"author_name":        bc.AuthorName,
			"author_domain":      bc.AuthorDomain,
			"author_avatar_html": bc.AuthorAvatarHTML,
			"published":          bc.Published,
			"published_human":    bc.PublishedHuman,
			"content":            bc.Content,
		})

		builder.WriteString(rendered)
	}

	return builder.String(), nil
}

// renderRecentPostsSection renders the {{#recent_posts}} section.
// This shows the 10 most recent posts.
func (e *Engine) renderRecentPostsSection(content string, ctx *RenderContext, depth int) (string, error) {
	// Use RecentPosts if available, otherwise use first 10 posts
	posts := ctx.RecentPosts
	if len(posts) == 0 && len(ctx.Posts) > 0 {
		limit := 10
		if len(ctx.Posts) < limit {
			limit = len(ctx.Posts)
		}
		posts = ctx.Posts[:limit]
	}

	var builder strings.Builder

	for _, post := range posts {
		// Create a temporary context for this iteration
		iterCtx := &RenderContext{
			URL:            post.URL,
			Title:          post.Title,
			Published:      post.Published,
			PublishedHuman: post.PublishedHuman,
			CommentCount:   post.CommentCount,

			// Copy site-level variables
			SiteURL:   ctx.SiteURL,
			SiteTitle: ctx.SiteTitle,
			Year:      ctx.Year,
		}

		// Process partials first (before variable substitution)
		processed, err := e.processPartials(content, iterCtx, depth+1)
		if err != nil {
			return "", err
		}

		// Substitute loop-specific variables
		rendered := e.substituteLoopVariables(processed, map[string]string{
			"url":                   post.URL,
			"title":                 post.Title,
			"excerpt":               post.Excerpt,
			"published":             post.Published,
			"published_human":       post.PublishedHuman,
			"comment_count":         fmt.Sprintf("%d", post.CommentCount),
			"comment_count_display": commentCountDisplay(post.CommentCount),
		})

		builder.WriteString(rendered)
	}

	return builder.String(), nil
}

// renderRecentCommentsSection renders the {{#recent_comments}} section.
// This shows the 10 most recent comments.
func (e *Engine) renderRecentCommentsSection(content string, ctx *RenderContext, depth int) (string, error) {
	// Use RecentComments if available, otherwise use first 10 comments
	comments := ctx.RecentComments
	if len(comments) == 0 && len(ctx.Comments) > 0 {
		limit := 10
		if len(ctx.Comments) < limit {
			limit = len(ctx.Comments)
		}
		comments = ctx.Comments[:limit]
	}

	var builder strings.Builder

	for _, comment := range comments {
		// Create a temporary context for this iteration
		iterCtx := &RenderContext{
			URL:            comment.URL,
			Published:      comment.Published,
			PublishedHuman: comment.PublishedHuman,
			TargetAuthor:   comment.TargetAuthor,
			Preview:        comment.Preview,

			// Copy site-level variables
			SiteURL:   ctx.SiteURL,
			SiteTitle: ctx.SiteTitle,
			Year:      ctx.Year,
		}

		// Process partials first (before variable substitution)
		processed, err := e.processPartials(content, iterCtx, depth+1)
		if err != nil {
			return "", err
		}

		// Substitute loop-specific variables
		rendered := e.substituteLoopVariables(processed, map[string]string{
			"url":             comment.URL,
			"target_author":   comment.TargetAuthor,
			"published":       comment.Published,
			"published_human": comment.PublishedHuman,
			"preview":         comment.Preview,
		})

		builder.WriteString(rendered)
	}

	return builder.String(), nil
}

// renderSiblingsSection renders the {{#siblings}} section for stream pages.
// Iterates adjacent posts displayed BELOW the focus (older-direction rail).
func (e *Engine) renderSiblingsSection(content string, ctx *RenderContext, depth int) (string, error) {
	return e.renderSiblingsList(content, ctx, depth, ctx.Siblings)
}

// renderSiblingsAboveSection renders the {{#siblings_above}} section, which
// iterates posts displayed ABOVE the focus (newer-direction rail). Shares
// the per-iteration variable substitution with renderSiblingsSection but
// drives is_above_focus → "is-above-focus" class output via the loop-var
// map so stream-post.html can apply the fade-into-topbar class.
func (e *Engine) renderSiblingsAboveSection(content string, ctx *RenderContext, depth int) (string, error) {
	return e.renderSiblingsList(content, ctx, depth, ctx.SiblingsAbove)
}

// renderSiblingsList is the shared implementation behind both siblings
// sections. The PostData slice's IsAboveFocus field drives the
// is_above_focus loop variable that stream-post.html consumes.
func (e *Engine) renderSiblingsList(content string, ctx *RenderContext, depth int, posts []PostData) (string, error) {
	var builder strings.Builder

	for _, post := range posts {
		iterCtx := &RenderContext{
			URL:            post.URL,
			Title:          post.Title,
			Published:      post.Published,
			PublishedHuman: post.PublishedHuman,
			CommentCount:   post.CommentCount,
			// TitleLinkState must thread through here too: processPartials
			// calls renderWithDepth on the loaded partial body, which
			// reaches substituteVariables(template, iterCtx) — and that
			// pass substitutes {{title_link_state}} from iterCtx.
			// Without this, the placeholder gets replaced with "" BEFORE
			// substituteLoopVariables (below) ever runs, so the loop-var
			// "title_link_state": post.TitleLinkState entry has nothing
			// left to substitute.
			TitleLinkState: post.TitleLinkState,

			SiteURL:   ctx.SiteURL,
			SiteTitle: ctx.SiteTitle,
			Year:      ctx.Year,
		}

		processed, err := e.processPartials(content, iterCtx, depth+1)
		if err != nil {
			return "", err
		}

		// is_above_focus drives the optional .is-above-focus class on
		// the rendered <article>. Empty string when false so the class
		// list reads cleanly; "is-above-focus" when true.
		aboveClass := ""
		if post.IsAboveFocus {
			aboveClass = "is-above-focus"
		}
		rendered := e.substituteLoopVariables(processed, map[string]string{
			"url":                   post.URL,
			"title":                 post.Title,
			"excerpt":               post.Excerpt,
			"body_html":             post.BodyHTML,
			"published":             post.Published,
			"published_human":       post.PublishedHuman,
			"comment_count":         fmt.Sprintf("%d", post.CommentCount),
			"comment_count_display": commentCountDisplay(post.CommentCount),
			// SG-3 (step-05/5.h): "is-empty" when count is 0, "" otherwise.
			// Stream-post.html threads this onto the comment-badge anchor;
			// stream.css hides .entry-comments-badge.is-empty so no-comment
			// posts don't render a "0" badge.
			"comment_count_class":   commentCountClass(post.CommentCount),
			"is_above_focus":        aboveClass,
			"title_link_state":      post.TitleLinkState,
		})

		builder.WriteString(rendered)
	}

	return builder.String(), nil
}

// renderFollowingSection renders the {{#following}} section for each followed author.
func (e *Engine) renderFollowingSection(content string, ctx *RenderContext, depth int) (string, error) {
	var builder strings.Builder

	for _, f := range ctx.Following {
		iterCtx := &RenderContext{
			URL:        f.URL,
			AuthorName: f.AuthorName,
			SiteTitle:  f.SiteTitle,

			SiteURL: ctx.SiteURL,
			Year:    ctx.Year,
		}

		processed, err := e.processPartials(content, iterCtx, depth+1)
		if err != nil {
			return "", err
		}

		displayName := f.AuthorName
		if displayName == "" {
			displayName = f.Domain
		}

		rendered := e.substituteLoopVariables(processed, map[string]string{
			"url":         f.URL,
			"domain":      f.Domain,
			"author_name": displayName,
			"site_title":  f.SiteTitle,
		})

		builder.WriteString(rendered)
	}

	return builder.String(), nil
}

// renderTargetsSection renders the {{#targets}} section for each tag target.
func (e *Engine) renderTargetsSection(content string, ctx *RenderContext, depth int) (string, error) {
	var builder strings.Builder

	for _, target := range ctx.Targets {
		iterCtx := &RenderContext{
			SiteURL:   ctx.SiteURL,
			SiteTitle: ctx.SiteTitle,
			Year:      ctx.Year,
		}

		processed, err := e.processPartials(content, iterCtx, depth+1)
		if err != nil {
			return "", err
		}

		rendered := e.substituteLoopVariables(processed, map[string]string{
			"uri":   target.URI,
			"added": target.Added,
		})

		builder.WriteString(rendered)
	}

	return builder.String(), nil
}

// renderTagsSection renders the {{#tags}} section for the tag index.
func (e *Engine) renderTagsSection(content string, ctx *RenderContext, depth int) (string, error) {
	var builder strings.Builder

	for _, tag := range ctx.Tags {
		iterCtx := &RenderContext{
			SiteURL:   ctx.SiteURL,
			SiteTitle: ctx.SiteTitle,
			Year:      ctx.Year,
		}

		processed, err := e.processPartials(content, iterCtx, depth+1)
		if err != nil {
			return "", err
		}

		rendered := e.substituteLoopVariables(processed, map[string]string{
			"tag_name": tag.TagName,
			"count":    fmt.Sprintf("%d", tag.Count),
			"updated":  tag.Updated,
		})

		builder.WriteString(rendered)
	}

	return builder.String(), nil
}

// escapedOpenBrace is a sentinel that replaces "{{" in user data during loop
// variable substitution. This prevents user-supplied values (e.g. a post title
// containing "{{> partial}}") from being interpreted as template syntax.
// The sentinel is restored to "{{" by the top-level Render function.
const escapedOpenBrace = "\x00\x00"

// substituteLoopVariables replaces {{variable}} with values from a map.
// This is used for loop-specific variables within section content.
// Any "{{" in substituted values is escaped to prevent template injection.
func (e *Engine) substituteLoopVariables(template string, vars map[string]string) string {
	return loopVarPattern.ReplaceAllStringFunc(template, func(match string) string {
		name := match[2 : len(match)-2]
		if val, ok := vars[name]; ok {
			return strings.ReplaceAll(val, "{{", escapedOpenBrace)
		}
		return match
	})
}
