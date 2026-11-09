package server

import (
	"context"
	"fmt"
	"io/fs"
	"path"
	"time"

	"github.com/vdibart/polis-cli/cli-go/pkg/bundle"
	"github.com/vdibart/polis-cli/cli-go/pkg/storage"
)

// DataDirStorage adapts a single-tenant site to the serve.Storage interface
// used by the shared static-content handler. Used by polis-server's --reader /
// --mirror modes (step 5.a) to serve rendered tenant content alongside the
// structured-query API, and by the editor surface for the site's own published
// files.
//
// Single-tenant means the handle parameter on each method is ignored; every
// read goes to the one site this server serves.
//
// ⭐ It reads through the SITE'S STORE, so a server whose site is not on local
// disk serves what that store holds. serve.Storage itself is unchanged: the
// hosted storage implements the same interface, with its own hardened read.
//
// ⚠️ A relative path that escapes the site is refused here now, by the store's
// key validation, where the directory join this replaced read whatever it
// named. The upstream handler (serve.ServeTenantPublic) still rejects
// traversal first; this is a second refusal, not the only one.
type DataDirStorage struct {
	Site storage.Site
}

// NewDataDirStorage returns a Storage adapter over site.
func NewDataDirStorage(site storage.Site) *DataDirStorage {
	return &DataDirStorage{Site: site}
}

// StatPublicFile stats a file relative to the site. handle is unused
// (single-tenant).
//
// ⚠️ serve.Storage returns an fs.FileInfo, which the store does not, so the
// store's Info is wrapped. The handler reads only IsDir from it.
func (s *DataDirStorage) StatPublicFile(_, relativePath string) (fs.FileInfo, error) {
	if err := s.usable(); err != nil {
		return nil, err
	}
	info, err := s.Site.Store().Stat(context.Background(), relativePath)
	if err != nil {
		return nil, err
	}
	return siteFileInfo{name: path.Base(relativePath), info: info}, nil
}

// ReadPublicFile reads a file relative to the site. handle is unused
// (single-tenant).
func (s *DataDirStorage) ReadPublicFile(_, relativePath string) ([]byte, error) {
	if err := s.usable(); err != nil {
		return nil, err
	}
	return s.Site.Store().Get(context.Background(), relativePath)
}

// LoadBundle returns the site's bundle.json or the default pub.polis.core
// bundle if the file isn't present. handle is unused.
func (s *DataDirStorage) LoadBundle(_ string) *bundle.Bundle {
	if b, err := bundle.LoadBundleAt(s.Site, "content/pub.polis.core/bundle.json"); err == nil {
		return b
	}
	return bundle.DefaultCoreBundle()
}

// usable refuses a zero site handle with a not-found error, so a server with
// no site serves 404s rather than dereferencing a nil store.
func (s *DataDirStorage) usable() error {
	if !s.Site.Valid() {
		return fmt.Errorf("no site to serve from: %w", fs.ErrNotExist)
	}
	return nil
}

// siteFileInfo presents a store's Info as an fs.FileInfo.
type siteFileInfo struct {
	name string
	info storage.Info
}

func (f siteFileInfo) Name() string       { return f.name }
func (f siteFileInfo) Size() int64        { return f.info.Size }
func (f siteFileInfo) ModTime() time.Time { return f.info.ModTime }
func (f siteFileInfo) IsDir() bool        { return f.info.IsDir }
func (f siteFileInfo) Sys() any           { return nil }
func (f siteFileInfo) Mode() fs.FileMode {
	if f.info.IsDir {
		return f.info.Mode | fs.ModeDir
	}
	return f.info.Mode
}
