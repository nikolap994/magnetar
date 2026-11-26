# Changelog

All notable changes to this project are documented here. The format follows
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/) and the project uses [Semantic Versioning](https://semver.org/).

## [0.1.0] — 2026-11-26

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
- Signed, reproducible release builds for Linux and Windows (amd64, arm64).
