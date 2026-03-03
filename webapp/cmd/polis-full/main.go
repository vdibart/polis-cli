// polis-full is the bundled Polis binary with both CLI commands and the serve command.
package main

import (
	"io/fs"
	"log"
	"os"

	"github.com/vdibart/polis-cli/cli-go/pkg/cmd"
	"github.com/vdibart/polis-cli/webapp/internal/server"
	"github.com/vdibart/polis-cli/webapp/internal/webui"
)

// Version is set at build time with -ldflags
var Version = "dev"

func main() {
	// Set version for CLI commands
	cmd.Version = Version

	// Check if first argument is "serve" to start the server
	if len(os.Args) > 1 && os.Args[1] == "serve" {
		runServer(os.Args[2:], Version)
	} else {
		// Dispatch to CLI command handler
		cmd.Execute(os.Args[1:])
	}
}

func runServer(args []string, cliVersion string) {
	// Parse serve-specific flags
	dataDir := "."

	// Simple flag parsing for serve command
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--data-dir":
			if i+1 < len(args) {
				dataDir = args[i+1]
				i++
			}
		case "-d":
			if i+1 < len(args) {
				dataDir = args[i+1]
				i++
			}
		}
	}

	// Get the embedded web UI filesystem
	webFS, err := fs.Sub(webui.Assets, "www")
	if err != nil {
		log.Fatal("Failed to create sub filesystem:", err)
	}

	// Run the server with CLI version for metadata
	server.Run(webFS, dataDir, server.RunOptions{CLIVersion: cliVersion})
}
