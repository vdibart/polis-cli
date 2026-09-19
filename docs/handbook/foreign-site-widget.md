# Tour: The injected widget on the logged-in visitor template

*For* [Contributors](../README.md#contributing-to-polis) — *Kind* [Tour](../README.md#kinds-of-page) — *See also* [concept](../general/concepts/architecture.md)

> A guided tour of how a logged-in polis visitor's nav appears on someone else's polis site, plus the companion comment/follow widget. Source-of-truth concept docs live in [`../general/`](../general/); this tour walks the source code with you. Map of all threads: [`../../AGENTS.md`](../../AGENTS.md).

## The observation

Sign in to your polis site (`<you>.polis.pub`). Now go visit somebody else's polis site — `alice.polis.pub`. You notice immediately:

- The host's topbar gains **your** avatar and icon row, painted in **your** theme.
- The host's "sign in" link is gone — replaced by your handle label on the right.
- The host's wordmark on the left also vanishes when your nav arrives.
- Below the host's bio, a polis comment/follow widget appears as a clean button or comment box.

Now compare what an anonymous visitor would be served: the same wordmark, the same "sign in" link, and an *empty* placeholder div. Your copy has that placeholder filled in and one extra script tag. By the time the page paints, your nav is there. **Something is rewriting the page between the server and your screen.**

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
   │   │   <script src="https://polis.pub/widget-1.4.5.js" defer>       │  │
   │   │                                                                │  │
   │   └────────────────────────────────────────────────────────────────┘  │
   │                                                                       │
   │   ┌─ patched at SERVE time, for every visitor ─────────────────────┐  │
   │   │                                                                │  │
   │   │   #2 script tag gains integrity="sha384-..."                   │  │
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

The two scripts (`widget.js` and `nav.js`) are loaded from `polis.pub` — they're not part of the per-tenant content. The hosted webapp serves both as embedded assets on the canonical domain. `widget.js` is versioned by URL and integrity-protected by SRI; `nav.js` is served at one unversioned URL with a one-hour cache and no SRI attribute. Tenants don't host or update the widget code; updating `polis.pub`'s binary updates the widget for every tenant simultaneously.

We'll walk each injection separately.

---

## Injection 1: The nav widget (serve-time)

This is the one that surprises most developers. The static HTML on `alice.polis.pub` has an **empty placeholder** at publish time. The page that arrives at your browser has the placeholder *filled in*. Where does the patch come from?

### 1.a Publish time: empty placeholder

In the shape template ([`cli-go/pkg/bundle/fixtures/pub.polis.core/shapes/v4/stream.html`](https://github.com/vdibart/polis-cli/blob/main/cli-go/pkg/bundle/fixtures/pub.polis.core/shapes/v4/stream.html)):

```html
<a class="polis-wordmark" href="https://polis.pub/">polis</a>
<div id="polis-nav-root" data-home="{{widget_home}}" data-handle="{{widget_handle}}" data-host="{{widget_host}}"></div>
...
<a class="polis-signin" href="https://polis.pub/">sign in</a>
```

At render time, `{{widget_home}}`, `{{widget_handle}}`, `{{widget_host}}` resolve to **empty strings** — the rendering pipeline has no visitor context. The placeholder is there, but inert. The host's wordmark + sign-in link are visible.

The CSS in `stream.css` has two rules for this:

```css
/* When the nav widget populates #polis-nav-root, hide the static wordmark
   so the icon-row chrome can take its place. */
.polis-topbar:has(#polis-nav-root:not(:empty)) .polis-wordmark {
    display: none;
}

/* Hide the static sign-in once the nav widget hydrates */
.polis-topbar:has(.pn-nav) .polis-signin,
.polis-topbar:has(.pn-fallback) .polis-signin {
    display: none;
}
```

This is the *handshake*. The static HTML is correct for anonymous visitors out of the box. Children inside `#polis-nav-root` (and the `.pn-nav` class `nav.js` adds to it) are the signal "switch to logged-in mode."

### 1.b Serve time: the patch (`webapp/internal/hosted/hosted.go`, `injectNavWidgetPlaceholder`)

When you request `alice.polis.pub/posts/2026/01/hello.html` *while logged in as a different tenant*, the hosted webapp's response path runs the page through `injectNavWidgetPlaceholder` (abridged):

```go
func injectNavWidgetPlaceholder(data []byte, visitorHandle, handle, baseDomain, hostTheme string) []byte {
	if visitorHandle == "" {
		// Logged-out visitors keep the host's "sign in" anchor; no
		// widget chrome layered on top. Callers gate at
		// serveTenantPublic; this guard is defense-in-depth.
		return data
	}
	homeURL := fmt.Sprintf("https://%s.%s", visitorHandle, baseDomain)
	hostHandle := fmt.Sprintf("%s.%s", handle, baseDomain)
	// data-host-theme carries the AUTHOR tenant's active theme name so
	// nav.js can pick an owner-themed sentence filter ...
	hydratedAttrs := fmt.Sprintf(
		`<div id="polis-nav-root" data-home="%s" data-handle="%s" data-host="%s" data-base="%s" data-host-theme="%s">`,
		homeURL, visitorHandle, hostHandle, baseDomain, hostTheme)
	// ... find the placeholder, swap in hydratedAttrs ...

	// Inject the nav.js script tag right before </body>. `defer`
	// preserves document order — nav.js runs before the v4 stream
	// controller, so the controller picks up any DOM moves the widget
	// makes (e.g. relocating the polis-signin into a handle-label
	// position) before binding handlers.
	scriptTag := fmt.Sprintf(`<script src="https://%s/nav.js" defer></script>`, baseDomain)
	// ... inject before </body> ...
}
```

Two things this function does:

1. **Patch the placeholder** — find `<div id="polis-nav-root"`, replace its attributes with hydrated ones that point at the visitor's home and identify the host being viewed.
2. **Inject the script tag** — append `<script src="https://polis.pub/nav.js" defer></script>` right before `</body>`.

The unauthenticated path is a no-op: `if visitorHandle == "" { return data }`. The same holds when you visit your *own* site — the caller only sets `visitorHandle` when the session's handle differs from the tenant being served. **The widget injection is fundamentally a per-request transformation, not a publish-time decision.**

This function is called from `publicHTMLPostProcess` (in `hosted.go`), the post-process hook that `serveTenantPublic` hands to the shared serving handler in `webapp/internal/serve/`. The same hook also runs `injectWidgetIntegrity` for every visitor (see 2.b), and serves the `/pql/` HTML landing as well as static pages.

### 1.c Client side: hydration (`webapp/internal/hosted/nav/nav.js`)

By the time the browser parses the page, `#polis-nav-root` has populated `data-home` / `data-handle` / `data-host`, and `<script src="https://polis.pub/nav.js" defer>` is queued.

Once `nav.js` runs:

```javascript
var root = document.getElementById('polis-nav-root');
if (!root) return;

var homeURL = root.getAttribute('data-home');
var handle = root.getAttribute('data-handle');
var hostDomain = root.getAttribute('data-host') || '';
var baseDomain = root.getAttribute('data-base') || 'polis.pub';
var hostTheme = root.getAttribute('data-host-theme') || '';  // AUTHOR's active theme (owner-themed filter)
```

If `homeURL` is empty, this is an anonymous-visitor page that somehow ended up loading nav.js — bail. Otherwise, the script:

1. **Fetches the visitor's nav state** — cross-origin `GET https://<visitor>.polis.pub/api/nav/state` to retrieve their theme colours, avatar config, counts and badge-dot state. Their own site is the source of truth for their identity; nav.js is just a renderer.
2. **Renders the avatar (with its hover menu) and the icon row** into `#polis-nav-root`, adds the `pn-nav` class, and applies the visitor's theme colours as inline CSS custom properties. Each icon links back to the matching PQL view on the visitor's *own* dashboard. The sentence filter in the middle of the bar is the **host's** — nav.js recolours it, in the visitor's theme or, where the host's theme defines filter colours, the host's (`data-host-theme`).
3. **Repurposes the host's `.polis-signin` anchor** as the visitor's handle label (`polis-handle-label`) at the right side of the topbar, linking to the visitor's home.

The two CSS rules above then hide the host's wordmark and sign-in link, since the placeholder now has children and the `pn-nav` class. The handshake closes.

### 1.d Why this design

A few elegant properties fall out of "patch at serve time":

- **Per-tenant static HTML stays identical for every visitor.** No content forking based on auth. Easy to cache, easy to mirror, no per-visitor render cost.
- **Authentication is a server-side concern.** The browser never decides "am I logged in?" — the server decides and patches accordingly. No client-side flicker between anonymous and logged-in states.
- **CSS contains the visual coordination.** The wordmark/sign-in hide automatically when children appear, with no extra JS. Two rules, one signal, fully reversible (clear `#polis-nav-root`'s children and the unauth chrome comes back).
- **The widget code is centralized.** `nav.js` lives once at `polis.pub/nav.js`, served from the hosted webapp's embedded bytes. Every tenant page that loads it pulls the same byte-identical copy. ⚠️ Unlike `widget.js` (next section), its script tag carries no SRI attribute.

---

## Injection 2: The comment/follow widget (static-template, runtime state)

This is the *other* injected widget — the one that shows the comment box or follow button below a post. Different mechanism from the nav widget. We'll walk it briefly because it appears in the same DOM neighborhood.

### 2.a Template ([`stream.html`](https://github.com/vdibart/polis-cli/blob/main/cli-go/pkg/bundle/fixtures/pub.polis.core/shapes/v4/stream.html))

The comment/follow widget's container and script tag are in the static template:

```html
<div class="site-follow" id="polis-widget-follow" data-author="{{author_domain}}"></div>
...
<div id="polis-widget"
     data-page-type="post"
     data-post-url="{{url}}"
     data-author="{{author_domain}}">
    <!-- noscript + fallback links -->
</div>
<script src="https://polis.pub/widget-{{widget_version}}.js"
        crossorigin="anonymous" defer></script>
```

Every visitor — anonymous or logged-in — gets this script tag, at a versioned URL (`/widget-1.4.5.js`). The rendered HTML carries **no** integrity attribute: the hosted server adds one at serve time (2.b), so the pin always matches the bytes that server ships. If the bytes get tampered with in transit, the browser refuses to execute.

### 2.b Embedded asset and serve-time SRI (`webapp/internal/hosted/widget_embed.go`, `injectWidgetIntegrity` in `hosted.go`)

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

The widget JavaScript is embedded into the hosted binary at compile time and the SHA-384 integrity hash is computed once at init. When `polis.pub` serves `/widget-1.4.5.js`, it streams the embedded bytes. When it serves a tenant's HTML page, `injectWidgetIntegrity` finds the `<script src="https://polis.pub/widget-` tag and inserts `integrity="sha384-..."` (skipping a tag that already has one), so the browser will refuse to run anything that doesn't match. A page served by anything other than the hosted server — a self-hosted site, a static mirror — has no integrity attribute.

### 2.c State machine (`webapp/internal/hosted/widget/widget.js`)

When the widget script runs, it reads stored credentials — first the per-origin `localStorage` key `polis_widget` (a JSON object with `instance_url` and `widget_token`), then, as a fallback, the `polis_instance` and `polis_widget_token` cookies scoped to `.polis.pub` — and picks a state:

- **`unknown`** — no stored polis identity. Renders a "sign up & comment" CTA.
- **`owner`** — the stored instance's domain matches `data-author` (checked first). Renders an "edit" link (you're looking at your own site).
- **`known`** — an instance URL but no widget token. Renders a "sign in to comment" CTA that walks back to the visitor's home.
- **`connected`** — both an instance URL and a widget token. Renders a reply / comment textarea inline.

A token arriving in the URL fragment (`#polis_widget_token=...`) is stored and stripped from the address bar.

The state machine + rendering happen inside a **closed Shadow DOM** rooted at `#polis-widget` (and `#polis-widget-follow`). Shadow DOM isolation keeps the widget's CSS from leaking into the host page and vice versa — the host can use any theme without breaking the widget's typography.

### 2.d Cross-tenant identity

The comment/follow widget writes its identity to cookies scoped to `.polis.pub` as well as to `localStorage` (which is per-origin):

```javascript
var cookieOpts = '; domain=.polis.pub; path=/; max-age=31536000; SameSite=Lax; Secure';
document.cookie = 'polis_instance=' + encodeURIComponent(instanceUrl) + cookieOpts;
if (token) {
  document.cookie = 'polis_widget_token=' + encodeURIComponent(token) + cookieOpts;
}
```

Because the cookie domain is `.polis.pub`, any subdomain (`alice.polis.pub`, `bob.polis.pub`, `polis.pub` itself) can read it. The visitor's identity "roams" across tenants without OAuth, without third-party cookies, without per-tenant accounts. The same widget JavaScript on every site uses the same shared identity.

The nav widget does not use these cookies. Whether it appears is decided on the server: when the page is requested, the hosted server looks up the visitor's **session** (`getSession`) and passes its handle as `visitorHandle` — or the empty string that no-ops the injection.

---

## Versioning + integrity ([`cli-go/pkg/render/page.go`](https://github.com/vdibart/polis-cli/blob/main/cli-go/pkg/render/page.go))

`widget.js` is versioned at the URL level (`nav.js` is not):

```go
// WidgetVersion is the single source of truth for the current polis widget version.
// Update this constant when widget.js changes. Theme snippets reference it via
// the {{widget_version}} template variable, so they never need manual version bumps.
const WidgetVersion = "1.4.5"
```

The render pipeline stamps `{{widget_version}}` into every emitted HTML page, so the `<script src="/widget-X.Y.Z.js">` always points at the version that was current at publish time. The hosted server's `serveWidgetJS` handles versioned requests:

- Current version → serve embedded bytes with `Cache-Control: public, max-age=86400`.
- Old version → `302 Found` to the current version, deliberately with `Cache-Control: no-store` — a `301`/cached redirect would let Cloudflare or the browser pin a redirect that goes stale on the next widget bump, so the redirect itself is never cached.

So published HTML from a year ago still links to a working script — it just gets redirected to the current one. The integrity attribute keeps up because it is never in the published file: `injectWidgetIntegrity` stamps the current hash into every page as it is served, so an old page and a new one both pin the bytes the running binary ships. ⚠️ The hash describes the *current* bytes, so a page whose tag still names an old version pins the bytes it will be redirected to, not the version its URL names.

---

## Pull the thread

A few concept docs give you the philosophical context:

- **[`../general/concepts/architecture.md`](../general/concepts/architecture.md)** — polis.pub-as-implementation vs polis-the-protocol. The injected widget is a polis.pub feature; the protocol doesn't require it. A self-hosted polis site can opt to use the same widget (pointing back at polis.pub) or roll its own.
- **[`../general/concepts/snap-off-architecture.md`](../general/concepts/snap-off-architecture.md)** — The widget hosting is a snap-off layer. Different tenants can use different widget implementations; the contract is the placeholder + the `<script>` tag.
- **[`../general/security/security-model.md`](../general/security/security-model.md)** — Why SRI matters here (CDN-compromise blast radius), why widget tokens are separate from session tokens, why cross-tenant cookies need `SameSite=Lax`.
- **[`../webapp/designer/navigation.md`](../webapp/designer/navigation.md)** — The nav anatomy the injected nav widget renders into. Same nav design as the owner SPA; different hydration path.
- **[`../general/concepts/themes.md`](../general/concepts/themes.md#cross-theme-compatibility)** — Cross-theme compatibility rules. Your nav (in your theme) sits on top of their content (in their theme). The widget code obeys these rules.

## What you should now understand

If you followed the tour end-to-end:

- The "logged-in visitor template" isn't a different template. It's the **same per-tenant HTML**, **patched at serve time** when the requester is authenticated.
- The nav widget injection is a serve-time transformation (`injectNavWidgetPlaceholder`) — patch a placeholder, append a script tag, done. CSS handles the visual coordination via `:has()` selectors. No client-side flicker.
- The comment/follow widget is *not* injected at serve time — it's in the static template, loaded by every visitor, with a state machine that decides what UI to render based on cross-tenant cookies.
- Both widget scripts are embedded into the polis.pub binary and served only from `polis.pub` — so updating the binary updates every tenant's widget simultaneously. `widget.js` is also versioned by URL and SRI-pinned at serve time; `nav.js` is neither.
- The comment widget's identity roams via `.polis.pub`-scoped cookies; the nav widget's comes from the server-side session. No OAuth, no per-tenant accounts, no third-party-cookie shenanigans.

If you want to go deeper:

- The starter map of every thread: [`../../AGENTS.md`](../../AGENTS.md)
- The owner-SPA equivalent of this nav (same anatomy, different hydration): [`url-as-filter.md`](url-as-filter.md) → that tour walks `app.js`, which is the *owner-SPA* nav driver
- How DS data flows in to populate badge dots in either nav: [`ds-to-stream.md`](ds-to-stream.md)
