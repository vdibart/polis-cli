// Polis owner-extras — Layer 3 / Layer 4 extension surface.
//
// Loaded ONLY by the owner SPA's index.html (alongside the v4 stream
// controller served from /bundle-assets/pub.polis.core/shapes/v4/
// stream.js). Public per-post artifacts never load this file, so the
// extension registries on window.PolisStream stay empty for non-auth
// visitors — the layered model the architectural pivot describes
// (CSS converges, code diverges along file boundaries) is enforced
// here.
//
// What lives here (now and in subsequent step-6 sub-steps):
//   - 6.d (this file): skeleton + readiness check + onEscape registration
//     for the avatar dropdown menu. The Edit About menu item is wired
//     in index.html directly (calls App.openAboutEditor); owner-extras
//     doesn't need to mediate.
//   - 6.e: icon-row preset buttons (gateway/paragraph/comment/people/
//     envelope/edit) — calls PolisStream.setFilterScope/Type/etc.
//     to load preset sentences. Indicator-dot wiring against existing
//     /api/counts plumbing.
//   - 6.f.2: activity render mode — afterRender hooks per type that
//     consume notification rule templates from registry.json.
//   - 6.g: optional Layer-4 DM renderer override.
//   - 6.h: inline post editor card.
//   - 6.i: stream-item afterRender decorators (unpublish / bless / deny
//     hover affordances).
//   - 6.j: comment-WRITE CTA on canonical pages.
//
// API surface (window.PolisOwnerExtras):
//   .ready                 — Promise that resolves when window.PolisStream
//                            is available + body.is-owner is set.
//   (More methods land as sub-steps need them.)

