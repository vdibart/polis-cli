# Choose a theme, or write your own

*For* [Writers](../../README.md#writing-on-polis) · [Developers](../../README.md#building-on-polis) — *Kind* [Guide](../../README.md#kinds-of-page) — *See also* [concept](../concepts/themes.md) · [reference](../../cli/user/command-reference.md#themes) · [guide](../../webapp/user/user-manual.md#site-theme)

A theme decides how your published site looks. This page walks through the task: switching between the themes polis ships, then
writing a CSS-only theme of your own. What a theme *is*, the variables it sets and the list of themes that ship are in
[the themes concept](../concepts/themes.md). This page does not repeat them.

## 1 · Switch to a theme that ships

1. **See what you can choose.** The shipped themes, which shapes each one works with, and the two that are reserved are listed in
   [*What ships today*](../concepts/themes.md#what-ships-today). You are only offered themes that work with your site's active
   [shape](../concepts/shapes.md).
2. **Switch.**
   - **In the browser:** follow [*Site Theme*](../../webapp/user/user-manual.md#site-theme) in the user manual. Your site
     re-renders straight away.
   - **At the command line:** follow [*Themes*](../../cli/user/command-reference.md#themes) in the command reference. You set
     `active_theme` in `.polis/bundles/registry.json` and re-render.
3. **Check it.** Open your published site. The theme is private configuration: it lives in the registry, not in
   `.well-known/polis`, so nothing about your public identity changes.

Your site theme and the web app's own light or dark appearance are separate settings
([*Light vs dark*](../concepts/themes.md#light-vs-dark)). Changing one does not change the other.

## 2 · Write your own CSS-only theme

### ⚠️ Where a custom theme can run today

**Only on a site you render yourself**, with the CLI or the local web app. A theme polis did not ship has no entry in the bundle's
declaration, and that has three consequences:

| Where | What happens to a theme polis did not ship |
|---|---|
| **A site on polis.pub** | polis.pub's maintenance cycle moves any undeclared theme directory into quarantine and resets an undeclared `active_theme` to a random shipped theme. **A custom theme cannot be used there.** |
| **Your own site, after `tailor --apply`** | [Tailor](../concepts/actors.md#tailor) deletes undeclared theme directories as drift and resets an undeclared `active_theme` to a random shipped theme. **Keep your theme's source outside `.polis/`**; after running it, copy the theme back in, set `active_theme` again and re-render. |
| **The web app's theme picker** | lists only declared themes, so yours is never offered. Switch to it from the command line. |

⚠️ **Do not edit a shipped theme's stylesheet in place.** `polis render` reinstalls every shipped file whose bytes differ from the
copy built into the CLI, so your edits are overwritten on the next render. Copy it under a new name instead, as below.

### Steps

The examples call the theme `my-theme`. Use your own name; lowercase letters, digits and hyphens are safe everywhere it appears.

1. **Start from a copy.** Pick the shipped theme closest to what you want from
   [the list](../concepts/themes.md#what-ships-today), and copy its stylesheet into a directory named after your theme. ⛔ **The
   file name must match the directory name**: polis finds a CSS-only theme by looking for `<name>/<name>.css`.

   ```text
   .polis/bundles/pub.polis.core/themes/my-theme/my-theme.css
   ```

2. **Set the palette.** Change the `--color-*` values in the `:root` block. Keep every name, even ones your palette does not use:
   the shapes read them all ([*CSS variable contract*](../concepts/themes.md#css-variable-contract)).

3. **Set both filter buckets** if your site uses the stream shape. The six `--filter-dark-*` and six `--filter-light-*` tokens
   colour the sentence filter when someone visits your site with their own top bar. Their three rules are in
   [*Cross-theme compatibility*](../concepts/themes.md#cross-theme-compatibility).

4. **Make it active.** In `.polis/bundles/registry.json`, set `active_theme` to the fully-qualified name, then re-render:

   ```text
   "active_theme": "pub.polis.themes.my-theme"
   ```

   ```text
   polis render --force
   ```

   The render copies your stylesheet into the site's `styles.css`. On the stream shape it is appended after the shape's own
   stylesheet, so your values win.

5. **Check both shapes, if you want both.** Switch `active_shape` ([*Active shape*](../concepts/shapes.md#active-shape-per-tenant)),
   re-render, and look again. A palette that reads well on one shape's page anatomy can fail on the other's.

### Going beyond CSS

A theme can also replace individual templates. Put a template with the shape's file name, such as `post.html` for the blog shape or
`stream-post.html` for the stream shape, in your theme's directory. The renderer looks in the theme's directory before the shape's
([*The render pipeline*](../concepts/shapes.md#the-render-pipeline)), and the template syntax is in
[templating](../../cli/user/templating.md).

⚠️ **An override stops following the shape.** When a later polis release changes that template, a CSS-only theme picks up the
change and your copy does not. Override only what CSS cannot do.

### What your theme does not reach

- **The web app's own chrome.** The top bar and page colours of the web app are defined per shipped theme inside the web app, so
  they do not follow your palette.
- **A visitor's top bar.** When someone signed in visits your site, the top bar is theirs. Your theme supplies only the sentence
  filter, from the buckets in step 3.
