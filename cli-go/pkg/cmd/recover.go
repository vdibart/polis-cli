package cmd

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"os"
	"strings"

	"golang.org/x/term"

	"github.com/vdibart/polis-cli/cli-go/pkg/branch"
	"github.com/vdibart/polis-cli/cli-go/pkg/owner"
	"github.com/vdibart/polis-cli/cli-go/pkg/signing"
	polisurl "github.com/vdibart/polis-cli/cli-go/pkg/url"
)

const recoverUsage = `Usage: polis recover sign --domain <host> [--challenge <file>] [--key <path>]

Sign a sign-in recovery challenge with the key you hold, and print the
signature to paste back into the setup or recovery screen.

The challenge is the message the screen shows under "Copy message only"
(three lines, starting pub.polis.recovery.challenge.v1), or the challenge
JSON. Give it in any of three ways:

  polis recover sign --domain alice.vercel.app --challenge challenge.txt   a file
  pbpaste | polis recover sign --domain alice.vercel.app                   a pipe
  polis recover sign --domain alice.vercel.app                             pasted, then Ctrl-D

--domain is required: it is the site YOU are signing in to, and a challenge
for any other site is refused. The challenge names a site too, but a page
pretending to be yours could relay your real site's challenge, so the site
must come from you, not from the text you were given.

Anything that is not a challenge is refused, so this command never signs
arbitrary text under the recovery namespace.

The key defaults to ~/.polis/recovery/<host>, where 'polis branch' writes it.
It must not be readable by anyone else.

`

func handleRecover(args []string) {
	if len(args) == 0 || args[0] != "sign" {
		fmt.Fprint(os.Stderr, recoverUsage)
		os.Exit(1)
	}
	handleRecoverSign(args[1:])
}

func handleRecoverSign(args []string) {
	fs := flag.NewFlagSet("recover sign", flag.ExitOnError)
	challenge := fs.String("challenge", "", "A file holding the challenge; - for stdin (the default)")
	key := fs.String("key", "", "Your recovery private key (default ~/.polis/recovery/<host>)")
	domain := fs.String("domain", "", "The site you are signing in to (required); a challenge for any other is refused")
	fs.Usage = func() {
		fmt.Fprint(fs.Output(), recoverUsage)
		fs.PrintDefaults()
	}
	rest, err := parseFlagsInterspersed(fs, args)
	if err != nil {
		exitError("%v", err)
	}
	if len(rest) != 0 {
		exitError("polis recover sign takes the challenge from --challenge <file> or stdin, not as an argument")
	}
	stdinIsTerminal := term.IsTerminal(int(os.Stdin.Fd()))
	if *challenge == "" && stdinIsTerminal && !jsonOutput {
		fmt.Fprintln(os.Stderr, "Paste the challenge message, then press Ctrl-D:")
	}
	input, err := readChallenge(*challenge, os.Stdin, stdinIsTerminal)
	if err != nil {
		exitError("%v", err)
	}

	res, err := recoverSign(input, *key, *domain)
	if err != nil {
		exitError("%v", err)
	}
	if jsonOutput {
		outputJSON(map[string]interface{}{
			"status":  "success",
			"command": "recover sign",
			"data": map[string]interface{}{
				"domain":    res.Domain,
				"key":       res.KeyPath,
				"namespace": owner.RecoveryNamespace,
				"signature": res.Signature,
			},
		})
		return
	}
	fmt.Fprintf(os.Stderr, "[i] Signed the challenge for %s with %s. Paste this signature back:\n", res.Domain, res.KeyPath)
	fmt.Print(res.Signature)
	if !strings.HasSuffix(res.Signature, "\n") {
		fmt.Println()
	}
}

// maxChallengeBytes bounds what is read as a challenge: a real one is three
// short lines, or a small JSON document.
const maxChallengeBytes = 64 * 1024

