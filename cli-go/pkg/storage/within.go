package storage

import (
	"path"
	"strings"
)

// Within reports whether key, once cleaned, sits strictly beneath prefix.
//
// ⛔ THE LAST CHECK BEFORE A KEY BUILT FROM FOREIGN INPUT ADDRESSES THE STORE.
// The store refuses a key that leaves the site root, but a key that walks to
// ANOTHER place inside the site (`content/x/../../.polis/...`) is a valid key,
// so the store cannot catch it. A caller that derived a key from a comment URL,
// an event or a peer's data proves it is still under the prefix it intended.
func Within(key, prefix string) bool {
	k := strings.TrimPrefix(path.Clean("/"+key), "/")
	p := strings.Trim(path.Clean("/"+prefix), "/")
	if p == "" || k == p {
		return false
	}
	// ⭐ The cleaned key must equal the raw key, too: a key that only lands
	// under the prefix after '..' resolves was built by walking.
	return strings.HasPrefix(k, p+"/") && k == strings.TrimPrefix(key, "/")
}
