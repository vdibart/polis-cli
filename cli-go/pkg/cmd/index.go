package cmd

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/vdibart/polis-cli/cli-go/pkg/index"
)

func handleIndex(args []string) {
	dir := getDataDir()

	if !isPolisSite(dir) {
		exitError("Not a polis site directory")
	}

	// Resolved through the bundle pointer, not assumed: `dir` and `mount` are
	// user-configurable, so a hardcoded content/pub.polis.core would be wrong
	// on any site that moved one.
	indexPath := index.IndexPath(dir)

	file, err := os.Open(indexPath)
	if err != nil {
		if os.IsNotExist(err) {
			if jsonOutput {
				outputJSON(map[string]interface{}{
					"status":  "success",
					"command": "index",
					"data": map[string]interface{}{
						"entries":       []interface{}{},
						"count":         0,
						"skipped":       0,
						"skipped_lines": []int{},
					},
				})
			} else {
				fmt.Println("[i] No posts indexed yet.")
			}
			return
		}
		exitError("Failed to open index: %v", err)
	}
	defer file.Close()

	// A line that does not parse is COUNTED and reported, never dropped in
	// silence: a listing of the parseable half is not a listing of the index
	// (Signet epic 44 C1, close-out F15).
	var entries []map[string]interface{}
	skippedLines := []int{}
	lineNo := 0
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		lineNo++
		line := scanner.Text()
		if strings.TrimSpace(line) == "" {
			continue
		}

		// A literal `null` parses without error into a nil map: valid JSON,
		// and not an entry. Counted, like anything else this cannot list.
		var entry map[string]interface{}
		if err := json.Unmarshal([]byte(line), &entry); err != nil || entry == nil {
			skippedLines = append(skippedLines, lineNo)
			continue
		}
		entries = append(entries, entry)
	}

	if err := scanner.Err(); err != nil {
		exitError("Failed to read index: %v", err)
	}

	if jsonOutput {
		outputJSON(map[string]interface{}{
			"status":  "success",
			"command": "index",
			"data": map[string]interface{}{
				"entries":       entries,
				"count":         len(entries),
				"skipped":       len(skippedLines),
				"skipped_lines": skippedLines,
			},
		})
	} else {
		if len(skippedLines) > 0 {
			fmt.Fprintf(os.Stderr, "[!] Skipped %d unreadable line(s) in %s: %v\n", len(skippedLines), indexPath, skippedLines)
		}
		if len(entries) == 0 {
			fmt.Println("[i] No posts indexed yet.")
			return
		}

		// Output JSONL (one entry per line) in human mode too
		for _, entry := range entries {
			data, _ := json.Marshal(entry)
			fmt.Println(string(data))
		}
	}
}
