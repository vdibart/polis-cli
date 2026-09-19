package cmd

import (
	"bytes"
	"strings"
	"testing"

	"github.com/vdibart/polis-cli/cli-go/pkg/agent"
)

// Signet epic 11 D11 — `polis init` asks about Rosie with the one source text,
// and nothing is pre-selected.

func TestRosiePromptShowsTheOneText(t *testing.T) {
	var out bytes.Buffer
	PromptForRosie(strings.NewReader("\n"), &out)
	if !strings.Contains(out.String(), agent.RosieText.Plain()) {
		t.Fatalf("the init prompt does not render agent.RosieText:\n%s", out.String())
	}
}

func TestRosiePromptOnlyAnExplicitYesSwitchesHerOn(t *testing.T) {
	for input, want := range map[string]bool{
		"\n": false, "": false, "n\n": false, "no\n": false, "yse\n": false, "1\n": false,
		"y\n": true, "yes\n": true, " YES \n": true,
	} {
		var out bytes.Buffer
		if got := PromptForRosie(strings.NewReader(input), &out); got != want {
			t.Errorf("input %q → %v, want %v", input, got, want)
		}
	}
}
