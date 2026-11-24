// The v5 owner app's frame (v5 spec part 1, 3.2, 3.9): the menu and the
// section box, the addresses, the dots, hints, help and the footer. Each
// section (gather, write, message, enhance) draws its own page into the pane
// the frame gives it, through PolisV5.section(name, { mount, ... }).
//
// ⭐ The rules are pure functions (PolisV5.model), tested with node; the
// drawing below them is the mockups' behaviour, bound to /v1.
(function (root) {
  'use strict';

  // The menu's order (1.3): gather leads.
  var SECTIONS = ['gather', 'write', 'message', 'enhance'];
  var GROUPS = ['you', 'site', 'people', 'agents', 'data'];

  // ── The rules ────────────────────────────────────────────────────────

  // A sentence travels in an address as /pql/ does: words joined by '+',
  // anything else (an address's '/') encoded.
  function encodeSentence(s) {
    return String(s).trim().split(/\s+/).map(encodeURIComponent).join('+');
  }
  function decodeSentence(s) {
    try { return decodeURIComponent(String(s).replace(/\+/g, ' ')); } catch (e) { return String(s).replace(/\+/g, ' '); }
  }

  // route reads an address under /_/ (3.9): the section, a view's sentence,
  // an enhance group. The home (/_/) is { home: true }. The server has
  // already sent every other address to its place, so anything unknown here
  // is the home too.
  function route(pathname) {
    var p = String(pathname || '').replace(/^\/_\/?/, '').replace(/\/+$/, '');
    if (!p) return { home: true };
    var parts = p.split('/');
    if (SECTIONS.indexOf(parts[0]) < 0) return { home: true };
    var r = { section: parts[0] };
    if (parts[1] === 'pql' && parts[2]) r.sentence = decodeSentence(parts.slice(2).join('/'));
    if (parts[0] === 'enhance' && GROUPS.indexOf(parts[1]) >= 0) r.group = parts[1];
    return r;
  }

  // address is where a section, or a view of it, lives (3.9). A filter the
  // owner changes is a moment, so the frame goes back to the section's own
  // address then (sentence omitted).
  function address(section, opts) {
    opts = opts || {};
    if (opts.sentence) return '/_/' + section + '/pql/' + encodeSentence(opts.sentence);
    if (section === 'enhance' && opts.group) return '/_/enhance/' + opts.group;
    return '/_/' + section;
  }

  // lastSection is where /_/ opens: the section with the latest seen time
  // (every section records the owner leaving it), else gather (3.9).
  function lastSection(seen) {
    var best = '', at = '';
    SECTIONS.forEach(function (s) {
      var t = seen && seen[s];
      if (t && (!at || Date.parse(t) > Date.parse(at))) { best = s; at = t; }
    });
    return best || 'gather';
  }

  // legacyHash is where an old notification link's fragment goes (3.9's
  // legacy table): a fragment never reaches the server.
  function legacyHash(hash) {
    switch (hash) {
      case '#blessings': return '/_/write/pql/all+comments+from+all+polis+to+bless';
      case '#followers': return '/_/gather/pql/all+activity+about+me';
    }
    return '';
  }

  // menuOrder is the menu with the current section first (it moves into the
  // box), the rest in their order, then help (1.3).
  function menuOrder(current) {
    var rest = SECTIONS.filter(function (s) { return s !== current; });
    return (SECTIONS.indexOf(current) >= 0 ? [current] : []).concat(rest, ['help']);
  }

  // dotSentences are what a section's menu dot asks: has anything arrived
  // since the owner last left that section (1.6)? Each is asked with
  // new_for=<section>&only_new=1, so it reads and clears nothing.
  // ⚠️ Messages are asked as message's own list asks them (my mutuals): the
  // site refuses any other scope for messages, with a 400.
  var DOT_SENTENCES = {
    write: ['all comments from all polis to bless'],
    gather: ['all activity about me', 'all posts from my network'],
    message: ['all messages from my mutuals'],
    enhance: [],
  };

  // DOT_EVERY is how often the dots are asked again while the app is open.
  // ⭐ The asking is also what keeps the owner's network current: the site's
  // own 30-second sync runs only while a tab holds the live connection
  // (/api/sse), which this app does not open, and a read of the owner's
  // network asks for a sync once it is two minutes old (syncIfStale).
  var DOT_EVERY = 60 * 1000;

  // inAppLink is where a click on a link goes when it is a place in this app
  // (/_/…) to open in place, or null to leave the browser to it: a new tab
  // or window asked for, a download, or anywhere else.
  function inAppLink(href, opts) {
    opts = opts || {};
    if (opts.modified || (opts.target && opts.target !== '_self') || opts.download) return null;
    if (!/^\/_(\/|$)/.test(String(href || ''))) return null;
    var hash = String(href).indexOf('#') >= 0 ? String(href).slice(String(href).indexOf('#')) : '';
    return { route: route(String(href).split('#')[0]), href: String(href), hash: hash };
  }

  // A colour from the site's own data is used only as a colour: a hex
  // colour, or an hsl() of plain numbers (the colour the site gives someone
  // who chose no avatar of their own).
  var HEX = /^#[0-9a-f]{3,8}$/i;
  var HSL = /^hsl\(\s*\d{1,3}(\.\d+)?\s*,\s*\d{1,3}(\.\d+)?%\s*,\s*\d{1,3}(\.\d+)?%\s*\)$/i;
  function colour(c) { return typeof c === 'string' && (HEX.test(c) || HSL.test(c)) ? c : ''; }

  var PATTERNS = {
    rings: function (c) { return "<svg xmlns='http://www.w3.org/2000/svg' width='28' height='28'><circle cx='14' cy='14' r='10' fill='none' stroke='" + c + "' stroke-width='1.5'/><circle cx='14' cy='14' r='5' fill='none' stroke='" + c + "' stroke-width='1'/></svg>"; },
    cross: function (c) { return "<svg xmlns='http://www.w3.org/2000/svg' width='28' height='28'><line x1='4' y1='4' x2='24' y2='24' stroke='" + c + "' stroke-width='1.5'/><line x1='24' y1='4' x2='4' y2='24' stroke='" + c + "' stroke-width='1.5'/></svg>"; },
    grid: function (c) { return "<svg xmlns='http://www.w3.org/2000/svg' width='28' height='28'><line x1='9' y1='0' x2='9' y2='28' stroke='" + c + "' stroke-width='0.8'/><line x1='19' y1='0' x2='19' y2='28' stroke='" + c + "' stroke-width='0.8'/><line x1='0' y1='9' x2='28' y2='9' stroke='" + c + "' stroke-width='0.8'/><line x1='0' y1='19' x2='28' y2='19' stroke='" + c + "' stroke-width='0.8'/></svg>"; },
    dots: function (c) { return "<svg xmlns='http://www.w3.org/2000/svg' width='28' height='28'><circle cx='7' cy='7' r='2' fill='" + c + "'/><circle cx='21' cy='7' r='2' fill='" + c + "'/><circle cx='14' cy='14' r='2' fill='" + c + "'/><circle cx='7' cy='21' r='2' fill='" + c + "'/><circle cx='21' cy='21' r='2' fill='" + c + "'/></svg>"; },
    stripes: function (c) { return "<svg xmlns='http://www.w3.org/2000/svg' width='28' height='28'><line x1='-2' y1='6' x2='6' y2='-2' stroke='" + c + "' stroke-width='1.5'/><line x1='5' y1='13' x2='13' y2='5' stroke='" + c + "' stroke-width='1.5'/><line x1='12' y1='20' x2='20' y2='12' stroke='" + c + "' stroke-width='1.5'/><line x1='19' y1='27' x2='27' y2='19' stroke='" + c + "' stroke-width='1.5'/><line x1='26' y1='34' x2='34' y2='26' stroke='" + c + "' stroke-width='1.5'/></svg>"; },
    diamond: function (c) { return "<svg xmlns='http://www.w3.org/2000/svg' width='28' height='28'><polygon points='14,4 24,14 14,24 4,14' fill='none' stroke='" + c + "' stroke-width='1.5'/></svg>"; },
    halves: function (c) { return "<svg xmlns='http://www.w3.org/2000/svg' width='28' height='28'><rect x='0' y='14' width='28' height='14' fill='" + c + "' opacity='0.4'/></svg>"; },
  };

  // avatarStyle is a mark's inline style from a site's avatar (the same
  // patterns the visitor's mark uses), or '' to keep the default. ⛔ Every
  // colour is checked to be a colour: an agent with the profile scope can
  // set the avatar, and this goes into a style attribute.
  function avatarStyle(av, b64) {
    if (!av || !colour(av.bg)) return '';
    var s = 'background:' + av.bg + ';color:' + (colour(av.fg) || '#fff') + ';';
    var w = Number(av.border_w);
    if (colour(av.border) && w > 0 && w <= 6) s += 'box-shadow:0 0 0 ' + w + 'px ' + av.border + ';';
    var pc = colour(av.pattern_color);
    if (pc && PATTERNS[av.pattern]) s += 'background-image:url(data:image/svg+xml;base64,' + b64(PATTERNS[av.pattern](pc)) + ');background-size:cover;';
    return s;
  }

  // initial is a mark's letter: the first letter of the site's name.
  function initial(name) {
    var m = String(name || '').match(/[A-Za-z0-9]/);
    return m ? m[0].toLowerCase() : '·';
  }

  // menuClick is what a click on a menu item does (1.13): 'help' toggles
  // help; another section is a switch ('go'); the section the owner is in,
  // with its dot lit, takes them to what arrived ('again'); the section they
  // are in otherwise goes back to its top ('top', as v4 did), except while a
  // sheet is open, whose writing keeps its place ('stay').
  function menuClick(way, current, hasNew, sheetOpen) {
    if (way === 'help') return 'help';
    if (way !== current) return 'go';
    if (hasNew) return 'again';
    return sheetOpen ? 'stay' : 'top';
  }

  // footOperatorLinks: whether the footer links to polis.pub's terms and
  // privacy. Only on polis.pub (the instance with an operator); polis.pub is
  // not the operator of a laptop, a self-hosted or a remote instance.
  function footOperatorLinks(caps) { return !!(caps && caps.hasOperator); }

  var model = {
    footOperatorLinks: footOperatorLinks,
    SECTIONS: SECTIONS, GROUPS: GROUPS, DOT_SENTENCES: DOT_SENTENCES,
    encodeSentence: encodeSentence, decodeSentence: decodeSentence, route: route, address: address,
    lastSection: lastSection, legacyHash: legacyHash, menuOrder: menuOrder, inAppLink: inAppLink, DOT_EVERY: DOT_EVERY,
    avatarStyle: avatarStyle, initial: initial, menuClick: menuClick,
  };

  // ── The drawing ──────────────────────────────────────────────────────

  var ICONS = {
    write: '<path d="M4 20h4L19 9l-4-4L4 16z"/><path d="M13.5 6.5l4 4"/>',
    gather: '<path d="M5 20 C 5 1, 19 1, 19 20"/>',
    message: '<rect x="3" y="5" width="18" height="14" rx="2"/><path d="M3 7l9 6 9-6"/>',
    enhance: '<path d="M12 3l1.8 5.2L19 10l-5.2 1.8L12 17l-1.8-5.2L5 10l5.2-1.8z"/><path d="M19 16l.7 2 2 .7-2 .7-.7 2-.7-2-2-.7 2-.7z"/>',
    help: '<circle cx="12" cy="12" r="8.5"/><path d="M9.6 9.5a2.5 2.5 0 0 1 4.9.7c0 1.8-2.5 2-2.5 3.7"/><path d="M12 16.8v.2"/>',
  };

  var sections = {};     // name → { mount(pane, ctx), toolbar(el, ctx), leave() }
  var state = { section: '', arrived: '', site: null, seen: {} };

  function boot() {
    var api = root.PolisAPI, doc = root.document, $ = function (id) { return doc.getElementById(id); };

    // An old notification link's fragment goes to its place first.
    var fromHash = legacyHash(root.location.hash);
    if (fromHash) root.history.replaceState(null, '', fromHash);

    // ── hints (1.16): one at a time, gone after 2.8s or the next click ──
    var hintTimer = 0;
    function hint(text) {
      var el = $('hint');
      el.textContent = text;
      el.classList.add('show');
      clearTimeout(hintTimer);
      hintTimer = setTimeout(function () { el.classList.remove('show'); }, 2800);
    }
    doc.addEventListener('click', function () { if ($('hint').classList.contains('show')) { clearTimeout(hintTimer); hintTimer = setTimeout(function () { $('hint').classList.remove('show'); }, 0); } }, true);

    // ── the menu ──
    var ways = $('ways');
    menuOrder('').forEach(function (k) {
      var b = doc.createElement('button');
      b.type = 'button';
      b.className = 'way';
      b.dataset.way = k;
      b.title = k;
      if (k === 'help') { b.id = 'help'; b.setAttribute('aria-controls', 'fly'); b.setAttribute('aria-expanded', 'false'); }
      b.innerHTML = '<svg viewBox="0 0 24 24" aria-hidden="true">' + ICONS[k] + '</svg>' + (k === 'help' ? '' : '<span class="dot"></span>') + '<span class="lbl">' + k + '</span>';
      ways.appendChild(b);
    });

    // ── the panes and the toolbars, one per section ──
    var col = $('col'), center = $('center');
    SECTIONS.forEach(function (k) {
      var pane = doc.createElement('section');
      pane.className = 'pane';
      pane.dataset.pane = k;
      pane.setAttribute('aria-label', k);
      col.appendChild(pane);
      var tbar = doc.createElement('div');
      tbar.className = 'tbar';
      tbar.dataset.for = k;
      center.appendChild(tbar);
    });

    // ── the box (1.13), as the mockups place it ──
    function contentLeft() { var c = col; return c.getBoundingClientRect().left + parseFloat(root.getComputedStyle(c).paddingLeft); }
    // On a narrow screen the content's edge is under your mark, so the box starts clear of it.
    function boxLeft() { var me = $('me'); return Math.max(contentLeft(), me ? me.getBoundingClientRect().right + 12 : 0); }
    function boxRight() { return col.getBoundingClientRect().right - parseFloat(root.getComputedStyle(col).paddingRight); }
    function placeBox(animate) {
      var box = $('box'), on = doc.querySelector('.way.on');
      if (!on) return;
      var L = boxLeft(), right = boxRight();
      box.classList.toggle('still', !animate);
      box.style.left = L + 'px';
      box.style.width = (right - L) + 'px';
      fitWays(L, right);
    }
    // Fits the menu beside the toolbar: labels go first, then the lit item's, then the filters.
    // Runs again whenever the toolbar changes width, since its counts arrive after the first fit.
    function fitWays(L, right) {
      var on = doc.querySelector('.way.on');
      if (!on) return;
      var vw = doc.documentElement.clientWidth;
      center.style.left = (L + 14) + 'px';
      ways.classList.remove('tight', 'inside', 'cramped');
      doc.body.classList.remove('squeezed');
      ways.style.left = (right - 5 - on.offsetWidth) + 'px';
      if (ways.getBoundingClientRect().right > vw - 16) ways.classList.add('tight');
      if (ways.getBoundingClientRect().right > vw - 16) {
        ways.classList.add('inside');
        ways.style.left = (right - 5 - ways.offsetWidth) + 'px';
        if (L + 14 + center.offsetWidth > right - 5 - ways.offsetWidth - 8) {
          ways.classList.add('cramped');
          ways.style.left = (right - 5 - ways.offsetWidth) + 'px';
          if (L + 14 + center.offsetWidth > right - 5 - ways.offsetWidth - 8) doc.body.classList.add('squeezed');
        }
      }
    }

    // ── leaving a section marks it seen (1.6): switch, navigate, close ──
    function markLeft(section, keepalive) {
      if (!section || !state.arrived) return;
      api.post('/v1/seen', { section: section, up_to: state.arrived }, { keepalive: keepalive });
      state.seen[section] = state.arrived;
    }
    root.addEventListener('pagehide', function () { markLeft(state.section, true); });

    // ── the menu dots: something arrived since the owner left that section,
    // or, for the section the owner is in, since it drew what it shows ──
    var dotsBusy = false;
    async function dots() {
      if (dotsBusy) return;
      dotsBusy = true;
      try {
        for (var i = 0; i < SECTIONS.length; i++) {
          var k = SECTIONS[i], has = false;
          if (k === state.section) {
            // The section asks itself: everything it already shows counts as new
            // against the seen time, so only something beyond that lights it.
            // It puts what arrived in place, and clears the dot itself once the
            // owner has seen it (ctx.dot).
            var s = sections[k];
            if (s && s.fresher && await s.fresher() && k === state.section) setDot(k, true);
            continue;
          }
          var asks = DOT_SENTENCES[k];
          for (var j = 0; j < asks.length && !has; j++) {
            var r = await api.query(asks[j], { new_for: k, only_new: 1, limit: 1 });
            has = r.ok && r.items.length > 0;
            if (api.isSignedOut()) return;
          }
          // A section the owner moved into while this was asked keeps no dot.
          setDot(k, has && k !== state.section);
        }
      } finally { dotsBusy = false; }
    }
    // ⛔ Signed out, the dots stop asking: an open tab would otherwise ask
    // every minute all night, each answer a refused request. Coming back to
    // the tab asks once, and a success (signed in again elsewhere) restarts
    // them.
    var dotTimer = root.setInterval(dots, DOT_EVERY);
    api.onSignedOut(function () {
      root.clearInterval(dotTimer);
      dotTimer = 0;
      hint(api.errorText(401, null));
    });
    doc.addEventListener('visibilitychange', async function () {
      if (doc.hidden) return;
      await dots();
      if (!dotTimer && !api.isSignedOut()) dotTimer = root.setInterval(dots, DOT_EVERY);
    });
    function setDot(k, on) {
      var b = ways.querySelector('[data-way="' + k + '"]');
      if (b) b.classList.toggle('has-new', on);
    }

    // ── switching section (1.13): the menu slides and the box stretches ──
    function go(sec, opts) {
      opts = opts || {};
      if (SECTIONS.indexOf(sec) < 0) sec = 'gather';
      var leaving = state.section;
      if (leaving === sec && !opts.force) return;
      if (leaving && sections[leaving] && sections[leaving].leave) {
        if (sections[leaving].leave() === false) return; // the section kept the owner (unsaved writing)
      }
      if (leaving && leaving !== sec) markLeft(leaving, false);
      var all = Array.prototype.slice.call(ways.querySelectorAll('.way'));
      var first = new Map(all.map(function (w) { return [w, w.getBoundingClientRect()]; }));
      menuOrder(sec).forEach(function (k) { ways.appendChild(all.find(function (w) { return w.dataset.way === k; })); });
      all.forEach(function (w) { w.classList.toggle('on', w.dataset.way === sec); w.setAttribute('aria-current', w.dataset.way === sec ? 'page' : 'false'); });
      doc.body.dataset.sec = sec;
      state.section = sec;
      state.arrived = new Date().toISOString();
      setDot(sec, false);
      placeBox(!!leaving);
      if (leaving) {
        all.forEach(function (w) {
          var a = first.get(w), b = w.getBoundingClientRect(), dx = a.left - b.left;
          if (dx && w.animate) w.animate([{ transform: 'translateX(' + dx + 'px)' }, { transform: 'none' }], { duration: 480, easing: 'cubic-bezier(.22,.8,.24,1)' });
        });
      }
      if (opts.push !== false) {
        var target = address(sec, opts);
        if (root.location.pathname !== target) root.history.pushState(null, '', target);
      }
      var s = sections[sec], pane = col.querySelector('[data-pane="' + sec + '"]');
      if (s && s.mount && !pane.dataset.mounted) { pane.dataset.mounted = '1'; s.mount(pane, ctx); }
      if (s && s.show) s.show(opts, ctx);
      fitWays(boxLeft(), boxRight());   // the section's toolbar exists only now; fitted before it, a phone kept its filters over the menu
      if (!s && !pane.dataset.mounted) {
        pane.dataset.mounted = '1';
        pane.innerHTML = '<p class="soon"></p>';
        pane.firstChild.textContent = sec + ' is coming in its own pass.';
      }
      doc.title = sec + ' · ' + ((state.site && hostOf(state.site.base_url)) || 'polis');
      root.scrollTo({ top: 0 });
      closeHelp();
    }
    function hostOf(u) { try { return new URL(u).host; } catch (e) { return ''; } }

    ways.addEventListener('click', function (e) {
      var b = e.target.closest('.way');
      if (!b) return;
      var does = menuClick(b.dataset.way, state.section, b.classList.contains('has-new'), doc.body.classList.contains('focus'));
      if (does === 'help') { e.stopPropagation(); if (!closeHelp()) openHelp(); return; }
      if (does === 'stay') return;
      if (does === 'top') {
        var still = root.matchMedia && root.matchMedia('(prefers-reduced-motion: reduce)').matches;
        root.scrollTo({ top: 0, behavior: still ? 'auto' : 'smooth' });
        return;
      }
      // The section the owner is in, with its dot lit: take the owner to what
      // arrived (a section that put it in place), or draw it again.
      if (does === 'again' && sections[state.section] && sections[state.section].again) { sections[state.section].again(); return; }
      go(b.dataset.way, { force: does === 'again' });
    });

    // ── a place in the app opens in place (3.9), from any section ──
    // ⛔ Never a reload: the messages key lives in this tab's memory and a
    // page load (or a pagehide) wipes it, so a link that reloaded the app
    // locked the owner's messages again.
    doc.addEventListener('click', function (e) {
      if (e.defaultPrevented || e.button !== 0) return;
      var a = e.target.closest && e.target.closest('a[href]');
      if (!a) return;
      var to = inAppLink(a.getAttribute('href'), {
        modified: e.metaKey || e.ctrlKey || e.shiftKey || e.altKey,
        target: a.getAttribute('target'), download: a.hasAttribute('download'),
      });
      if (!to) return;
      e.preventDefault();
      if (root.location.pathname + root.location.hash !== to.href) root.history.pushState(null, '', to.href);
      var r = to.route;
      go(r.home ? lastSection(state.seen) : r.section, { push: false, sentence: r.sentence, group: r.group, force: true });
    });
    root.addEventListener('popstate', function () {
      var r = route(root.location.pathname);
      go(r.home ? lastSection(state.seen) : r.section, { push: false, sentence: r.sentence, group: r.group, force: true });
    });
    root.addEventListener('resize', function () { placeBox(false); });
    if (root.ResizeObserver) {
      var refit = 0;
      new root.ResizeObserver(function () {
        if (refit) return;
        refit = root.requestAnimationFrame(function () { refit = 0; fitWays(boxLeft(), boxRight()); });
      }).observe(center);
      // ⭐ The column moves without the window resizing: a classic scrollbar
      // appears once the list is taller than the window and narrows the page,
      // which re-centres the column, and no resize event fires. Placed before
      // that, the box sat beside the cards and the menu crowded it until a zoom
      // change re-placed it. The page's root element narrows when the scrollbar
      // appears (the column, centred at a fixed width, may only move), so
      // watching both re-places the box the moment either changes.
      var replace = 0;
      var reflow = new root.ResizeObserver(function () {
        if (replace) return;
        replace = root.requestAnimationFrame(function () { replace = 0; placeBox(false); });
      });
      reflow.observe(doc.documentElement);
      reflow.observe(col);
    }
    if (doc.fonts && doc.fonts.ready) doc.fonts.ready.then(function () { placeBox(false); });

    // ── help: kay's flyout (1.10) ──
    var fly = $('fly');
    fly.innerHTML = '<div class="fly-head"><div class="fly-who"><span class="fly-mark" aria-hidden="true">k</span><div><div class="fly-name">kay</div><div class="fly-sub">your guide on polis</div></div></div>' +
      '<button class="fly-x" id="fly-x" type="button" aria-label="Close">&times;</button></div><div class="fly-body" id="fly-body" aria-live="polite"></div>';
    fly.hidden = false;
    fly.setAttribute('aria-hidden', 'true');
    // key is which help to show: the current section's, or 'polis' (the
    // footer's "how it works": kay's orientation to polis itself).
    function paintHelp(key) {
      var h = (root.PolisV5Help || {})[key || state.section];
      var body = $('fly-body');
      if (!h) { body.innerHTML = ''; return; }
      body.innerHTML = '<p class="k lead">' + h.lead + '</p>' +
        '<div class="els">' + h.els.map(function (e) { return '<div class="el"><div class="en">' + e[0] + '</div><div class="ed">' + e[1] + '</div></div>'; }).join('') + '</div>' +
        (h['else'] ? '<div class="else"><div class="lbl">ELSEWHERE</div>' + h['else'] + '</div>' : '');
      if (h.topics && h.topics.length) {
        var box = doc.createElement('div');
        box.className = 'asks';
        box.innerHTML = '<div class="lbl">Related topics</div>';
        h.topics.forEach(function (t) {
          var b = doc.createElement('button');
          b.type = 'button';
          b.className = 'ask';
          b.innerHTML = t[0];
          b.onclick = function () {
            var p = doc.createElement('p');
            p.className = 'k';
            p.innerHTML = '<em>' + t[0] + '</em> ' + t[1];
            box.parentNode.insertBefore(p, box);
            b.remove();
            if (!box.querySelector('.ask')) box.remove();
          };
          box.appendChild(b);
        });
        body.appendChild(box);
      }
    }
    function openHelp(key) { paintHelp(key); doc.body.classList.add('flying'); fly.setAttribute('aria-hidden', 'false'); $('help').setAttribute('aria-expanded', 'true'); }
    function closeHelp() {
      if (!doc.body.classList.contains('flying')) return false;
      doc.body.classList.remove('flying');
      fly.setAttribute('aria-hidden', 'true');
      $('help').setAttribute('aria-expanded', 'false');
      return true;
    }
    $('fly-x').onclick = closeHelp;
    fly.addEventListener('click', function (e) {
      var a = e.target.closest('a[href^="/_/"]');
      if (!a) return;
      e.preventDefault();
      var r = route(a.getAttribute('href'));
      go(r.section || 'gather', { group: r.group, sentence: r.sentence });
    });
    doc.addEventListener('click', function (e) { if (doc.body.classList.contains('flying') && !e.target.closest('#fly, #help')) closeHelp(); }, true);
    doc.addEventListener('keydown', function (e) { if (e.key === 'Escape' && closeHelp()) e.stopImmediatePropagation(); }, true);

    // ── the footer (3.2, decided 2026-11-23): invite (still to be built), how it
    // works (kay's orientation, in the help flyout), and on polis.pub only its
    // terms and privacy: polis.pub is not the operator of any other instance ──
    var foot = $('foot');
    foot.innerHTML = '<button class="invite" id="invite" type="button"><svg viewBox="0 0 16 16" aria-hidden="true"><path d="M8 2.5v11M2.5 8h11"/></svg>invite</button>' +
      '<button class="how" id="how" type="button" aria-controls="fly">how it works</button>';
    $('invite').onclick = function () { hint('Inviting people is coming back in its own pass.'); };
    $('how').onclick = function (e) { e.stopPropagation(); openHelp('polis'); };
    api.get('/v1/openapi.json').then(function (r) {
      var caps = r.ok && r.data ? r.data['x-polis-capabilities'] || {} : {};
      if (!model.footOperatorLinks(caps)) return;
      foot.insertAdjacentHTML('beforeend', '<a href="https://polis.pub/legal/terms" target="_blank" rel="noopener">terms</a>' +
        '<a href="https://polis.pub/legal/privacy" target="_blank" rel="noopener">privacy</a>');
    });

    // ── your mark (3.2): your avatar; it opens enhance's You group ──
    var me = $('me');
    me.onclick = function () { go('enhance', { group: 'you' }); };

    var ctx = { api: api, hint: hint, go: go, placeBox: placeBox, state: state, model: model, dot: setDot };

    // ── the first draw ──
    (async function () {
      var site = await api.get('/v1/site');
      if (site.ok) {
        state.site = site.data;
        var host = hostOf(site.data.base_url);
        me.setAttribute('aria-label', host);
        // Your address beside your mark; it and "view my site" open your public site.
        if (host && /^https?:\/\//.test(site.data.base_url)) {
          var ms = $('me-site');
          ms.href = site.data.base_url;
          $('me-host').textContent = host;
          ms.hidden = false;
        }
        // ⭐ A mark is the avatar alone: no initial over it (the mockups'
        // letters were placeholders, Vincent 2026-11-17).
        me.textContent = '';
        var st = avatarStyle(site.data.avatar, function (s) { return root.btoa(s); });
        if (st) me.setAttribute('style', st);
      }
      var r = route(root.location.pathname), sec = r.section;
      if (r.home) {
        var seen = await api.get('/v1/seen');
        if (seen.ok && seen.data) state.seen = seen.data.seen || {};
        sec = lastSection(state.seen);
      } else {
        api.get('/v1/seen').then(function (s) { if (s.ok && s.data) state.seen = s.data.seen || {}; });
      }
      // ⭐ /_/ shows the section in place, with no second redirect (3.9).
      go(sec, { push: false, sentence: r.sentence, group: r.group });
      dots();
    })();
  }

  root.PolisV5 = {
    model: model,
    section: function (name, impl) { sections[name] = impl; },
  };
  if (typeof module !== 'undefined' && module.exports) module.exports = root.PolisV5;
  if (root.document && root.document.addEventListener) {
    // ⚠️ After DOMContentLoaded, which fires once every deferred script has
    // run, so each section's script has registered before the first draw.
    // (A deferred script runs while the document is "interactive", so
    // booting then would draw before the sections exist.)
    if (root.document.readyState === 'complete') boot();
    else root.document.addEventListener('DOMContentLoaded', boot);
  }
})(typeof window !== 'undefined' ? window : globalThis);
