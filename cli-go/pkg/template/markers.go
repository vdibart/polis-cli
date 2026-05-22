package template

import (
	"fmt"
	"strings"
)

// WrapWithMarkers wraps content with HTML comment markers for snippet editing.
// These markers allow the webapp to identify snippet boundaries in rendered HTML.
//
// Format:
//
//	<!-- POLIS-SNIPPET-START: {source}:{path} path={path} -->
//	<span class="polis-snippet-boundary" data-snippet="{source}:{path}" data-path="{path}" data-source="{source}" hidden></span>
//	{content}
//	<!-- POLIS-SNIPPET-END: {source}:{path} -->
func WrapWithMarkers(content, path, source string) string {
	// Clean the path (remove leading/trailing whitespace)
	path = strings.TrimSpace(path)

	// Build the full identifier
	identifier := fmt.Sprintf("%s:%s", source, path)

	// Build the markers
	startMarker := fmt.Sprintf("<!-- POLIS-SNIPPET-START: %s path=%s -->", identifier, path)
	boundarySpan := fmt.Sprintf(`<span class="polis-snippet-boundary" data-snippet="%s" data-path="%s" data-source="%s" hidden></span>`,
		identifier, path, source)
	endMarker := fmt.Sprintf("<!-- POLIS-SNIPPET-END: %s -->", identifier)

	// Wrap the content
	return fmt.Sprintf("%s\n%s\n%s\n%s", startMarker, boundarySpan, content, endMarker)
}

