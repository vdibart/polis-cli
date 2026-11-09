package cmd

import "testing"

// Go's flag package stops at the first positional argument, so a flag typed
// after the path used to be ignored and the prompt still appeared. Both
// spellings must work on either side of the path.
func TestUnpublishYesFlagBeforeOrAfterPath(t *testing.T) {
	const path = "content/pub.polis.core/post/20260201/my-post.md"
	cases := []struct {
		name string
		args []string
		yes  bool
	}{
		{"no flag", []string{path}, false},
		{"-y before path", []string{"-y", path}, true},
		{"-y after path", []string{path, "-y"}, true},
		{"--yes before path", []string{"--yes", path}, true},
		{"--yes after path", []string{path, "--yes"}, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := parseUnpublishArgs(tc.args)
			if err != nil {
				t.Fatalf("parseUnpublishArgs(%q): %v", tc.args, err)
			}
			if got.yes != tc.yes {
				t.Errorf("yes = %v, want %v", got.yes, tc.yes)
			}
			if len(got.paths) != 1 || got.paths[0] != path {
				t.Errorf("paths = %q, want [%q]", got.paths, path)
			}
		})
	}
}

func TestUnpublishFlagsAfterPathKeepValues(t *testing.T) {
	got, err := parseUnpublishArgs([]string{"--url", "https://you.example/x.md", "--type", "pub.polis.comment", "--yes"})
	if err != nil {
		t.Fatal(err)
	}
	if got.url != "https://you.example/x.md" || got.typ != "pub.polis.comment" || !got.yes {
		t.Errorf("got %+v", got)
	}
}

// After "--" everything is a path, even something that looks like a flag.
func TestUnpublishDoubleDashEndsFlags(t *testing.T) {
	got, err := parseUnpublishArgs([]string{"--", "-y"})
	if err != nil {
		t.Fatal(err)
	}
	if got.yes {
		t.Error("-y after -- must be a path, not the flag")
	}
	if len(got.paths) != 1 || got.paths[0] != "-y" {
		t.Errorf("paths = %q, want [\"-y\"]", got.paths)
	}
}

func TestUnpublishUnknownFlagIsAnError(t *testing.T) {
	if _, err := parseUnpublishArgs([]string{"content/pub.polis.core/post/x.md", "--nope"}); err == nil {
		t.Error("an unknown flag after the path must be rejected, not taken as a path")
	}
}
