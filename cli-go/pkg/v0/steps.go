package v0

import (
	"encoding/json"
	"regexp"
	"strings"
	"time"
)

// Step is one thing v0 did while answering, read from a message's
// experimental_content: a tool it called, a tool it looked up, or a thought.
//
// ⚠️ v0 DOCUMENTS experimental_content AS UNSTABLE ("may change"). Steps reads
// what it recognises and skips the rest, so a change of shape costs the steps,
// never the message: Content is still there.
type Step struct {
	Kind string `json:"kind"` // "tool", "search" or "thinking"
	// Label is v0's own short description ("Applied autumn palette").
	Label string `json:"label,omitempty"`
	// Tool is the tool's name as the MCP server declared it.
	Tool string `json:"tool,omitempty"`
	// Reason is v0's one-line reason for calling the tool.
	Reason string `json:"reason,omitempty"`
	// Input is the arguments v0 sent.
	Input    json.RawMessage `json:"input,omitempty"`
	Thought  string          `json:"thought,omitempty"`
	Started  string          `json:"started,omitempty"`
	Finished bool            `json:"finished"`
}

// Steps reads the steps of a message, in order. A message whose content it
// does not recognise has none.
func (m Message) Steps() []Step {
	// Shape, measured 2026-10-18: [[0, [node, ...]]], where a node is
	// ["AssistantMessageContentPart", {"part": {...}}] or ["p", {}, ...].
	var outer []json.RawMessage
	if json.Unmarshal(m.Experimental, &outer) != nil {
		return nil
	}
	var steps []Step
	for _, block := range outer {
		var pair []json.RawMessage
		if json.Unmarshal(block, &pair) != nil || len(pair) < 2 {
			continue
		}
		var nodes []json.RawMessage
		if json.Unmarshal(pair[1], &nodes) != nil {
			continue
		}
		for _, n := range nodes {
			if s, ok := stepOf(n); ok {
				steps = append(steps, s)
			}
		}
	}
	return steps
}

func stepOf(node json.RawMessage) (Step, bool) {
	var parts []json.RawMessage
	if json.Unmarshal(node, &parts) != nil || len(parts) < 2 {
		return Step{}, false
	}
	var tag string
	if json.Unmarshal(parts[0], &tag) != nil || tag != "AssistantMessageContentPart" {
		return Step{}, false
	}
	var body struct {
		Part struct {
			Type             string `json:"type"`
			TaskNameActive   string `json:"taskNameActive"`
			TaskNameComplete string `json:"taskNameComplete"`
			ToolDisplayName  string `json:"toolDisplayName"`
			CreatedAt        int64  `json:"createdAt"`
			FinishedAt       int64  `json:"finishedAt"`
			Parts            []struct {
				Type             string          `json:"type"`
				Input            json.RawMessage `json:"input"`
				PermissionPrompt string          `json:"permissionPrompt"`
				Thought          string          `json:"thought"`
			} `json:"parts"`
		} `json:"part"`
	}
	if json.Unmarshal(parts[1], &body) != nil {
		return Step{}, false
	}
	p := body.Part
	s := Step{Label: p.TaskNameComplete, Finished: p.FinishedAt > 0}
	if s.Label == "" || !s.Finished {
		s.Label = p.TaskNameActive
	}
	if p.CreatedAt > 0 {
		s.Started = time.UnixMilli(p.CreatedAt).UTC().Format(time.RFC3339Nano)
	}
	switch p.Type {
	case "task-dynamic-tool-v1":
		s.Kind, s.Tool = "tool", p.ToolDisplayName
		for _, q := range p.Parts {
			if q.Type == "starting-dynamic-tool-execution" {
				s.Input, s.Reason = q.Input, q.PermissionPrompt
			}
		}
	case "task-search-tools-v1":
		s.Kind = "search"
	case "task-thinking-v1":
		s.Kind = "thinking"
		for _, q := range p.Parts {
			if q.Thought != "" {
				s.Thought = q.Thought
			}
		}
	default:
		return Step{}, false
	}
	return s, true
}

// Text is the message's prose as paragraphs, one per text block v0 wrote,
// joined by a blank line. ⚠️ v0's own content runs the blocks together with
// nothing between them ("…shortcuts firstI added…"), and a block's sentences
// can touch too ("…tools.I'll…"), so both get a break. A message whose
// content it does not recognise reads as its Content.
func (m Message) Text() string {
	var outer []json.RawMessage
	if json.Unmarshal(m.Experimental, &outer) != nil {
		return m.Content
	}
	var paras []string
	for _, block := range outer {
		var pair []json.RawMessage
		if json.Unmarshal(block, &pair) != nil || len(pair) < 2 {
			continue
		}
		var nodes [][]json.RawMessage
		if json.Unmarshal(pair[1], &nodes) != nil {
			continue
		}
		for _, n := range nodes {
			var tag string
			if len(n) < 3 || json.Unmarshal(n[0], &tag) != nil || tag != "p" {
				continue
			}
			var b strings.Builder
			for _, child := range n[2:] {
				collectText(child, &b)
			}
			if t := strings.TrimSpace(touchingSentences.ReplaceAllString(b.String(), "$1 $2")); t != "" {
				paras = append(paras, t)
			}
		}
	}
	if len(paras) == 0 {
		return m.Content
	}
	return strings.Join(paras, "\n\n")
}

// touchingSentences is a sentence's end with the next one's start against it.
var touchingSentences = regexp.MustCompile(`([a-z][.!?])([A-Z])`)

// collectText appends the text in one node of v0's tree: a string, or
// [tag, attrs, children...].
func collectText(node json.RawMessage, b *strings.Builder) {
	var s string
	if json.Unmarshal(node, &s) == nil {
		b.WriteString(s)
		return
	}
	var parts []json.RawMessage
	if json.Unmarshal(node, &parts) != nil || len(parts) < 3 {
		return
	}
	for _, child := range parts[2:] {
		collectText(child, b)
	}
}
