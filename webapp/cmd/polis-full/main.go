// polis-full is the bundled Polis binary with both CLI commands and the serve command.
package main

import (
	"fmt"
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

func printServeUsage() {
	fmt.Print(`Polis serve - run the local web app

Usage:
  polis serve [options]

Options:
  -d, --data-dir <path>   Site directory to serve (default: current directory)
  -p, --port <n>          Port to bind on localhost (default: auto-pick)
      --dev               Developer mode: enable the localhost messaging UI
                          (compose/send). DMs are read-only on localhost by
                          default — a served export is offline and DMs are a
                          networked feature. With --dev the send/compose UI is
                          shown for development + testing; delivery to a real
                          peer still requires a reachable network.
      --mirror            Serve a read-only clone (data tenancy); default --owner
      --reader            Reader surface (default --editor for --owner)
  -h, --help              Show this help

Read an exported archive offline:
  unzip your .polis export, run 'polis serve' inside it, open the localhost URL,
  and enter your password to read your messages. See 'polis dm decrypt --help'
  for the CLI extract path.
`)
}

func runServer(args []string, cliVersion string) {
	dataDir := "."
	port := 0 // 0 = auto-pick

	// Two-axis mode flags (step 5.a) — see cmd/server/main.go for the
	// full description; same parsing rules apply.
	dataMode := ""
	surface := ""
	devMode := false

	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--help", "-h":
			printServeUsage()
			return
		case "--dev":
			// Developer mode: enable the localhost messaging UI (read-only by
			// default). See docs/cli/user/command-reference.md (polis serve).
			devMode = true
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
		DevMode:    devMode,
	})
}
