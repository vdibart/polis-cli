package site

import (
	"testing"

	"github.com/vdibart/polis-cli/cli-go/pkg/storage"
)

// ⭐ The api pointer is written only when it changes, removed with nil, and
// survives every other writer of .well-known/polis (the struct writer and the
// other pointer setters), because it rides Extra.
func TestAPIPointerIsWrittenOnceAndPreserved(t *testing.T) {
	dir, _ := seedEveryMemberSite(t)
	s := storage.At(dir)
	p := &APIPointer{Description: "https://alice.example.com/v1/openapi.json", Version: "v1", MCP: "https://alice.example.com/mcp"}

	if changed, err := SetAPIPointer(s, p); err != nil || !changed {
		t.Fatalf("first write: changed=%v err=%v", changed, err)
	}
	if changed, err := SetAPIPointer(s, p); err != nil || changed {
		t.Fatalf("same value again: changed=%v err=%v, want no write", changed, err)
	}

	// Other writers of the document keep it.
	wk, err := LoadWellKnown(s)
	if err != nil {
		t.Fatal(err)
	}
	wk.AuthorName = "Alice B"
	if err := SaveWellKnown(s, wk); err != nil {
		t.Fatal(err)
	}
	if err := SetAgentsPointer(s, "/content/pub.polis.core/attestation/agents.json"); err != nil {
		t.Fatal(err)
	}
	if got := APIPointerOf(s); got == nil || *got != *p {
		t.Fatalf("after other writers: %+v, want %+v", got, p)
	}

	if changed, err := SetAPIPointer(s, nil); err != nil || !changed {
		t.Fatalf("remove: changed=%v err=%v", changed, err)
	}
	if got := APIPointerOf(s); got != nil {
		t.Fatalf("after removal: %+v", got)
	}
}
