package storage

import (
	"bytes"
	"context"
	"io"
	"os"
)

// OpenRead opens the value at key for a streaming read.
//
// ⭐ IT EXISTS FOR FILES THAT GROW WITHOUT BOUND (the feed cache, the
// notification state), whose readers scan line by line instead of reading
// the file whole. On a store with a real path it opens that file, so nothing
// changes for a filesystem: the bytes are streamed, never held. On a store with
// no filesystem there is no file to stream, so the value is read whole, which
// is the only thing such a store can do.
//
// Absence is fs.ErrNotExist on either branch (ErrNotFound wraps it), so a
// caller's "no file yet" check is unchanged.
func OpenRead(site Site, key string) (io.ReadCloser, error) {
	st := site.Store()
	if st.Capabilities().RealPath {
		path, err := st.RealPath(key)
		if err != nil {
			return nil, err
		}
		// NOT Get: this is the streaming read the function exists for.
		return os.Open(path)
	}
	data, err := st.Get(context.Background(), key)
	if err != nil {
		return nil, err
	}
	return io.NopCloser(bytes.NewReader(data)), nil
}
