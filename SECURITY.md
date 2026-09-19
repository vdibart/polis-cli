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

Instead, please email security concerns to: **vdibart@duck.com**

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

### Out of Scope

- Issues in third-party dependencies (please report to the respective projects)
- Social engineering attacks
- Denial of service attacks

## Security Best Practices for Users

1. **Verify downloads**: Each release publishes a `checksums.txt` of SHA-256 sums for its archives. Download it beside the archive and check:
   ```bash
   sha256sum -c --ignore-missing checksums.txt
   ```

2. **Protect your keys**: Your Ed25519 private key in `.polis/keys/` should never be shared

3. **Use HTTPS**: Always use HTTPS URLs for your `POLIS_BASE_URL`

4. **Review before blessing**: Always preview comments before blessing them
   ```bash
   polis preview <comment-url>
   ```
