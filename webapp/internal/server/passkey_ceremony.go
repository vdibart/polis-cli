package server

// PasskeyCeremony is the ceremony kind the passkey provider reports.
//
// ⭐ The SPA runs WebAuthn for any provider of this kind, so the browser code is
// keyed by what the ceremony IS rather than by which provider asked for it.
const PasskeyCeremony = "webauthn"

// Ceremony implements owner.Ceremonial.
func (*PasskeyProvider) Ceremony() string { return PasskeyCeremony }
