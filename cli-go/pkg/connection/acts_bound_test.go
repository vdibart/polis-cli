package connection

import (
	"encoding/json"
	"fmt"
	"testing"

	"github.com/vdibart/polis-cli/cli-go/pkg/storage"
)

// ⛔ The owner's acts never push a connection's out of the record. The owner
// writes far more often (a draft saves while they type), and Judge checks an
// agent's marked work against the connections' acts, so each kind keeps its
// own newest (maxActs for connections, maxOwnerActs for the owner).
func TestTheOwnersActsNeverPushAConnectionsOut(t *testing.T) {
	line := func(a Act) []byte { b, _ := json.Marshal(a); return b }
	var lines [][]byte
	lines = append(lines, line(Act{Agent: "claude", Grant: "https://maya.example/g.json", Path: "/v1/content/post", RequestID: "agent-oldest"}))
	for i := 0; i < maxOwnerActs+10; i++ {
		lines = append(lines, line(Act{Path: "/v1/content/post/actions/save_draft", RequestID: fmt.Sprintf("owner-%d", i)}))
	}
	out := trimActs(lines)

	var agents, owners int
	var firstOwner string
	for _, l := range out {
		var a Act
		if err := json.Unmarshal(l, &a); err != nil {
			t.Fatal(err)
		}
		if a.Owner() {
			if owners == 0 {
				firstOwner = a.RequestID
			}
			owners++
		} else {
			agents++
		}
	}
	if agents != 1 {
		t.Errorf("connection acts kept = %d, want the one, however many the owner made", agents)
	}
	if owners != maxOwnerActs || firstOwner != "owner-10" {
		t.Errorf("owner acts kept = %d from %s, want the newest %d (from owner-10)", owners, firstOwner, maxOwnerActs)
	}
}

func TestAgentActsLeavesTheOwnersOut(t *testing.T) {
	site := storage.At(t.TempDir())
	for _, a := range []Act{
		{Agent: "claude", Grant: "https://maya.example/g.json", Path: "/v1/content/post", Status: 201},
		{Path: "/v1/content/post", Status: 201, Op: "pub.polis.post.create", Surface: "v5"},
	} {
		if err := RecordAct(site, a); err != nil {
			t.Fatal(err)
		}
	}
	all, _ := Acts(site, "")
	agents, _ := AgentActs(site)
	if len(all) != 2 || len(agents) != 1 || agents[0].Agent != "claude" {
		t.Errorf("all = %+v, agents = %+v", all, agents)
	}
	if all[1].Op != "pub.polis.post.create" || all[1].Surface != "v5" || !all[1].Owner() {
		t.Errorf("owner act = %+v, want its op and surface recorded", all[1])
	}
}
