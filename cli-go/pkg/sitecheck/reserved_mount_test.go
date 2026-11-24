package sitecheck

import (
	"strings"
	"testing"
)

// polis validate reports a bundle.json already on disk with a type mounted
// under the owner's interface (/_), which writers now refuse.
func TestValidateReportsAMountUnderTheOwnersInterface(t *testing.T) {
	bad := []byte(`{"name":"pub.polis.core","version":"1","types":{"pub.polis.post":{"dir":"post","mount":"/_/posts"}}}`)
	st := BundleJSONBytes(bad, nil)
	if st.OK || !strings.Contains(st.Message, "/_/posts") {
		t.Errorf("a mount under /_ passed: %+v", st)
	}
	good := []byte(`{"name":"pub.polis.core","version":"1","types":{"pub.polis.post":{"dir":"post","mount":"/posts"}}}`)
	if st := BundleJSONBytes(good, nil); !st.OK {
		t.Errorf("an ordinary mount failed: %+v", st)
	}
}
