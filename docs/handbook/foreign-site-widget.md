# Tour: The injected widget on the logged-in visitor template

> A guided tour of how a logged-in polis visitor's nav appears on someone else's polis site, plus the companion comment/follow widget. Source-of-truth concept docs live in [`../general/`](../general/); this tour walks the source code with you. Map of all threads: [`../../AGENTS.md`](../../AGENTS.md).

## The observation

Sign in to your polis site (`<you>.polis.pub`). Now go visit somebody else's polis site — `alice.polis.pub`. You notice immediately:

- A nav bar appears at the top, styled in **your** theme, with **your** avatar and handle. It autohides after a couple seconds and reveals on hover at the top edge.
- The host's "sign in" link is gone — replaced by your handle label on the right.
- The host's wordmark on the left also vanishes when your nav arrives.
- Below the host's bio, a polis comment/follow widget appears as a clean button or comment box.

Open DevTools. View-source on Alice's page. The HTML looks identical to what an anonymous visitor would see — same wordmark, same "sign in" link, same empty placeholder div. But by the time the page paints, your nav is there. **Something is rewriting the page between the server and your screen.**

That something is the **serve-time nav injection**. And it's a different mechanism from the **comment/follow widget**, which is in the page's static template and loaded for everyone. This tour walks both.

## The two injections

```
   ┌───────────────────────────────────────────────────────────────────────┐
   │                                                                       │
   │  Per-tenant static HTML on alice.polis.pub/posts/2026/01/hello.html   │
   │                                                                       │
   │   ┌─ rendered at PUBLISH time, identical for all visitors ─────────┐  │
   │   │                                                                │  │
   │   │   <div id="polis-nav-root" data-home="" ...></div>     ◄─── 1  │  │
   │   │   <a class="polis-wordmark" href="https://polis.pub/">polis</a>│  │
   │   │   ...                                                          │  │
   │   │   <a class="polis-signin" href="https://polis.pub/">sign in</a>│  │
   │   │   ...                                                          │  │
   │   │   <div id="polis-widget" data-author="alice.polis.pub">  ◄─── 2│  │
   │   │   <script src="https://polis.pub/widget-1.4.4.js"              │  │
   │   │           integrity="sha384-...">                              │  │
   │   │                                                                │  │
   │   └────────────────────────────────────────────────────────────────┘  │
   │                                                                       │
   │   ┌─ patched at SERVE time, only for authenticated visitors ───────┐  │
   │   │                                                                │  │
   │   │   #1 placeholder's empty attrs filled with VISITOR data        │  │
   │   │   <script src="https://polis.pub/nav.js" defer></script>       │  │
   │   │   (appended right before </body>)                              │  │
   │   │                                                                │  │
   │   └────────────────────────────────────────────────────────────────┘  │
   │                                                                       │
   └───────────────────────────────────────────────────────────────────────┘

      Both scripts load from polis.pub, fetch state, render into shadow/regular DOM.

      Result:
        - For anonymous visitors: comment widget renders an "unknown" state;
          nav placeholder stays empty; the host's wordmark + sign-in stay visible.
        - For logged-in visitors: nav widget renders into the placeholder; the
          comment widget detects "connected" state and shows a reply CTA.
```

The two scripts (`widget.js` and `nav.js`) are loaded from `polis.pub` — they're not part of the per-tenant content. The hosted webapp serves both as embedded assets on the canonical domain, versioned by URL, integrity-protected by SRI. Tenants don't host or update the widget code; updating `polis.pub`'s binary updates the widget for every tenant simultaneously.

We'll walk each injection separately.

---

## Injection 1: The nav widget (serve-time)

This is the one that surprises most developers. The static HTML on `alice.polis.pub` has an **empty placeholder** at publish time. The page that arrives at your browser has the placeholder *filled in*. Where does the patch come from?

### 1.a Publish time: empty placeholder

