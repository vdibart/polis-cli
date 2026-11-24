package server

import (
	"net/http"
	"testing"
)

func TestCustomCSSRouteIsOwnerOnly(t *testing.T) {
	s, _ := passkeyServer(t)
	if code, _ := gateRefusal(t, s, "/api/settings/custom-css"); code == http.StatusOK {
		t.Error("/api/settings/custom-css passed the owner gate without a session")
	}
}
