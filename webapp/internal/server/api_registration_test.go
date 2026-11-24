package server

import (
	"net/http"
	"testing"
)

// The last site operations the old app's /api held: validating the site,
// and its registration with the discovery service, are /v1 operations.
func TestValidateAndRegistrationAreAPIOperations(t *testing.T) {
	s, h := queryRoutes(t)
	owner := mint(t, s)

	code, out := apiCall(t, h, "GET", "/v1/site/validate", owner, "")
	if code != http.StatusOK || out["status"] == nil {
		t.Errorf("validate = %d %v, want 200 with a status", code, out)
	}

	// ⛔ Registering or unregistering the site is the owner's alone: an
	// agent's scoped token is refused before anything is signed or sent.
	agent := mint(t, s, "publish", "read")
	for _, path := range []string{"/v1/site/register", "/v1/site/unregister"} {
		if code, out := apiCall(t, h, "POST", path, agent, "{}"); code != http.StatusForbidden {
			t.Errorf("%s with an agent's token = %d %v, want 403", path, code, out)
		}
	}
}
