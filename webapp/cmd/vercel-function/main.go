// Command vercel-function builds the remote instance's Vercel function for a
// release, without the Vercel CLI, and writes the two release assets an
// instance and `polis branch deploy` read by version:
//
//	vercel-function.tar.gz  the routed Build Output API tree: config.json and
//	                        functions/api/index.func/{.vc-config.json,bootstrap}
//	vercel-function.json    its manifest (pub.polis.vercel-function.v1)
//
//	go run ./cmd/vercel-function -version X.Y.Z -out DIR
//	go run ./cmd/vercel-function -verify-release X.Y.Z    # the release smoke check
//
// ⚠️ EXPERIMENTAL, as vercelentry is: remote instances on Vercel are
// exploratory. ⚠️ A build from this command has not been deployed to a live
// Vercel project; its layout is copied from a real `vercel build`
// (builder_config.json, vcConfig below).
//
// ⭐ The manifest is computed from the exact tarball written, in one pass, so
// the two cannot disagree: an instance refuses a build that differs from its
// manifest by a single digest.
package main

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	_ "embed"
	"encoding/json"
	"flag"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"time"

	"github.com/vdibart/polis-cli/cli-go/pkg/csp"
	"github.com/vdibart/polis-cli/cli-go/pkg/provision"
)

// builderConfig is the config.json Vercel's builder makes from vercel.json for
// this function, before the instance routing is applied. Copied from a
// `vercel build` of webapp/ (2026-10-22).
//
//go:embed builder_config.json
var builderConfig []byte

// vcConfig is the function's .vc-config.json, byte for byte as that build
// wrote it once the binary was moved into the function (no filePathMap).
//
//go:embed vc_config.json
var vcConfig []byte

const functionDir = "functions/api/index.func"

func main() {
	version := flag.String("version", "", "the release version (X.Y.Z) to build and stamp")
	out := flag.String("out", "", "directory to write the two assets into")
	verify := flag.String("verify-release", "", "check that release X.Y.Z publishes both assets and that they agree")
	flag.Parse()
	var err error
	switch {
	case *verify != "":
		err = verifyRelease(*verify)
	case *version != "" && *out != "":
		err = build(*version, *out)
	default:
		err = fmt.Errorf("give -version and -out, or -verify-release")
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "vercel-function:", err)
		os.Exit(1)
	}
}

func build(version, out string) error {
	if !provision.IsReleasedVersion(version) {
		return fmt.Errorf("%q is not a release version", version)
	}
	tree, err := os.MkdirTemp("", "vercel-function-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(tree)
	fn := filepath.Join(tree, filepath.FromSlash(functionDir))
	if err := os.MkdirAll(fn, 0o755); err != nil {
		return err
	}
	cmd := exec.Command("go", "build", "-trimpath",
		"-ldflags", "-s -w -X github.com/vdibart/polis-cli/webapp/vercelentry.Version="+version,
		"-o", filepath.Join(fn, "bootstrap"), "./cmd/vercel-bootstrap")
	cmd.Env = append(os.Environ(), "GOOS=linux", "GOARCH=amd64", "CGO_ENABLED=0")
	cmd.Stdout, cmd.Stderr = os.Stderr, os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("building the function: %w", err)
	}
	if err := writeTree(tree); err != nil {
		return err
	}
	archive, manifest, err := pack(tree, version)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(out, 0o755); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(out, provision.FunctionBuildAsset), archive, 0o644); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(out, provision.FunctionManifestAsset), manifest, 0o644); err != nil {
		return err
	}
	fmt.Fprintf(os.Stderr, "[i] wrote %s and %s for %s\n", provision.FunctionBuildAsset, provision.FunctionManifestAsset, version)
	return nil
}

// writeTree writes everything but the binary: the routed config.json and the
// function's .vc-config.json.
func writeTree(tree string) error {
	routed, err := routedConfig()
	if err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(tree, "config.json"), routed, 0o644); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(tree, filepath.FromSlash(functionDir), ".vc-config.json"), vcConfig, 0o644)
}

// routedConfig is the builder's config.json with the instance routing applied,
// using the page CSP the instance's own server sends.
func routedConfig() ([]byte, error) {
	return provision.VercelInstanceConfig(builderConfig, provision.InstanceRouting{PageCSP: csp.TenantContent()})
}

// pack archives the tree the way provision reads a build, then computes the
// manifest from those exact bytes.
func pack(tree, version string) (archive, manifest []byte, err error) {
	code, err := provision.ArchiveFunctionBuild(tree)
	if err != nil {
		return nil, nil, err
	}
	m, err := provision.FunctionManifestFor(code, version)
	if err != nil {
		return nil, nil, err
	}
	mj, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return nil, nil, err
	}
	return code.Archive, append(mj, '\n'), nil
}

// verifyRelease fetches both assets of a published release and checks them the
// way an instance does before it deploys: ReleasedFunctionCode refuses a build
// that differs from its manifest by one digest. It also checks the build's
// config.json carries the instance routing.
func verifyRelease(version string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	code, err := provision.ReleasedFunctionCode(ctx, &http.Client{Timeout: time.Minute}, version)
	if err != nil {
		return err
	}
	cfg, err := fileInArchive(code.Archive, "config.json")
	if err != nil {
		return err
	}
	want, err := routedConfig()
	if err != nil {
		return err
	}
	if !bytes.Equal(cfg, want) {
		return fmt.Errorf("the release's config.json is not the routed config this build generates")
	}
	fmt.Printf("[ok] release %s publishes a function build that matches its manifest\n", version)
	return nil
}

func fileInArchive(archive []byte, name string) ([]byte, error) {
	gz, err := gzip.NewReader(bytes.NewReader(archive))
	if err != nil {
		return nil, err
	}
	tr := tar.NewReader(gz)
	for {
		h, err := tr.Next()
		if err != nil {
			return nil, fmt.Errorf("%s is not in the build", name)
		}
		if h.Name == name {
			var b bytes.Buffer
			if _, err := b.ReadFrom(tr); err != nil {
				return nil, err
			}
			return b.Bytes(), nil
		}
	}
}
