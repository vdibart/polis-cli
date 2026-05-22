package main

import (
	"io/fs"
	"log"
	"os"
	"strconv"

	"github.com/vdibart/polis-cli/webapp/internal/server"
	"github.com/vdibart/polis-cli/webapp/internal/webui"
)

// Version is set at build time with -ldflags
var Version = "dev"

func main() {
	// Default to current working directory (matches bundled binary behavior)
	dataDir := "."
	port := 0 // 0 = auto-pick an available port

	// Two-axis mode flags (step 5.a):
	//   --owner / --mirror — data tenancy (default --owner)
	//   --editor / --reader — surface (default --editor for --owner;
	//                                  default --reader for --mirror)
	// Invalid combo: --mirror --editor → server.Run rejects at startup.
	dataMode := ""
	surface := ""

	args := os.Args[1:]
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

	// Run the server
	server.Run(webFS, dataDir, server.RunOptions{
		CLIVersion: Version,
		Port:       port,
		DataMode:   dataMode,
		Surface:    surface,
	})
}
