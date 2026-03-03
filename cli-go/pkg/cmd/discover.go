package cmd

import (
	"flag"
	"fmt"
	"strings"

	"github.com/vdibart/polis-cli/cli-go/pkg/discovery"
	"github.com/vdibart/polis-cli/cli-go/pkg/feed"
	"github.com/vdibart/polis-cli/cli-go/pkg/following"
)

func handleDiscover(args []string) {
	fs := flag.NewFlagSet("discover", flag.ExitOnError)
	specificAuthor := fs.String("author", "", "Check a specific author")
	fs.Parse(args)

	dir := getDataDir()

	if !isPolisSite(dir) {
		exitError("Not a polis site directory")
	}

	if discoveryURL == "" {
		exitError("Discovery service not configured (set DISCOVERY_SERVICE_URL)")
	}

	myDomain := discovery.ExtractDomainFromURL(baseURL)

	// Load following list
	followingPath := following.DefaultPath(dir)
	f, err := following.Load(followingPath)
	if err != nil {
		exitError("Failed to load following.json: %v", err)
	}

	if f.Count() == 0 {
		if !jsonOutput {
			fmt.Println("[i] Not following any authors. Use 'polis follow <url>' to follow someone.")
		}
		if jsonOutput {
			outputJSON(map[string]interface{}{
				"status":  "success",
				"command": "discover",
				"data": map[string]interface{}{
					"authors_checked": 0,
					"total_new_items": 0,
					"items":           []interface{}{},
				},
			})
		}
		return
	}

	// Build list of followed domains
	var domains []string
	for _, entry := range f.All() {
		d := discovery.ExtractDomainFromURL(entry.URL)
		if d != "" {
			// If specific author requested, only include that one
			if *specificAuthor != "" {
				specificDomain := discovery.ExtractDomainFromURL(*specificAuthor)
				if d != specificDomain {
					continue
				}
			}
			domains = append(domains, d)
		}
	}

	if len(domains) == 0 {
		if *specificAuthor != "" {
			exitError("Not following %s", *specificAuthor)
		}
		exitError("No valid domains in following list")
	}

	// Determine discovery domain for cache scoping
	discoveryDomain := discovery.ExtractDomainFromURL(discoveryURL)
	if discoveryDomain == "" {
		discoveryDomain = "default"
	}

	// Load feed cursor
	cm := feed.NewCacheManager(dir, discoveryDomain)
	cursor, _ := cm.GetCursor()

	// Query DS stream
	client := discovery.NewClient(discoveryURL, discoveryKey)
	typeFilter := "pub.polis.post.published,pub.polis.post.republished,pub.polis.comment.published,pub.polis.comment.republished"
	actorFilter := discovery.JoinDomains(domains)

	result, err := client.StreamQuery(cursor, 1000, typeFilter, actorFilter, "")
	if err != nil {
		exitError("Failed to query discovery stream: %v", err)
	}

	// Transform events to feed items
	handler := &feed.FeedHandler{
		MyDomain:        myDomain,
		FollowedDomains: make(map[string]bool, len(domains)),
	}
	for _, d := range domains {
		handler.FollowedDomains[d] = true
	}

	items := handler.Process(result.Events)

	// Merge into cache
	newCount := 0
	if len(items) > 0 {
		newCount, err = cm.MergeItems(items)
		if err != nil {
			exitError("Failed to merge feed items: %v", err)
		}
	}

	// Update cursor
	if result.Cursor != "" && result.Cursor != cursor {
		_ = cm.SetCursor(result.Cursor)
	}

	if !jsonOutput {
		fmt.Printf("[i] Checking %d followed author(s)...\n\n", len(domains))

		// Print items grouped by author
		if len(items) > 0 {
			currentAuthor := ""
			for _, item := range items {
				if item.AuthorURL != currentAuthor {
					currentAuthor = item.AuthorURL
					fmt.Printf("\n%s\n", currentAuthor)
				}
				typeLabel := strings.ToUpper(item.Type)
				dateStr := item.Published
				if len(dateStr) > 10 {
					dateStr = dateStr[:10]
				}
				fmt.Printf("  [%s] %s - %s\n", typeLabel, item.Title, dateStr)
			}
		}

		fmt.Println()
		if newCount > 0 {
			fmt.Printf("[✓] Found %d new item(s)\n", newCount)
		} else {
			fmt.Println("[i] No new content from followed authors")
		}
	}

	if jsonOutput {
		var jsonItems []map[string]interface{}
		for _, item := range items {
			jsonItems = append(jsonItems, map[string]interface{}{
				"author":    item.AuthorURL,
				"type":      item.Type,
				"title":     item.Title,
				"url":       item.URL,
				"published": item.Published,
			})
		}

		outputJSON(map[string]interface{}{
			"status":  "success",
			"command": "discover",
			"data": map[string]interface{}{
				"authors_checked": len(domains),
				"total_new_items": newCount,
				"items":           jsonItems,
			},
		})
	}
}
