package webui

import "embed"

// www is embedded as a directory (not the `www/*` glob) so files whose
// names start with `_` or `.` are skipped automatically — that is how
// `_pql_test.html` (an in-browser dev-only PQL parser test runner) is
// kept out of production builds.
//
//go:embed www
var Assets embed.FS
