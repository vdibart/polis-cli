package cmd

import (
	"bufio"
	"flag"
	"fmt"
	"os"
	"strings"

	"golang.org/x/term"

	"github.com/vdibart/polis-cli/cli-go/pkg/license"
	"github.com/vdibart/polis-cli/cli-go/pkg/site"
)

// licensePromptText is the choice shown at `polis init` and by `polis license`.
//
// Four properties it has to hold, each learned the hard way:
//
//   - ⛔ NOTHING IS PRE-SELECTED. The recommendation is shown as TEXT and is
//     never the answer pressing enter gives. A default nobody chose is not a
//     choice, and it is the same imposition whether it arrives silently or
//     behind a prompt someone dismissed. Pressing enter states NOTHING.
//     (This reverses epic 01's Decision 2, which pre-selected `reserved`.)
//   - The machine values are SHOWN. Nobody should agree to something opaque,
//     and it teaches the vocabulary in passing.
//   - NON-RETROACTIVITY IS STATED AT THE MOMENT OF CHOOSING. It is the property
//     people otherwise discover far too late — when they tighten their terms
//     and find their whole archive unchanged.
//   - "Unstated" is a GENUINE option. Forcing a declaration is its own
//     imposition, and unknown is a defined state rather than a gap.
const licensePromptText = `
Your posts can carry terms describing how others may use them.

  1. Reserved  (recommended)
     Read and quote freely with a link back. Search engines may index
     your work and send people to it. AI training and answer-engine
     summaries require asking.
     → train-ai=n  search=y  ai-input=n  attribution=required

  2. Open
     Anyone may use your work for anything, including AI training.
     → train-ai=y  search=y  ai-input=y

  3. Unstated
     Publish no terms. Readers fall back to their own assumptions.

You can change this at any time with 'polis license'. Note that terms are
not retroactive — posts already published keep the terms they were signed
with.

Press enter to state nothing for now.

Choice: `

// isInteractive reports whether there is a human at the other end to ask.
//
// When there is not, `polis init` states NOTHING rather than picking on the
// user's behalf. A script that wants terms passes --license.
func isInteractive() bool {
	return term.IsTerminal(int(os.Stdin.Fd()))
}

// PromptForLicense asks the user to choose terms and returns the choice name.
//
// Only ever called for an interactive terminal. Non-interactive callers pass
// --license explicitly, and hosted signup states nothing at all; no path may
// decide for the user.
//
// ⛔ EVERY ANSWER THAT IS NOT AN EXPLICIT CHOICE RETURNS "none". Empty input, a
// typo, and a read error alike. Terms are a signed statement of intent, so the
// only input that may produce one is input that names it — a mistyped "r" must
// never publish a licence, and the cost of the two failure directions is not
// symmetric: an unwanted grant is irreversible for everything already
// published, an unwanted silence costs one `polis license reserved`.
func PromptForLicense(in *os.File, out *os.File) string {
	fmt.Fprint(out, licensePromptText)
	reader := bufio.NewReader(in)
	answer, err := reader.ReadString('\n')
	if err != nil && answer == "" {
		return "none"
	}
	switch strings.TrimSpace(answer) {
	case "1", "reserved":
		return "reserved"
	case "2", "open":
		return "open"
	default:
		// "", "3", "none", "unstated", and anything unrecognised.
		return "none"
	}
}

