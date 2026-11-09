// owner-auth.js — the owner-auth screens: setup (which is also recovery),
// sign in, the list of ways to sign in, and the removal confirmation.
//
// ⛔⛔ NOTHING HERE MAY BE SHAPED LIKE ONE PROVIDER. Every list and both
// screens render from what the server reports about its registry — name,
// available, enrolled, enrollable, and the ceremony KIND. The only code that
// differs per provider is the browser ceremony itself, and it is keyed by kind
// in CEREMONIES below, never by provider name. A provider of a kind this file
// knows renders and works with no change here; one of an unknown kind is still
// listed, and still removable, and is marked as one this page cannot run.
// TestOwnerAuthUINamesNoProvider fails the build on a provider name anywhere in
// the SPA source outside the ceremony table.
//
// ⭐ First run and recovery are ONE screen because they are one flow: sign a
// challenge with the recovery key, then set up a way to sign in. An owner who
// has never signed in and an owner who has lost their way in do exactly the
// same thing, so they see exactly the same page.
(function () {
    'use strict';

    // ── The ceremony table ─────────────────────────────────────────────
    // CEREMONY-TABLE-BEGIN
    // One entry per ceremony KIND. Each knows how to run its kind's browser
    // half of "begin, then complete" for enrolment, and of sign-in.
    const CEREMONIES = {
        webauthn: {
            setUpLabel: 'Create a passkey',
            signInLabel: 'Sign in with a passkey',
            describe: 'a passkey on this device or a security key',
            supported() {
                return !!(window.PublicKeyCredential && navigator.credentials);
            },
            async enrol(name) {
                const options = await postJSON('/api/enrol/' + encodeURIComponent(name) + '/begin', {});
                const cred = await navigator.credentials.create({ publicKey: creationOptions(options.publicKey || options) });
                return postJSON('/api/enrol/' + encodeURIComponent(name) + '/complete', credentialJSON(cred));
            },
            async signIn() {
                const options = await postJSON('/api/signin/begin', {});
                const cred = await navigator.credentials.get({ publicKey: requestOptions(options.publicKey || options) });
                return postJSON('/api/signin/complete', credentialJSON(cred));
            },
        },
    };
    // CEREMONY-TABLE-END

    // ── WebAuthn JSON plumbing (base64url both ways) ───────────────────
    function b64urlToBuf(s) {
        const pad = '='.repeat((4 - (s.length % 4)) % 4);
        const bin = atob((s + pad).replace(/-/g, '+').replace(/_/g, '/'));
        const out = new Uint8Array(bin.length);
        for (let i = 0; i < bin.length; i++) out[i] = bin.charCodeAt(i);
        return out.buffer;
    }
    function bufToB64url(buf) {
        const bytes = new Uint8Array(buf);
        let bin = '';
        for (let i = 0; i < bytes.length; i++) bin += String.fromCharCode(bytes[i]);
        return btoa(bin).replace(/\+/g, '-').replace(/\//g, '_').replace(/=+$/, '');
    }
    function creationOptions(o) {
        if (window.PublicKeyCredential && PublicKeyCredential.parseCreationOptionsFromJSON) {
            return PublicKeyCredential.parseCreationOptionsFromJSON(o);
        }
        const out = Object.assign({}, o, {
            challenge: b64urlToBuf(o.challenge),
            user: Object.assign({}, o.user, { id: b64urlToBuf(o.user.id) }),
        });
        if (o.excludeCredentials) {
            out.excludeCredentials = o.excludeCredentials.map(c => Object.assign({}, c, { id: b64urlToBuf(c.id) }));
        }
        return out;
    }
    function requestOptions(o) {
        if (window.PublicKeyCredential && PublicKeyCredential.parseRequestOptionsFromJSON) {
            return PublicKeyCredential.parseRequestOptionsFromJSON(o);
        }
        const out = Object.assign({}, o, { challenge: b64urlToBuf(o.challenge) });
        if (o.allowCredentials) {
            out.allowCredentials = o.allowCredentials.map(c => Object.assign({}, c, { id: b64urlToBuf(c.id) }));
        }
        return out;
    }
    function credentialJSON(cred) {
        if (!cred) throw new Error('The browser returned no credential.');
        if (typeof cred.toJSON === 'function') return cred.toJSON();
        const r = cred.response;
        const response = { clientDataJSON: bufToB64url(r.clientDataJSON) };
        if (r.attestationObject) response.attestationObject = bufToB64url(r.attestationObject);
        if (r.authenticatorData) response.authenticatorData = bufToB64url(r.authenticatorData);
        if (r.signature) response.signature = bufToB64url(r.signature);
        if (r.userHandle) response.userHandle = bufToB64url(r.userHandle);
        return { id: cred.id, rawId: bufToB64url(cred.rawId), type: cred.type, response, clientExtensionResults: {} };
    }

    // ── HTTP ───────────────────────────────────────────────────────────
    class AuthError extends Error {
        constructor(status, body) {
            super((body && body.error) || ('HTTP ' + status));
            this.status = status;
            this.body = body || {};
        }
    }
    async function readJSON(res) {
        try { return await res.json(); } catch (e) { return {}; }
    }
    async function request(method, path, body) {
        const opts = { method, credentials: 'same-origin', headers: { 'Accept': 'application/json' } };
        if (body !== undefined) {
            opts.headers['Content-Type'] = 'application/json';
            opts.body = JSON.stringify(body);
        }
        const res = await fetch(path, opts);
        const json = await readJSON(res);
        if (!res.ok) throw new AuthError(res.status, json);
        return json;
    }
    const postJSON = (path, body) => request('POST', path, body);

    // ── Small helpers ──────────────────────────────────────────────────
    function esc(s) {
        return String(s == null ? '' : s).replace(/[&<>"']/g, c => ({ '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#39;' }[c]));
    }
    // ⭐ A provider's display name is its registry name, capitalised. Nothing
    // here keeps a per-provider label.
    function displayName(name) {
        const s = String(name || '').replace(/[-_]+/g, ' ');
        return s.charAt(0).toUpperCase() + s.slice(1);
    }
    function ceremonyFor(way) {
        const c = way && way.kind ? CEREMONIES[way.kind] : null;
        return c && c.supported() ? c : null;
    }
    function errorText(err) {
        if (err && err.name === 'NotAllowedError') return 'The browser prompt was dismissed or timed out.';
        if (err && err.name === 'InvalidStateError') return 'That authenticator is already set up here.';
        return (err && err.message) || String(err);
    }
    function remedyText(err) {
        return err && err.body && err.body.remedy ? err.body.remedy : '';
    }
    // The server's messages are lower-case fragments; shown to a person they
    // read as sentences.
    function sentence(s) {
        s = String(s || '').trim();
        if (!s) return '';
        s = s.charAt(0).toUpperCase() + s.slice(1);
        return /[.!?]$/.test(s) ? s : s + '.';
    }
    function siteLabel() {
        const base = (window.App && App.siteBaseUrl) || '';
        return base.replace(/^https?:\/\//, '').replace(/\/$/, '') || window.location.host;
    }
    async function copy(text, btn) {
        try {
            await navigator.clipboard.writeText(text);
            if (btn) {
                const was = btn.textContent;
                btn.textContent = 'Copied';
                btn.classList.add('copied');
                setTimeout(() => { btn.textContent = was; btn.classList.remove('copied'); }, 1500);
            }
        } catch (e) {
            if (window.App) App.showToast('Could not copy; select the text instead', 'error');
        }
    }

    // ── Which screen ───────────────────────────────────────────────────
    // ⛔ Decided from the gate's refusal REASON, never guessed from
    // capabilities. 200 means the caller is the owner already.
    async function check() {
        let res;
        try {
            res = await fetch('/api/providers', { credentials: 'same-origin', headers: { 'Accept': 'application/json' } });
        } catch (e) {
            return { owner: true };
        }
        if (res.ok) return { owner: true };
        const body = await readJSON(res);
        if (body.reason === 'no-provider' || body.reason === 'not-signed-in') {
            return { owner: false, reason: body.reason, waysIn: body.ways_in || [], remedy: body.remedy || '' };
        }
        // ⚠️ Any other answer is not an owner-auth refusal (an older server, a
        // proxy). Fall through to the app, which reports its own errors.
        return { owner: true };
    }

    let state = { waysIn: [], reason: '' };

    function screenEl() { return document.getElementById('owner-auth-screen'); }

    function frame(inner) {
        return `
            <header class="owner-auth-topbar">
                <span class="owner-auth-site">${esc(siteLabel())}</span>
            </header>
            <main class="owner-auth-column">${inner}</main>`;
    }

    function show(html) {
        const el = screenEl();
        el.innerHTML = frame(html);
        if (window.App) App.showScreen('auth');
        return el;
    }

    // ── SIGN IN ────────────────────────────────────────────────────────
    function renderSignIn(notice) {
        const usable = state.waysIn.filter(w => w.enrolled);
        const rows = usable.map(w => {
            const c = ceremonyFor(w);
            return c
                ? `<button class="primary owner-auth-wide" data-sign-in="${esc(w.name)}">${esc(c.signInLabel)}</button>`
                : `<p class="owner-auth-muted">${esc(displayName(w.name))} is set up, but this page cannot run its sign-in.</p>`;
        }).join('');
        const el = show(`
            <section class="owner-auth-card">
                <h1 class="owner-auth-title">Sign in</h1>
                <p class="owner-auth-lede">This is the owner&rsquo;s page for ${esc(siteLabel())}.</p>
                ${notice ? `<p class="owner-auth-notice">${esc(notice)}</p>` : ''}
                <div class="owner-auth-actions">${rows || '<p class="owner-auth-muted">Nothing that can sign you in here is set up.</p>'}</div>
                <p class="owner-auth-error hidden" id="owner-auth-error"></p>
                <p class="owner-auth-quiet"><a href="#" id="owner-auth-recover">Can&rsquo;t sign in?</a></p>
            </section>`);
        el.querySelector('#owner-auth-recover').addEventListener('click', (e) => {
            e.preventDefault();
            renderSetup();
        });
        el.querySelectorAll('[data-sign-in]').forEach(btn => {
            btn.addEventListener('click', async () => {
                const way = state.waysIn.find(w => w.name === btn.dataset.signIn);
                btn.disabled = true;
                try {
                    await ceremonyFor(way).signIn(way.name);
                    window.location.reload();
                } catch (err) {
                    btn.disabled = false;
                    showError(el, err);
                }
            });
        });
    }

    function showError(el, err) {
        const box = el.querySelector('#owner-auth-error');
        if (!box) return;
        box.textContent = [errorText(err), remedyText(err)].filter(Boolean).map(sentence).join(' ');
        box.classList.remove('hidden');
    }

    // ── SETUP (which is also RECOVERY) ─────────────────────────────────
    function oneLiner(ch) {
        // ⭐ The message travels through printf so its line breaks and its
        // final newline are exactly the bytes the server verifies. Domain and
        // nonce are hostname and base64url characters, so single quotes are
        // safe around them.
        const msg = ch.message.replace(/\n/g, '\\n');
        return `printf '${msg}' | ssh-keygen -Y sign -n ${ch.namespace} -f <your recovery key>`;
    }

    async function renderSetup() {
        const el = show(`
            <section class="owner-auth-card">
                <h1 class="owner-auth-title">Prove this site is yours</h1>
                <p class="owner-auth-lede">Setting up for the first time and getting back in after losing access are the
                    same step: sign a challenge with your recovery key, then set up a way to sign in.</p>
                <ol class="owner-auth-steps">
                    <li class="owner-auth-step" id="owner-auth-step-sign">
                        <h2>Sign this challenge</h2>
                        <div id="owner-auth-challenge"><p class="owner-auth-muted">Asking for a challenge&hellip;</p></div>
                    </li>
                    <li class="owner-auth-step" id="owner-auth-step-paste">
                        <h2>Paste the signature</h2>
                        <textarea id="owner-auth-signature" class="owner-auth-mono" rows="7" spellcheck="false"
                            placeholder="-----BEGIN SSH SIGNATURE-----"></textarea>
                        <div class="owner-auth-actions">
                            <button class="primary" id="owner-auth-verify">Verify</button>
                        </div>
                    </li>
                    <li class="owner-auth-step is-pending" id="owner-auth-step-enrol">
                        <h2>Set up a way to sign in</h2>
                        <div id="owner-auth-enrol"><p class="owner-auth-muted">Available once the signature checks out.</p></div>
                    </li>
                </ol>
                <p class="owner-auth-error hidden" id="owner-auth-error"></p>
                <details class="owner-auth-why">
                    <summary>Why a recovery key?</summary>
                    <p>This site holds only the public half of your recovery key, in <code>POLIS_RECOVERY_KEY</code>.
                        The private half never leaves your machine, so there is nothing here for anyone else to read.
                        Lost the key? Set a new public key on the instance, redeploy, and sign with the new one.</p>
                </details>
                ${state.reason === 'not-signed-in' ? '<p class="owner-auth-quiet"><a href="#" id="owner-auth-back">Back to sign in</a></p>' : ''}
            </section>`);
        const back = el.querySelector('#owner-auth-back');
        if (back) back.addEventListener('click', (e) => { e.preventDefault(); renderSignIn(); });

        let challenge = null;
        const loadChallenge = async () => {
            const box = el.querySelector('#owner-auth-challenge');
            try {
                challenge = await request('GET', '/api/recover/challenge');
            } catch (err) {
                box.innerHTML = `<p class="owner-auth-error">${esc([errorText(err), remedyText(err)].filter(Boolean).map(sentence).join(' '))}</p>`;
                return;
            }
            const line = oneLiner(challenge);
            box.innerHTML = `
                <p class="owner-auth-muted">Run this where your recovery key lives:</p>
                <pre class="owner-auth-mono owner-auth-code" id="owner-auth-command">${esc(line)}</pre>
                <div class="owner-auth-actions">
                    <button class="btn-copy" id="owner-auth-copy-command">Copy command</button>
                    <button class="btn-copy" id="owner-auth-copy-message">Copy message only</button>
                    <button class="btn-copy" id="owner-auth-new-challenge">New challenge</button>
                </div>
                <p class="owner-auth-hint">Or save the message to a file and run <code>${esc(challenge.sign_with)}</code>,
                    or: copy the message only and run <code>polis recover sign --domain ${esc(location.host)}</code>, which uses the key <code>polis branch</code> made.
                    This challenge works once and expires at ${esc(new Date(challenge.expires).toLocaleTimeString())}.</p>`;
            box.querySelector('#owner-auth-copy-command').addEventListener('click', (e) => copy(line, e.currentTarget));
            box.querySelector('#owner-auth-copy-message').addEventListener('click', (e) => copy(challenge.message, e.currentTarget));
            box.querySelector('#owner-auth-new-challenge').addEventListener('click', loadChallenge);
        };
        await loadChallenge();

        el.querySelector('#owner-auth-verify').addEventListener('click', async (e) => {
            const btn = e.currentTarget;
            const signature = el.querySelector('#owner-auth-signature').value.trim();
            if (!challenge || !signature) return;
            btn.disabled = true;
            el.querySelector('#owner-auth-error').classList.add('hidden');
            try {
                await postJSON('/api/recover/verify', { nonce: challenge.nonce, signature });
            } catch (err) {
                btn.disabled = false;
                showError(el, err);
                // ⚠️ A nonce is spent on every attempt, right or wrong.
                await loadChallenge();
                return;
            }
            el.querySelector('#owner-auth-step-sign').classList.add('is-done');
            el.querySelector('#owner-auth-step-paste').classList.add('is-done');
            el.querySelector('#owner-auth-step-enrol').classList.remove('is-pending');
            renderEnrolChoices(el);
        });
    }

    function renderEnrolChoices(el) {
        const box = el.querySelector('#owner-auth-enrol');
        const choices = state.waysIn.filter(w => w.enrollable);
        if (!choices.length) {
            box.innerHTML = '<p class="owner-auth-muted">Nothing on this instance can be set up as a way to sign in.</p>';
            return;
        }
        box.innerHTML = `<p class="owner-auth-muted">Proved. Choose how you will sign in from now on.</p>` +
            choices.map(w => {
                const c = ceremonyFor(w);
                return c
                    ? `<div class="owner-auth-choice">
                           <div><strong>${esc(displayName(w.name))}</strong><span class="owner-auth-muted"> &middot; ${esc(c.describe)}</span></div>
                           <button class="primary" data-enrol="${esc(w.name)}">${esc(c.setUpLabel)}</button>
                       </div>`
                    : `<div class="owner-auth-choice is-unsupported">
                           <div><strong>${esc(displayName(w.name))}</strong><span class="owner-auth-muted"> &middot; this page cannot run its set-up</span></div>
                       </div>`;
            }).join('');
        box.querySelectorAll('[data-enrol]').forEach(btn => {
            btn.addEventListener('click', async () => {
                const way = state.waysIn.find(w => w.name === btn.dataset.enrol);
                btn.disabled = true;
                try {
                    await ceremonyFor(way).enrol(way.name);
                } catch (err) {
                    btn.disabled = false;
                    showError(el, err);
                    return;
                }
                // ⭐ The enrolment session is spent; what it set up is the
                // way in from here, so sign in with it.
                way.enrolled = true;
                state.reason = 'not-signed-in';
                renderSignIn(displayName(way.name) + ' is set up. Sign in with it to continue.');
            });
        });
    }

    // ── PROVIDER LIST (Settings) ───────────────────────────────────────
    async function renderProviderList(container) {
        if (!container) return;
        let data;
        try {
            data = await request('GET', '/api/providers');
        } catch (err) {
            container.innerHTML = '';
            return;
        }
        const providers = data.providers || [];
        // ⭐ Trust that comes from how the instance is run (loopback on a
        // laptop, an operator upstream) is never a session to end.
        const ambient = providers.some(p => p.enrolled && !p.enrollable);
        const rows = providers.map(p => {
            const c = ceremonyFor(p);
            let status, statusClass, action = '';
            if (!p.available) {
                status = 'Not available on this instance';
                statusClass = 'is-unavailable';
            } else if (p.enrolled) {
                status = p.enrollable ? 'Set up' : 'Applies automatically here';
                statusClass = 'is-on';
                if (p.enrollable) action = `<button class="btn-danger-sm" data-remove="${esc(p.name)}">Remove</button>`;
            } else {
                status = 'Not set up';
                statusClass = 'is-off';
                if (p.enrollable && c) action = `<button class="btn-copy" data-setup="${esc(p.name)}">${esc(c.setUpLabel)}</button>`;
            }
            return `
                <div class="settings-row owner-way ${statusClass}">
                    <span class="settings-row-label owner-way-name">${esc(displayName(p.name))}</span>
                    <span class="owner-way-status">${esc(status)}</span>
                    <div class="settings-row-actions">${action}</div>
                </div>`;
        }).join('');
        const recovery = data.recovery_configured
            ? 'A recovery key is configured. If you lose every way in, &ldquo;Can&rsquo;t sign in?&rdquo; on the sign-in page gets you back.'
            : 'No recovery key is configured. If you lose every way in, you would set <code>POLIS_RECOVERY_KEY</code> on the instance and redeploy first.';
        container.innerHTML = `
            <div class="settings-section" id="settings-ways-in">
                <div class="settings-section-label">Ways to sign in</div>
                <div class="settings-card">
                    ${rows || '<div class="settings-row"><span class="owner-way-status">Nothing is registered.</span></div>'}
                    <div class="settings-row settings-row-stacked">
                        <span class="owner-way-note">${recovery}</span>
                    </div>
                    ${ambient ? '' : `
                    <div class="settings-row">
                        <span class="owner-way-note">Signed in on this browser.</span>
                        <div class="settings-row-actions">
                            <button class="btn-copy" data-sign-out="">Sign out</button>
                            <button class="btn-copy" data-sign-out="everywhere">Sign out everywhere</button>
                        </div>
                    </div>`}
                </div>
            </div>`;
        container.querySelectorAll('[data-remove]').forEach(btn => {
            btn.addEventListener('click', () => removeProvider(btn.dataset.remove, providers, data, container));
        });
        container.querySelectorAll('[data-setup]').forEach(btn => {
            btn.addEventListener('click', () => setUpFromSettings(btn.dataset.setup, providers, container));
        });
        container.querySelectorAll('[data-sign-out]').forEach(btn => {
            btn.addEventListener('click', async () => {
                const q = btn.dataset.signOut === 'everywhere' ? '?everywhere=1' : '';
                try { await postJSON('/api/signout' + q); } catch (e) { /* the reload says where we stand */ }
                window.location.href = (window.App ? App.basePath : '/_') + '/';
            });
        });
    }

    // ⭐ Removing or adding a way in outlives the session, so the server may ask
    // for a fresh sign-in. Do it in place, with whatever is set up, then retry.
    async function withFreshSignIn(providers, act) {
        try {
            return await act();
        } catch (err) {
            if (err.status !== 401) throw err;
            const way = providers.find(p => p.enrolled && ceremonyFor(p));
            if (!way) throw err;
            await ceremonyFor(way).signIn(way.name);
            return act();
        }
    }

    async function setUpFromSettings(name, providers, container) {
        const way = providers.find(p => p.name === name);
        try {
            await withFreshSignIn(providers, () => ceremonyFor(way).enrol(name));
            if (window.App) App.showToast(displayName(name) + ' is set up', 'success');
        } catch (err) {
            if (window.App) App.showToast(errorText(err), 'error');
        }
        renderProviderList(container);
    }

    async function removeProvider(name, providers, data, container) {
        const del = (ack) => request('DELETE', '/api/providers?name=' + encodeURIComponent(name) + (ack ? '&acknowledge=recovery' : ''));
        try {
            await withFreshSignIn(providers, () => del(false));
        } catch (err) {
            // ⛔ D6: the last way in may be removed, but only once the owner
            // has seen how they would get back in and said so.
            if (err.status === 409 && err.body.consequence) {
                const ok = await confirmLastRemoval(name, err.body, data.recovery_configured);
                if (!ok) return;
                try {
                    await del(true);
                } catch (err2) {
                    if (window.App) App.showToast(errorText(err2), 'error');
                    return;
                }
                // Nothing lets this browser in now; the reload shows the
                // setup screen, which is the recovery path just described.
                window.location.href = (window.App ? App.basePath : '/_') + '/';
                return;
            }
            if (window.App) App.showToast([errorText(err), remedyText(err)].filter(Boolean).map(sentence).join(' '), 'error');
            return;
        }
        if (window.App) App.showToast(displayName(name) + ' removed', 'success');
        renderProviderList(container);
    }

    // ── REMOVAL CONFIRMATION ───────────────────────────────────────────
    function confirmLastRemoval(name, body, recoveryConfigured) {
        return new Promise((resolve) => {
            const modal = document.createElement('div');
            modal.className = 'modal-overlay';
            modal.innerHTML = `
                <div class="modal confirm-modal owner-auth-modal" role="dialog" aria-modal="true" aria-labelledby="owner-auth-modal-title">
                    <div class="modal-header">
                        <h3 id="owner-auth-modal-title">Remove ${esc(displayName(name))}?</h3>
                        <button class="modal-close" data-action="cancel" aria-label="Close">&times;</button>
                    </div>
                    <div class="modal-body">
                        <p><strong>${esc(sentence(body.error || 'this is the last way you can sign in'))}</strong>
                            ${esc(sentence(body.consequence))}</p>
                        <div class="owner-auth-recovery-path">
                            <div class="owner-auth-recovery-label">How you would get back in</div>
                            <ol>
                                ${recoveryConfigured ? '' : '<li>Set <code>POLIS_RECOVERY_KEY</code> on the instance to your recovery public key, and redeploy. <strong>None is set now.</strong></li>'}
                                <li>Open this site and choose <em>Can&rsquo;t sign in?</em></li>
                                <li>Sign the challenge it shows with your recovery key.</li>
                                <li>Set up a new way to sign in.</li>
                            </ol>
                            <p class="owner-auth-muted">${esc(sentence(body.remedy))}</p>
                        </div>
                        <label class="owner-auth-ack">
                            <input type="checkbox" id="owner-auth-ack">
                            <span>I understand I will be signed out, and how to get back in.</span>
                        </label>
                    </div>
                    <div class="modal-footer">
                        <button class="secondary" data-action="cancel">Keep it</button>
                        <button class="danger" data-action="confirm" disabled>Remove anyway</button>
                    </div>
                </div>`;
            const done = (v) => { modal.remove(); document.removeEventListener('keydown', onKey); resolve(v); };
            const onKey = (e) => { if (e.key === 'Escape') done(false); };
            const confirmBtn = modal.querySelector('[data-action="confirm"]');
            modal.querySelector('#owner-auth-ack').addEventListener('change', (e) => { confirmBtn.disabled = !e.target.checked; });
            modal.querySelectorAll('[data-action="cancel"]').forEach(b => b.addEventListener('click', () => done(false)));
            confirmBtn.addEventListener('click', () => done(true));
            modal.addEventListener('click', (e) => { if (e.target === modal) done(false); });
            document.addEventListener('keydown', onKey);
            document.body.appendChild(modal);
            modal.querySelector('#owner-auth-ack').focus();
        });
    }

    // ── Entry point ────────────────────────────────────────────────────
    // Returns true when an auth screen was shown and the app must not boot.
    async function gate() {
        const result = await check();
        if (result.owner) return false;
        state = { waysIn: result.waysIn, reason: result.reason };
        if (result.reason === 'no-provider') {
            await renderSetup();
        } else {
            renderSignIn();
        }
        return true;
    }

    window.PolisOwnerAuth = { gate, renderProviderList, renderSetup, renderSignIn };
})();
