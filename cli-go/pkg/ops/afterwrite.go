package ops

import (
	"context"
	"errors"
	"io/fs"
	"time"

	"github.com/vdibart/polis-cli/cli-go/pkg/bundle"
	"github.com/vdibart/polis-cli/cli-go/pkg/discovery"
	"github.com/vdibart/polis-cli/cli-go/pkg/draft"
	"github.com/vdibart/polis-cli/cli-go/pkg/hooks"
	"github.com/vdibart/polis-cli/cli-go/pkg/render"
	"github.com/vdibart/polis-cli/cli-go/pkg/storage"
)

// ⭐⭐ WHAT A WRITE CAUSES BELONGS TO THE ACTION, AND HAPPENS ONCE, HERE.
//
// A write's side effects (the render, the owner's hook, removing the draft a
// post was published from) used to be each caller's own: the browser app's
// /api handlers did them, the engine behind /v1 did almost none (it re-rendered
// the whole site after every write, a draft autosave included, and ran no
// hook), and the CLI did its own. Three copies drifted. The engine now reports
// each successful write as a Written value, and AfterWrite acts on it the same
// way for every host. A host supplies only what genuinely differs: where its
// events go, the site to render against, whether hooks may run, and extras
// only it has.

// Written is what one successful write did, as the engine reports it.
type Written struct {
	Type    string // the content type, e.g. pub.polis.post
	Action  string // the operation, e.g. create
	Path    string // the content path written, when the write names one
	Title   string
	Version string
	// DraftID is the draft a post was published from, when the caller named
	// one: it is removed once the post is out.
	DraftID string
	Marker  discovery.AgentMarker
	// Result is the operation's answer, for effects that depend on it (a
	// comment's hook runs only when the comment came back blessed).
	Result map[string]any
}

// Op is the operation's name, <type>.<action>.
func (w Written) Op() string { return w.Type + "." + w.Action }

// renderKind is how much a write changes the rendered site.
type renderKind int

const (
	renderNone    renderKind = iota // private state only: nothing a reader sees changes
	renderItem                      // one post: incremental where the shape supports it
	renderRemoval                   // one post taken down: incremental on v5, else the whole site
	renderFull                      // the whole site
)

// writeEffect is what one operation causes.
type writeEffect struct {
	// event is the domain event the write is logged under, once, with D8's
	// field set. eventByPackage means the package that does the work already
	// logs its own outcome (a DM send's dm.send.delivered or .failed, a
	// delivery's dm.deliver.*), stamped with the same fields by the engine, so
	// a second event here would count the write twice.
	event  string
	render renderKind
	hook   hooks.HookEvent // "" for none
	// hookIfBlessed runs the hook only when the answer says the item was
	// blessed (a comment the post's author had already accepted).
	hookIfBlessed bool
	removesDraft  bool
}

// eventByPackage marks a write whose package logs its own outcome.
const eventByPackage = "(logged by its package)"

// writeEffects is every write operation's effects. ⛔ A write operation with
// no entry gets a full render and a generic event (safe defaults: nothing a
// reader sees goes stale, nothing goes unlogged), and
// TestEveryWriteOperationDeclaresItsEffects fails until it has one, so a new
// operation is a decision, not an accident.
//
// ⛔ The event names are the ones the browser app's handlers have always used
// where one exists (pub.polis.post.publish, …): dashboards key on them. A new
// name follows pub.polis.<type>.<verb>.
var writeEffects = map[string]writeEffect{
	"pub.polis.post.create":        {event: "pub.polis.post.publish", render: renderItem, hook: hooks.EventPostPublish, removesDraft: true},
	"pub.polis.post.update":        {event: "pub.polis.post.republish", render: renderItem, hook: hooks.EventPostRepublish},
	"pub.polis.post.save_draft":    {event: "pub.polis.post.draft_save", render: renderNone}, // a draft is private (.polis)
	"pub.polis.post.discard_draft": {event: "pub.polis.post.draft_discard", render: renderNone},
	"pub.polis.post.unpublish":     {event: "pub.polis.post.unpublish", render: renderRemoval}, // the old app's name; the pages that showed it change
	// One event for the API's one step (sign and send); "sent" and "status"
	// say how far it got. The browser app's two steps log sign and beseech.
	"pub.polis.comment.create": {event: "pub.polis.comment.sign", render: renderFull, hook: hooks.EventPostComment, hookIfBlessed: true},
	"pub.polis.comment.update": {event: "pub.polis.comment.republish", render: renderFull},
	"pub.polis.dm.send":        {event: eventByPackage, render: renderNone}, // messages are never rendered
	"pub.polis.dm.mark_read":   {event: "pub.polis.dm.mark_read", render: renderNone},
	"pub.polis.dm.delete":      {event: "pub.polis.dm.delete", render: renderNone},
	"pub.polis.dm.retry":       {event: eventByPackage, render: renderNone},
	"pub.polis.dm.deliver":     {event: eventByPackage, render: renderNone},
	"pub.polis.tag.apply":      {event: "pub.polis.tag.applied", render: renderFull}, // tag pages are rendered (tags/<name>/)
	"pub.polis.tag.remove":     {event: "pub.polis.tag.removed", render: renderFull},
	"pub.polis.tag.delete":     {event: "pub.polis.tag.deleted", render: renderFull},
}

