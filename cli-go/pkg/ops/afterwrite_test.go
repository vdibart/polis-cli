package ops

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vdibart/polis-cli/cli-go/pkg/bundle"
	"github.com/vdibart/polis-cli/cli-go/pkg/discovery"
	"github.com/vdibart/polis-cli/cli-go/pkg/draft"
	"github.com/vdibart/polis-cli/cli-go/pkg/hooks"
	"github.com/vdibart/polis-cli/cli-go/pkg/render"
	"github.com/vdibart/polis-cli/cli-go/pkg/storage"
)

// fakeRenderer records what the module asked of it.
type fakeRenderer struct {
	streamed   []string
	v5         []string
	full       int
	streamFail bool
	v5Fail     bool
}

func (f *fakeRenderer) RenderV5Change(p string) (*render.RenderStats, bool, error) {
	f.v5 = append(f.v5, p)
	if f.v5Fail {
		return nil, true, errors.New("v5 incremental failed")
	}
	return &render.RenderStats{PostsRendered: 1}, true, nil
}

func (f *fakeRenderer) PublishStream(p string) error {
	f.streamed = append(f.streamed, p)
	if f.streamFail {
		return errors.New("cascade failed")
	}
	return nil
}

func (f *fakeRenderer) RenderAll(bool) (*render.RenderStats, error) {
	f.full++
	return &render.RenderStats{PostsRendered: 3}, nil
}

type emitted struct {
	action string
	fields map[string]any
}

// named is the events with one name.
func named(events []emitted, action string) []emitted {
	var out []emitted
	for _, e := range events {
		if e.action == action {
			out = append(out, e)
		}
	}
	return out
}

// moduleOn returns an after-write module over a site on the given shape, with
// a fake renderer and a recording event sink.
func moduleOn(t *testing.T, shape string, hooksOn bool) (*AfterWrite, *fakeRenderer, *[]emitted, storage.Site) {
	t.Helper()
	site := storage.At(t.TempDir())
	if shape != "" {
		if err := bundle.SetActiveShapeName(site, shape); err != nil {
			t.Fatal(err)
		}
	}
	fr := &fakeRenderer{}
	var events []emitted
	a := NewAfterWrite(AfterWriteConfig{
		Site:        site,
		NewRenderer: func() (SiteRenderer, error) { return fr, nil },
		HooksOn:     hooksOn,
		Emit:        func(action string, f map[string]any) { events = append(events, emitted{action, f}) },
	})
	return a, fr, &events, site
}

// ⛔ Every write operation the engine serves states what it causes. Without
// an entry a write gets a full render, which is safe but slow; the point of
// this test is that a new operation is a decision someone made.
func TestEveryWriteOperationDeclaresItsEffects(t *testing.T) {
	h := NewBuiltinCoreHandler()
	for _, op := range h.Operations() {
		if !op.Write {
			continue
		}
		name := op.Type + "." + op.Action
		if _, ok := writeEffects[name]; !ok {
			t.Errorf("%s writes but has no entry in writeEffects: say what it renders, which hook it "+
				"runs and whether it removes a draft", name)
		}
	}
	for name := range writeEffects {
		found := false
		for _, op := range h.Operations() {
			if op.Type+"."+op.Action == name {
				found = true
			}
		}
		if !found {
			t.Errorf("writeEffects names %s, which is not an operation", name)
		}
	}
}

func TestAPostRendersIncrementallyWhereTheShapeCanAndInFullOtherwise(t *testing.T) {
	const path = "content/pub.polis.core/post/20261121/hello.md"
	w := Written{Type: "pub.polis.post", Action: "create", Path: path}

	a, fr, events, _ := moduleOn(t, "v4", false)
	a.Apply(context.Background(), w)
	if len(fr.streamed) != 1 || fr.streamed[0] != path || fr.full != 0 {
		t.Errorf("v4: streamed %v, full %d; want one incremental render of the post", fr.streamed, fr.full)
	}
	if r := named(*events, "pub.polis.site.render"); len(r) != 1 || r[0].fields["mode"] != RenderModeIncremental {
		t.Errorf("v4 events = %+v, want one pub.polis.site.render with mode incremental", *events)
	}

	a, fr, events, _ = moduleOn(t, "v5", false)
	a.Apply(context.Background(), w)
	if len(fr.v5) != 1 || fr.v5[0] != path || fr.full != 0 || len(fr.streamed) != 0 {
		t.Errorf("v5: v5 %v, streamed %v, full %d; want one v5 change render of the post", fr.v5, fr.streamed, fr.full)
	}
	if r := named(*events, "pub.polis.site.render"); len(r) != 1 || r[0].fields["mode"] != RenderModeIncremental || r[0].fields["posts"] != 1 {
		t.Errorf("v5 events = %+v, want mode incremental with the render's counts", *events)
	}

	a, fr, _, _ = moduleOn(t, "v3", false)
	a.Apply(context.Background(), w)
	if len(fr.v5) != 0 || len(fr.streamed) != 0 || fr.full != 1 {
		t.Errorf("v3: v5 %v, streamed %v, full %d; want one full render", fr.v5, fr.streamed, fr.full)
	}
}

