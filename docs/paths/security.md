# Reading path: security

What polis protects, what it assumes, where it stops, and how to check it yourself. Each step says what it adds to the one
before; the pages themselves hold the detail. For the whole reviewer's door, see
[the documentation's front page](../README.md#reviewing-the-security-and-identity-design).

1. [How polis thinks about identity and trust](../signet/overview.md): the design on one page, including *What we haven't solved*.
2. [Security model](../general/security/security-model.md): the cryptographic foundations ([§1](../general/security/security-model.md#1-cryptographic-foundations)), key management ([§2](../general/security/security-model.md#2-key-management)) and the attack analysis ([§7](../general/security/security-model.md#7-attack-vectors-and-mitigations)).
3. [Known issues](../general/security/known-issues.md): [a signature proves the key, not the person](../general/security/known-issues.md#2-a-signature-proves-the-key-not-the-person).
4. [Signing base](../signet/spec/signing-base.md): exactly which bytes a signature covers, and the traps around them.
5. [Key history](../signet/spec/key-history.md): what a key chain [cannot prove, and who can](../signet/spec/key-history.md#5-what-the-chain-cannot-prove-and-who-can), and [what it does not do](../signet/spec/key-history.md#8-what-this-does-not-do).
6. [Custody](../signet/spec/custody.md): what an operator holds, and why nothing published can stop it ([expected, never allowed](../signet/spec/custody.md#2--expected-never-allowed); [a tenant's key](../signet/spec/custody.md#12-custody-of-a-tenants-key--the-tenant-half)). The same question for the person whose key it is: [who holds your key](../general/security/who-holds-your-key.md).
7. [Delegation](../signet/concepts/delegation.md): why an agent signs with the user's key and marks every act, then the spec's [what the user-agent design does not claim](../signet/spec/delegation.md#8-what-this-design-does-not-claim).
8. [Witnesses](../signet/spec/witness.md): [the discovery service's side, stated honestly](../signet/spec/witness.md#8-the-discovery-services-side-stated-honestly).
9. [The actors](../general/concepts/actors.md): what runs beside you on a hosted service, and what each may not do.
10. [Direct-message encryption](../general/security/dm-encryption.md): what it protects, and what it does not; the bytes are in the [at-rest format](../../cli-go/pkg/dm/FORMAT.md) and the [delivery protocol](../../cli-go/pkg/dm/PROTOCOL.md).
11. [Registration and privacy](../general/security/registration-and-privacy.md): what the discovery service learns about you.
12. [Check a site](../signet/guides/verify-content.md), then [recipe 5](../signet/recipes/05-verify-a-site.md): **verify a site yourself, trusting nothing.** [Recipe 14](../signet/recipes/14-verify-signed-edges.md) does it for a follow file with nothing but `curl`, `jq` and OpenSSH.
13. [Report a vulnerability](../general/security/SECURITY.md).
