package cmd

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/vdibart/polis-cli/cli-go/pkg/version"
)

func handleExtract(args []string) {
	if len(args) < 2 {
		exitError("Usage: polis extract <file> <hash>")
	}

	file := args[0]
	targetHash := args[1]
	dir := getDataDir()

	// Validate file exists
	fullPath := file
	if !filepath.IsAbs(file) {
		fullPath = filepath.Join(dir, file)
	}

	// NOT the storage interface: `polis extract` accepts an absolute path, so
	// fullPath may be outside the site entirely. The store refuses absolute
	// keys by design, and routing this through it would break the documented
	// behaviour rather than preserve it.
	if _, err := os.Stat(fullPath); errors.Is(err, fs.ErrNotExist) {
		exitError("File not found: %s", file)
	}

	// Ensure hash has sha256: prefix
	if !strings.HasPrefix(targetHash, "sha256:") {
		targetHash = "sha256:" + targetHash
	}

	// Reconstruct the version
	content, err := version.ReconstructVersion(fullPath, targetHash, ".versions")
	if err != nil {
		exitError("Failed to reconstruct version: %v", err)
	}

	if jsonOutput {
		outputJSON(map[string]interface{}{
			"status":  "success",
			"command": "extract",
			"data": map[string]interface{}{
				"file":    file,
				"version": targetHash,
				"content": content,
			},
		})
	} else {
		// Output the content directly to stdout
		fmt.Print(content)
	}
}
