# Security Policy

> **Scope:** This file documents the vulnerability reporting policy only. For the deeper security docs, see:
> - [`security-model.md`](https://github.com/vdibart/polis-cli/blob/main/docs/general/security/security-model.md) — full threat model, cryptographic design (Ed25519 signing, key continuity / proto-TOFU), trust model, attack vectors
> - [`dm-encryption.md`](https://github.com/vdibart/polis-cli/blob/main/docs/general/security/dm-encryption.md) — direct-message encryption design
> - [`registration-and-privacy.md`](https://github.com/vdibart/polis-cli/blob/main/docs/general/security/registration-and-privacy.md) — discovery-service registration, what unregistering removes, privacy guarantees

## Supported Versions

Security fixes are made in the **latest release only**. There are no maintenance branches, so a fix reaches
you by upgrading. Earlier releases are not patched.

## Reporting a Vulnerability

We take security seriously. If you discover a security vulnerability in Polis CLI, please report it responsibly.

### How to Report

**Please do NOT report security vulnerabilities through public GitHub issues.**

Instead, please email security concerns to: **security@polis.pub**, or use **Report a vulnerability** on the
[Security tab of polis-cli](https://github.com/vdibart/polis-cli/security), which opens a private advisory only you and the
maintainers can see.

The same contact is published in machine-readable form at
[`https://polis.pub/.well-known/security.txt`](https://polis.pub/.well-known/security.txt) (RFC 9116), which points back
to this page as its policy. It is served on polis.pub's root domain only: a site at `<name>.polis.pub` is its owner's, and
polis.pub does not publish a contact on their behalf.

Include the following in your report:
- Description of the vulnerability
- Steps to reproduce the issue
- Potential impact
- Any suggested fixes (optional)

### What to Expect

- **Acknowledgment**: We will acknowledge receipt of your report within 48 hours
- **Updates**: We will provide updates on our progress within 7 days
- **Resolution**: We aim to resolve critical vulnerabilities within 30 days
- **Credit**: With your permission, we will credit you in the security advisory

### Scope

This security policy applies to the polis project, meaning the code in this repository:
- The Go CLI (`cli-go/cmd/polis/`)
- The Bash CLI (`cli-bash/`)
- The webapp (`webapp/`)
- Associated configuration and metadata files

It also applies to two services operated alongside the project:
- **polis.pub**, the hosted service. ⚠️ **polis.pub is not the polis project or this repository.** It is a service that runs
  polis for people who do not want to host it themselves, and the code that operates it is not in this repository.
- **The discovery service** that polis.pub operates. Its source is not in this repository either.

For these two services you cannot point at code, so describe the behaviour you observed: the request, the response, and
why it is a vulnerability.

**Vulnerabilities in dependencies.** polis checks its dependencies for known vulnerabilities before every release and
every deploy. A vulnerability in a dependency is in scope when polis's own code reaches it: for example, a flaw in the
MCP SDK or in the OAuth library behind polis.pub's sign-in server that a request to polis can trigger. Report it here; we
fix or work around it in polis and coordinate with the dependency's maintainers. A flaw in a dependency that polis's code
does not reach belongs with that project.

### Out of Scope

- Vulnerabilities in third-party dependencies that polis's code does not reach (please report them to the respective
  projects)
- Social engineering attacks
- Denial of service attacks

## Security Best Practices for Users

1. **Verify downloads**: Each release publishes a `checksums.txt` of SHA-256 sums for its files and, from the first
   signed release on, `checksums.txt.sig`: a signature over it by the polis release key. The key's public half is
   [`release/release-signing-key.pub`](https://github.com/vdibart/polis-cli/blob/main/release/release-signing-key.pub)
   in this repository. Download `checksums.txt`, `checksums.txt.sig` and the archive into one directory, then check the
   signature first and the archive second:
   ```bash
   echo "polis-release namespaces=\"polis-release\" $(cut -d' ' -f1-2 release-signing-key.pub)" > allowed_signers
   ssh-keygen -Y verify -f allowed_signers -I polis-release -n polis-release -s checksums.txt.sig < checksums.txt
   sha256sum -c --ignore-missing checksums.txt
   ```
   The first command must print `Good "polis-release" signature`. A checksum alone shows that a download arrived
   intact; the signature shows it is the file the release key signed. Releases made before signing began carry no
   `checksums.txt.sig`, and their checksums show only the first. Each archive also has a software bill of materials
   beside it (`<archive>.sbom.json`, SPDX), listed in the same `checksums.txt`, so the signature covers it too.

   The installer (`scripts/install.sh`, the one-line install in the README) does both checks itself, with the
   release key pinned in the script, and installs nothing if either fails. It refuses a release that carries no
   signature unless you set `POLIS_ALLOW_UNSIGNED=1`, and without `ssh-keygen` (OpenSSH 8.0+) it stops and points
   here, so you can run the steps above by hand. `polis-upgrade` checks the same signature.

2. **Protect your keys**: Your Ed25519 private key in `.polis/keys/` should never be shared

3. **Use HTTPS**: Always use HTTPS URLs for your `POLIS_BASE_URL`

4. **Review before blessing**: Always preview comments before blessing them
   ```bash
   polis preview <comment-url>
   ```