// An unpublish renders the pages that showed the post on v5; on any other
// shape (v4's cascade cannot place a post that is gone) the whole site.
func TestAnUnpublishRendersIncrementallyOnlyOnV5(t *testing.T) {
	const path = "content/pub.polis.core/post/20261121/gone.md"
	w := Written{Type: "pub.polis.post", Action: "unpublish", Path: path}
	a, fr, events, _ := moduleOn(t, "v5", false)
	a.Apply(context.Background(), w)
	if len(fr.v5) != 1 || fr.v5[0] != path || fr.full != 0 {
		t.Errorf("v5: v5 %v, full %d; want the change render", fr.v5, fr.full)
	}
	if r := named(*events, "pub.polis.site.render"); len(r) != 1 || r[0].fields["mode"] != RenderModeIncremental {
		t.Errorf("v5 events = %+v", *events)
	}
	a, fr, _, _ = moduleOn(t, "v4", false)
	a.Apply(context.Background(), w)
	if len(fr.streamed) != 0 || len(fr.v5) != 0 || fr.full != 1 {
		t.Errorf("v4: streamed %v, v5 %v, full %d; want a full render", fr.streamed, fr.v5, fr.full)
	}
}

// ⭐ A failed shortcut never leaves a page stale: the module falls back to a
// full render.
func TestAFailedIncrementalRenderFallsBackToAFullOne(t *testing.T) {
	a5, fr5, events5, _ := moduleOn(t, "v5", false)
	fr5.v5Fail = true
	a5.Apply(context.Background(), Written{Type: "pub.polis.post", Action: "update", Path: "content/pub.polis.core/post/x.md"})
	if r := named(*events5, "pub.polis.site.render"); fr5.full != 1 || len(r) != 1 || r[0].fields["mode"] != RenderModeFull {
		t.Errorf("v5: full = %d, events = %+v; want the fallback full render", fr5.full, *events5)
	}

	a, fr, events, _ := moduleOn(t, "v4", false)
	fr.streamFail = true
	a.Apply(context.Background(), Written{Type: "pub.polis.post", Action: "update", Path: "content/pub.polis.core/post/x.md"})
	if r := named(*events, "pub.polis.site.render"); fr.full != 1 || len(r) != 1 || r[0].fields["mode"] != RenderModeFull {
		t.Errorf("full = %d, events = %+v; want the fallback full render", fr.full, *events)
	}
}

// The waste this removes: a draft autosave, a DM and a read mark re-rendered
// the whole site, because the engine rendered after every write.
func TestPrivateWritesRenderNothing(t *testing.T) {
	for _, op := range []string{"post.save_draft", "post.discard_draft", "dm.send", "dm.mark_read", "dm.delete"} {
		parts := strings.SplitN(op, ".", 2)
		a, fr, events, _ := moduleOn(t, "v4", false)
		a.Apply(context.Background(), Written{Type: "pub.polis." + parts[0], Action: parts[1]})
		if len(fr.streamed) != 0 || fr.full != 0 || len(named(*events, "pub.polis.site.render")) != 0 {
			t.Errorf("%s rendered (streamed %v, full %d, events %v); it changes nothing a reader sees", op, fr.streamed, fr.full, *events)
		}
	}
	// Tags are rendered (tags/<name>/), so a tag write renders the site.
	a, fr, _, _ := moduleOn(t, "v4", false)
	a.Apply(context.Background(), Written{Type: "pub.polis.tag", Action: "apply"})
	if fr.full != 1 {
		t.Errorf("tag.apply: full = %d, want a full render", fr.full)
	}
	// An operation nobody declared renders in full: stale pages are the worse failure.
	a, fr, _, _ = moduleOn(t, "v4", false)
	a.Apply(context.Background(), Written{Type: "com.example.thing", Action: "create"})
	if fr.full != 1 {
		t.Errorf("an undeclared write: full = %d, want a full render", fr.full)
	}
}

