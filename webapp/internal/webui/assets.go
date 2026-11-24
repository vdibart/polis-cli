package webui

import "embed"

// www is embedded as a directory (not the `www/*` glob) so files whose
// names start with `_` or `.` are skipped automatically: nothing dev-only
// placed there can reach a production build.
//
//go:embed www
var Assets embed.FS
