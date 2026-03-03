package cmd

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

func handleIndex(args []string) {
	dir := getDataDir()

	if !isPolisSite(dir) {
		exitError("Not a polis site directory")
	}

	indexPath := filepath.Join(dir, "content", "pub.polis.core", "index.jsonl")

	file, err := os.Open(indexPath)
	if err != nil {
		if os.IsNotExist(err) {
			if jsonOutput {
				outputJSON(map[string]interface{}{
					"status":  "success",
					"command": "index",
					"data": map[string]interface{}{
						"entries": []interface{}{},
						"count":   0,
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

	var entries []map[string]interface{}
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := scanner.Text()
		if line == "" {
			continue
		}

		var entry map[string]interface{}
		if err := json.Unmarshal([]byte(line), &entry); err != nil {
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
				"entries": entries,
				"count":   len(entries),
			},
		})
	} else {
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
