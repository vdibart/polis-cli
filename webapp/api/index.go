// Package handler is the Vercel function that serves a polis instance. Every
// path is rewritten to it (vercel.json); the instance itself is in vercelentry.
//
// ⚠️ EXPERIMENTAL, as vercelentry is: remote instances on Vercel are exploratory.
package handler

import (
	"net/http"

	"github.com/vdibart/polis-cli/webapp/vercelentry"
)

// Handler is the function's entry point, as Vercel's Go runtime requires.
func Handler(w http.ResponseWriter, r *http.Request) {
	vercelentry.Handler(w, r)
}
