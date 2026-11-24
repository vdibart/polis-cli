package server

import (
	"encoding/json"
	"strings"
	"testing"
)

// TestTheThemeSwitchRefusesAReservedTheme: sols (polis.pub's logged-out page)
// and stardust (why.polis.pub's) are never a site's choice through the API,
// even when installed. The theme-assignment census
// (cli-go/cmd/polis/theme_assign_census_test.go) points here.
func TestTheThemeSwitchRefusesAReservedTheme(t *testing.T) {
	s, h := queryRoutes(t)
	setupTestTheme(t, s, "sols")
	setupTestTheme(t, s, "stardust")
	owner := mint(t, s)
	for _, name := range []string{"sols", "stardust", "pub.polis.themes.sols"} {
		body, _ := json.Marshal(map[string]string{"theme": name})
		code, out := apiCall(t, h, "POST", "/v1/site/theme", owner, string(body))
		if code != 400 || !strings.Contains(strings.ToLower(jsonText(out)), "reserved") {
			t.Errorf("switching to %s = %d %v, want a 400 saying it is reserved", name, code, out)
		}
	}
}

func jsonText(v any) string { b, _ := json.Marshal(v); return string(b) }
