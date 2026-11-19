# Security Policy

## Supported versions

Security fixes apply to the latest minor release line of each component and the `main` branch. Fixes are released in new versions and are not backported to older minor lines.

| Component | Supported versions |
| --- | --- |
| TokenRouter | `0.7.x` (latest release) and `main` |
| `plugin-sdk/go` | `0.2.x` (latest release) |
| `plugin-sdk/web` | `0.3.x` (latest release) |
| Older releases | Unsupported |

## Reporting a vulnerability

Do not report security vulnerabilities in public GitHub issues, pull requests, or Discord channels.

Submit reports privately through [GitHub Private Vulnerability Reporting](https://github.com/TokenFlux/TokenRouter/security/advisories/new).

If you cannot use GitHub Private Vulnerability Reporting, email [security@tokenflux.com](mailto:security@tokenflux.com) with the details below.

## What to include

Include enough detail for maintainers to reproduce and assess the issue:

- The affected component and exact version, commit SHA, or container image tag (do not use `latest`).
- A description of the vulnerability and its expected security impact.
- Step-by-step reproduction instructions, a minimal proof of concept, or redacted HTTP requests and logs.
- Relevant configuration or deployment details, and a suggested fix if you have one.

Redact live API keys, tokens, credentials, and personal data before submitting.

## Disclosure and CVE process

1. Maintainers aim to acknowledge private reports within 5 business days and share progress updates during triage and remediation.
2. Confirmed vulnerabilities are tracked in a private GitHub Security Advisory, and patches are prepared in a private fork before release.
3. For confirmed vulnerabilities affecting supported versions of TokenRouter, maintainers can request a CVE identifier through GitHub Security Advisories (with GitHub acting as the CVE Numbering Authority) and credit the reporter in the published advisory unless anonymity is requested.
4. Keep vulnerability details confidential until a patched release is published, or until an agreed disclosure date (typically 90 days after confirmation).

## Scope

In scope: unmodified releases and `main` branch code in `TokenFlux/TokenRouter`, including the backend, frontend, official deployment scripts, and `plugin-sdk/`.

Out of scope:

- Third-party hosted services, API relays, or modified forks unless reproduced against the unmodified repository.
- Vulnerabilities in upstream model providers, payment processors, or dependencies without a demonstrated impact on TokenRouter.
- Code execution performed by an administrator-installed plugin, because plugins run as administrator-trusted code without an OS or browser sandbox. Unauthorized users gaining the ability to install, modify, or execute plugins remain in scope.
- Denial-of-service findings caused by missing reverse-proxy or network-edge controls, such as exposing the server directly to the public internet with `server.client_ip.trust_forwarded_headers=true`.
- Reports without a demonstrated security impact, such as missing headers alone or automated scanner output without verification.
