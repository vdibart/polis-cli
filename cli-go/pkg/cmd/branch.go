package cmd

import (
	"flag"
	"fmt"
	"strings"

	"github.com/vdibart/polis-cli/cli-go/pkg/branch"
	"github.com/vdibart/polis-cli/cli-go/pkg/clone"
	"github.com/vdibart/polis-cli/cli-go/pkg/signing"
)

const branchUsage = `Usage: polis branch <export.zip | export-dir> --to <url> [--out <dir> | --archive <file>]
                    [--from-domain <origin>] [--without-private] [--recovery-key <path>] [--custody server]

Turn an export of YOUR site (with its private key) into a new site at a new
address, ready to deploy there. The new site's key is made here; the exported
key signs the handover to it, under the origin's domain, so everything the site
signed before still verifies at the new address.

It also makes your recovery key: the key you sign in with the first time, and
whenever you lose access. Its private half is written OUTSIDE the new site
(default ~/.polis/recovery/<new host>) and never deployed; its public half goes
with the site, for the deploy to set on the instance. Sign a challenge with
'polis recover sign --domain <new host>'.

Nothing is sent anywhere: no discovery service is told, and the origin site is
not changed. Deploying the result is the next step: 'polis branch deploy'
(see polis branch deploy --help).

The output is a directory named after the new host, or --out <dir> (which
must be empty), or a zip archive with --archive <file>.

Refuses, writing nothing, when:
  - the export holds no private key, or a key that is not the one its
    .well-known/polis publishes (without it the result would be a mirror)
  - the origin's domain is unknown: the export has no .well-known/did.json
    naming it, and no --from-domain was given
  - the export has no private residue (settings, policies, drafts), unless
    --without-private, which resets each to its default and lists them
  - <export> is not a polis site (it has no .well-known/polis)
  - --to is missing, is not an http(s) site root (no path, query or
    credentials), or is the origin's own address
  - both --out and --archive are given, or the output and the export
    contain one another
  - --custody names anything but server (client is not built yet)
  - the output directory is not empty, or the recovery key path exists
  - the new site fails a check that the export passes

Example:
  polis branch export.zip --to https://alice.example --out ./alice
  polis branch deploy ./alice --config deploy.toml

Flags may come before or after the export.

`

