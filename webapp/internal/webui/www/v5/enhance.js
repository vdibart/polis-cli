// enhance (v5 spec 2.4, 1.15, 3.6): the owner's settings, in groups that fold
// to a line each (you · your site · people · agents · your data). The filters
// are the groups; search finds a setting by its label and opens its group.
//
// ⛔ No row is drawn that does nothing: a row whose operation does not exist
// yet, or not on this kind of instance, is left out until it does (passkeys,
// signing out everywhere on polis.pub, leaving polis.pub, the email
// preferences, the messages password, "show straight away").
//
// Every change is a /v1 operation, the owner-only ones included (1.2), except
// polis.pub's own account actions (the sign-in email, signing out, and the
// verified export that is polis.pub's download), which stay polis.pub's.
(function (root) {
  'use strict';

  // ── The rules ────────────────────────────────────────────────────────

  var GROUPS = [
    { id: 'you', filter: 'you', title: 'You' },
    { id: 'site', filter: 'your site', title: 'Your site' },
    { id: 'people', filter: 'people', title: 'People' },
    { id: 'agents', filter: 'agents', title: 'Agents' },
    { id: 'data', filter: 'your data', title: 'Your data' },
  ];

  // instance is what kind of instance this is, from the API's description:
  // polis.pub (an operator), a remote instance (a public API, no operator),
  // or the owner's own machine.
  function instance(caps) {
    if (caps && caps.hasOperator) return 'polis.pub';
    if (caps && caps.hasPublicApi) return 'remote';
    return 'local';
  }

  // rows is which rows each group draws on this instance, given what it can
  // do: an operation the description lists, an agent address, the account.
  function rows(f) {
    var where = instance(f.caps), has = function (p) { return !!(f.paths && f.paths[p]); };
    var out = {
      you: ['name', 'avatar'],
      site: ['address', 'about', 'theme', 'terms', 'rebuild'],
      // People: who is shown at once, and the messages password (2.4); the
      // email preferences wait on gap E4.
      people: has('/v1/site/policy') ? ['show-straight-away', 'message-password'] : ['message-password'],
      agents: ['rosie'],
      data: ['key', 'directory'],
    };
    if (where === 'polis.pub' && f.account) out.you.push('email');
    if (where !== 'local') out.you.push('signed-in');
    if (where === 'polis.pub' || f.mcp) out.agents.push('agent-address');
    out.agents.push('tokens', 'connections');
    if (has('/v1/site/download') || where === 'polis.pub') out.data.push('download');
    return out;
  }

  // everywhere: signing out every browser exists only where the instance
  // keeps the sessions (a remote instance; polis.pub's is gap E2).
  function everywhere(caps) { return instance(caps) === 'remote'; }

  // agentAddress is where an agent app connects (3.6): the site's own /mcp
  // on polis.pub, the description's MCP endpoint elsewhere, none on a laptop.
  function agentAddress(caps, mcp, base) {
    if (instance(caps) === 'polis.pub') return String(base || '').replace(/\/+$/, '') + '/mcp';
    return mcp || '';
  }

  // MCP_STEPS are the agents notice's steps (Vincent 2026-11-20): how an
  // owner connects an agent of their own, the address shown in the first.
  var MCP_LEAD = 'Claude, ChatGPT, Cursor and other apps that speak MCP can work on your site for you: write and publish posts, reply to comments, change how your site looks. An agent does only what you tick when you connect it, everything it makes carries its mark, and you can disconnect it here at any time.';
  var MCP_STEPS = [
    'Copy your agent address:',
    'In your app, add it as a custom connector or MCP server.',
    'A window opens: sign in to your site and tick what your agent may do.',
  ];

  // mcpNote is the notice's markup for an agent address, or '' where the
  // site has none (a laptop) or the owner put it away. esc escapes.
  function mcpNote(addr, hidden, icon, esc) {
    if (!addr || hidden) return '';
    return '<aside class="mcpnote" aria-label="Connect your own agent">' +
      '<button class="mcp-x" type="button" data-do="mcp-hide" aria-label="Put this away" title="Put this away"><svg viewBox="0 0 16 16" aria-hidden="true"><path d="M4 4l8 8M12 4l-8 8"/></svg></button>' +
      '<h4><svg viewBox="0 0 24 24" aria-hidden="true">' + icon + '</svg>Bring your own AI agent</h4>' +
      '<p>' + esc(MCP_LEAD) + '</p>' +
      '<ol><li>' + esc(MCP_STEPS[0]) + '<div class="mcp-addr"><code>' + esc(addr) + '</code><button class="slim" data-do="copy-addr" data-text="' + esc(addr) + '" type="button">Copy</button></div></li>' +
      '<li>' + esc(MCP_STEPS[1]) + '</li><li>' + esc(MCP_STEPS[2]) + '</li></ol>' +
      '<a class="mcp-more" href="/_/enhance/agents#connections">Your agents and their permissions \u203a</a></aside>';
  }

  // matches is search (2.4): a setting found by its label, words in any order.
  function matches(text, words) {
    var t = String(text || '').toLowerCase(), ws = String(words || '').toLowerCase().split(/\s+/).filter(Boolean);
    return ws.every(function (w) { return t.indexOf(w) >= 0; });
  }

  // termsLine is the terms of use as one line.
  function termsLine(lic) {
    var t = lic && lic.stated && lic.terms;
    if (!t) return 'none stated';
    var profile = String(t.profile || '').replace(/^pub\.polis\.license\./, '').replace(/\/\d+$/, '');
    var bits = [];
    if (t['train-ai']) bits.push(t['train-ai'] === 'y' ? 'AI training allowed' : 'no AI training');
    if (t.search) bits.push(t.search === 'y' ? 'search allowed' : 'no search');
    if (t.attribution === 'required') bits.push('attribution required');
    var name = profile ? profile.charAt(0).toUpperCase() + profile.slice(1) : 'Stated';
    return bits.length ? name + ': ' + bits.join(' · ') : name;
  }

  // randomTokens is Randomise (2.4): a new look within the active theme, a
  // value for every choice token from what it allows, and the site's colour
  // and fonts. Colours that carry text are left to the theme, so the
  // contrast check holds. rnd: () => [0, 1).
  function randomTokens(contract, rnd) {
    rnd = rnd || Math.random;
    var pick = function (a) { return a[Math.floor(rnd() * a.length)]; };
    var out = {};
    (contract.tokens || []).forEach(function (t) {
      if (t.kind === 'choice' && t.allowed && t.allowed.length && t.name !== 'scheme') out[t.name] = pick(t.allowed);
    });
    var fonts = contract.fonts || [];
    if (fonts.length) {
      var names = fonts.map(function (f) { return typeof f === 'string' ? f : f.family || f.name; }).filter(Boolean);
      if (names.length) { out['font-content'] = pick(names); out['font-ui'] = pick(names); }
    }
    // The site's colour stands on the theme's own surface: dark on a light
    // one, light on a dark one, so the contrast check holds.
    // A hue's brightness differs (yellow reads lighter than blue at the same
    // lightness), so each candidate is checked, and drawn again until it
    // clears 3:1 with a little to spare.
    var surface = (contract.values || {}).surface || '#eceae6', dark = luminance(surface) < 0.4;
    for (var i = 0; i < 40; i++) {
      var h = Math.floor(rnd() * 360), s = 35 + Math.floor(rnd() * 30), l = dark ? 64 + Math.floor(rnd() * 12) : 24 + Math.floor(rnd() * 14);
      out.site = hslHex(h, s, l);
      if (contrast(out.site, surface) >= 3.2) break;
      out.site = dark ? '#9ec7d0' : '#2f5f66';
    }
    return out;
  }
  function contrast(a, b) {
    var x = luminance(a), y = luminance(b);
    return (Math.max(x, y) + 0.05) / (Math.min(x, y) + 0.05);
  }
  function luminance(hex) {
    var m = String(hex).match(/^#([0-9a-f]{6})$/i);
    if (!m) return 1;
    var c = [0, 2, 4].map(function (i) {
      var v = parseInt(m[1].slice(i, i + 2), 16) / 255;
      return v <= 0.03928 ? v / 12.92 : Math.pow((v + 0.055) / 1.055, 2.4);
    });
    return 0.2126 * c[0] + 0.7152 * c[1] + 0.0722 * c[2];
  }
  function hslHex(h, s, l) {
    s /= 100; l /= 100;
    var k = function (n) { return (n + h / 30) % 12; }, a = s * Math.min(l, 1 - l);
    var f = function (n) { return Math.round(255 * (l - a * Math.max(-1, Math.min(k(n) - 3, Math.min(9 - k(n), 1))))); };
    return '#' + [f(0), f(8), f(4)].map(function (x) { return ('0' + x.toString(16)).slice(-2); }).join('');
  }

  // randomAvatar is the avatar's Randomise: a background with readable
  // letters on it, a ring and a pattern drawn from it.
  function randomAvatar(rnd) {
    rnd = rnd || Math.random;
    var patterns = ['none', 'rings', 'cross', 'grid', 'dots', 'stripes', 'diamond', 'halves'];
    var h = Math.floor(rnd() * 360), l = 28 + Math.floor(rnd() * 14);
    var bg = hslHex(h, 35 + Math.floor(rnd() * 25), l);
    return {
      bg: bg, fg: '#ffffff',
      border: hslHex((h + 30) % 360, 30 + Math.floor(rnd() * 20), 50), border_w: Math.floor(rnd() * 3),
      pattern: patterns[Math.floor(rnd() * patterns.length)], pattern_color: hslHex(h, 40, Math.min(l + 18, 70)),
    };
  }

  // shortKey is the key as the row shows it: its type and its ends.
  function shortKey(k) {
    var parts = String(k || '').trim().split(/\s+/);
    if (parts.length < 2) return '';
    var b = parts[1];
    return parts[0].replace(/^ssh-/, '') + ' · ' + (b.length > 16 ? b.slice(0, 8) + '…' + b.slice(-4) : b);
  }

  // ── Show straight away (2.4): whether comments are blessed at once ──
  // The three choices are three versions of the public rules' comment lines
  // (bless shows a comment at once; review leaves it for the owner).
  var SELF = 'bless pub.polis.comment from self';
  var SHOWING = {
    'no-one': [SELF, 'review pub.polis.comment from all'],
    following: [SELF, 'bless pub.polis.comment from following', 'bless pub.polis.comment from thread-blessed', 'review pub.polis.comment from all'],
    anyone: [SELF, 'bless pub.polis.comment from all'],
  };
  function norm(r) { return String(r || '').trim().replace(/\s+/g, ' '); }
  function isShowingRule(r) { return /^(bless|review) pub\.polis\.comment /.test(norm(r)); }
  function active(list) {
    return (list || []).filter(function (p) { return p && (p.active === undefined || p.active) && norm(p.policy); }).map(function (p) { return norm(p.policy); });
  }

  // showing is which choice the rules are, or 'custom' when they are none of
  // the three (or the private rules decide comments too): then the row never
  // overwrites them.
  function showing(policy) {
    if (!policy) return 'custom';
    if (active(policy['private']).some(isShowingRule)) return 'custom';
    var lines = active(policy['public']).filter(isShowingRule).join('\n');
    for (var k in SHOWING) if (SHOWING[k].join('\n') === lines) return k;
    return 'custom';
  }

  // showingRules is the public rules with their comment lines replaced by a
  // choice's, in the place the first of them stood; every other rule kept.
  function showingRules(policy, choice) {
    var rules = active(policy && policy['public']), out = [], placed = false;
    rules.forEach(function (r) {
      if (!isShowingRule(r)) { out.push(r); return; }
      if (!placed) { out = out.concat(SHOWING[choice]); placed = true; }
    });
    if (!placed) {
      var i = out.length && /^deny all from all$/.test(out[out.length - 1]) ? out.length - 1 : out.length;
      out.splice.apply(out, [i, 0].concat(SHOWING[choice]));
    }
    return out;
  }

  // togglesGroup is whether a click on a group's card opens or folds it. A
  // closed card is one target, edge to edge, padding included. An open card
  // is a target above its rows (the heading's band), never inside them, and
  // never on a control.
  function togglesGroup(open, inRows, onControl, clickY, rowsTop) {
    if (inRows || onControl) return false;
    return !open || clickY < rowsTop;
  }

  var model = {
    togglesGroup: togglesGroup,
    SHOWING: SHOWING, showing: showing, showingRules: showingRules,
    GROUPS: GROUPS, instance: instance, rows: rows, everywhere: everywhere, agentAddress: agentAddress, mcpNote: mcpNote, MCP_STEPS: MCP_STEPS,
    matches: matches, termsLine: termsLine, randomTokens: randomTokens, randomAvatar: randomAvatar, shortKey: shortKey,
    tokenScopes: tokenScopes,
  };
  if (typeof module !== 'undefined' && module.exports) module.exports = model;

  // ── The drawing ──────────────────────────────────────────────────────

  var V5 = root.PolisV5, Card = root.PolisV5Card;
  if (!V5 || !Card || !root.document) return;
  var doc = root.document, esc = Card.esc;

  var ICON = {
    you: '<circle cx="12" cy="8.5" r="3.6"/><path d="M5 20c.8-3.8 3.6-5.6 7-5.6s6.2 1.8 7 5.6"/>',
    site: '<rect x="3" y="4.5" width="18" height="15" rx="2.5"/><path d="M3 9h18"/>',
    people: '<path d="M4 5.5h16v10.5H10l-6 4.5z"/>',
    agents: '<path d="M12 3l1.8 5.2L19 10l-5.2 1.8L12 17l-1.8-5.2L5 10l5.2-1.8z"/><path d="M19 16l.7 2 2 .7-2 .7-.7 2-.7-2-2-.7 2-.7z"/>',
    data: '<circle cx="8" cy="15" r="4"/><path d="M11 12l8.5-8.5M16 7l3 3M14 9l2 2"/>',
  };
  var SEARCH = '<svg viewBox="0 0 24 24" aria-hidden="true"><circle cx="11" cy="11" r="6.5"/><path d="M20 20l-4.2-4.2"/></svg>';
  var CLOSE = '<svg class="x" viewBox="0 0 24 24" aria-hidden="true"><path d="M6 6l12 12M18 6L6 18"/></svg>';
  // The scopes a token may be given: the description's own vocabulary
  // (x-polis-scopes), the list the consent page reads, in its words, never a
  // list of the app's own (one fell behind: it offered 7 of 11). Only what an
  // agent may hold: no full (the owner's own token), no tag.
  function tokenScopes(desc) {
    return ((desc && desc['x-polis-scopes']) || []).filter(function (s) { return s && s.agent === true && s.name; })
      .map(function (s) { return { name: s.name, sentence: s.sentence || s.name, warning: s.warning || '' }; });
  }

  var Vault = root.PolisV5Key;
  var ctx = null, pane = null, list = null, tbar = null, sq = null, find = null;
  var st = { f: null, picked: [], openGroups: {}, previousTokens: null };

  function hint(t) { ctx.hint(t); }
  function failed(r) { hint(r.error || 'That did not work.'); }
  function fmtDate(iso) { var d = new Date(iso); return isNaN(d) ? '' : Card.fullDate(iso) + ', ' + ('0' + d.getHours()).slice(-2) + ':' + ('0' + d.getMinutes()).slice(-2); }

  // ── loading what the rows show ──
  async function load() {
    var api = ctx.api;
    var got = await Promise.all([
      api.get('/v1/site'), api.get('/v1/openapi.json'), api.get('/v1/content/license/current'), api.get('/v1/site/status'),
      api.get('/v1/agents/rosie'), api.get('/v1/agents/connections'), api.get('/v1/content/theme'), api.get('/v1/site/policy'),
    ]);
    var d = function (i) { return got[i].ok ? got[i].data || {} : {}; };
    var doc0 = d(1), caps = doc0['x-polis-capabilities'] || {};
    var f = {
      site: d(0), caps: caps, mcp: doc0['x-polis-mcp'] || '', paths: doc0.paths || {}, license: d(2), status: d(3),
      rosie: (d(4).rosie) || {}, connections: d(5).connections || [],
      // The themes this site's shape (v5) can wear.
      themes: (d(6).themes || []).filter(function (t) { return (t.compatible_shapes || []).indexOf('pub.polis.shapes.v5') >= 0; })
        .map(function (t) { return t.name; }).filter(Boolean),
      policy: d(7),
      scopes: tokenScopes(doc0),
      account: null,
      keyring: Vault ? await Vault.keyring(true) : null,
    };
    // polis.pub's own account (the sign-in email), on polis.pub only.
    if (instance(caps) === 'polis.pub') {
      var acc = await api.get('/api/account/info');
      if (acc.ok) f.account = acc.data || {};
    }
    st.f = f;
    draw();
  }

  // ── a row (1.15): label, value, description; the controls at the right ──
  function row(id, label, value, desc, controls, extra) {
    return '<div class="row" data-row="' + id + '"' + (extra || '') + '><span class="k">' + label + '</span>' +
      (value ? '<span class="v">' + value + '</span>' : '') + (desc ? '<span class="d">' + desc + '</span>' : '') +
      '<div class="act">' + (controls || '') + '</div></div>';
  }
  function sub(text) { return '<div class="sub-lbl">' + text + '</div>'; }

  function rowsHTML(g) {
    var f = st.f, s = f.site, want = rows(f)[g], host = Card.hostOf(s.base_url) || root.location.host;
    var on = function (id) { return want.indexOf(id) >= 0; };
    var h = '';
    if (g === 'you') {
      h += row('name', 'Name on your posts', esc(s.author_name || ''), '', '<button class="slim" data-do="edit-name" type="button">Edit</button>');
      h += row('avatar', 'Avatar', '', 'It’s what sits beside your posts everywhere.',
        '<span class="av mine-av" aria-hidden="true"></span><button class="slim" data-do="avatar" type="button">Randomise</button>');
      if (on('email')) {
        h += sub('WAYS TO SIGN IN');
        h += row('email', 'Email', esc(f.account.email || ''), 'We send a six-digit code. Type it where you’re signing in, in any browser.', '<button class="slim" data-do="edit-email" type="button">Change</button>');
      }
      if (on('signed-in')) {
        h += row('signed-in', 'Signed in here', '', everywhere(f.caps) ? 'Signing out everywhere signs out every browser you’ve used. Your agents stay connected: they have their own permission.' : '',
          '<button class="slim" data-do="signout" type="button">Sign out</button>' + (everywhere(f.caps) ? '<button class="slim" data-do="signout-all" type="button">Everywhere</button>' : ''));
      }
    }
    if (g === 'site') {
      h += row('address', 'Address', esc(host), '', '<a class="slim" href="' + esc(s.base_url || '/') + '" target="_blank" rel="noopener">Visit</a>');
      h += row('about', 'About you', esc(String(s.about || '').slice(0, 160)) + (String(s.about || '').length > 160 ? '…' : ''), 'What a visitor sees when they open your site’s mark: a few lines, in markdown.', '<button class="slim" data-do="edit-about" type="button">Edit</button>');
      h += row('theme', 'Theme', esc(String(s.theme || '').replace(/^pub\.polis\.themes\./, '').replace(/-/g, ' ')),
        'How your site looks to everyone who visits it. To customise it in detail (its colours, fonts and the feel of its cards), ask your agent: it can change any of them for you.',
        '<button class="slim" data-do="theme" type="button">Change</button><button class="slim" data-do="randomise" type="button">Randomise</button>' +
        (st.previousTokens ? '<button class="slim" data-do="revert" type="button">Revert</button>' : ''));
      h += row('terms', 'Terms of use', esc(termsLine(f.license)), 'What others may do with your writing. Changes apply from now on; posts keep the terms they were signed with.', '<button class="slim" data-do="terms" type="button">Change</button>');
      h += row('rebuild', 'Something looks stale?', '', 'Rebuilds every page of your site from what you’ve written.', '<button class="slim" data-do="rebuild" type="button">Rebuild</button>');
    }
    if (g === 'people' && on('show-straight-away')) {
      var now = showing(f.policy);
      h += sub('COMMENTS ON YOUR POSTS');
      h += row('show-straight-away', 'Show straight away', now === 'custom' ? 'your own rules' : '',
        (f.rosie && f.rosie.on === false ? 'Rosie is off, so nothing is shown straight away; every comment waits for you in write. '
          : ({ anyone: 'Every comment shows under your post at once; none waits for you. ',
            following: 'Comments from people you follow, and from anyone you have blessed in that thread, show at once; the rest wait for you in write. ',
            'no-one': 'Every comment but your own waits for you in write. ' })[now] || 'Your rules for comments are your own, so the choices here would overwrite them. '),
        '<div class="seg">' + [['anyone', 'anyone'], ['following', 'people you follow'], ['no-one', 'no one']].map(function (c) {
          return '<button type="button" data-do="showing" data-v="' + c[0] + '"' + (now === c[0] ? ' class="on"' : '') + (now === 'custom' ? ' disabled' : '') + '>' + c[1] + '</button>';
        }).join('') + '</div><button class="link" data-do="policy-reset" type="button">Restore the defaults</button>');
    }
    if (g === 'people' && on('message-password')) {
      var cur = Vault ? Vault.model.current(f.keyring) : null, isSet = !!(cur && cur.kind === 'password');
      var who = instance(f.caps) === 'polis.pub' ? 'polis.pub' : 'the server your site runs on';
      h += sub('MESSAGES');
      h += row('message-password', 'Message password',
        isSet ? 'Set, since ' + esc(monthYear(cur.created_at)) : esc('Not set: ' + who + ' could read your messages'),
        'Your messages are sealed with a key only it opens. Nobody else holds it, including ' + esc(who) + ' and any agent you connect.',
        isSet ? '<button class="slim" data-do="pw-change" type="button">Change</button><button class="slim" data-do="pw-phrase" type="button">New recovery phrase</button>'
          : '<button class="slim go" data-do="pw-set" type="button">Set a password</button>');
    }
    if (g === 'agents') {
      var r = f.rosie;
      h += sub('WORKING FOR YOU');
      // ⭐ Rosie is shown, not switched, here (Vincent 2026-11-19): the row says
      // what she does and whether she is on, with no control.
      h += row('rosie', 'Rosie' + (instance(f.caps) === 'polis.pub' ? ' <span class="tag">from polis.pub</span>' : ''), r.on ? 'on' : 'off',
        esc((r.text && r.text.does) || 'Answers comment requests on your posts using your own rules.'), '');
      if (on('agent-address')) {
        var addr = agentAddress(f.caps, f.mcp, s.base_url);
        h += sub('CONNECT YOUR OWN AGENT');
        h += row('agent-address', 'Your agent address', '<code>' + esc(addr) + '</code>', 'Add it in Claude, ChatGPT, Cursor or any app that speaks MCP. The app opens a window where you sign in and tick what your agent may do.',
          '<button class="slim" data-do="copy-addr" data-text="' + esc(addr) + '" type="button">Copy</button>');
      }
      h += row('tokens', 'Tokens', '', 'For a script or an app that can’t open that window. A token carries the same kind of permission, and you can withdraw it here.',
        '<button class="slim" data-do="make-token" type="button">Make a token</button>');
      var conns = f.connections.filter(function (c) { return c.live; });
      conns.forEach(function (c, i) {
        var acted = c.last_used ? 'Connected ' + esc(fmtDate(c.created)) + ' · last acted ' + esc(fmtDate(c.last_used)) : 'Connected ' + esc(fmtDate(c.created)) + ' · has not acted yet';
        h += row(i === 0 ? 'connections' : 'connection-' + i, esc(c.name) + ' <span class="tag">your agent</span>', acted,
          esc((c.scopes || []).join(' · ')), '<button class="slim danger" data-do="disconnect" data-grant="' + esc(c.grant_id) + '" data-name="' + esc(c.name) + '" type="button">Disconnect</button>');
      });
    }
    if (g === 'data') {
      h += row('key', 'Your key', '<code>' + esc(shortKey(s.public_key)) + '</code>', 'Everything you write is signed with it. Your site lists every key it has had.',
        '<button class="slim" data-do="copy-key" type="button">Copy</button><button class="slim danger" data-do="rotate" type="button">Rotate</button>');
      var dir = f.status.directory || {};
      h += row('directory', 'The polis directory', esc(dir.error ? 'Not reachable just now' : dir.configured ? 'Listed' : 'Not listed'), 'How other sites find your posts and comments.', '');
      if (on('download')) h += row('download', 'Take it with you', '', 'A zip of your whole site: posts, comments, settings and your key.', '<button class="slim" data-do="download" type="button">Download</button>');
    }
    return h;
  }

  function monthYear(iso) {
    var d = new Date(iso);
    return isNaN(d) ? '' : ['Jan', 'Feb', 'Mar', 'Apr', 'May', 'Jun', 'Jul', 'Aug', 'Sep', 'Oct', 'Nov', 'Dec'][d.getUTCMonth()] + ' ' + d.getUTCFullYear();
  }

  function summary(g) {
    var f = st.f, s = f.site;
    if (g === 'you') return esc(s.author_name || '') + (f.account && f.account.email ? ' · signs in with an email code' : '');
    if (g === 'site') return esc(Card.hostOf(s.base_url)) + ' · ' + esc(String(s.theme || '').replace(/^pub\.polis\.themes\./, '').replace(/-/g, ' ')) + ' · your terms: ' + esc(termsLine(f.license).split(':')[0].toLowerCase());
    if (g === 'people') {
      var s0 = showing(f.policy);
      var shown = s0 === 'anyone' ? 'Comments from anyone show straight away' : s0 === 'following' ? 'Comments from people you follow show straight away' : s0 === 'no-one' ? 'Every comment waits for you' : 'Your own rules for comments';
      var c0 = Vault ? Vault.model.current(f.keyring) : null;
      return shown + ' \u00b7 ' + (c0 && c0.kind === 'password' ? 'messages locked with your password' : 'messages not under a password yet');
    }
    if (g === 'agents') {
      var n = f.connections.filter(function (c) { return c.live; }).length;
      return 'Rosie is ' + (f.rosie.on ? 'on' : 'off') + ' · ' + (n ? n + ' of your agents connected' : 'no agent of yours connected yet');
    }
    if (g === 'data') {
      var dir = f.status.directory || {};
      return (dir.configured && !dir.error ? 'listed in the polis directory' : 'the polis directory') + (rows(f).data.indexOf('download') >= 0 ? ' · take everything with you any time' : '');
    }
    return '';
  }

  // A group with no rows on this instance is not drawn (no row that does nothing).
  function present() { var r = rows(st.f); return GROUPS.filter(function (g) { return r[g.id].length; }); }

  // The notice, once put away, stays away in this browser. ⚠️ Storage can be
  // missing or refuse; the notice then simply shows.
  var MCP_HIDDEN = 'polis.v5.mcpnote.hidden';
  function mcpHidden() { try { return root.localStorage.getItem(MCP_HIDDEN) === '1'; } catch (e) { return false; } }

  function draw() {
    var groups = present();
    tbar.querySelector('.kinds').innerHTML = groups.map(function (g) {
      return '<button type="button" data-g="' + g.id + '" aria-pressed="false">' + g.filter + '</button>';
    }).join('');
    list.innerHTML = mcpNote(agentAddress(st.f.caps, st.f.mcp, st.f.site.base_url), mcpHidden(), ICON.agents, esc) + groups.map(function (g) {
      // The pinned row (1.5): an open group waits under the menu as one line
      // once its heading scrolls away; a click on it folds the group.
      return '<section class="item grp' + (st.openGroups[g.id] ? ' open' : '') + '" data-g="' + g.id + '" aria-label="' + g.title + '">' +
        '<div class="stick" aria-hidden="true"><div class="srow"><div class="sin"><span class="smark"><svg viewBox="0 0 24 24">' + ICON[g.id] + '</svg></span>' +
        '<span class="sttl">' + g.title + '</span><span class="ssum">' + summary(g.id) + '</span></div></div></div>' +
        '<span class="mark"><svg viewBox="0 0 24 24" aria-hidden="true">' + ICON[g.id] + '</svg></span>' +
        '<h3 tabindex="0" role="button" aria-expanded="' + (st.openGroups[g.id] ? 'true' : 'false') + '">' + g.title + '</h3><div class="sum">' + summary(g.id) + '</div>' +
        '<div class="rows"><div><div class="rows-in">' + rowsHTML(g.id) + '</div></div></div></section>';
    }).join('') + '<div class="nothing" hidden>Nothing matches that.</div>';
    var av = list.querySelector('.mine-av'), me = doc.getElementById('me');
    if (av && me && me.getAttribute('style')) av.setAttribute('style', me.getAttribute('style'));
    paintFilters();
    runFind();
  }

  function paintFilters() {
    tbar.querySelectorAll('.kinds button').forEach(function (b) {
      var on = st.picked.indexOf(b.dataset.g) >= 0;
      b.classList.toggle('on', on);
      b.setAttribute('aria-pressed', on ? 'true' : 'false');
    });
    list.querySelectorAll('.grp').forEach(function (s) { s.hidden = !!st.picked.length && st.picked.indexOf(s.dataset.g) < 0; });
  }

  function toggleGroup(sec, open) {
    var on = open === undefined ? !sec.classList.contains('open') : open;
    sec.classList.toggle('open', on);
    if (!on) sec.classList.remove('stuck');
    st.openGroups[sec.dataset.g] = on;
    sec.querySelector('h3').setAttribute('aria-expanded', on ? 'true' : 'false');
  }

  // openAt opens a group and brings a row into view (3.9: /_/enhance/<group>#<row>).
  function openAt(group, rowId) {
    var sec = list.querySelector('.grp[data-g="' + group + '"]');
    if (!sec) return;
    toggleGroup(sec, true);
    var target = rowId ? sec.querySelector('[data-row="' + rowId + '"]') : sec;
    setTimeout(function () { (target || sec).scrollIntoView({ behavior: 'smooth', block: 'center' }); }, 350);
  }

  // ── search (2.4): a setting by its label; its group opens ──
  function runFind() {
    var words = find.value.trim(), any = false;
    list.querySelectorAll('.grp').forEach(function (sec) {
      var hit = false;
      sec.querySelectorAll('.row, .sub-lbl').forEach(function (r) {
        var k = r.classList.contains('row') ? (r.querySelector('.k') || {}).textContent : '';
        var show = !words || (r.classList.contains('row') && matches(k, words));
        r.hidden = !!words && !show;
        hit = hit || (words && show);
      });
      var title = matches(sec.querySelector('h3').textContent, words);
      if (words && title) sec.querySelectorAll('.row, .sub-lbl').forEach(function (r) { r.hidden = false; });
      var visible = !words || hit || title;
      sec.classList.toggle('found-hide', !visible);
      if (words && visible) sec.classList.add('open');
      if (!words) sec.classList.toggle('open', !!st.openGroups[sec.dataset.g]);
      any = any || visible;
    });
    var nm = list.querySelector('.nothing');
    if (nm) nm.hidden = any || !words;
  }
  function openSearch() { doc.body.classList.add('searching'); sq.setAttribute('aria-label', 'Close search'); setTimeout(function () { find.focus(); }, 60); }
  function closeSearch() { doc.body.classList.remove('searching'); sq.setAttribute('aria-label', 'Search settings'); find.value = ''; runFind(); }

  // ── editing a value in its row ──
  // A text edited in its row. A markdown one (the bio, which the site renders
  // as markdown) is written as the sheet's words are: rich text with the
  // sheet's formatting buttons by default, markdown by choice (the sheet's
  // field, one implementation). ⭐ Its Save stays off until the text differs
  // from what was opened: an untouched bio is never sent again.
  function editInRow(rowEl, current, markdown, label, save) {
    var act = rowEl.querySelector('.act');
    var was = act.innerHTML;
    var Sheet = root.PolisV5Sheet;
    var field = markdown ? '<textarea class="edit-in" rows="5" aria-label="' + esc(label) + '"></textarea>' : '<input class="edit-in" aria-label="' + esc(label) + '">';
    var box = doc.createElement('div');
    box.className = 'row-edit';
    box.innerHTML = (markdown && Sheet ? '<div class="fmt row-fmt" aria-label="Formatting"></div><div class="body-rich edit-rich" hidden></div>' : '') +
      field + '<div class="edit-row"><button class="link cancel" type="button">cancel</button><button class="slim go save" type="button">Save</button></div>';
    rowEl.appendChild(box);
    act.innerHTML = '';
    var input = box.querySelector('.edit-in'), saveBtn = box.querySelector('.save');
    input.value = current || '';
    var opened = input.value, fld = null;
    function text() { return fld ? fld.text() : input.value; }
    function changed() { if (markdown) saveBtn.disabled = text() === opened; }
    if (markdown && Sheet) {
      fld = Sheet.field({ doc: doc, fmt: box.querySelector('.row-fmt'), rich: box.querySelector('.edit-rich'), body: input,
        kitAt: Sheet.editorAddress(doc), onChange: changed, onInput: changed, onMount: function () { fld.focus(); } });
      fld.ensure();
    } else input.addEventListener('input', changed);
    changed();
    input.focus();
    var done = function () { if (fld) fld.destroy(); box.remove(); act.innerHTML = was; };
    box.querySelector('.cancel').onclick = done;
    saveBtn.onclick = async function () {
      var v = text();
      if (markdown && v === opened) return;
      saveBtn.disabled = true;
      var r = await save(v);
      if (!r.ok) { saveBtn.disabled = false; failed(r); return; }
      if (fld) fld.destroy();
      hint('Saved.');
      await load();
    };
  }

  // A choice of a few options, in its row.
  function chooseInRow(rowEl, options, current, pickIt) {
    var act = rowEl.querySelector('.act'), was = act.innerHTML;
    act.innerHTML = '<div class="seg">' + options.map(function (o) {
      return '<button type="button" data-v="' + esc(o[0]) + '"' + (o[0] === current ? ' class="on"' : '') + '>' + esc(o[1]) + '</button>';
    }).join('') + '</div><button class="link cancel" type="button">cancel</button>';
    act.querySelector('.cancel').onclick = function () { act.innerHTML = was; };
    act.querySelectorAll('.seg button').forEach(function (b) { b.onclick = function () { pickIt(b.dataset.v); }; });
  }

  async function act(b) {
    var api = ctx.api, f = st.f, rowEl = b.closest('.row'), what = b.dataset.do;
    switch (what) {
      case 'edit-name':
        return editInRow(rowEl, f.site.author_name, false, 'Name on your posts', function (v) { return api.post('/v1/site/name', { author_name: v.trim() }); });
      case 'edit-about':
        return editInRow(rowEl, f.site.about, true, 'About you', function (v) { return api.post('/v1/site/about', { content: v }); });
      case 'edit-email':
        // polis.pub's account action: confirmed from the new address.
        return editInRow(rowEl, f.account.email, false, 'Email', function (v) { return api.post('/api/account/change-email', { email: v.trim() }); });
      case 'avatar': {
        var r = await api.post('/v1/site/avatar', { avatar: randomAvatar() });
        if (!r.ok) return failed(r);
        hint('Saved.');
        var site = await api.get('/v1/site');
        if (site.ok) {
          var style = V5.model.avatarStyle(site.data.avatar, function (s) { return root.btoa(s); });
          var me = doc.getElementById('me');
          if (me && style) me.setAttribute('style', style);
        }
        return load();
      }
      case 'theme':
        return chooseInRow(rowEl, f.themes.map(function (t) { return [t, String(t).replace(/^pub\.polis\.themes\./, '').replace(/-/g, ' ')]; }),
          String(f.site.theme || '').replace(/^pub\.polis\.themes\./, ''), async function (v) {
            var r = await api.post('/v1/site/theme', { theme: v });
            if (!r.ok) return failed(r);
            hint('Saved.');
            st.previousTokens = null;
            load();
          });
      case 'randomise': {
        var contract = await api.get('/v1/site/tokens');
        if (!contract.ok) return failed(contract);
        // A look the contrast check refuses is tried again, a few times.
        for (var i = 0; i < 4; i++) {
          var rr = await api.post('/v1/site/tokens', { tokens: randomTokens(contract.data) });
          if (rr.ok) {
            if (!st.previousTokens) st.previousTokens = (rr.data && rr.data.previous) || {};
            hint('Saved.');
            return load();
          }
          if (rr.status !== 400) return failed(rr);
        }
        return hint('No look that reads well came up. Try again.');
      }
      case 'revert': {
        var rv = await api.post('/v1/site/tokens', { tokens: st.previousTokens || {}, replace: true });
        if (!rv.ok) return failed(rv);
        st.previousTokens = null;
        hint('Saved.');
        return load();
      }
      case 'terms': {
        var cur = (f.license && f.license.stated && f.license.terms && String(f.license.terms.profile || '').replace(/^pub\.polis\.license\./, '').replace(/\/\d+$/, '')) || 'none';
        return chooseInRow(rowEl, [['reserved', 'reserved'], ['open', 'open'], ['none', 'none']], cur, async function (v) {
          if (v === 'none' && !root.confirm('Withdraw your terms of use? Posts you publish from now on will state none; posts already published keep theirs.')) return;
          var r = await api.post('/v1/site/license', { profile: v });
          if (!r.ok) return failed(r);
          hint('Saved.');
          load();
        });
      }
      case 'rebuild': {
        b.disabled = true;
        var rb = await api.post('/v1/site/rebuild', {});
        b.disabled = false;
        return rb.ok ? hint('Rebuilt.') : failed(rb);
      }
      case 'mcp-hide': {
        try { root.localStorage.setItem(MCP_HIDDEN, '1'); } catch (e) { /* shows again next visit */ }
        var note = b.closest('.mcpnote');
        if (note) note.remove();
        return hint('Put away. Your agent address stays under agents.');
      }
      case 'copy-addr':
        return copy(b.dataset.text, 'Copied. Paste it into your agent app as a new connector or MCP server.');
      case 'copy-key':
        return copy(String(f.site.public_key || '').trim(), 'Copied.');
      case 'make-token':
        return tokenForm(rowEl);
      case 'showing': {
        if (b.classList.contains('on')) return;
        var rs = await api.post('/v1/site/policy', { which: 'public', rules: showingRules(f.policy, b.dataset.v) });
        if (!rs.ok) return failed(rs);
        hint('Saved.');
        return load();
      }
      case 'policy-reset': {
        if (!root.confirm('Restore your rules for comments and messages to the defaults? Rules you or an agent added are removed.')) return;
        var rr = await api.post('/v1/site/policy/reset', {});
        if (!rr.ok) return failed(rr);
        hint('Saved.');
        return load();
      }
      case 'pw-set':
        return pwFlow(rowEl, 'set');
      case 'pw-change':
        return pwFlow(rowEl, 'change');
      case 'pw-phrase':
        return pwFlow(rowEl, 'phrase');
      case 'disconnect': {
        if (!root.confirm('Disconnect ' + b.dataset.name + '? This is permanent: to use it again, connect it again.')) return;
        var rd = await api.post('/v1/agents/connections/disconnect', { grant_id: b.dataset.grant });
        if (!rd.ok) return failed(rd);
        hint('Disconnected. Its permission is withdrawn. What it already made keeps its mark.');
        return load();
      }
      case 'rotate': {
        if (!root.confirm('Rotate your key? Your site makes a new key and signs the handover with the old one. What you signed before still checks out. This cannot be undone.')) return;
        b.disabled = true;
        var ro = await api.post('/v1/site/key/rotate', {});
        b.disabled = false;
        if (!ro.ok) return failed(ro);
        hint('Your new key is in place.');
        return load();
      }
      case 'download': {
        if (instance(f.caps) === 'polis.pub') {
          // polis.pub's verified export: a link by email.
          var ex = await api.post('/api/export/request', {});
          return ex.ok ? hint((ex.data && ex.data.message) || 'Check your email for a download link.') : failed(ex);
        }
        root.location.href = '/v1/site/download';
        return;
      }
      case 'signout':
      case 'signout-all': {
        var path = instance(f.caps) === 'polis.pub' ? '/auth/logout' : '/api/signout' + (what === 'signout-all' ? '?everywhere=1' : '');
        if (what === 'signout-all' && !root.confirm('Sign out of every browser? Your agents stay connected.')) return;
        await api.post(path, {});
        root.location.href = '/';
        return;
      }
    }
  }

  async function copy(text, said) {
    try { await root.navigator.clipboard.writeText(text); hint(said); } catch (e) { hint('Copying is not allowed here; select it and copy it yourself.'); }
  }

  // The messages password (2.4, 3.6), in its row: everything happens in this
  // browser, and only what is wrapped is sent.
  function pwFlow(rowEl, kind) {
    var old = rowEl.querySelector('.pwflow');
    if (old) old.remove();
    var box = doc.createElement('div');
    box.className = 'pwflow on';
    var field = function (label, ac) { return '<input type="password" aria-label="' + label + '" placeholder="' + label + '" autocomplete="' + ac + '">'; };
    var forms = {
      set: '<h4>Choose a password</h4><p>We don\u2019t keep it. If you forget it, only your recovery phrase can help.</p>' + field('Choose a password', 'new-password'),
      change: '<h4>Change password</h4><p>Your current password opens the key; the new one wraps it again. Your recovery phrase keeps working.</p>' + field('Current password', 'current-password') + field('New password', 'new-password'),
      phrase: '<h4>A new recovery phrase</h4><p>Your current password opens the key; a new phrase is made for it. Your old phrase stops working now.</p>' + field('Current password', 'current-password'),
    };
    var go = { set: 'Continue', change: 'Change password', phrase: 'Make a new phrase' }[kind];
    box.innerHTML = forms[kind] + '<p class="err" hidden></p><div class="row2"><button class="link" type="button" data-pw="cancel">Cancel</button><button class="slim go" type="button" data-pw="go">' + go + '</button></div>';
    rowEl.appendChild(box);
    box.querySelector('input').focus();
    var say = function (s) { var e = box.querySelector('.err'); e.textContent = s; e.hidden = !s; };
    var showPhrase = function (phrase, then) {
      box.innerHTML = '<h4>Save your recovery phrase</h4><p>These 12 words are the only way back in if you forget your password. Store them somewhere safe, not in polis.</p>' +
        '<div class="words">' + phrase.split(/\s+/).map(function (w, i) { return '<span><i>' + (i + 1) + '</i>' + esc(w) + '</span>'; }).join('') + '</div>' +
        '<p class="warn">Anyone who has this phrase can read all your messages. Keep it offline.</p>' +
        '<div class="row2"><button class="slim" type="button" data-pw="copy">Copy</button><button class="slim go" type="button" data-pw="saved">I\u2019ve saved it</button></div>';
      box.onclick = function (e) {
        var b = e.target.closest('[data-pw]');
        if (!b) return;
        e.stopPropagation();
        if (b.dataset.pw === 'copy') return copy(phrase, 'Copied.');
        hint(then);
        load();
      };
    };
    box.onclick = async function (e) {
      var b = e.target.closest('[data-pw]');
      if (!b) return;
      e.stopPropagation();
      if (b.dataset.pw === 'cancel') { box.remove(); return; }
      var ins = box.querySelectorAll('input');
      if (Array.prototype.some.call(ins, function (i) { return !i.value; })) return;
      b.disabled = true;
      say('');
      try {
        if (kind === 'set') showPhrase(await Vault.setPassword(ins[0].value), 'Your messages are under your password now.');
        if (kind === 'change') { await Vault.changePassword(ins[0].value, ins[1].value); hint('Password changed. Your messages weren\u2019t touched and your recovery phrase still works.'); load(); }
        if (kind === 'phrase') showPhrase(await Vault.newPhrase(ins[0].value), 'Your new recovery phrase is in place; the old one no longer works.');
      } catch (err) {
        say(Vault.isWrong(err) ? 'That password didn\u2019t work. Try again?' : (err && err.message) || 'That did not work.');
        b.disabled = false;
      }
    };
  }

  // Make a token (3.6): name it, choose what it may do, copy it once.
  function tokenForm(rowEl) {
    if (rowEl.querySelector('.row-edit')) return;
    var box = doc.createElement('div');
    box.className = 'row-edit';
    box.innerHTML = '<input class="edit-in" aria-label="What the token is for" placeholder="what it is for">' +
      '<div class="scopes">' + ((st.f && st.f.scopes) || []).map(function (s) {
        // ⛔ Nothing is pre-ticked, policy:write least of all: its warning is said beside it.
        return '<label><input type="checkbox" value="' + esc(s.name) + '"> ' + esc(s.sentence) +
          (s.warning ? ' <span class="warn">' + esc(s.warning) + '</span>' : '') + '</label>';
      }).join('') + '</div>' +
      '<div class="edit-row"><button class="link cancel" type="button">cancel</button><button class="slim go save" type="button">Make it</button></div>';
    rowEl.appendChild(box);
    box.querySelector('.cancel').onclick = function () { box.remove(); };
    box.querySelector('.save').onclick = async function () {
      var scopes = Array.prototype.map.call(box.querySelectorAll('.scopes input:checked'), function (i) { return i.value; });
      if (!scopes.length) { hint('Choose what it may do.'); return; }
      var r = await ctx.api.post('/v1/agents/tokens', { name: box.querySelector('.edit-in').value.trim(), scopes: scopes });
      if (!r.ok) return failed(r);
      var token = (r.data && r.data.token) || '';
      box.innerHTML = '<p class="d">Copy it now: it is shown this once.</p><code class="token">' + esc(token) + '</code>' +
        '<div class="edit-row"><button class="slim" type="button">Copy</button><button class="link done" type="button">done</button></div>';
      box.querySelector('.slim').onclick = function () { copy(token, 'Copied.'); };
      box.querySelector('.done').onclick = function () { load(); };
    };
  }

  // ── the section ──
  V5.section('enhance', {
    mount: function (p, frameCtx) {
      ctx = frameCtx;
      pane = p;
      if (Vault) Vault.init(ctx.api);
      tbar = doc.querySelector('.tbar[data-for="enhance"]');
      tbar.innerHTML = '<button class="sq" type="button" aria-label="Search settings" title="Search settings">' + SEARCH + CLOSE + '</button>' +
        '<div class="views kinds" role="group" aria-label="Groups"></div>' +
        '<label class="find">' + SEARCH + '<input type="search" autocomplete="off" spellcheck="false" placeholder="find a setting" aria-label="Find a setting"></label>';
      sq = tbar.querySelector('.sq');
      find = tbar.querySelector('.find input');
      list = doc.createElement('div');
      list.className = 'feed groups';
      pane.appendChild(list);
      sq.onclick = function () { if (doc.body.classList.contains('searching')) closeSearch(); else openSearch(); };
      find.addEventListener('input', runFind);
      find.addEventListener('keydown', function (e) { if (e.key === 'Escape') { e.stopPropagation(); closeSearch(); } });
      // A group filter is a moment (3.9): the address stays the section's.
      tbar.querySelector('.kinds').addEventListener('click', function (e) {
        var b = e.target.closest('button');
        if (!b) return;
        var i = st.picked.indexOf(b.dataset.g);
        if (i >= 0) st.picked.splice(i, 1); else st.picked.push(b.dataset.g);
        paintFilters();
      });
      list.addEventListener('click', function (e) {
        var srow = e.target.closest('.grp .srow');
        if (srow) {
          var g = srow.closest('.grp'), y = g.getBoundingClientRect().top + root.scrollY - 82;
          toggleGroup(g, false);
          root.scrollTo({ top: Math.max(0, y), behavior: 'smooth' });
          return;
        }
        var b = e.target.closest('[data-do]');
        if (b) { e.preventDefault(); act(b); return; }
        var g = e.target.closest('.grp');
        if (g && togglesGroup(g.classList.contains('open'), !!e.target.closest('.rows'),
          !!e.target.closest('a, button, input, select, textarea, label'),
          e.clientY, g.querySelector('.rows').getBoundingClientRect().top)) toggleGroup(g);
      });
      // An open group pins once its heading has passed under the menu.
      root.addEventListener('scroll', function () {
        if (doc.body.dataset.sec !== 'enhance') return;
        list.querySelectorAll('.grp').forEach(function (g) {
          if (!g.classList.contains('open')) { g.classList.remove('stuck'); return; }
          var h = g.querySelector('h3').getBoundingClientRect();
          g.classList.toggle('stuck', h.bottom < 74 && g.getBoundingClientRect().bottom > 164);
        });
      }, { passive: true });
      list.addEventListener('keydown', function (e) {
        if ((e.key === 'Enter' || e.key === ' ') && e.target.matches('.grp > h3')) { e.preventDefault(); toggleGroup(e.target.closest('.grp')); }
      });
    },
    show: async function (opts) {
      await load();
      if (opts && opts.group) openAt(opts.group, String(root.location.hash || '').replace(/^#/, ''));
    },
    leave: function () { if (doc.body.classList.contains('searching')) closeSearch(); return true; },
  });
})(typeof window !== 'undefined' ? window : globalThis);
