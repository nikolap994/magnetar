# Contributing to Magnetar

Thanks for helping make Magnetar better!

## Development setup

```sh
git clone https://github.com/nikolap994/magnetar.git
cd magnetar
make run          # builds and starts Magnetar, opening the UI
make test lint    # what CI runs
```

Useful flags while developing: `--dir ./data` keeps settings and sessions out of your real profile,
`--no-browser` skips opening a tab, and `--verbose` turns on debug logs. The web UI lives in `web/static/` and is
embedded at build time, so just rebuild after editing it.

## Guidelines

- **Keep the UI dependency-free.** No frameworks, CDNs or npm packages. Render untrusted text with `textContent`, never
  `innerHTML`, and keep everything compatible with the strict Content-Security-Policy.
- **Security first.** Any new endpoint must sit behind `authed(...)`, mutate state only via non-GET methods, and limit
  request sizes.
- **Tests.** Add or update tests for behaviour changes (`go test ./...`).
- **Formatting.** Run `make fmt`; CI rejects unformatted code.
- **Commits.** Use clear, imperative messages; [Conventional Commits](https://www.conventionalcommits.org/)
  (`feat:`, `fix:`, `docs:` …) are preferred because they generate the release notes.

## Releasing (maintainers)

```sh
git tag -a v0.2.0 -m "v0.2.0"
git push origin v0.2.0
```

The release workflow runs `make release`, signs the checksums with Sigstore, attests build provenance and publishes
the GitHub release with the notes from `CHANGELOG.md`.

## Code of conduct

Be respectful and constructive. Harassment or discrimination of any kind is not tolerated; maintainers may remove
comments, commits or contributors that violate this. This project follows the spirit of the
[Contributor Covenant](https://www.contributor-covenant.org/version/2/1/code_of_conduct/).
