// The v5 owner app's card (v5 spec 1.5, part 4): every item in a list is one
// card, built from the same parts in the same order as the public site's,
// which the v5 shape draws in Go: one markup contract, two renderers, and a
// test that they agree (v5-card.test.js).
//
//   <article class="item <kind>" data-…>   state beside the item, never in its words
//     .stick > .srow > .sin                the pinned row
//     .hd                                  the mark, then the first row
//     h3 | .on | .what                     the lead
//     .ex                                  the body
//     .ifoot                               the rule and the footer, only when it has items
//     .thread                              the thread, opened inside the card
//
// ⭐ The card draws an item's state; it never works it out (1.5): new, waiting,
// blessed and the counts come with the item from /v1.
(function (root) {
  'use strict';

  var MONTHS = ['Jan', 'Feb', 'Mar', 'Apr', 'May', 'Jun', 'Jul', 'Aug', 'Sep', 'Oct', 'Nov', 'Dec'];

  function esc(s) {
    return String(s == null ? '' : s).replace(/[&<>"']/g, function (c) {
      return { '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#39;' }[c];
    });
  }

  // fullDate is an archive's date: "28 Aug 2026" (the owner's own work).
  function fullDate(iso) {
    var d = new Date(iso);
    if (isNaN(d)) return '';
    return d.getUTCDate() + ' ' + MONTHS[d.getUTCMonth()] + ' ' + d.getUTCFullYear();
  }

  // relativeTime is a stream's time: "just now", "5m", "2h", "yesterday",
  // "10 Nov" (what others did), the year added when it is not this one.
  function relativeTime(iso, now) {
    var d = new Date(iso), n = now ? new Date(now) : new Date();
    if (isNaN(d)) return '';
    var s = (n - d) / 1000;
    if (s < 60) return 'just now';
    if (s < 3600) return Math.floor(s / 60) + 'm';
    if (s < 86400) return Math.floor(s / 3600) + 'h';
    if (s < 172800) return 'yesterday';
    var out = d.getUTCDate() + ' ' + MONTHS[d.getUTCMonth()];
    return d.getUTCFullYear() === n.getUTCFullYear() ? out : out + ' ' + d.getUTCFullYear();
  }

  function hostOf(u) {
    var m = String(u || '').match(/^https?:\/\/([^/]+)/);
    return m ? m[1].toLowerCase() : '';
  }

  // absolute is an item's address as a full URL: own items carry their path.
  function absolute(u, base) {
    if (/^https?:\/\//.test(u || '')) return u;
    if (!u) return '';
    return String(base || '').replace(/\/+$/, '') + '/' + String(u).replace(/^\/+/, '');
  }

  // linkText is an address as a line: without its scheme or a page's
  // extension (vdibart.polis.pub/comments/20260304/hello). A place in the
  // owner's app reads as what it shows: one post (/_/write#<post path>) as
  // that post's address on the owner's site, a view as its sentence.
  function linkText(u, me) {
    var s = String(u || '');
    var app = s.match(/^\/_\/[a-z]+(?:\/pql\/([^#]*))?(?:#(.*))?$/);
    if (app) {
      if (app[2]) return ((me || '') + app[2]).replace(/\.(html|md)$/, '');
      if (app[1]) { try { return decodeURIComponent(app[1].replace(/\+/g, ' ')); } catch (e) { return app[1].replace(/\+/g, ' '); } }
    }
    return s.replace(/^https?:\/\//, '').replace(/\.(html|md)$/, '');
  }

  function plural(n, noun) { return n + ' ' + noun + (n === 1 ? '' : 's'); }

  // The verbs an event's first row carries, by the event's kind (1.5).
  var VERBS = {
    'new-follower': 'followed you', 'lost-follower': 'unfollowed you',
    // As spec 3.4 labels them: each verb is followed by the event's lead.
    'new-comment': 'commented on', 'blessing-requested': 'asked to comment on',
    'blessing-granted': 'blessed your comment on', 'blessing-denied': 'turned your comment away',
  };

  // parts is a card's model: what it shows, in 1.5's order, from an item and
  // where it is drawn. ctx: { section, me (the site's host), base (its
  // address), own (every item is the owner's), now }.
  function parts(item, ctx) {
    ctx = ctx || {};
    var me = (ctx.me || '').toLowerCase();
    var type = item.type || 'post';
    var author = (item.author_domain || hostOf(item.author_url) || item.actor || '').toLowerCase();
    // ⭐ ctx.own: the sentence says whose items these are (all … from me),
    // so the card need not work it out from an address a site may not have.
    var mine = !!ctx.own || (!!me && author === me);
    var kind = type === 'event' ? 'event' : type === 'comment' ? 'cmt' : type === 'profile' ? 'person' : 'post';
    var url = absolute(item.url, mine ? ctx.base : '');
    var p = {
      kind: kind,
      mine: mine,
      data: { path: item.url || '' },
      mark: { who: author },
      first: { newDot: !!item['new'], handle: '', verb: '', time: '', timeFull: '', tools: [] },
      lead: null,
      body: '',
      footer: [],
      out: url,
      outSite: hostOf(url) || author,
    };
    if (item.blessed) p.data.blessed = '1';
    if (item.waiting) p.data.waiting = String(item.waiting);
    if (item.agent) p.data.agent = String(item.agent);
    if (item['new']) p.data['new'] = '1';
    if (kind === 'person') return person(p, item, author, me);

    // The first row: where the time sits differs by card, on purpose (1.5, C1).
    if (mine) {
      p.first.time = fullDate(item.published);
      p.first.timeLeads = true;
      // Republished: "· edited <date>" after the date (3.3).
      if (item.edited) p.first.edited = fullDate(item.edited);
      // Made by one of your agents: "· by <agent>", after the date and any
      // edit, naming who made the version shown (2.1, design item 11). The
      // server sends agent and grant together or neither.
      if (item.agent && item.grant) p.first.agent = String(item.agent);
      // The link out lives under the rule, with copy beside it, not here.
      p.first.tools = kind === 'event' ? [] : ['edit'];
    } else {
      p.first.handle = author;
      // The mark and the handle go to that person's site (Vincent, 2026-11-17).
      p.who = item.author_url && /^https?:\/\//.test(item.author_url) ? item.author_url : (author ? 'https://' + author : '');
      p.first.time = relativeTime(item.published, ctx.now);
      if (kind === 'cmt') p.first.verb = 'commented on';
      if (kind === 'event') p.first.verb = VERBS[item.event] || '';
    }
    p.first.timeFull = item.published || '';

    // The lead.
    if (kind === 'post') {
      if (item.title && !item.title_redundant) p.lead = { tag: 'h3', text: item.title };
      else p.untitled = true;
    } else if (kind === 'cmt') {
      // Without the post's title (a comment from the network may not carry
      // it), the post's address stands in for it.
      p.lead = { tag: 'on', title: item.target_post_title || linkText(item.target_url).replace(/^[^/]*\//, '') || '', site: (item.target_post_author_domain || hostOf(item.target_url) || ''), url: item.target_url || '' };
    } else if (kind === 'event') {
      // Who and what happened are the first row's; the lead is what the event
      // points at, when it points past the person (a follow does not). An
      // event of a kind with no verb says its own sentence instead.
      if (!VERBS[item.event]) p.lead = { tag: 'lead', text: item.message || '', url: item.link || '' };
      else if (!/follower$/.test(item.event) && item.link) p.lead = { tag: 'lead', text: linkText(item.link, me), url: item.link };
      // Someone who followed: Follow sits under the row, on its margin (1.5).
      if (item.event === 'new-follower' && author && author !== me) p.cta = { follow: author };
    }

    // The owner's own comment links out to the post it answered (3.3).
    if (kind === 'cmt' && mine && item.target_url) {
      p.out = item.target_url;
      p.outSite = hostOf(item.target_url);
      p.outLabel = 'Open the post on ' + p.outSite;
    }

    // The body: the server's rendered HTML, or the excerpt as text.
    if (kind === 'post') p.body = item.body_html ? { html: item.body_html } : { text: item.excerpt || '' };
    // Another site's post comes with its excerpt only: the card can always be
    // opened, and opening it reads the whole post (wire's opts.whole).
    if (kind === 'post' && !mine && !item.body_html && item.url) p.data.whole = 'fetch';
    if (kind === 'cmt') p.body = item.comment_body_html ? { html: item.comment_body_html } : { text: item.excerpt || '' };

    // The footer, in its fixed order: the counts on the left; on the right
    // copy the link, open it, reply. ("show less" sits above the rule.) Copy and open are on the
    // owner's own items too. An event has none.
    if (kind !== 'event') {
      var n = Number(item.comment_count) || 0;
      if (kind === 'post') p.footer.push({ k: 'comments', n: n, label: plural(n, 'comment') });
      if (mine && kind === 'post' && item.waiting) p.footer.push({ k: 'waiting', n: Number(item.waiting), label: item.waiting + ' waiting' });
      p.footer.push({ k: 'sp' }, { k: 'copy' }, { k: 'out' });
      if (!mine && kind === 'post') p.footer.push({ k: 'reply', label: 'reply' });
    }
    return p;
  }

  // person is the card of someone found by search (2.2): their handle, how
  // the two stand, their latest post as the lead, and Follow under it.
  // It has no footer (1.5).
  var STANDING = { mutual: 'you follow each other', 'follows-you': 'follows you' };
  function person(p, item, author, me) {
    var you = !!me && author === me;
    p.mine = false;
    p.first.handle = author;
    p.who = you ? '' : (item.author_url || (author ? 'https://' + author : ''));
    p.first.verb = you ? 'that\u2019s you' : (STANDING[item.relationship] || '');
    p.out = item.author_url || (author ? 'https://' + author : '');
    p.outSite = author;
    var rp = item.recent_post;
    if (rp && rp.title && rp.url) p.lead = { tag: 'lead', text: rp.title, url: rp.url };
    if (!you && author) p.cta = { follow: author };
    p.first.tools = ['out'];
    return p;
  }

  var OUT = '<svg viewBox="0 0 24 24" aria-hidden="true"><path d="M14 4h6v6M20 4l-9 9M18 14v5a1 1 0 0 1-1 1H5a1 1 0 0 1-1-1V7a1 1 0 0 1 1-1h5"/></svg>';
  var COPY = '<svg viewBox="0 0 24 24" aria-hidden="true"><rect x="8" y="8" width="12" height="12" rx="2"/><path d="M16 8V5a1 1 0 0 0-1-1H5a1 1 0 0 0-1 1v10a1 1 0 0 0 1 1h3"/></svg>';
  var CMT = '<svg viewBox="0 0 24 24" aria-hidden="true"><path d="M5 5h14v10H9l-4 4z"/></svg>';

  function outLink(p, cls) {
    var name = p.outLabel || 'Open on ' + p.outSite;
    return '<a class="' + cls + '" href="' + esc(p.out) + '" target="_blank" rel="noopener" aria-label="' + esc(name) + '" title="' + esc(name) + '">' + OUT + '</a>';
  }

  function leadHTML(p) {
    var l = p.lead;
    if (!l) return '';
    if (l.tag === 'h3') return '<h3>' + esc(l.text) + '</h3>';
    if (l.tag === 'on') {
      return '<p class="on">replied to <q>' + (l.url ? '<a class="pq" href="' + esc(l.url) + '" target="_blank" rel="noopener">' + esc(l.title) + '</a>' : esc(l.title)) + '</q>' +
        (l.site ? ' <span class="site">on ' + esc(l.site) + '</span>' : '') + '</p>';
    }
    // A place in the owner's app opens in place; anything else in a new tab.
    var away = /^https?:\/\//.test(l.url || '') ? ' target="_blank" rel="noopener"' : '';
    return '<div class="what">' + (l.url ? '<a href="' + esc(l.url) + '"' + away + '>' + esc(l.text) + '</a>' : esc(l.text)) + '</div>';
  }

  function pinnedLead(p) {
    var l = p.lead;
    if (!l) return '';
    if (l.tag === 'on') return '<small>replied to</small> “' + esc(l.title) + '”' + (l.site ? ' <small>on ' + esc(l.site) + '</small>' : '');
    return esc(l.text);
  }

  // html draws a card from its parts. markHTML(who) gives the mark (the app
  // knows each site's avatar). Every value from the item is escaped; a body
  // is the server's own rendering.
  function html(p, markHTML) {
    var mark = markHTML ? markHTML(p.mark.who, p.mine) : '<span class="av" aria-hidden="true"></span>';
    var data = Object.keys(p.data).map(function (k) { return ' data-' + k + '="' + esc(p.data[k]) + '"'; }).join('');
    var f = p.first;
    var time = f.time ? '<time class="hdt" datetime="' + esc(f.timeFull) + '">' + esc(f.time) + '</time>' : '';
    // Someone else's mark and handle are links to their site, in this window.
    var who = p.who ? ' href="' + esc(p.who) + '"' : '';
    var first = (f.newDot ? '<span class="new-dot" aria-label="new"></span>' : '') +
      (who ? '<a class="who-mark"' + who + ' tabindex="-1" aria-hidden="true">' + mark + '</a>' : mark) +
      (f.timeLeads ? time : '') +
      (f.edited ? '<span class="edited">\u00b7 edited ' + esc(f.edited) + '</span>' : '') +
      (f.agent ? '<a class="by-agent" href="/_/enhance/agents#connections" title="' + esc(f.agent) +
        ', an agent you connected, made this. See its connection in enhance \u203a Agents.">\u00b7 by ' + esc(f.agent) + '</a>' : '') +
      (f.handle ? (who ? '<a class="hh"' + who + '>' + esc(f.handle) + '</a>' : '<b class="hh">' + esc(f.handle) + '</b>') : '') +
      (f.verb ? '<span class="vb">' + esc(f.verb) + '</span>' : '') +
      '<span class="sp"></span>' +
      (!f.timeLeads ? time : '') +
      (f.tools.length ? '<span class="tools">' + f.tools.map(function (t) {
        return t === 'edit' ? '<button class="link edit" type="button">edit</button>' : outLink(p, 'out');
      }).join('') + '</span>' : '');
    var pinned = '<div class="stick" aria-hidden="true"><div class="srow"><div class="sin">' + mark +
      (f.handle ? '<span class="shandle">' + esc(f.handle) + '</span>' : '<span class="sdate">' + esc(f.time) + '</span>') + '<span class="sttl">' + pinnedLead(p) + '</span>' +
      // No link out here: it is under the card's rule.
      '<span class="stools">' + (f.tools.indexOf('edit') >= 0 ? '<button class="link edit" type="button" tabindex="-1">edit</button>' : '') +
      '</span></div></div></div>';
    var body = p.body ? '<div class="ex">' + (p.body.html !== undefined ? p.body.html : '<p>' + esc(p.body.text) + '</p>') + '</div>' : '';
    var foot = p.footer.length ? '<div class="ifoot">' + p.footer.map(function (x) {
      switch (x.k) {
        case 'out': return outLink(p, 'out');
        case 'comments': return '<button class="n cm-n" type="button">' + CMT + '<b>' + x.n + '</b> ' + esc(x.label.replace(/^\d+ /, '')) + '</button>';
        case 'waiting': return '<button class="n wait has" type="button"><b>' + x.n + '</b> waiting</button>';
        case 'reply': return '<button class="link reply" type="button">reply</button>';
        case 'sp': return '<span class="sp"></span>';
        case 'copy': return '<button class="copy" type="button" data-url="' + esc(p.out) + '" aria-label="Copy the link" title="Copy the link">' + COPY + '</button>';
      }
      return '';
    }).join('') + '</div>' : '';
    // "show less": above the rule, at the right, only while the card is open.
    var less = p.footer.length ? '<button class="fold" type="button"></button>' : '';
    var thread = p.kind === 'post' ? '<div class="thread"><div><div class="thread-in"></div></div></div>' : '';
    // A Follow button sits under the words, on their margin, never in a
    // footer (1.5); the section draws whether the owner follows them.
    var cta = p.cta && p.cta.follow ? '<div class="cta"><button class="slim follow" type="button" data-site="' + esc(p.cta.follow) + '">+ Follow</button></div>' : '';
    return '<article class="item ' + p.kind + (p.untitled ? ' untitled' : '') + '"' + data + ' tabindex="0">' +
      pinned + '<div class="hd">' + first + '</div>' + leadHTML(p) + body + less + cta + foot + thread + '</article>';
  }

  // ── Behaviour: opening, folding, the pinned row (1.5) ──

  // A click on a card opens it; on an open card's first row, pinned row or
  // body it folds. A click inside the thread, a form, a link, a button or
  // anywhere under the rule (the footer's icons sit close: a near miss must
  // do nothing) leaves the card be.
  function clickFolds(target, open) {
    if (target.closest('a, button, input, textarea, select, .thread, form, .ifoot')) return null;
    if (!open) return 'open';
    if (target.closest('.hd, .srow, .ex')) return 'fold';
    return null;
  }

  // copyLink puts a card's address on the clipboard and says so for a moment.
  function copyLink(b) {
    try { navigator.clipboard.writeText(b.getAttribute('data-url')).catch(function () {}); } catch (x) { /* no clipboard here */ }
    b.classList.add('done');
    b.title = 'Copied';
    clearTimeout(b.doneT);
    b.doneT = setTimeout(function () { b.classList.remove('done'); b.title = 'Copy the link'; }, 1400);
  }

  function wire(list, opts) {
    opts = opts || {};
    if (opts.whole) list._whole = opts.whole;
    var bar = opts.bar || 64;
    list.addEventListener('click', function (e) {
      var card = e.target.closest('.item');
      if (!card || !list.contains(card)) return;
      var srow = e.target.closest('.srow');
      if (srow && !e.target.closest('a, button')) { fold(card, true); return; }
      var cp = e.target.closest('.ifoot .copy');
      if (cp) { copyLink(cp); return; }
      var foldBtn = e.target.closest('.fold');
      if (foldBtn) { fold(card, true); return; }
      var cm = e.target.closest('.cm-n, .wait');
      if (cm) { toggleThread(card, opts); return; }
      // Selecting words is not a click on the card.
      if (root.getSelection && String(root.getSelection())) return;
      var act = clickFolds(e.target, card.classList.contains('open'));
      // A body that already shows in full has nothing to open (no "show less").
      if (act === 'open' && !fitsWhole(card)) open(card);
      if (act === 'fold') fold(card, false);
    });
    list.addEventListener('keydown', function (e) {
      if ((e.key === 'Enter' || e.key === ' ') && e.target.classList && e.target.classList.contains('item')) {
        e.preventDefault();
        if (e.target.classList.contains('open')) fold(e.target, false); else if (!fitsWhole(e.target)) open(e.target);
      }
    });
    // The pinned row: an open card pins once its heading has passed under the menu.
    function pin() {
      list.querySelectorAll('.item.open').forEach(function (card) {
        var head = card.querySelector('h3, .on, .what') || card.querySelector('.hd');
        var r = card.getBoundingClientRect(), h = head.getBoundingClientRect();
        card.classList.toggle('stuck', h.bottom < bar && r.bottom > bar + 60);
      });
    }
    root.addEventListener('scroll', pin, { passive: true });
    fits(list);
    // The web fonts change how much a body holds: measure again once they're in.
    if (root.document && root.document.fonts && root.document.fonts.ready) root.document.fonts.ready.then(function () { fits(list); });
  }

  // Opening lets the body grow to its full height and then go free; folding
  // eases it back to its opening lines (the mockups' motion).
  function open(card) {
    if (card.classList.contains('open')) return;
    card.classList.add('open');
    var ex = card.querySelector('.ex');
    if (ex) {
      ex.style.maxHeight = ex.scrollHeight + 'px';
      ex.addEventListener('transitionend', function done() {
        if (card.classList.contains('open')) ex.style.maxHeight = 'none';
        ex.removeEventListener('transitionend', done);
      });
    }
    var f = card.querySelector('.fold');
    if (f) f.textContent = 'show less';
    if (card.dataset.whole === 'fetch') readWhole(card, ex);
  }

  // readWhole swaps a card's excerpt for the whole post, read through the
  // loader its list was wired with. A post that can't be read keeps its
  // excerpt, and the next opening tries again.
  function readWhole(card, ex) {
    var host = card.parentNode;
    while (host && !host._whole) host = host.parentNode;
    if (!host || !ex) return;
    card.dataset.whole = 'reading';
    Promise.resolve(host._whole(card)).then(function (html) {
      if (!html) { card.dataset.whole = 'fetch'; return; }
      ex.innerHTML = html;
      // The title above already says the post's first line: drop it here.
      var h = card.querySelector('h3'), first = ex.firstElementChild;
      if (h && first && first.tagName === 'P' && first.textContent.trim() === h.textContent.trim()) first.remove();
      delete card.dataset.whole;
      if (card.classList.contains('open') && ex.style.maxHeight && ex.style.maxHeight !== 'none') ex.style.maxHeight = ex.scrollHeight + 'px';
    }, function () { card.dataset.whole = 'fetch'; });
  }
  function fold(card, scroll) {
    var ex = card.querySelector('.ex');
    if (ex && card.classList.contains('open')) {
      ex.style.maxHeight = ex.scrollHeight + 'px';
      root.requestAnimationFrame(function () { ex.style.maxHeight = ''; });
    }
    card.classList.remove('open', 'stuck', 'talk');
    var f = card.querySelector('.fold');
    if (f) f.textContent = '';
    if (scroll) backToTop(card, ex);
  }
  // backToTop brings a card the reader just folded back to its header, just
  // below the fixed bar (the card's scroll-margin-top). The scroll starts at
  // once and is checked again when the body has finished folding, because the
  // page moves under it while the body shrinks.
  function backToTop(card, ex) {
    var still = root.matchMedia && root.matchMedia('(prefers-reduced-motion: reduce)').matches;
    var go = function (how) { card.scrollIntoView({ block: 'start', behavior: how }); };
    go(still ? 'auto' : 'smooth');
    var done = false;
    var settle = function () {
      if (done) return;
      done = true;
      var margin = parseFloat(root.getComputedStyle(card).scrollMarginTop) || 0;
      if (Math.abs(card.getBoundingClientRect().top - margin) > 2) go('auto');
    };
    // The reader taking over (a wheel, a touch, a key) ends it: never pull
    // them back from where they chose to go.
    var stand = function () { done = true; };
    ['wheel', 'touchstart', 'keydown'].forEach(function (t) { root.addEventListener(t, stand, { once: true, passive: true }); });
    if (ex) ex.addEventListener('transitionend', settle, { once: true });
    root.setTimeout(settle, 700);
  }
  function toggleThread(card, opts) {
    // The count opens the comments; the post stays folded (the mockups).
    // The count closes them again: no "hide comments" of its own.
    var on = !card.classList.contains('talk');
    card.classList.toggle('talk', on);
    var cm = card.querySelector('.cm-n');
    if (cm) cm.setAttribute('aria-expanded', on ? 'true' : 'false');
    var inner = card.querySelector('.thread-in');
    if (on && inner && !inner.dataset.loaded) {
      inner.dataset.loaded = '1';
      if (opts.thread) opts.thread(card, inner);
      else inner.innerHTML = '<p class="cm-none">No comments yet.</p>';
    }
  }
  // fitsWhole: a card whose body shows in full while folded.
  function fitsWhole(card) {
    var ex = card.querySelector('.ex');
    return !!ex && ex.classList.contains('fits') && !card.dataset.whole;
  }

  // A collapsed body fades only when there is more of it than shows.
  function fits(list) {
    // ⚠️ A hidden body measures 0 by 0, which would read as fitting: only a
    // body that is laid out counts.
    list.querySelectorAll('.item:not(.open) .ex').forEach(function (ex) { ex.classList.toggle('fits', !ex.parentNode.dataset.whole && ex.clientHeight > 0 && ex.scrollHeight <= ex.clientHeight + 1); });
  }

  var card = {
    parts: parts, html: html, wire: wire, fits: fits, open: open, fold: fold,
    // openThread opens a card's thread (and the card), loading it once.
    openThread: function (card, opts) { if (!card.classList.contains('talk')) toggleThread(card, opts || {}); },
    clickFolds: clickFolds, fullDate: fullDate, relativeTime: relativeTime, esc: esc, linkText: linkText, hostOf: hostOf,
  };
  root.PolisV5Card = card;
  if (typeof module !== 'undefined' && module.exports) module.exports = card;
})(typeof window !== 'undefined' ? window : globalThis);
