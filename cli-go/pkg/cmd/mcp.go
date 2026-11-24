package cmd

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/vdibart/polis-cli/cli-go/pkg/mcpgen"
)

// handleMCP serves `polis mcp`: an MCP server over stdio for an agent running
// on this machine (Claude Code or Desktop launches it), whose tools are
// generated from a polis site's own API description and act through that
// site's /v1.
//
// ⭐ Nothing to host: point it at `polis serve` on this machine, at a remote
// instance, or at a site on polis.pub. The token comes from POLIS_TOKEN, never
// a flag, so it stays out of shell history and process lists.
func handleMCP(args []string) {
	site := os.Getenv("POLIS_BASE_URL")
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--site":
			if i+1 < len(args) {
				site = args[i+1]
				i++
			}
		case "-h", "--help":
			fmt.Println(mcpUsage)
			return
		}
	}
	if site == "" {
		exitError("polis mcp: name the site with --site <address> (or POLIS_BASE_URL)")
	}
	site = strings.TrimRight(site, "/")
	client := &http.Client{Timeout: 60 * time.Second}
	token := os.Getenv("POLIS_TOKEN")
	// ⭐ The description is fetched WITH the token, so the site describes only
	// what this token's grant can do and no tool offered always answers 403.
	req, err := http.NewRequest(http.MethodGet, site+"/v1/openapi.json", nil) //nolint:gosec // G704: the owner's own site URL, from their own command line
	if err != nil {
		exitError("polis mcp: %v", err)
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := client.Do(req) //nolint:gosec // G704: the owner's own site URL, from their own command line
	if err != nil {
		exitError("polis mcp: could not reach %s: %v", site, err)
	}
	desc, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	_ = resp.Body.Close() // read side; the body is already read
	if err != nil || resp.StatusCode != http.StatusOK {
		exitError("polis mcp: %s did not describe its API (status %d)", site, resp.StatusCode)
	}
	ts, err := mcpgen.FromDescription(desc)
	if err != nil {
		exitError("polis mcp: %v", err)
	}
	ex := &mcpgen.Executor{Base: site, Token: token, Client: client}
	srv := mcpgen.NewServer(ts, ex, Version)
	if err := srv.Run(context.Background(), &mcp.StdioTransport{}); err != nil {
		exitError("polis mcp: %v", err)
	}
}

const mcpUsage = `polis mcp [--site <address>]

Serve MCP over stdio for an agent on this machine. The tools are generated
from the site's own API description (<address>/v1/openapi.json) and act
through its /v1 with the token in POLIS_TOKEN.

  --site <address>   the site, e.g. https://alice.polis.pub or the address
                     'polis serve' prints (default: POLIS_BASE_URL)

Owner-only operations and messages are never tools; ask how_do_i for the
steps instead.`
