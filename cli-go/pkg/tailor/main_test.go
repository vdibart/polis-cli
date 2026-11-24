package tailor

import (
	"os"
	"testing"
)

// No test reaches the network: cli-update's release lookup is switched off
// unless a test points it at a local server.
func TestMain(m *testing.M) {
	latestReleaseURL = ""
	os.Exit(m.Run())
}
