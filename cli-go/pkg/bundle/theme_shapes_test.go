package bundle

import "testing"

// A site created before the stream shape existed declares its core themes as
// blog-only. UnionThemeShapes brings each up to what the defaults declare,
// keeps a shape the site added itself, touches no other field, and a second
// pass changes nothing.
func TestUnionThemeShapes_PreStreamBundle(t *testing.T) {
	b := DefaultCoreBundle()
	for name, th := range b.Themes {
		th.CompatibleShapes = []string{"pub.polis.shapes.v3"}
		if name == "vice" {
			th.CompatibleShapes = append(th.CompatibleShapes, "org.example.shapes.zine")
			th.Version = "0.9.0"
		}
	}
	b.Themes["org.example.custom"] = &Theme{Name: "org.example.custom", Version: "1.0.0", CSS: "c.css",
		CompatibleShapes: []string{"pub.polis.shapes.v3"}}

	defaults := DefaultCoreBundle()
	changed := b.UnionThemeShapes(defaults)
	if len(changed) == 0 {
		t.Fatal("expected themes to change")
	}
	for name, def := range defaults.Themes {
		for _, s := range def.CompatibleShapes {
			if !containsString(b.Themes[name].CompatibleShapes, s) {
				t.Errorf("%s: compatible_shapes = %v, missing %s", name, b.Themes[name].CompatibleShapes, s)
			}
		}
		if !containsString(b.Themes[name].CompatibleShapes, "pub.polis.shapes.v3") {
			t.Errorf("%s lost pub.polis.shapes.v3 — the union never removes", name)
		}
	}
	if !containsString(b.Themes["vice"].CompatibleShapes, "org.example.shapes.zine") {
		t.Error("vice lost the site's own shape")
	}
	if b.Themes["vice"].Version != "0.9.0" {
		t.Error("the union changed a field other than compatible_shapes")
	}
	if got := b.Themes["org.example.custom"].CompatibleShapes; len(got) != 1 {
		t.Errorf("a theme the defaults do not declare was touched: %v", got)
	}
	if again := b.UnionThemeShapes(defaults); len(again) != 0 {
		t.Errorf("second pass changed %v; want nothing", again)
	}
}

func TestUnionThemeShapes_NoOpOnDefaults(t *testing.T) {
	if changed := DefaultCoreBundle().UnionThemeShapes(DefaultCoreBundle()); len(changed) != 0 {
		t.Errorf("default bundle changed: %v", changed)
	}
}
