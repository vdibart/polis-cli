// message (v5 spec 2.3, 3.5): private conversations, one card for each
// person, only between people who follow each other. Messages are the
// owner's alone, so everything here is an owner-only /v1 operation, reached
// on the owner's own sign-in.
//
// ⭐ Sealing follows the messages key (2.3). With no messages password the
// site holds the key, opens the messages (dm.get) and seals what is sent
// (dm.send). With a password the key is the owner's, in this browser
// (msgkey.js): locked until the owner unlocks it, then each message's box is
// opened here and what is sent is sealed here (dm.send_sealed).
(function (root) {
  'use strict';

  // ── The rules ────────────────────────────────────────────────────────

  var LIST = 'all messages from my mutuals';

  // conversationSite is the person an addressed view names (3.9): a sentence
  // in message naming exactly one site opens that conversation.
  function conversationSite(sentence) {
    var m = String(sentence || '').trim().toLowerCase().match(/^all messages from (?:https?:\/\/)?([a-z0-9]([a-z0-9-]*[a-z0-9])?(\.[a-z0-9]([a-z0-9-]*[a-z0-9])?)+)\/?$/);
    return m ? m[1] : '';
  }

  // address is a conversation's address (3.9).
  function address(site) { return '/_/message/pql/all+messages+from+' + site; }

  // counts is what the filters say (1.4): conversations, narrowed to those
  // with something unread while unread is picked, and the unread ones.
  function counts(convos, unreadOn) {
    var unread = convos.filter(function (c) { return c.unread > 0; }).length;
    return { conversations: unreadOn ? unread : convos.length, unread: unread };
  }

  function label(k, n) { return k === 'unread' ? n + ' unread' : n + (n === 1 ? ' conversation' : ' conversations'); }

  // mine: the owner's own side of a conversation (the site's mailbox names
  // the owner "me").
  function mine(msg, me) { var f = String(msg.from || '').toLowerCase(); return f === 'me' || (!!me && f === me); }

  // words is what a message says where this tab can read it: its content,
  // or the words opened from its box (text), else null.
  function words(m) { return typeof m.text === 'string' ? m.text : (m.locked ? null : m.content); }

  // latest is a conversation's newest message that can be read, or null.
  function latest(messages) {
    for (var i = (messages || []).length - 1; i >= 0; i--) if (words(messages[i]) !== null) return messages[i];
    return null;
  }

  // sealing is who seals and opens the messages: 'site' with no password
  // (or no keyring yet: the site makes one), 'browser' under a password.
  function sealing(keyring) {
    if (!keyring || !Array.isArray(keyring.epochs)) return 'site';
    var cur = keyring.epochs.filter(function (e) { return e.id === keyring.current; })[0];
    return cur && cur.kind === 'password' ? 'browser' : 'site';
  }

  // picker is the new-conversation panel's two groups (2.3, 3.5): people who
  // follow each other with the owner, and one-way follows, with what each
  // row says and offers. refused names, from dm.recipients, the people who
  // follow each other with the owner yet take no messages from them, and
  // why. words narrow both.
  function picker(mutuals, network, open, words, refused) {
    refused = refused || {};
    var w = String(words || '').trim().toLowerCase();
    var hit = function (s) { return !w || s.indexOf(w) >= 0; };
    var mutualRows = mutuals.filter(hit).map(function (s) {
      if (refused[s] && !open[s]) return { site: s, note: refused[s], action: '' };
      return open[s] ? { site: s, note: 'conversation open', action: 'Open' } : { site: s, note: '', action: 'Message' };
    });
    var oneWay = network.filter(function (p) { return hit(p.site) && mutuals.indexOf(p.site) < 0; }).map(function (p) {
      if (p.relationship === 'follows-you') return { site: p.site, note: 'follows you', action: 'Follow back' };
      return { site: p.site, note: 'you follow them; they don’t follow you yet', action: '' };
    }).filter(function (r) { return r.note; });
    return { mutual: mutualRows, oneWay: oneWay };
  }

  var model = {
    LIST: LIST, conversationSite: conversationSite, address: address, counts: counts, label: label,
    mine: mine, latest: latest, words: words, sealing: sealing, picker: picker,
  };
  if (typeof module !== 'undefined' && module.exports) module.exports = model;

  // ── The drawing ──────────────────────────────────────────────────────

  var V5 = root.PolisV5, Card = root.PolisV5Card, Vault = root.PolisV5Key;
  if (!V5 || !Card || !Vault || !root.document) return;
  var doc = root.document;
  var esc = Card.esc;

  var PLUS = '<svg viewBox="0 0 16 16" aria-hidden="true"><path d="M8 2.5v11M2.5 8h11"/></svg>';
  var LOCK = '<svg viewBox="0 0 24 24" aria-hidden="true"><rect x="5" y="10.5" width="14" height="9.5" rx="2"/><path d="M8.5 10.5V8a3.5 3.5 0 0 1 7 0v2.5"/></svg>';

  var ctx = null, pane = null, list = null, panel = null, tbar = null, plus = null;
  var st = { convos: [], unreadOn: false, mode: 'site', avatars: {}, mutuals: [], network: [], loading: 0 };

  function me() {
    var site = (ctx.state && ctx.state.site) || {};
    return Card.hostOf(site.base_url) || root.location.host;
  }
  function mark(site) {
    var style = V5.model.avatarStyle(st.avatars[site], function (s) { return root.btoa(s); });
    return '<span class="av" aria-hidden="true"' + (style ? ' style="' + esc(style) + '"' : '') + '></span>';
  }

  // ── the filters ──
  function paintFilters() {
    var n = counts(st.convos, st.unreadOn);
    var k = tbar.querySelector('[data-kind="convo"]'), u = tbar.querySelector('.newk');
    k.innerHTML = '<b>' + n.conversations + '</b> ' + esc(label('conversations', n.conversations).replace(/^\d+ /, ''));
    u.innerHTML = '<b>' + n.unread + '</b> unread';
    u.classList.toggle('has', n.unread > 0);
    u.classList.toggle('on', st.unreadOn);
    u.setAttribute('aria-pressed', st.unreadOn ? 'true' : 'false');
    doc.body.classList.toggle('only-unread', st.unreadOn);
  }

  // ── a conversation's card (1.5: compact; the latest message leads) ──
  function cardFor(c) {
    var el = doc.createElement('article');
    el.className = 'item convo' + (c.unread ? ' unread' : '');
    el.tabIndex = 0;
    el.dataset.site = c.site;
    el.innerHTML = '<div class="stick" aria-hidden="true"><div class="srow"><div class="sin">' + mark(c.site) + '<span class="sttl">' + esc(c.site) + '</span><span class="slead"></span></div></div></div>' +
      (c.unread ? '<span class="new-dot" aria-label="unread"></span>' : '') + mark(c.site) +
      '<div class="corner"><div class="ch"><time class="hdt"></time><b class="hh">' + esc(c.site) + '</b></div></div>' +
      '<div class="what pre"></div><div class="what mutual">you follow each other</div>' +
      '<div class="msgs"><div><div class="msgs-in"></div>' +
      '<div class="ir-in"><textarea class="ir-t" rows="2" aria-label="Your message"></textarea>' +
      '<p class="sheet-err" role="alert" hidden></p>' +
      '<div class="ir-row"><span class="sp"></span><button class="slim go send" type="button" disabled>Send</button></div></div>' +
      '<div class="cfoot"><button class="link close" type="button">close</button></div></div></div>';
    el.convo = c;
    var t = el.querySelector('.ir-t');
    t.placeholder = 'Write to ' + c.site + '…';
    t.addEventListener('input', function () { el.querySelector('.send').disabled = !t.value.trim(); t.style.height = 'auto'; t.style.height = t.scrollHeight + 'px'; });
    t.addEventListener('keydown', function (e) { if (e.key === 'Enter' && (e.metaKey || e.ctrlKey) && t.value.trim()) send(el); });
    el.querySelector('.send').onclick = function (e) { e.stopPropagation(); send(el); };
    el.querySelector('.close').onclick = function (e) { e.stopPropagation(); shut(el); };
    el.querySelector('.srow').onclick = function (e) { e.stopPropagation(); shut(el, true); };
    el.querySelector('.msgs').addEventListener('click', function (e) { e.stopPropagation(); });
    el.addEventListener('click', function () { if (el.classList.contains('open')) shut(el); else openCard(el); });
    el.addEventListener('keydown', function (e) {
      if (e.target === el && (e.key === 'Enter' || e.key === ' ')) { e.preventDefault(); if (el.classList.contains('open')) shut(el); else openCard(el); }
    });
    paint(el);
    return el;
  }

  function paint(el, fresh) {
    var c = el.convo, you = me(), last = latest(c.messages);
    var pre = el.querySelector('.pre');
    if (last) pre.innerHTML = (mine(last, you) ? '<span class="you">you</span>' : '') + esc(words(last));
    else pre.innerHTML = c.messages.length ? '<i>locked</i>' : '<i>No messages yet.</i>';
    el.querySelector('.slead').textContent = last ? (mine(last, you) ? 'you: ' : '') + words(last) : '';
    var hdt = el.querySelector('.hdt');
    hdt.textContent = c.at ? Card.relativeTime(c.at) : '';
    hdt.setAttribute('datetime', c.at || '');
    hdt.style.display = c.at ? '' : 'none';
    var box = el.querySelector('.msgs-in');
    if (!c.messages.length) { box.innerHTML = '<div class="msg-none">No messages yet. Say hello.</div>'; return; }
    // Messages under an earlier key this tab can't open: one box per key,
    // above the rest (they are older), which opens in place (2.3).
    var cur = Vault.current(), groups = cur ? Vault.model.earlier(c.messages, cur.id, Vault.isUnlocked) : [];
    var hidden = {};
    groups.forEach(function (g) { hidden[g.epoch] = true; });
    box.innerHTML = groups.map(function (g) {
      return '<div class="earlier" data-epoch="' + g.epoch + '"><div class="eh">' + LOCK + esc(g.count + ' earlier message' + (g.count === 1 ? '' : 's') + ' \u00b7 an earlier key' + (g.months ? ' (' + g.months + ')' : '')) + '<span class="more">unlock \u203a</span></div>' +
        '<div class="eb"><input class="fld" type="password" aria-label="Enter that password" placeholder="Enter that password">' +
        '<p class="err" hidden></p><div class="row2"><button class="link swap" type="button">use your recovery phrase</button><span class="sp"></span><button class="slim go eu" type="button">Unlock these messages</button></div></div></div>';
    }).join('') + c.messages.filter(function (m) { return !(m.locked && hidden[m.key_epoch]); }).map(function (m) {
      var own = mine(m, you), w = words(m);
      return '<div class="msg' + (own ? ' mine' : '') + (m === fresh ? ' new' : '') + '"><div class="mm"><b>' + esc(own ? 'you' : c.site) + '</b><span>' + esc(Card.relativeTime(m.timestamp)) + '</span></div>' +
        '<div class="mx">' + (w === null ? '<i>locked</i>' : esc(w)) + '</div></div>';
    }).join('');
  }

  // Opening reads it: the dot goes and the unread count drops (1.6). One
  // conversation is open at a time.
  function openCard(el) {
    list.querySelectorAll('.convo.open').forEach(function (o) { if (o !== el) shut(o); });
    el.classList.add('open');
    var c = el.convo;
    if (c.unread && c.id) {
      c.unread = 0;
      el.classList.remove('unread');
      var d = el.querySelector('.new-dot');
      if (d) d.remove();
      paintFilters();
      ctx.api.post('/v1/content/dm/actions/mark_read', { conversation_id: c.id });
    }
    // The other person has no password: said above the composer (2.3).
    if (!el.dataset.checked) {
      el.dataset.checked = '1';
      ctx.api.get('/v1/messages/protection-status?recipient_url=' + encodeURIComponent('https://' + c.site)).then(function (r) {
        if (!r.ok || !r.data || r.data.protected !== false) return;
        var line = doc.createElement('div');
        line.className = 'theirs';
        line.textContent = c.site + ' hasn\u2019t set a messages password. Be careful what you send.';
        var ir = el.querySelector('.ir-in');
        ir.insertBefore(line, ir.firstChild);
      });
    }
    setTimeout(function () {
      var t = el.querySelector('.ir-t');
      if (!t.disabled) t.focus({ preventScroll: true });
      el.querySelector('.ir-in').scrollIntoView({ behavior: 'smooth', block: 'center' });
    }, 200);
  }
  function shut(el, toTop) {
    var y = el.getBoundingClientRect().top + root.scrollY - 82;
    el.classList.remove('open', 'stuck');
    if (toTop) root.scrollTo({ top: Math.max(0, y), behavior: 'smooth' });
  }

  async function send(el) {
    var c = el.convo, t = el.querySelector('.ir-t'), b = el.querySelector('.send'), err = el.querySelector('.sheet-err');
    var text = t.value.trim();
    if (!text || st.mode === 'locked') return;
    b.disabled = true;
    err.hidden = true;
    // With no password the site seals; under one, this browser does (2.3).
    var r;
    if (st.mode === 'site') r = await ctx.api.post('/v1/content/dm/actions/send', { recipient_url: 'https://' + c.site, content: text });
    else { try { r = await Vault.send('https://' + c.site, text); } catch (e2) { r = { ok: false, error: e2.message }; } }
    // A message saved but not delivered answers 2xx with a warning, from
    // either send (the site's, and the browser's sealed one).
    var undelivered = r.ok && r.data && (r.data.warning || r.data.error);
    if (!r.ok || undelivered) {
      err.textContent = r.ok ? undelivered : r.error;
      err.hidden = false;
      b.disabled = false;
      return;
    }
    if (r.data && r.data.conversation_id) c.id = r.data.conversation_id;
    var m = { from: 'me', content: text, text: text, timestamp: new Date().toISOString() };
    c.messages.push(m);
    c.at = m.timestamp;
    t.value = '';
    t.style.height = '';
    paint(el, m);
    // The newest conversation leads the list.
    if (list.firstElementChild !== el) { list.insertBefore(el, list.firstElementChild); el.querySelector('.ir-in').scrollIntoView({ block: 'center' }); }
  }

  // ── loading ──
  async function load(openSite) {
    var ticket = ++st.loading;
    var got = await Promise.all([
      ctx.api.query(LIST, { limit: 100 }),
      ctx.api.get('/v1/content/dm'),
      Vault.keyring(true),
    ]);
    if (ticket !== st.loading) return;
    var q = got[0], dl = got[1];
    if (!q.ok) { list.innerHTML = '<p class="cm-none"></p>'; list.firstChild.textContent = q.error; return; }
    // No keyring yet is the site's to make (404): sealing by the site.
    st.mode = sealing(got[2]) === 'site' ? 'site' : (Vault.state() === 'unlocked' ? 'unlocked' : 'locked');
    var unread = {};
    ((dl.ok && dl.data && dl.data.conversations) || []).forEach(function (c) { unread[c.id] = c.unread_count || 0; });
    st.convos = q.items.map(function (it) {
      return { id: it.conversation_id || it.id, site: String(it.sender_domain || '').toLowerCase(), at: it.published, unread: unread[it.conversation_id || it.id] || 0, messages: [] };
    }).filter(function (c) { return c.site; });
    // Each card's latest words come from its conversation: read by the site,
    // or opened here from each message's box.
    if (st.mode !== 'locked') {
      var reads = await Promise.all(st.convos.map(function (c) { return ctx.api.get('/v1/content/dm/' + encodeURIComponent(c.id)); }));
      if (ticket !== st.loading) return;
      reads.forEach(function (r, i) { if (r.ok && r.data) st.convos[i].messages = (r.data.messages || []).map(openOne); });
    }
    draw();
    if (openSite) openWith(openSite);
  }

  function openOne(m) {
    if (m.locked) { var w = Vault.open(m); if (w !== null) m.text = w; }
    return m;
  }

  function draw() {
    list.innerHTML = '';
    // No password yet: one quiet line heads the list; locked: the unlock card,
    // with the list blurred behind it (2.3).
    doc.body.classList.toggle('msg-nopw', st.mode === 'site');
    doc.body.classList.toggle('msg-locked', st.mode === 'locked');
    if (st.mode === 'locked') step('pw');
    st.convos.forEach(function (c) { list.appendChild(cardFor(c)); });
    if (!st.convos.length) {
      var none = doc.createElement('p');
      none.className = 'cm-none';
      none.textContent = 'No messages yet.';
      list.appendChild(none);
    }
    paintFilters();
  }

  // openWith opens the conversation with a site, or starts one (2.3: nothing
  // is sent until the first message).
  function openWith(site) {
    closeNew();
    var el = Array.prototype.filter.call(list.querySelectorAll('.convo'), function (x) { return x.dataset.site === site; })[0];
    if (!el) {
      var c = { id: '', site: site, at: '', unread: 0, messages: [] };
      st.convos.unshift(c);
      var none = list.querySelector('.cm-none');
      if (none) none.remove();
      el = cardFor(c);
      el.classList.add('new');
      var first = list.querySelector('.convo');
      list.insertBefore(el, first || null);
      paintFilters();
    }
    root.scrollTo({ top: 0, behavior: 'smooth' });
    setTimeout(function () { openCard(el); }, 250);
  }

  // ── starting a conversation (2.3, 3.5) ──
  async function loadPeople() {
    var got = await Promise.all([
      ctx.api.query('all profiles from my mutuals', { limit: 200 }),
      ctx.api.query('all profiles from my network', { limit: 200 }),
      ctx.api.get('/v1/messages/recipients'),
    ]);
    // Who can be messaged, and why not (dm.recipients).
    st.refused = {};
    ((got[2].ok && got[2].data && got[2].data.recipients) || []).forEach(function (r) {
      if (r.status === 'no-dm' && r.domain) st.refused[String(r.domain).toLowerCase()] = r.reason || 'takes no messages from you';
    });
    got = got.slice(0, 2);
    var site = function (p) { return String(p.author_domain || '').toLowerCase(); };
    got.forEach(function (r) { r.items.forEach(function (p) { if (p.author_avatar) st.avatars[site(p)] = p.author_avatar; }); });
    st.mutuals = got[0].items.map(site).filter(Boolean);
    st.network = got[1].items.map(function (p) { return { site: site(p), relationship: p.relationship }; }).filter(function (p) { return p.site; });
    paintPicks();
  }
  function paintPicks() {
    var open = {};
    st.convos.forEach(function (c) { open[c.site] = true; });
    var g = picker(st.mutuals, st.network, open, panel.querySelector('input').value, st.refused);
    var html = (g.mutual.length ? '<div class="pick-lbl">you follow each other</div>' : '') + g.mutual.map(function (r) {
      return '<div class="pick">' + mark(r.site) + '<b>' + esc(r.site) + '</b>' + (r.note ? '<span class="why">' + esc(r.note) + '</span>' : '') +
        '<button class="slim' + (r.action === 'Message' ? ' go' : '') + '" type="button" data-to="' + esc(r.site) + '">' + r.action + '</button></div>';
    }).join('');
    if (g.oneWay.length) {
      html += '<div class="pick-lbl">follow each other to message</div>' + g.oneWay.map(function (r) {
        return '<div class="pick cant">' + mark(r.site) + '<b>' + esc(r.site) + '</b><span class="why">' + esc(r.note) + '</span>' +
          (r.action ? '<button class="slim" type="button" data-back="' + esc(r.site) + '">' + r.action + '</button>' : '') + '</div>';
      }).join('');
    }
    panel.querySelector('.picks').innerHTML = html;
  }
  async function followBack(b) {
    var site = b.dataset.back;
    b.disabled = true;
    var r = await ctx.api.post('/v1/content/follow', { url: 'https://' + site });
    b.disabled = false;
    if (!r.ok) { ctx.hint(r.error); return; }
    st.mutuals.push(site);
    ctx.hint('Following ' + site + '. You follow each other now, so you can message.');
    paintPicks();
  }
  function openNew() {
    doc.body.classList.add('starting');
    plus.classList.add('on');
    plus.setAttribute('aria-label', 'Close');
    panel.querySelector('input').value = '';
    loadPeople();
    setTimeout(function () { panel.querySelector('input').focus(); }, 200);
  }
  function closeNew() {
    if (!doc.body.classList.contains('starting')) return false;
    doc.body.classList.remove('starting');
    plus.classList.remove('on');
    plus.setAttribute('aria-label', 'Start a conversation');
    return true;
  }

  // ── the unlock card (2.3): password, recovery phrase, a new password, a
  // fresh key; every check is the decryption itself, in this browser ──
  var unlockCard = null, pwLine = null;
  var STEPS = {
    pw: '<h3>Your messages are locked</h3><p>Only your messages password, or your recovery phrase, opens them. polis.pub can\u2019t.</p>' +
      '<input class="fld" type="password" aria-label="Message password" placeholder="Message password" autocomplete="current-password">' +
      '<p class="err" hidden></p><div class="row2"><button class="link" type="button" data-u="to-phrase">Use your recovery phrase</button><span class="sp"></span><button class="slim go" type="button" data-u="unlock">Unlock</button></div>',
    phrase: '<h3>Your recovery phrase</h3><p>The 12 words from when you set your password. It opens everything, and then you choose a new password.</p>' +
      '<textarea class="fld" rows="2" aria-label="Your recovery phrase" placeholder="word one, word two, \u2026" spellcheck="false" autocomplete="off"></textarea>' +
      '<p class="ok" hidden>\u2713 a valid phrase</p><p class="err" hidden></p>' +
      '<div class="row2"><button class="link" type="button" data-u="to-fresh">Lost your phrase too?</button><span class="sp"></span><button class="link" type="button" data-u="to-pw">Use your password</button><button class="slim go" type="button" data-u="recover" disabled>Recover</button></div>',
    newpw: '<h3>You\u2019re back in. Set a new password.</h3><p>It wraps the same key again, so nothing is lost, and your recovery phrase keeps working.</p>' +
      '<input class="fld" type="password" aria-label="New password" placeholder="New password" autocomplete="new-password"><p class="err" hidden></p>' +
      '<div class="row2"><span class="sp"></span><button class="slim go" type="button" data-u="set-new">Set password</button></div>',
    fresh: '<h3>Start a fresh key</h3><p class="warn">You get a new key, a new password and a new recovery phrase. Earlier messages stay here, sealed, readable only if your old password or phrase ever turns up. Nothing is deleted.</p>' +
      '<input class="fld" type="password" aria-label="New password" placeholder="A new password" autocomplete="new-password"><p class="err" hidden></p>' +
      '<div class="row2"><button class="link" type="button" data-u="to-phrase">Back</button><span class="sp"></span><button class="slim danger" type="button" data-u="fresh">Create a fresh key</button></div>',
    saved: '<h3>Save your recovery phrase</h3><p>These 12 words are the only way back in if you forget your password. Store them somewhere safe, not in polis.</p>' +
      '<div class="words"></div><p class="warn">Anyone who has this phrase can read all your messages. Keep it offline.</p>' +
      '<div class="row2"><span class="sp"></span><button class="slim" type="button" data-u="copy">Copy</button><button class="slim go" type="button" data-u="saved">I\u2019ve saved it</button></div>',
  };
  var phraseShown = '';
  function step(name) {
    unlockCard.querySelector('.steps').innerHTML = STEPS[name];
    unlockCard.dataset.step = name;
    if (name === 'saved') {
      unlockCard.querySelector('.words').innerHTML = phraseShown.split(/\s+/).map(function (w, i) { return '<span><i>' + (i + 1) + '</i>' + esc(w) + '</span>'; }).join('');
    }
    var f = unlockCard.querySelector('.fld');
    if (f && doc.body.dataset.sec === 'message') setTimeout(function () { f.focus({ preventScroll: true }); }, 60);
  }
  function say(text) { var e = unlockCard.querySelector('.err'); if (e) { e.textContent = text; e.hidden = !text; } }
  function wrong(e) { return Vault.isWrong(e) ? 'That password didn\u2019t work. Try again?' : (e && e.message) || 'That did not work.'; }

  async function unlockAct(b) {
    var f = unlockCard.querySelector('.fld'), v = f ? f.value : '';
    switch (b.dataset.u) {
      case 'to-phrase': return step('phrase');
      case 'to-pw': return step('pw');
      case 'to-fresh': return step('fresh');
      case 'unlock':
        if (!v) return;
        b.disabled = true;
        try { await Vault.unlock(v, 'password'); ctx.hint('Unlocked for this tab. Closing it locks them again.'); load(); }
        catch (e) { say(wrong(e)); b.disabled = false; }
        return;
      case 'recover':
        b.disabled = true;
        try { await Vault.unlock(v, 'phrase'); step('newpw'); }
        catch (e) { say(Vault.isWrong(e) ? 'That phrase didn\u2019t open them.' : wrong(e)); b.disabled = false; }
        return;
      case 'set-new':
        if (!v) return;
        b.disabled = true;
        try { await Vault.setNewPassword(v); ctx.hint('Your messages are under your new password now.'); load(); }
        catch (e) { say(wrong(e)); b.disabled = false; }
        return;
      case 'fresh':
        if (!v) return;
        b.disabled = true;
        try { phraseShown = await Vault.freshKey(v); step('saved'); }
        catch (e) { say(wrong(e)); b.disabled = false; }
        return;
      case 'copy':
        try { await root.navigator.clipboard.writeText(phraseShown); ctx.hint('Copied.'); } catch (e) { ctx.hint('Copying is not allowed here; write them down.'); }
        return;
      case 'saved':
        phraseShown = '';
        load();
    }
  }

  // ── an earlier key's box: it is the control, and opens in place (2.3) ──
  async function earlierAct(e) {
    var box = e.target.closest('.earlier');
    if (!box || e.target.closest('input, textarea')) return;
    e.stopPropagation();
    if (!box.classList.contains('open')) { box.classList.add('open'); var f0 = box.querySelector('.fld'); if (f0) f0.focus(); return; }
    var b = e.target.closest('button');
    if (!b) return;
    var id = Number(box.dataset.epoch), f = box.querySelector('.fld'), err = box.querySelector('.err');
    var show = function (t2) { if (err) { err.textContent = t2; err.hidden = !t2; } };
    if (b.classList.contains('swap')) {
      var phrase = f.tagName !== 'TEXTAREA';
      var nf = doc.createElement(phrase ? 'textarea' : 'input');
      nf.className = 'fld';
      if (phrase) { nf.rows = 2; nf.setAttribute('aria-label', 'Your recovery phrase'); nf.placeholder = 'that key\u2019s recovery phrase'; }
      else { nf.type = 'password'; nf.setAttribute('aria-label', 'Enter that password'); nf.placeholder = 'Enter that password'; }
      f.replaceWith(nf);
      b.textContent = phrase ? 'use that password' : 'use your recovery phrase';
      nf.focus();
      return;
    }
    if (b.classList.contains('eu')) {
      b.disabled = true;
      try {
        await Vault.unlock(f.value, f.tagName === 'TEXTAREA' ? 'phrase' : 'password', id);
        // Keep these unlocked? Wrap that key under the current password.
        box.querySelector('.eb').innerHTML = '<div class="q"><b>Keep these unlocked?</b>Wrap that earlier key under your current password, so you aren\u2019t asked again.' +
          '<input class="fld" type="password" aria-label="Your current password" placeholder="Your current password"><p class="err" hidden></p>' +
          '<div class="row2"><button class="link keep-no" type="button">Just view</button><span class="sp"></span><button class="slim go keep" type="button">Keep unlocked</button></div></div>';
        var conv = box.closest('.convo');
        conv.convo.messages = conv.convo.messages.map(openOne);
        var keep = box.cloneNode(true);
        paint(conv);
        var msgs = conv.querySelector('.msgs-in');
        msgs.insertBefore(keep, msgs.firstChild);
        keep.classList.add('open', 'asked');
      } catch (e2) { show(wrong(e2)); b.disabled = false; }
      return;
    }
    if (b.classList.contains('keep-no')) { box.remove(); return; }
    if (b.classList.contains('keep')) {
      b.disabled = true;
      try { await Vault.keepUnlocked(id, f.value); ctx.hint('Kept unlocked under your current password.'); box.remove(); }
      catch (e3) { show(wrong(e3)); b.disabled = false; }
    }
  }

  // ── the section ──
  V5.section('message', {
    mount: function (p, frameCtx) {
      ctx = frameCtx;
      pane = p;
      tbar = doc.querySelector('.tbar[data-for="message"]');
      tbar.innerHTML = '<button class="newbtn" type="button" aria-label="Start a conversation" title="Start a conversation">' + PLUS + '</button>' +
        '<div class="views kinds" role="group" aria-label="Filters"><button type="button" data-kind="convo" aria-pressed="false"></button>' +
        '<span class="ksep" aria-hidden="true"></span><button type="button" class="newk" aria-pressed="false"></button></div>';
      plus = tbar.querySelector('.newbtn');
      panel = doc.createElement('div');
      panel.className = 'newc';
      panel.innerHTML = '<div><div class="newc-in"><h4>Message someone</h4>' +
        '<p>Direct messages are only between people who follow each other. One conversation with each person.</p>' +
        '<label class="find"><svg viewBox="0 0 24 24" aria-hidden="true"><circle cx="11" cy="11" r="6.5"/><path d="M20 20l-4.2-4.2"/></svg>' +
        '<input autocomplete="off" spellcheck="false" placeholder="find someone you follow each other with" aria-label="Find someone"></label>' +
        '<div class="picks"></div></div></div>';
      list = doc.createElement('div');
      list.className = 'feed convos';
      pwLine = doc.createElement('div');
      pwLine.className = 'pwline';
      pwLine.innerHTML = 'Your messages aren\u2019t under a password yet, so polis.pub could read them.<a href="/_/enhance/people#message-password">Set a password</a>';
      unlockCard = doc.createElement('section');
      unlockCard.className = 'item unlock';
      unlockCard.setAttribute('aria-label', 'Your messages are locked');
      unlockCard.innerHTML = '<span class="lk">' + LOCK + '</span><div class="steps"></div>';
      pane.appendChild(panel);
      pane.appendChild(pwLine);
      pane.appendChild(unlockCard);
      pane.appendChild(list);
      Vault.init(ctx.api);
      pwLine.querySelector('a').addEventListener('click', function (e) { e.preventDefault(); root.history.pushState(null, '', '/_/enhance/people#message-password'); ctx.go('enhance', { group: 'people', push: false }); });
      unlockCard.addEventListener('click', function (e) { var b = e.target.closest('[data-u]'); if (b) unlockAct(b); });
      unlockCard.addEventListener('keydown', function (e) {
        if (e.key !== 'Enter' || e.target.tagName !== 'INPUT') return;
        var go = unlockCard.querySelector('.slim.go, .slim.danger');
        if (go) unlockAct(go);
      });
      unlockCard.addEventListener('input', function (e) {
        if (unlockCard.dataset.step !== 'phrase' || e.target.tagName !== 'TEXTAREA') return;
        var ok = Vault.validPhrase(e.target.value);
        unlockCard.querySelector('.ok').hidden = !ok;
        unlockCard.querySelector('[data-u="recover"]').disabled = !ok;
      });
      list.addEventListener('click', earlierAct, true);
      // An unlocked key locks again when the tab has been idle: draw again.
      Vault.onChange(function () { if (doc.body.dataset.sec === 'message' && st.mode !== 'site') load(); });
      plus.onclick = function () { if (!closeNew()) openNew(); };
      tbar.querySelector('.newk').onclick = function () {
        if (!st.unreadOn && !counts(st.convos).unread) return;
        st.unreadOn = !st.unreadOn;
        paintFilters();
      };
      // Conversations is everything; picking it again changes nothing (1.4).
      tbar.querySelector('[data-kind="convo"]').onclick = function () { if (st.unreadOn) { st.unreadOn = false; paintFilters(); } };
      panel.querySelector('input').addEventListener('input', paintPicks);
      panel.addEventListener('click', function (e) {
        var to = e.target.closest('[data-to]');
        if (to) { openWith(to.dataset.to); return; }
        var back = e.target.closest('[data-back]');
        if (back) followBack(back);
      });
      doc.addEventListener('keydown', function (e) {
        if (e.key !== 'Escape' || doc.body.dataset.sec !== 'message') return;
        if (closeNew()) return;
        list.querySelectorAll('.convo.open').forEach(function (el) { shut(el); });
      });
      // The pinned row, as on write and gather, for a long conversation.
      root.addEventListener('scroll', function () {
        list.querySelectorAll('.convo').forEach(function (el) {
          if (!el.classList.contains('open')) { el.classList.remove('stuck'); return; }
          var head = el.querySelector('.corner').getBoundingClientRect();
          el.classList.toggle('stuck', head.bottom < 74 && el.getBoundingClientRect().bottom > 164);
        });
      }, { passive: true });
    },
    // Each visit draws what is there now; an address naming one person
    // opens that conversation (3.9).
    show: function (opts) {
      st.unreadOn = false;
      load(opts && opts.sentence ? conversationSite(opts.sentence) : '');
    },
    leave: function () { closeNew(); return true; },
  });
})(typeof window !== 'undefined' ? window : globalThis);
