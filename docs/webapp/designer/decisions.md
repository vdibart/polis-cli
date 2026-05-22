# Design Decisions

A log of key design decisions and the reasoning behind them. Consult this before making new decisions to ensure consistency.

---

## The nav is the connective tissue

**Decision:** A persistent icon nav bar is the single element that unifies the entire polis.pub experience across all page states.

**Why:** Without the nav, visiting someone else's site feels like leaving the app. The widget (previous approach) was bolted on. The nav makes polis feel like one place — your context travels with you. It says "you're still logged in, your stuff is one gesture away."

**Implication:** Every logged-in page must render the same nav bar. The nav's icon positions, sizing, and behavior must be identical everywhere. Only tooltips and active states change.

---

## Push-down, not overlay

**Decision:** The nav pushes content down when visible. It never overlays content.

**Why:** Overlay says "I'm a popup imposed on this page" — it's what browser extensions do. Push-down says "this is the architecture." The spatial relationship is honest: your context is always above, their content is always below. Nothing gets obscured. Alice's site renders at full fidelity in every state.

**Implication:** The nav must have a fixed, known height (50px). Content areas must handle the nav appearing/disappearing smoothly (CSS transition on height).

---

## Autohide on foreign sites

**Decision:** When visiting someone else's site, the nav autohides after 2 seconds. Hover the top edge to reveal.

**Why:** The nav is yours, but you're on someone else's space. Keeping it permanently visible would be disrespectful to their content and creates persistent visual conflict between themes. Autohide gives their site full viewport while keeping your context one gesture away.

**Implication:** Need a trigger zone, a hint pill, and smooth height transitions. The nav must not steal focus from the content below when it appears.

---

## Icon nav over text nav

**Decision:** The nav uses SVG icons instead of text labels like "Following", "Followers", "Messages".

**Why:** Text nav was too crowded and competed with the "polis" wordmark for attention. Icons are more compact, scale better across themes, and create a cleaner visual rhythm. The Feed/Home text toggle we tried got lost in the nav bar — icons with tooltips are clearer.

**Implication:** Icons must be recognizable without labels. Tooltips provide context on hover. Icon meanings must be consistent and learnable.

---

## Avatar = home, not "polis"

**Decision:** Clicking the avatar navigates to the feed (home). Clicking "polis" does not navigate — it scrolls to top.

**Why:** The avatar is the most personal element on screen. Users instinctively expect clicking it to "go home." The "polis" wordmark is branding — clicking a logo doesn't feel natural as navigation. Making the avatar the home button and "polis" pure branding simplifies the mental model.

**Implication:** Dashboard access moves to the avatar hover menu. The feed is the primary destination, not the dashboard. The dashboard is an admin view accessed through a menu, not a top-level click.

---

## Blended feed over toggle

**Decision:** The feed (polis.pub logged in) shows one blended stream. No "Your network" / "All of polis" toggle.

**Why:** The toggle created a decision where there shouldn't be one. A new user would have to choose between two feeds they don't understand. A blended stream naturally mixes personal and discovery content — your posts, posts from people you follow, and posts from the wider network interleaved chronologically. This is what social feeds actually do well. Filtering can be added later.

**Implication:** The stream algorithm (when built) needs to balance personal and discovery content intelligently. No UI for mode switching needed.

---

## SOLS as system theme, not a personal theme

**Decision:** The SOLS palette is reserved for the logged-out system experience. Users cannot select it as their personal theme.

**Why:** Three reasons:
1. If your theme matches the system theme, there's no visual shift when you log in — you lose the "you've arrived" signal.
2. On other people's sites, a SOLS-themed nav could blur with default system styling.
3. Reserving SOLS makes theme selection meaningful — every other choice is a statement away from the default.

**Implication:** The theme picker must exclude SOLS. Self-hosted users who bring their own themes are not affected by this restriction.

---

## User's theme applies to nav + their own content

**Decision:** When logged in, the user's theme applies to both the nav bar AND their content pages (feed, dashboard, my content). When visiting someone else, the nav stays in the user's theme but the content area uses the site owner's theme.

**Why:** This makes the nav feel like it belongs to you — it's not a neutral chrome, it's YOUR chrome. The visual consistency between your nav and your content pages reinforces ownership. The contrast between your nav and someone else's content makes the boundary clear.

**Implication:** Every theme must define both nav variables and page variables. The nav must look decent on top of any other theme's content (validated via theme-adaptive-nav mockup).

---

## "Show me then tell me" landing page

**Decision:** The polis.pub landing page shows live network activity at the top, then unfolds the educational narrative below on scroll.

**Why:** Activity proves the system is alive before you read a word of explanation. The stream IS the pitch. Educational content ("what is polis", "how does blessing work", "three steps") supports the proof but doesn't lead with it. Starting with a marketing hero would feel like every other SaaS landing page.

**Implication:** The activity stream must be real (or feel real). The brand lockup ("The space we share" + subtitle) sits above the stream to provide context. The educational sections below are optional reading — the top of the page must convert on its own.

---

## Dashboard behind the avatar menu

**Decision:** The dashboard is accessed via the avatar hover menu, not a dedicated nav icon or top-level click.

**Why:** The dashboard is an admin workspace (blessings, post management, network digest). It's not where users spend most of their time — the feed is. Putting it behind the avatar menu keeps the nav clean and the primary flow (avatar → feed) simple. Power users who manage blessings frequently will learn the hover gesture quickly.

**Implication:** The avatar menu must be discoverable. The hover interaction needs to be reliable (pure CSS, no click required). The menu should feel substantial enough to be worth hovering — name, handle, stats, then actions.

---

## Consistent nav height across all states

**Decision:** The icon nav bar (50px) matches the system topbar height on the landing page.

**Why:** When transitioning from logged-out to logged-in (e.g., clicking "Sign in"), the nav should not jump in height. Visual consistency in the top strip makes the transition feel smooth rather than jarring.

**Implication:** Both the system topbar and icon nav use 50px height. The autohide `visible` state also uses 50px.
