package cmd

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"

	"github.com/vdibart/polis-cli/cli-go/pkg/discovery"
	"github.com/vdibart/polis-cli/cli-go/pkg/site"
	"github.com/vdibart/polis-cli/cli-go/pkg/storage"
	polisurl "github.com/vdibart/polis-cli/cli-go/pkg/url"
)

const branchAnnounceUsage = `Usage: polis branch announce

Tell your discovery service where this site was branched from, so the
software of people who follow the original can offer them this address.
Run it in the branched site's directory, after ` + "`polis register`" + `.

It presents the handover in this site's key history (the original's key
signing over to this site's key) and the discovery service checks it against
the key it saw the original holding. It shows that this site descends from
the original, never that the original's owner agrees, and it changes nothing
of the original's: no follow is moved and nothing is frozen. Optional: the
branch is valid without it.

Note: today nothing runs this for you. You have to know to run it once the
branch is registered.

Example:
  polis register && polis branch announce

Refuses when this site has no handover from another domain in its key
history (it was not made by polis branch), and when it is not registered
with the discovery service yet.
`

// handleBranchAnnounce is `polis branch announce`.
//
// ⚠️ MECHANICALLY CORRECT, NOT USEFUL YET (Vincent, 2026-11-08). The DS checks
// the announcement properly and delivers the event, but nothing prompts an
// owner to run this: it depends on them knowing to, after `polis register`,
// with only a hint line from `polis branch deploy` to tell them. Making the
// announcement automatic (for example at registration, for a site whose key
// history holds a handover from another domain) is left for when the branch
// work resumes. Don't mistake this command for that.
func handleBranchAnnounce(args []string) {
	fs := flag.NewFlagSet("branch announce", flag.ExitOnError)
	fs.Usage = func() { fmt.Fprint(fs.Output(), branchAnnounceUsage) }
	fs.Parse(args)
	if fs.NArg() != 0 {
		exitError("Usage: polis branch announce (polis branch announce --help for details)")
	}

	dir := getDataDir()
	if !isPolisSite(dir) {
		exitError("Not a polis site directory")
	}
	baseURL := os.Getenv("POLIS_BASE_URL")
	if baseURL == "" {
		exitError("POLIS_BASE_URL not set")
	}
	domain := polisurl.ExtractDomain(baseURL)
	if domain == "" {
		exitError("Could not extract domain from POLIS_BASE_URL")
	}
	dsURL := os.Getenv("DISCOVERY_SERVICE_URL")
	if dsURL == "" {
		dsURL = DefaultDiscoveryServiceURL
	}

	s := storage.At(dir)
	chain, err := site.LoadKeyHistory(s)
	if err != nil {
		exitError("Could not read this site's key history: %v", err)
	}
	source, handover, ok := branchHandover(chain, domain)
	if !ok {
		exitError("This site's key history holds no handover from another domain, so it was not made by polis branch; there is nothing to announce")
	}
	if !discovery.IsRegisteredLocally(s, dsURL) {
		exitError("Not registered with %s yet, so it could not check the announcement. Register first: polis register", dsURL)
	}
	privKey, err := loadPrivateKey(dir)
	if err != nil {
		exitError("Failed to load private key: %v", err)
	}

	witness, err := discovery.NewClient(dsURL).AnnounceBranch(domain, source, handover, privKey)
	if err != nil {
		var refused *discovery.BranchRefusedError
		if errors.As(err, &refused) && refused.Status == 404 {
			exitError("The discovery service refused the announcement (404). It may not support branch announcements yet, or it does not know %s: %s", source, refused.Body)
		}
		exitError("Failed to announce the branch: %v", err)
	}
	saved := ""
	if witness != nil {
		if key := branchWitnessKey(dsURL); key != "" {
			if b, err := json.MarshalIndent(witness, "", "  "); err == nil {
				if err := s.Store().Put(context.Background(), key, append(b, '\n'), storage.WithMode(0o600)); err != nil {
					fmt.Fprintf(os.Stderr, "[!] Warning: could not keep the witness: %v\n", err)
				} else {
					saved = key
				}
			}
		}
	}
	if jsonOutput {
		outputJSON(map[string]interface{}{
			"success":       true,
			"domain":        domain,
			"source_domain": source,
			"witnessed":     witness != nil,
			"witness_file":  saved,
		})
		return
	}
	fmt.Printf("Announced: %s was branched from %s\n", domain, source)
	if saved != "" {
		fmt.Printf("[i] The discovery service's witness is kept at %s\n", saved)
	}
}

// branchHandover finds the most recent handover in a chain that was signed
// under another domain: the mark polis branch leaves. It returns that domain
// and the handover as the DS reads it.
func branchHandover(chain *site.KeyHistoryBlock, domain string) (string, discovery.BranchHandover, bool) {
	if chain == nil {
		return "", discovery.BranchHandover{}, false
	}
	entries := append(append([]site.KeyHistoryEntry{}, chain.History...), chain.Current)
	for i := len(entries) - 1; i >= 1; i-- {
		e := entries[i]
		if e.SigningDomain == "" || e.SigningDomain == domain || e.TransitionSig == nil {
			continue
		}
		return e.SigningDomain, discovery.BranchHandover{
			OldKey:        entries[i-1].Key,
			NewKey:        e.Key,
			Timestamp:     e.ValidFrom,
			TransitionSig: *e.TransitionSig,
		}, true
	}
	return "", discovery.BranchHandover{}, false
}

// branchWitnessKey is where the announcing site keeps the DS's witness,
// beside its registration marker for that DS.
func branchWitnessKey(dsURL string) string {
	d := discovery.ExtractDomainFromURL(dsURL)
	if d == "" {
		return ""
	}
	return ".polis/ds/" + d + "/branched.json"
}
