package server

import (
	"net/http"
	"time"
)

// NewHTTPServer is the http.Server every polis listener uses: the hosted
// service and the self-hosted one.
//
// ⭐ ReadHeaderTimeout is the point: http.ListenAndServe sets no timeout at
// all, so a client that sends its headers one byte at a time holds a
// connection (and its goroutine) open forever. IdleTimeout closes kept-alive
// connections nobody is using.
//
// ⛔ NO WriteTimeout and no ReadTimeout. A WriteTimeout would cut a large
// download (the site's archive) mid-stream, and a ReadTimeout a slow but
// legitimate upload. Body sizes are bounded per route instead (limitBody).
func NewHTTPServer(addr string, h http.Handler) *http.Server {
	return &http.Server{
		Addr:              addr,
		Handler:           h,
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       120 * time.Second,
	}
}
