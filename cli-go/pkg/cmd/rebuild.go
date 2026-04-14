package cmd

import (
	"flag"
	"fmt"
	"os"

	"github.com/vdibart/polis-cli/cli-go/pkg/index"
)

func handleRebuild(args []string) {
	fs := flag.NewFlagSet("rebuild", flag.ExitOnError)
	rebuildPosts := fs.Bool("posts", false, "Rebuild posts index")
	rebuildComments := fs.Bool("comments", false, "Rebuild comments index")
	rebuildNotifications := fs.Bool("notifications", false, "Clear notifications")
	rebuildAll := fs.Bool("all", false, "Rebuild everything")
	fs.Parse(args)

	// If nothing specified, show usage
	if !*rebuildPosts && !*rebuildComments && !*rebuildNotifications && !*rebuildAll {
		exitError("Usage: polis rebuild --posts|--comments|--notifications|--all")
	}

	dir := getDataDir()

	if !isPolisSite(dir) {
		exitError("Not a polis site directory")
	}

	baseURL := os.Getenv("POLIS_BASE_URL")

	opts := index.RebuildOptions{
		Posts:         *rebuildPosts || *rebuildAll,
		Comments:      *rebuildComments || *rebuildAll,
		Notifications: *rebuildNotifications || *rebuildAll,
		All:           *rebuildAll,
		Generator:     generator,
	}

	result, err := index.RebuildIndex(dir, baseURL, opts)
	if err != nil {
		exitError("Failed to rebuild: %v", err)
	}

	if jsonOutput {
		outputJSON(map[string]interface{}{
			"status":  "success",
			"command": "rebuild",
			"data": map[string]interface{}{
				"posts_rebuilt":         result.PostsRebuilt,
				"comments_rebuilt":      result.CommentsRebuilt,
				"notifications_cleared": result.NotificationsCleared,
			},
		})
	} else {
		fmt.Println("[✓] Rebuild complete!")
		if opts.Posts || opts.All {
			fmt.Printf("  Posts indexed: %d\n", result.PostsRebuilt)
		}
		if opts.Comments || opts.All {
			fmt.Printf("  Comments indexed: %d\n", result.CommentsRebuilt)
		}
		if opts.Notifications || opts.All {
			fmt.Printf("  Notifications cleared: %d\n", result.NotificationsCleared)
		}
	}
}
