package seen

import (
	"testing"
	"time"

	"github.com/vdibart/polis-cli/cli-go/pkg/storage"
)

func TestMarkMovesForwardOnlyAndPerSection(t *testing.T) {
	site := storage.At(t.TempDir())
	now := time.Date(2026, 11, 14, 12, 0, 0, 0, time.UTC)

	if got, _ := Get(site); len(got) != 0 {
		t.Fatalf("a new site has seen nothing: %v", got)
	}
	t1 := now.Add(-2 * time.Hour)
	if got, err := Mark(site, "gather", t1, now); err != nil || !got.Equal(t1) {
		t.Fatalf("mark gather: %v %v", got, err)
	}
	// A stale tab leaving later with an earlier arrival does not move it back.
	if got, _ := Mark(site, "gather", t1.Add(-time.Hour), now); !got.Equal(t1) {
		t.Errorf("moved backwards to %v", got)
	}
	// A time in the future is taken as now.
	if got, _ := Mark(site, "gather", now.Add(time.Hour), now); !got.Equal(now) {
		t.Errorf("future time stored as %v, want now", got)
	}
	// Other sections are untouched.
	all, _ := Get(site)
	if _, ok := all["write"]; ok || !all["gather"].Equal(now) {
		t.Errorf("sections = %v", all)
	}
	if _, err := Mark(site, "settings", now, now); err == nil {
		t.Error("an unknown section was accepted")
	}
	if _, err := Mark(site, "write", time.Time{}, now); err == nil {
		t.Error("a missing time was accepted")
	}
}

// Every section of the owner's app keeps a seen time, enhance included, so
// the app can open where the owner was last.
func TestEverySectionKeepsASeenTime(t *testing.T) {
	site := storage.At(t.TempDir())
	now := time.Now().UTC()
	for _, s := range []string{"gather", "write", "message", "enhance"} {
		if _, err := Mark(site, s, now, now); err != nil {
			t.Errorf("Mark(%s): %v", s, err)
		}
	}
}