// describeLicenseChoice prints what was actually chosen. `polis init` must SHOW
// the choice, not merely record it — the whole consent argument rests on the
// user having seen it.
//
// ⚠️ The nil branch is now the DEFAULT outcome of `polis init`, not an edge
// case: pressing enter at the prompt states nothing. So it has to say how to
// state terms later. That sentence is the whole difference between DECLINING a
// choice and never being told there was one.
func describeLicenseChoice(choice string, terms *license.Terms) {
	if terms == nil {
		fmt.Println("[i] Licence: none stated. Your posts publish no terms.")
		fmt.Println("    You can state them whenever you like — nothing here is final:")
		fmt.Println("      polis license reserved   the recommendation")
		fmt.Println("      polis license open       grant everything")
		fmt.Println("      polis license            show what this site says today")
		return
	}
	fmt.Printf("[✓] Licence: %s\n", terms.Profile)
	fmt.Printf("    %s\n", license.ContentUsage(terms))
	if terms.AIInput != "" {
		fmt.Printf("    ai-input=%s (RSL — summarising into an answer)\n", terms.AIInput)
	}
	if terms.Attribution != "" {
		fmt.Printf("    attribution=%s\n", terms.Attribution)
	}
	fmt.Println("    Terms are not retroactive: posts already published keep the")
	fmt.Println("    terms they were signed with.")
}

func handleLicense(args []string) {
	fs := flag.NewFlagSet("license", flag.ExitOnError)
	fs.Parse(args)

	dir := getDataDir()
	if !isPolisSite(dir) {
		exitError("Not a polis site directory (no .well-known/polis found)")
	}

	remaining := fs.Args()

	// No argument: report the current state rather than changing anything.
	if len(remaining) == 0 {
		terms, err := site.SiteTerms(dir)
		if err != nil {
			exitError("Failed to read licence: %v", err)
		}
		if jsonOutput {
			outputJSON(map[string]interface{}{
				"status":  "success",
				"command": "license",
				"data":    map[string]interface{}{"terms": terms, "pointer": site.LicensePointer(dir)},
			})
			return
		}
		if terms == nil {
			fmt.Println("[i] This site states no terms.")
			fmt.Println()
			fmt.Println("    Your posts don't carry terms describing how others may use them.")
			fmt.Println("    Run `polis license reserved` (or `open`) to state some.")
			fmt.Println()
			fmt.Println("    Terms are not retroactive — stating them now applies to posts")
			fmt.Println("    published from here on, not to your archive.")
			return
		}
		describeLicenseChoice("", terms)
		fmt.Printf("    Signed source: %s\n", site.LicensePointer(dir))
		return
	}

	choice := remaining[0]
	if _, _, err := license.ParseProfileName(choice); err != nil {
		exitError("%v", err)
	}

	privKey, err := loadPrivateKey(dir)
	if err != nil {
		exitError("Failed to load private key: %v", err)
	}

	if strings.EqualFold(choice, "none") || strings.EqualFold(choice, "unstated") {
		if err := site.WithdrawLicense(dir); err != nil {
			exitError("Failed to withdraw licence: %v", err)
		}
		if jsonOutput {
			// terms: null is the withdrawn state, and it is the same shape a
			// bare `polis license` returns for a site that never stated any —
			// so a script gets one answer to "what does this site say" rather
			// than two. Without this branch the command printed human text in
			// --json mode and broke every caller piping to a parser.
			outputJSON(map[string]interface{}{
				"status":  "success",
				"command": "license",
				"data":    map[string]interface{}{"terms": nil, "pointer": site.LicensePointer(dir)},
			})
			return
		}
		fmt.Println("[✓] Licence withdrawn. This site now states no terms.")
		fmt.Println("    Posts already published are unchanged — they keep the terms")
		fmt.Println("    they were signed with.")
		return
	}

	if _, err := site.StateLicense(dir, choice, baseURL, privKey); err != nil {
		exitError("Failed to state licence: %v", err)
	}
	terms, err := site.SiteTerms(dir)
	if err != nil {
		exitError("Failed to read back licence: %v", err)
	}

	if jsonOutput {
		outputJSON(map[string]interface{}{
			"status":  "success",
			"command": "license",
			"data":    map[string]interface{}{"terms": terms},
		})
		return
	}
	describeLicenseChoice(choice, terms)
	fmt.Println()
	fmt.Println("    Run `polis render` to regenerate robots.txt, rsl.xml, and your")
	fmt.Println("    terms page from the signed licence.")
}