func TestPublishingFromADraftRemovesItButAnAgentNeverDoes(t *testing.T) {
	a, _, _, site := moduleOn(t, "", false)
	keep, err := draft.Save(site, "", "kept")
	if err != nil {
		t.Fatal(err)
	}
	gone, err := draft.Save(site, "", "published")
	if err != nil {
		t.Fatal(err)
	}
	exists := func(id string) bool {
		_, err := os.Stat(site.Path(filepath.FromSlash(draft.Key(id))))
		return err == nil
	}

	// ⛔ An agent's publish (a marked write) names the owner's draft: kept.
	a.Apply(context.Background(), Written{Type: "pub.polis.post", Action: "create", DraftID: keep,
		Marker: discovery.AgentMarker{Agent: "claude", Grant: "https://maya.example/g.json"}})
	if !exists(keep) {
		t.Error("an agent's publish removed the owner's draft")
	}
	// The owner's publish from a draft removes it.
	a.Apply(context.Background(), Written{Type: "pub.polis.post", Action: "create", DraftID: gone})
	if exists(gone) {
		t.Error("the owner published from a draft and it was not removed")
	}
	// An update never removes a draft, and a draft that is not there is no error.
	a.Apply(context.Background(), Written{Type: "pub.polis.post", Action: "update", DraftID: keep})
	if !exists(keep) {
		t.Error("an update removed a draft")
	}
	a.Apply(context.Background(), Written{Type: "pub.polis.post", Action: "create", DraftID: "draft-not-there"})
}

