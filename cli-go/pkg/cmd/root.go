// Package cmd provides the CLI command handlers for the polis CLI.
package cmd

import (
	"bufio"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/vdibart/polis-cli/cli-go/pkg/comment"
	"github.com/vdibart/polis-cli/cli-go/pkg/discovery"
	"github.com/vdibart/polis-cli/cli-go/pkg/dm"
	"github.com/vdibart/polis-cli/cli-go/pkg/feed"
	"github.com/vdibart/polis-cli/cli-go/pkg/following"
	"github.com/vdibart/polis-cli/cli-go/pkg/index"
	"github.com/vdibart/polis-cli/cli-go/pkg/metadata"
	"github.com/vdibart/polis-cli/cli-go/pkg/notification"
	"github.com/vdibart/polis-cli/cli-go/pkg/publish"
	"github.com/vdibart/polis-cli/cli-go/pkg/site"
	"github.com/vdibart/polis-cli/cli-go/pkg/stream"
	"github.com/vdibart/polis-cli/cli-go/pkg/tag"
	"github.com/vdibart/polis-cli/cli-go/pkg/theme"
)

// Version is set at build time with -ldflags
var Version = "dev"

// Global flags and config
var (
	dataDir      string
	jsonOutput   bool
	discoveryURL string
	discoveryKey string
	baseURL      string
	requestID    string   // UUIDv4 for cross-boundary tracing
	cliLogFile   *os.File // optional log file (POLIS_LOG_FILE env var)
)

// DefaultDiscoveryServiceURL is the default discovery service URL.
const DefaultDiscoveryServiceURL = "https://ds.polis.pub"

// generateRequestID creates a UUIDv4 using crypto/rand.
func generateRequestID() string {
	var uuid [16]byte
	_, _ = rand.Read(uuid[:])
	uuid[6] = (uuid[6] & 0x0f) | 0x40 // version 4
	uuid[8] = (uuid[8] & 0x3f) | 0x80 // variant 10
	return fmt.Sprintf("%08x-%04x-%04x-%04x-%012x",
		uuid[0:4], uuid[4:6], uuid[6:8], uuid[8:10], uuid[10:16])
}

// initCLILogger opens the log file if POLIS_LOG_FILE is set.
func initCLILogger() {
	logPath := os.Getenv("POLIS_LOG_FILE")
	if logPath == "" {
		return
	}
	f, err := os.OpenFile(logPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0600)
	if err != nil {
		fmt.Fprintf(os.Stderr, "[!] Failed to open log file %s: %v\n", logPath, err)
		return
	}
	cliLogFile = f
}

// logCLIAction writes a structured JSON log line to the CLI log file.
func logCLIAction(action string, fields map[string]interface{}) {
	if cliLogFile == nil {
		return
	}
	obj := map[string]interface{}{
		"ts":         time.Now().UTC().Format(time.RFC3339),
		"source":     "cli",
		"action":     action,
		"request_id": requestID,
	}
	for k, v := range fields {
		obj[k] = v
	}
	data, err := json.Marshal(obj)
	if err != nil {
		return
	}
	fmt.Fprintln(cliLogFile, string(data))
}

