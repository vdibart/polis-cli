package server

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// ⛔ THE INVARIANT (epic 11 Done when): no operator actor writes a marker, and no
// Rosie act lacks one. Enforced over the source, because the property is about
// every writer that exists and every one added later.
func TestNoOperatorWritesAMarkerAndNoRosieActLacksOne(t *testing.T) {
	roots := []string{filepath.Join("..", "..", "..", "cli-go"), filepath.Join("..", "..")}

	// Only the marked signers may construct a marker, and only Rosie may call
	// them.
	markerSetters := regexp.MustCompile(`\b(Agent|Grant):\s+\S`)
	markedCall := regexp.MustCompile(`\b(GrantAsAgent|DenyAsAgent|UpdateRelationshipMarked)\(`)
	unmarkedSigners := regexp.MustCompile(`\bblessing\.(Grant|GrantByVersion|Deny|DenyRequest)\(`)

	allowedMarkerFiles := map[string]bool{
		"cli-go/pkg/blessing/grant.go": true, // copies the marker it was handed onto the entry
	}
	allowedMarkedCallers := map[string]bool{
		"cli-go/pkg/blessing/agent.go":    true,
		"cli-go/pkg/blessing/grant.go":    true, // the shared helper; unmarked callers pass the zero marker
		"cli-go/pkg/discovery/client.go":  true,
		"webapp/internal/server/rosie.go": true,
	}

	for _, root := range roots {
		_ = filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
			if err != nil || info.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
				return nil
			}
			rel := normalise(path)
			data, rerr := os.ReadFile(path)
			if rerr != nil {
				return nil
			}
			src := string(data)
			for i, line := range strings.Split(src, "\n") {
				trimmed := strings.TrimSpace(line)
				if strings.HasPrefix(trimmed, "//") {
					continue
				}
				if markedCall.MatchString(line) && !strings.HasPrefix(trimmed, "func ") && !allowedMarkedCallers[rel] {
					t.Errorf("%s:%d calls a marked signer outside Rosie: %s", rel, i+1, trimmed)
				}
				if strings.Contains(src, "BlessedComment{") && markerSetters.MatchString(line) &&
					strings.Contains(line, "marker.") == false && !allowedMarkerFiles[rel] &&
					(strings.HasPrefix(trimmed, "Agent:") || strings.HasPrefix(trimmed, "Grant:")) {
					t.Errorf("%s:%d sets a blessed-entry marker outside the marked signer: %s", rel, i+1, trimmed)
				}
				if rel == "webapp/internal/server/rosie.go" && unmarkedSigners.MatchString(line) {
					t.Errorf("%s:%d — Rosie calls an UNMARKED signer: %s", rel, i+1, trimmed)
				}
				if rel == "webapp/internal/server/sync.go" && unmarkedSigners.MatchString(line) {
					t.Errorf("%s:%d — the sync auto-decides through an UNMARKED signer: %s", rel, i+1, trimmed)
				}
			}
			return nil
		})
	}
}

func normalise(path string) string {
	p := filepath.ToSlash(path)
	for _, marker := range []string{"cli-go/", "webapp/"} {
		if i := strings.LastIndex(p, marker); i >= 0 {
			return p[i:]
		}
	}
	// Paths under the webapp root arrive as ../../internal/...
	return "webapp/" + strings.TrimLeft(strings.TrimPrefix(p, "../../"), "/")
}
