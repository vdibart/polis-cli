# Security Policy

> **Scope:** This file documents the vulnerability reporting policy only. For the deeper security docs, see:
> - [`security-model.md`](security-model.md) — full threat model, cryptographic design (Ed25519 signing, key continuity / proto-TOFU), trust model, attack vectors
> - [`dm-encryption.md`](dm-encryption.md) — direct-message encryption design
> - [`registration-and-privacy.md`](registration-and-privacy.md) — discovery-service registration, hard delete, privacy guarantees

## Supported Versions

| Version | Supported          |
| ------- | ------------------ |
| 0.50.x+ | :white_check_mark: |
| < 0.50  | :x:                |

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

This security policy applies to:
- The Go CLI (`cli-go/cmd/polis/`)
- The Bash CLI (`cli-bash/bin/polis`)
- The webapp (`webapp/`)
- Associated configuration and metadata files

### Out of Scope

- Issues in third-party dependencies (please report to the respective projects)
- Social engineering attacks
- Denial of service attacks

## Security Best Practices for Users

1. **Verify downloads**: Always verify the SHA256 checksum after downloading
   ```bash
   sha256sum -c polis.sha256
   ```

2. **Protect your keys**: Your Ed25519 private key in `.polis/keys/` should never be shared

3. **Use HTTPS**: Always use HTTPS URLs for your `POLIS_BASE_URL`

4. **Review before blessing**: Always preview comments before blessing them
   ```bash
   polis preview <comment-url>
   ```
