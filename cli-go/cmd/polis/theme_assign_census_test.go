package main

import (
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
)

// ⛔ THE THEME-ASSIGNMENT CENSUS. A reserved theme (sols, polis.pub's
// logged-out theme; stardust, why.polis.pub's) must never be given to a new or
// repaired site. Seven tenants got one before 2026-09-26, when a random pick
// could still land on them (5da36195).
//
// Every way code gives a site a theme is listed here with why it cannot hand
// out a reserved one. A NEW call site fails the build until it is added, so
// the question is asked of it. theme.SelectRandomTheme refusing reserved
// themes is pinned by TestSelectRandomThemeNeverPicksAReservedTheme; the
// switch's refusal by TestTheThemeSwitchRefusesAReservedTheme; bash's init by
// test_random_theme_never_reserved.
var themeAssigners = map[string]string{
	// theme.SelectRandomTheme: skips every reserved theme (theme.IsReserved).
	"cli-go/pkg/site/init.go SelectRandomTheme":              "polis init: random, never reserved",
	"cli-go/pkg/render/page.go SelectRandomTheme":            "the first render of a site with no theme: random, never reserved",
	"webapp/internal/hosted/first_post.go SelectRandomTheme": "hosted signup: random among the themes offered for v5, never reserved",
	"cli-go/pkg/medic/medic.go SelectRandomTheme":            "Medic's repair of a dangling or empty theme: random, never reserved",
	"cli-go/pkg/tailor/checks.go SelectRandomTheme":          "Tailor's repairs: random, never reserved",
	// bundle.SetActiveThemeName with a fixed, existing or typed name.
	"cli-go/pkg/site/init.go SetActiveThemeName":     `polis init --theme <name>: only what the person typed (cli-go/pkg/cmd/init.go), never chosen for them; an operator may put its own site on a reserved theme this way`,
	"cli-go/pkg/medic/medic.go SetActiveThemeName":   `the studio13 → studio13-nk rename, and qualifying the site's OWN bare theme name: keeps the site's theme, never chooses one`,
	"cli-go/pkg/tailor/checks.go SetActiveThemeName": `the same two, for a self-hosted site`,
}

var themeAssignCall = regexp.MustCompile(`\b(?:theme\.SelectRandomTheme|bundle\.SetActiveThemeName)\(`)

func TestEveryWayToGiveASiteAThemeIsReviewed(t *testing.T) {
	root := filepath.Join("..", "..", "..")
	found := map[string]bool{}
	for _, dir := range []string{"cli-go/pkg", "webapp/internal"} {
		_ = filepath.Walk(filepath.Join(root, dir), func(path string, info os.FileInfo, err error) error {
			if err != nil || info.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
				return nil
			}
			rel, _ := filepath.Rel(root, path)
			rel = filepath.ToSlash(rel)
			// The theme and bundle packages define these; their own calls are
			// the mechanism, not a caller.
			if strings.HasPrefix(rel, "cli-go/pkg/theme/") || strings.HasPrefix(rel, "cli-go/pkg/bundle/") {
				return nil
			}
			data, err := os.ReadFile(path)
			if err != nil {
				return nil
			}
			for _, m := range themeAssignCall.FindAllString(string(data), -1) {
				name := strings.TrimSuffix(m[strings.Index(m, ".")+1:], "(")
				found[rel+" "+name] = true
			}
			return nil
		})
	}
	var unreviewed, gone []string
	for k := range found {
		if _, ok := themeAssigners[k]; !ok {
			unreviewed = append(unreviewed, k)
		}
	}
	for k := range themeAssigners {
		if file, _, _ := strings.Cut(k, " "); closedAndAbsent(file) {
			continue // the public repository: Medic and the hosted service do not ship
		}
		if !found[k] {
			gone = append(gone, k)
		}
	}
	sort.Strings(unreviewed)
	sort.Strings(gone)
	for _, k := range unreviewed {
		t.Errorf("%s gives a site a theme and is not in themeAssigners: say why it can never hand out sols or stardust", k)
	}
	for _, k := range gone {
		t.Errorf("themeAssigners lists %s, which no longer exists: remove it", k)
	}
}
