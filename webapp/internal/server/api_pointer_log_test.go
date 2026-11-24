package server

import (
	"testing"

	"github.com/vdibart/polis-cli/cli-go/pkg/mode"
)

// ⭐ The operator's monitor for a missing pointer reads one event: a site
// that should carry the pointer and does not logs pub.polis.site.api_pointer
// with published false, whether it was withdrawn or could not be written; a
// site that publishes it logs published true, once.
func TestAMissingAPIPointerIsLogged(t *testing.T) {
	s := newConfiguredServer(t)
	s.Logger = NewStdoutLogger(LogLevelBasic)
	s.Mode = mode.Custodied
	s.APIPointerEnabled = true

	published := eventsNamed(captureEvents(t, func() { s.EnsureAPIPointer(); s.EnsureAPIPointer() }), "pub.polis.site.api_pointer")
	if len(published) != 1 || published[0]["published"] != true || published[0]["failed"] != nil {
		t.Fatalf("publishing logged %v, want one published:true", published)
	}

	s.APIPointerEnabled = false
	withdrawn := eventsNamed(captureEvents(t, s.EnsureAPIPointer), "pub.polis.site.api_pointer")
	if len(withdrawn) != 1 || withdrawn[0]["published"] != false {
		t.Fatalf("withdrawing logged %v, want one published:false", withdrawn)
	}

	s.APIPointerEnabled = true
	s.BaseURL = ""
	failed := eventsNamed(captureEvents(t, s.EnsureAPIPointer), "pub.polis.site.api_pointer")
	if len(failed) != 1 || failed[0]["published"] != false || failed[0]["failed"] != true {
		t.Fatalf("a site with no base URL logged %v, want published:false failed:true", failed)
	}

	s.Mode = mode.Local // no public API: no pointer is the right state, and nothing is logged
	s.BaseURL = "http://localhost:8080"
	if got := eventsNamed(captureEvents(t, s.EnsureAPIPointer), "pub.polis.site.api_pointer"); len(got) != 0 {
		t.Fatalf("a local site logged %v", got)
	}
}
