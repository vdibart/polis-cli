package render

import (
	"strings"
	"testing"
)

func TestAvatarHTML_DefaultNoConfig(t *testing.T) {
	got := AvatarHTML(AvatarConfig{}, "D", 0)
	want := `<span class="avatar-initial">D</span>`
	if got != want {
		t.Errorf("default avatar: want %q, got %q", want, got)
	}
}

func TestAvatarHTML_DefaultWithSize(t *testing.T) {
	got := AvatarHTML(AvatarConfig{}, "D", 28)
	for _, sub := range []string{`class="avatar-initial"`, "width:28px;height:28px;line-height:28px;", ">D</span>"} {
		if !strings.Contains(got, sub) {
			t.Errorf("sized default avatar missing %q, got: %s", sub, got)
		}
	}
}

func TestAvatarHTML_BGFGAndInitial(t *testing.T) {
	got := AvatarHTML(AvatarConfig{BG: "#3a9e8a", FG: "#ffffff"}, "a", 0)
	for _, sub := range []string{"background-color:#3a9e8a", "color:#ffffff", ">a</span>"} {
		if !strings.Contains(got, sub) {
			t.Errorf("missing %q, got: %s", sub, got)
		}
	}
}

func TestAvatarHTML_Border(t *testing.T) {
	got := AvatarHTML(AvatarConfig{BG: "#000", FG: "#fff", Border: "#c8a85f", BorderW: 2}, "v", 0)
	if !strings.Contains(got, "border:2px solid #c8a85f") {
		t.Errorf("expected border, got: %s", got)
	}
	// Border ignored when width is 0.
	got2 := AvatarHTML(AvatarConfig{BG: "#000", FG: "#fff", Border: "#c8a85f", BorderW: 0}, "v", 0)
	if strings.Contains(got2, "border:") {
		t.Errorf("expected no border at width 0, got: %s", got2)
	}
}

func TestAvatarHTML_PatternBlanksInitial(t *testing.T) {
	got := AvatarHTML(AvatarConfig{BG: "#3a9e8a", FG: "#fff", Pattern: "dots", PatternColor: "#5fc0ac"}, "a", 28)
	if !strings.Contains(got, "background-image:url(data:image/svg+xml;base64,") {
		t.Errorf("expected pattern background-image, got: %s", got)
	}
	if !strings.Contains(got, `></span>`) {
		t.Errorf("expected blanked initial with a pattern, got: %s", got)
	}
}

func TestAvatarHTML_PatternNoneKeepsInitial(t *testing.T) {
	got := AvatarHTML(AvatarConfig{BG: "#3a9e8a", FG: "#fff", Pattern: "none", PatternColor: "#5fc0ac"}, "a", 0)
	if strings.Contains(got, "background-image") {
		t.Errorf("pattern=none should not add a background-image, got: %s", got)
	}
	if !strings.Contains(got, ">a</span>") {
		t.Errorf("pattern=none should keep the initial, got: %s", got)
	}
}

func TestAvatarHTML_UnknownPatternKeepsInitial(t *testing.T) {
	got := AvatarHTML(AvatarConfig{BG: "#3a9e8a", FG: "#fff", Pattern: "spirograph", PatternColor: "#5fc0ac"}, "a", 0)
	if strings.Contains(got, "background-image") {
		t.Errorf("unknown pattern should not add a background-image, got: %s", got)
	}
	if !strings.Contains(got, ">a</span>") {
		t.Errorf("unknown pattern should keep the initial, got: %s", got)
	}
}

func TestAvatarHTML_AllPatternsRender(t *testing.T) {
	for name := range avatarPatterns {
		got := AvatarHTML(AvatarConfig{BG: "#4a6ea0", FG: "#fff", Pattern: name, PatternColor: "#7e9cc4"}, "x", 28)
		if !strings.Contains(got, "background-image:url(data:image/svg+xml;base64,") {
			t.Errorf("pattern %q did not render a background-image: %s", name, got)
		}
	}
}

func TestAvatarHTML_EscapesInitial(t *testing.T) {
	got := AvatarHTML(AvatarConfig{}, "<", 0)
	if strings.Contains(got, "><") && !strings.Contains(got, "&lt;") {
		t.Errorf("expected escaped initial, got: %s", got)
	}
}

func TestLoadOrFetchAuthorAvatar_CachesAndNegativeCaches(t *testing.T) {
	orig := fetchAuthorAvatar
	defer func() { fetchAuthorAvatar = orig }()
	authorAvatarMu.Lock()
	authorAvatarCache = map[string]authorAvatarEntry{}
	authorAvatarMu.Unlock()

	var calls int
	fetchAuthorAvatar = func(domain string) (AvatarConfig, bool) {
		calls++
		if domain == "ok.example" {
			return AvatarConfig{BG: "#123456", FG: "#ffffff"}, true
		}
		return AvatarConfig{}, false
	}

	cfg, ok := LoadOrFetchAuthorAvatar("ok.example")
	if !ok || cfg.BG != "#123456" {
		t.Fatalf("want cache miss → hit, got %+v ok=%v", cfg, ok)
	}
	if _, ok := LoadOrFetchAuthorAvatar("ok.example"); !ok {
		t.Fatal("want cached hit")
	}
	if calls != 1 {
		t.Errorf("expected 1 fetch (second served from cache), got %d", calls)
	}

	if _, ok := LoadOrFetchAuthorAvatar("dead.example"); ok {
		t.Error("expected ok=false for failed fetch")
	}
	LoadOrFetchAuthorAvatar("dead.example") // should be negative-cached
	if calls != 2 {
		t.Errorf("expected 2 fetches (failure negative-cached), got %d", calls)
	}

	if _, ok := LoadOrFetchAuthorAvatar(""); ok {
		t.Error("empty domain should return ok=false without fetching")
	}
}

func TestFallbackAvatarConfig_Deterministic(t *testing.T) {
	a := FallbackAvatarConfig("discover.polis.pub")
	if a != FallbackAvatarConfig("discover.polis.pub") {
		t.Error("fallback config should be deterministic for a domain")
	}
	if a.FG != "#ffffff" {
		t.Errorf("want white fg, got %q", a.FG)
	}
	if !strings.HasPrefix(a.BG, "hsl(") {
		t.Errorf("want hsl() bg, got %q", a.BG)
	}
	if FallbackAvatarConfig("mayoinmotion.polis.pub").BG == a.BG {
		t.Error("distinct domains should produce distinct hues here")
	}
}
