// The sheet: one editor for all writing (v5 spec 1.14), a new post, an edit,
// a reply. Opening it is one motion in place (focus mode): the box's edge
// gives way to the page, the sheet grows under the toolbar, the list steps
// back and the footer slides away. Esc, the close (×) or any menu item steps
// back out. Nothing navigates.
//
// Unfinished writing is saved on the owner's site as a draft as it is
// written (op:post.save_draft, owner-only), and removed by the server once the
// post it became is published (post.create's draft_id).
// A post's title is its first line; there is no title field.
(function (root) {
  'use strict';

  // label is what the publish button says: what it does (1.14).
  function label(mode) {
    return mode === 'edit' ? 'Republish' : mode === 'reply' ? 'Publish reply' : 'Publish';
  }

  // format applies a formatting button to the text and its selection [a, z),
  // in markdown, and answers the new text and selection. It is the markdown
  // side of the editor: writing in markdown, or before the rich text has
  // loaded (or if it cannot).
  function format(v, a, z, fmt) {
    var sel = v.slice(a, z), ls = v.lastIndexOf('\n', a - 1) + 1;
    function wrap(m) { return { value: v.slice(0, a) + m + sel + m + v.slice(z), a: a + m.length, z: z + m.length }; }
    // line replaces the current line's block marker (a heading's #s, a
    // quote's >, a list's bullet or number) with mark.
    function line(mark) {
      var old = (v.slice(ls).match(/^(?:#{1,6} |> |[-*+] |\d+[.)] )/) || [''])[0];
      var d = mark.length - old.length;
      return { value: v.slice(0, ls) + mark + v.slice(ls + old.length), a: Math.max(ls, a + d), z: Math.max(ls, z + d) };
    }
    switch (fmt) {
      case 'bold': return wrap('**');
      case 'italic': return wrap('_');
      case 'code': return wrap('`');
      // The first line is the post's title, which "# " marks; below it, "## ".
      case 'heading': return line(v.slice(0, ls).trim() ? '## ' : '# ');
      case 'body': return line('');
      case 'quote': return { value: v.slice(0, ls) + '> ' + v.slice(ls), a: a + 2, z: z + 2 };
      case 'list': return line('- ');
      case 'numbers': return line('1. ');
      case 'divider': {
        // A rule stands on its own between blank lines; "***" at the very
        // start, where "---" would read as frontmatter.
        var before = v.slice(0, z).replace(/\s+$/, ''), after = v.slice(z).replace(/^\s+/, '');
        var hr = (before ? before + '\n\n---' : '***') + '\n\n';
        return { value: hr + after, a: hr.length, z: hr.length };
      }
      case 'link': {
        var at = sel ? z + 11 : a + 1;
        return { value: v.slice(0, a) + '[' + sel + '](https://)' + v.slice(z), a: at, z: at };
      }
    }
    return { value: v, a: a, z: z };
  }

  // republishable is whether the publish button may be pressed: there are
  // words, and an edit has changed something. ⭐ A post opened and left alone
  // is never sent again, so it keeps its bytes and its signature.
  function republishable(mode, text, opened) {
    if (!String(text).trim()) return false;
    return mode !== 'edit' || text !== opened;
  }

  // request is the /v1 call that publishes what the sheet holds. A new post
  // written in a draft names it, and the server removes the draft once the
  // post is out.
  function request(mode, text, target, draftId) {
    if (mode === 'edit') {
      // One of the owner's comments, or a post: each by its file's path.
      var type = target.type === 'comment' ? 'comment' : 'post';
      return { method: 'PUT', path: '/v1/content/' + type + '/' + encodeURIComponent(target.id || type), body: { path: target.path, markdown: text } };
    }
    if (mode === 'reply') {
      var body = { in_reply_to: target.url, markdown: text };
      if (target.root) body.root_post = target.root;
      return { method: 'POST', path: '/v1/content/comment', body: body };
    }
    var post = { markdown: text };
    if (draftId) post.draft_id = draftId;
    return { method: 'POST', path: '/v1/content/post', body: post };
  }

  var I = function (d) { return '<svg viewBox="0 0 24 24" aria-hidden="true">' + d + '</svg>'; };
  var FMT = [
    ['body', 'Body text', I('<path d="M13 5v14M17 5v14M17 5h-6.5a4 4 0 0 0 0 8H13"/>'), ''],
    ['heading', 'Heading', 'H', ''],
    ['sep'],
    ['bold', 'Bold', 'B', ''],
    ['italic', 'Italic', 'I', 'i'],
    ['code', 'Code', I('<path d="M9 8l-4 4 4 4M15 8l4 4-4 4"/>'), ''],
    ['link', 'Link', I('<path d="M10 14a4 4 0 0 0 5.7 0l3-3a4 4 0 0 0-5.7-5.7l-1 1"/><path d="M14 10a4 4 0 0 0-5.7 0l-3 3a4 4 0 0 0 5.7 5.7l1-1"/>'), ''],
    ['sep'],
    ['quote', 'Quote', I('<path d="M5 7v10M10 9h9M10 15h6"/>'), ''],
    ['list', 'Bulleted list', I('<path d="M10 7h10M10 12h10M10 17h10"/><circle cx="5" cy="7" r=".6"/><circle cx="5" cy="12" r=".6"/><circle cx="5" cy="17" r=".6"/>'), ''],
    ['numbers', 'Numbered list', I('<path d="M10 7h10M10 12h10M10 17h10"/><path d="M4 5.5l1.2-.7V9M4 15.2a1.1 1.1 0 1 1 1.9.8L4 18.6h2.2"/>'), ''],
    ['divider', 'Divider', I('<path d="M4 12h16"/><path d="M8 7h8M8 17h8" opacity=".35"/>'), ''],
    ['sep', 'hist'],
    ['undo', 'Undo', I('<path d="M9 14L4 9l5-5"/><path d="M4 9h10a6 6 0 0 1 0 12h-3"/>'), 'hist'],
    ['redo', 'Redo', I('<path d="M15 14l5-5-5-5"/><path d="M20 9H10a6 6 0 0 0 0 12h3"/>'), 'hist'],
    ['sep', 'mdsep'],
    ['markdown', 'Markdown', 'md', 'md'],
  ];

  // The writer's choice of markdown over rich text, remembered in this
  // browser only (a convenience: without it, rich text). One choice for every
  // place the owner writes.
  var MD_KEY = 'polis.v5.sheet.markdown';
  function prefersMarkdown() {
    try { return root.localStorage.getItem(MD_KEY) === '1'; } catch (e) { return false; }
  }
  // editorAddress is the vendored editor's address, with its version, which
  // the shell names (so the file is fetched only when the sheet needs it).
  function editorAddress(doc) {
    var m = doc.querySelector && doc.querySelector('meta[name="polis-editor"]');
    return (m && m.getAttribute('content')) || '';
  }
  // fmtButtons is the formatting buttons, as a toolbar holds them.
  function fmtButtons() {
    return FMT.map(function (f) {
      return f[0] === 'sep' ? '<span class="sep' + (f[1] ? ' ' + f[1] : '') + '"></span>' :
        '<button type="button" data-fmt="' + f[0] + '" aria-label="' + f[1] + '" title="' + f[1] + '"' + (f[3] ? ' class="' + f[3] + '"' : '') +
        (f[0] === 'markdown' ? ' aria-pressed="false"' : '') + '>' + f[2] + '</button>';
    }).join('');
  }

  // field is one place where the owner writes markdown, in rich text by
  // default (rich.js over the vendored editor, loaded the first time it is
  // wanted): the sheet's words, and the bio in enhance. The text area is
  // markdown: the writer's choice, and what the field falls back to until the
  // editor has loaded, or if it cannot. The same buttons act on either.
  // o: { doc, fmt (filled here with the buttons), rich (the box the editor
  // goes in), body (the text area), kitAt (the editor's address; none:
  // markdown only), live() (whether the buttons act now), onChange() (the
  // editor came or went), onInput() (the writer changed the words or the
  // selection), onMount() (the editor has just taken the text area's place) }.
  function field(o) {
    var doc = o.doc, fmt = o.fmt, richBox = o.rich, body = o.body;
    var Rich = root.PolisV5Rich, kitAt = Rich ? (o.kitAt || '') : '';
    var live = o.live || function () { return true; };
    var onChange = o.onChange || function () {}, onInput = o.onInput || onChange;
    fmt.innerHTML = fmtButtons();
    var ed = { handle: null, host: null, kit: null, loading: null, failed: !kitAt, token: 0, gone: false };
    var md = !kitAt || prefersMarkdown();
    function loadKit() {
      if (ed.kit || ed.failed) return Promise.resolve(ed.kit);
      if (!ed.loading) {
        ed.loading = import(kitAt).then(function (k) { ed.kit = k; return k; },
          function () { ed.failed = true; showMode(); return null; });
      }
      return ed.loading;
    }
    // text is the words as they stand, in markdown.
    function text() { return ed.handle ? ed.handle.markdown() : body.value; }
    // dropRich puts the text area back in the rich editor's place; keep
    // carries the rich text's markdown over into it (switching to markdown).
    function dropRich(keep) {
      var h = ed.handle, host = ed.host;
      ed.handle = null; ed.host = null; ed.token++;
      if (h) {
        if (keep) body.value = h.markdown();
        Promise.resolve(h.destroy()).catch(function () {}).then(function () { if (host) host.remove(); });
      }
      richBox.hidden = true;
      body.hidden = false;
    }
    // ensureRich puts the words in the rich editor once it has loaded, unless
    // the writer writes in markdown, or has typed in the text area meanwhile.
    // It answers whether the editor is up, once that is settled.
    function ensureRich() {
      if (md || ed.failed || ed.handle || ed.gone) return Promise.resolve(!!ed.handle);
      var t = ++ed.token, snap = body.value;
      return loadKit().then(async function (kit) {
        if (!kit || md || ed.gone || t !== ed.token || body.value !== snap) return false;
        var host = doc.createElement('div');
        richBox.appendChild(host);
        var h;
        try { h = await Rich.mount(kit, host, snap, { onUpdate: onRich, askLink: askLink }); } catch (e) { host.remove(); return false; }
        if (ed.gone || t !== ed.token || body.value !== snap) { Promise.resolve(h.destroy()).then(function () { host.remove(); }); return false; }
        ed.handle = h; ed.host = host;
        var pm = host.querySelector('[contenteditable]');
        if (pm) { pm.setAttribute('role', 'textbox'); pm.setAttribute('aria-multiline', 'true'); pm.setAttribute('aria-label', body.getAttribute('aria-label')); }
        richBox.hidden = false;
        body.hidden = true;
        showMode();
        onChange();
        if (o.onMount) o.onMount();
        return true;
      });
    }
    function setText(value) {
      dropRich(false);
      body.value = value;
    }
    function setMarkdown(on) {
      md = on;
      try { root.localStorage.setItem(MD_KEY, on ? '1' : '0'); } catch (e) { /* a convenience only */ }
      if (on) dropRich(true); else ensureRich();
      showMode();
      onChange();
      focus();
    }
    function onRich(h) { pressed(h.active()); onInput(); }
    // askLink asks for a link's address: '' takes the link off, null leaves it.
    function askLink(current) {
      var v = root.prompt(current ? 'Link to (leave it empty to remove the link):' : 'Link to:', current || 'https://');
      if (v === null || (!current && v.trim() === 'https://')) return null;
      return v;
    }
    function pressed(a) {
      fmt.querySelectorAll('button[data-fmt]').forEach(function (b) {
        var f = b.dataset.fmt;
        if (f === 'markdown') return;
        if (a && f in a) b.setAttribute('aria-pressed', a[f] ? 'true' : 'false'); else b.removeAttribute('aria-pressed');
      });
    }
    function showMode() {
      var mdBtn = fmt.querySelector('[data-fmt="markdown"]');
      fmt.classList.toggle('plain', !ed.handle);
      fmt.classList.toggle('no-rich', ed.failed);
      if (mdBtn) mdBtn.setAttribute('aria-pressed', md || ed.failed ? 'true' : 'false');
      if (!ed.handle) pressed(null);
    }
    function focus() {
      if (ed.handle) ed.handle.focus(); else body.focus({ preventScroll: true });
    }
    function setName(name) {
      body.setAttribute('aria-label', name);
      if (ed.host) { var pm = ed.host.querySelector('[contenteditable]'); if (pm) pm.setAttribute('aria-label', name); }
    }
    showMode();

    fmt.querySelectorAll('button').forEach(function (b) {
      b.addEventListener('mousedown', function (e) { e.preventDefault(); });
      b.addEventListener('click', function (e) {
        if (!live()) return;
        e.stopPropagation();
        var f = b.dataset.fmt;
        if (f === 'markdown') { if (!ed.failed) setMarkdown(!md); return; }
        // In rich text the editor's own change (onRich) reports the input.
        if (ed.handle) { ed.handle.run(f); return; }
        if (f === 'undo' || f === 'redo') return;
        var r = format(body.value, body.selectionStart, body.selectionEnd, f);
        body.value = r.value;
        body.focus();
        body.setSelectionRange(r.a, r.z);
        onInput();
      });
    });
    body.addEventListener('input', function () { onInput(); });

    return {
      text: text, setText: setText, ensure: ensureRich, focus: focus, setName: setName,
      // load fetches the editor ahead, unless the writer writes in markdown.
      load: function () { return md ? Promise.resolve(null) : loadKit(); },
      rich: function () { return !!ed.handle; },
      // destroy takes the editor down for good (the field is leaving the page).
      destroy: function () { ed.gone = true; dropRich(false); },
    };
  }

  // create puts the sheet in a pane, its + and formatting in the section's
  // toolbar, before the toolbar's filters (which it hides while writing).
  // opts: { api, hint, plusLabel, onPublished(result, mode) }, and for a
  // section whose toolbar button is not a + (gather's search): button, the
  // toolbar's own button, which becomes the sheet's close while it is open
  // (the sheet then opens only for a reply, never from the toolbar).
  // formatting: false leaves the buttons out (the sheet is then markdown only).
  function create(pane, tbar, opts) {
    var doc = root.document, api = opts.api;
    var state = { mode: 'new', target: null, draft: '', opened: '', saveTimer: 0, saving: null };
    var own = !opts.button, formatting = opts.formatting !== false;

    var plus = opts.button;
    if (own) {
      plus = doc.createElement('button');
      plus.type = 'button';
      plus.className = 'plus';
      plus.id = 'plus';
      plus.setAttribute('aria-label', opts.plusLabel || 'Write something new');
      plus.innerHTML = '<svg viewBox="0 0 16 16" aria-hidden="true"><path d="M8 2.5v11M2.5 8h11"/></svg>';
      tbar.insertBefore(plus, tbar.firstChild);
      tbar.tabIndex = 0;
      tbar.setAttribute('aria-label', opts.plusLabel || 'Write something new');
    }
    var fmt = doc.createElement('div');
    fmt.className = 'fmt';
    fmt.setAttribute('aria-label', 'Formatting');
    if (own) fmt.id = 'fmt';
    // The buttons sit just after the section's button.
    function fmtHome() { tbar.insertBefore(fmt, plus && plus.parentNode === tbar ? plus.nextSibling : tbar.firstChild); }
    if (formatting) fmtHome();

    var sheet = doc.createElement('div');
    sheet.className = 'sheet';
    sheet.innerHTML = '<div><div class="re"><div class="k"></div><div class="x"></div></div>' +
      '<div class="body-wrap"><div class="body-rich" hidden></div><textarea class="body-in" aria-label="Write something" tabindex="-1"></textarea>' +
      '<div class="signoff" aria-hidden="true">Yours, truly.</div></div>' +
      '<p class="sheet-err" role="alert" hidden></p>' +
      '<div class="pub-row"><button class="link unpub" type="button" hidden>unpublish</button>' +
      '<button class="slim publish" type="button" disabled>Publish</button></div></div>';
    pane.insertBefore(sheet, pane.firstChild);
    var body = sheet.querySelector('.body-in'), pub = sheet.querySelector('.publish'), err = sheet.querySelector('.sheet-err');
    var unpub = sheet.querySelector('.unpub'), richBox = sheet.querySelector('.body-rich');
    richBox.inert = true;

    // ── the words: rich text by default, markdown by choice (field) ──
    var fld = field({
      doc: doc, fmt: fmt, rich: richBox, body: body, kitAt: formatting ? editorAddress(doc) : '',
      live: function () { return isOpen(); },
      onChange: function () { changed(); },
      onInput: function () { changed(); saveSoon(); },
      onMount: function () { if (isOpen()) fld.focus(); },
    });
    function text() { return fld.text(); }
    function setText(value) { fld.setText(value); }
    function focus() { fld.focus(); }
    // The editor's file is fetched ahead, the first time a hand goes near
    // the toolbar, so it is usually there by the time the sheet opens.
    if (formatting) ['pointerenter', 'focusin'].forEach(function (k) { tbar.addEventListener(k, function () { fld.load(); }, { once: true }); });

    // fitFmt gives the formatting buttons the room up to the menu, so on a
    // narrow page they scroll sideways instead of running under it; on a
    // phone, where the menu fills the box, they move to a row of their own at
    // the top of the sheet (and back into the toolbar when there is room).
    function fitFmt() {
      var ways = doc.querySelector('.ways');
      if (!formatting || !ways || !isOpen()) return;
      fmt.style.maxWidth = '';
      fmt.classList.remove('below');
      if (fmt.parentNode !== tbar) fmtHome();
      var room = Math.floor(ways.getBoundingClientRect().left - fmt.getBoundingClientRect().left - 8);
      if (fmt.scrollWidth <= room) return;
      if (room >= 160) { fmt.style.maxWidth = room + 'px'; return; }
      fmt.classList.add('below');
      var inner = sheet.firstChild;
      inner.insertBefore(fmt, inner.firstChild);
    }
    if (formatting) root.addEventListener('resize', fitFmt);

    function isOpen() { return doc.body.classList.contains('focus'); }
    function say(text) { err.textContent = text || ''; err.hidden = !text; }
    function changed() {
      var v = text();
      pub.disabled = !republishable(state.mode, v, state.opened);
      sheet.querySelector('.signoff').classList.toggle('gone', v.length > 0);
      if (!body.hidden) {
        body.style.height = 'auto';
        body.style.height = body.scrollHeight + 'px';
      }
    }

    // ── drafts: kept on the site as the owner writes (new posts) ──
    function saveSoon() {
      if (state.mode !== 'new') return;
      clearTimeout(state.saveTimer);
      state.saveTimer = setTimeout(saveNow, 1200);
    }
    async function saveNow() {
      clearTimeout(state.saveTimer);
      var v = text();
      if (state.mode !== 'new' || !v.trim()) return;
      var r = await api.post('/v1/content/post/actions/save_draft', state.draft ? { id: state.draft, markdown: v } : { markdown: v });
      if (r.ok && r.data && r.data.id) state.draft = r.data.id;
      else if (!r.ok) say('Your draft could not be saved just now: ' + r.error);
    }

    function open(mode, target, words, draftId) {
      state.mode = mode || 'new';
      state.target = target || null;
      state.draft = draftId || '';
      pub.textContent = label(state.mode);
      unpub.hidden = !canUnpublish(state.mode, state.target);
      doc.body.classList.toggle('replying', state.mode === 'reply');
      if (state.mode === 'reply') {
        sheet.querySelector('.re .k').textContent = target.self ? 'replying to yourself' : 'replying to ' + (target.site || '');
        sheet.querySelector('.re .x').textContent = target.quote || '';
      }
      var name = state.mode === 'reply' ? 'Your reply' : 'Write something';
      if (words !== undefined) setText(words);
      fld.setName(name);
      // What the sheet opened with: an edit is republishable once it differs.
      state.opened = text();
      say('');
      root.scrollTo({ top: 0, behavior: 'smooth' });
      doc.body.classList.add('focus');
      plus.setAttribute('aria-label', 'Close');
      plus.title = 'Close (Esc)';
      body.tabIndex = 0;
      richBox.inert = false;
      changed();
      fld.ensure();
      setTimeout(function () { fitFmt(); focus(); }, 120);
    }

    // close steps back out of focus mode. ⭐ Nothing is lost: a new post's
    // words are already a draft, and are saved once more on the way out.
    function close() {
      if (!isOpen()) return false;
      if (state.mode === 'new' && text().trim()) saveNow();
      doc.body.classList.remove('focus');
      plus.setAttribute('aria-label', opts.plusLabel || 'Write something new');
      plus.title = '';
      if (doc.activeElement && sheet.contains(doc.activeElement)) doc.activeElement.blur();
      body.tabIndex = -1;
      richBox.inert = true;
      if (state.mode !== 'new') {
        setTimeout(function () { setText(''); doc.body.classList.remove('replying'); state.mode = 'new'; state.target = null; state.opened = ''; pub.textContent = label('new'); changed(); }, 400);
      }
      return true;
    }

    // ⚠️ Stops the button's other listeners too: a section's own use of its
    // button (gather's search) must not also run on the click that closes.
    plus.addEventListener('click', function (e) { if (isOpen()) { e.stopImmediatePropagation(); close(); } });
    if (own) {
      tbar.addEventListener('click', function (e) { if (!isOpen() && !e.target.closest('.views, .find')) open('new'); });
      tbar.addEventListener('keydown', function (e) {
        if (!isOpen() && e.target === tbar && (e.key === 'Enter' || e.key === ' ')) { e.preventDefault(); open('new'); }
      });
    }
    sheet.addEventListener('click', function (e) { if (isOpen() && !e.target.closest('button, .body-rich')) focus(); });
    doc.addEventListener('keydown', function (e) { if (e.key === 'Escape' && isOpen()) close(); });

    pub.addEventListener('click', async function (e) {
      e.stopPropagation();
      var words = text();
      if (!republishable(state.mode, words, state.opened)) return;
      words = words.trim();
      pub.disabled = true;
      clearTimeout(state.saveTimer);
      var q = request(state.mode, words, state.target || {}, state.mode === 'new' ? state.draft : '');
      var r = q.method === 'PUT' ? await api.put(q.path, q.body) : await api.post(q.path, q.body);
      if (!r.ok) { say(r.error); pub.disabled = false; return; }
      var mode = state.mode;
      state.draft = '';
      setText('');
      state.opened = '';
      changed();
      close();
      if (opts.hint) opts.hint(mode === 'edit' ? 'Signed again and republished.' : mode === 'reply' ? 'Your reply is published on your site.' : 'Published, signed with your key.');
      if (opts.onPublished) opts.onPublished(r.data, mode);
    });

    // Taking the post down: asked first (it cannot be undone), then the one
    // operation; its text comes back as a draft.
    unpub.addEventListener('click', async function (e) {
      e.stopPropagation();
      if (!canUnpublish(state.mode, state.target)) return;
      if (!root.confirm('Unpublish this post? It leaves your site and polis, and its text comes back as a draft. ' +
        'Publishing it again makes a new post; comments shown on it are not shown again. This cannot be undone.')) return;
      unpub.disabled = true;
      var q = unpublishRequest(state.target);
      var r = await api.post(q.path, q.body);
      unpub.disabled = false;
      if (!r.ok) { say(r.error); return; }
      setText('');
      state.opened = '';
      changed();
      close();
      if (opts.hint) opts.hint('Unpublished. Its text is in your drafts.');
      if (opts.onPublished) opts.onPublished(r.data, 'unpublish');
    });

    return { open: open, close: close, isOpen: isOpen, saveNow: saveNow };
  }

  // canUnpublish is whether the sheet offers to take its post down: only while
  // editing one of the owner's published posts (a comment is not offered).
  function canUnpublish(mode, target) {
    return mode === 'edit' && !!target && target.type !== 'comment' && typeof target.path === 'string' &&
      target.path.indexOf('content/pub.polis.core/post/') === 0;
  }
  // unpublishRequest is the one operation that takes a post down (/v1, owner only).
  function unpublishRequest(target) {
    return { method: 'POST', path: '/v1/content/post/actions/unpublish', body: { path: target.path } };
  }

  var sheet = { label: label, field: field, editorAddress: editorAddress, format: format, republishable: republishable, request: request, create: create, canUnpublish: canUnpublish, unpublishRequest: unpublishRequest };
  root.PolisV5Sheet = sheet;
  if (typeof module !== 'undefined' && module.exports) module.exports = sheet;
})(typeof window !== 'undefined' ? window : globalThis);
