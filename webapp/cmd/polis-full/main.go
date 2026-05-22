// polis-full is the bundled Polis binary with both CLI commands and the serve command.
package main

import (
	"io/fs"
	"log"
	"os"
	"strconv"

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
	dataDir := "."
	port := 0 // 0 = auto-pick

	// Two-axis mode flags (step 5.a) — see cmd/server/main.go for the
	// full description; same parsing rules apply.
	dataMode := ""
	surface := ""

	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--data-dir", "-d":
			if i+1 < len(args) {
				dataDir = args[i+1]
				i++
			}
		case "--port", "-p":
			if i+1 < len(args) {
				p, err := strconv.Atoi(args[i+1])
				if err != nil || p <= 0 || p > 65535 {
					log.Fatalf("invalid --port %q", args[i+1])
				}
				port = p
				i++
			}
		case "--owner":
			if dataMode != "" {
				log.Fatalf("--owner conflicts with prior --%s", dataMode)
			}
			dataMode = server.DataModeOwner
		case "--mirror":
			if dataMode != "" {
				log.Fatalf("--mirror conflicts with prior --%s", dataMode)
			}
			dataMode = server.DataModeMirror
		case "--editor":
			if surface != "" {
				log.Fatalf("--editor conflicts with prior --%s", surface)
			}
			surface = server.SurfaceEditor
		case "--reader":
			if surface != "" {
				log.Fatalf("--reader conflicts with prior --%s", surface)
			}
			surface = server.SurfaceReader
		}
	}

	// Get the embedded web UI filesystem
	webFS, err := fs.Sub(webui.Assets, "www")
	if err != nil {
		log.Fatal("Failed to create sub filesystem:", err)
	}

	// Run the server with CLI version for metadata
	server.Run(webFS, dataDir, server.RunOptions{
		CLIVersion: cliVersion,
		Port:       port,
		DataMode:   dataMode,
		Surface:    surface,
	})
}
