package vercelentry

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// ⛔⛔ THE STATIC OUTPUT DIRECTORY MUST BE EMPTY. Vercel serves static files
// BEFORE any rewrite, and with the default output directory (".") a build
// copied the whole webapp tree into them: go.mod, the agent notes and the server's
// source would all have been public. The committed directory holds only a
// .gitkeep, and every path reaches the function.
func TestTheVercelBuildPublishesNoStaticFiles(t *testing.T) {
	raw, err := os.ReadFile("../vercel.json")
	if err != nil {
		t.Fatal(err)
	}
	var cfg struct {
		Framework       *string `json:"framework"`
		BuildCommand    *string `json:"buildCommand"`
		OutputDirectory string  `json:"outputDirectory"`
		Rewrites        []struct {
			Source      string `json:"source"`
			Destination string `json:"destination"`
		} `json:"rewrites"`
	}
	if err := json.Unmarshal(raw, &cfg); err != nil {
		t.Fatal(err)
	}
	out := filepath.Clean(cfg.OutputDirectory)
	if cfg.OutputDirectory == "" || out == "." || out == ".." || filepath.IsAbs(out) {
		t.Fatalf("outputDirectory %q: it must name an empty directory inside webapp/, never the webapp tree", cfg.OutputDirectory)
	}
	entries, err := os.ReadDir(filepath.Join("..", out))
	if err != nil {
		t.Fatalf("outputDirectory %q: %v", cfg.OutputDirectory, err)
	}
	for _, e := range entries {
		if e.Name() != ".gitkeep" {
			t.Errorf("outputDirectory %q holds %q, which Vercel would serve before the function", cfg.OutputDirectory, e.Name())
		}
	}
	if cfg.Framework != nil {
		t.Errorf("framework %q: a framework preset brings its own output directory and build", *cfg.Framework)
	}
	if cfg.BuildCommand == nil || *cfg.BuildCommand != "" {
		t.Error("buildCommand must be the empty string: the deploy builds the function locally")
	}
	if len(cfg.Rewrites) != 1 || cfg.Rewrites[0].Source != "/(.*)" || cfg.Rewrites[0].Destination != "/api/index" {
		t.Errorf("rewrites %+v: want every path to the function, /(.*) → /api/index", cfg.Rewrites)
	}
}
