package server

import (
	"io/fs"
	"os"
	"path/filepath"

	"github.com/vdibart/polis-cli/cli-go/pkg/bundle"
)

// DataDirStorage adapts a single-tenant data directory to the
// serve.Storage interface used by the shared static-content handler.
// Used by polis-server's --reader / --mirror modes (step 5.a) to serve
// rendered tenant content alongside the structured-query API.
//
// Single-tenant means the handle parameter on each method is ignored;
// the storage resolves all paths against the configured DataDir.
type DataDirStorage struct {
	DataDir string
}

// NewDataDirStorage returns a Storage adapter rooted at dataDir.
func NewDataDirStorage(dataDir string) *DataDirStorage {
	return &DataDirStorage{DataDir: dataDir}
}

// StatPublicFile stats a file relative to the data directory. handle
// is unused (single-tenant).
func (s *DataDirStorage) StatPublicFile(_ , relativePath string) (fs.FileInfo, error) {
	return os.Stat(filepath.Join(s.DataDir, relativePath))
}

// ReadPublicFile reads a file relative to the data directory. handle
// is unused (single-tenant).
func (s *DataDirStorage) ReadPublicFile(_, relativePath string) ([]byte, error) {
	return os.ReadFile(filepath.Join(s.DataDir, relativePath))
}

// LoadBundle returns the data dir's bundle.json or the default
// pub.polis.core bundle if the file isn't present. handle is unused.
func (s *DataDirStorage) LoadBundle(_ string) *bundle.Bundle {
	bundlePath := filepath.Join(s.DataDir, "content", "pub.polis.core", "bundle.json")
	if b, err := bundle.LoadBundle(bundlePath); err == nil {
		return b
	}
	return bundle.DefaultCoreBundle()
}
