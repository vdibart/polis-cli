package agent

import "strings"

// Text is the one friendly description of Rosie (epic 11 D11).
//
// ⛔ ONE SOURCE. `polis init` prints Plain(); Settings → Rosie renders the same
// fields, served by the web app's settings API. Two copies would drift, and the
// promise "you were told what the switch controls" rests on what was shown.
//
// ⛔ NEVER THE WORDS grant, disclosure OR attribution. A person switching a
// helper on is not signing a legal instrument, and being addressed as if they
// were is what makes them wonder whether they are in the wrong product.
// TestRosieTextAvoidsTheInstrumentWords.
type Text struct {
	Title string `json:"title"`
	// Does says what Rosie does.
	Does string `json:"does"`
	// IfOff says what stops when she is off — the part a person most needs.
	IfOff string `json:"if_off"`
	// Anytime says the choice can be changed at any time.
	Anytime string `json:"anytime"`
	// CatchUp is shown when switching her on: she also looks at comments
	// already waiting (epic 11 D13).
	CatchUp string `json:"catch_up"`
	// Traced says her acts are marked as hers.
	Traced string `json:"traced"`
	// NotStarted is shown on polis.pub while the operator has not started her.
	NotStarted string `json:"not_started"`
}

// RosieText is the text, as shipped.
var RosieText = Text{
	Title:      "Rosie, your helper",
	Does:       "Rosie approves or turns away comments on your posts, using the rules you have already set. That is all she does today.",
	IfOff:      "If Rosie is off, comments wait for you to approve them yourself. Nothing else changes: your site stays connected and your messages keep working.",
	Anytime:    "You can switch her off, or back on, at any time.",
	CatchUp:    "When you switch her on, she also looks at comments already waiting for you, and decides them by the same rules.",
	Traced:     "Everything Rosie does is marked as hers and kept on your own site, so you can always see exactly what she did.",
	NotStarted: "Rosie has not started on polis.pub yet. Your choice is saved, and she follows it once she starts.",
}

// Plain renders the text for a terminal: the paragraphs a person reads before
// choosing, without the hosted-only NotStarted line.
func (t Text) Plain() string {
	return strings.Join([]string{t.Title, t.Does, t.IfOff, t.Anytime, t.CatchUp, t.Traced}, "\n\n")
}
