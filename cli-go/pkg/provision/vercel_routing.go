package provision

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
)

// vercelRoutesTemplate is the header routing of a remote instance on Vercel.
//
// ⭐⭐ PUBLISH = DEPLOY. The site's public tree is served as the deployment's
// own static files, and every path that is not one falls through to the
// function. So the routing carries HEADERS only, never a rewrite:
//
//   - "headers" apply to every response, static or function (the .well-known
//     CORS and cache rules);
//   - "static" apply only when a static file answered (the router's hit
//     phase): the content types Vercel cannot infer (an extensionless
//     .well-known/polis, .jsonl) and the page CSP, on "/", every *.html and
//     every path ending "/" (a directory's index.html answers it, and its
//     request path has no .html). ⚠️ Never to the function:
//     the owner's app sets its own nonce-bearing CSP, and a second CSP header
//     would be enforced alongside it and block the app's scripts.
//
// ⛔ No source names a private path (cmd/polis/vercel_routes_test.go), and
// privacy does not rest on the routing anyway: a private name is never
// uploaded as a static file (staticFiles).
//
//go:embed vercel_routes.json
var vercelRoutesTemplate []byte

// VercelRoutesTemplate returns the routes template, for tests and tooling.
func VercelRoutesTemplate() []byte { return append([]byte(nil), vercelRoutesTemplate...) }

// pageCSPPlaceholder is the template's stand-in for the page CSP, which the
// instance supplies (it is the webapp's, and this package cannot import it).
const pageCSPPlaceholder = "{{page-csp}}"

type routesTemplate struct {
	Headers []headerRule `json:"headers"`
	Static  []headerRule `json:"static"`
}

type headerRule struct {
	Source  string `json:"source"`
	Headers []struct {
		Key   string `json:"key"`
		Value string `json:"value"`
	} `json:"headers"`
}

// InstanceRouting is what an instance's routing needs from the instance.
type InstanceRouting struct {
	// PageCSP is the Content-Security-Policy every static page is served
	// with: the same policy the instance's own server sends for site pages.
	PageCSP string
}

// VercelInstanceConfig returns a build's Build Output API config.json with the
// instance's header routing in it: the template's "headers" ahead of every
// route, and its "static" rules in the hit phase, which runs only when a
// static file answered. The build's own routes (its filesystem phase, its
// catch-all to the function, its error phase) are kept as they are.
//
// ⭐ THIS IS WHAT vercel.json BECOMES IN A PREBUILT DEPLOYMENT: `vercel build`
// compiles vercel.json into routes, and a prebuilt deployment reads only the
// routes. One build of the function serves every instance.
//
// ⛔ A build whose routes have no filesystem phase is refused rather than
// guessed at: without one, the static files would never be served.
func VercelInstanceConfig(config []byte, r InstanceRouting) ([]byte, error) {
	csp := strings.TrimSpace(r.PageCSP)
	if csp == "" {
		return nil, fmt.Errorf("provision: the instance routing needs the page CSP")
	}
	if strings.ContainsAny(csp, "\r\n\x00") || strings.Contains(csp, pageCSPPlaceholder) {
		return nil, fmt.Errorf("provision: the page CSP is not a single header value")
	}
	headers, static, err := instanceRoutes(csp)
	if err != nil {
		return nil, err
	}
	var cfg map[string]any
	if err := json.Unmarshal(config, &cfg); err != nil {
		return nil, fmt.Errorf("provision: the build's config.json does not parse")
	}
	routes, _ := cfg["routes"].([]any)
	fs, hit, errPhase := -1, -1, -1
	for i, rt := range routes {
		m, ok := rt.(map[string]any)
		if !ok {
			continue
		}
		switch m["handle"] {
		case "filesystem":
			if fs < 0 {
				fs = i
			}
		case "hit":
			if hit < 0 {
				hit = i
			}
		case "error":
			if errPhase < 0 {
				errPhase = i
			}
		}
	}
	if fs < 0 {
		return nil, fmt.Errorf("provision: the build's config.json has no filesystem phase, so no static file would be served")
	}
	out := make([]any, 0, len(routes)+len(headers)+len(static)+1)
	out = append(out, headers...)
	switch {
	case hit >= 0:
		// The build has a hit phase already: ours go first in it.
		out = append(out, routes[:hit+1]...)
		out = append(out, static...)
		out = append(out, routes[hit+1:]...)
	case errPhase >= 0:
		// ⭐ A hit phase of our own, before the error phase, where builders
		// put theirs.
		out = append(out, routes[:errPhase]...)
		out = append(out, map[string]any{"handle": "hit"})
		out = append(out, static...)
		out = append(out, routes[errPhase:]...)
	default:
		out = append(out, routes...)
		out = append(out, map[string]any{"handle": "hit"})
		out = append(out, static...)
	}
	cfg["routes"] = out
	return json.MarshalIndent(cfg, "", "  ")
}

// instanceRoutes turns the template into Build Output API header routes, with
// the page CSP in place of its placeholder.
func instanceRoutes(csp string) (headers, static []any, err error) {
	var t routesTemplate
	if err := json.Unmarshal(vercelRoutesTemplate, &t); err != nil {
		return nil, nil, fmt.Errorf("provision: the routes template does not parse: %w", err)
	}
	conv := func(rules []headerRule) ([]any, error) {
		var out []any
		for _, h := range rules {
			src, err := sourceRegexp(h.Source)
			if err != nil {
				return nil, err
			}
			kv := map[string]string{}
			for _, e := range h.Headers {
				kv[e.Key] = strings.ReplaceAll(e.Value, pageCSPPlaceholder, csp)
			}
			out = append(out, map[string]any{"src": src, "headers": kv, "continue": true})
		}
		return out, nil
	}
	if headers, err = conv(t.Headers); err != nil {
		return nil, nil, err
	}
	if static, err = conv(t.Static); err != nil {
		return nil, nil, err
	}
	return headers, static, nil
}

// sourceRegexp converts the template's source forms to the anchored
// expression a route matches with: "/", a literal path, "/dir/:path*",
// "/:path*" (every path), "*.ext" (every path ending in that extension) and
// "*/" (every path ending in a slash: a directory's index).
// ⛔ Any other form is refused, so a template edit cannot match more than it
// appears to.
var (
	literalPath = regexp.MustCompile(`^/[A-Za-z0-9._/-]*$`)
	extension   = regexp.MustCompile(`^\*\.[a-z0-9]+$`)
)

func sourceRegexp(source string) (string, error) {
	switch {
	case source == "/":
		return "^/$", nil
	case source == "/:path*":
		return "^/.*$", nil
	case source == "*/":
		return "^/.*/$", nil
	case extension.MatchString(source):
		return `^/.*\.` + strings.TrimPrefix(source, "*.") + "$", nil
	case strings.HasSuffix(source, "/:path*"):
		dir := strings.TrimSuffix(source, "/:path*")
		if !literalPath.MatchString(dir) || dir == "" {
			break
		}
		return "^" + regexp.QuoteMeta(dir) + "(?:/(.*))?$", nil
	case literalPath.MatchString(source):
		return "^" + regexp.QuoteMeta(source) + "$", nil
	}
	return "", fmt.Errorf("provision: the routes template has a source this generator does not convert: %s", source)
}
