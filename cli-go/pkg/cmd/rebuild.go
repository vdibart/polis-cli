package cmd

import (
	"flag"
	"fmt"
	"sort"
	"strings"

	"github.com/vdibart/polis-cli/cli-go/pkg/index"
)

func handleRebuild(args []string) {
	fs := flag.NewFlagSet("rebuild", flag.ExitOnError)
	rebuildPosts := fs.Bool("posts", false, "Rebuild the post entries in the content index")
	rebuildComments := fs.Bool("comments", false, "Rebuild the comment entries in the content index and reconcile blessed.json")
	rebuildTags := fs.Bool("tags", false, "Rebuild the tag entries in the content index")
	rebuildAttestations := fs.Bool("attestations", false, "Rebuild the attestation entries in the content index")
	rebuildAll := fs.Bool("all", false, "Rebuild every content type")
	// DEPRECATED. Clearing notifications reconstructs nothing — it is a delete.
	// Kept accepted so no one's script breaks; it prints a pointer to the verb
	// that owns it now. Signet epic 25 D5.
	rebuildNotifications := fs.Bool("notifications", false, "Deprecated: use `polis notifications clear`")
	fs.Parse(args)

	// If nothing specified, show usage
	if !*rebuildPosts && !*rebuildComments && !*rebuildTags && !*rebuildAttestations &&
		!*rebuildNotifications && !*rebuildAll {
		exitError("Usage: polis rebuild --posts|--comments|--tags|--attestations|--all")
	}

	dir := getDataDir()

	if !isPolisSite(dir) {
		exitError("Not a polis site directory")
	}

	if *rebuildNotifications && !jsonOutput {
		fmt.Println("[!] `polis rebuild --notifications` is deprecated — it clears state rather than rebuilding it.")
		fmt.Println("    Use `polis notifications clear` instead.")
	}

	opts := index.RebuildOptions{
		Posts:         *rebuildPosts,
		Comments:      *rebuildComments,
		Tags:          *rebuildTags,
		Attestations:  *rebuildAttestations,
		Notifications: *rebuildNotifications,
		All:           *rebuildAll,
		Generator:     generator,
	}

	result, err := index.RebuildIndex(dir, opts)
	if err != nil {
		exitError("Failed to rebuild: %v", err)
	}

	if jsonOutput {
		data := map[string]interface{}{
			"posts_rebuilt":         result.PostsRebuilt,
			"comments_rebuilt":      result.CommentsRebuilt,
			"notifications_cleared": result.NotificationsCleared,
		}
		if ci := result.ContentIndex; ci != nil {
			data["content_index"] = ci
		}
		outputJSON(map[string]interface{}{
			"status":  "success",
			"command": "rebuild",
			"data":    data,
		})
		return
	}

	fmt.Println("[✓] Rebuild complete!")
	if ci := result.ContentIndex; ci != nil {
		fmt.Printf("  Content index: %d entries\n", ci.Total)
		// ⚠️ Say what was PRESERVED as well as what was rebuilt. A silent
		// "8 posts rebuilt" is what let R24-9's comment loss hide.
		printCounts("    rebuilt", ci.Rebuilt)
		printCounts("    preserved", ci.Preserved)
		printCounts("    skipped", ci.Skipped)
	}
	if opts.Comments || opts.All {
		fmt.Printf("  Blessed comments: %d\n", result.CommentsRebuilt)
	}
	if opts.Notifications {
		fmt.Printf("  Notifications cleared: %d\n", result.NotificationsCleared)
	}
}

// printCounts renders a per-entry-type count map in a stable order, or nothing
// when it is empty.
func printCounts(label string, counts map[string]int) {
	if len(counts) == 0 {
		return
	}
	types := make([]string, 0, len(counts))
	for t := range counts {
		types = append(types, t)
	}
	sort.Strings(types)

	parts := make([]string, 0, len(types))
	for _, t := range types {
		parts = append(parts, fmt.Sprintf("%d %s", counts[t], t))
	}
	fmt.Printf("%s: %s\n", label, strings.Join(parts, ", "))
}
