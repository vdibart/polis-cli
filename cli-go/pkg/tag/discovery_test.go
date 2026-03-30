package tag

import (
	"testing"
)

func TestTagTargetURL_Deterministic(t *testing.T) {
	base := "https://alice.polis.pub"

	url1 := TagTargetURL(base, "rust", "https://bob.com/posts/hello")
	url2 := TagTargetURL(base, "rust", "https://bob.com/posts/hello")

	if url1 != url2 {
		t.Errorf("Same inputs produced different URLs:\n  %s\n  %s", url1, url2)
	}
}

func TestTagTargetURL_DifferentTargets(t *testing.T) {
	base := "https://alice.polis.pub"

	url1 := TagTargetURL(base, "rust", "https://bob.com/posts/hello")
	url2 := TagTargetURL(base, "rust", "https://bob.com/posts/world")

	if url1 == url2 {
		t.Error("Different targets produced the same URL")
	}
}

func TestTagTargetURL_DifferentTags(t *testing.T) {
	base := "https://alice.polis.pub"
	target := "https://bob.com/posts/hello"

	url1 := TagTargetURL(base, "rust", target)
	url2 := TagTargetURL(base, "go", target)

	if url1 == url2 {
		t.Error("Different tags produced the same URL")
	}
}

func TestTagTargetURL_Format(t *testing.T) {
	url := TagTargetURL("https://alice.polis.pub", "rust", "https://bob.com/posts/hello")

	// Should start with base URL
	if url[:len("https://alice.polis.pub/tags/rust/")] != "https://alice.polis.pub/tags/rust/" {
		t.Errorf("URL format unexpected: %s", url)
	}

	// Hash portion should be 16 chars
	prefix := "https://alice.polis.pub/tags/rust/"
	hash := url[len(prefix):]
	if len(hash) != 16 {
		t.Errorf("Hash portion length = %d, want 16", len(hash))
	}
}

func TestTagTargetMetadata(t *testing.T) {
	meta := TagTargetMetadata("rust", "https://bob.com/posts/hello")

	if meta["tag"] != "rust" {
		t.Errorf("meta.tag = %q, want %q", meta["tag"], "rust")
	}
	if meta["target"] != "https://bob.com/posts/hello" {
		t.Errorf("meta.target = %q", meta["target"])
	}
	if len(meta) != 2 {
		t.Errorf("meta has %d keys, want 2", len(meta))
	}
}