// writeHook installs a conventional post-publish hook that records its run.
func writeHook(t *testing.T, site storage.Site, event hooks.HookEvent) string {
	t.Helper()
	marker := site.Path("hook-ran")
	script := site.Path(".polis", "webapp", "hooks", string(event)+".sh")
	if err := os.MkdirAll(filepath.Dir(script), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(script, []byte("#!/bin/sh\necho ran > "+marker+"\necho done\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	return marker
}

// ⛔ Hooks run only where the host turns them on. The CLI turns them off by
// decision, so a site with a hook script does not run it from `polis publish`.
func TestTheOwnersHookRunsOnlyWhereTheHostTurnsHooksOn(t *testing.T) {
	w := Written{Type: "pub.polis.post", Action: "create", Path: "content/pub.polis.core/post/x.md", Title: "Hello"}

	off, _, _, site := moduleOn(t, "", false)
	ran := writeHook(t, site, hooks.EventPostPublish)
	off.Apply(context.Background(), w)
	if _, err := os.Stat(ran); err == nil {
		t.Fatal("a hook ran on a host with hooks off")
	}

	on, _, events, site := moduleOn(t, "", true)
	ran = writeHook(t, site, hooks.EventPostPublish)
	on.Apply(context.Background(), w)
	if _, err := os.Stat(ran); err != nil {
		t.Fatal("the post-publish hook did not run on a host with hooks on")
	}
	var logged bool
	for _, e := range *events {
		if e.action == "pub.polis.hook.post_publish" {
			logged = true
		}
	}
	if !logged {
		t.Errorf("events = %+v, want pub.polis.hook.post_publish", *events)
	}
}

func TestACommentsHookRunsOnlyWhenItCameBackBlessed(t *testing.T) {
	a, _, _, site := moduleOn(t, "", true)
	ran := writeHook(t, site, hooks.EventPostComment)
	a.Apply(context.Background(), Written{Type: "pub.polis.comment", Action: "create", Result: map[string]any{"auto_blessed": false}})
	if _, err := os.Stat(ran); err == nil {
		t.Fatal("the post-comment hook ran for a comment that was not blessed")
	}
	a.Apply(context.Background(), Written{Type: "pub.polis.comment", Action: "create", Result: map[string]any{"auto_blessed": true}})
	if _, err := os.Stat(ran); err != nil {
		t.Fatal("the post-comment hook did not run for a comment that came back blessed")
	}
}

func TestAHostsExtraRunsAfterTheSharedEffects(t *testing.T) {
	fr := &fakeRenderer{}
	var order []string
	a := NewAfterWrite(AfterWriteConfig{
		Site:        storage.At(t.TempDir()),
		NewRenderer: func() (SiteRenderer, error) { order = append(order, "render"); return fr, nil },
		Extra:       func(context.Context, Written) { order = append(order, "extra") },
	})
	a.Apply(context.Background(), Written{Type: "pub.polis.tag", Action: "apply"})
	if strings.Join(order, ",") != "render,extra" {
		t.Errorf("order = %v, want render then the host's extra", order)
	}
}

// ⭐ Every write is logged once, under the browser app's name where it has
// one, with D8's field set: the request id, the operation, the surface, the
// agent marker when an agent wrote, and the result. ⛔ Never a body.
func TestAWriteIsLoggedOnceUnderItsNameWithTheFieldSet(t *testing.T) {
	a, _, events, _ := moduleOn(t, "v5", false)
	meta := &RequestMeta{RequestID: "req-1", Surface: SurfaceOwnerApp}
	ctx := WithRequestMeta(context.Background(), meta)
	a.Apply(ctx, Written{Type: "pub.polis.post", Action: "create", Path: "content/pub.polis.core/post/x.md",
		Title: "Hello", Version: "sha256:ab", Result: map[string]any{"path": "content/pub.polis.core/post/x.md", "markdown": "the body"}})

	pub := named(*events, "pub.polis.post.publish")
	if len(pub) != 1 {
		t.Fatalf("events = %+v, want one pub.polis.post.publish", *events)
	}
	f := pub[0].fields
	for k, want := range map[string]any{"request_id": "req-1", "op": "pub.polis.post.create", "surface": SurfaceOwnerApp,
		"result": "ok", "path": "content/pub.polis.core/post/x.md", "title": "Hello", "version": "sha256:ab"} {
		if f[k] != want {
			t.Errorf("%s = %v, want %v", k, f[k], want)
		}
	}
	if _, ok := f["markdown"]; ok {
		t.Error("the event carries the post's body")
	}
	if _, ok := f["agent"]; ok {
		t.Error("the owner's write carries an agent")
	}
	// The render it caused carries the same request id, and comes after.
	if (*events)[0].action != "pub.polis.post.publish" {
		t.Errorf("first event = %s, want the write itself", (*events)[0].action)
	}
	if r := named(*events, "pub.polis.site.render"); len(r) != 1 || r[0].fields["request_id"] != "req-1" {
		t.Errorf("render events = %+v, want one with the request id", r)
	}
}

func TestAnAgentsWriteIsLoggedAsTheAgentsWithItsGrant(t *testing.T) {
	a, _, events, _ := moduleOn(t, "", false)
	ctx := WithRequestMeta(context.Background(), &RequestMeta{RequestID: "req-2", Surface: SurfaceToken})
	a.Apply(ctx, Written{Type: "pub.polis.tag", Action: "apply",
		Marker: discovery.AgentMarker{Agent: "claude", Grant: "https://maya.example/g.json"}})
	ev := named(*events, "pub.polis.tag.applied")
	if len(ev) != 1 {
		t.Fatalf("events = %+v", *events)
	}
	f := ev[0].fields
	if f["surface"] != SurfaceAgent || f["agent"] != "claude" || f["grant"] != "https://maya.example/g.json" {
		t.Errorf("fields = %v, want surface agent with the marker", f)
	}
}

// A write whose package logs its own outcome (a DM send) is not logged twice.
func TestADMSendIsNotLoggedASecondTime(t *testing.T) {
	a, _, events, _ := moduleOn(t, "", false)
	a.Apply(context.Background(), Written{Type: "pub.polis.dm", Action: "send"})
	if len(*events) != 0 {
		t.Errorf("events = %+v, want none: the DM package logs dm.send.delivered or .failed", *events)
	}
}

func TestACommentSignedButNotSentSaysSo(t *testing.T) {
	a, _, events, _ := moduleOn(t, "", false)
	a.Apply(context.Background(), Written{Type: "pub.polis.comment", Action: "create",
		Result: map[string]any{"sent": false, "send_error": "ds down"}})
	ev := named(*events, "pub.polis.comment.sign")
	if len(ev) != 1 || ev[0].fields["result"] != "signed_not_sent" || ev[0].fields["sent"] != false {
		t.Errorf("events = %+v, want one pub.polis.comment.sign, result signed_not_sent", *events)
	}
	if _, ok := ev[0].fields["send_error"]; ok {
		t.Error("the event carries the raw send error")
	}
}

func TestEveryWriteOperationNamesItsEvent(t *testing.T) {
	for op, e := range writeEffects {
		if e.event == "" {
			t.Errorf("%s names no event", op)
			continue
		}
		if e.event != eventByPackage && !strings.HasPrefix(e.event, "pub.polis.") {
			t.Errorf("%s logs %q, which is not a pub.polis.* name", op, e.event)
		}
	}
}
