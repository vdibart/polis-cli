package cmd

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/vdibart/polis-cli/cli-go/pkg/agent"
)

// PromptForRosie asks whether to switch Rosie on, and returns true only for an
// explicit yes (Signet epic 11 D11).
//
// ⛔ NOTHING IS PRE-SELECTED. Enter, a typo and a read error all leave her off —
// the licence prompt's rule, for the same reason: a default nobody chose is not
// a choice. The cost is asymmetric the other way round too: switching her on
// later is one click in Settings → Rosie.
//
// ⛔ THE WORDS ARE agent.RosieText — the same source Settings → Rosie renders.
//
// ⚠️ Only ever called for an interactive terminal. A non-interactive init states
// nothing, and `--json` is never prompted.
func PromptForRosie(in io.Reader, out io.Writer) bool {
	fmt.Fprintf(out, "\n%s\n\nSwitch Rosie on? Type yes or no (press enter to leave her off): ", agent.RosieText.Plain())
	answer, err := bufio.NewReader(in).ReadString('\n')
	if err != nil && answer == "" {
		return false
	}
	switch strings.ToLower(strings.TrimSpace(answer)) {
	case "y", "yes":
		return true
	}
	return false
}

// switchRosieOnAtInit issues the user's own grant after `polis init`, and says
// what happened.
//
// ⚠️ The grant is not registered with a discovery service here: a site that has
// just been created is not registered anywhere yet. The record on the site is
// the record.
func switchRosieOnAtInit(dir, base, privateKeyPath string) {
	if strings.TrimSpace(base) == "" {
		fmt.Println("[i] Rosie: not switched on — she needs your site's address.")
		fmt.Println("    Set POLIS_BASE_URL, then switch her on in the web app (`polis-full serve` or `polis-server`): Settings → Rosie.")
		return
	}
	key, err := os.ReadFile(privateKeyPath)
	if err != nil {
		key, err = os.ReadFile(filepath.Join(dir, ".polis", "keys", "id_ed25519"))
	}
	if err != nil {
		fmt.Printf("[!] Rosie: not switched on — could not read the site key: %v\n", err)
		return
	}
	w, _, err := agent.Enable(agent.Config{SiteDir: dir, BaseURL: base, PrivateKey: key})
	if err != nil {
		fmt.Printf("[!] Rosie: not switched on: %v\n", err)
		return
	}
	logCLIAction("pub.polis.agent.grant_issued", map[string]interface{}{
		"agent": agent.Rosie, "basis": w.Basis, "behaviours": w.Behaviours, "grant_url": w.URL, "trigger": "init",
	})
	fmt.Println("[✓] Rosie: on. She works while the web app runs (`polis-full serve` or `polis-server`).")
	fmt.Println("    Switch her off at any time in Settings → Rosie.")
}

// describeRosieDeclined says how to switch her on later — the difference between
// declining a choice and never being told there was one.
func describeRosieDeclined() {
	fmt.Println("[i] Rosie: off. Comments will wait for you to approve them yourself.")
	fmt.Println("    Switch her on whenever you like in the web app (`polis-full serve` or `polis-server`): Settings → Rosie.")
}
