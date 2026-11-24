package ops

import (
	"context"
	"strings"
	"testing"

	"github.com/vdibart/polis-cli/cli-go/pkg/discovery"
)

// ⛔ Under a grant, comment.create may not send a comment signed earlier: the
// owner's unmarked words would go out as the agent's act.
func TestAnAgentCannotSendACommentSignedEarlier(t *testing.T) {
	h := NewBuiltinCoreHandler()
	_, err := h.Handle(context.Background(), ActionRequest{Action: "create", ContentType: "pub.polis.comment", Payload: map[string]any{"comment_id": "x"}},
		HandlerEnv{Marker: discovery.AgentMarker{Agent: "a", Grant: "https://alice.example/g.json"}})
	if err == nil || !strings.Contains(err.Error(), "signed earlier") {
		t.Errorf("err = %v", err)
	}
}
