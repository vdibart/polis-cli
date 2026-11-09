// Package v0 is a client for v0's Platform API (https://v0.app/docs/api/platform),
// the calls an owner's polis instance makes to talk to v0 for its owner: check a
// key, register the instance's own MCP endpoint, and hold a chat whose tools
// are the site's.
//
// ⚠️ EXPERIMENTAL. Part of an exploratory feature: the owner's AI changing
// their site through the instance's API. This package's API may change or be
// removed without notice; do not depend on it. It also reads fields v0 itself
// documents as unstable (see steps.go).
//
// ⭐ v0 ACTS ON THE SITE ONLY THROUGH MCP. Nothing here changes the site; a
// chat created with MCPServerIDs makes v0 call the instance's /mcp, which is
// authorised by an API key the owner minted. This package carries the
// conversation, never the authority.
//
// ⛔ The API key is a secret. It is passed in, used at the point of the call,
// and never logged, returned or put in an error.
package v0

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// DefaultBase is v0's Platform API.
const DefaultBase = "https://api.v0.dev/v1"

// Client calls v0's Platform API with one API key.
type Client struct {
	key  string
	base string
	http *http.Client
}

// New returns a client for the key. base "" means DefaultBase.
func New(key, base string) *Client {
	if base == "" {
		base = DefaultBase
	}
	return &Client{key: strings.TrimSpace(key), base: strings.TrimRight(base, "/"),
		http: &http.Client{Timeout: 60 * time.Second}}
}

// APIError is a refusal from v0. ⭐ It carries v0's own message, never the key.
type APIError struct {
	Status  int
	Message string
}

func (e *APIError) Error() string {
	if e.Message == "" {
		return fmt.Sprintf("v0 answered %d", e.Status)
	}
	return fmt.Sprintf("v0 answered %d: %s", e.Status, e.Message)
}

// Unauthorized reports a refused key.
func Unauthorized(err error) bool {
	ae, ok := err.(*APIError)
	return ok && (ae.Status == http.StatusUnauthorized || ae.Status == http.StatusForbidden)
}

// NotFound reports a missing chat or server.
func NotFound(err error) bool {
	ae, ok := err.(*APIError)
	return ok && ae.Status == http.StatusNotFound
}

func (c *Client) call(ctx context.Context, method, path string, in, out any) error {
	var body io.Reader
	if in != nil {
		b, err := json.Marshal(in)
		if err != nil {
			return err
		}
		body = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.base+path, body)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+c.key)
	if in != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("could not reach v0: %w", err)
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if resp.StatusCode/100 != 2 {
		var e struct {
			Error struct {
				Message string `json:"message"`
			} `json:"error"`
			Message string `json:"message"`
		}
		_ = json.Unmarshal(data, &e)
		msg := e.Error.Message
		if msg == "" {
			msg = e.Message
		}
		if len(msg) > 300 {
			msg = msg[:300]
		}
		return &APIError{Status: resp.StatusCode, Message: msg}
	}
	if out != nil && len(data) > 0 {
		if err := json.Unmarshal(data, out); err != nil {
			return fmt.Errorf("v0 answered something unexpected: %w", err)
		}
	}
	return nil
}

// User is the account a key belongs to.
type User struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

// Me checks the key: the account it belongs to, or an error that Unauthorized
// recognises.
func (c *Client) Me(ctx context.Context) (User, error) {
	var u User
	err := c.call(ctx, http.MethodGet, "/user", nil, &u)
	return u, err
}

// MCPServer is an MCP server registered with v0.
type MCPServer struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	URL     string `json:"url"`
	Enabled bool   `json:"enabled"`
}

// CreateMCPServer registers an MCP server that v0 reaches with a bearer token.
func (c *Client) CreateMCPServer(ctx context.Context, name, url, description, bearer string) (MCPServer, error) {
	var s MCPServer
	err := c.call(ctx, http.MethodPost, "/mcp-servers", map[string]any{
		"name": name, "url": url, "description": description,
		"auth": map[string]string{"type": "bearer", "token": bearer},
	}, &s)
	return s, err
}