In the shape template ([`cli-go/pkg/bundle/fixtures/pub.polis.core/shapes/v4/stream.html`](https://github.com/vdibart/polis-cli/blob/main/cli-go/pkg/bundle/fixtures/pub.polis.core/shapes/v4/stream.html)):

```html
<!-- The hosted nav-widget populates #polis-nav-root for authenticated
     visitors; CSS hides the wordmark when the widget root has children. -->
<a class="polis-wordmark" href="https://polis.pub/">polis</a>
<div id="polis-nav-root" data-home="{{widget_home}}" data-handle="{{widget_handle}}" data-host="{{widget_host}}"></div>
...
<a class="polis-signin" href="https://polis.pub/">sign in</a>
```

At render time, `{{widget_home}}`, `{{widget_handle}}`, `{{widget_host}}` resolve to **empty strings** — the rendering pipeline has no visitor context. The placeholder is there, but inert. The host's wordmark + sign-in link are visible.

The CSS in `stream.css` has a clever rule:

```css
/* When the placeholder has children, hide the wordmark + sign-in. */
.polis-topbar:has(#polis-nav-root:not(:empty)) .polis-wordmark,
.polis-topbar:has(#polis-nav-root:not(:empty)) .polis-signin { display: none; }
```

This is the *handshake*. The static HTML is correct for anonymous visitors out of the box. The presence of children inside `#polis-nav-root` is the signal "switch to logged-in mode."

### 1.b Serve time: the patch ([`webapp/internal/hosted/nav_inject.go`](https://github.com/vdibart/polis-cli/blob/main/webapp/internal/hosted/nav_inject.go))

When `<you>.polis.pub` requests `alice.polis.pub/posts/2026/01/hello.html` *while logged in*, the hosted webapp's response path runs the page through `injectNavWidgetPlaceholder`:

```go
func injectNavWidgetPlaceholder(data []byte, visitorHandle, handle, baseDomain string) []byte {
    if visitorHandle == "" {
        // Anonymous visitors get the page unchanged — host's "sign in"
        // anchor stays visible; nav script never loads.
        return data
    }
    homeURL := fmt.Sprintf("https://%s.%s", visitorHandle, baseDomain)
    hostHandle := fmt.Sprintf("%s.%s", handle, baseDomain)
    hydratedAttrs := fmt.Sprintf(
        `<div id="polis-nav-root" data-home="%s" data-handle="%s" data-host="%s" data-base="%s">`,
        homeURL, visitorHandle, hostHandle, baseDomain)
    // ... find the placeholder, swap in hydratedAttrs ...

    // Append nav.js script tag before </body>. `defer` matters — nav.js
    // must run before the v4 stream controller binds, so any DOM moves
    // it makes (e.g., repurposing .polis-signin into a handle-label) are
    // visible to the controller.
    scriptTag := fmt.Sprintf(`<script src="https://%s/nav.js" defer></script>`, baseDomain)
    // ... inject before </body> ...
}
```

Two things this function does:

1. **Patch the placeholder** — find `<div id="polis-nav-root"`, replace its attributes with hydrated ones that point at the visitor's home and identify the host being viewed.
2. **Inject the script tag** — append `<script src="https://polis.pub/nav.js" defer></script>` right before `</body>`.

The unauthenticated path is a no-op: `if visitorHandle == "" { return data }`. Anonymous visitors get the page byte-identical to what was rendered. **The widget injection is fundamentally a per-request transformation, not a publish-time decision.**

This function is called from `serveTenantPublic` (in [`hosted.go`](https://github.com/vdibart/polis-cli/blob/main/webapp/internal/hosted/hosted.go)), which delegates to a shared serving handler from `webapp/internal/serve/` and supplies the auth-aware post-process hook.

### 1.c Client side: hydration ([`webapp/internal/hosted/nav/nav.js`](https://github.com/vdibart/polis-cli/blob/main/webapp/internal/hosted/nav/nav.js))

By the time the browser parses the page, `#polis-nav-root` has populated `data-home` / `data-handle` / `data-host`, and `<script src="https://polis.pub/nav.js" defer>` is queued.

Once `nav.js` runs:

```javascript
var root = document.getElementById('polis-nav-root');
if (!root) return;

var homeURL = root.getAttribute('data-home');
var handle = root.getAttribute('data-handle');
var hostDomain = root.getAttribute('data-host') || '';
var baseDomain = root.getAttribute('data-base') || 'polis.pub';
```

If `homeURL` is empty, this is an anonymous-visitor page that somehow ended up loading nav.js — bail. Otherwise, the script:

1. **Fetches the visitor's nav state** — cross-origin `GET https://<visitor>.polis.pub/api/nav/state` to retrieve their theme variables, avatar config, follower/following/post counts, badge-dot state. Their own site is the source of truth for their identity; nav.js is just a renderer.
2. **Renders the avatar + icon row + handle label + (eventually) sentence-filter** into `#polis-nav-root`, applying the visitor's theme variables as inline CSS custom properties on the root.
3. **Repurposes the host's `.polis-signin` anchor** as the visitor's handle label, anchored to the right side of the topbar — visually distinguishing where they are vs whose site they're on.
4. **Wires the autohide behavior** — 2-second linger then slide up to `height: 0` over 250ms; reveal trigger zone is a 6px-tall strip at the top of the viewport.

The CSS `:has(#polis-nav-root:not(:empty))` rule then automatically hides the host's wordmark and sign-in link, since the placeholder now has children. The handshake closes.

### 1.d Why this design

A few elegant properties fall out of "patch at serve time":

- **Per-tenant static HTML stays identical for every visitor.** No content forking based on auth. Easy to cache, easy to mirror, no per-visitor render cost.
- **Authentication is a server-side concern.** The browser never decides "am I logged in?" — the server decides and patches accordingly. No client-side flicker between anonymous and logged-in states.
- **CSS contains the visual coordination.** The wordmark/sign-in hide automatically when children appear, with no extra JS. Two rules, one signal, fully reversible (clear `#polis-nav-root`'s children and the unauth chrome comes back).
- **The widget code is centralized.** `nav.js` lives once at `polis.pub/nav.js`, served from the hosted webapp's embedded bytes. Every tenant page that loads it pulls the same byte-identical copy. SRI integrity (next section) ensures it can't be tampered with mid-flight.

---

## Injection 2: The comment/follow widget (static-template, runtime state)

This is the *other* injected widget — the one that shows the comment box or follow button below a post. Different mechanism from the nav widget. We'll walk it briefly because it appears in the same DOM neighborhood.

### 2.a Template ([`stream.html`](https://github.com/vdibart/polis-cli/blob/main/cli-go/pkg/bundle/fixtures/pub.polis.core/shapes/v4/stream.html))

The comment/follow widget's container and script tag are in the static template:

```html
<div class="site-follow" id="polis-widget-follow" data-author="{{author_domain}}"></div>
...
<div id="polis-widget" data-author="{{author_domain}}" data-post-url="{{canonical_url}}"></div>
...
<script src="https://{{base_domain}}/widget-{{widget_version}}.js"
        integrity="{{widget_integrity}}" crossorigin="anonymous"></script>
```

Every visitor — anonymous or logged-in — gets this script tag. Versioned URL (`/widget-1.4.4.js`), SRI integrity attribute pinning the bytes. If the bytes get tampered with mid-flight, the browser refuses to execute.

### 2.b Embedded asset ([`webapp/internal/hosted/widget_embed.go`](https://github.com/vdibart/polis-cli/blob/main/webapp/internal/hosted/widget_embed.go))

```go
//go:embed widget/widget.js
var WidgetJS []byte

var WidgetIntegrity string

func init() {
    if len(WidgetJS) > 0 {
        widgetJSEtag = fmt.Sprintf(`"widget-%s"`, WidgetVersion)
        sum := sha512.Sum384(WidgetJS)
        WidgetIntegrity = "sha384-" + base64.StdEncoding.EncodeToString(sum[:])
    }
}
```

The widget JavaScript is embedded into the hosted binary at compile time and the SHA-384 integrity hash is computed once at init. When `polis.pub` serves `/widget-1.4.4.js`, it streams the embedded bytes. The template's `{{widget_integrity}}` variable resolves to the SRI string, so the browser will refuse to run anything that doesn't match.

### 2.c State machine ([`webapp/internal/hosted/widget/widget.js`](https://github.com/vdibart/polis-cli/blob/main/webapp/internal/hosted/widget/widget.js))

When the widget script runs, it inspects local storage and `*.polis.pub` cookies to determine state:

- **`unknown`** — no stored polis identity. Renders a "sign up & comment" CTA.
- **`known`** — has a `polis_instance` cookie but no widget token. Renders a "sign in to comment" CTA that walks back to the visitor's home.
- **`connected`** — has both `polis_instance` and `widget_token`. Renders a reply / comment textarea inline.
- **`owner`** — stored instance URL matches `data-author`. Renders an "edit on dashboard" link (you're looking at your own site).

The state machine + rendering happen inside a **closed Shadow DOM** rooted at `#polis-widget` (and `#polis-widget-follow`). Shadow DOM isolation keeps the widget's CSS from leaking into the host page and vice versa — the host can use any theme without breaking the widget's typography.

### 2.d Cross-tenant identity

Both widgets share identity state via cookies scoped to `.polis.pub`:

```javascript
document.cookie = 'polis_instance=' + encodeURIComponent(instanceUrl) +
                  '; domain=.polis.pub; path=/; max-age=31536000; SameSite=Lax; Secure';
```

Because the cookie domain is `.polis.pub`, any subdomain (`alice.polis.pub`, `bob.polis.pub`, `polis.pub` itself) can read it. The visitor's identity "roams" across tenants without OAuth, without third-party cookies, without per-tenant accounts. The same widget JavaScript on every site uses the same shared identity.

This is also how `nav.js` knows you're logged in: when the page is requested, the hosted server reads the visitor's session cookie (or a `widget_token` cookie) and decides whether to call `injectNavWidgetPlaceholder` with a real `visitorHandle` or with the empty string that no-ops the injection.

---

## Versioning + integrity ([`cli-go/pkg/render/page.go`](https://github.com/vdibart/polis-cli/blob/main/cli-go/pkg/render/page.go))

Both widget scripts are versioned at the URL level:

```go
// WidgetVersion is the single source of truth for the current polis widget version.
// Update this constant when widget.js changes. Theme snippets reference it via
// the {{widget_version}} template variable, so they never need manual version bumps.
const WidgetVersion = "1.4.4"
```

The render pipeline stamps `{{widget_version}}` into every emitted HTML page, so the `<script src="/widget-X.Y.Z.js">` always points at the version that was current at publish time. The hosted server's `serveWidgetJS` handles versioned requests:

- Current version → serve embedded bytes with `Cache-Control: max-age=86400`.
- Old version → `301 Moved Permanently` to the current version, with a `Cache-Control: max-age=3600` redirect cache.

So published HTML from a year ago still links to a working script — it just gets redirected to the current one. The SRI integrity attribute moves with the redirect because the template is re-rendered with the current `{{widget_integrity}}` whenever the page is regenerated. If you have an old un-re-rendered page from before a widget update, the SRI hash in its `<script>` tag won't match the new bytes, and the browser will refuse to execute — which is the right failure mode: better a broken widget than a tampered one.

---

## Pull the thread

A few concept docs give you the philosophical context:

- **[`../general/architecture.md`](../general/architecture.md)** — polis.pub-as-implementation vs polis-the-protocol. The injected widget is a polis.pub feature; the protocol doesn't require it. A self-hosted polis site can opt to use the same widget (pointing back at polis.pub) or roll its own.
- **[`../general/snap-off-architecture.md`](../general/snap-off-architecture.md)** — The widget hosting is a snap-off layer. Different tenants can use different widget implementations; the contract is the placeholder + the `<script>` tag.
- **[`../general/security-model.md`](../general/security-model.md)** — Why SRI matters here (CDN-compromise blast radius), why widget tokens are separate from session tokens, why cross-tenant cookies need `SameSite=Lax`.
- **[`../webapp/designer/navigation.md`](../webapp/designer/navigation.md)** — The nav anatomy the injected nav widget renders into. Same nav design as the owner SPA; different hydration path.
- **[`../webapp/designer/theme-system.md`](../webapp/designer/theme-system.md)** — Cross-theme compatibility rules. Your nav (in your theme) sits on top of their content (in their theme). The widget code obeys these rules.

## What you should now understand

If you followed the tour end-to-end:

- The "logged-in visitor template" isn't a different template. It's the **same per-tenant HTML**, **patched at serve time** when the requester is authenticated.
- The nav widget injection is a serve-time transformation (`injectNavWidgetPlaceholder`) — patch a placeholder, append a script tag, done. CSS handles the visual coordination via `:has()` selectors. No client-side flicker.
- The comment/follow widget is *not* injected at serve time — it's in the static template, loaded by every visitor, with a state machine that decides what UI to render based on cross-tenant cookies.
- Both widget scripts are embedded into the polis.pub binary, versioned by URL, SRI-pinned by the rendered HTML, served only from `polis.pub` — so updating the binary updates every tenant's widget simultaneously.
- Cross-tenant identity roams via `.polis.pub`-scoped cookies. No OAuth, no per-tenant accounts, no third-party-cookie shenanigans.

If you want to go deeper:

- The starter map of every thread: [`../../AGENTS.md`](../../AGENTS.md)
- The owner-SPA equivalent of this nav (same anatomy, different hydration): [`url-as-filter.md`](url-as-filter.md) → that tour walks `app.js`, which is the *owner-SPA* nav driver
- How DS data flows in to populate badge dots in either nav: [`ds-to-stream.md`](ds-to-stream.md)
