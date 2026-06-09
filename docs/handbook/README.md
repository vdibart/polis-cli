# Handbook

Curated walk-throughs of polis source code — the "guided tours" referenced by trail markers in the publicly-served code.

The trail markers in source files name what each file does and link to concept docs. When a thread spans several files in a way that's hard to follow from comments alone, a guided tour lives here as a walked-through MD doc. Each tour:

- Starts with an **observation** — something a developer notices on polis.pub or in view-source.
- Walks through the **mechanism** across files with code excerpts.
- Lands at the **concept docs** in [`../general/`](../general/) as the philosophical destination.

For the map of every thread and the recipes that compose them, see [`AGENTS.md`](../../AGENTS.md) at the repo root.

## Tours

### Overview (read first for the stream-screen)

- [stream-overview.md](stream-overview.md) — How the polis.pub infinity stream actually works, end to end. The synthesis tour: the sentence-filter widget, the four-layer composition (shape / filter / data / chrome), the design statement behind the surface, and clear pointers down into the two deep tours below and out to the PQL + DS concept docs.

### Deep tours (file-by-file walks)

- [url-as-filter.md](url-as-filter.md) — Why does the URL change as you scroll or click an icon? Pull the thread from `app.js` through `pql.js` to `stream.js` and out to the infinity stream philosophy.
- [ds-to-stream.md](ds-to-stream.md) — How does content from sites you don't host appear in your stream, and where do the cross-tenant comment counts come from? Pull the thread through the webapp sync loop, the DS stream and counts endpoints, the feed transformer, the local JSONL cache, and back to the rendered view.
- [foreign-site-widget.md](foreign-site-widget.md) — How does your nav appear on someone else's polis site when you're logged in? Pull the thread through the serve-time placeholder patch (`nav_inject.go`), the hydrator (`nav.js`), and the companion comment/follow widget that's in every page's static template.
- [dm-encryption.md](dm-encryption.md) — How is a direct message encrypted, delivered server-to-server with no broker, stored where its own server can't read it, and unlocked again in your browser? Pull the thread through the epoch keyring (`keyring.go`), the browser-only password upgrade (`dm.js` / `epoch.go`), point-to-point signed delivery (`send.go` / `receive.go`), the ciphertext-at-rest mailbox (`mailbox.go`), and out to the DM-encryption concept doc.

## Adding a tour

Tours are an *escalation*, not a default. The criterion: a reader following the trail markers alone leaves wanting more. When that becomes consistent for a given thread, escalate it.

A new tour should:

1. Live at `docs/handbook/<thread-name>.md`.
2. Open with the observable phenomenon — the surface thing a dev would notice and wonder about.
3. Walk the source files in their natural order along the thread, with code excerpts and explanation.
4. Cite each file by GitHub path (`github.com/vdibart/polis-cli/blob/main/...`) so the reader can click through.
5. End with "pull the thread" pointers into the concept docs.
6. Get cross-referenced from [`AGENTS.md`](../../AGENTS.md) (the threads table) and from each source file's trail marker.
