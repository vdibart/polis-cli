package site

import (
	"fmt"
	"regexp"
	"strings"
)

// Routine identity edits.
//
// ⭐ These exist so that setting a display name is a command, not file surgery.
// The two actor sites' names were set by hand on a production volume; that it
// needed `jq` and a truncate-in-place to keep ownership is the reason. Both go
// through LoadWellKnown → SaveWellKnown, the one lossless writer, and are shared
// by `polis site set` and the web app's settings handlers.

// MaxAuthorNameLen is the longest display name accepted, in bytes.
const MaxAuthorNameLen = 50

var avatarHexColor = regexp.MustCompile(`^#[0-9a-fA-F]{6}$`)

// AvatarPatterns are the pattern names an avatar may carry.
var AvatarPatterns = map[string]bool{
	"none": true, "rings": true, "cross": true, "grid": true,
	"dots": true, "stripes": true, "diamond": true, "halves": true,
}

// ValidateAuthorName trims name and checks it, returning the value to store.
func ValidateAuthorName(name string) (string, error) {
	name = strings.TrimSpace(name)
	if len(name) > MaxAuthorNameLen {
		return "", fmt.Errorf("display name must be %d characters or less", MaxAuthorNameLen)
	}
	return name, nil
}

// SetAuthorName sets author_name in .well-known/polis, keeping every other
// member exactly as it was.
//
// ⚠️ This changes .well-known/polis, which Judge snapshots: expect one
// judge.alert.wellknown_change per call.
func SetAuthorName(siteDir, name string) (string, error) {
	name, err := ValidateAuthorName(name)
	if err != nil {
		return "", err
	}
	wk, err := LoadWellKnown(siteDir)
	if err != nil {
		return "", fmt.Errorf("load .well-known/polis: %w", err)
	}
	wk.AuthorName = name
	if err := SaveWellKnown(siteDir, wk); err != nil {
		return "", err
	}
	return name, nil
}

// ValidateAvatar checks an avatar config. A nil config is valid: it means "no
// custom avatar".
func ValidateAvatar(a *AvatarConfig) error {
	if a == nil {
		return nil
	}
	for _, c := range []string{a.BG, a.FG} {
		if !avatarHexColor.MatchString(c) {
			return fmt.Errorf("invalid color %q: must be #RRGGBB hex", c)
		}
	}
	for _, c := range []string{a.Border, a.PatternColor} {
		if c != "" && !avatarHexColor.MatchString(c) {
			return fmt.Errorf("invalid color %q: must be #RRGGBB hex", c)
		}
	}
	if a.BorderW < 0 || a.BorderW > 3 {
		return fmt.Errorf("invalid border width %d: must be 0-3", a.BorderW)
	}
	if a.Pattern != "" && !AvatarPatterns[a.Pattern] {
		return fmt.Errorf("invalid pattern %q", a.Pattern)
	}
	return nil
}

// SetAvatar replaces the avatar block in .well-known/polis (nil removes it),
// keeping every other member, and regenerates favicon.svg from it.
//
// The favicon is a projection: a failure there is returned as a warning, not
// an error, because the identity document is already written.
func SetAvatar(siteDir string, a *AvatarConfig) (faviconErr error, err error) {
	if err := ValidateAvatar(a); err != nil {
		return nil, err
	}
	wk, err := LoadWellKnown(siteDir)
	if err != nil {
		return nil, fmt.Errorf("load .well-known/polis: %w", err)
	}
	wk.Avatar = a
	if err := SaveWellKnown(siteDir, wk); err != nil {
		return nil, err
	}
	return WriteFavicon(siteDir), nil
}
