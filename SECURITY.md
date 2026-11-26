# Security Policy

## Supported versions

Security fixes are made for the latest release. Please upgrade before reporting.

## Reporting a vulnerability

**Please do not open public issues for security problems.**

Report privately through [GitHub Security Advisories](https://github.com/nikolap994/magnetar/security/advisories/new).
Include:

- the affected version (`magnetar --version`) and operating system,
- a description of the issue and its impact,
- steps to reproduce or a proof of concept.

You can expect an acknowledgement within 72 hours and a fix or mitigation plan within 14 days for confirmed issues.
We will credit you in the release notes unless you prefer otherwise.

## Scope

In scope: the Magnetar binary, its web UI and API, release artifacts and the build pipeline.
Out of scope: vulnerabilities in the BitTorrent protocol itself, and attacks that require an attacker who can already
run code as your OS user.

## Verifying releases

All release artifacts are signed with Sigstore and have build-provenance attestations. See
[docs/SIGNING.md](docs/SIGNING.md).
