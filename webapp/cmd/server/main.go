package main

import (
	"io/fs"
	"log"
	"os"

	"github.com/vdibart/polis-cli/webapp/internal/server"
	"github.com/vdibart/polis-cli/webapp/internal/webui"
)

// Version is set at build time with -ldflags
var Version = "dev"

func main() {
	// Default to current working directory (matches bundled binary behavior)
	dataDir := "."

	// Simple flag parsing for --data-dir / -d
	args := os.Args[1:]
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--data-dir", "-d":
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

	// Run the server
	server.Run(webFS, dataDir, server.RunOptions{CLIVersion: Version})
}
