// gather (v5 spec 2.2, 3.4): what is happening around the owner's polis
// network. Posts and comments from people the owner follows and what others
// did that concerns them, merged newest first; search for people and
// writing beyond the network; follow; a post's thread opened under its card,
// and a reply written in the card or, in focus mode, in the sheet.
//
// ⭐ Every view is a PQL sentence on /v1 (1.1), and every count comes from
// the same sentence as its list (1.4), so the number and the list never
// disagree. New is state, not a word in a sentence: new_for=gather.
(function (root) {
  'use strict';

  // ── The rules ────────────────────────────────────────────────────────

  var ORDER = ['post', 'comment', 'event'];
  var KINDS = {
    post: 'all posts from my network',
    comment: 'all comments from my network',
    event: 'all activity about me',
  };
  // The counts block names what it counts by PQL type.
  var COUNTS = { post: 'posts', comment: 'comments', event: 'activity' };
  var NOUN = { post: 'post', comment: 'comment', event: 'event' };

  function norm(s) { return String(s || '').trim().toLowerCase().replace(/\s+/g, ' '); }

  // kindOf is the filter an addressed view picks (3.9): one of gather's own
  // sentences, or '' for any other.
  function kindOf(sentence) {
    var s = norm(sentence);
    for (var i = 0; i < ORDER.length; i++) if (norm(KINDS[ORDER[i]]) === s) return ORDER[i];
    return '';
  }

  // counts is what the filters say (1.4): each kind counts what the other
  // picked filters leave, and new counts the new items of the picked kinds
  // (of every kind when none is picked). s: { totals, fresh (new, per kind),
  // passed (new items scrolled past, per kind), picked, newOn }.
  function counts(s) {
    var out = {};
    var fresh = function (k) { return Math.max(0, (s.fresh[k] || 0) - ((s.passed && s.passed[k]) || 0)); };
    ORDER.forEach(function (k) { out[k] = s.newOn ? fresh(k) : (s.totals[k] || 0); });
    out['new'] = ORDER.filter(function (k) { return !s.picked.length || s.picked.indexOf(k) >= 0; })
      .reduce(function (n, k) { return n + fresh(k); }, 0);
    return out;
  }

  // label is a filter's words: "1 post", "6 comments", "3 new". atLeast marks
  // a count that is a lower bound ("300+ posts"): the server listed it in
  // counts_capped because it reached the cache's limit or the read limit.
  function label(k, n, atLeast) {
    if (k === 'new') return n + ' new';
    return n + (atLeast ? '+' : '') + ' ' + NOUN[k] + (n === 1 && !atLeast ? '' : 's');
  }

  // merge is several answers as one list, newest first.
  function merge(lists) {
    var all = [].concat.apply([], lists);
    var at = function (it) { var t = Date.parse(it.published); return isNaN(t) ? -Infinity : t; };
    return all.sort(function (a, b) { return at(b) - at(a); });
  }

  // kindOfItem is which filter an item answers to.
  function kindOfItem(it) { return it.type === 'event' ? 'event' : it.type === 'comment' ? 'comment' : 'post'; }

  // isAddress: a site's address typed into search, which goes straight to
  // that site (2.2) rather than searching for the words.
  function isAddress(w) {
    return /^(https?:\/\/)?[a-z0-9]([a-z0-9-]*[a-z0-9])?(\.[a-z0-9]([a-z0-9-]*[a-z0-9])?)+\/?$/i.test(String(w || '').trim());
  }
  function siteOf(w) { return String(w || '').trim().toLowerCase().replace(/^https?:\/\//, '').replace(/\/+$/, ''); }

  // followLabel is the Follow button's words: the public site's button (3.4).
  function followLabel(on, hover) { return on ? (hover ? '− Unfollow' : '✓ Following') : '+ Follow'; }

  // threadSentence is a post's thread (2.2).
  function threadSentence(postURL) { return 'all comments on ' + postURL; }

  // followed is the follow list's sites, as hosts.
  function followed(list) {
    return (list || []).map(function (f) {
      var u = typeof f === 'string' ? f : (f && (f.url || f.domain || f.author_url)) || '';
      return siteOf(u);
    }).filter(Boolean);
  }

  // arrivals are what each kind's new list (now, in ORDER; null where the
  // ask failed) holds that its drawn list (drawn, by kind) did not, by kind;
  // a kind with nothing new is left out. An item is known by its id, else its
  // address, as its card is.
  function arrivals(drawn, now) {
    var out = {};
    ORDER.forEach(function (k, i) {
      if (!now[i]) return;
      var had = {};
      (drawn[k] || []).forEach(function (it) { had[it.id || it.url || ''] = true; });
      var a = now[i].filter(function (it) { return !had[it.id || it.url || '']; });
      if (a.length) out[k] = a;
    });
    return out;
  }

  // followIntent reads the follow a link from polis.pub carries to gather
  // (spec 3.9: '/_/following?intent=follow&target=<site>…' lands here with the
  // follow carried out): the site to follow, and the address without the
  // intent's parameters, so a reload asks nothing. null when there is no
  // follow to carry out. ⭐ The token (ft, fe) was checked by the server
  // before this page was served; an intent it refused was stripped, so one
  // that arrives is this owner's own.
  function followIntent(search) {
    var q = new URLSearchParams(String(search || '').replace(/^\?/, ''));
    if (q.get('intent') !== 'follow') return null;
    var site = siteOf(q.get('target'));
    ['intent', 'target', 'ft', 'fe'].forEach(function (k) { q.delete(k); });
    var rest = q.toString();
    return { site: isAddress(site) ? site : '', search: rest ? '?' + rest : '' };
  }

  var model = {
    followIntent: followIntent,
    ORDER: ORDER, KINDS: KINDS, COUNTS: COUNTS, arrivals: arrivals,
    kindOf: kindOf, counts: counts, label: label, merge: merge, kindOfItem: kindOfItem,
    isAddress: isAddress, siteOf: siteOf, followLabel: followLabel, threadSentence: threadSentence, followed: followed,
  };
  if (typeof module !== 'undefined' && module.exports) module.exports = model;

  // ── The drawing ──────────────────────────────────────────────────────

  var V5 = root.PolisV5, Card = root.PolisV5Card, Sheet = root.PolisV5Sheet;
  if (!V5 || !Card || !Sheet || !root.document) return;
  var doc = root.document;

  var SEARCH = '<svg viewBox="0 0 24 24" aria-hidden="true"><circle cx="11" cy="11" r="6.5"/><path d="M20 20l-4.2-4.2"/></svg>';
  var CLOSE = '<svg class="x" viewBox="0 0 16 16" aria-hidden="true"><path d="M4 4l8 8M12 4l-8 8"/></svg>';

  var ctx = null, pane = null, list = null, found = null, tbar = null, sq = null, find = null, sheet = null;
  var st = {
    picked: [], newOn: false, adhoc: '',
    totals: {}, atLeast: {}, fresh: {}, passed: {}, items: {}, freshItems: {}, adhocItems: [],
    following: {}, seenPast: {}, loading: 0, finding: 0,
    arrived: {},   // card keys that arrived while the owner was here and not yet seen: they hold the menu dot
  };

  function me() {
    var site = (ctx.state && ctx.state.site) || {};
    return Card.hostOf(site.base_url) || root.location.host;
  }
  function cardCtx() { return { me: me(), base: ((ctx.state && ctx.state.site) || {}).base_url || root.location.origin }; }

  // markFor is a site's mark: its own avatar where the item carries one.
  function markFor(item) {
    return function (who) {
      var style = V5.model.avatarStyle(item.author_avatar, function (s) { return root.btoa(s); });
      return '<span class="av" aria-hidden="true"' + (style ? ' style="' + Card.esc(style) + '"' : '') + '></span>';
    };
  }

  function draw(box, items) {
    var c = cardCtx();
    box.innerHTML = items.map(function (it) {
      var h = Card.html(Card.parts(it, c), markFor(it));
      // The filter an item answers to, and its key, beside it.
      return h.replace('<article ', '<article data-kind="' + kindOfItem(it) + '" data-key="' + Card.esc(kindOfItem(it) + ':' + (it.id || it.url || '')) + '" ');
    }).join('');
    box._items = items;
    paintFollows(box);
    Card.fits(box);
  }

  // ── the filters (1.4) ──
  function paintFilters() {
    var n = counts(st);
    tbar.querySelectorAll('.kinds button').forEach(function (b) {
      var k = b.dataset.kind || 'new';
      var on = k === 'new' ? st.newOn : st.picked.indexOf(k) >= 0;
      b.classList.toggle('on', on);
      b.setAttribute('aria-pressed', on ? 'true' : 'false');
      var words = label(k, n[k], !st.newOn && !!st.atLeast[k]).split(' ');
      b.innerHTML = '<b>' + words[0] + '</b> ' + Card.esc(words.slice(1).join(' '));
      if (k === 'new') b.classList.toggle('has', n['new'] > 0);
    });
  }

  function shown() {
    if (st.adhoc) return st.adhocItems;
    var kinds = st.picked.length ? st.picked : ORDER;
    return merge(kinds.map(function (k) { return (st.newOn ? st.freshItems[k] : st.items[k]) || []; }));
  }

  function repaint() {
    paintFilters();
    var items = shown();
    draw(list, items);
    if (!items.length) {
      list.innerHTML = '<p class="cm-none"></p>';
      list.firstChild.textContent = st.newOn ? 'Nothing new since you were last here.' : 'Nothing here yet.';
    }
    watchPast();
  }

  // pick changes a filter. A filter is a moment, so the address goes back
  // to the section's own (3.9).
  function pick(k) {
    if (k === 'new') st.newOn = !st.newOn;
    else {
      var i = st.picked.indexOf(k);
      if (i >= 0) st.picked.splice(i, 1); else st.picked.push(k);
    }
    if (st.adhoc) st.adhoc = '';
    if (root.location.pathname !== '/_/gather') root.history.replaceState(null, '', '/_/gather');
    repaint();
  }

  // ⭐ surface=spa asks the server to stamp each post with its comment count
  // from the network, blessed or not (spec 1.7: outside the author's own site
  // an unblessed comment is a comment). Without it a network post carries no
  // count at all, and its card says 0.
  var COUNTED = 'spa';

  // ── loading: each sentence once for its list and count, once kept to new ──
  async function load() {
    var mine = ++st.loading;
    var asks = [];
    ORDER.forEach(function (k) {
      var o = { new_for: 'gather', limit: 30, counts: COUNTS[k], surface: COUNTED };
      asks.push(ctx.api.query(KINDS[k], o));
      asks.push(ctx.api.query(KINDS[k], { new_for: 'gather', only_new: 1, limit: 100 }));
    });
    if (st.adhoc) asks.push(ctx.api.query(st.adhoc, { new_for: 'gather', limit: 50, surface: COUNTED }));
    var got = await Promise.all(asks);
    if (mine !== st.loading) return;
    var bad = got.filter(function (r) { return !r.ok; })[0];
    if (bad) { list.innerHTML = '<p class="cm-none"></p>'; list.firstChild.textContent = bad.error; return; }
    ORDER.forEach(function (k, i) {
      var all = got[i * 2], fresh = got[i * 2 + 1];
      st.items[k] = all.items;
      var c = all.counts[COUNTS[k]];
      st.totals[k] = typeof c === 'number' ? c : all.items.length;
      st.atLeast[k] = (all.counts_capped || []).indexOf(COUNTS[k]) >= 0;
      st.freshItems[k] = fresh.items;
      st.fresh[k] = fresh.items.length;
    });
    if (st.adhoc) st.adhocItems = got[got.length - 1].items;
    st.passed = {};
    st.seenPast = {};
    st.arrived = {};
    repaint();
  }

  function cardKey(it) { return kindOfItem(it) + ':' + (it.id || it.url || ''); }

  // arrive puts what arrived while the owner is in gather into what it shows
  // (v4 waited for a click; Vincent 2026-11-20): the counts take it, and the
  // cards a view shows go in at the top, each with its new dot, the reader's
  // place held. The menu dot stays lit until the owner has seen them.
  function arrive(byKind) {
    var added = [];
    Object.keys(byKind).forEach(function (k) {
      var a = byKind[k];
      st.freshItems[k] = a.concat(st.freshItems[k] || []);
      st.items[k] = a.concat(st.items[k] || []);
      st.fresh[k] = (st.fresh[k] || 0) + a.length;
      st.totals[k] = (st.totals[k] || 0) + a.length;
      a.forEach(function (it) { st.arrived[cardKey(it)] = true; });
      // Shown now, so leaving gather sees them too: the visit's seen time moves
      // up to the newest that arrived (never past now), and they are not new
      // again next time.
      a.forEach(function (it) {
        var t = Date.parse(it.published), was = Date.parse(ctx.state.arrived);
        if (!isNaN(t) && t > was) ctx.state.arrived = new Date(Math.min(t, Date.now())).toISOString();
      });
      if (!st.adhoc && (!st.picked.length || st.picked.indexOf(k) >= 0)) added = added.concat(a);
    });
    paintFilters();
    if (!added.length) return;
    if (!list.querySelector('.item')) { repaint(); return; }
    added = merge([added]);
    // Hold the reader's place: the first card in view stays where it is.
    var anchor = null, before = 0;
    Array.prototype.some.call(list.children, function (c) {
      if (c.getBoundingClientRect().bottom > 64) { anchor = c; before = c.getBoundingClientRect().top; return true; }
      return false;
    });
    var box = doc.createElement('div');
    draw(box, added);
    var first = list.firstElementChild;
    while (box.firstElementChild) list.insertBefore(box.firstElementChild, first);
    list._items = added.concat(list._items || []);
    Card.fits(list);
    if (anchor) {
      var moved = anchor.getBoundingClientRect().top - before;
      if (Math.abs(moved) > 1) root.scrollBy(0, moved);
    }
    watchPast();
  }

  // seen clears the menu dot once every card that arrived here has been in
  // full view, in a tab the owner is looking at.
  function seen(key) {
    if (!st.arrived[key] || doc.hidden) return;
    delete st.arrived[key];
    if (!Object.keys(st.arrived).length) ctx.dot('gather', false);
  }
  // Cards that arrived while the tab was in the background count once the
  // owner looks: observing them again reports the ones in full view.
  doc.addEventListener('visibilitychange', function () {
    if (!doc.hidden && doc.body.dataset.sec === 'gather' && Object.keys(st.arrived).length) watchPast();
  });

  // ── a follow link from polis.pub (3.9): carried out at once, as the v4 app
  // did. The owner asked for it by clicking Follow on polis.pub, and the
  // server's token check proves this session made that request. ──
  var followingLoaded = null, intentTaken = false;
  async function followFromLink() {
    if (intentTaken) return;
    intentTaken = true;
    var fi = followIntent(root.location.search);
    if (!fi) return;
    root.history.replaceState(null, '', root.location.pathname + fi.search);
    if (!fi.site) return;
    if (followingLoaded) await followingLoaded;
    if (st.following[fi.site]) { ctx.hint('You already follow ' + fi.site + '.'); return; }
    var r = await ctx.api.post('/v1/content/follow', { url: 'https://' + fi.site });
    if (!r.ok) { ctx.hint(r.error); return; }
    st.following[fi.site] = true;
    paintFollows(pane);
    ctx.hint('Following ' + fi.site + '. What they write will gather here.');
  }

  async function loadFollowing() {
    var r = await ctx.api.get('/v1/content/follow');
    if (!r.ok) return;
    st.following = {};
    followed(r.data && r.data.following).forEach(function (s) { st.following[s] = true; });
    paintFollows(pane);
  }

  // ── new, as it scrolls past (1.6): the count goes down; the dots stay
  // until the owner leaves gather, when everything that was new is seen ──
  var past = null;
  function watchPast() {
    if (!root.IntersectionObserver) return;
    if (past) past.disconnect();
    var bar = 64;
    past = new root.IntersectionObserver(function (entries) {
      entries.forEach(function (e) {
        var card = e.target.closest('.item'), key = card && card.dataset.key;
        if (!key || st.seenPast[key] === 'past') return;
        if (e.isIntersecting && e.intersectionRatio >= 1) { st.seenPast[key] = 'seen'; seen(key); return; }
        if (!e.isIntersecting && st.seenPast[key] === 'seen' && e.boundingClientRect.bottom <= bar + 1) {
          st.seenPast[key] = 'past';
          var k = card.dataset.kind;
          st.passed[k] = (st.passed[k] || 0) + 1;
          paintFilters();
        }
      });
    }, { rootMargin: '-' + bar + 'px 0px 0px 0px', threshold: [0, 1] });
    list.querySelectorAll('.item[data-new] .hd').forEach(function (hd) { past.observe(hd); });
  }

  // ── following (3.4): the same button as on the public site ──
  function paintFollows(box) {
    (box || pane).querySelectorAll('.follow[data-site]').forEach(function (b) {
      var on = !!st.following[b.dataset.site];
      b.textContent = followLabel(on, b.matches(':hover'));
      b.classList.toggle('following', on);
    });
  }
  async function toggleFollow(b) {
    var site = b.dataset.site, on = !!st.following[site];
    b.disabled = true;
    var r = on
      ? await ctx.api.post('/v1/content/follow/actions/delete', { url: 'https://' + site })
      : await ctx.api.post('/v1/content/follow', { url: 'https://' + site });
    b.disabled = false;
    if (!r.ok) { ctx.hint(r.error); return; }
    if (on) delete st.following[site]; else st.following[site] = true;
    paintFollows(pane);
    b.classList.add('pulse');
    setTimeout(function () { b.classList.remove('pulse'); }, 400);
    // gather keeps its hint after following, unlike the public site (3.4).
    ctx.hint(on ? 'Unfollowed ' + site + '.' : 'Following ' + site + '. What they write will gather here.');
  }

  // ── a post's thread, opened under its card (2.2, 1.5) ──
  function itemOf(card) {
    var box = card.parentNode, i = Array.prototype.indexOf.call(box.children, card);
    return (box._items || [])[i];
  }
  // postURL is a post's full address (the owner's own items carry a path).
  function postURL(it) {
    if (/^https?:\/\//.test(it.url || '')) return it.url;
    return cardCtx().base.replace(/\/+$/, '') + '/' + String(it.url || '').replace(/^\/+/, '');
  }

  // readWhole is another site's post in full: the card has only its excerpt.
  async function readWhole(card) {
    var it = itemOf(card);
    if (!it) return '';
    var r = await ctx.api.get('/v1/content/post/body?url=' + encodeURIComponent(postURL(it)));
    return (r.ok && r.data && r.data.body_html) || '';
  }

  async function loadThread(card, inner) {
    var it = itemOf(card);
    if (!it) return;
    var ir = inner.querySelector('.ir-in');
    var r = await ctx.api.query(threadSentence(postURL(it)), { limit: 50 });
    var box = doc.createElement('div');
    box.className = 'cms';
    if (!r.ok) { box.innerHTML = '<p class="cm-none"></p>'; box.firstChild.textContent = r.error; }
    else if (!r.items.length) box.innerHTML = '<p class="cm-none">No comments yet.</p>';
    else {
      var you = me();
      box.innerHTML = r.items.map(function (c) {
        var who = (c.author_domain || Card.hostOf(c.author_url) || '').toLowerCase();
        var words = c.comment_body_html ? c.comment_body_html : '<p>' + Card.esc(c.excerpt || '') + '</p>';
        return '<div class="cm' + (who === you ? ' mine' : '') + '" data-blessed="' + (c.blessed === false ? '0' : '1') + '" data-url="' + Card.esc(c.url || '') + '" data-site="' + Card.esc(who) + '">' +
          '<div class="cm-meta"><b>' + Card.esc(who) + '</b><span>' + Card.esc(Card.relativeTime(c.published)) + '</span>' +
          (st.following[who] ? '<span class="auto">you follow them</span>' : '') + '</div>' +
          '<div class="cm-x">' + words + '</div>' +
          '<div class="cm-act"><button class="link reply" type="button">reply</button>' +
          (c.url ? '<a class="out" href="' + Card.esc(c.url) + '" target="_blank" rel="noopener" aria-label="Open on ' + Card.esc(who) + '" title="Open on ' + Card.esc(who) + '"><svg viewBox="0 0 24 24" aria-hidden="true"><path d="M14 4h6v6M20 4l-9 9M18 14v5a1 1 0 0 1-1 1H5a1 1 0 0 1-1-1V7a1 1 0 0 1 1-1h5"/></svg></a>' : '') +
          '</div></div>';
      }).join('');
    }
    var old = inner.querySelector('.cms');
    if (old) old.remove();
    inner.appendChild(box);
    if (ir) inner.insertBefore(ir, inner.firstChild);
  }

  // ── replying in the card (3.4): a small sheet between the post and the
  // other comments; "focus mode" moves the words to the sheet ──
  function replyTarget(card, cm) {
    var it = itemOf(card), url = postURL(it);
    if (!cm) return { url: url, site: Card.hostOf(url), quote: it.title || it.excerpt || '' };
    return { url: cm.dataset.url, root: url, site: cm.dataset.site, quote: (cm.querySelector('.cm-x') || {}).textContent || '' };
  }

  function openReply(card, cm) {
    // Replying in the card: the post opened in full, then the reply, then
    // the other comments (1.14).
    Card.open(card);
    Card.openThread(card, { thread: loadThread });
    var inner = card.querySelector('.thread-in');
    var old = inner.querySelector('.ir-in');
    if (old) old.remove();
    var target = replyTarget(card, cm);
    var ir = doc.createElement('div');
    ir.className = 'ir-in';
    ir.innerHTML = '<div class="ir-k' + (cm ? ' on' : '') + '"><b></b><span></span></div>' +
      '<textarea class="ir-t" rows="3" aria-label="Your reply" placeholder="Write a reply…"></textarea>' +
      '<p class="sheet-err" role="alert" hidden></p>' +
      '<div class="ir-row"><button class="link fm" type="button">focus mode</button><span class="sp"></span>' +
      '<button class="link cancel" type="button">cancel</button><button class="slim go" type="button" disabled>Publish reply</button></div>';
    if (cm) { ir.querySelector('.ir-k b').textContent = 'replying to ' + target.site; ir.querySelector('.ir-k span').textContent = target.quote; }
    inner.insertBefore(ir, inner.firstChild);
    card.classList.add('composing');
    var t = ir.querySelector('.ir-t'), go = ir.querySelector('.go'), err = ir.querySelector('.sheet-err');
    t.addEventListener('input', function () { go.disabled = !t.value.trim(); });
    ir.querySelector('.cancel').onclick = function () { ir.remove(); card.classList.remove('composing'); };
    ir.querySelector('.fm').onclick = function () {
      var words = t.value;
      ir.remove();
      card.classList.remove('composing');
      sheet.open('reply', target, words);
    };
    go.onclick = async function () {
      go.disabled = true;
      var q = Sheet.request('reply', t.value.trim(), target);
      var r = await ctx.api.post(q.path, q.body);
      if (!r.ok) { err.textContent = r.error; err.hidden = false; go.disabled = false; return; }
      ir.remove();
      card.classList.remove('composing');
      ctx.hint('Your reply is published on your site.');
      loadThread(card, inner);
    };
    setTimeout(function () { t.focus({ preventScroll: true }); }, 200);
  }

  // ── search (2.2): one field; an address goes to that site, words search ──
  function openSearch() {
    doc.body.classList.add('searching');
    sq.setAttribute('aria-label', 'Close search');
    setTimeout(function () { find.focus(); }, 60);
    if (ctx.placeBox) ctx.placeBox(false);
  }
  function closeSearch() {
    doc.body.classList.remove('searching');
    sq.setAttribute('aria-label', 'search');
    find.value = '';
    find.blur();
    found.hidden = true;
    found.innerHTML = '';
    list.hidden = false;
  }
  var findTimer = 0;
  async function runFind() {
    var words = find.value.trim(), mine = ++st.finding;
    if (!words) { found.hidden = true; found.innerHTML = ''; list.hidden = false; return; }
    var people, writing;
    if (isAddress(words)) {
      var site = siteOf(words);
      var r = await ctx.api.query('all profiles from all polis', { search: site, limit: 10 });
      if (mine !== st.finding) return;
      var hit = r.items.filter(function (p) { return (p.author_domain || '').toLowerCase() === site; });
      people = hit.length ? hit : [{ type: 'profile', id: 'profile:' + site, author_domain: site, author_url: 'https://' + site }];
      writing = [];
    } else {
      var both = await Promise.all([
        ctx.api.query('all profiles from all polis', { search: words, limit: 10 }),
        ctx.api.query('all posts from all polis', { search: words, limit: 20, surface: COUNTED }),
      ]);
      if (mine !== st.finding) return;
      var bad = both.filter(function (x) { return !x.ok; })[0];
      if (bad) { found.hidden = false; list.hidden = true; found.innerHTML = '<p class="nothing"></p>'; found.firstChild.textContent = bad.error; return; }
      people = both[0].items;
      writing = both[1].items;
    }
    found.hidden = false;
    list.hidden = true;
    found.innerHTML = '';
    if (!people.length && !writing.length) {
      found.innerHTML = '<p class="nothing"></p>';
      found.firstChild.textContent = 'Nothing on polis matches “' + words + '” yet.';
      return;
    }
    [['people and sites', people], ['writing', writing]].forEach(function (g) {
      if (!g[1].length) return;
      var head = doc.createElement('div');
      head.className = 'lbl2';
      head.textContent = g[0];
      var box = doc.createElement('div');
      box.className = 'list';
      found.appendChild(head);
      found.appendChild(box);
      draw(box, g[1]);
      Card.wire(box, { thread: loadThread, whole: readWhole });
    });
  }

  // ── the section ──
  V5.section('gather', {
    mount: function (p, frameCtx) {
      ctx = frameCtx;
      pane = p;
      tbar = doc.querySelector('.tbar[data-for="gather"]');
      tbar.innerHTML = '<button class="sq" type="button" aria-label="search">' + SEARCH + CLOSE + '</button>' +
        '<div class="views kinds" role="group" aria-label="Filters">' +
        ORDER.map(function (k) { return '<button type="button" data-kind="' + k + '" aria-pressed="false"></button>'; }).join('') +
        '<span class="ksep" aria-hidden="true"></span><button type="button" class="newk" aria-pressed="false"></button></div>' +
        '<label class="find">' + SEARCH + '<input type="search" autocomplete="off" spellcheck="false" placeholder="find people, sites, or post titles" aria-label="Find"></label>';
      sq = tbar.querySelector('.sq');
      find = tbar.querySelector('.find input');
      list = doc.createElement('div');
      list.className = 'list';
      found = doc.createElement('div');
      found.className = 'found';
      found.hidden = true;
      pane.appendChild(found);
      pane.appendChild(list);
      // The sheet, for a reply in focus mode, in rich text with write's
      // formatting buttons; the search button is its close.
      sheet = Sheet.create(pane, tbar, {
        api: ctx.api, hint: ctx.hint, button: sq, plusLabel: 'search',
        onPublished: function () {
          pane.querySelectorAll('.item.talk').forEach(function (card) { loadThread(card, card.querySelector('.thread-in')); });
        },
      });
      sq.addEventListener('click', function () { if (doc.body.classList.contains('searching')) closeSearch(); else openSearch(); });
      tbar.querySelector('.kinds').addEventListener('click', function (e) {
        var b = e.target.closest('button');
        if (b) pick(b.dataset.kind || 'new');
      });
      find.addEventListener('keydown', function (e) {
        if (e.key === 'Escape') { e.stopPropagation(); closeSearch(); }
        if (e.key === 'Enter') { clearTimeout(findTimer); runFind(); }
      });
      find.addEventListener('input', function () { clearTimeout(findTimer); findTimer = setTimeout(runFind, 300); });

      Card.wire(list, { thread: loadThread, whole: readWhole });
      pane.addEventListener('click', function (e) {
        var f = e.target.closest('.follow[data-site]');
        if (f) { e.stopPropagation(); toggleFollow(f); return; }
        var rep = e.target.closest('.reply');
        if (rep) {
          var card = rep.closest('.item');
          if (card) { e.stopPropagation(); openReply(card, rep.closest('.cm')); }
          return;
        }
        // A place in the owner's app (/_/…) opens in place: the frame's.
      }, true);
      pane.addEventListener('mouseover', function (e) { var b = e.target.closest('.follow'); if (b) b.textContent = followLabel(!!st.following[b.dataset.site], true); });
      pane.addEventListener('mouseout', function (e) { var b = e.target.closest('.follow'); if (b) b.textContent = followLabel(!!st.following[b.dataset.site], false); });
      followingLoaded = loadFollowing();
    },
    // Each visit draws what is there now: the view an address names (3.9),
    // or everything.
    show: function (opts) {
      var k = opts && opts.sentence ? kindOf(opts.sentence) : '';
      st.picked = k ? [k] : [];
      st.newOn = false;
      st.adhoc = opts && opts.sentence && !k ? opts.sentence : '';
      paintFilters();
      load();
      followFromLink();
    },
    // fresher answers the frame's dot while the owner is in gather: has
    // anything new arrived beyond what gather drew? (What it drew is new
    // against the seen time too, so the plain dot question cannot tell.)
    // What arrived is put in at once (arrive). Asking is also a read of the
    // owner's network, which keeps the network synced while the tab stays on
    // gather.
    fresher: async function () {
      if (!Object.keys(st.freshItems).length) return false;   // not drawn yet
      var mine = st.loading;
      var got = await Promise.all(ORDER.map(function (k) {
        return ctx.api.query(KINDS[k], { new_for: 'gather', only_new: 1, limit: 100 });
      }));
      if (mine !== st.loading || doc.body.dataset.sec !== 'gather') return false;   // redrawn, or left, meanwhile
      var a = arrivals(st.freshItems, ORDER.map(function (k, i) { return got[i].ok ? got[i].items : null; }));
      if (!Object.keys(a).length) return false;
      arrive(a);
      return true;
    },
    // again is the owner pressing gather while in it with its dot lit: up to
    // what arrived.
    again: function () { root.scrollTo({ top: 0, behavior: 'smooth' }); },
    // Leaving while replying in focus mode first steps out of the sheet.
    leave: function () {
      if (sheet && sheet.close()) return false;
      if (doc.body.classList.contains('searching')) closeSearch();
      return true;
    },
  });
})(typeof window !== 'undefined' ? window : globalThis);
