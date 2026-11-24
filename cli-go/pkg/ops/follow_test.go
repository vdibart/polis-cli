package ops

import (
	"context"
	"errors"
	"testing"

	"github.com/vdibart/polis-cli/cli-go/pkg/storage"
)

// The shared follow's checks are every caller's (the API, the app, the
// widget, the CLI), and come before anything is asked of the network.
func TestTheSharedFollowsChecksComeFirst(t *testing.T) {
	c := FollowSite{Site: storage.At(t.TempDir()), PrivateKey: []byte("k"), BaseURL: "https://maya.example"}
	for name, url := range map[string]string{
		"not https":   "http://alice.example",
		"bare scheme": "https://",
		"own site":    "https://MAYA.example",
	} {
		if _, err := Follow(context.Background(), c, url); !errors.Is(err, ErrFollowInvalid) {
			t.Errorf("follow %s: err = %v, want ErrFollowInvalid", name, err)
		}
	}
	if _, err := Unfollow(context.Background(), c, "http://alice.example"); !errors.Is(err, ErrFollowInvalid) {
		t.Errorf("unfollow over http: err = %v", err)
	}
	c.PrivateKey = nil
	if _, err := Follow(context.Background(), c, "https://alice.example"); !errors.Is(err, ErrFollowInvalid) || err.Error() != "Not configured: no private key" {
		t.Errorf("no key: err = %v", err)
	}
}
