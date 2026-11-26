# Changelog

All notable changes to this project are documented here. The format follows
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/) and the project uses [Semantic Versioning](https://semver.org/).

## [0.2.0] - 2026-11-26

### Added
- In-app updater: daily check of GitHub Releases (can be disabled), release notes in the app, one-click
  install & restart. Updates are verified with an Ed25519 release key compiled into Magnetar and a signed
  SHA-256 list before anything is installed; Windows installs run the setup silently, `.deb` installs use
  `pkexec apt-get`, portable builds swap the executable in place.

### Security
- Paths from torrent metadata can no longer point outside the save folder when checking for existing data.
- Stricter validation of user-supplied folders (no `..`, NUL bytes or Windows device paths).
- Logout cookie carries the `Secure` attribute like the session cookie; log file close errors are handled.

## [0.1.1] - 2026-11-26

### Added
- Author credit and GitHub links in the sidebar, empty state and Settings → About.

### Changed
- "No torrents yet" is now centred in the window.

## [0.1.0] - 2026-11-26

### Added
- First public release.
- BitTorrent engine with magnet links, `.torrent` files, DHT, PEX, LSD, µTP, UPnP, web seeds and v2 hybrid torrents.
- Modern web UI: filters, search, sorting, bulk actions, context menu, detail panel with files/peers/trackers and
  piece map, drag & drop, paste-anywhere, keyboard shortcuts, notifications, dark and light themes.
- Per-file priorities, sequential download, speed limits, seed-ratio limit.
- Security: protocol encryption policy, VPN/IP binding, IP blocklists, anonymous mode, loopback-only UI with one-time
  launch links, Argon2id passwords, CSRF/DNS-rebinding protection and strict CSP.
- Desktop client: app window, tray icon (open, pause/resume all, alternative speed, quit), start at sign-in.
- Windows installer (per-user, no admin) with Start Menu/desktop shortcuts, magnet/.torrent association and a clean
  uninstaller; Ubuntu/Debian `.deb` package with desktop entry, icons and AppStream metadata.
- Magnet links opened from a browser show an "Add torrent" window to choose the download folder.
- Folder browser for choosing download locations; remembered recent folders.
- Re-adding a removed torrent verifies existing files and resumes instead of starting over.
- Alternative ("turtle") speed limits with a one-click toggle, quick speed-limit dialog with presets.
- `register`/`unregister` commands for magnet and `.torrent` handling on Windows and Linux.
- Speed-limit fields pick KiB/s, MiB/s or GiB/s automatically.
- Signed, reproducible release builds for Linux and Windows (amd64, arm64), built with Go 1.26.11 and gated by
  `govulncheck`.
