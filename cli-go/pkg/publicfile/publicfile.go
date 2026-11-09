// Package publicfile is the one rule for which files of a site a reader may
// fetch: what the self-hosted serve handler answers, and what a remote
// instance's static deploy publishes.
//
// ⛔ THEY MUST AGREE, AND THAT IS WHY THIS IS A PACKAGE. The deploy once
// applied only the private-name denial, so anything else in the site
// directory (notes.txt, backup.tar.gz, site.sqlite, README, an old export zip)
// became public on Vercel although serve would never have answered it. A
// deploy is a publication, so it must not be wider than serving.
package publicfile

import (
	"path"
	"strings"

	"github.com/vdibart/polis-cli/cli-go/pkg/privatepath"
)

// ServedExtensions are the extensions a site's files are served with.
//
// `.js` is here because the v4 shape installs `/stream.js` at the site root;
// without it the stream controller never loads.
var ServedExtensions = map[string]bool{
	".html": true, ".css": true, ".js": true, ".json": true,
	".jsonl": true, ".md": true, ".svg": true, ".xml": true,
}

// RootFiles are files served at the site root by EXACT NAME, whatever their
// extension, because a protocol names them (robots.txt), with their type.
var RootFiles = map[string]string{
	"robots.txt": "text/plain; charset=utf-8",
}

// ExtensionlessServed reports whether an extensionless file may be served at
// key. ⭐ Only under .well-known/ (.well-known/polis): anywhere else an
// extensionless file is a key, a git internal or a dotfile far more often
// than it is content.
func ExtensionlessServed(key string) bool { return strings.HasPrefix(key, ".well-known/") }

// Servable reports whether the file at the site-relative key may be fetched
// by a reader. A private name in any segment is never servable.
func Servable(key string) bool {
	if key == "" || privatepath.Denied(key) {
		return false
	}
	if _, ok := RootFiles[key]; ok {
		return true
	}
	ext := path.Ext(key)
	if ext == "" {
		return ExtensionlessServed(key)
	}
	return ServedExtensions[ext]
}