(function () {
    'use strict';

    var POLL_INTERVAL_MS = 20;
    var POLL_MAX_ATTEMPTS = 50; // ~1s budget; matches app.js _activateStreamScreen retry cap

    // Wait for the v4 stream controller to be available on window. The
    // <script defer> ordering in index.html guarantees this in the happy
    // path; the poll is for the failure modes (404 / network / parse
    // error) where the controller never appears. Surface a clear toast
    // on exhaustion so silent-spin doesn't happen.
    function waitForController() {
        return new Promise(function (resolve, reject) {
            var attempts = 0;
            (function check() {
                if (window.PolisStream && typeof window.PolisStream.afterRender === 'function') {
                    resolve(window.PolisStream);
                    return;
                }
                attempts++;
                if (attempts > POLL_MAX_ATTEMPTS) {
                    reject(new Error('PolisStream controller did not load (max ' + POLL_MAX_ATTEMPTS + ' attempts)'));
                    return;
                }
                setTimeout(check, POLL_INTERVAL_MS);
            })();
        });
    }

    // Avatar dropdown — currently CSS-:hover-revealed (see style.css
    // .avatar-wrap:hover .avatar-menu rule). owner-extras adds keyboard-
    // dismiss support: pressing Esc with the menu hovered moves focus
    // off the .avatar-wrap so the :hover state drops and the menu
    // closes. This is the lightest-touch fix that doesn't require
    // converting the CSS hover to JS state.
    //
    // Why blur the wrap rather than add a class? :hover is set by the
    // browser based on cursor position; CSS can't override it from JS.
    // Blurring shifts focus, but doesn't move the cursor — so the menu
    // would only close if the cursor isn't over the menu. The actual
    // mechanism: focus-within is also a hover-trigger via CSS; blurring
    // any focused descendant + simulating a mouseleave is overkill for
    // a hover-only menu. For 6.d we register the onEscape hook as a
    // no-op stub for the avatar menu — the natural close (move cursor
    // away) is the dominant pattern. Future sub-steps that add JS-
    // toggled menus (compose surface in 6.h) will register real Esc
    // handlers via the same registry.
    function registerAvatarMenuEscape(stream) {
        // Stub registration so the onEscape registry has at least one
        // entry from owner-extras; future sub-steps add real handlers.
        // No-op for the avatar menu itself (CSS-hover-driven).
        stream.onEscape(function () {
            // Reserved for future menu-close behavior. Currently no-op:
            // the avatar menu closes naturally when the cursor leaves.
        });
    }

    // step-06/6.e: icon-row preset definitions.
    //
    // Each preset corresponds to one of the 5 sentence-loading icons in
    // the topbar (avatar | gateway | paragraph | comment | people |
    // envelope | sep | edit). Clicking the icon loads the preset's
    // filter sentence on the stream (navigating to /feed first if
    // needed), then emits pub.polis.stream.preset_loaded telemetry.
    //
    // The 'edit' icon is intentionally NOT in this map — it's the
    // inline editor trigger (lands in 6.h). For 6.e it falls through
    // to its existing onclick (App.navigateTo('/posts/new'), legacy
    // editor) so users can still compose; 6.h replaces.
    // R22 #12: qualifier locked to 'all' across every preset; the
    // 'new' option is no longer surfaced in the dropdown.
    var ICON_PRESETS = {
        gateway: {
            label: 'all activity from my network',
            // modifier:'by-date' must be present even though activity has no
            // modifier slot (FILTER_MODIFIER_OPTIONS_BY_TYPE has no 'activity'
            // entry, so updateModifierSlotVisibility hides it and the sentence
            // stays "all activity from my network"). stream.js's setFilter +
            // selectOption both FORCE filterModifier='by-date' on a type change
            // when the type has no modifier table, so the live filter state is
            // always {…, modifier:'by-date'} on this view. Omitting it here (as
            // the only preset that did) made filterMatchesPreset compare
            // 'by-date' !== '' and fail, so clicking the gateway icon while
            // already on this view fell through to loadPreset (reload) instead
            // of the scroll-to-top branch. The other four presets all specify
            // their modifier; this aligns gateway with them.
            filter: { qualifier: 'all', type: 'activity', scope: 'my-network', modifier: 'by-date' },
            display: { qualifierLabel: 'all', typeLabel: 'activity', scopeLabel: 'my network' },
        },
        paragraph: {
            label: 'all posts from me by date',
            filter: { qualifier: 'all', type: 'posts', scope: 'me', modifier: 'by-date' },
            display: { qualifierLabel: 'all', typeLabel: 'posts', scopeLabel: 'me', modifierLabel: 'by date' },
        },
        comment: {
            label: 'all comments from all polis to bless',
            filter: { qualifier: 'all', type: 'comments', scope: 'all-polis', modifier: 'to-bless' },
            display: { qualifierLabel: 'all', typeLabel: 'comments', scopeLabel: 'all polis', modifierLabel: 'to bless' },
        },
        people: {
            // 06-profiles: people icon rewires from the obsolete
            // type=follows preset to the new type=profiles surface.
            // Default qualifier is my-network (the natural "manage who
            // I follow" landing), default sort is by-name.
            label: 'all profiles from my network by name',
            filter: { qualifier: 'all', type: 'profiles', scope: 'my-network', modifier: 'by-name' },
            display: { qualifierLabel: 'all', typeLabel: 'profiles', scopeLabel: 'my network', modifierLabel: 'by name' },
        },
        envelope: {
            // R24 follow-up #4: dms locked to scope=my-mutuals
            // (DMs are between mutuals — drafts is the only type
            // that locks to scope=me). Preset values mirror the lock
            // so the rollover tooltip + loaded sentence stay
            // consistent with what the personal-lock will enforce.
            label: 'all messages from my mutuals by date',
            filter: { qualifier: 'all', type: 'dms', scope: 'my-mutuals', modifier: 'by-date' },
            display: { qualifierLabel: 'all', typeLabel: 'messages', scopeLabel: 'my mutuals', modifierLabel: 'by date' },
        },
    };

    // Exact-match comparator: does the current filter snapshot equal
    // the preset's stored filter, slot-for-slot? Used to distinguish
    // "user is parked on the preset's default" (click → scroll-to-top)
    // from "user is on a related sentence the preset's icon happens to
    // match" (click → reload preset to reset the scope/modifier).
    // Treats undefined/empty as equal so partially-populated snapshots
    // don't false-negative.
    function filterMatchesPreset(snapshot, presetFilter) {
        if (!snapshot || !presetFilter) return false;
        var fields = ['qualifier', 'type', 'scope', 'modifier'];
        for (var i = 0; i < fields.length; i++) {
            var f = fields[i];
            if ((snapshot[f] || '') !== (presetFilter[f] || '')) return false;
        }
        return true;
    }

    // Best-effort fire-and-forget telemetry for icon-preset usage.
    // Routes to POST /api/v1/event with an allowlisted event name —
    // server validates + LogEvent's via the standard structured-event
    // pipeline. Failure is silent (telemetry shouldn't block UX).
    function emitPresetLoadedEvent(presetName, filter) {
        try {
            fetch('/api/v1/event', {
                method: 'POST',
                headers: { 'Content-Type': 'application/json' },
                body: JSON.stringify({
                    event: 'pub.polis.stream.preset_loaded',
                    fields: {
                        preset_name: presetName,
                        qualifier: filter.qualifier || '',
                        type: filter.type || '',
                        scope: filter.scope || '',
                        modifier: filter.modifier || '',
                    },
                }),
                credentials: 'same-origin',
            }).catch(function () {});
        } catch (e) {
            // No-op — never let telemetry break UX.
        }
    }

    // Wire each icon button to its preset. Removes the legacy onclick
    // attribute the icon ships with (App.navigateTo, set in 6.a as a
    // placeholder) and replaces with a click listener that loads the
    // preset filter. Runs on DOMContentLoaded — buttons exist by then
    // (they're static markup in index.html).
    //
    // step-06/6.h: also wires the edit icon (not in ICON_PRESETS — it's
    // not a filter preset; it opens the inline editor card).
    function wireIconPresets(stream) {
        Object.keys(ICON_PRESETS).forEach(function (presetName) {
            var btn = document.getElementById('nav-btn-' + presetName);
            if (!btn) {
                console.warn('[polis-owner-extras] preset button #nav-btn-' + presetName + ' not found');
                return;
            }
            // Strip the inline onclick (index.html sets it to
            // App.navigateTo('/')) so this listener fully owns the click —
            // same pattern as the edit button below. The inline handler is
            // a parse-time listener, so it fires FIRST: navigateTo('/')
            // re-resolves the route, rewrites the URL to /_/, and
            // re-activates the stream screen (re-rendering the column).
            // That re-render stepped on the "already on this preset →
            // scroll to top" branch below, so clicking the active icon
            // reloaded the stream instead of scrolling up. removeAttribute
            // here preserves the boot-failure fallback: if owner-extras
            // never runs, wireIconPresets never strips the attribute, so
            // the inline navigateTo survives as the safety net.
            btn.removeAttribute('onclick');
            btn.addEventListener('click', function (ev) {
                // R27 follow-up: if this preset is already the active
                // icon, treat the click as "scroll to top" instead of
                // re-applying the same filter. Check activePresetName
                // (owner-extras' tracked state), NOT btn.classList —
                // the inline onclick fallback's navigateTo+_updateNavActive
                // sets gateway .active on every preset click since all
                // icons share view='conversations', so the DOM class
                // gives a false positive within this handler.
                //
                // 2026-05-18: tighten the same-icon check to "filter
                // exactly matches the preset's default" rather than just
                // "icon-family matches". matchIconForFilter is loose
                // (type=dms → envelope regardless of scope), so a user
                // on "all messages from @alice.polis.pub" would have
                // activePresetName === 'envelope' even though the
                // current sentence is NOT the envelope preset (which is
                // scope=my-mutuals). Clicking envelope from a scoped
                // DM thread should return the user to the messages
                // INBOX (preset filter), not scroll-to-top of the
                // current thread. Compare against ICON_PRESETS[name]
                // .filter field-by-field.
                if (activePresetName === presetName
                    && filterMatchesPreset(currentFilterSnapshot, ICON_PRESETS[presetName].filter)) {
                    ev.preventDefault();
                    window.scrollTo({ top: 0, behavior: 'smooth' });
                    return;
                }
                loadPreset(presetName);
            });
        });
        var editBtn = document.getElementById('nav-btn-edit');
        if (editBtn) {
            // Edit icon: stays addEventListener-only because openEditor
            // mounts the inline editor card inside the stream — there's
            // no comparable "fall back to navigateTo" inline handler.
            // Inline onclick set in index.html points at /posts/new
            // (legacy editor) which we DO want to suppress; remove it
            // and let addEventListener own the click. R24 #4: the icon
            // is now a toggle — second click closes the editor.
            editBtn.removeAttribute('onclick');
            editBtn.addEventListener('click', function (ev) {
                ev.preventDefault();
                // DM surfaces: openEditor delegates to openDMComposer,
                // which internally treats an already-open composer as
                // "close-then-mount-new" (so switching reply→new works).
                // For a nav-+ toggle we want a true close; check the body
                // class and short-circuit before falling through to
                // openEditor. Without this the second + click closed and
                // immediately re-opened the composer (looked like a flash).
                if (isDMComposerOpen()) {
                    closeDMComposer();
                    return;
                }
                if (isEditorOpen()) {
                    closeEditor();
                } else {
                    openEditor();
                }
            });
        }
    }

    // Mobile hamburger nav. Hides the inline icon row on narrow
    // viewports and shows a drawer with labeled rows + the user's
    // avatar/site-name header (which normally lives at the topbar's
    // right edge but has no room when the topbar is collapsed).
    function closeNavMenu() {
        document.body.classList.remove('nav-menu-open');
        var ham = document.getElementById('nav-hamburger');
        if (ham) ham.setAttribute('aria-expanded', 'false');
        var drawer = document.getElementById('nav-mobile-drawer');
        if (drawer) drawer.setAttribute('aria-hidden', 'true');
    }
    function toggleNavMenu() {
        var open = !document.body.classList.contains('nav-menu-open');
        document.body.classList.toggle('nav-menu-open', open);
        var ham = document.getElementById('nav-hamburger');
        if (ham) ham.setAttribute('aria-expanded', open ? 'true' : 'false');
        var drawer = document.getElementById('nav-mobile-drawer');
        if (drawer) drawer.setAttribute('aria-hidden', open ? 'false' : 'true');
    }
    function populateMobileNavHeader() {
        var domain = (window.App && window.App.siteBaseUrl) || '';
        var hostname = '';
        if (domain) {
            try { hostname = new URL(domain).hostname; } catch (e) { hostname = domain; }
        }
        var siteEl = document.getElementById('nav-mobile-site');
        if (siteEl) {
            siteEl.textContent = hostname || '';
            siteEl.href = domain || '#';
        }
        // Mirror the desktop avatar's background (gradient set by
        // existing JS on #nav-avatar) so the drawer header reads as
        // the same identity. Avatar is a CSS-painted circle; we read
        // its computed background and re-apply.
        var srcAvatar = document.getElementById('nav-avatar');
        var dstAvatar = document.getElementById('nav-mobile-avatar');
        if (srcAvatar && dstAvatar) {
            var bg = window.getComputedStyle(srcAvatar).background;
            if (bg) dstAvatar.style.background = bg;
        }
    }
    function wireMobileNav() {
        var hamburger = document.getElementById('nav-hamburger');
        if (hamburger) {
            hamburger.addEventListener('click', function (ev) {
                ev.stopPropagation();
                toggleNavMenu();
            });
        }
        // Wire each drawer item to the same handlers the desktop
        // icon-row uses (loadPreset / openEditor / App methods).
        var items = document.querySelectorAll('.nav-mobile-item');
        items.forEach(function (btn) {
            btn.addEventListener('click', function (ev) {
                ev.stopPropagation();
                var action = btn.getAttribute('data-action');
                if (action === 'preset') {
                    loadPreset(btn.getAttribute('data-preset'));
                } else if (action === 'edit') {
                    if (isEditorOpen()) {
                        closeEditor();
                    } else {
                        openEditor();
                    }
                } else if (action === 'copy-follow-link' && window.App && typeof window.App.copyFollowLink === 'function') {
                    window.App.copyFollowLink();
                } else if (action === 'settings' && window.App && typeof window.App.navigateTo === 'function') {
                    window.App.navigateTo('/settings');
                }
                closeNavMenu();
            });
        });
        // Close on outside click.
        document.addEventListener('click', function (ev) {
            if (!document.body.classList.contains('nav-menu-open')) return;
            var drawer = document.getElementById('nav-mobile-drawer');
            var trigger = document.getElementById('nav-hamburger');
            if (drawer && drawer.contains(ev.target)) return;
            if (trigger && trigger.contains(ev.target)) return;
            closeNavMenu();
        });
        // Close on Esc.
        document.addEventListener('keydown', function (ev) {
            if (ev.key === 'Escape' && document.body.classList.contains('nav-menu-open')) {
                closeNavMenu();
            }
        });
        populateMobileNavHeader();
    }

    // step-06/6.e bugfix: when a preset loads, the clicked icon should
    // show as active. The base SPA's _updateNavActive only fires on
    // VIEW changes (currentView → icon mapping), and preset clicks
    // don't change the view (filterScope/Type/etc. change on the same
    // view='conversations'). Without this, switching from gateway →
    // people leaves gateway lit up. owner-extras owns this state
    // override since it's the layer that knows about presets at all.
    //
    // Runs AFTER the navigateTo's _updateNavActive (which sets gateway
    // active for view='conversations'), so this overrides the default.
    // Tracks which preset is currently applied (or null if no preset
    // has been loaded). Used by the click handler's "scroll to top if
    // already active" check — the DOM .active class isn't reliable
    // for this because navigateTo's _updateNavActive (fired by the
    // inline onclick fallback BEFORE this handler) sets gateway active
    // on every preset click regardless of which one was clicked,
    // since all icons map to view='conversations'.
    var activePresetName = null;
    function setActiveIcon(presetName) {
        activePresetName = presetName;
        Object.keys(ICON_PRESETS).forEach(function (name) {
            var btn = document.getElementById('nav-btn-' + name);
            if (btn) btn.classList.toggle('active', name === presetName);
        });
        // edit isn't a preset; clear its active state too.
        var editBtn = document.getElementById('nav-btn-edit');
        if (editBtn) editBtn.classList.remove('active');
    }

    // Load a preset by name. If the user isn't on the stream-screen
    // (legacy /_/blessings or similar), navigate to /feed first so
    // there's somewhere for the filter widget to live; once the SPA
    // resolves the route + activates stream-screen, apply the filter.
    function loadPreset(presetName) {
        var preset = ICON_PRESETS[presetName];
        if (!preset) return;

        var apply = function () {
            // setFilter fires onFilterChange → activity-mode class
            // syncs automatically via the registered listener (see
            // registerActivityDecorators below). We don't need to
            // call syncActivityModeClass here anymore.
            window.PolisStream.setFilter(preset.filter, preset.display);
            // setActiveIcon AFTER setFilter (and after any navigateTo's
            // _updateNavActive call has run) so it wins.
            setActiveIcon(presetName);
            emitPresetLoadedEvent(presetName, preset.filter);
        };

        // Already on the stream view? Apply directly. Otherwise navigate
        // first; await navigateTo so applyFilter runs after the screen
        // is active.
        var streamScreen = document.getElementById('stream-screen');
        var streamActive = streamScreen && !streamScreen.classList.contains('hidden');
        if (streamActive) {
            apply();
            return;
        }
        if (window.App && typeof window.App.navigateTo === 'function') {
            // navigateTo is async; await before applying. Fall back to
            // immediate apply if the Promise pattern isn't available.
            var navResult = window.App.navigateTo('/');
            if (navResult && typeof navResult.then === 'function') {
                navResult.then(apply);
            } else {
                apply();
            }
        } else {
            apply();
        }
    }

    // step-06/6.f.2 — Activity render mode (Layer 3 portion).
    //
    // When the user picks the activity filter type (gateway preset,
    // setFilter sets type='activity'), owner-extras:
    //   1. Toggles body.is-activity-mode → CSS in stream.css compacts
    //      each entry (Layer 2).
    //   2. afterRender hooks per type append a .activity-signal element
    //      with the rule's icon + templated message (e.g., "alice
    //      commented on bob's post about X"). The rule set comes from
    //      registry.json via /api/v1/bundle/notification-rules.
    //
    // Outside activity mode the body class isn't set; the Layer-2 CSS
    // doesn't fire; the .activity-signal elements are hidden by the
    // body:not(.is-activity-mode) rule.
    //
    // Rule lookup keys on entry type → event name:
    //   post → pub.polis.post.published (most common entry case)
    //   comment → pub.polis.comment.published
    //   follow → pub.polis.follow.announced
    //   dm → (no rule shipped today; falls through to a default signal)

    // Map entry type → preferred event name for rule lookup.
    var ENTRY_TYPE_TO_EVENT = {
        post: 'pub.polis.post.published',
        comment: 'pub.polis.comment.published',
        follow: 'pub.polis.follow.announced',
    };

    // step-06/6.f.2 review concern #4: client-side icon glyph
    // resolution. Mirrors iconLookup in cli-go/pkg/bundle/bundle.go
    // (the Go-side map used by NotificationRules() to resolve symbolic
    // names to display characters). Without this, the activity-signal
    // icon was emitting a placeholder dot (·) regardless of which rule
    // the entry matched. Names must stay in sync with the Go map; if a
    // new icon name is added there, add the matching glyph here too.
    var ICON_GLYPHS = {
        pencil:   '✏️',  // ✏️
        comment:  '💬',  // 💬
        prayer:   '🔔',  // 🔔
        check:    '✓',        // ✓
        x:        '✗',        // ✗
        follow:   '👤',  // 👤
        unfollow: '👤',  // 👤 (same — distinguished by template text)
    };

    // Icon SVGs for entry-type signals that should match the topbar nav
    // exactly (so visual identity is consistent between "click my-posts
    // icon in nav" and "see a post in activity"). When an icon name is
    // present in this map, the activity-signal renders the SVG instead
    // of the unicode glyph above. Names mirror ICON_GLYPHS keys.
    //
    // The SVG strings are HARDCODED constants; rendering via innerHTML
    // is safe here. Do not extend this map with user-supplied content.
    //
    // Sourcing: lifted verbatim from index.html's icon-row buttons so
    // the post-type signal in activity matches the "paragraph" nav-icon
    // on /_/posts (3 horizontal lines = posts list). Likewise comment
    // matches the chat-bubble. follow uses the people SVG. dm uses the
    // envelope.
    var ICON_SVGS = {
        // Post → paragraph (matches index.html nav-btn-paragraph).
        pencil: '<svg viewBox="0 0 24 24" width="14" height="14" fill="none" stroke="currentColor" stroke-width="1.6" stroke-linecap="round" stroke-linejoin="round"><line x1="4" y1="7" x2="20" y2="7"/><line x1="4" y1="12" x2="20" y2="12"/><line x1="4" y1="17" x2="14" y2="17"/></svg>',
    };

    // Notification rule registry: event-name keyed map. Populated at
    // init from /api/v1/bundle/notification-rules.
    var notificationRulesByEvent = {};

    function loadNotificationRules() {
        return fetch('/api/v1/bundle/notification-rules', {
            credentials: 'same-origin',
        }).then(function (resp) {
            if (!resp.ok) throw new Error('HTTP ' + resp.status);
            return resp.json();
        }).then(function (data) {
            var rules = (data && data.rules) || [];
            for (var i = 0; i < rules.length; i++) {
                if (rules[i] && rules[i].on) {
                    notificationRulesByEvent[rules[i].on] = rules[i];
                }
            }
        }).catch(function (err) {
            // Non-fatal — activity mode falls back to a default signal
            // shape if the rule fetch fails. Log but don't block init.
            console.warn('[polis-owner-extras] notification rule fetch failed:', err);
        });
    }

    // Substitute {{var}} placeholders in a rule template with values
    // from the entry meta. Unknown placeholders pass through as-is so
    // they're visible during debugging instead of silently empty.
    //
    // step-06/6.f.2 review nit #9: expected entry-meta shape (passed
    // into the afterRender hooks; comes from streamItemResponse JSON
    // server-side):
    //   {
    //     id, type, title, url, published, published_human,
    //     author_url, author_domain,
    //     target_url, target_domain,    // for comments: post being commented on
    //     excerpt, hash, ...,            // type-specific fields
    //   }
    // If upstream renames a field (e.g. target_url → subject_url),
    // applyTemplate's hardcoded substitutions silently fall through to
    // the literal placeholder ({{post_name}}). Update both sides
    // together when meta shape changes.
    function applyTemplate(tmpl, meta) {
        if (!tmpl) return '';
        return tmpl.replace(/\{\{(\w+)\}\}/g, function (full, key) {
            // Common substitutions — extend as new template variables
            // surface in the rule set.
            if (key === 'actor') return meta.author_domain || meta.author_url || full;
            if (key === 'post_name') return meta.target_url || meta.title || full;
            // Pass-through for unknown placeholders.
            var v = meta[key];
            return (v !== undefined && v !== null) ? String(v) : full;
        });
    }

    // Extract a tenant handle (e.g. "alice.polis.pub") from an
    // author-domain or author-url meta field. Falls back to '' if neither
    // is present or parseable. Used by the activity-signal handle button
    // to drive the "all posts from <handle> by date" filter shortcut.
    function extractHandleFromMeta(meta) {
        if (!meta) return '';
        if (meta.author_domain) return meta.author_domain;
        if (meta.author_url) {
            try { return new URL(meta.author_url).hostname; } catch (e) { /* ignore */ }
        }
        return '';
    }

    // Append an .activity-signal element to the entry. Idempotent — if
    // a signal already exists (e.g., re-render), replace it.
    //
    // Layout (per resolved-feedback 2026-05-07):
    //   [icon] [handle-button] [verb-from-rule]
    //          <title>
    //          <excerpt-1-2-lines>
    //
    // Handle button: button element styled as a link; click sets the
    // sentence filter to "all posts from <handle> by date" via
    // PolisStream.setFilter. Stops propagation so it doesn't trigger
    // the entry's own click handler.
    //
    // Verb: derived from the notification rule's template, with
    // {{actor}} stripped (we render the actor explicitly as the handle
    // button — the rule template's actor placeholder is redundant).
    // Falls back to a per-type default if no rule matches.
    //
    // Title + excerpt: drawn from meta.title + meta.excerpt when present,
    // letting activity entries feel content-rich rather than thin.
    function appendActivitySignal(entry, meta) {
        if (!entry) return;
        // 06-profiles: follow events get their own visual treatment in
        // renderFollow (italic body "followed <target>" + byline; no
        // gutter glyph). Short-circuit here so that if this dormant
        // signal layer is ever revived, follow events don't end up with
        // a person/plus glyph competing with the timeline-dot.
        if (meta && meta.type === 'follow') return;
        if (meta && typeof meta.event_type === 'string' &&
            meta.event_type.indexOf('pub.polis.follow.') === 0) {
            return;
        }
        var existing = entry.querySelector('.activity-signal');
        if (existing) existing.parentNode.removeChild(existing);

        var eventName = ENTRY_TYPE_TO_EVENT[meta.type];
        var rule = eventName && notificationRulesByEvent[eventName];
        var icon = (rule && rule.icon) || meta.type;

        var signal = document.createElement('div');
        signal.className = 'activity-signal';

        // Header line: icon + handle button + verb.
        var head = document.createElement('div');
        head.className = 'activity-signal-head';

        var iconSpan = document.createElement('span');
        iconSpan.className = 'activity-signal-icon';
        // Prefer SVG when available so the icon matches the topbar nav
        // exactly (post-type → paragraph SVG, etc.); fall back to the
        // unicode glyph for icon names without an SVG variant.
        if (ICON_SVGS[icon]) {
            iconSpan.innerHTML = ICON_SVGS[icon];
        } else {
            iconSpan.textContent = ICON_GLYPHS[icon] || '·';
        }
        iconSpan.setAttribute('data-rule-icon', icon);
        head.appendChild(iconSpan);

        var handle = extractHandleFromMeta(meta);
        if (handle) {
            var handleBtn = document.createElement('button');
            handleBtn.type = 'button';
            handleBtn.className = 'activity-signal-handle';
            handleBtn.textContent = handle;
            handleBtn.title = 'See all posts from ' + handle;
            handleBtn.addEventListener('click', function (ev) {
                ev.stopPropagation();
                ev.preventDefault();
                if (window.PolisStream && typeof window.PolisStream.setFilter === 'function') {
                    window.PolisStream.setFilter({
                        qualifier: 'all',
                        type: 'posts',
                        scope: '@' + handle,
                        modifier: 'by-date',
                    }, {
                        qualifierLabel: 'all',
                        typeLabel: 'posts',
                        scopeLabel: handle,
                        modifierLabel: 'by date',
                    });
                }
            });
            head.appendChild(handleBtn);
        }

        // Verb derived from rule template, minus the {{actor}} placeholder
        // (we already rendered the handle as a button). Trim leading
        // whitespace + connectors so "{{actor}} published a new post"
        // becomes "published a new post".
        var verb = '';
        if (rule && rule.template) {
            verb = applyTemplate(rule.template, meta)
                .replace(/^\s*[A-Za-z0-9._-]+\s+/, '') // strip leading "<actor> "
                .trim();
        }
        if (!verb) {
            // Per-type default verbs when no rule matches.
            if (meta.type === 'post') verb = 'posted';
            else if (meta.type === 'comment') verb = 'commented';
            else if (meta.type === 'follow') verb = 'followed';
            else if (meta.type === 'dm') verb = 'sent a message';
            else verb = '';
        }
        if (verb) {
            var verbSpan = document.createElement('span');
            verbSpan.className = 'activity-signal-verb';
            verbSpan.textContent = verb;
            head.appendChild(verbSpan);
        }

        signal.appendChild(head);

        // Title (post / comment) — drop the "<type>: " prefix that the
        // earlier fallback emitted. Just the title text on its own line.
        if (meta.title) {
            var titleEl = document.createElement('div');
            titleEl.className = 'activity-signal-title';
            titleEl.textContent = meta.title;
            signal.appendChild(titleEl);
        }

        // Excerpt — clamped to 2 lines via CSS so activity stays
        // scannable while feeling alive vs the previous "title only"
        // shape. Suppressed when title-redundancy already covers it
        // (the title IS the excerpt's first sentence; rendering the
        // excerpt below the title would visibly duplicate text).
        if (meta.excerpt && !meta.title_redundant) {
            var excerptEl = document.createElement('div');
            excerptEl.className = 'activity-signal-excerpt';
            excerptEl.textContent = meta.excerpt;
            signal.appendChild(excerptEl);
        } else if (meta.excerpt && meta.title_redundant) {
            // When the title is redundant with the excerpt's opening,
            // show the excerpt INSTEAD of the title (skip the title
            // line above) — same pattern handleFeedGrouped's
            // renderConversationsTabbed uses. The .activity-signal-title
            // element added above gets replaced.
            var titleNode = signal.querySelector('.activity-signal-title');
            if (titleNode) titleNode.parentNode.removeChild(titleNode);
            var excerptOnly = document.createElement('div');
            excerptOnly.className = 'activity-signal-excerpt';
            excerptOnly.textContent = meta.excerpt;
            signal.appendChild(excerptOnly);
        }

        entry.appendChild(signal);
    }

    // Register afterRender hooks for each entry type. Each hook is a
    // no-op when body.is-activity-mode isn't set (CSS hides the signal
    // anyway, but we skip the DOM work to keep render cost down on
    // non-activity views).
    //
    // step-06/6.f.2 review concern #1: also subscribes to
    // PolisStream.onFilterChange so body.is-activity-mode tracks ANY
    // filter-type change (preset click, dropdown pick, programmatic
    // setFilterType) — not just the preset-click code path. Without
    // this, picking type=activity from the dropdown left body.is-
    // activity-mode unset and the Layer-2 CSS gate didn't engage.
    // R20-5 redesign: activity is now a heterogenous union of natural
    // per-type renderings (renderPost, renderCommentThread, renderFollow)
    // — no projection. We still toggle body.is-activity-mode so CSS can
    // tighten the body-cap to a smaller preview, but we no longer
    // append .activity-signal pills or wire whole-entry click. The
    // natural .entry-title-link click already opens the canonical
    // post/comment page (which auto-engages focus mode per R20/R17),
    // and the public stream's no-hover-tint convention applies
    // unchanged. Plumbing for the prior projection (notification
    // rules, activity-signal CSS, appendActivitySignal builder) is
    // intentionally left in place per user direction so we can revive
    // the projection model later without re-wiring from scratch.
    function registerActivityDecorators(stream) {
        // Tracks the icon bucket the most recent filter snapshot mapped to,
        // so the "mark surface viewed" hook can edge-trigger only when the
        // bucket changes (mid-edit slot churn — qualifier→type→scope→
        // modifier — would otherwise re-POST the viewed cursor for every
        // keystroke). null means "no surface in scope" (e.g. paragraph,
        // people, drafts) or "haven't seen a snapshot yet."
        var prevSurfaceIcon = null;
        stream.onFilterChange(function (snapshot) {
            currentFilterSnapshot = snapshot;
            syncActivityModeClass(snapshot.type);
            // Reconcile activePresetName with the actual filter state.
            // Without this, manual dropdown picks left activePresetName
            // pointing at the last-clicked preset, and clicking the
            // same icon again was treated as "scroll to top" (the
            // preset-already-active branch in wireIconPresets) instead
            // of "reset filter back to the preset's shape." Match the
            // snapshot against each preset's filter; setActiveIcon to
            // the matching preset name, or null if no preset matches.
            syncActiveIconFromFilter(snapshot);
            // Edge-triggered "mark this surface viewed" — clears the
            // gateway / comment / envelope nav dot the first time the
            // user lands on each surface in a session, regardless of
            // the entry path (icon click, PQL URL, dropdown). Same
            // bucketing as syncActiveIconFromFilter so the icon-highlight
            // and dot-clear stay in lockstep.
            var matched = matchIconForFilter(snapshot);
            var isSurface = matched === 'gateway' || matched === 'comment' || matched === 'envelope';
            if (isSurface && matched !== prevSurfaceIcon &&
                window.App && typeof window.App.markSurfaceViewed === 'function') {
                window.App.markSurfaceViewed(matched);
            }
            prevSurfaceIcon = matched;
            // is-filter-comments toggles CSS that hides the ghost-
            // compose row when the user is on any "all comments…"
            // view. The ghost prompt ("yours, truly") is post-shaped;
            // it doesn't make sense on a comments stream. The nav +
            // still opens the editor unchanged.
            syncCommentsFilterClass(snapshot.type);
            // 06-profiles Phase 3: ensure the .entry--search row is
            // mounted at the top of the stream under
            // (profiles, all-polis), removed otherwise.
            ensureSearchEntryMounted(snapshot);
            // DM scope dropdown: populate the recent-conversations list
            // whenever the user is on any DM surface (inbox OR a thread).
            // Cheap fetch — single request, cached for 30s — so toggling
            // between dm scopes doesn't spam /api/dm/conversations.
            if (snapshot.type === 'dms') {
                refreshDMScopeOptions();
            }
            // PQL URL push (chunk B). Compose the snapshot to a
            // /_/pql/<sentence> URL and push to history. Debounced
            // 300ms so rapid slot edits don't shovel five entries
            // into the back stack. First push uses replaceState to
            // avoid polluting history with the implicit /_/ → /_/pql/…
            // transition on initial filter hydration.
            pushFilterURL(snapshot);
        });

        // Seed currentFilterSnapshot + activePresetName from the CURRENT
        // filter. addFilterChangeListener does NOT replay to new
        // subscribers, and stream.js fires its initial fireFilterChange
        // during hydration — BEFORE this subscription attaches (it runs in
        // the ready-promise block, after the controller is up). Without
        // seeding, both stay null until the user's first filter change, so
        // the very first click on the already-active preset icon failed the
        // "already on this preset → scroll to top" condition and fell
        // through to loadPreset, reloading the stream in place instead of
        // scrolling up. Seed directly (NOT by invoking the callback above)
        // so we don't re-fire its side effects — pushFilterURL (would
        // rewrite the URL on load) and markSurfaceViewed — on first paint.
        if (typeof stream.getFilter === 'function') {
            var initialSnapshot = stream.getFilter();
            if (initialSnapshot) {
                currentFilterSnapshot = initialSnapshot;
                syncActiveIconFromFilter(initialSnapshot);
            }
        }
    }

    // PQL URL push (chunk B). Debounced compose-and-push wired into
    // the onFilterChange subscription above. First push after page
    // load uses replaceState so the implicit /_/ → /_/pql/… transition
    // doesn't leave a useless history entry; subsequent pushes use
    // pushState so the browser back button restores prior filter
    // state. Skipped when window.PQL hasn't loaded (defensive — pql.js
    // is loaded by the same SPA shell as owner-extras, but the script
    // tags are independent).
    var pqlPushTimer = null;
    var pqlPushFirst = true;
    function pushFilterURL(snapshot) {
        if (!window.PQL || typeof window.PQL.composeURL !== 'function') return;
        if (pqlPushTimer) clearTimeout(pqlPushTimer);
        pqlPushTimer = setTimeout(function () {
            pqlPushTimer = null;
            var basePath = (window.App && window.App.basePath) || '/_';
            var canonical = window.PQL.composeURL(snapshot, basePath);
            // Skip the push if the URL hasn't changed — a no-op push
            // still creates a duplicate history entry on some browsers.
            if (canonical === window.location.pathname) return;
            if (pqlPushFirst) {
                pqlPushFirst = false;
                window.history.replaceState({}, '', canonical);
            } else {
                window.history.pushState({}, '', canonical);
            }
        }, 300);
    }

    // Pulls the inbox via /api/dm/conversations and threads the top 10
    // most-recent conversations into PolisStream.setDMScopeOptions so the
    // scope dropdown for type=dms surfaces them as fast-switcher entries.
    // 30s in-memory cache keeps the dropdown responsive without re-
    // fetching on every filter change.
    var dmScopeCache = { at: 0, inflight: null };
    function refreshDMScopeOptions() {
        var now = Date.now();
        if (dmScopeCache.inflight) return dmScopeCache.inflight;
        if (now - dmScopeCache.at < 30 * 1000) return Promise.resolve();
        dmScopeCache.inflight = fetch('/api/dm/conversations', { credentials: 'same-origin' })
            .then(function (r) { return r.ok ? r.json() : { conversations: [] }; })
            .then(function (data) {
                var convs = (data && data.conversations) || [];
                convs.sort(function (a, b) {
                    return (b.last_message_at || '').localeCompare(a.last_message_at || '');
                });
                var opts = convs.slice(0, 10).map(function (c) {
                    return { label: c.peer_domain, value: '@' + c.peer_domain };
                });
                if (window.PolisStream && typeof window.PolisStream.setDMScopeOptions === 'function') {
                    window.PolisStream.setDMScopeOptions(opts);
                }
                dmScopeCache.at = Date.now();
            })
            .catch(function (e) {
                console.warn('[owner-extras] refreshDMScopeOptions failed:', e);
            })
            .then(function () { dmScopeCache.inflight = null; });
        return dmScopeCache.inflight;
    }

    // 06-profiles Phase 3: search entry lifecycle. The .entry--search
    // is a stream entry that sits as the first child of the .stream
    // container under filterType=profiles && filterScope=all-polis.
    // It's part of the rail visually (the magnifying-glass replaces
    // the timeline-dot for that one row, mirroring how .editor-card-plus
    // replaces the dot for the inline editor).
    //
    // Why mount via the filter-change listener rather than a renderer:
    // (a) the row isn't an item from the server — it's a UI affordance,
    // (b) clearStreamForFilterChange wipes everything in .stream, so
    //     we re-mount on each filter change rather than relying on the
    //     server to ship a phantom item, (c) the renderer pipeline
    //     would emit the row on every page-load fetch, racing with
    //     the user's typing.
    function ensureSearchEntryMounted(snapshot) {
        // Mount the search row on either profiles scope. all-polis
        // searches the DS site directory; my-network filters the user's
        // own follow list (helpful for owners who follow many sites
        // and need to locate one without scrolling the sorted list).
        var isProfilesView = snapshot.type === 'profiles';
        var isSearchableScope = snapshot.scope === 'all-polis' || snapshot.scope === 'my-network';
        var shouldMount = isProfilesView && isSearchableScope;
        var streamRoot = document.querySelector('.layout-right .stream') ||
                         document.querySelector('.stream');
        if (!streamRoot) return;
        // The post editor has no meaning on the profiles surface — close
        // it on entry so the search row is the only top-of-stream affordance.
        if (shouldMount && isEditorOpen()) {
            closeEditor();
        }
        // body.is-search-active drives a CSS rule that hides the
        // ghost-compose ("yours, truly") row when the search row is
        // mounted — the search input visually replaces the ghost-
        // compose slot at the top of the stream.
        document.body.classList.toggle('is-search-active', shouldMount);
        var existing = streamRoot.querySelector(':scope > .entry--search');
        if (!shouldMount) {
            if (existing) existing.parentNode.removeChild(existing);
            // Reset the search query when leaving the profiles surface
            // entirely so the next visit starts clean. Toggling between
            // my-network and all-polis preserves the query — searching
            // for "alice" and switching scope is a natural exploration.
            if (window.PolisStream && typeof window.PolisStream.setSearchQuery === 'function') {
                var prev = window.PolisStream.getSearchQuery && window.PolisStream.getSearchQuery();
                if (prev) window.PolisStream.setSearchQuery('');
            }
            return;
        }
        if (existing) {
            // Already mounted; just refresh the placeholder to match
            // the current scope (matters when toggling all-polis ↔
            // my-network without leaving the surface).
            var existingInput = existing.querySelector('input');
            if (existingInput) {
                existingInput.placeholder = snapshot.scope === 'my-network'
                    ? 'Search my follows by handle or name…'
                    : 'Search profiles by handle or name…';
            }
            return;
        }

        var article = document.createElement('article');
        article.className = 'entry entry--search';
        // Stay put across filter changes (data-polis-pinned is the
        // mechanism clearStreamForFilterChange uses to preserve the
        // inline editor; the search row uses the same hook). Without
        // this, the listener mounts the row from fireFilterChange but
        // applyFilter's clearStreamForFilterChange wipes it ms later.
        article.setAttribute('data-polis-pinned', 'search');

        // SVG magnifying-glass on the rail — mirrors the editor-card-plus
        // pattern for .entry--editor. Same column geometry => same
        // negative-left positioning via CSS.
        var iconNS = 'http://www.w3.org/2000/svg';
        var svg = document.createElementNS(iconNS, 'svg');
        svg.setAttribute('class', 'search-icon-rail');
        svg.setAttribute('viewBox', '0 0 24 24');
        svg.setAttribute('fill', 'none');
        svg.setAttribute('stroke', 'currentColor');
        svg.setAttribute('stroke-width', '2');
        var circle = document.createElementNS(iconNS, 'circle');
        circle.setAttribute('cx', '11');
        circle.setAttribute('cy', '11');
        circle.setAttribute('r', '7');
        svg.appendChild(circle);
        var line = document.createElementNS(iconNS, 'path');
        line.setAttribute('d', 'm20 20-3.5-3.5');
        svg.appendChild(line);
        article.appendChild(svg);

        var input = document.createElement('input');
        input.type = 'text';
        input.placeholder = snapshot.scope === 'my-network'
            ? 'Search my follows by handle or name…'
            : 'Search profiles by handle or name…';
        input.setAttribute('aria-label', 'Search profiles');
        // Restore the prior search value if the user re-entered the
        // surface (search state was cleared on exit; nothing to
        // restore in the common case).
        var existingQ = window.PolisStream && window.PolisStream.getSearchQuery && window.PolisStream.getSearchQuery();
        if (existingQ) input.value = existingQ;

        var debounceId = null;
        input.addEventListener('input', function () {
            if (debounceId) clearTimeout(debounceId);
            var v = input.value;
            debounceId = setTimeout(function () {
                if (window.PolisStream && typeof window.PolisStream.setSearchQuery === 'function') {
                    window.PolisStream.setSearchQuery(v);
                }
            }, 250);
        });
        article.appendChild(input);

        // Insert as the first child of the stream container so the
        // search row sits at the top of the timeline.
        streamRoot.insertBefore(article, streamRoot.firstChild);
    }

    function syncCommentsFilterClass(filterType) {
        document.body.classList.toggle('is-filter-comments', filterType === 'comments');
    }

    // Map a sentence-filter snapshot to the icon that should appear
    // selected. Strict preset-equality was too narrow — manually picking
    // "all posts from my network" left gateway dark even though gateway
    // is conceptually "the network feed surface."
    //
    // The rules (waterfall, first match wins):
    //
    //   1. comment   — only the exact "all comments from all polis to bless"
    //                  preset; the speech bubble keeps its specific meaning
    //                  as the blessing inbox.
    //   2. paragraph — anything the owner is authoring: type=drafts always,
    //                  or type=posts when scope=me. Wins over gateway for
    //                  the overlap case "my posts."
    //   3. gateway   — the network/feed surface: any remaining type=activity
    //                  / posts / comments combination.
    //   4. people    — type=profiles regardless of scope.
    //   5. envelope  — type=dms regardless of scope.
    //
    // null result clears all icon highlights (no preset matches the
    // current filter — e.g. an @site typeahead state).
    function syncActiveIconFromFilter(snapshot) {
        setActiveIcon(matchIconForFilter(snapshot));
    }

    function matchIconForFilter(snapshot) {
        var t = (snapshot && snapshot.type) || '';
        var s = (snapshot && snapshot.scope) || '';
        var m = (snapshot && snapshot.modifier) || '';
        var q = (snapshot && snapshot.qualifier) || '';

        // 1. comment — strict preset match (keeps the blessing-inbox meaning).
        if (q === 'all' && t === 'comments' && s === 'all-polis' && m === 'to-bless') {
            return 'comment';
        }
        // 2. paragraph — drafts always, or posts the owner wrote.
        if (t === 'drafts') return 'paragraph';
        if (t === 'posts' && s === 'me') return 'paragraph';
        // 3. gateway — the rest of the activity/posts/comments family.
        if (t === 'activity' || t === 'posts' || t === 'comments') return 'gateway';
        // 4. profiles → people.
        if (t === 'profiles') return 'people';
        // 5. messages → envelope.
        if (t === 'dms') return 'envelope';
        return null;
    }

    // Toggle body.is-activity-mode based on the current filter type.
    // Driven by the onFilterChange subscription above (so dropdown
    // picks fire it just like preset clicks do).
    function syncActivityModeClass(filterType) {
        document.body.classList.toggle('is-activity-mode', filterType === 'activity');
    }

    // ====================================================================
    // step-06/6.i — Stream-item afterRender decorators (owner actions)
    // ====================================================================
    //
    // Hover-revealed action chrome on each entry the owner has
    // authority over:
    //   - Posts authored by owner → [Unpublish]
    //   - Comments authored by owner → [Unpublish]
    //   - Comments directed at owner (incoming blessing requests) →
    //     [Bless] [Deny]
    //   - DMs that are unread → [Mark read] (best-effort; falls back
    //     to a navigation if the dedicated mark-read API isn't wired)
    //
    // Layered model: this is Layer 3 (afterRender hook in owner-extras
    // — never runs for public visitors who don't load this file).
    // CSS in stream.css gates visibility on body.is-owner (Layer 2)
    // for defense in depth.
    //
    // Click handlers reuse the existing App.unpublishPost /
    // App.unpublishComment / App.grantBlessing / App.denyBlessing
    // methods (they handle confirmations + toasts + post-action
    // suggestions). owner-extras pulls the args from entry meta and
    // dispatches.

    // Owner's domain — used to detect own-vs-other-authored entries.
    // Cached on first call. App.siteBaseUrl is populated during App.init
    // which races with owner-extras' init, so the first lookup may miss
    // and return ''; subsequent calls (after App.init resolves) cache
    // the resolved hostname so per-entry decoration doesn't re-parse
    // the URL on every render.
    var _ownerDomainCache = '';
    function ownerDomain() {
        if (_ownerDomainCache) return _ownerDomainCache;
        try {
            if (window.App && window.App.siteBaseUrl) {
                _ownerDomainCache = new URL(window.App.siteBaseUrl).hostname;
            }
        } catch (e) { /* ignore */ }
        return _ownerDomainCache;
    }

    // Best-effort stream re-fetch helper — used by owner-action
    // click handlers to pull the post-action server state back into
    // the stream. PolisStream.refresh re-fires applyFilter without
    // mutating state, so a successful unpublish/bless/deny sees the
    // stream re-pulled (the un-blessed comment vanishes, the
    // unpublished post drops out, etc.). Defensive guard for
    // controllers that predate refresh().
    function refreshStream() {
        if (window.PolisStream && typeof window.PolisStream.refresh === 'function') {
            try { window.PolisStream.refresh(); } catch (e) { /* ignore */ }
        }
    }

    // refreshOwnerCounts — paired with refreshStream for write operations
    // that change the owner-card stat counts (publish / republish / save-
    // as-draft / discard / unpublish). Forwards to loadOwnerCardCounts
    // if the card is mounted; safely no-ops otherwise (e.g. during
    // bootstrap before the card lands). Without this call the bio-row
    // "N posts · M drafts" stats stay stale until a page reload.
    function refreshOwnerCounts() {
        if (typeof loadOwnerCardCounts === 'function') {
            try { loadOwnerCardCounts(); } catch (e) { /* ignore */ }
        }
    }

    function makeActionButton(label, opts) {
        var btn = document.createElement('button');
        btn.type = 'button';
        btn.textContent = label;
        if (opts && opts.className) btn.className = opts.className;
        if (opts && opts.title) btn.title = opts.title;
        if (opts && opts.onclick) {
            btn.addEventListener('click', function (ev) {
                ev.stopPropagation();
                ev.preventDefault();
                try { opts.onclick(); } catch (e) { console.error('[owner-extras] action click error:', e); }
            });
        }
        return btn;
    }

    function appendActionsContainer(entry, buttons) {
        if (!entry || !buttons || !buttons.length) return;
        // Idempotent: drop a prior actions block before re-appending.
        var existing = entry.querySelector(':scope > .entry-actions');
        if (existing) existing.parentNode.removeChild(existing);
        var actions = document.createElement('div');
        actions.className = 'entry-actions';
        for (var i = 0; i < buttons.length; i++) actions.appendChild(buttons[i]);
        entry.appendChild(actions);
    }

    function decoratePost(entry, meta) {
        if (!meta) return;
        var ownDomain = ownerDomain();
        // R26 #5 prototype: cross-author posts surface the "add
        // comment" affordance on the timeline-dot itself (replaces
        // the dot with a comment icon on hover) rather than as a
        // right-justified action button. Visual cue reads as "click
        // here to add to the timeline". Mark the entry + wire the
        // click; CSS handles the dot → icon swap on hover.
        if (ownDomain && meta.author_domain && meta.author_domain !== ownDomain && meta.url) {
            entry.classList.add('entry--commentable');
            // Wire the comment-add CTA inline (mounts the new editor as a
            // child of this entry) instead of navigating to the legacy
            // /comments/new screen. wireCommentAddCTA factors out the dot
            // event handler so decoratePost + decorateComment stay in sync.
            wireCommentAddCTA(entry, meta.url, meta.author_domain);
            return;
        }
        if (meta.author_domain !== ownDomain) return;
        // Tag the entry as my own content. CSS uses .entry--mine to
        // suppress chrome that's redundant when the viewer IS the
        // author — the byline-domain ("vdibart.polis.pub" repeated on
        // every entry the user sees on their own feed) and the
        // comments-badge hover-+ swap (no reason to invite a self-
        // comment in the "all posts by me" view).
        entry.classList.add('entry--mine');
        // R20-10: drafts ride the post renderer (see
        // handleStreamItemsDrafts), so they hit this hook too. Mark the
        // entry so CSS can prepend a "Draft" pill on the meta-line —
        // matches the v4 entry layout convention rather than letting
        // drafts read as published posts.
        if (meta.draft) {
            entry.classList.add('is-draft');
            // R28: click on draft title/body opens the embedded editor
            // pre-filled with the draft (instead of navigating to the
            // legacy /_/posts/drafts/<id> route). Derive the draft id
            // from meta.url's trailing slug. The link's href is left
            // intact so middle-click / Ctrl-click still works as a
            // fallback if the user wants the legacy route.
            //
            // Delegation note: renderPost defers body_html injection
            // via setTimeout(0), which swaps the excerpt <a> for an
            // expanded <div> AFTER this afterRender hook fires. A
            // handler bound to the excerpt <a> would be discarded by
            // the swap. Binding to the article (which survives the
            // swap) and matching on the closest title-link <a> at
            // click time is robust to either pre- or post-injection
            // state.
            var draftId = '';
            if (meta.url) {
                var m = String(meta.url).match(/drafts\/([^/?#]+)/);
                if (m) draftId = m[1];
            }
            if (draftId) {
                entry.addEventListener('click', function (ev) {
                    if (ev.metaKey || ev.ctrlKey || ev.shiftKey || ev.button !== 0) return;
                    var t = ev.target;
                    if (!t || !t.closest) return;
                    // Only intercept clicks on the title/body link area
                    // — clicks on the timeline-dot or any other chrome
                    // (including the Discard rollover CTA) should
                    // fall through.
                    var hit = t.closest('a.entry-body, a.entry-body--expanded, a.entry-title-link, .entry-body, .entry-content');
                    if (!hit) return;
                    // Don't intercept clicks on body links (markdown
                    // links inside the rendered draft content). If
                    // the immediate <a> is a non-title link with a
                    // different href, let it through.
                    var anchor = t.closest('a');
                    if (anchor && anchor.href && !anchor.classList.contains('entry-title-link') &&
                        !anchor.classList.contains('entry-body') &&
                        !anchor.classList.contains('entry-body--excerpt')) {
                        return;
                    }
                    ev.preventDefault();
                    openEditor({ draftId: draftId });
                });
                // Discard rollover CTA — the entry-rollover-cta pattern
                // (hover-revealed, owner-gated, content-aware CTA per
                // entry). Mirrors the Unpublish wiring on own-posts a
                // few lines down: same makeActionButton +
                // appendActionsContainer helpers, same .danger styling,
                // same hover-only reveal via .entry-actions CSS gate.
                var discardBtn = makeActionButton('Discard', {
                    className: 'danger',
                    title: 'Permanently delete this draft',
                    onclick: function () {
                        if (window.App && typeof window.App.deleteDraft === 'function') {
                            var p = window.App.deleteDraft(draftId);
                            Promise.resolve(p).then(function () {
                                refreshStream();
                                refreshOwnerCounts();
                            }).catch(function () { /* ignore */ });
                        } else {
                            console.warn('[owner-extras] App.deleteDraft not available');
                        }
                    },
                });
                appendActionsContainer(entry, [discardBtn]);
            }
            // Draft entries have no source_path / no unpublish action;
            // skip the Unpublish-button branch below.
            return;
        }
        // App.unpublishPost requires the SOURCE path form (under
        // content/pub.polis.core/post/), NOT the rendered .html mount
        // path. The server now ships source_path on own-content stream
        // items (handlers_stream.go:loadOwnContentAsFeedItems). If the
        // server response is from an older binary that doesn't ship
        // source_path, the action is intentionally suppressed (the
        // alternative — passing meta.url — silently 400s).
        if (!meta.source_path) return;
        // Stash the source path on the article so loadPostIntoEditor
        // can find this entry (by querySelector) and hide it while
        // the editor is open — otherwise the user sees the editor
        // card up top AND the same post still rendered below it.
        entry.dataset.polisSourcePath = meta.source_path;
        // Edit rollover CTA — opens the editor in republish mode with
        // this post's content pre-filled. Mirrors the entry-rollover-cta
        // wiring used for Unpublish below: same makeActionButton +
        // appendActionsContainer plumbing, neutral styling (no .danger
        // — editing isn't destructive). Click hits openEditor which
        // routes through loadPostIntoEditor → /api/posts/{path} →
        // republish via /api/republish on submit.
        var editBtn = makeActionButton('Edit', {
            title: 'Edit this post (republish with changes)',
            onclick: function () {
                openEditor({ postPath: meta.source_path });
            },
        });
        var unpublishBtn = makeActionButton('Unpublish', {
            className: 'danger',
            title: 'Remove this post from your site + discovery',
            onclick: function () {
                if (window.App && typeof window.App.unpublishPost === 'function') {
                    var p = window.App.unpublishPost(meta.source_path);
                    // Refresh the stream + counts once the action completes
                    // so the unpublished post drops out of view AND the
                    // bio's posts/drafts counts both tick correctly
                    // (unpublish moves the post to drafts: -1 post, +1 draft).
                    Promise.resolve(p).then(function () {
                        refreshStream();
                        refreshOwnerCounts();
                    }).catch(function () { /* ignore */ });
                } else {
                    console.warn('[owner-extras] App.unpublishPost not available');
                }
            },
        });
        appendActionsContainer(entry, [editBtn, unpublishBtn]);
    }

    function decorateComment(entry, meta) {
        if (!meta) return;
        var owner = ownerDomain();
        if (!owner) return;

        // Item #4: comment-add hover affordance on comment entries
        // whose target post is by someone else. Parallels decoratePost's
        // .entry--commentable wiring — same dot-becomes-+ rollover CSS
        // applies. Click goes to /comments/new?reply_to=<target post
        // URL>, i.e. add a comment to the POST this comment belongs to
        // (not nest under the comment itself). Self-authored comments
        // still get this rollover when their target post is by someone
        // else; the existing self-comment branch below appends an
        // Unpublish action button on the right (separate slot from the
        // dot). Comments on the owner's own posts skip the rollover —
        // same logic as decoratePost skipping own posts.
        if (meta.target_url && meta.target_domain && meta.target_domain !== owner) {
            entry.classList.add('entry--commentable');
            // Same wiring as decoratePost — target the POST this comment
            // belongs to (meta.target_url), not the comment itself. The
            // editor mounts inline as a child of this entry--comment-thread.
            wireCommentAddCTA(entry, meta.target_url, meta.target_domain);
        }

        // Owner's own outgoing comment → unpublish via source path.
        // App.unpublishComment now accepts either a comment_id (legacy
        // call site) or a source path (v4 stream — the path form
        // bypasses FindBlessedComment and routes straight to the
        // unpublish handler).
        if (meta.author_domain === owner) {
            if (!meta.source_path) return;
            var unpublishBtn = makeActionButton('Unpublish', {
                className: 'danger',
                title: 'Unpublish this comment',
                onclick: function () {
                    if (window.App && typeof window.App.unpublishComment === 'function') {
                        var p = window.App.unpublishComment(meta.source_path);
                        Promise.resolve(p).then(refreshStream).catch(function () { /* ignore */ });
                    } else {
                        console.warn('[owner-extras] App.unpublishComment not available');
                    }
                },
            });
            appendActionsContainer(entry, [unpublishBtn]);
            return;
        }

        // Comment directed at owner (someone else's comment ON owner's
        // post) → bless / deny. target_domain check identifies it.
        if (meta.target_domain === owner) {
            var blessBtn = makeActionButton('Bless', {
                className: 'primary',
                title: 'Bless this comment',
                onclick: function () {
                    if (window.App && typeof window.App.grantBlessing === 'function') {
                        var p = window.App.grantBlessing(meta.hash || '', meta.url || '', meta.target_url || '');
                        Promise.resolve(p).then(refreshStream).catch(function () { /* ignore */ });
                    } else {
                        console.warn('[owner-extras] App.grantBlessing not available');
                    }
                },
            });
            var denyBtn = makeActionButton('Deny', {
                className: 'danger',
                title: 'Deny this comment',
                onclick: function () {
                    if (window.App && typeof window.App.denyBlessing === 'function') {
                        var p = window.App.denyBlessing(meta.url || '', meta.target_url || '');
                        Promise.resolve(p).then(refreshStream).catch(function () { /* ignore */ });
                    } else {
                        console.warn('[owner-extras] App.denyBlessing not available');
                    }
                },
            });
            appendActionsContainer(entry, [blessBtn, denyBtn]);
        }
    }

    function decorateDM(entry, meta) {
        if (!entry || !meta) return;
        // Click-to-open: any click on the inbox entry (outside the
        // Mark-read button) switches the active filter scope to this
        // peer's handle. The stream re-renders into thread mode via
        // the renderDMMessage path. Internal scope value is
        // `@<peerDomain>` to match parseStreamScope's expectation;
        // PQL parser (chunk B) will translate to/from the bare URL
        // form `<peerDomain>.polis.pub`.
        var peerDomain = meta.sender_domain || '';
        if (peerDomain && !entry.dataset.polisDmClickWired) {
            entry.dataset.polisDmClickWired = '1';
            entry.style.cursor = 'pointer';
            entry.addEventListener('click', function (ev) {
                // Don't hijack clicks on the entry-actions buttons
                // (Mark read et al.) or on any future inline links.
                var target = ev.target;
                if (target.closest('.entry-actions') || target.closest('a, button')) return;
                if (window.PolisStream && typeof window.PolisStream.setFilterScope === 'function') {
                    window.PolisStream.setFilterScope('@' + peerDomain, { label: peerDomain });
                }
            });
        }
        // Existing Mark-read affordance for unread conversations.
        if (!meta.unread) return;
        var btn = makeActionButton('Mark read', {
            title: 'Mark this conversation read',
            onclick: function () {
                // Best-effort: hit the existing DM mark-read endpoint
                // by conversation ID. The /api/dm/conversations/<id>
                // GET endpoint already auto-marks read on open; an
                // explicit mark-read POST may also exist. Try both;
                // log and continue on failure.
                fetch('/api/dm/mark-read', {
                    method: 'POST',
                    headers: { 'Content-Type': 'application/json' },
                    credentials: 'same-origin',
                    body: JSON.stringify({ conversation_id: meta.conversation_id || meta.id }),
                }).then(function (r) {
                    if (r.ok) {
                        entry.classList.remove('has-unread');
                        var actions = entry.querySelector(':scope > .entry-actions');
                        if (actions && actions.parentNode) actions.parentNode.removeChild(actions);
                    }
                }).catch(function (e) {
                    console.warn('[owner-extras] DM mark-read failed:', e);
                });
            },
        });
        appendActionsContainer(entry, [btn]);
    }

    // 06-profiles Phase 2: rollover-CTA wiring for profile entries.
    //
    // renderProfile in stream.js leaves an empty .entry-actions slot
    // on each entry; this decorator fills it with a Follow or Unfollow
    // button (variant determined by meta.following). The button hits
    // POST/DELETE /api/following — the same mutation endpoint the
    // legacy following list uses, so server-side this is a no-new-route
    // change. Optimistic UI flips the state line + button label before
    // the server responds; on error we revert and warn.
    //
    // Self-protection: profiles for the local tenant never show a CTA
    // (defense in depth — buildProfilesList already filters self, but
    // a stale render shouldn't be able to follow-self.)
    function decorateProfile(entry, meta) {
        if (!entry || !meta) return;
        if (meta.author_domain && meta.author_domain === ownerDomain()) return;

        var currentFollowing = !!meta.following;
        var profileURL = meta.author_url || (meta.author_domain ? ('https://' + meta.author_domain) : '');
        if (!profileURL) return; // no actionable target

        // Recent-post-attached click → toggle .is-expanded with
        // singleton semantics. Opening one collapses any other
        // .recent-post-attached.is-expanded on the page. The CSS
        // handles the visual swap (line-clamp on/off, chevron rotate,
        // accent border deepening). The full-body lazy-fetch the
        // mockup hints at is deferred — Phase 2 ships excerpt-only
        // since the feed cache already carries it; Phase 2.x can
        // wire the per-post fetch off the data-polis-recent-post-url
        // attribute we already set in renderProfile.
        var attached = entry.querySelector(':scope > .recent-post-attached');
        if (attached && !attached.dataset.polisExpandWired) {
            attached.dataset.polisExpandWired = '1';
            attached.addEventListener('click', function (ev) {
                ev.stopPropagation();
                var nowOpen = !attached.classList.contains('is-expanded');
                // Singleton collapse — find any other open block and close it.
                var others = document.querySelectorAll('.recent-post-attached.is-expanded');
                for (var i = 0; i < others.length; i++) {
                    if (others[i] !== attached) others[i].classList.remove('is-expanded');
                }
                attached.classList.toggle('is-expanded', nowOpen);
            });
        }

        // Whether the OTHER side follows ME — frozen from the initial
        // render's relationship snapshot. Used to compute the right
        // bilateral label after a toggle ("mutual" / "follows you")
        // instead of collapsing everything to "following" / "not following".
        var theyFollowMe = meta.relationship === 'mutual' || meta.relationship === 'follows-you';

        function setStateLine(nowFollowing) {
            entry.dataset.polisProfileFollowing = nowFollowing ? '1' : '0';
            // renderProfile in stream.js emits .follow-state on the
            // entry-meta-line byline (not inside a .profile-state-line
            // wrapper). The old selector silently no-op'd, so the
            // optimistic flip never reached the DOM.
            var fs = entry.querySelector('.follow-state');
            if (!fs) return;
            var label;
            if (nowFollowing && theyFollowMe) label = 'mutual';
            else if (!nowFollowing && theyFollowMe) label = 'follows you';
            else if (nowFollowing) label = 'following';
            else label = 'not following';
            fs.textContent = label;
            fs.classList.toggle('is-following', nowFollowing);
            fs.classList.toggle('is-not-following', !nowFollowing);
        }

        function paintCTA(nowFollowing) {
            currentFollowing = nowFollowing;
            var label = nowFollowing ? 'Unfollow' : '+ Follow';
            var variant = nowFollowing ? 'danger' : 'primary';
            var btn = makeActionButton(label, {
                className: variant,
                title: nowFollowing
                    ? 'Unfollow ' + (meta.author_domain || '')
                    : 'Follow ' + (meta.author_domain || ''),
                onclick: function () {
                    var becoming = !currentFollowing;
                    var method = becoming ? 'POST' : 'DELETE';
                    // Optimistic flip — UI follows the user's intent
                    // before the server confirms. Revert on failure.
                    setStateLine(becoming);
                    paintCTA(becoming);
                    fetch('/api/following', {
                        method: method,
                        headers: { 'Content-Type': 'application/json' },
                        body: JSON.stringify({ url: profileURL }),
                        credentials: 'same-origin',
                    }).then(function (r) {
                        if (!r.ok) throw new Error('HTTP ' + r.status);
                    }).catch(function (err) {
                        // Revert state + CTA. Toast surfacing the
                        // failure is the host app's responsibility;
                        // log here so dev tools show the cause.
                        setStateLine(!becoming);
                        paintCTA(!becoming);
                        console.warn('[owner-extras] follow toggle failed:', err);
                    });
                },
            });
            appendActionsContainer(entry, [btn]);
        }

        paintCTA(currentFollowing);
    }

    function registerStreamItemDecorators(stream) {
        stream.afterRender('post', decoratePost);
        stream.afterRender('comment', decorateComment);
        stream.afterRender('dm', decorateDM);
        stream.afterRender('profile', decorateProfile);
    }

    // ── Persist local read state ──────────────────────────────────
    //
    // stream.js fires a `polis:entry-read` CustomEvent when an entry
    // transitions from unread → read in-DOM (scroll-past clearing,
    // focus-mode comment-thread opens). Without persistence the cleared
    // state lives in this page session only — a reload re-renders the
    // entry as unread because the server-side feed cache still flags it.
    //
    // POST /api/feed/read writes through to the local feed cache
    // (.polis/ds/<domain>/state/pub.polis.feed.jsonl). v3's app.js used
    // the same endpoint via _flushMarkRead — we replicate the batching
    // pattern here so v4's read-state persists without depending on
    // v3 SPA code (which is slated for retirement).
    var readQueue = [];
    var readQueueSeen = Object.create(null);
    var readFlushTimer = null;
    function flushReadQueue() {
        readFlushTimer = null;
        var ids = readQueue.splice(0);
        readQueueSeen = Object.create(null);
        if (!ids.length) return;
        ids.forEach(function (id) {
            fetch('/api/feed/read', {
                method: 'POST',
                headers: { 'Content-Type': 'application/json' },
                body: JSON.stringify({ id: id }),
            }).catch(function () { /* ignore */ });
        });
    }
    function setupReadPersistence() {
        document.addEventListener('polis:entry-read', function (ev) {
            var id = ev && ev.detail && ev.detail.id;
            if (!id || readQueueSeen[id]) return;
            readQueueSeen[id] = true;
            readQueue.push(id);
            if (readFlushTimer) clearTimeout(readFlushTimer);
            readFlushTimer = setTimeout(flushReadQueue, 1000);
        });
    }

    // ====================================================================
    // R22 #11 — Scratch-column owner card
    // ====================================================================
    //
    // Mirrors the v4 public site-identity layout: avatar + name +
    // handle + bio + stats. Differences for the owner SPA:
    //   - stats are post / draft counts (not posts/followers/following)
    //   - bio supports inline editing (click bio area or "edit" link)
    //   - Save/Cancel writes through /api/about and re-renders
    //
    // Public CSS already covers the layout (.layout-left, .site-bio*,
    // .site-stats). The setupAboutToggle / setupStatLinks routines
    // in stream.js handle show/hide collapse and stat-link clicks
    // automatically as long as the markup matches — they were always
    // built generic; we just ship empty placeholders that this
    // function fills in.

    var ownerCardEditing = false;

    function setupOwnerCard() {
        var card = document.getElementById('owner-card');
        if (!card) return;
        // Fetch in parallel — populate as each lands.
        loadOwnerCardIdentity();
        loadOwnerCardBio();
        loadOwnerCardCounts();
        wireOwnerCardEdit();
        // Visibility is gated on the active sentence — owner card only
        // shows for "all <type> from me" where type is posts | drafts |
        // comments. Subscribe via onFilterChange so dropdown picks +
        // preset clicks + programmatic setters all sync. Also derive
        // initial state from the current controller snapshot if
        // available, so the card lands in the right state on first
        // paint without waiting for the first filter event.
        if (window.PolisStream && typeof window.PolisStream.onFilterChange === 'function') {
            window.PolisStream.onFilterChange(syncOwnerCardVisibility);
        }
        // Best-effort initial sync — the filter slots are SSR'd so we
        // can read them directly. Defaults to hidden if we can't tell.
        var initialType = readSlotText('type');
        var initialScope = readSlotText('scope');
        syncOwnerCardVisibility({ type: initialType, scope: initialScope });
    }

    function readSlotText(slotKind) {
        var el = document.querySelector('.sentence-filter .sf-slot[data-filter-slot="' + slotKind + '"]');
        return el ? (el.textContent || '').trim() : '';
    }

    function syncOwnerCardVisibility(snapshot) {
        var card = document.getElementById('owner-card');
        if (!card) return;
        var t = snapshot && snapshot.type;
        var s = snapshot && snapshot.scope;
        var ownerCardTypes = { posts: true, drafts: true, comments: true };
        // Editor-mode bail-out — if the user is mid-edit on the bio,
        // don't yank the form out from under them on a filter change.
        // Visibility resyncs the next time they change the filter.
        if (ownerCardEditing) return;
        var show = ownerCardTypes[t] && s === 'me';
        var wasHidden = card.classList.contains('hidden');
        card.classList.toggle('hidden', !show);
        // 2026-05-18: when transitioning from hidden→visible, re-run the
        // bio overflow measurement. setupAboutToggle's initial pass may
        // have run while the card was display:none (initial SSR filter
        // is "from my network", not "me", so the card boots hidden) —
        // bio.scrollHeight would have read 0, falsely concluded no
        // overflow, and the bio got stuck expanded. Re-measure now that
        // the card has real geometry. setupAboutToggle is idempotent.
        if (wasHidden && show && window.PolisStream
            && typeof window.PolisStream.setupAboutToggle === 'function') {
            window.PolisStream.setupAboutToggle();
        }
    }

    function loadOwnerCardIdentity() {
        // R25 follow-up: previously guarded on `window.App` but the
        // SPA declares App as a top-level `const`, which doesn't
        // attach to window in modern script semantics. The guard
        // therefore short-circuited every invocation — /api/status
        // was never fetched and the topbar handle / owner-card name
        // never populated. Function uses fetch directly (no App
        // dependency) so the guard is just dead weight.
        fetch('/api/status', { credentials: 'same-origin' })
            .then(function (resp) { return resp.ok ? resp.json() : null; })
            .then(function (data) {
                if (!data) return;
                // R23 #6: avatar element removed from owner card markup.
                // R25 follow-up #4: populate the topbar label at the
                // right edge with site_title from .well-known/polis
                // (the human-readable site name), falling back to
                // author_name, then to the base_url hostname. base_url
                // becomes the link target either way.
                var topbarHandle = document.getElementById('polis-topbar-handle');
                if (topbarHandle) {
                    // 2026-05-18: the topbar identity marker is the polis
                    // handle (e.g. `vdibart.polis.pub`), not a site title
                    // or author name. Always derive from base_url hostname.
                    // Earlier cascade tried site_title first, but on hosted
                    // tenants without an explicit site_title the server's
                    // GetSiteTitle() falls back to the full POLIS_BASE_URL
                    // — so users saw `https://vdibart.polis.pub` in the
                    // topbar instead of just the handle. Localhost didn't
                    // repro because POLIS_BASE_URL is usually unset there
                    // and the cascade tumbled past site_title to a sensible
                    // value. The fix: skip the cascade for this slot and
                    // always use URL(base_url).hostname.
                    var label = '';
                    if (data.base_url) {
                        try { label = new URL(data.base_url).hostname; }
                        catch (e) { /* ignore */ }
                    }
                    topbarHandle.textContent = label;
                    if (data.base_url) topbarHandle.setAttribute('href', data.base_url);
                }
                // 2026-05-18: only show name when it differs from the
                // handle. Tenants that haven't set a distinct author_name
                // publish the bare domain as their "name" (e.g. discover
                // .polis.pub's .well-known has author_name="discover
                // .polis.pub"), which would render the same string twice
                // — once as #owner-card-name and again as #owner-card
                // -handle. Mirrors the public-template fix at
                // page.go: ctx.AuthorName = "" when name == domain.
                var nameEl = document.getElementById('owner-card-name');
                var handleEl = document.getElementById('owner-card-handle');
                var hostname = '';
                if (data.base_url) {
                    try { hostname = new URL(data.base_url).hostname; }
                    catch (e) { /* ignore */ }
                }
                if (handleEl && hostname) handleEl.textContent = hostname;
                if (nameEl) {
                    var name = data.author_name || '';
                    nameEl.textContent = (name && name !== hostname) ? name : '';
                }
                // After identity lands, re-run setupAboutToggle so it
                // measures the bio's overflow against the now-populated
                // column. setupAboutToggle is idempotent — calling it
                // again just re-attaches handlers and re-measures.
                if (window.PolisStream && typeof window.PolisStream.setupAboutToggle === 'function') {
                    window.PolisStream.setupAboutToggle();
                }
            }).catch(function () { /* ignore */ });
    }

    function loadOwnerCardBio() {
        fetch('/api/about', { credentials: 'same-origin' })
            .then(function (resp) { return resp.ok ? resp.json() : null; })
            .then(function (data) {
                if (!data) return;
                var bioEl = document.getElementById('owner-card-bio');
                if (!bioEl) return;
                // /api/about already round-trips the markdown through
                // goldmark and ships content_html alongside the raw
                // content (handlers.go:3365). Use the server-rendered
                // HTML so inline formatting (bold/italic/links/lists)
                // renders the same way it does on the public surface.
                // Stash the raw markdown for the editor to load when
                // the user clicks "edit".
                var md = (data.content || '').trim();
                bioEl.dataset.rawMarkdown = md;
                if (!md) {
                    bioEl.innerHTML = '';
                    return;
                }
                bioEl.innerHTML = data.content_html || '';
                if (window.PolisStream && typeof window.PolisStream.setupAboutToggle === 'function') {
                    window.PolisStream.setupAboutToggle();
                }
            }).catch(function () { /* ignore */ });
    }

    function loadOwnerCardCounts() {
        // Posts count.
        fetch('/api/posts', { credentials: 'same-origin' })
            .then(function (resp) { return resp.ok ? resp.json() : null; })
            .then(function (data) {
                if (!data) return;
                var n = (data.posts && data.posts.length) || data.count || 0;
                var el = document.getElementById('owner-card-posts-count');
                if (el) el.textContent = String(n);
            }).catch(function () { /* ignore */ });
        // Drafts count.
        fetch('/api/drafts', { credentials: 'same-origin' })
            .then(function (resp) { return resp.ok ? resp.json() : null; })
            .then(function (data) {
                if (!data) return;
                var n = (data.drafts && data.drafts.length) || 0;
                var el = document.getElementById('owner-card-drafts-count');
                if (el) el.textContent = String(n);
            }).catch(function () { /* ignore */ });
    }

    function wireOwnerCardEdit() {
        var editLink = document.getElementById('owner-card-edit-link');
        var bioWrap = document.getElementById('owner-card-bio-view');
        var bioEdit = document.getElementById('owner-card-bio-edit');
        var bioEl = document.getElementById('owner-card-bio');
        var ta = document.getElementById('owner-card-bio-textarea');
        var saveBtn = document.getElementById('owner-card-bio-save');
        var cancelBtn = document.getElementById('owner-card-bio-cancel');
        if (!editLink || !bioWrap || !bioEdit || !bioEl || !ta || !saveBtn || !cancelBtn) return;

        function openEdit() {
            if (ownerCardEditing) return;
            ownerCardEditing = true;
            ta.value = bioEl.dataset.rawMarkdown || '';
            bioWrap.classList.add('hidden');
            bioEdit.classList.remove('hidden');
            // Try to mount Milkdown over the textarea. Same pattern as
            // the inline post editor — textarea is the always-on
            // baseline; promote on success.
            if (window.App && typeof window.App._initMilkdown === 'function' && window.MilkdownBridge) {
                try {
                    var p = window.App._initMilkdown('owner-card-bio-textarea');
                    Promise.resolve(p).then(function () {
                        var mount = document.getElementById('milkdown-owner-card-bio');
                        if (mount) mount.classList.remove('hidden');
                        ta.classList.add('hidden');
                    }).catch(function () { /* keep textarea */ });
                } catch (e) { /* keep textarea */ }
            }
            ta.focus();
        }
        function closeEdit() {
            if (!ownerCardEditing) return;
            ownerCardEditing = false;
            // Tear down Milkdown if we mounted it.
            if (window.App && typeof window.App._destroyMilkdown === 'function') {
                try { window.App._destroyMilkdown('owner-card-bio-textarea'); } catch (e) { /* ignore */ }
            }
            var mount = document.getElementById('milkdown-owner-card-bio');
            if (mount) mount.classList.add('hidden');
            ta.classList.remove('hidden');
            bioEdit.classList.add('hidden');
            bioWrap.classList.remove('hidden');
        }

        editLink.addEventListener('click', function (ev) {
            ev.preventDefault();
            ev.stopPropagation();
            openEdit();
        });
        cancelBtn.addEventListener('click', function () { closeEdit(); });
        saveBtn.addEventListener('click', function () {
            var content = (window.App && typeof window.App.getEditorContent === 'function')
                ? window.App.getEditorContent('owner-card-bio-textarea')
                : ta.value;
            saveBtn.disabled = true;
            cancelBtn.disabled = true;
            fetch('/api/about', {
                method: 'POST',
                credentials: 'same-origin',
                headers: { 'Content-Type': 'application/json' },
                body: JSON.stringify({ content: content }),
            }).then(function (resp) {
                saveBtn.disabled = false;
                cancelBtn.disabled = false;
                if (!resp.ok) throw new Error('save failed');
                closeEdit();
                loadOwnerCardBio();
            }).catch(function (err) {
                console.warn('[owner-extras] about save failed:', err);
                saveBtn.disabled = false;
                cancelBtn.disabled = false;
            });
        });
    }

    // ====================================================================
    // step-06/6.h — Inline post editor card
    // ====================================================================
    //
    // Editor lives as a real .entry inside .stream — same DOM grammar as
    // a regular stream entry (timeline-dot + content), with editor-
    // specific chrome and a sentinel data-polis-pinned="editor" attr
    // that the filter-change clear logic in stream.js skips so the
    // editor stays put across filter changes (resolved decision #17).
    //
    // Spec (resolved decisions #13, #17, #18, #19):
    //   - Click edit icon → editor mounts as first item in the timeline.
    //   - Fixed-height card (CSS); inner scroll on body. Stream below
    //     remains visible.
    //   - Sticky across filter changes ONLY. Cross-screen nav / refresh
    //     do NOT preserve (the underlying draft is autosaved to disk;
    //     just the card state isn't restored).
    //   - Tiered Esc: in focus mode → exit focus (editor stays);
    //     out of focus mode → close editor (= Cancel button).
    //   - Ctrl+Shift+F → full-screen takeover via body.is-editor-focus.
    //   - Buttons: Publish + Save as draft + last-saved timestamp.

    // EDITOR_TITLE_ID removed — the editor card no longer has a
    // dedicated title input. Title is derived server-side from the
    // body's leading `# heading` (via cli-go's publish.ExtractTitle);
    // no `#` → renderer's TitleStartsFirstSentence redundancy detector
    // suppresses title chrome at every render path.
    var EDITOR_BODY_ID  = 'stream-editor-body';
    var EDITOR_MILKDOWN_ID = 'milkdown-stream-editor';
    var EDITOR_STATUS_ID = 'stream-editor-status';
    var EDITOR_CARD_SELECTOR = '.entry--editor[data-polis-pinned="editor"]';

    // Per-editor-session state; reset on close.
    //   draftId  — set when editing a draft. Save → POST /api/drafts.
    //   postPath — set when editing a previously-published post (republish
    //              mode). Save/Publish → POST /api/republish keyed on the
    //              source path; "Save as draft" is hidden in this mode
    //              since the post is already live.
    //   draftId + postPath are mutually exclusive — opening one clears
    //   the other in openEditor / closeEditor.
    var editorState = {
        draftId: '',
        postPath: '',
        lastSavedAt: '',
        autoSaveTimer: null,
    };

    function isEditorOpen() {
        return !!document.querySelector(EDITOR_CARD_SELECTOR);
    }

    function isEditorFocusMode() {
        return document.body.classList.contains('is-editor-focus');
    }

    // Open the editor card. Idempotent — re-opening when already open
    // is a no-op (focuses the body). If not on stream-screen,
    // navigates to /feed first; the card mounts after activation.
    //
    // opts.draftId  — fetches /api/drafts/{id} + pre-fills the body
    //                 for resuming a draft.
    // opts.postPath — fetches /api/posts/{path} + pre-fills the body
    //                 for editing a published post (republish mode).
    // (Mutually exclusive — caller should pass at most one.)
    // Latest filter snapshot, updated by the onFilterChange subscription
    // below. Read by openEditor to route the ghost-compose CTA to the
    // right editor surface (post / DM-new / DM-reply) per the current
    // type+scope.
    var currentFilterSnapshot = null;

    function openEditor(opts) {
        opts = opts || {};
        // Profiles dispatch: there is no "compose a profile" action — the
        // + on this surface should focus the search input that sits where
        // the ghost-compose normally lives. We never mount the post editor
        // here (it makes no sense in context and the user has flagged its
        // appearance as a bug).
        var fs = currentFilterSnapshot;
        if (fs && fs.type === 'profiles') {
            var searchInput = document.querySelector('.entry--search input');
            if (searchInput) {
                try { searchInput.focus(); } catch (e) { /* ignore */ }
            }
            return;
        }
        // DM dispatch: when the user is on a DM surface, the ghost-compose
        // CTA opens the DM composer instead of the post editor. Inbox
        // (scope=my-mutuals) → new-conversation mode with recipient
        // picker. Thread (scope=@<handle>) → reply mode targeting that
        // peer. Any other dm scope (none today) falls back to no-op.
        if (fs && fs.type === 'dms') {
            if (fs.scope === 'my-mutuals') {
                openDMComposer('new');
                return;
            }
            if (typeof fs.scope === 'string' && fs.scope.indexOf('@') === 0) {
                openDMComposer('reply', { replyPeer: fs.scope.substring(1) });
                return;
            }
        }
        var apply = function () {
            // Exit any active read-focus before opening write-focus — the
            // user is pivoting from "reading this" to "editing this," not
            // doing both. Without this, clicking the Edit rollover CTA on
            // a post the user had previously click-focused leaves the
            // read-focus dim+blur active AND mounts the editor on top,
            // visually combining both modes. Same class of bug we fixed
            // for drafts at 3d56ff9 (eligibility exclusion) — generalized
            // here to all editor entry points so any future surface that
            // calls openEditor gets the clean transition for free.
            if (window.PolisStream && typeof window.PolisStream.exitFocusMode === 'function') {
                try { window.PolisStream.exitFocusMode(); } catch (e) { /* ignore */ }
            }
            if (isEditorOpen()) {
                var bodyEl = document.getElementById(EDITOR_BODY_ID);
                if (bodyEl) bodyEl.focus();
                return;
            }
            mountEditorCard();
            if (opts.draftId) {
                loadDraftIntoEditor(opts.draftId);
            } else if (opts.postPath) {
                loadPostIntoEditor(opts.postPath);
            }
        };

        var streamScreen = document.getElementById('stream-screen');
        var streamActive = streamScreen && !streamScreen.classList.contains('hidden');
        if (streamActive) {
            apply();
            return;
        }
        if (window.App && typeof window.App.navigateTo === 'function') {
            var navResult = window.App.navigateTo('/');
            if (navResult && typeof navResult.then === 'function') {
                navResult.then(apply);
            } else {
                apply();
            }
        } else {
            apply();
        }
    }

    // Fetch draft markdown and pre-fill the editor. Loads the full
    // markdown (including any leading `# Title` heading) into the
    // body — the editor no longer has a separate title input, so the
    // heading round-trips as the first line of the body. If Milkdown
    // has already mounted by the time the fetch resolves, the
    // textarea swap will have happened — we set the textarea value
    // before Milkdown mounts (when possible) and call
    // MilkdownBridge.setMarkdown to reseed if it has. Fallback:
    // textarea remains the source of truth if Milkdown never hydrates.
    function loadDraftIntoEditor(draftId) {
        editorState.draftId = draftId;
        // No status pill on draft load — the editor opening with content
        // visible is self-evidently a successful load. Status is reserved
        // for transient state (Saving… / Saved / Save failed) that the
        // user can't otherwise observe.
        fetch('/api/drafts/' + encodeURIComponent(draftId), { credentials: 'same-origin' })
            .then(function (r) {
                if (!r.ok) throw new Error('HTTP ' + r.status);
                return r.json();
            })
            .then(function (data) {
                var md = (data && data.markdown) || '';
                var bodyEl = document.getElementById(EDITOR_BODY_ID);
                if (bodyEl) {
                    bodyEl.value = md;
                    // If Milkdown already mounted by the time the
                    // fetch resolves, push the new markdown into it.
                    // App._milkdownIdFor resolves the textarea's mount
                    // (milkdown-stream-editor) via sibling lookup. If
                    // Milkdown hasn't mounted yet, the textarea value
                    // is the seed _initMilkdown picks up.
                    if (window.App && typeof window.App._milkdownIdFor === 'function' && window.MilkdownBridge) {
                        var editorId = window.App._milkdownIdFor(EDITOR_BODY_ID);
                        if (editorId && window.MilkdownBridge.isReady && window.MilkdownBridge.isReady(editorId)) {
                            try { window.MilkdownBridge.setMarkdown(editorId, md); } catch (e) { /* ignore */ }
                        }
                    }
                }
            })
            .catch(function () {
                setEditorStatus('Load failed');
            });
    }

    // Load a published post into the editor for republish-mode editing.
    // Fetches /api/posts/{path}, pre-fills the body with the existing
    // markdown. Reconstructs a leading `# Title` from frontmatter if
    // the body doesn't already start with one — the user sees + can
    // edit the title via the body's first line per the v4 convention.
    // publishEditor inspects editorState.postPath to route the publish
    // action through /api/republish (sign-with-updated-version) rather
    // than /api/publish (new-post).
    function loadPostIntoEditor(postPath) {
        editorState.postPath = postPath;
        editorState.draftId = '';
        fetch('/api/posts/' + encodeURIComponent(postPath), { credentials: 'same-origin' })
            .then(function (r) {
                if (!r.ok) throw new Error('HTTP ' + r.status);
                return r.json();
            })
            .then(function (data) {
                var body = (data && data.markdown) || '';
                var title = (data && data.title) || '';
                var bodyStartsWithHeading = /^#\s+/.test(body.trim());
                if (!bodyStartsWithHeading && title) {
                    body = '# ' + title + '\n\n' + body;
                }
                var bodyEl = document.getElementById(EDITOR_BODY_ID);
                if (bodyEl) {
                    bodyEl.value = body;
                    if (window.App && typeof window.App._milkdownIdFor === 'function' && window.MilkdownBridge) {
                        var editorId = window.App._milkdownIdFor(EDITOR_BODY_ID);
                        if (editorId && window.MilkdownBridge.isReady && window.MilkdownBridge.isReady(editorId)) {
                            try { window.MilkdownBridge.setMarkdown(editorId, body); } catch (e) { /* ignore */ }
                        }
                    }
                }
                // Hide the "Save as draft" button in republish mode —
                // the post is already live; saving it back to drafts
                // would be a separate concept (unpublish-to-drafts)
                // that has its own dedicated flow.
                var card = document.querySelector(EDITOR_CARD_SELECTOR);
                if (card) {
                    card.classList.add('is-republish');
                }
                // Surface the mode in the primary action's label.
                var primaryBtn = document.querySelector(EDITOR_CARD_SELECTOR + ' .editor-card-footer button.primary');
                if (primaryBtn) primaryBtn.textContent = 'Republish';
                // Hide the source entry while it's being edited — the
                // editor card up top already shows its content. CSS
                // (stream.css `.entry.is-being-edited`) handles the
                // hide. closeEditor's `.is-being-edited` cleanup
                // removes the class so the entry re-shows on cancel,
                // and refreshStream after a successful republish
                // re-renders the entry with the new content anyway.
                var matchingEntries = document.querySelectorAll(
                    '.entry[data-polis-source-path="' + (typeof CSS !== 'undefined' && CSS.escape ? CSS.escape(postPath) : postPath) + '"]'
                );
                for (var i = 0; i < matchingEntries.length; i++) {
                    matchingEntries[i].classList.add('is-being-edited');
                }
            })
            .catch(function () {
                setEditorStatus('Load failed');
            });
    }

    // Close the editor card. Drops the card from the DOM (and exits
    // focus mode if active). Underlying draft autosave file persists
    // independently — closing the card doesn't delete the draft.
    function closeEditor() {
        // Tear down Milkdown before unmounting so the editor instance
        // + its event listeners + the underlying ProseMirror state
        // don't leak. App._destroyMilkdown is keyed by textarea ID,
        // not mount ID. No-op if Milkdown never hydrated (fallback
        // textarea path).
        if (window.App && typeof window.App._destroyMilkdown === 'function') {
            try { window.App._destroyMilkdown(EDITOR_BODY_ID); } catch (e) { /* ignore */ }
        }
        var card = document.querySelector(EDITOR_CARD_SELECTOR);
        if (card && card.parentNode) card.parentNode.removeChild(card);
        document.body.classList.remove('is-editor-focus');
        document.body.classList.remove('has-open-editor');
        if (editorState.autoSaveTimer) {
            clearTimeout(editorState.autoSaveTimer);
            editorState.autoSaveTimer = null;
        }
        editorState.draftId = '';
        editorState.postPath = '';
        editorState.lastSavedAt = '';
        // Re-show any entries hidden because the user was editing them
        // (republish mode). refreshStream after publish re-renders them
        // anyway, but on Cancel the stream isn't re-fetched, so we
        // restore visibility explicitly here.
        var hidden = document.querySelectorAll('.entry.is-being-edited');
        for (var i = 0; i < hidden.length; i++) {
            hidden[i].classList.remove('is-being-edited');
        }
        // R24 follow-up #6: drop the active state on the edit nav
        // button — the editor is no longer open.
        var editBtn = document.getElementById('nav-btn-edit');
        if (editBtn) editBtn.classList.remove('active');
    }

    function mountEditorCard() {
        var stream = document.querySelector('.stream');
        if (!stream) {
            console.warn('[polis-owner-extras] cannot mount editor: .stream container missing');
            return;
        }
        // body class drives CSS gates that only fire while the editor
        // is open (e.g. hiding .stream-top-fade so its gradient
        // doesn't bleed onto the editor's title).
        document.body.classList.add('has-open-editor');

        var card = document.createElement('article');
        card.className = 'entry entry--editor';
        card.setAttribute('data-polis-entry-type', 'editor');
        card.setAttribute('data-polis-pinned', 'editor');
        // tabindex=-1 makes the card focusable so the scoped keydown
        // listener (attached below) catches Esc/Ctrl+Shift+F bubbling
        // up from inputs and textareas inside the card.
        card.setAttribute('tabindex', '-1');

        // R27 follow-up: editor card omits the timeline-dot. The dot
        // marked the editor's position on the timeline like other
        // entries, but the editor is a compose surface, not a real
        // post indicator — and once the card was pinned right under
        // the topbar (margin-top: -nav-content-gap), the orphan dot
        // sat awkwardly under the nav with no adjacent timeline
        // activity. Letting the bar pass through the editor card
        // unbroken reads cleaner.

        // Rail + glyph as a real button — same gesture as the ghost-
        // compose row's + (which opens the editor): clicking this one
        // closes it. Visual styling lives in stream.css (.editor-card-
        // plus). Was previously a CSS pseudo on .entry--editor::before
        // but pseudos can't carry event handlers, so we couldn't wire
        // the click. Hidden in focus mode (CSS) since focus mode swaps
        // the rail-+ for a full-width dashed separator rule.
        var railPlus = document.createElement('button');
        railPlus.className = 'editor-card-plus';
        railPlus.type = 'button';
        railPlus.setAttribute('aria-label', 'Close editor');
        railPlus.addEventListener('click', function () { closeEditor(); });
        card.appendChild(railPlus);

        // R23 #16: header bar dropped — body is the first thing in
        // the card. Status indicator + cancel button move to the
        // footer alongside Save / Publish so the top of the editor is
        // distraction-free.
        // Title input dropped entirely: title is derived from the
        // body's leading `# heading` server-side (cli-go's
        // publish.ExtractTitle), and the render-layer redundancy
        // detector (cli-go/pkg/render/stream.go TitleStartsFirstSentence)
        // suppresses title chrome when no `#` heading was provided.
        // Convention: user types `# Foo` as the first line for a
        // titled post, or skips it for a title-less post.
        var status = document.createElement('span');
        status.className = 'editor-card-status';
        status.id = EDITOR_STATUS_ID;
        status.textContent = '';

        var bodyWrap = document.createElement('div');
        bodyWrap.className = 'editor-card-body';
        // R22 #8: textarea is visible by DEFAULT; the milkdown-mount
        // starts hidden. When MilkdownBridge.create successfully
        // hydrates the mount, we swap visibility (textarea → hidden,
        // mount → visible). Earlier shape had the mount visible from
        // the start with the textarea hidden — if Milkdown's deferred
        // ESM hadn't finished loading by the time the user clicked
        // into the body, they hit an empty <div> with no contenteditable
        // and "Milkdown not working" was actually "nothing's mounted
        // yet, your click went into the void."
        var milkdownMount = document.createElement('div');
        milkdownMount.className = 'milkdown-mount hidden';
        milkdownMount.id = EDITOR_MILKDOWN_ID;
        bodyWrap.appendChild(milkdownMount);
        var bodyArea = document.createElement('textarea');
        bodyArea.id = EDITOR_BODY_ID;
        bodyArea.placeholder = 'Write your post…';
        bodyArea.addEventListener('input', scheduleAutoSave);
        bodyWrap.appendChild(bodyArea);
        card.appendChild(bodyWrap);

        var footer = document.createElement('div');
        footer.className = 'editor-card-footer';
        // R23 #16: status indicator + cancel button moved here from
        // the dropped header. Order across the row: [status] [hint]
        // [...] [cancel] [save] [publish].
        var hint = document.createElement('span');
        hint.className = 'editor-card-hint';
        hint.innerHTML = '<kbd>Ctrl</kbd>+<kbd>Shift</kbd>+<kbd>F</kbd> focus';
        var spacer = document.createElement('span');
        spacer.className = 'spacer';
        var cancelBtn = document.createElement('button');
        cancelBtn.type = 'button';
        cancelBtn.className = 'editor-card-cancel-btn';
        cancelBtn.textContent = 'Cancel';
        cancelBtn.title = 'Cancel (Esc)';
        cancelBtn.addEventListener('click', function () { closeEditor(); });
        var saveBtn = document.createElement('button');
        saveBtn.type = 'button';
        saveBtn.textContent = 'Save as draft';
        saveBtn.addEventListener('click', function () { saveDraft(false); });
        var publishBtn = document.createElement('button');
        publishBtn.type = 'button';
        publishBtn.className = 'primary';
        publishBtn.textContent = 'Publish';
        publishBtn.addEventListener('click', publishEditor);
        footer.appendChild(status);
        footer.appendChild(hint);
        footer.appendChild(spacer);
        footer.appendChild(cancelBtn);
        footer.appendChild(saveBtn);
        footer.appendChild(publishBtn);
        card.appendChild(footer);

        // Mount as FIRST child of .stream so it sits above all entries.
        stream.insertBefore(card, stream.firstChild);

        // Card-scoped keyboard handler (Esc + Ctrl+Shift+F). Attached
        // here rather than at init so it lives only as long as the
        // card itself — no global Esc-trap when the editor isn't open.
        attachEditorKeyboardTo(card);

        // Try to mount Milkdown over the textarea (existing app.js
        // helper). MilkdownBridge is loaded as a deferred ES module
        // and may not be ready at the moment the user opens the
        // editor for the first time — wait for the milkdown:ready
        // event before initializing. Falls back to the textarea by
        // un-hiding it if Milkdown ultimately fails (the bridge fires
        // ready exactly once per page, so we use a memoized flag).
        // R22 #8: textarea is the always-on baseline. We try to mount
        // Milkdown over it; on success we hide the textarea + reveal
        // the mount. On failure (or if MilkdownBridge never loads),
        // the textarea stays visible and the user can still type.
        // CRITICAL: must verify Milkdown actually hydrated for THIS
        // editor instance before swapping visibility. App._initMilkdown
        // catches its own errors and falls back to textarea internally —
        // which means its returned promise resolves normally even on
        // failure, so a naive .then(promoteToMilkdown) would still
        // fire and undo the fallback (hide textarea, show empty mount).
        // MilkdownBridge.isReady(editorId) is the authoritative
        // success signal: only swap visibility when the instance
        // exists in the bridge's editors Map. Without this check,
        // any Milkdown init failure (e.g. lazy-import race, esm.sh
        // glitch, MilkdownError on missing plugin context) leaves
        // the user with hidden textarea + broken mount = unclickable
        // body. */
        var promoteToMilkdown = function () {
            if (!window.MilkdownBridge) return;
            if (typeof window.MilkdownBridge.isReady !== 'function') return;
            if (!window.MilkdownBridge.isReady(EDITOR_MILKDOWN_ID)) return;
            var mount = document.getElementById(EDITOR_MILKDOWN_ID);
            var ta = document.getElementById(EDITOR_BODY_ID);
            if (!mount || !ta) return;
            mount.classList.remove('hidden');
            ta.classList.add('hidden');
            // Transfer focus to ProseMirror's contenteditable. The
            // earlier bodyArea.focus() landed on the textarea which is
            // now hidden — without this transfer, the user has to
            // click into the body before they can type. ProseMirror
            // renders its editable surface as the `.ProseMirror` div
            // inside the mount (its contenteditable attr is set
            // there); calling focus() on it is the canonical way to
            // resume cursor-in-editor.
            var pmEditable = mount.querySelector('.ProseMirror');
            if (pmEditable && typeof pmEditable.focus === 'function') {
                pmEditable.focus();
            }
        };
        // Kick off Milkdown init. App._initMilkdown now handles the
        // lazy bridge import internally (~5 esm.sh requests on first
        // open thanks to the ?bundle importmap), so we no longer wait
        // for a global 'milkdown:ready' event. On import failure the
        // textarea fallback is left in place.
        var initMilkdownNow = function () {
            if (!window.App || typeof window.App._initMilkdown !== 'function') return;
            if (!document.getElementById(EDITOR_BODY_ID)) return; // editor closed
            try {
                var p = window.App._initMilkdown(EDITOR_BODY_ID);
                Promise.resolve(p).then(promoteToMilkdown).catch(function () {});
            } catch (e) { /* keep textarea fallback */ }
        };
        initMilkdownNow();

        // Focus the body so the user starts typing immediately. If
        // Milkdown ends up hydrating the contenteditable, focus
        // transfers there naturally (the textarea is hidden but the
        // mount inherits from this element's initial focus state via
        // ProseMirror's initial render).
        bodyArea.focus();

        // R24 follow-up #6: light up the edit nav button so the user
        // can read at a glance "the editor is currently open". Cleared
        // by closeEditor.
        var editBtn = document.getElementById('nav-btn-edit');
        if (editBtn) editBtn.classList.add('active');
    }

    // Generate a slug-based draft ID from a title-like string.
    // Mirrors App._slugFromTitle's behavior cheaply (lowercase,
    // alnum-or-dash). Callers derive the title-like string from the
    // body's leading `# heading` via App.extractTitleFromMarkdown.
    function slugFromTitle(title) {
        return (title || '').toLowerCase()
            .replace(/[^a-z0-9]+/g, '-')
            .replace(/^-+|-+$/g, '')
            .substring(0, 80);
    }

    function setEditorStatus(text, persisted) {
        var statusEl = document.getElementById(EDITOR_STATUS_ID);
        if (statusEl) statusEl.textContent = text || '';
        if (persisted) editorState.lastSavedAt = new Date().toISOString();
    }

    function getEditorContent() {
        // Prefer App.getEditorContent (the abstraction that reads from
        // Milkdown when ready, textarea otherwise) over reading the
        // textarea directly. Milkdown only writes back to the textarea
        // via its listener plugin in some lifecycle paths; the
        // abstraction guarantees we get the latest content from
        // wherever it lives.
        // No title field anymore — body is the source of truth; any
        // leading `# heading` is part of the body markdown.
        var body = '';
        if (window.App && typeof window.App.getEditorContent === 'function') {
            body = window.App.getEditorContent(EDITOR_BODY_ID) || '';
        } else {
            var bodyEl = document.getElementById(EDITOR_BODY_ID);
            body = bodyEl ? bodyEl.value : '';
        }
        return { body: body };
    }

    function scheduleAutoSave() {
        if (editorState.autoSaveTimer) clearTimeout(editorState.autoSaveTimer);
        editorState.autoSaveTimer = setTimeout(function () {
            saveDraft(true);
        }, 1500);
    }

    function saveDraft(silent) {
        // In republish mode the post is already live — autosave + the
        // "Save as draft" button both no-op so we don't silently fork
        // a draft from the published post on every keystroke.
        if (editorState.postPath) return;
        var c = getEditorContent();
        if (!c.body.trim()) return;
        if (!silent) setEditorStatus('Saving…');
        // Derive draftId from the body's leading `# heading` (if any)
        // via App.extractTitleFromMarkdown. If there's no heading we
        // leave the id empty; the server's /api/drafts endpoint names
        // it (untitled-<random> via publish.Slugify).
        if (!editorState.draftId && window.App && typeof window.App.extractTitleFromMarkdown === 'function') {
            var derivedTitle = window.App.extractTitleFromMarkdown(c.body);
            if (derivedTitle) {
                editorState.draftId = slugFromTitle(derivedTitle);
            }
        }
        var wasNewDraft = !editorState.draftId;
        return fetch('/api/drafts', {
            method: 'POST',
            headers: { 'Content-Type': 'application/json' },
            credentials: 'same-origin',
            body: JSON.stringify({ id: editorState.draftId || '', markdown: c.body }),
        }).then(function (r) {
            if (!r.ok) throw new Error('HTTP ' + r.status);
            return r.json();
        }).then(function (result) {
            if (result && result.id) editorState.draftId = result.id;
            setEditorStatus('Saved', true);
            // First save creates a new file on disk → owner-card drafts
            // count needs to tick up. Subsequent autosaves to the same
            // draft are no-ops on the count, so we only refresh on the
            // transition from no-draftId to has-draftId.
            if (wasNewDraft && editorState.draftId) {
                refreshOwnerCounts();
            }
        }).catch(function () {
            setEditorStatus('Save failed');
        });
    }

    function publishEditor() {
        var c = getEditorContent();
        if (!c.body.trim()) return;
        var isRepublish = !!editorState.postPath;
        var primaryLabel = isRepublish ? 'Republish' : 'Publish';
        var publishBtn = document.querySelector(EDITOR_CARD_SELECTOR + ' .editor-card-footer button.primary');
        if (publishBtn) {
            publishBtn.disabled = true;
            publishBtn.textContent = isRepublish ? 'Republishing…' : 'Publishing…';
        }
        var endpoint, payload;
        if (isRepublish) {
            endpoint = '/api/republish';
            payload = { path: editorState.postPath, markdown: c.body };
        } else {
            endpoint = '/api/publish';
            payload = { markdown: c.body, filename: '', draft_id: editorState.draftId || '' };
        }
        fetch(endpoint, {
            method: 'POST',
            headers: { 'Content-Type': 'application/json' },
            credentials: 'same-origin',
            body: JSON.stringify(payload),
        }).then(function (r) {
            if (!r.ok) throw new Error('HTTP ' + r.status);
            return r.json();
        }).then(function (result) {
            if (result && result.success) {
                if (window.App && typeof window.App.showToast === 'function') {
                    var verb = isRepublish ? 'Republished' : 'Published';
                    window.App.showToast(verb + ': ' + (result.title || 'Post'), 'success');
                }
                closeEditor();
                // Re-fetch the stream + refresh the owner-card stat counts
                // so the published / republished post lands in view and
                // the "N posts" stat ticks up immediately. Without the
                // counts refresh the bio row stays stale until reload.
                refreshStream();
                refreshOwnerCounts();
            }
        }).catch(function (e) {
            if (window.App && typeof window.App.showToast === 'function') {
                window.App.showToast('Failed to ' + primaryLabel.toLowerCase() + ': ' + e.message, 'error');
            }
            if (publishBtn) { publishBtn.disabled = false; publishBtn.textContent = primaryLabel; }
        });
    }

    // Tiered Esc + focus-mode keyboard handler. Scoped to the editor
    // card via `attachEditorKeyboardTo(card)` rather than the document.
    // Document-level scoping (the prior shape) intercepted Esc whenever
    // the editor was open — including mid-typing in the body
    // when the user pressed Esc to dismiss a native autocomplete or to
    // clear focus. Card-scoped means: only when the event originated
    // inside the editor (focus/keydown bubbles up from any input or
    // textarea inside the card).
    function editorCardKeydown(ev) {
        // Ctrl+Shift+F: toggle focus mode (enter ↔ exit). Esc still
        // exits focus mode too (tiered handler below); having both
        // makes Ctrl+Shift+F a true toggle for muscle-memory users
        // who pressed it to enter.
        if ((ev.ctrlKey || ev.metaKey) && ev.shiftKey && (ev.key === 'F' || ev.key === 'f')) {
            ev.preventDefault();
            document.body.classList.toggle('is-editor-focus');
            return;
        }
        // Esc: tiered behavior. In focus mode → exit focus only
        // (editor stays). Out of focus mode → close editor
        // (= Cancel). Outside editor: not reached because the
        // listener is scoped to the card.
        if (ev.key === 'Escape') {
            ev.preventDefault();
            if (isEditorFocusMode()) {
                document.body.classList.remove('is-editor-focus');
            } else {
                closeEditor();
            }
        }
    }

    function attachEditorKeyboardTo(card) {
        if (!card) return;
        card.addEventListener('keydown', editorCardKeydown);
    }

    // ====================================================================
    // Inline comment editor — child of post entry
    // ====================================================================
    //
    // Clicking the comment-add CTA on a post's timeline-dot opens an
    // inline editor mounted INSIDE the post's .entry article, beneath
    // .entry-body. Visually it reads as part of the post (theme's
    // --color-comment-accent border + gradient bg, matching the existing
    // .comment-attached chrome) so the user perceives "commenting on the
    // post," not "threading off the existing comment." If the post
    // already has a .comment-attached / .entry-comments-panel--cards
    // shown beneath it, the .has-open-comment-editor class on the entry
    // hides that block (CSS-only) while the editor is open.
    //
    // Singleton: only one comment editor open at a time. Opening on a
    // new post closes the prior one (draft preserved via autosave).
    //
    // Portability — buildCommentEditorCard is a pure factory: every
    // I/O path is supplied as a callback. owner-extras passes SPA-
    // flavored callbacks here (sign+beseech + /api/comments/drafts);
    // Phase 3 widget integration will pass widget-flavored callbacks
    // (POST /api/widget/comment + bearer token + skip drafts). The
    // DOM + CSS + Milkdown wiring is identical across surfaces.

    // ====================================================================
    // DM COMPOSER MODAL (chunk C — v3-DM port)
    // ====================================================================
    //
    // Two modes, branched in openDMComposer:
    //   - 'new':   recipient picker (typeahead + visible mutuals list) +
    //              first-message editor. Selecting an EXISTING-conversation
    //              mutual jumps to that conversation's thread view
    //              immediately (composer dismisses, no message sent).
    //              Selecting a NEW mutual collapses the picker to a
    //              selected-recipient row + opens the message editor.
    //   - 'reply': message editor only, targeted at the current
    //              conversation's peer. Send → editor closes; user
    //              re-opens via "yours, truly" CTA to write again.
    //
    // Singleton: only one composer open at a time. Mounted in place of
    // the ghost-compose entry at the top of the stream. Keyboard nav:
    // arrow keys move through the picker list, Enter selects, Esc
    // dismisses.
    //
    // Design source: docs/design/v4/mockups/07-messages.html.

    var DM_COMPOSER_SELECTOR = '.entry--composer[data-polis-pinned="dm-composer"]';
    var DM_COMPOSER_BODY_ID = 'dm-composer-body';
    var DM_COMPOSER_INPUT_ID = 'dm-composer-recipient-input';
    var DM_COMPOSER_LIST_ID = 'dm-composer-mutuals-list';

    var dmComposerState = {
        card: null,
        mode: '',                // 'new' or 'reply'
        replyPeer: '',            // 'reply' mode: peer handle
        selectedRecipient: null, // 'new' mode: { domain, url, name }
        mutuals: [],              // 'new' mode: cached mutuals list
        focusIdx: -1,             // 'new' mode: keyboard-focused row
        submitInFlight: false,
    };

    function isDMComposerOpen() {
        return !!dmComposerState.card;
    }

    // Public entry point. Singleton-enforces, builds the composer card
    // for the requested mode, mounts it at the top of the stream in
    // place of the ghost-compose entry.
    function openDMComposer(mode, opts) {
        opts = opts || {};
        if (isDMComposerOpen()) {
            closeDMComposer();
        }
        // Close any other open editor (post / comment) so we don't
        // stack write surfaces.
        if (typeof isEditorOpen === 'function' && isEditorOpen()) {
            closeEditor();
        }
        if (typeof isCommentEditorOpen === 'function' && isCommentEditorOpen()) {
            closeCommentEditor();
        }
        // Exit any active read-focus — write modes always supersede.
        if (window.PolisStream && typeof window.PolisStream.exitFocusMode === 'function') {
            try { window.PolisStream.exitFocusMode(); } catch (e) { /* ignore */ }
        }

        dmComposerState.mode = mode;
        dmComposerState.replyPeer = opts.replyPeer || '';
        dmComposerState.selectedRecipient = null;
        dmComposerState.focusIdx = -1;
        dmComposerState.submitInFlight = false;

        var card = buildDMComposerCard(mode, opts);
        dmComposerState.card = card;

        // Hide the ghost-compose entry so the composer visually takes
        // its slot (parity with the post editor's has-open-editor CSS
        // gate). Ghost lives as a SIBLING of .stream in .layout-right
        // — the older `.stream > .entry--ghost-compose` selector here
        // never matched, leaving "yours, truly" stranded above the
        // composer card.
        var ghost = document.querySelector('.layout-right > .entry--ghost-compose') ||
                    document.querySelector('.entry--ghost-compose');
        if (ghost) ghost.classList.add('hidden');

        // Mount the composer as the first child of .stream so it sits
        // exactly where the ghost was.
        var streamRoot = document.querySelector('.layout-right .stream') ||
                         document.querySelector('.stream');
        if (streamRoot) streamRoot.insertBefore(card, streamRoot.firstChild);

        document.body.classList.add('has-open-dm-composer');

        // For 'new' mode, fetch mutuals list. For 'reply' mode, focus
        // the message body immediately.
        if (mode === 'new') {
            loadDMComposerMutuals();
            var input = document.getElementById(DM_COMPOSER_INPUT_ID);
            if (input) input.focus();
        } else {
            var body = document.getElementById(DM_COMPOSER_BODY_ID);
            if (body) body.focus();
        }
    }

    function closeDMComposer() {
        if (!dmComposerState.card) return;
        var card = dmComposerState.card;
        if (card.parentNode) card.parentNode.removeChild(card);
        // Unhide the ghost-compose entry (ghost is a sibling of .stream,
        // not a child — see openDMComposer for the selector rationale).
        var ghost = document.querySelector('.layout-right > .entry--ghost-compose') ||
                    document.querySelector('.entry--ghost-compose');
        if (ghost) ghost.classList.remove('hidden');
        document.body.classList.remove('has-open-dm-composer');
        dmComposerState.card = null;
        dmComposerState.mode = '';
        dmComposerState.replyPeer = '';
        dmComposerState.selectedRecipient = null;
        dmComposerState.mutuals = [];
        dmComposerState.focusIdx = -1;
        dmComposerState.submitInFlight = false;
    }

    // Build the composer card DOM. Returns an HTMLElement ready to
    // mount. Encapsulates the recipient-picker / message-editor /
    // toolbar wiring. No I/O — caller is responsible for fetching
    // mutuals (loadDMComposerMutuals) and posting messages (sendDM).
    function buildDMComposerCard(mode, opts) {
        var card = document.createElement('article');
        card.className = 'entry entry--composer';
        card.setAttribute('data-polis-pinned', 'dm-composer');

        var header = document.createElement('div');
        header.className = 'composer-header';

        // Dot lives inside the header so its absolute positioning
        // resolves against the header box (which is short and matches
        // the meta-line role of a regular .entry). top:50% then lands
        // the dot's center on the header text's center.
        var dot = document.createElement('span');
        dot.className = 'timeline-dot';
        header.appendChild(dot);

        var headerLabel = document.createElement('span');
        if (mode === 'new') {
            headerLabel.textContent = 'new message';
        } else {
            headerLabel.className = 'composer-context';
            headerLabel.innerHTML = 'replying to ';
            var peerSpan = document.createElement('span');
            peerSpan.className = 'composer-context-peer';
            peerSpan.textContent = opts.replyPeer || '';
            headerLabel.appendChild(peerSpan);
        }
        var closeBtn = document.createElement('span');
        closeBtn.className = 'composer-close';
        closeBtn.setAttribute('title', 'cancel');
        closeBtn.setAttribute('role', 'button');
        closeBtn.setAttribute('aria-label', 'Close composer');
        closeBtn.textContent = '×';
        closeBtn.addEventListener('click', function () { closeDMComposer(); });
        header.appendChild(headerLabel);
        header.appendChild(closeBtn);
        card.appendChild(header);

        if (mode === 'new') {
            // Recipient input + list. Both are visible until a recipient
            // is selected; then they collapse and the message body appears.
            var input = document.createElement('input');
            input.type = 'text';
            input.className = 'recipient-input';
            input.id = DM_COMPOSER_INPUT_ID;
            input.placeholder = 'to: search by name or handle…';
            input.addEventListener('input', function () {
                dmComposerState.focusIdx = -1;
                renderMutualsList();
            });
            input.addEventListener('keydown', handleRecipientKeydown);
            card.appendChild(input);

            var list = document.createElement('div');
            list.className = 'mutuals-list';
            list.id = DM_COMPOSER_LIST_ID;
            card.appendChild(list);

            // Selected-recipient row (hidden until a new mutual is picked).
            var selected = document.createElement('div');
            selected.className = 'selected-recipient';
            card.appendChild(selected);
        }

        // Message body + toolbar (shared across modes; hidden in 'new'
        // mode until a recipient is selected via CSS .has-selected).
        var body = document.createElement('div');
        body.className = 'composer-body';
        var textarea = document.createElement('textarea');
        textarea.id = DM_COMPOSER_BODY_ID;
        textarea.placeholder = 'say something…';
        textarea.addEventListener('keydown', function (ev) {
            if (ev.key === 'Escape') {
                ev.preventDefault();
                closeDMComposer();
            }
            // Ctrl/Cmd-Enter sends.
            if ((ev.ctrlKey || ev.metaKey) && ev.key === 'Enter') {
                ev.preventDefault();
                handleSendClick();
            }
        });
        body.appendChild(textarea);

        var toolbar = document.createElement('div');
        toolbar.className = 'composer-toolbar';
        var cancelBtn = document.createElement('button');
        cancelBtn.textContent = 'cancel';
        cancelBtn.addEventListener('click', function () { closeDMComposer(); });
        var sendBtn = document.createElement('button');
        sendBtn.className = 'primary';
        sendBtn.textContent = 'send';
        sendBtn.addEventListener('click', handleSendClick);
        toolbar.appendChild(cancelBtn);
        toolbar.appendChild(sendBtn);
        body.appendChild(toolbar);
        card.appendChild(body);

        // 'reply' mode: body always visible. 'new' mode: body hidden
        // until a recipient is selected (CSS class flip).
        if (mode === 'reply') {
            card.classList.add('is-reply');
        }

        return card;
    }

    // Fetch the mutuals list for the recipient picker. Uses the same
    // /api/dm/recipients endpoint the v3 composer used. Filters to
    // status=='open' (mutual-follow + DM-policy permits) per the
    // spec ("DMs require mutual follow"); other statuses still
    // surface but with a disabled-row treatment so the user sees
    // why they can't write to that author.
    function loadDMComposerMutuals() {
        dmComposerState.mutuals = [];
        renderMutualsList();
        fetch('/api/dm/recipients', { credentials: 'same-origin' })
            .then(function (r) { return r.ok ? r.json() : { recipients: [] }; })
            .then(function (data) {
                var recipients = (data && data.recipients) || [];
                // Also pull conversations so we can flag which mutuals
                // already have an active thread (picker doubles as
                // fast-switcher per the spec).
                return fetch('/api/dm/conversations', { credentials: 'same-origin' })
                    .then(function (r) { return r.ok ? r.json() : { conversations: [] }; })
                    .then(function (cd) {
                        var convDomains = {};
                        ((cd && cd.conversations) || []).forEach(function (c) {
                            if (c && c.peer_domain) convDomains[c.peer_domain] = true;
                        });
                        dmComposerState.mutuals = recipients.map(function (r) {
                            return {
                                domain: r.domain,
                                url: r.url,
                                name: r.author_name || r.domain.split('.')[0],
                                status: r.status,
                                followsUs: r.follows_us,
                                hasConv: !!convDomains[r.domain],
                            };
                        });
                        renderMutualsList();
                    });
            })
            .catch(function (e) {
                console.warn('[owner-extras] load DM recipients failed:', e);
            });
    }

    function renderMutualsList() {
        var list = document.getElementById(DM_COMPOSER_LIST_ID);
        if (!list) return;
        var input = document.getElementById(DM_COMPOSER_INPUT_ID);
        var query = input ? input.value.trim().toLowerCase() : '';
        var filtered = dmComposerState.mutuals.filter(function (m) {
            if (!query) return true;
            return m.domain.toLowerCase().indexOf(query) !== -1 ||
                   m.name.toLowerCase().indexOf(query) !== -1;
        });
        list.innerHTML = '';
        if (filtered.length === 0) {
            var empty = document.createElement('div');
            empty.className = 'mutuals-empty';
            empty.textContent = query ? 'No matching mutuals.' : 'Follow + be followed back to start a conversation.';
            list.appendChild(empty);
            return;
        }
        filtered.forEach(function (m, idx) {
            var row = document.createElement('div');
            row.className = 'mutual-row';
            if (m.status !== 'open') row.classList.add('is-disabled');
            if (idx === dmComposerState.focusIdx) row.classList.add('is-focused');

            var av = document.createElement('span');
            av.className = 'av';
            av.textContent = (m.name || m.domain || '?')[0].toUpperCase();
            row.appendChild(av);

            var nameBlock = document.createElement('div');
            nameBlock.className = 'name-block';
            var nameEl = document.createElement('div');
            nameEl.className = 'name';
            nameEl.textContent = m.name;
            nameBlock.appendChild(nameEl);
            var handleEl = document.createElement('div');
            handleEl.className = 'handle-line';
            handleEl.textContent = m.domain;
            nameBlock.appendChild(handleEl);
            row.appendChild(nameBlock);

            if (m.hasConv) {
                var tag = document.createElement('span');
                tag.className = 'has-conv';
                tag.textContent = 'in conversation';
                row.appendChild(tag);
            }

            if (m.status === 'open') {
                row.addEventListener('click', function () { selectRecipient(m); });
            }
            list.appendChild(row);
        });
    }

    function handleRecipientKeydown(ev) {
        if (ev.key === 'Escape') {
            ev.preventDefault();
            closeDMComposer();
            return;
        }
        var rows = document.querySelectorAll('#' + DM_COMPOSER_LIST_ID + ' .mutual-row');
        var max = rows.length - 1;
        if (max < 0) return;
        if (ev.key === 'ArrowDown') {
            ev.preventDefault();
            dmComposerState.focusIdx = Math.min(max, dmComposerState.focusIdx + 1);
            renderMutualsList();
        } else if (ev.key === 'ArrowUp') {
            ev.preventDefault();
            dmComposerState.focusIdx = Math.max(0, dmComposerState.focusIdx - 1);
            renderMutualsList();
        } else if (ev.key === 'Enter') {
            ev.preventDefault();
            var idx = dmComposerState.focusIdx >= 0 ? dmComposerState.focusIdx : 0;
            // Re-filter to match what's rendered (so Enter selects the
            // FOCUSED visible row, not a stale index from the full list).
            var input = document.getElementById(DM_COMPOSER_INPUT_ID);
            var query = input ? input.value.trim().toLowerCase() : '';
            var visible = dmComposerState.mutuals.filter(function (m) {
                if (!query) return true;
                return m.domain.toLowerCase().indexOf(query) !== -1 ||
                       m.name.toLowerCase().indexOf(query) !== -1;
            });
            var pick = visible[idx];
            if (pick && pick.status === 'open') selectRecipient(pick);
        }
    }

    function selectRecipient(m) {
        // If they already have a conversation with this mutual, jump
        // straight to its thread view (composer dismisses, no message
        // sent). Per the spec: picker doubles as fast-switcher.
        if (m.hasConv) {
            closeDMComposer();
            if (window.PolisStream && typeof window.PolisStream.setFilterScope === 'function') {
                window.PolisStream.setFilterScope('@' + m.domain, { label: m.domain });
            }
            return;
        }
        // New conversation: collapse picker, render selected-recipient
        // row, focus the message body.
        dmComposerState.selectedRecipient = m;
        var card = dmComposerState.card;
        if (!card) return;
        card.classList.add('has-selected');
        var selectedSlot = card.querySelector('.selected-recipient');
        if (selectedSlot) {
            selectedSlot.innerHTML = '';
            var av = document.createElement('span');
            av.className = 'av';
            av.textContent = (m.name || m.domain || '?')[0].toUpperCase();
            selectedSlot.appendChild(av);

            var info = document.createElement('div');
            var label = document.createElement('div');
            label.className = 'to-label';
            label.textContent = 'to';
            info.appendChild(label);
            var line = document.createElement('div');
            line.className = 'to-line';
            line.innerHTML = '<span class="to-name"></span> · <span class="to-handle"></span>';
            line.querySelector('.to-name').textContent = m.name;
            line.querySelector('.to-handle').textContent = m.domain;
            info.appendChild(line);
            selectedSlot.appendChild(info);

            var changeLink = document.createElement('span');
            changeLink.className = 'change';
            changeLink.textContent = 'change';
            changeLink.addEventListener('click', function () {
                dmComposerState.selectedRecipient = null;
                card.classList.remove('has-selected');
                var input = document.getElementById(DM_COMPOSER_INPUT_ID);
                if (input) input.focus();
            });
            selectedSlot.appendChild(changeLink);
        }
        var body = document.getElementById(DM_COMPOSER_BODY_ID);
        if (body) body.focus();
    }

    function handleSendClick() {
        if (dmComposerState.submitInFlight) return;
        var textarea = document.getElementById(DM_COMPOSER_BODY_ID);
        var content = textarea ? textarea.value.trim() : '';
        if (!content) {
            if (textarea) textarea.focus();
            return;
        }
        var mode = dmComposerState.mode;
        var recipientURL = '';
        var peerDomain = '';
        if (mode === 'reply') {
            peerDomain = dmComposerState.replyPeer;
            recipientURL = 'https://' + peerDomain;
        } else if (mode === 'new') {
            var sel = dmComposerState.selectedRecipient;
            if (!sel) {
                if (window.App && typeof window.App.showToast === 'function') {
                    window.App.showToast('Pick a recipient first', 'error');
                }
                return;
            }
            peerDomain = sel.domain;
            recipientURL = sel.url || ('https://' + sel.domain);
        }
        if (!recipientURL) return;

        dmComposerState.submitInFlight = true;
        fetch('/api/dm/send', {
            method: 'POST',
            headers: { 'Content-Type': 'application/json' },
            credentials: 'same-origin',
            body: JSON.stringify({ recipient_url: recipientURL, content: content }),
        }).then(function (r) {
            if (!r.ok && r.status !== 202) {
                return r.text().then(function (t) { throw new Error(t || ('HTTP ' + r.status)); });
            }
            return r.json();
        }).then(function () {
            // Per spec:
            //   new mode → jump to that conversation's thread view, editor closes.
            //   reply mode → editor closes; user re-opens via CTA to write again.
            closeDMComposer();
            if (window.PolisStream && typeof window.PolisStream.setFilterScope === 'function') {
                if (mode === 'new') {
                    window.PolisStream.setFilterScope('@' + peerDomain, { label: peerDomain });
                } else {
                    // reply: refresh the current thread so the new
                    // message appears at the top.
                    if (typeof window.PolisStream.refresh === 'function') {
                        window.PolisStream.refresh();
                    }
                }
            }
            // Bust the DM scope dropdown cache so the next dropdown
            // open shows the freshly-active conversation.
            dmScopeCache.at = 0;
        }).catch(function (e) {
            console.warn('[owner-extras] DM send failed:', e);
            if (window.App && typeof window.App.showToast === 'function') {
                window.App.showToast('Failed to send: ' + e.message, 'error');
            }
        }).then(function () {
            dmComposerState.submitInFlight = false;
        });
    }

    // ====================================================================
    // COMMENT EDITOR CARD — original v3-comment-replacement section below
    // ====================================================================

    var COMMENT_EDITOR_BODY_ID = 'stream-comment-editor-body';
    var COMMENT_EDITOR_MILKDOWN_ID = 'milkdown-stream-comment-editor';
    var COMMENT_EDITOR_STATUS_ID = 'stream-comment-editor-status';
    var COMMENT_EDITOR_SELECTOR = '.comment-editor[data-polis-pinned="comment-editor"]';

    var commentEditorState = {
        entry: null,            // the .entry article hosting the open editor
        targetPostUrl: '',
        draftId: '',
        autoSaveTimer: null,
        submitInFlight: false,
    };

    function isCommentEditorOpen() {
        return !!commentEditorState.entry;
    }

    function setCommentEditorStatus(text, savedFlavor) {
        var el = document.getElementById(COMMENT_EDITOR_STATUS_ID);
        if (!el) return;
        el.textContent = text || '';
        if (savedFlavor) {
            el.classList.add('is-saved');
        } else {
            el.classList.remove('is-saved');
        }
    }

    // buildCommentEditorCard — pure factory. Returns an HTMLElement
    // ready to mount inside a post entry. All I/O goes through the
    // supplied callbacks (see Phase 3 portability constraints above).
    //
    // opts:
    //   targetPostUrl      — required, the URL of the post being replied to
    //   targetPostDomain   — optional, display string for "replying to X"
    //   onSubmit({content}) → Promise   — fires when Comment is clicked
    //   onSaveDraft({content}) → Promise — fires on autosave + Save button
    //   onLoadDraft() → Promise<{content}> — fires once on mount
    //   onCancel()                       — fires when Cancel / Esc closes
    //   hideDraftButton    — optional, omits "Save as draft" (widget mode)
    function buildCommentEditorCard(opts) {
        var targetPostUrl = opts.targetPostUrl || '';
        var targetPostDomain = opts.targetPostDomain || '';
        var onSubmit = opts.onSubmit || function () { return Promise.resolve(); };
        var onSaveDraft = opts.onSaveDraft || function () { return Promise.resolve(); };
        var onLoadDraft = opts.onLoadDraft || function () { return Promise.resolve({ content: '' }); };
        var onCancel = opts.onCancel || function () {};

        var card = document.createElement('div');
        card.className = 'comment-editor';
        card.setAttribute('data-polis-pinned', 'comment-editor');
        card.setAttribute('data-polis-target-url', targetPostUrl);

        // Timeline-dot--small — matches the .comment-attached marker so
        // the editor reads as a child of the post (same gutter dot lands
        // on the timeline bar).
        var dot = document.createElement('div');
        dot.className = 'timeline-dot timeline-dot--small';
        dot.setAttribute('aria-hidden', 'true');
        card.appendChild(dot);

        // Meta line: speech-bubble icon + "you" + "replying to <domain>"
        var meta = document.createElement('div');
        meta.className = 'comment-meta-line';
        var svgNS = 'http://www.w3.org/2000/svg';
        var icon = document.createElementNS(svgNS, 'svg');
        icon.setAttribute('class', 'comment-meta-icon');
        icon.setAttribute('viewBox', '0 0 24 24');
        icon.setAttribute('fill', 'none');
        icon.setAttribute('stroke', 'currentColor');
        icon.setAttribute('stroke-width', '2');
        icon.setAttribute('aria-hidden', 'true');
        var iconPath = document.createElementNS(svgNS, 'path');
        iconPath.setAttribute('d', 'M21 15a2 2 0 0 1-2 2H7l-4 4V5a2 2 0 0 1 2-2h14a2 2 0 0 1 2 2Z');
        icon.appendChild(iconPath);
        meta.appendChild(icon);
        var youSpan = document.createElement('span');
        youSpan.className = 'comment-author';
        youSpan.textContent = 'you';
        meta.appendChild(youSpan);
        if (targetPostDomain) {
            var targetSpan = document.createElement('span');
            targetSpan.className = 'comment-editor-target';
            targetSpan.textContent = 'replying to ';
            var targetLink = document.createElement('a');
            targetLink.href = targetPostUrl;
            targetLink.textContent = targetPostDomain;
            targetSpan.appendChild(targetLink);
            meta.appendChild(targetSpan);
        }
        card.appendChild(meta);

        // Body — same textarea-fallback-with-milkdown-overlay pattern as
        // the post editor. Textarea is the always-on baseline; Milkdown
        // mount starts hidden and is revealed only on successful hydration.
        var bodyWrap = document.createElement('div');
        bodyWrap.className = 'comment-editor-body';
        var milkdownMount = document.createElement('div');
        milkdownMount.className = 'milkdown-mount hidden';
        milkdownMount.id = COMMENT_EDITOR_MILKDOWN_ID;
        bodyWrap.appendChild(milkdownMount);
        var bodyArea = document.createElement('textarea');
        bodyArea.id = COMMENT_EDITOR_BODY_ID;
        bodyArea.placeholder = 'Share your thoughts on this post…';
        bodyArea.addEventListener('input', function () {
            scheduleCommentAutosave(onSaveDraft);
        });
        bodyWrap.appendChild(bodyArea);
        card.appendChild(bodyWrap);

        // Footer — status pill left, action buttons right. No <kbd> hint
        // since there's no Ctrl+Shift+F focus mode for comments.
        var footer = document.createElement('div');
        footer.className = 'comment-editor-footer';
        var status = document.createElement('span');
        status.className = 'comment-editor-status';
        status.id = COMMENT_EDITOR_STATUS_ID;
        // No initial copy — empty editor with visible body + Comment
        // button is self-evidently a new comment. Status fills with
        // transient state on save/send (Saving… / Draft saved / etc.).
        status.textContent = '';
        var spacer = document.createElement('span');
        spacer.className = 'spacer';
        var cancelBtn = document.createElement('button');
        cancelBtn.type = 'button';
        cancelBtn.className = 'comment-editor-cancel-btn';
        cancelBtn.textContent = 'Cancel';
        cancelBtn.addEventListener('click', function () { onCancel(); });
        footer.appendChild(status);
        footer.appendChild(spacer);
        footer.appendChild(cancelBtn);
        if (!opts.hideDraftButton) {
            var saveBtn = document.createElement('button');
            saveBtn.type = 'button';
            saveBtn.textContent = 'Save as draft';
            saveBtn.addEventListener('click', function () {
                var content = readCommentEditorContent();
                if (!content.trim()) return;
                setCommentEditorStatus('Saving…');
                Promise.resolve(onSaveDraft({ content: content }))
                    .then(function () { setCommentEditorStatus('Draft saved · just now', true); })
                    .catch(function () { setCommentEditorStatus('Save failed'); });
            });
            footer.appendChild(saveBtn);
        }
        var submitBtn = document.createElement('button');
        submitBtn.type = 'button';
        submitBtn.className = 'primary';
        submitBtn.textContent = 'Comment';
        submitBtn.addEventListener('click', function () {
            if (commentEditorState.submitInFlight) return;
            var content = readCommentEditorContent();
            if (!content.trim()) {
                setCommentEditorStatus('Write something first');
                return;
            }
            commentEditorState.submitInFlight = true;
            submitBtn.disabled = true;
            submitBtn.textContent = 'Sending…';
            setCommentEditorStatus('Signing & sending…');
            Promise.resolve(onSubmit({ content: content }))
                .then(function () {
                    commentEditorState.submitInFlight = false;
                    // onSubmit caller is responsible for closing the
                    // editor (so it can also kick off PolisStream.refresh
                    // at the right moment).
                })
                .catch(function (err) {
                    commentEditorState.submitInFlight = false;
                    submitBtn.disabled = false;
                    submitBtn.textContent = 'Comment';
                    var msg = (err && err.message) ? err.message : 'Send failed';
                    setCommentEditorStatus(msg);
                });
        });
        footer.appendChild(submitBtn);
        card.appendChild(footer);

        // Esc-to-close, scoped to the card (not document) so it doesn't
        // intercept Esc anywhere else on the page.
        card.addEventListener('keydown', function (ev) {
            if (ev.key === 'Escape') {
                ev.preventDefault();
                onCancel();
            }
        });

        // Stop click bubbling so clicks inside the editor don't reach the
        // parent .entry's bindCardClick — without this, typing or clicking
        // inside the body would trigger focus mode on the host post (the
        // entry is often focus-mode-eligible because body_html is shipped).
        // Button handlers already ran in the bubble phase before this fires.
        card.addEventListener('click', function (ev) {
            ev.stopPropagation();
        });

        // Preload an existing draft, if any. Fire-and-forget; the textarea
        // is already in the DOM so onLoadDraft can set its .value directly
        // before Milkdown hydrates (Milkdown picks up the textarea's value
        // as its initial seed).
        Promise.resolve(onLoadDraft()).then(function (loaded) {
            if (loaded && loaded.content) {
                bodyArea.value = loaded.content;
                setCommentEditorStatus('Draft loaded', true);
                // If Milkdown already hydrated by the time the draft
                // landed, push the content into the bridge too.
                if (window.App && typeof window.App._milkdownIdFor === 'function' && window.MilkdownBridge) {
                    var editorId = window.App._milkdownIdFor(COMMENT_EDITOR_BODY_ID);
                    if (editorId && window.MilkdownBridge.isReady && window.MilkdownBridge.isReady(editorId)) {
                        try { window.MilkdownBridge.setMarkdown(editorId, loaded.content); } catch (e) { /* ignore */ }
                    }
                }
            }
            if (loaded && loaded.draftId) {
                commentEditorState.draftId = loaded.draftId;
            }
        }).catch(function () { /* ignore — start fresh */ });

        return card;
    }

    function readCommentEditorContent() {
        if (window.App && typeof window.App.getEditorContent === 'function') {
            return window.App.getEditorContent(COMMENT_EDITOR_BODY_ID) || '';
        }
        var el = document.getElementById(COMMENT_EDITOR_BODY_ID);
        return el ? el.value : '';
    }

    function scheduleCommentAutosave(onSaveDraft) {
        if (commentEditorState.autoSaveTimer) clearTimeout(commentEditorState.autoSaveTimer);
        commentEditorState.autoSaveTimer = setTimeout(function () {
            commentEditorState.autoSaveTimer = null;
            var content = readCommentEditorContent();
            if (!content.trim()) return;
            setCommentEditorStatus('Saving…');
            Promise.resolve(onSaveDraft({ content: content }))
                .then(function () { setCommentEditorStatus('Draft saved · just now', true); })
                .catch(function () { setCommentEditorStatus('Save failed'); });
        }, 1500);
    }

    // mountCommentEditor — SPA wiring. Singleton-enforces, builds the
    // factory card with SPA-flavored callbacks, mounts it as a child of
    // the target entry, flips .has-open-comment-editor on the entry so
    // any existing comment-attached block is hidden via CSS.
    function mountCommentEditor(entry, targetPostUrl, targetPostDomain) {
        if (!entry || !targetPostUrl) return;
        // Toggle-close if the editor is already open on this same entry —
        // matches the post editor's rail-+ behavior (re-click closes).
        if (isCommentEditorOpen() && commentEditorState.entry === entry) {
            closeCommentEditor();
            return;
        }
        // Singleton: close any other open editor first. Draft is
        // preserved by the in-flight autosave (which fires on input);
        // explicitly trigger one final save before tearing down so any
        // unsaved keystrokes flush.
        if (isCommentEditorOpen()) {
            closeCommentEditor();
        }
        // Exit any active read-focus — same pattern as openEditor.
        // Without this, clicking the timeline-dot CTA on a post the
        // user had previously click-focused mounts the comment editor
        // on top of the still-active read-focus dim+blur.
        if (window.PolisStream && typeof window.PolisStream.exitFocusMode === 'function') {
            try { window.PolisStream.exitFocusMode(); } catch (e) { /* ignore */ }
        }

        commentEditorState.entry = entry;
        commentEditorState.targetPostUrl = targetPostUrl;
        commentEditorState.draftId = '';
        commentEditorState.submitInFlight = false;
        entry.classList.add('has-open-comment-editor');

        var card = buildCommentEditorCard({
            targetPostUrl: targetPostUrl,
            targetPostDomain: targetPostDomain || '',
            onLoadDraft: function () {
                return fetch('/api/comments/drafts', { credentials: 'same-origin' })
                    .then(function (r) { return r.ok ? r.json() : { drafts: [] }; })
                    .then(function (data) {
                        var drafts = (data && data.drafts) || [];
                        var match = null;
                        for (var i = 0; i < drafts.length; i++) {
                            if (drafts[i].in_reply_to === targetPostUrl) { match = drafts[i]; break; }
                        }
                        if (!match) return { content: '' };
                        return { content: match.content || '', draftId: match.id };
                    });
            },
            onSaveDraft: function (payload) {
                return fetch('/api/comments/drafts', {
                    method: 'POST',
                    headers: { 'Content-Type': 'application/json' },
                    credentials: 'same-origin',
                    body: JSON.stringify({
                        id: commentEditorState.draftId || '',
                        in_reply_to: targetPostUrl,
                        content: payload.content,
                    }),
                }).then(function (r) {
                    if (!r.ok) throw new Error('HTTP ' + r.status);
                    return r.json();
                }).then(function (result) {
                    if (result && result.id) commentEditorState.draftId = result.id;
                });
            },
            onSubmit: function (payload) {
                // Two-step pipeline: sign → beseech. The sign step is the
                // authoritative "comment captured locally" boundary —
                // once it returns 200 the comment is signed + saved to
                // .polis/.../comments/pending/ on disk. The beseech step
                // is the DS-registration leg; it can succeed, return a
                // 'deferred' status (site not registered), or fail
                // (network / server error). In all three beseech
                // outcomes we close the editor + refresh the stream
                // because the comment IS captured locally; only the
                // toast varies. Matches v3's pattern at app.js:5763.
                return fetch('/api/comments/sign', {
                    method: 'POST',
                    headers: { 'Content-Type': 'application/json' },
                    credentials: 'same-origin',
                    body: JSON.stringify({
                        draft_id: commentEditorState.draftId || '',
                        in_reply_to: targetPostUrl,
                        content: payload.content,
                    }),
                }).then(function (r) {
                    if (!r.ok) throw new Error('HTTP ' + r.status);
                    return r.json();
                }).then(function (signResult) {
                    if (!signResult || !signResult.success) {
                        throw new Error((signResult && signResult.error) || 'Sign failed');
                    }
                    var commentId = (signResult.comment && signResult.comment.id) || signResult.id;
                    var toastFn = (window.App && typeof window.App.showToast === 'function')
                        ? window.App.showToast.bind(window.App)
                        : function () {};
                    var finishUp = function () {
                        closeCommentEditor();
                        // Re-fetch the stream so the new comment lands in
                        // the attached-comment slot on this post — per Q2:
                        // "editor closes, new comment fills the slot."
                        if (window.PolisStream && typeof window.PolisStream.refresh === 'function') {
                            window.PolisStream.refresh();
                        }
                    };
                    return fetch('/api/comments/beseech', {
                        method: 'POST',
                        headers: { 'Content-Type': 'application/json' },
                        credentials: 'same-origin',
                        body: JSON.stringify({ comment_id: commentId }),
                    }).then(function (r) {
                        if (!r.ok) {
                            // Beseech itself errored (5xx / 4xx). Surface
                            // as a warning toast — the comment is still
                            // signed and on disk; the user can retry via
                            // the pending-comments view later.
                            return r.text().then(function (text) {
                                toastFn('Comment saved locally. Could not send for blessing: ' + (text || 'HTTP ' + r.status), 'warning', 6000);
                                finishUp();
                            });
                        }
                        return r.json().then(function (beseechResult) {
                            var status = beseechResult && beseechResult.status;
                            if (status === 'deferred') {
                                // Server detected the site isn't registered
                                // with the DS. Comment is captured locally;
                                // DS contact is deferred until registration.
                                toastFn(beseechResult.message || 'Comment saved locally — register your site to send for blessing.', 'info', 6000);
                            } else if (status === 'blessed') {
                                toastFn('Comment auto-blessed', 'success');
                            } else {
                                toastFn('Comment sent for blessing', 'success');
                            }
                            finishUp();
                        });
                    }).catch(function (beseechErr) {
                        // Network failure (DNS, offline, etc.). Comment
                        // is still signed locally.
                        toastFn('Comment saved locally. Could not send for blessing: ' + ((beseechErr && beseechErr.message) || 'network error'), 'warning', 6000);
                        finishUp();
                    });
                });
            },
            onCancel: function () { closeCommentEditor(); },
        });

        // Mount as the LAST child of the entry article. Per the plan, the
        // editor occupies the slot that .comment-attached / .entry-comments-
        // panel--cards would live in. Those siblings are hidden via
        // .has-open-comment-editor CSS rule — we don't reorder the DOM.
        entry.appendChild(card);

        // Kick off Milkdown over the textarea. Same lazy-import pattern
        // as the post editor — App._initMilkdown handles the bridge load
        // internally; on failure the textarea stays as the working baseline.
        if (window.App && typeof window.App._initMilkdown === 'function') {
            try {
                var p = window.App._initMilkdown(COMMENT_EDITOR_BODY_ID);
                Promise.resolve(p).then(function () {
                    if (!window.MilkdownBridge || !window.MilkdownBridge.isReady) return;
                    if (!window.MilkdownBridge.isReady(COMMENT_EDITOR_MILKDOWN_ID)) return;
                    var mount = document.getElementById(COMMENT_EDITOR_MILKDOWN_ID);
                    var ta = document.getElementById(COMMENT_EDITOR_BODY_ID);
                    if (!mount || !ta) return;
                    mount.classList.remove('hidden');
                    ta.classList.add('hidden');
                    // Transfer focus from the (now-hidden) textarea to
                    // ProseMirror's contenteditable. Without this, the
                    // user has to click into the body to start typing.
                    // See the matching post-editor handler above.
                    var pmEditable = mount.querySelector('.ProseMirror');
                    if (pmEditable && typeof pmEditable.focus === 'function') {
                        pmEditable.focus();
                    }
                }).catch(function () { /* keep textarea fallback */ });
            } catch (e) { /* keep textarea fallback */ }
        }

        // Focus the body so the user starts typing immediately.
        var bodyArea = document.getElementById(COMMENT_EDITOR_BODY_ID);
        if (bodyArea) bodyArea.focus();
    }

    function closeCommentEditor() {
        // Tear down Milkdown so its event listeners + ProseMirror state
        // don't leak when the next editor opens.
        if (window.App && typeof window.App._destroyMilkdown === 'function') {
            try { window.App._destroyMilkdown(COMMENT_EDITOR_BODY_ID); } catch (e) { /* ignore */ }
        }
        if (commentEditorState.autoSaveTimer) {
            clearTimeout(commentEditorState.autoSaveTimer);
            commentEditorState.autoSaveTimer = null;
        }
        if (commentEditorState.entry) {
            commentEditorState.entry.classList.remove('has-open-comment-editor');
            var card = commentEditorState.entry.querySelector(COMMENT_EDITOR_SELECTOR);
            if (card && card.parentNode) card.parentNode.removeChild(card);
        }
        commentEditorState.entry = null;
        commentEditorState.targetPostUrl = '';
        commentEditorState.draftId = '';
        commentEditorState.submitInFlight = false;
    }

    // wireCommentAddCTA — shared helper called from both decoratePost
    // and decorateComment. Both decorators historically navigated to
    // /comments/new?reply_to=<url>; this helper centralizes the
    // mountCommentEditor swap so wiring stays in sync across entry
    // types.
    function wireCommentAddCTA(entry, targetPostUrl, targetPostDomain) {
        if (!entry || !targetPostUrl) return;
        // Dot lives inside the entry's meta container (.entry-meta-line
        // for posts, .entry-meta for the legacy sub-types). Scope to
        // the meta container's direct child so we never grab the
        // nested .timeline-dot--small inside .comment-attached (that
        // dot belongs to the attached comment, not the post-level CTA).
        var dot = entry.querySelector(
            ':scope > .entry-meta-line > .timeline-dot, :scope > .entry-meta > .timeline-dot'
        );
        if (!dot) return;
        dot.setAttribute('role', 'button');
        dot.setAttribute('title', 'Reply to this post');
        dot.setAttribute('aria-label', 'Reply to this post');
        dot.addEventListener('click', function (ev) {
            ev.preventDefault();
            ev.stopPropagation();
            mountCommentEditor(entry, targetPostUrl, targetPostDomain);
        });
    }

    // Public surface. Other owner-extras entry points (6.h inline
    // editor, etc.) will append to this object as they land.
    var ready = waitForController().then(function (stream) {
        registerAvatarMenuEscape(stream);
        // Register owner-only filter-type options (activity / dms /
        // drafts / profiles) before any preset clicks fire. Public
        // bundle ships FILTER_TYPE_OPTIONS = [posts, comments]; the
        // owner SPA needs the additional types so the type-slot
        // dropdown surfaces them and the sentence filter renders
        // correctly (e.g. "new activity from my network").
        // (mentions is grammar-parsed but feature-deferred per
        // resolved-decision #9; not registered here.)
        if (typeof stream.replaceFilterOptions === 'function') {
            // 06-profiles: "follows" removed from the type slot.
            // "profiles" added as a sibling between comments and
            // messages. posts + comments remain indented under
            // activity to read as a hierarchy. dms surfaces as
            // "messages" in the UI; the value stays 'dms' so the
            // server route + code references are unchanged.
            stream.replaceFilterOptions('type', [
                { label: 'activity', value: 'activity' },
                { label: 'posts',    value: 'posts',    indent: true },
                { label: 'comments', value: 'comments', indent: true },
                { label: 'profiles', value: 'profiles' },
                { label: 'messages', value: 'dms'      },
                { label: 'drafts',   value: 'drafts'   },
            ]);
        } else if (typeof stream.registerFilterOption === 'function') {
            // Older controller without replaceFilterOptions — fall
            // back to additive registrations. The public defaults
            // [posts, comments] are already present; just append the
            // owner-only types.
            stream.registerFilterOption('type', { label: 'activity', value: 'activity', prepend: true });
            stream.registerFilterOption('type', { label: 'profiles', value: 'profiles' });
            stream.registerFilterOption('type', { label: 'drafts', value: 'drafts' });
            stream.registerFilterOption('type', { label: 'dms', value: 'dms' });
        }
        // Unlock qualifier + scope slots — public-render ships them as
        // sf-slot--locked because public visitors have no read-state and
        // the scope is fixed to the tenant's identity. The owner SPA
        // routes through the same template, so we strip the lock at
        // init and have the controller attach its standard slot
        // click-handlers.
        if (typeof stream.unlockSlot === 'function') {
            // R22 #12: qualifier stays locked to "all" — drop the
            // `new` option entirely. Read-state tracking is unreliable
            // enough across types that filtering on it produced
            // confusing results; collapse to all-only and treat the
            // sentence as deterministic.
            stream.unlockSlot('scope');
        }
        wireIconPresets(stream);
        wireMobileNav();
        registerActivityDecorators(stream);
        // step-06/6.i: hover-revealed owner-action chrome on each
        // stream entry (unpublish on own posts/comments, bless/deny
        // on incoming blessing requests, mark-read on unread DMs).
        registerStreamItemDecorators(stream);
        setupReadPersistence();
        // R22 #11: scratch-column owner card — populates avatar +
        // bio + counts and wires the inline-edit flow.
        setupOwnerCard();
        // (Editor keyboard handling is card-scoped — wired in
        // mountEditorCard via attachEditorKeyboardTo, not at init.)
        // Kick off rule fetch — non-blocking; activity-signal renders
        // gracefully degrade to a default if it hasn't completed yet.
        loadNotificationRules();
        return stream;
    }).catch(function (err) {
        // Failure mode: stream controller never loaded. Surface to console
        // for ops; the SPA's _activateStreamScreen has its own toast on
        // the same failure, so we don't double-toast here.
        console.error('[polis-owner-extras] init failed:', err);
        throw err;
    });

    window.PolisOwnerExtras = {
        ready: ready,
        loadPreset: loadPreset,
        ICON_PRESETS: ICON_PRESETS,
        syncActivityModeClass: syncActivityModeClass,
        // step-06/6.h: editor card public surface.
        openEditor: openEditor,
        closeEditor: closeEditor,
        // DM composer surface (needed so App.toggleCompose can close
        // an already-open DM composer when the nav + is clicked).
        isDMComposerOpen: isDMComposerOpen,
        closeDMComposer: closeDMComposer,
        // Read by App._applyCountsFromSSE: when a counts push raises
        // hasNew* for the surface the user is already viewing, the
        // SPA auto-advances the cursor so the dot never appears for a
        // view the user is staring at. Returns the same bucket name
        // matchIconForFilter does (gateway / comment / envelope / …).
        getCurrentSurfaceIcon: function () {
            return matchIconForFilter(currentFilterSnapshot);
        },
    };
})();
