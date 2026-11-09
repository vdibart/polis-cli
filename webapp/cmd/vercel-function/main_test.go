package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/vdibart/polis-cli/cli-go/pkg/csp"
	"github.com/vdibart/polis-cli/cli-go/pkg/provision"
)

// ⭐ The released config.json is the builder's config with the instance
// routing applied, using the page CSP the instance's server sends: exactly
// what VercelInstanceConfig makes. A release that drifted from it would serve
// static pages with the wrong headers.
func TestTheReleasedConfigIsTheInstanceRouting(t *testing.T) {
	got, err := routedConfig()
	if err != nil {
		t.Fatal(err)
	}
	want, err := provision.VercelInstanceConfig(builderConfig, provision.InstanceRouting{PageCSP: csp.TenantContent()})
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, want) {
		t.Fatal("the released config.json is not VercelInstanceConfig's output for the page CSP")
	}
	if !bytes.Contains(got, []byte(csp.TenantContent())) {
		t.Fatal("the page CSP is not in the routed config")
	}
}

func TestTheFunctionConfigIsTheBuildersAndRunsTheBootstrap(t *testing.T) {
	var cfg map[string]any
	if err := json.Unmarshal(vcConfig, &cfg); err != nil {
		t.Fatal(err)
	}
	if cfg["handler"] != "bootstrap" || cfg["runtime"] != "provided.al2023" {
		t.Fatalf(".vc-config.json = %v", cfg)
	}
	if _, ok := cfg["filePathMap"]; ok {
		t.Fatal(".vc-config.json still points at a binary outside the build")
	}
}

// ⭐ The manifest describes the tarball written beside it, digest for digest,
// and the tarball passes the same allow-list an instance's deploy applies.
func TestTheManifestVerifiesAgainstTheTarball(t *testing.T) {
	tree := t.TempDir()
	fn := filepath.Join(tree, filepath.FromSlash(functionDir))
	if err := os.MkdirAll(fn, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(fn, "bootstrap"), []byte("not a real binary"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := writeTree(tree); err != nil {
		t.Fatal(err)
	}
	archive, manifest, err := pack(tree, "1.2.3")
	if err != nil {
		t.Fatal(err)
	}
	m, err := provision.ParseFunctionManifest(manifest)
	if err != nil {
		t.Fatal(err)
	}
	if m.Version != "1.2.3" || len(m.Files) != 3 {
		t.Fatalf("manifest %+v; want version 1.2.3 and three files", m)
	}
	again, err := provision.FunctionManifestFor(provision.Code{Archive: archive, Format: provision.FormatTarGz}, "1.2.3")
	if err != nil {
		t.Fatal(err)
	}
	for i := range m.Files {
		if m.Files[i] != again.Files[i] {
			t.Fatalf("the manifest disagrees with its tarball at %s", m.Files[i].File)
		}
	}
	cfg, err := fileInArchive(archive, "config.json")
	if err != nil {
		t.Fatal(err)
	}
	want, _ := routedConfig()
	if !bytes.Equal(cfg, want) {
		t.Fatal("the tarball's config.json is not the routed config")
	}
}

func TestADevelopmentVersionIsRefused(t *testing.T) {
	if err := build("dev", t.TempDir()); err == nil {
		t.Fatal("a build for a development version was not refused")
	}
}
