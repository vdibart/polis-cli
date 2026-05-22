package notification

// Rule defines how a stream event type maps to a notification.
type Rule struct {
	ID        string       `json:"id"`
	EventType string       `json:"event_type"`
	Enabled   bool         `json:"enabled"`
	Filter    RuleFilter   `json:"filter"`
	Template  RuleTemplate `json:"template"`
	Batch     bool         `json:"batch"`
	// BatchWindow is a duration string like "24h". Only used when Batch is true.
	BatchWindow string `json:"batch_window,omitempty"`
}

// RuleFilter specifies how to determine relevance of an event.
type RuleFilter struct {
	// Relevance is one of: "target_domain", "source_domain", "followed_author"
	Relevance string `json:"relevance"`
}

// RuleTemplate defines the display format for a notification.
type RuleTemplate struct {
	Icon    string `json:"icon"`
	Message string `json:"message"`
	// Link is a URL template for the notification's click target.
	// Supports {{var}} substitution (same vars as Message).
	// Examples: "/_/#blessings", "/_/#followers", "{{source_url}}"
	Link string `json:"link,omitempty"`
}
