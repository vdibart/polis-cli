package server

import (
	"net/http"
	"testing"
)

func TestTheShortcutRouteIsOwnerOnly(t *testing.T) {
	s, _ := passkeyServer(t)
	for _, p := range []string{"/api/nav-shortcuts"} {
		if code, _ := gateRefusal(t, s, p); code == http.StatusOK {
			t.Errorf("%s passed the owner gate without a session", p)
		}
	}
}
