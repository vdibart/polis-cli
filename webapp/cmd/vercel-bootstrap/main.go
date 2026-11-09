// Command vercel-bootstrap is a polis instance as a Vercel function's
// executable: the "bootstrap" a provided.al2023 function runs.
//
// ⚠️ EXPERIMENTAL, as vercelentry is: remote instances on Vercel are
// exploratory.
//
// ⭐ It is what Vercel's Go builder generates around api/index.go, written
// down so a release can build the function with `go build` alone, without the
// Vercel CLI: the bridge turns each Lambda invocation into an http.Request
// for the instance's handler. cmd/vercel-function builds it.
package main

import (
	"net/http"

	"github.com/vercel/go-bridge/go/bridge"

	"github.com/vdibart/polis-cli/webapp/vercelentry"
)

func main() {
	bridge.Start(http.HandlerFunc(vercelentry.Handler))
}