// Execute is the main entry point for the CLI.
func Execute(args []string) {
	// Generate request ID for cross-boundary tracing
	requestID = generateRequestID()

	// Initialize CLI file logger (optional, via POLIS_LOG_FILE env var)
	initCLILogger()
	defer func() {
		if cliLogFile != nil {
			cliLogFile.Close()
		}
	}()

	// Propagate CLI version to all packages that embed it in metadata
	publish.Version = Version
	comment.Version = Version
	metadata.Version = Version
	following.Version = Version
	index.Version = Version
	notification.Version = Version
	theme.Version = Version
	feed.Version = Version
	site.Version = Version
	dm.Version = Version
	tag.Version = Version

	// Load .env file (does not override existing env vars, matches bash CLI)
	loadEnv()

	// Propagate discovery config to packages that register with discovery
	discoveryURL = os.Getenv("DISCOVERY_SERVICE_URL")
	if discoveryURL == "" {
		discoveryURL = DefaultDiscoveryServiceURL
	}
	discoveryKey = os.Getenv("DISCOVERY_SERVICE_KEY")
	baseURL = os.Getenv("POLIS_BASE_URL")

	publish.DiscoveryURL = discoveryURL
	publish.DiscoveryKey = discoveryKey
	publish.BaseURL = baseURL

	comment.DiscoveryURL = discoveryURL
	comment.DiscoveryKey = discoveryKey
	comment.BaseURL = baseURL

	stream.DiscoveryURL = discoveryURL
	stream.DiscoveryKey = discoveryKey
	stream.BaseURL = baseURL

	tag.DiscoveryURL = discoveryURL
	tag.DiscoveryKey = discoveryKey
	tag.BaseURL = baseURL

	if len(args) < 1 {
		printUsage()
		os.Exit(1)
	}

	// Parse global flags
	var filteredArgs []string
	for i := 0; i < len(args); i++ {
		arg := args[i]
		switch {
		case arg == "--json":
			jsonOutput = true
		case arg == "--data-dir" && i+1 < len(args):
			dataDir = args[i+1]
			i++ // Skip the next arg (the value)
		case len(arg) > 11 && arg[:11] == "--data-dir=":
			dataDir = arg[11:]
		default:
			filteredArgs = append(filteredArgs, arg)
		}
	}

	if len(filteredArgs) < 1 {
		printUsage()
		os.Exit(1)
	}

	// Propagate DataDir to packages that need it for registration checks
	resolvedDataDir := getDataDir()
	stream.DataDir = resolvedDataDir
	tag.DataDir = resolvedDataDir
	following.DataDir = resolvedDataDir
	following.DiscoveryURL = discoveryURL

	// One-time migration: backfill registration marker for already-registered sites
	discovery.MigrateRegistrationState(resolvedDataDir, discoveryURL, baseURL)

	command := filteredArgs[0]
	cmdArgs := filteredArgs[1:]

	// Log CLI action for network-capable commands (audit trail)
	defer func() {
		switch command {
		case "post", "republish", "comment", "follow", "unfollow", "discover",
			"blessing", "register", "unregister", "dm", "clone", "rotate-key", "tag":
			logCLIAction(command, map[string]interface{}{
				"args_count": len(cmdArgs),
			})
		}
	}()

	switch command {
	case "init":
		handleInit(cmdArgs)
	case "validate":
		handleValidate(cmdArgs)
	case "render":
		handleRender(cmdArgs)
	case "post":
		handlePublish(cmdArgs)
	case "republish":
		handleRepublish(cmdArgs)
	case "comment":
		handleComment(cmdArgs)
	case "preview":
		handlePreview(cmdArgs)
	case "extract":
		handleExtract(cmdArgs)
	case "index":
		handleIndex(cmdArgs)
	case "about":
		handleAbout(cmdArgs)
	case "follow":
		handleFollow(cmdArgs)
	case "unfollow":
		handleUnfollow(cmdArgs)
	case "discover":
		handleDiscover(cmdArgs)
	case "blessing":
		handleBlessing(cmdArgs)
	case "rebuild":
		handleRebuild(cmdArgs)
	case "rotate-key":
		handleRotateKey(cmdArgs)
	case "unpublish":
		handleUnpublish(cmdArgs)
	case "notifications":
		handleNotifications(cmdArgs)
	case "clone":
		handleClone(cmdArgs)
	case "register":
		handleRegister(cmdArgs)
	case "unregister":
		handleUnregister(cmdArgs)
	case "dm":
		handleDM(cmdArgs)
	case "tag":
		handleTag(cmdArgs)
	case "serve":
		handleServe(cmdArgs)
	case "version", "--version", "-v":
		if jsonOutput {
			outputJSON(map[string]interface{}{
				"status":  "success",
				"command": "version",
				"data": map[string]interface{}{
					"version": Version,
				},
			})
		} else {
			fmt.Printf("polis %s\n", Version)
		}
	case "help", "--help", "-h":
		printUsage()
	default:
		fmt.Fprintf(os.Stderr, "Unknown command: %s\n", command)
		printUsage()
		os.Exit(1)
	}
}

