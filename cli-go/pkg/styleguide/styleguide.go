// Package styleguide carries the v5 style guide, the rules every v5 page
// follows, so the binaries can give it to agents: over MCP (the
// polis://style-guide resource) and over HTTP at Path.
//
// ⛔ ONE SOURCE. The guide is written in docs/webapp/designer/style-guide.md.
// style-guide.txt here is a copy only because a Go module cannot embed a file
// outside itself (and .txt, so the docs tests do not read it as a page whose
// links are relative to this directory); TestTheEmbeddedGuideIsThePublishedPage
// fails the build when the two differ (refresh with POLIS_UPDATE_STYLE_GUIDE=1).
package styleguide

import (
	_ "embed"
	"net/url"
	"regexp"
)

//go:embed style-guide.txt
var text string

// PageURL is where the guide is published; its relative links are relative to it.
const PageURL = "https://github.com/vdibart/polis-cli/blob/main/docs/webapp/designer/style-guide.md"

var relativeLink = regexp.MustCompile(`\]\(([^)#:][^):]*)\)`)

// Text is the guide, as markdown, its relative links made absolute against
// PageURL so they work wherever an agent reads it (MCP, HTTP).
func Text() string {
	base, _ := url.Parse(PageURL)
	return relativeLink.ReplaceAllStringFunc(text, func(m string) string {
		ref := relativeLink.FindStringSubmatch(m)[1]
		u, err := url.Parse(ref)
		if err != nil {
			return m
		}
		return "](" + base.ResolveReference(u).String() + ")"
	})
}

// URI is the guide as an MCP resource.
const URI = "polis://style-guide"

// Path is where an instance serves it over HTTP: beside polis.pub's agent
// files (/llms.txt, /for/agents), and at the same path on any instance.
const Path = "/for/agents/style-guide"

// MIMEType is how it is served.
const MIMEType = "text/markdown; charset=utf-8"
