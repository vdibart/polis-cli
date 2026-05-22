// Apply cached themes before first paint to prevent FOUC. The server
// injects data-site-theme directly into the <html> tag (spaHandler reads
// bundle.GetActiveThemeName and substitutes the {{theme_attr}}
// placeholder) — that's authoritative for first paint. localStorage is
// the fallback when the server-injected attribute is absent.
//
// CRITICAL: always set data-theme (defaulting to dark) so the selector
// `:root:not([data-theme])` in style.css does NOT match. That selector
// has specificity (0,0,2,0) — higher than nav-themes.css's
// `html[data-site-theme="X"]` at (0,0,1,1) — so when no data-theme is
// set, the style.css sols-default wins the cascade and body paints
// sols even with a correct data-site-theme injection on the html tag.
// Setting data-theme=dark unconditionally makes `[data-theme="dark"]`
// match instead (0,0,1,0), which loses to nav-themes' (0,0,1,1) — the
// theme-correct rule wins.
//
// Lives in an external file (R18-21) so the admin SPA can ship a
// strict CSP without 'unsafe-inline' / hash / nonce on this script.
// Loaded at the top of <head> WITHOUT defer/async so it executes
// before any subsequent rendering.
(function () {
    try {
        var t = localStorage.getItem('polis-webapp-theme') || 'dark';
        document.documentElement.dataset.theme = t;
        if (!document.documentElement.dataset.siteTheme) {
            var st = localStorage.getItem('polis-site-theme');
            if (st) document.documentElement.dataset.siteTheme = st;
        }
    } catch (e) {
        document.documentElement.dataset.theme = 'dark';
    }
})();
