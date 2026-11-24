package server

import (
	"net/http"
	"strings"

	"github.com/vdibart/polis-cli/cli-go/pkg/blessing"
	polisurl "github.com/vdibart/polis-cli/cli-go/pkg/url"
)

// waitingByPost is the post author's own pending list, grouped by the post
// each request is on (its address normalised to .md): the comments waiting
// for this site's owner to bless or turn away.
//
// ⭐ ONE SOURCE FOR THE WAITING VIEW AND THE WAITING COUNT. "all comments on
// <post> to bless" and the waiting count on "all posts from me" both read
// this, so the number on a post's card and the list it opens cannot
// disagree.
//
// ⛔ Only the owner has waiting comments to see on their own site: asked by
// anyone else, the answer is empty (nil, nil), as "to bless" is in the
// stream. The discovery service answers the same question in full.
func (s *Server) waitingByPost(r *http.Request) (map[string][]blessing.IncomingRequest, error) {
	if s.DiscoveryURL == "" || !s.isOwnerReading(r) {
		return nil, nil
	}
	myDomain := extractDomainFromURL(s.GetBaseURL())
	if myDomain == "" {
		return nil, nil
	}
	pending, err := blessing.FetchPendingRequests(s.NewReadDSClient(r), myDomain)
	if err != nil {
		return nil, err
	}
	by := map[string][]blessing.IncomingRequest{}
	for _, p := range pending {
		post := polisurl.NormalizeToMD(p.InReplyTo)
		by[post] = append(by[post], p)
	}
	return by, nil
}

// stampWaiting sets each of the owner's own posts' waiting count, for the
// owner only. ⚠️ A failed pending read leaves every count out rather than
// stamping zeros, which would say nothing waits.
func (s *Server) stampWaiting(r *http.Request, items []streamItemResponse) {
	waiting, err := s.waitingByPost(r)
	if err != nil {
		s.LogWarn("stream items: pending read for waiting counts failed: %v", err)
		return
	}
	if len(waiting) == 0 {
		return
	}
	base := strings.TrimRight(s.GetBaseURL(), "/")
	for i := range items {
		if items[i].Type != "post" {
			continue
		}
		// ⚠️ An own post's item carries its site-relative path; a request
		// names the post by its full address.
		addr := items[i].URL
		if strings.HasPrefix(addr, "/") {
			addr = base + addr
		}
		items[i].Waiting = len(waiting[polisurl.NormalizeToMD(addr)])
	}
}
