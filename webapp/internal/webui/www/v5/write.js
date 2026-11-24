// write (v5 spec 2.1, 3.3): everything the owner has written, newest first:
// their posts, and their comments on other people's posts. It is where a
// comment waiting for their blessing is decided.
//
// The sheet behind the toolbar's + writes (1.14); the filters are the two
// kinds and the one state, waiting (1.4); a post's thread opens under its
// card with the waiting comments first, each blessed or turned away there.
(function (root) {
  'use strict';

  // ── The rules ────────────────────────────────────────────────────────

  var KINDS = { post: 'all posts from me', comment: 'all comments from me' };
  var WAITING = 'all comments from all polis to bless';

  function norm(s) { return String(s || '').trim().toLowerCase().replace(/\s+/g, ' '); }

  // view is what an addressed sentence opens (3.9): a kind, the waiting
  // queue, or one post's waiting comments.
  function view(sentence) {
    var s = norm(sentence);
    if (s === KINDS.post) return { kind: 'post' };
    if (s === KINDS.comment) return { kind: 'comment' };
    if (s === WAITING) return { waiting: true };
    var m = String(sentence || '').trim().match(/^all comments on (\S+) to bless$/i);
    if (m) return { waiting: true, post: m[1] };
    return {};
  }

  // counts is what the filters say (1.4): each kind counts what the other
  // picked filters leave; waiting counts the comments waiting on the picked
  // posts (on every post when none is picked, and none when only comments
  // are, since nothing waits on a comment of the owner's).
  function counts(s) {
    var kinds = s.picked.length ? s.picked : ['post', 'comment'];
    var waitingPosts = (s.items.post || []).filter(function (p) { return p.waiting > 0; });
    return {
      post: s.waitingOn ? waitingPosts.length : s.totals.post || 0,
      comment: s.waitingOn ? 0 : s.totals.comment || 0,
      waiting: kinds.indexOf('post') >= 0 ? s.waiting : 0,
    };
  }

  function label(k, n) { return k === 'waiting' ? n + ' waiting' : n + ' ' + k + (n === 1 ? '' : 's'); }

  // merge is the two answers as one list, newest first.
  function merge(lists) {
    var at = function (it) { var t = Date.parse(it.published); return isNaN(t) ? -Infinity : t; };
    return [].concat.apply([], lists).sort(function (a, b) { return at(b) - at(a); });
  }

  // samePost compares two addresses of one post, whatever their form (a
  // path or a full address, .md or .html).
  function postKey(u) {
    return String(u || '').replace(/^https?:\/\/[^/]+/, '').replace(/\.(md|html)$/, '').replace(/^\/content\/pub\.polis\.core\/post\//, '/posts/');
  }

  // thread is a post's thread as write draws it (3.3): the waiting comments
  // first, then the rest, oldest first; each with what can be done to it.
  function thread(waiting, all, me) {
    var waitingURLs = {};
    waiting.forEach(function (c) { waitingURLs[postKey(c.url)] = true; });
    var rows = waiting.map(function (c) { return { c: c, state: 'waiting', actions: ['not here', 'Bless'] }; });
    all.filter(function (c) { return !waitingURLs[postKey(c.url)]; }).forEach(function (c) {
      var own = String(c.author_domain || '').toLowerCase() === me;
      rows.push({ c: c, state: c.blessed ? 'blessed' : 'not blessed', actions: c.blessed ? (own ? ['reply'] : ['reply', 'unbless']) : [] });
    });
    return rows;
  }

  var model = { KINDS: KINDS, WAITING: WAITING, view: view, counts: counts, label: label, merge: merge, postKey: postKey, thread: thread };
  if (typeof module !== 'undefined' && module.exports) module.exports = model;

  // ── The drawing ──────────────────────────────────────────────────────

  var V5 = root.PolisV5, Card = root.PolisV5Card, Sheet = root.PolisV5Sheet;
  if (!V5 || !Card || !Sheet || !root.document) return;
  var doc = root.document, esc = Card.esc;

  var sheet = null, list = null, ctx = null, tbar = null;
  var st = { picked: [], waitingOn: false, items: {}, totals: {}, waiting: 0, following: {}, loading: 0, openPost: '' };

  function me() { var site = (ctx.state && ctx.state.site) || {}; return Card.hostOf(site.base_url) || root.location.host; }
  function base() { var site = (ctx.state && ctx.state.site) || {}; return (site.base_url || root.location.origin).replace(/\/+$/, ''); }
  function cardCtx() { return { me: me(), base: base(), own: true }; }
  function full(u) { return /^https?:\/\//.test(u || '') ? u : base() + '/' + String(u || '').replace(/^\/+/, ''); }

  function mark() {
    var m = doc.getElementById('me');
    return '<span class="av" aria-hidden="true"' + (m && m.getAttribute('style') ? ' style="' + esc(m.getAttribute('style')) + '"' : '') + '></span>';
  }

  // ── the filters ──
  function paintFilters() {
    var n = counts(st);
    tbar.querySelectorAll('.kinds button').forEach(function (b) {
      var k = b.dataset.kind || 'waiting';
      var on = k === 'waiting' ? st.waitingOn : st.picked.indexOf(k) >= 0;
      b.classList.toggle('on', on);
      b.setAttribute('aria-pressed', on ? 'true' : 'false');
      var words = label(k, n[k]).split(' ');
      b.innerHTML = '<b>' + words[0] + '</b> ' + esc(words.slice(1).join(' '));
      if (k === 'waiting') b.classList.toggle('has', n.waiting > 0);
    });
  }

  function shown() {
    var kinds = st.picked.length ? st.picked : ['post', 'comment'];
    var items = merge(kinds.map(function (k) { return st.items[k] || []; }));
    if (st.waitingOn) items = items.filter(function (it) { return it.type === 'post' && it.waiting > 0; });
    return items;
  }

  function draw() {
    paintFilters();
    var items = shown(), c = cardCtx();
    list.innerHTML = items.map(function (it) {
      return Card.html(Card.parts(it, c), mark).replace('<article ', '<article data-key="' + esc(postKey(it.url)) + '" ');
    }).join('');
    list._items = items;
    if (!items.length) {
      list.innerHTML = '<p class="cm-none"></p>';
      list.firstChild.textContent = st.waitingOn ? 'Nothing is waiting for you.' : 'Nothing here yet.';
    }
    Card.fits(list);
    // The review queue: picking waiting opens each post that has one (2.1).
    if (st.waitingOn) list.querySelectorAll('.item.post').forEach(function (card) { Card.openThread(card, { thread: loadThread }); });
    if (st.openPost) openPost(st.openPost);
  }

  // A filter is a moment (3.9): the address goes back to /_/write.
  function pick(k) {
    if (k === 'waiting') st.waitingOn = !st.waitingOn;
    else {
      var i = st.picked.indexOf(k);
      if (i >= 0) st.picked.splice(i, 1); else st.picked.push(k);
    }
    st.openPost = '';
    if (root.location.pathname !== '/_/write') root.history.replaceState(null, '', '/_/write');
    draw();
  }

  async function load() {
    var ticket = ++st.loading, api = ctx.api;
    var got = await Promise.all([
      api.query(KINDS.post, { limit: 50, counts: 'posts', new_for: 'write' }),
      api.query(KINDS.comment, { limit: 50, counts: 'comments', new_for: 'write' }),
      api.query(WAITING, { limit: 200 }),
      api.get('/v1/content/follow'),
    ]);
    if (ticket !== st.loading) return;
    var bad = got.slice(0, 2).filter(function (r) { return !r.ok; })[0];
    if (bad) { list.innerHTML = '<p class="cm-none"></p>'; list.firstChild.textContent = bad.error; return; }
    st.items.post = got[0].items;
    st.items.comment = got[1].items;
    st.totals.post = typeof got[0].counts.posts === 'number' ? got[0].counts.posts : got[0].items.length;
    st.totals.comment = typeof got[1].counts.comments === 'number' ? got[1].counts.comments : got[1].items.length;
    st.waiting = got[2].ok ? got[2].items.length : 0;
    st.following = {};
    ((got[3].ok && got[3].data && got[3].data.following) || []).forEach(function (f) {
      st.following[Card.hostOf(f.url || f) || String(f.url || f).toLowerCase()] = true;
    });
    draw();
  }

  // ── a post's thread: waiting first, each decided in place (2.1, 3.3) ──
  function itemOf(card) { var i = Array.prototype.indexOf.call(list.children, card); return (list._items || [])[i]; }

  async function loadThread(card, inner) {
    var it = itemOf(card);
    if (!it) return;
    var post = full(it.url);
    var got = await Promise.all([
      ctx.api.query('all comments on ' + post + ' to bless', { limit: 50 }),
      ctx.api.query('all comments on ' + post, { limit: 50 }),
    ]);
    if (!got[1].ok) { inner.innerHTML = '<p class="cm-none"></p>'; inner.firstChild.textContent = got[1].error; return; }
    var rows = thread(got[0].ok ? got[0].items : [], got[1].items, me());
    if (!rows.length) { inner.innerHTML = '<p class="cm-none">No comments yet.</p>'; return; }
    inner.innerHTML = rows.map(function (r) {
      var c = r.c, who = String(c.author_domain || '').toLowerCase(), own = who === me();
      var words = c.comment_body_html || (c.excerpt ? '<p>' + esc(c.excerpt) + '</p>' : '');
      var acts = r.actions.map(function (a) {
        if (a === 'Bless') return '<button class="slim go" type="button" data-cm="bless">Bless</button>';
        if (a === 'not here') return '<button class="link" type="button" data-cm="deny">not here</button>';
        if (a === 'unbless') return '<button class="link" type="button" data-cm="unbless">unbless</button>';
        return '<button class="link" type="button" data-cm="reply">reply</button>';
      }).join('');
      var out = c.url && r.state !== 'waiting' ? '<a class="out" href="' + esc(c.url) + '" target="_blank" rel="noopener" aria-label="Open on ' + esc(who) + '" title="Open on ' + esc(who) + '"><svg viewBox="0 0 24 24" aria-hidden="true"><path d="M14 4h6v6M20 4l-9 9M18 14v5a1 1 0 0 1-1 1H5a1 1 0 0 1-1-1V7a1 1 0 0 1 1-1h5"/></svg></a>' : '';
      return '<div class="cm' + (r.state === 'waiting' ? ' req' : '') + (own ? ' mine' : '') + '" data-url="' + esc(c.url || '') + '" data-site="' + esc(who) + '" data-version="' + esc(c.version || '') + '" data-blessed="' + (c.blessed ? '1' : '0') + '">' +
        '<div class="cm-meta"><b>' + esc(own ? 'you' : who) + '</b>' + (r.state === 'waiting' ? '<span>would like to comment</span>' : '') +
        '<span>' + esc(Card.fullDate(c.published)) + '</span>' + (st.following[who] && !own ? '<span class="auto">you follow them</span>' : '') + '</div>' +
        '<div class="cm-x">' + words + '</div><div class="cm-act">' + acts + out + '</div></div>';
    }).join('');
  }

  async function decide(btn) {
    var cm = btn.closest('.cm'), card = btn.closest('.item'), it = itemOf(card), what = btn.dataset.cm;
    if (what === 'reply') {
      var quote = (cm.querySelector('.cm-x') || {}).textContent || '';
      sheet.open('reply', { url: cm.dataset.url, root: full(it.url), site: cm.dataset.site, self: cm.dataset.site === me(), quote: quote.trim() });
      return;
    }
    var body = { comment_url: cm.dataset.url, in_reply_to: full(it.url) };
    if (what === 'bless' && cm.dataset.version) body.comment_version = cm.dataset.version;
    btn.disabled = true;
    var r = await ctx.api.post('/v1/content/comment/actions/' + what, body);
    btn.disabled = false;
    if (!r.ok) { ctx.hint(r.error); return; }
    if (what === 'deny') ctx.hint('Not here. It stays on ' + cm.dataset.site + ', just not under your post.');
    if (what === 'unbless') ctx.hint('Unblessed. It stays on ' + cm.dataset.site + ', and leaves your post at the next sync.');
    cm.classList.add('going');
    // The counts follow what was decided: one fewer waiting.
    if (what !== 'unbless') {
      st.waiting = Math.max(0, st.waiting - 1);
      it.waiting = Math.max(0, (it.waiting || 0) - 1);
    }
    setTimeout(function () {
      paintFilters();
      var w = card.querySelector('.wait b');
      if (w) { w.textContent = it.waiting; card.querySelector('.wait').classList.toggle('has', it.waiting > 0); }
      loadThread(card, card.querySelector('.thread-in'));
    }, 260);
  }

  // /_/write#<post path> opens that post's card, in view (3.9);
  // /_/write#edit:<post path> (the public site's edit, 3.8) opens it to edit.
  function openPost(path) {
    var editing = path.indexOf('edit:') === 0;
    if (editing) path = path.slice(5);
    var card = list.querySelector('.item[data-key="' + postKey(path).replace(/"/g, '') + '"]');
    if (!card) return;
    st.openPost = '';
    Card.open(card);
    setTimeout(function () { card.scrollIntoView({ behavior: 'smooth', block: 'start' }); }, 200);
    var edit = editing && card.querySelector('.edit');
    if (edit) edit.click();
  }

  // A post's or comment's markdown as the owner wrote it, without the
  // frontmatter (made again on republishing): post.get / comment.get.
  async function source(type, path) {
    var r = await ctx.api.get('/v1/content/' + (type === 'comment' ? 'comment' : 'post') + '/source?path=' + encodeURIComponent(path));
    return r.ok && r.data ? r.data.markdown : null;
  }

  V5.section('write', {
    mount: function (pane, frameCtx) {
      ctx = frameCtx;
      tbar = doc.querySelector('.tbar[data-for="write"]');
      list = doc.createElement('div');
      list.className = 'list';
      list.id = 'list';
      pane.appendChild(list);
      sheet = Sheet.create(pane, tbar, {
        api: ctx.api, hint: ctx.hint, plusLabel: 'Write something new',
        onPublished: function () { load(); },
      });
      var kinds = doc.createElement('div');
      kinds.className = 'views kinds';
      kinds.setAttribute('role', 'group');
      kinds.setAttribute('aria-label', 'Filters');
      kinds.innerHTML = '<button type="button" data-kind="post" aria-pressed="false"></button><button type="button" data-kind="comment" aria-pressed="false"></button>' +
        '<span class="ksep" aria-hidden="true"></span><button type="button" class="newk" aria-pressed="false"></button>';
      tbar.appendChild(kinds);
      kinds.addEventListener('click', function (e) {
        var b = e.target.closest('button');
        if (!b) return;
        e.stopPropagation();
        if (!b.dataset.kind && !st.waitingOn && !st.waiting) return;
        pick(b.dataset.kind || 'waiting');
      });
      Card.wire(list, { thread: loadThread });
      list.addEventListener('click', function (e) {
        var d = e.target.closest('[data-cm]');
        if (d) { e.stopPropagation(); decide(d); return; }
        var edit = e.target.closest('.edit');
        if (!edit) return;
        var el = edit.closest('.item'), it = itemOf(el);
        if (!it || !it.source_path) return;
        source(it.type, it.source_path).then(function (text) {
          if (text === null) { ctx.hint('That text could not be read just now.'); return; }
          sheet.open('edit', { path: it.source_path, id: it.id, type: it.type }, text);
        });
      }, true);
    },
    // Each visit draws what is there now; an address picks its view (3.9).
    show: function (opts) {
      var v = view(opts && opts.sentence);
      st.picked = v.kind ? [v.kind] : [];
      st.waitingOn = !!v.waiting;
      st.openPost = v.post || String(root.location.hash || '').replace(/^#/, '');
      load();
    },
    // Leaving while writing first steps out of the sheet (1.14).
    leave: function () { return !(sheet && sheet.close()); },
  });
})(typeof window !== 'undefined' ? window : globalThis);