// readChallenge reads the challenge from the file named by --challenge, or
// from stdin when it is empty or "-".
//
// ⭐ An empty stdin that is NOT a terminal is the case worth a precise error:
// an editor, an agent or a script ran the command with nothing piped in, so
// "paste the message" is advice its user cannot follow. Name the forms that
// work instead.
//
// NOT the storage interface: the challenge file is wherever the owner saved
// it on their own machine, outside every site.
func readChallenge(path string, stdin io.Reader, stdinIsTerminal bool) ([]byte, error) {
	if path != "" && path != "-" {
		f, err := os.Open(path)
		if err != nil {
			return nil, fmt.Errorf("read the challenge file: %w", err)
		}
		defer f.Close()
		input, err := io.ReadAll(io.LimitReader(f, maxChallengeBytes))
		if err != nil {
			return nil, fmt.Errorf("read the challenge file %s: %w", path, err)
		}
		if len(strings.TrimSpace(string(input))) == 0 {
			return nil, fmt.Errorf("the challenge file %s is empty: save the setup screen's \"Copy message only\" text in it", path)
		}
		return input, nil
	}
	input, err := io.ReadAll(io.LimitReader(stdin, maxChallengeBytes))
	if err != nil {
		return nil, fmt.Errorf("read the challenge: %w", err)
	}
	if len(strings.TrimSpace(string(input))) == 0 && !stdinIsTerminal {
		return nil, fmt.Errorf("no challenge arrived on stdin, and there is no terminal to paste it at. " +
			"Save the setup screen's \"Copy message only\" text to a file and run: polis recover sign --domain <host> --challenge <file>. " +
			"Or pipe it in from the clipboard: pbpaste | polis recover sign --domain <host> (on Linux: xclip -o -selection clipboard | polis recover sign --domain <host>)")
	}
	return input, nil
}

// recoverSignResult is one signed challenge. The signature is public: it
// proves possession, and is useless for any other challenge.
type recoverSignResult struct {
	Domain    string
	KeyPath   string
	Signature string
}

// recoverSign signs a pasted challenge with the owner's recovery key.
//
// NOT the storage interface: the recovery key lives on the owner's machine,
// outside every site, and is never a site key.
//
// ⛔ Messages name files and domains, never key material.
func recoverSign(input []byte, keyPath, domainFlag string) (*recoverSignResult, error) {
	domain, _, err := owner.ParseChallenge(input)
	if err != nil {
		return nil, fmt.Errorf("that is not a recovery challenge: give it the message the setup screen shows under \"Copy message only\" " +
			"(three lines, the first pub.polis.recovery.challenge.v1)")
	}
	host := polisurl.ExtractDomain(domain)
	// ⛔ The site comes from the owner, never from the challenge alone: a
	// phishing page can relay the owner's REAL site's challenge, and a key
	// picked by the challenge's own domain would sign it for that site.
	if strings.TrimSpace(domainFlag) == "" {
		return nil, fmt.Errorf("say which site you are signing in to: polis recover sign --domain <host>. "+
			"This challenge is for %s; sign it only if that is the site whose setup screen you are on", host)
	}
	if want := polisurl.ExtractDomain(domainFlag); !strings.EqualFold(want, host) {
		return nil, fmt.Errorf("the challenge is for %s, not %s: refusing to sign it", host, want)
	}
	if keyPath == "" {
		if keyPath, err = branch.DefaultRecoveryKeyPath(host); err != nil {
			return nil, err
		}
	}
	info, err := os.Stat(keyPath)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, fmt.Errorf("no recovery key at %s: name yours with --key", keyPath)
	}
	if err != nil {
		return nil, fmt.Errorf("read the recovery key %s: %w", keyPath, err)
	}
	if info.IsDir() {
		return nil, fmt.Errorf("%s is a directory, not a key", keyPath)
	}
	// ⛔ As ssh does: a private key others can read is refused, not used.
	if info.Mode().Perm()&0o077 != 0 {
		return nil, fmt.Errorf("the recovery key %s is readable by others (mode %o): run chmod 600 %s, then sign again",
			keyPath, info.Mode().Perm(), keyPath)
	}
	priv, err := os.ReadFile(keyPath)
	if err != nil {
		return nil, fmt.Errorf("read the recovery key %s: %w", keyPath, err)
	}
	defer signing.ZeroKey(priv)
	sig, err := owner.SignChallenge(input, priv)
	if err != nil {
		return nil, fmt.Errorf("sign with %s: %v", keyPath, err)
	}
	return &recoverSignResult{Domain: host, KeyPath: keyPath, Signature: sig}, nil
}
