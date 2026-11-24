package ops

import (
	"context"
	"testing"

	"github.com/vdibart/polis-cli/cli-go/pkg/storage"
	"github.com/vdibart/polis-cli/cli-go/pkg/tag"
)

// A tag file written through the API carries the same generator as one
// written by polis tag: the tag package's own (both now pass none).
func TestATagWrittenThroughTheAPICarriesThePackagesGenerator(t *testing.T) {
	e, dir := newTestEngine(t)
	if _, err := e.Dispatch(context.Background(), ActionRequest{Action: "apply", ContentType: "pub.polis.tag",
		Payload: map[string]any{"tag": "reading", "target_uri": "https://alice.example/posts/a.md"}}); err != nil {
		t.Fatal(err)
	}
	tf, err := tag.LoadTag(storage.At(dir), "reading")
	if err != nil {
		t.Fatal(err)
	}
	if tf.Generator != tag.GetGenerator() {
		t.Errorf("generator = %q, want the tag package's %q", tf.Generator, tag.GetGenerator())
	}
}
