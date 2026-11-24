package bundle

import (
	"testing"

	"github.com/vdibart/polis-cli/cli-go/pkg/storage"
)

// Medic's shape upgrade, for a custodied site: the blog shape (v3) and the
// stream (v4) move to v5, the default; v5 and anything else are left alone,
// and so is a registry from a newer schema, which this build does not rewrite.
// (Neither older shape is deleted: a self-hosted site keeps it and renders as
// before; only Medic, which is never run on a self-hosted site, calls this.)
func TestUpgradeActiveShapeMovesTheBlogAndStreamShapesToV5(t *testing.T) {
	for _, c := range []struct {
		shape string
		want  string
		moved bool
	}{
		{"v3", "pub.polis.shapes.v3", true},
		{"v4", "pub.polis.shapes.v4", true},
		{"v5", "pub.polis.shapes.v5", false},
	} {
		site := storage.At(t.TempDir())
		if err := SetActiveShapeName(site, c.shape); err != nil {
			t.Fatal(err)
		}
		moved, err := UpgradeActiveShape(site)
		if err != nil || moved != c.moved {
			t.Errorf("%s: moved=%v err=%v, want moved=%v", c.shape, moved, err, c.moved)
		}
		reg, _ := LoadRegistry(site)
		if reg.ActiveShape != "pub.polis.shapes.v5" {
			t.Errorf("%s: active_shape = %q after the upgrade, want pub.polis.shapes.v5", c.shape, reg.ActiveShape)
		}
	}

	site := storage.At(t.TempDir())
	if err := SetActiveShapeName(site, "v4"); err != nil {
		t.Fatal(err)
	}
	reg, _ := LoadRegistry(site)
	reg.SchemaVersion = CurrentRegistrySchemaVersion + 1
	if err := SaveRegistry(site, reg); err != nil {
		t.Fatal(err)
	}
	if moved, _ := UpgradeActiveShape(site); moved {
		t.Error("a registry from a newer schema was rewritten")
	}
}