// ListMCPServers lists the servers registered for the key's account.
func (c *Client) ListMCPServers(ctx context.Context) ([]MCPServer, error) {
	var out struct {
		Data []MCPServer `json:"data"`
	}
	err := c.call(ctx, http.MethodGet, "/mcp-servers", nil, &out)
	return out.Data, err
}

// DeleteMCPServer removes a registration. A server already gone is not an error.
func (c *Client) DeleteMCPServer(ctx context.Context, id string) error {
	err := c.call(ctx, http.MethodDelete, "/mcp-servers/"+id, nil, nil)
	if NotFound(err) {
		return nil
	}
	return err
}

// Ask is one message to v0.
type Ask struct {
	Message string
	System  string
	// MCPServerIDs limits the chat to these servers. ⛔ Never empty here: an
	// omitted list falls back to every server the account has enabled.
	MCPServerIDs []string
	Model        string
}

func (a Ask) body(first bool) (map[string]any, error) {
	if strings.TrimSpace(a.Message) == "" {
		return nil, fmt.Errorf("a message is required")
	}
	if len(a.MCPServerIDs) == 0 {
		return nil, fmt.Errorf("a chat must name the MCP servers it may use")
	}
	b := map[string]any{
		"message": a.Message, "responseMode": "async", "mcpServerIds": a.MCPServerIDs,
	}
	if a.System != "" {
		b["system"] = a.System
	}
	if first {
		b["chatPrivacy"] = "private"
		model := a.Model
		if model == "" {
			model = "v0-pro"
		}
		b["modelConfiguration"] = map[string]any{"modelId": model, "imageGenerations": false, "thinking": false}
	}
	return b, nil
}

// StartChat starts a private chat and returns at once (async): v0 answers in
// the background, and GetChat reads its progress.
func (c *Client) StartChat(ctx context.Context, a Ask) (Chat, error) {
	b, err := a.body(true)
	if err != nil {
		return Chat{}, err
	}
	var ch Chat
	err = c.call(ctx, http.MethodPost, "/chats", b, &ch)
	return ch, err
}

// Continue sends a follow-up message in a chat, async.
func (c *Client) Continue(ctx context.Context, chatID string, a Ask) (Chat, error) {
	b, err := a.body(false)
	if err != nil {
		return Chat{}, err
	}
	var ch Chat
	err = c.call(ctx, http.MethodPost, "/chats/"+url.PathEscape(chatID)+"/messages", b, &ch)
	return ch, err
}

// GetChat reads a chat as it stands, mid-turn included.
func (c *Client) GetChat(ctx context.Context, id string) (Chat, error) {
	var ch Chat
	err := c.call(ctx, http.MethodGet, "/chats/"+url.PathEscape(id), nil, &ch)
	return ch, err
}

// Chat is a v0 chat. ⚠️ A message response may carry the chat's id under
// "chatId" rather than "id"; Key resolves both.
type Chat struct {
	ID       string    `json:"id"`
	ChatID   string    `json:"chatId"`
	WebURL   string    `json:"webUrl"`
	Messages []Message `json:"messages"`
}

// Key is the chat's id, whichever field carried it.
func (c Chat) Key() string {
	if c.ChatID != "" {
		return c.ChatID
	}
	return c.ID
}

// Message is one message of a chat.
type Message struct {
	ID           string          `json:"id"`
	Role         string          `json:"role"`
	Content      string          `json:"content"`
	FinishReason string          `json:"finishReason"`
	CreatedAt    string          `json:"createdAt"`
	Experimental json.RawMessage `json:"experimental_content"`
}

// Done reports whether an assistant message has finished: it has a finish
// reason other than a pause to call tools.
func (m Message) Done() bool {
	return m.Role == "assistant" && m.FinishReason != "" && m.FinishReason != "tool-calls"
}