func printUsage() {
	fmt.Print(`Polis - Decentralized Social Network CLI

Usage:
  polis [--json] <command> [options]

Global Flags:
  --json                          Output results in JSON format
  --data-dir <path>               Site data directory (default: current directory)

Commands related to creating or viewing content:
  polis post <file>               Create a new post
  polis comment <file> [url]      Create a comment on a post
  polis republish <file>          Update an already-published file
  polis preview <url>             Preview a post or comment with signature verification
  polis extract <file> <hash>     Reconstruct a specific version of a file

Commands related to requesting, reviewing, or granting blessings:
  polis blessing requests         List pending blessing requests
  polis blessing grant <hash>     Grant a blessing request by content hash
  polis blessing deny <hash>      Deny a blessing request by content hash
  polis blessing beseech <hash>   Re-request blessing by content hash
  polis blessing sync             Sync auto-blessed comments from discovery service

Commands related to following or unfollowing an author:
  polis follow <author-url>       Follow an author (auto-bless their comments)
  polis unfollow <author-url>     Unfollow an author

Commands related to content discovery:
  polis discover                  Check followed authors for new content
  polis discover --author <url>   Check a specific author
  polis discover --since <date>   Show items since date

Commands related to notifications:
  polis notifications             List unread notifications
  polis notifications list        List notifications (--type <types>)

Commands related to direct messages:
  polis dm list                   List DM conversations
  polis dm read <conv_id>         Read messages in a conversation
  polis dm send <url> <message>   Send a DM to a recipient
  polis dm retry [conv_id]        Retry delivering unsent messages

Commands related to tagging content:
  polis tag list                   List all tags
  polis tag show <name>            Show targets for a tag
  polis tag apply <name> <uri>     Tag content with a tag name
  polis tag remove <name> <uri>    Remove a target from a tag
  polis tag delete <name>          Delete an entire tag

Commands related to site administration:
  polis register                  Register site with discovery service
  polis unregister [--force]      Unregister site
  polis render [--force]          Render markdown to HTML

Commands related to cloning remote polis sites:
  polis clone <url> [dir]         Clone a public polis site
  polis clone <url> --full        Re-download all content
  polis clone <url> --diff        Only download new/changed content

Commands related to local configuration:
  polis init [options]            Initialize Polis directory structure
    --site-title <title>          Site display name
    --keys-dir <path>             Custom keys directory (default: .polis/keys)
    --posts-dir <path>            Custom posts directory (default: posts)
    --comments-dir <path>         Custom comments directory (default: comments)
    --snippets-dir <path>         Custom snippets directory (default: snippets)
    --versions-dir <path>         Custom versions directory (default: .versions)
  polis rebuild --posts|--comments|--notifications|--all
                                  Rebuild indexes and reset state
  polis index                     View index
  polis version                   Print CLI version
  polis about                     Show site, versions, config info
  polis unpublish <path>          Remove a published post
  polis rotate-key                Generate new keypair and rotate discovery key
  polis serve [-d|--data-dir PATH] Start local web server (bundled binary only)

Examples:
  polis init
  polis post my-post.md
  polis comment my-comment.md https://example.com/posts/hello.md
  polis preview https://example.com/posts/hello.md
  polis blessing requests
  polis discover
`)
}

// getDataDir returns the data directory, defaulting to current working directory
func getDataDir() string {
	if dataDir != "" {
		return dataDir
	}
	cwd, err := os.Getwd()
	if err != nil {
		exitError("Failed to get working directory: %v", err)
	}
	return cwd
}

// exitError prints an error message and exits
func exitError(format string, args ...interface{}) {
	msg := fmt.Sprintf(format, args...)
	if jsonOutput {
		output := map[string]interface{}{
			"success": false,
			"error":   msg,
		}
		json.NewEncoder(os.Stdout).Encode(output)
	} else {
		fmt.Fprintf(os.Stderr, "Error: %s\n", msg)
	}
	os.Exit(1)
}

// outputJSON outputs a JSON response
func outputJSON(data interface{}) {
	// Inject request_id into map-type JSON output for cross-boundary tracing
	if m, ok := data.(map[string]interface{}); ok && requestID != "" {
		m["request_id"] = requestID
	}
	json.NewEncoder(os.Stdout).Encode(data)
}

// loadEnv loads a .env file into the process environment.
// Search order: cwd/.env → ~/.polis/.env (matches bash CLI).
// Does NOT override existing environment variables.
func loadEnv() {
	// Try current working directory first
	cwd, err := os.Getwd()
	if err == nil {
		if loadEnvFile(filepath.Join(cwd, ".env")) {
			return
		}
	}
	// Fallback: ~/.polis/.env
	if home, err := os.UserHomeDir(); err == nil {
		loadEnvFile(filepath.Join(home, ".polis", ".env"))
	}
}

// loadEnvFile reads a KEY=VALUE file and sets env vars that aren't already set.
// Returns true if the file was found and loaded.
func loadEnvFile(path string) bool {
	f, err := os.Open(path)
	if err != nil {
		return false
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		parts := strings.SplitN(line, "=", 2)
		if len(parts) != 2 {
			continue
		}
		key := strings.TrimSpace(parts[0])
		value := strings.TrimSpace(parts[1])
		// Remove surrounding quotes
		if len(value) >= 2 && ((value[0] == '"' && value[len(value)-1] == '"') ||
			(value[0] == '\'' && value[len(value)-1] == '\'')) {
			value = value[1 : len(value)-1]
		}
		// Don't override existing env vars
		if os.Getenv(key) == "" {
			os.Setenv(key, value)
		}
	}
	return true
}