func handleBranch(args []string) {
	// ⭐ `polis branch deploy <dir>` is the second step. A branch export that
	// is literally named "deploy" is written ./deploy.
	if len(args) > 0 && args[0] == "deploy" {
		handleBranchDeploy(args[1:])
		return
	}
	if len(args) > 0 && args[0] == "announce" {
		handleBranchAnnounce(args[1:])
		return
	}
	fs := flag.NewFlagSet("branch", flag.ExitOnError)
	to := fs.String("to", "", "The address the branched site will be served at (https://…)")
	out := fs.String("out", "", "Directory to write the new site to (default: a directory named after the new address)")
	archive := fs.String("archive", "", "Write the new site as a zip archive at this path instead of a directory")
	fromDomain := fs.String("from-domain", "", "The origin's domain, when the export has no .well-known/did.json naming it")
	withoutPrivate := fs.Bool("without-private", false, "Branch without the private residue (policies, settings, drafts), resetting it to defaults")
	recoveryKey := fs.String("recovery-key", "", "Where to write your recovery private key (default ~/.polis/recovery/<new host>); never inside the new site")
	custody := fs.String("custody", string(branch.ServerSigned), "Which key you keep: server (the instance signs, you keep a recovery key). client is not built yet")
	fs.Usage = func() {
		fmt.Fprint(fs.Output(), branchUsage)
		fs.PrintDefaults()
	}
	// Flags on either side of the export: `polis branch export.zip --to …`
	// reads the same as `polis branch --to … export.zip`.
	rest, err := parseFlagsInterspersed(fs, args)
	if err != nil {
		exitError("%v", err)
	}
	if len(rest) != 1 {
		exitError("Usage: polis branch <export.zip | export-dir> --to <url> [--out <dir> | --archive <file>]")
	}
	if *out != "" && *archive != "" {
		exitError("Name one output: --out or --archive, not both")
	}
	outDir := *out
	if outDir == "" && *archive == "" && *to != "" {
		outDir = clone.ExtractDomainForDir(*to)
	}

	res, err := branch.Run(branch.Options{
		Source:         rest[0],
		To:             *to,
		Out:            outDir,
		Archive:        *archive,
		FromDomain:     *fromDomain,
		WithoutPrivate: *withoutPrivate,
		CLIThemesDir:   findCLIThemesDir(),
		Custody:        branch.Custody(*custody),
		RecoveryKey:    *recoveryKey,
	})
	if err != nil {
		exitError("Branch not made: %v", err)
	}

	logCLIAction("branch", map[string]interface{}{
		"from":      res.FromDomain,
		"to":        res.ToDomain,
		"epoch":     res.Epoch,
		"archive":   res.IsArchive,
		"annotated": res.Annotated,
	})

	if jsonOutput {
		outputJSON(map[string]interface{}{
			"status":  "success",
			"command": "branch",
			"data":    res,
		})
		return
	}

	fmt.Println("[✓] Branch made")
	fmt.Printf("  From: %s\n", res.FromDomain)
	fmt.Printf("  To:   %s\n", res.BaseURL)
	fmt.Printf("  Written to: %s\n", res.Output)
	fmt.Println()
	fmt.Println("[i] New public key:")
	fmt.Printf("  %s\n", res.NewPublicKey)
	fmt.Printf("[i] Appended epoch %d to public_key_history, signed by the exported key under %s\n", res.Epoch, res.FromDomain)
	if res.Annotated > 0 {
		fmt.Printf("[i] Recorded %s as the signing domain of %d earlier handover(s), so they verify at the new address\n", res.FromDomain, res.Annotated)
	}
	printBranchResign("following.json", res.FollowResign)
	printBranchResign("blessed.json", res.BlessedResign)
	for _, e := range res.ResignErrors {
		fmt.Printf("[!] %s\n", e)
	}
	if res.MessagesKeyRemoved {
		fmt.Println("[i] Removed public_key_messages: the DM keyring stays with the origin, so set up DMs again at the new site")
	}
	if len(res.LeftBehind) > 0 {
		fmt.Printf("[i] Not carried: %s\n", strings.Join(res.LeftBehind, "; "))
	}
	if len(res.ResidueReset) > 0 {
		fmt.Println("[!] Branched without the private residue. Reset to defaults:")
		for _, r := range res.ResidueReset {
			fmt.Printf("    - %s: %s\n", r.What, r.Default)
		}
	}
	fmt.Printf("[i] Rendered %d posts and %d comments for the new address\n", res.PostsRendered, res.CommentsRendered)
	if v := res.Validation; v != nil {
		fmt.Printf("[i] polis validate on the new site: %d passed, %d failed, %d warning, %d not applicable\n",
			v.Totals.Passed, v.Totals.Failed, v.Totals.Warning, v.Totals.NotApplicable)
	}
	if len(res.InheritedFailures) > 0 {
		fmt.Printf("[!] These checks fail on the export too, so the branch carried them: %s\n", strings.Join(res.InheritedFailures, ", "))
	}
	fmt.Println()
	fmt.Printf("[i] Your recovery key is at %s\n", res.RecoveryKeyPath)
	fmt.Println("    It is how you sign in to the new site the first time, and whenever you lose access: keep it, and keep it")
	fmt.Println("    off the server. Its public half travels with the site, for the deploy to set as POLIS_RECOVERY_KEY.")
	fmt.Println("[!] Your export still holds the retired private key. Delete it once the branch is deployed.")
	fmt.Println("[i] Nothing was announced to a discovery service, and the origin site is unchanged.")
	if !res.IsArchive {
		fmt.Printf("[i] Try it locally: polis serve --data-dir %s\n", res.Output)
	}
}

func printBranchResign(file string, out signing.ResignOutcome) {
	switch out {
	case signing.ResignDone:
		fmt.Printf("[i] Re-signed %s under the new key\n", file)
	case signing.ResignNotValid:
		fmt.Printf("[!] Left %s as it was: its signature did not verify under the exported key, so it was not re-signed\n", file)
	case signing.ResignRefused:
		fmt.Printf("[!] Left %s as it was: it holds fields this build cannot re-sign\n", file)
	}
}
