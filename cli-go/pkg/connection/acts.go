package connection

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io/fs"
	"sync"
	"time"

	"github.com/vdibart/polis-cli/cli-go/pkg/storage"
)

// ActsKey is the site's private record of what its connections did: one JSON
// line per write made under a grant. ⭐ In the site's own private store, so it
// moves with the site; ⛔ under .polis, so it is never served.
//
// ⭐ It is where an act with NOTHING SIGNED (a name, a theme, a seen mark) is
// recorded at all, and it lists the signed ones too, so the owner can ask one
// place "what did this connection do?".
const ActsKey = ".polis/connections/acts.jsonl"

// maxActs bounds the record: the newest are kept. ⛔ The bound is PER KIND:
// up to maxActs of the connections' acts and up to maxOwnerActs of the
// owner's. The owner writes far more often (a draft saves while they type),
// and one shared bound would let the owner's acts push a connection's out,
// which is the evidence Judge checks an agent's marked work against.
const maxActs = 2000

// maxOwnerActs is the owner's bound. Smaller, because the whole record is
// rewritten on every act and the owner's are the frequent ones.
const maxOwnerActs = 500

// MaxActs is that bound, for a reader that must know whether the record still
// reaches back to a given act (a full record may have dropped older ones).
const MaxActs = maxActs

// Act is one write made through the API: a connection's (Agent and Grant
// set), or the owner's own (both empty).
type Act struct {
	Time   string `json:"ts"`
	Agent  string `json:"agent"`
	Grant  string `json:"grant"`
	Method string `json:"method"`
	Path   string `json:"path"`
	Status int    `json:"status"`
	// RequestID correlates the act with the request and security logs, and
	// with the write's domain event.
	RequestID string `json:"request_id,omitempty"`
	// Op is the operation (<type>.<action>) and Surface who wrote (v5, token,
	// agent), as the write's event records them.
	Op      string `json:"op,omitempty"`
	Surface string `json:"surface,omitempty"`
}

// Owner reports an act the owner made, not a connection.
func (a Act) Owner() bool { return a.Agent == "" && a.Grant == "" }

var actsMu sync.Mutex

// RecordAct appends a to the site's record of connection acts. ⚠️ The lock
// is per process; two processes on one site's store (not a shape polis runs)
// could lose a line.
func RecordAct(site storage.Site, a Act) error {
	if a.Time == "" {
		a.Time = time.Now().UTC().Format(time.RFC3339)
	}
	line, err := json.Marshal(a)
	if err != nil {
		return err
	}
	actsMu.Lock()
	defer actsMu.Unlock()
	ctx := context.Background()
	data, err := site.Store().Get(ctx, ActsKey)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	lines := bytes.Split(bytes.TrimRight(data, "\n"), []byte("\n"))
	if len(data) == 0 {
		lines = nil
	}
	lines = trimActs(append(lines, line))
	return site.Store().Put(ctx, ActsKey, append(bytes.Join(lines, []byte("\n")), '\n'), storage.WithMode(0o600))
}

// Acts returns the recorded acts, oldest first, optionally only grantURL's.
func Acts(site storage.Site, grantURL string) ([]Act, error) {
	data, err := site.Store().Get(context.Background(), ActsKey)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var out []Act
	for _, l := range bytes.Split(data, []byte("\n")) {
		var a Act
		if len(bytes.TrimSpace(l)) == 0 || json.Unmarshal(l, &a) != nil {
			continue
		}
		if grantURL == "" || a.Grant == grantURL {
			out = append(out, a)
		}
	}
	return out, nil
}

// trimActs keeps the newest lines of each kind within its bound, in order.
func trimActs(lines [][]byte) [][]byte {
	owner := make([]bool, len(lines))
	var nOwner, nAgent int
	for i, l := range lines {
		var a Act
		if json.Unmarshal(l, &a) == nil && a.Owner() {
			owner[i] = true
			nOwner++
		} else {
			nAgent++
		}
	}
	dropOwner, dropAgent := nOwner-maxOwnerActs, nAgent-maxActs
	if dropOwner <= 0 && dropAgent <= 0 {
		return lines
	}
	out := lines[:0:0]
	for i, l := range lines {
		if owner[i] && dropOwner > 0 {
			dropOwner--
			continue
		}
		if !owner[i] && dropAgent > 0 {
			dropAgent--
			continue
		}
		out = append(out, l)
	}
	return out
}

// AgentActs is the record's connection acts only, the owner's left out: what
// a check of an agent's marked work reads, so the bound it reasons about is
// the connections' own.
func AgentActs(site storage.Site) ([]Act, error) {
	all, err := Acts(site, "")
	var out []Act
	for _, a := range all {
		if !a.Owner() {
			out = append(out, a)
		}
	}
	return out, err
}
