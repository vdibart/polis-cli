// The v5 owner app's one door to the site: the /v1 API, which every agent
// uses too (v5 spec 1.2). Requests go same-origin with no token: the owner's
// own sign-in is what the server reads. ⛔ Nothing in the app calls /api/.
//
// Every answer is { ok, status, data, error }: a failure is said in place,
// next to what failed (spec 1.16), never thrown at the page.
(function (root) {
  'use strict';

  function errorText(status, body) {
    if (body && body.error) {
      if (typeof body.error === 'string') return body.error;
      if (body.error.message) return body.error.message;
    }
    if (status === 401) return 'You are signed out. Sign in again to carry on.';
    if (status === 403) return 'That is not allowed here.';
    if (status === 429) return 'Too many requests just now. Try again in a minute.';
    if (status >= 500) return 'Your site could not do that just now.';
    return 'That did not work.';
  }

  // ⭐ A 401 means the owner's sign-in has lapsed (the app sends no token).
  // The app is told once (onSignedOut), so it can stop asking on a timer: a
  // tab left open overnight otherwise asks every minute, and every refusal is
  // a security event. Any later success means signed in again.
  var signedOut = false, signedOutListeners = [];
  function noteStatus(status) {
    if (status === 401) {
      if (signedOut) return;
      signedOut = true;
      signedOutListeners.forEach(function (fn) { try { fn(); } catch (e) { /* a listener's own fault */ } });
    } else if (status >= 200 && status < 300) {
      signedOut = false;
    }
  }

  async function call(method, path, body, opts) {
    var init = {
      method: method,
      credentials: 'same-origin',
      headers: { Accept: 'application/json' },
      keepalive: !!(opts && opts.keepalive),
    };
    if (body !== undefined) {
      init.headers['Content-Type'] = 'application/json';
      init.body = JSON.stringify(body);
    }
    var res;
    try {
      res = await root.fetch(path, init);
    } catch (e) {
      return { ok: false, status: 0, data: null, error: 'Your site could not be reached.' };
    }
    var data = null;
    try { data = await res.json(); } catch (e) { /* an empty or non-JSON body */ }
    noteStatus(res.status);
    if (!res.ok) return { ok: false, status: res.status, data: data, error: errorText(res.status, data) };
    return { ok: true, status: res.status, data: data, error: '' };
  }

  // queryPath is /v1/query for a PQL sentence, with its options (counts,
  // new_for, only_new, limit, cursor, search) as the API takes them.
  function queryPath(sentence, opts) {
    var q = 'pql=' + encodeURIComponent(sentence);
    Object.keys(opts || {}).forEach(function (k) {
      if (opts[k] !== undefined && opts[k] !== null && opts[k] !== '') q += '&' + k + '=' + encodeURIComponent(opts[k]);
    });
    return '/v1/query?' + q;
  }

  var api = {
    get: function (path) { return call('GET', path); },
    post: function (path, body, opts) { return call('POST', path, body === undefined ? {} : body, opts); },
    put: function (path, body) { return call('PUT', path, body); },
    queryPath: queryPath,
    // query answers a PQL sentence: { ok, items, next, counts, counts_capped,
    // error }. counts_capped names the counts that are lower bounds.
    query: async function (sentence, opts) {
      var r = await call('GET', queryPath(sentence, opts));
      var env = (r.data && r.data.result) || r.data || {};
      return {
        ok: r.ok, status: r.status, error: r.error,
        items: env.items || [],
        next: (env.pagination && env.pagination.next_cursor) || env.next_cursor || '',
        counts: env.counts || {},
        counts_capped: env.counts_capped || [],
      };
    },
    errorText: errorText,
    isSignedOut: function () { return signedOut; },
    onSignedOut: function (fn) { signedOutListeners.push(fn); },
  };

  root.PolisAPI = api;
  if (typeof module !== 'undefined' && module.exports) module.exports = api;
})(typeof window !== 'undefined' ? window : globalThis);
