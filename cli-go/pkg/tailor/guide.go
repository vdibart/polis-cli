package tailor

import (
	"regexp"
	"strings"
)

// GuidePath is the public upgrade guide: one manual recipe per check, so a
// person, or an AI assistant working with them, can pick which changes to make
// and in what order, and check each with a dry run.
const GuidePath = "docs/cli/user/upgrade-a-site.md"

var releaseVersion = regexp.MustCompile(`^\d+\.\d+\.\d+$`)

// GuideURL is the guide on GitHub, as of the release this binary was built
// from (its recipes match these checks), or as of main for a development build.
// A release binary's version is X.Y.Z+<source sha>; the public tag is vX.Y.Z.
func GuideURL(version string) string {
	ref := "main"
	version, _, _ = strings.Cut(strings.TrimPrefix(version, "v"), "+")
	if releaseVersion.MatchString(version) {
		ref = "v" + version
	}
	return "https://github.com/vdibart/polis-cli/blob/" + ref + "/" + GuidePath
}
