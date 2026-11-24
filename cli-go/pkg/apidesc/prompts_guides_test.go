package apidesc

import (
	"regexp"
	"testing"

	"github.com/vdibart/polis-cli/cli-go/pkg/agentguide"
)

// A prompt that names a workflow guide names one every MCP server serves; and
// the prompts for titling, replying and reviewing blessings each name theirs.
func TestPromptsNameOnlyGuidesThatExist(t *testing.T) {
	served := map[string]bool{}
	for _, g := range agentguide.Guides() {
		served[g.URI] = true
	}
	uri := regexp.MustCompile(`polis://guides/[a-z-]+`)
	named := map[string]string{}
	for _, p := range Prompts() {
		for _, u := range uri.FindAllString(p.Text, -1) {
			if !served[u] {
				t.Errorf("prompt %s names %s, which no server serves", p.Name, u)
			}
			named[p.Name] = named[p.Name] + u
		}
	}
	for name, want := range map[string]string{"draft_a_post": guideTitles, "reply_to_a_post": guideReplies, "comments_waiting": guideBlessing, "whats_new": guideUntrusted} {
		if !regexp.MustCompile(regexp.QuoteMeta(want)).MatchString(named[name]) {
			t.Errorf("prompt %s does not name %s", name, want)
		}
	}
}