// DeclaredWriteEvent is the domain event a write operation the engine serves
// is logged under, and whether the operation declares one. "" with true means
// the package doing the work logs its own outcome (a DM send).
func DeclaredWriteEvent(op string) (string, bool) {
	e, ok := writeEffects[op]
	if !ok {
		return "", false
	}
	if e.event == eventByPackage {
		return "", true
	}
	return e.event, true
}

func effectOf(op string) writeEffect {
	if e, ok := writeEffects[op]; ok {
		return e
	}
	return writeEffect{event: "pub.polis.content.write", render: renderFull}
}

// SiteRenderer is the part of a page renderer the after-write module uses.
// *render.PageRenderer satisfies it.
type SiteRenderer interface {
	PublishStream(postSourceRelPath string) error
	RenderV5Change(postSourceRelPath string) (*render.RenderStats, bool, error)
	RenderAll(force bool) (*render.RenderStats, error)
}

// AfterWriteConfig is what a host supplies.
type AfterWriteConfig struct {
	// Site is the site the writes landed on.
	Site storage.Site
	// NewRenderer builds a renderer for the site. nil renders nothing (a host
	// that renders elsewhere, or a test).
	NewRenderer func() (SiteRenderer, error)
	// Hooks is the owner's hook configuration, or nil when hooks may not run
	// on this host. ⛔ The CLI passes nil: it runs no publish hook, by
	// decision. RunHooks also discovers a script by convention, so "may run"
	// is the host's choice, not the config's.
	Hooks   *hooks.HookConfig
	HooksOn bool
	Emit    func(action string, fields map[string]any)
	Warn    func(format string, args ...any)
	// Extra runs after the shared effects, for what only this host has (the
	// hosted service's per-tenant cache, for one).
	Extra func(ctx context.Context, w Written)
	// SkipRenderShapes are shapes this host does not render after a write:
	// the CLI has never rendered a v3 (blog) site on publish, and still does
	// not.
	SkipRenderShapes map[string]bool
}

// AfterWrite acts on each successful write.
type AfterWrite struct{ cfg AfterWriteConfig }

// NewAfterWrite returns the shared after-write module for a host.
func NewAfterWrite(cfg AfterWriteConfig) *AfterWrite { return &AfterWrite{cfg: cfg} }

// emit logs an event about w with D8's field set.
func (a *AfterWrite) emit(ctx context.Context, w Written, action string, fields map[string]any) {
	if a.cfg.Emit != nil {
		a.cfg.Emit(action, EventFields(ctx, w.Op(), w.Marker, fields))
	}
}

func (a *AfterWrite) warn(format string, args ...any) {
	if a.cfg.Warn != nil {
		a.cfg.Warn(format, args...)
	}
}

// Apply does what w causes. A failed effect is reported and never undoes the
// write: the post is published whether or not its page re-rendered.
func (a *AfterWrite) Apply(ctx context.Context, w Written) {
	e := effectOf(w.Op())

	// The write itself, logged first, so its event precedes what it caused.
	if e.event != eventByPackage {
		a.emit(ctx, w, e.event, domainFields(w))
	}

	switch e.render {
	case renderItem:
		a.render(ctx, w, w.Path)
	case renderRemoval:
		a.render(ctx, w, w.Path, true)
	case renderFull:
		a.render(ctx, w, "")
	}

	// ⛔ Drafts are the owner's alone: an agent's publish (a marked write)
	// never removes one, whatever draft_id it names.
	ownerWrite := w.Marker.Agent == "" && w.Marker.Grant == ""
	if e.removesDraft && w.DraftID != "" && ownerWrite && a.cfg.Site.Valid() {
		if err := draft.Discard(a.cfg.Site, w.DraftID); err != nil && !errors.Is(err, fs.ErrNotExist) {
			a.warn("published, but its draft %s was not removed: %v", draft.SanitizeID(w.DraftID), err)
		}
	}

	if e.hook != "" && a.cfg.HooksOn && (!e.hookIfBlessed || blessed(w.Result)) {
		a.runHook(ctx, e.hook, w)
	}

	if a.cfg.Extra != nil {
		a.cfg.Extra(ctx, w)
	}
}

// domainFields are a write's own fields on its event: what it names, and what
// its answer says about how far it got. ⛔ Never a body: an event is metadata.
func domainFields(w Written) map[string]any {
	f := map[string]any{"result": "ok"}
	for k, v := range map[string]string{"path": w.Path, "title": w.Title, "version": w.Version} {
		if v != "" {
			f[k] = v
		}
	}
	for _, k := range []string{"id", "sent", "status", "auto_blessed", "tag", "target_uri"} {
		if v, ok := w.Result[k]; ok {
			f[k] = v
		}
	}
	if _, failed := w.Result["send_error"]; failed {
		f["result"] = "signed_not_sent"
	}
	return f
}

// blessed reports an answer that says the item came back blessed.
func blessed(result map[string]any) bool {
	b, _ := result["auto_blessed"].(bool)
	return b
}

// render re-renders after a change to postPath ("" for the whole site).
func (a *AfterWrite) render(ctx context.Context, w Written, postPath string, removed ...bool) {
	if a.cfg.NewRenderer == nil {
		return
	}
	if len(a.cfg.SkipRenderShapes) > 0 {
		if shape, err := bundle.GetActiveShapeName(a.cfg.Site); err == nil && a.cfg.SkipRenderShapes[shape] {
			return
		}
	}
	r, err := a.cfg.NewRenderer()
	if err != nil {
		a.warn("render after write: %v", err)
		return
	}
	choose := RenderAfterChange
	if len(removed) > 0 && removed[0] {
		choose = RenderAfterRemoval // the post is gone: v4's shortcut cannot place it
	}
	mode, stats, err := choose(a.cfg.Site, r, postPath)
	if err != nil {
		a.warn("render after write: %v", err)
		return
	}
	fields := map[string]any{"mode": mode}
	if postPath != "" {
		fields["path"] = postPath
	}
	if stats != nil {
		fields["posts"] = stats.PostsRendered
		fields["comments"] = stats.CommentsRendered
	}
	a.emit(ctx, w, "pub.polis.site.render", fields)
}

// Render modes, as the render event records them.
const (
	RenderModeIncremental = "incremental"
	RenderModeFull        = "full"
)

// incrementalShapes are the shapes whose renderer can re-render one post and
// its neighbours instead of the whole site.
var incrementalShapes = map[string]bool{"v4": true}

// RenderAfterChange renders a site after one post changed (postPath), or in
// full (postPath ""). ⭐ The one place the choice is made: incremental where the
// site's shape supports it, full otherwise, and full again if the incremental
// render fails, so a page is never left stale by a failed shortcut.
func RenderAfterChange(site storage.Site, r SiteRenderer, postPath string) (string, *render.RenderStats, error) {
	if postPath != "" {
		if shape, err := bundle.GetActiveShapeName(site); err == nil && shape == "v5" {
			return renderV5(r, postPath)
		}
		if shape, err := bundle.GetActiveShapeName(site); err == nil && incrementalShapes[shape] {
			if err := r.PublishStream(postPath); err == nil {
				return RenderModeIncremental, nil, nil
			}
		}
	}
	stats, err := r.RenderAll(true)
	return RenderModeFull, stats, err
}

// RenderAfterRemoval renders a site after one post was taken down: v5 renders
// incrementally (the pages that showed it); every other shape renders in full.
func RenderAfterRemoval(site storage.Site, r SiteRenderer, postPath string) (string, *render.RenderStats, error) {
	if shape, err := bundle.GetActiveShapeName(site); err == nil && shape == "v5" && postPath != "" {
		return renderV5(r, postPath)
	}
	stats, err := r.RenderAll(true)
	return RenderModeFull, stats, err
}

// renderV5 is v5's change render: incremental when its record allows, the
// whole site otherwise (RenderV5Change decides), and the whole site again if
// the incremental render fails part way, so no page is left stale.
func renderV5(r SiteRenderer, postPath string) (string, *render.RenderStats, error) {
	stats, incremental, err := r.RenderV5Change(postPath)
	if err != nil && incremental {
		stats, err = r.RenderAll(true)
		return RenderModeFull, stats, err
	}
	if incremental {
		return RenderModeIncremental, stats, err
	}
	return RenderModeFull, stats, err
}

// runHook runs the owner's hook for an event. A hook that fails is reported
// and never fails the write.
func (a *AfterWrite) runHook(ctx context.Context, event hooks.HookEvent, w Written) {
	payload := &hooks.HookPayload{
		Event:         event,
		Path:          w.Path,
		Title:         w.Title,
		Version:       w.Version,
		Timestamp:     time.Now().UTC().Format("2006-01-02T15:04:05Z"),
		CommitMessage: hooks.GenerateCommitMessage(event, w.Title),
	}
	res, err := hooks.RunHook(a.cfg.Site, a.cfg.Hooks, payload)
	if err != nil {
		a.warn("%s hook failed: %v", event, err)
		return
	}
	if res != nil && res.Executed {
		a.emit(ctx, w, "pub.polis.hook."+hookEventName(event), map[string]any{"output": res.Output})
	}
}

// hookEventName is the event name a hook run logs under: post-publish →
// post_publish, the name the browser app has always used.
func hookEventName(e hooks.HookEvent) string {
	b := []byte(e)
	for i := range b {
		if b[i] == '-' {
			b[i] = '_'
		}
	}
	return string(b)
}
