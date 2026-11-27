# Changelog

All notable changes to this project are documented here. The format follows
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/) and the project uses [Semantic Versioning](https://semver.org/).

## [Unreleased]

### Added
- Play video and audio inside Magnetar while they download: the pieces you are watching are fetched first, seeking
  works, and subtitles (.srt/.vtt) next to the video are offered in the player.
- Quick look for images and text files (subtitles, .nfo, readme).
- Open file, Show in folder and Open folder. Programs, scripts and web pages from torrents are never opened
  directly; these actions only work on the computer running Magnetar.
- VPN kill switch: bind Magnetar to a network connection (for example your VPN) by name. It follows the VPN's
  address when it reconnects, and all torrent traffic stops while the VPN is down.
- Proxy support (SOCKS5 and HTTP) for trackers and web seeds, and with SOCKS5 optionally for peer connections.
- Download queue: by default at most 3 downloads, 3 uploads and 5 torrents are active; the rest wait as "Queued"
  and start by themselves. Force start, queue order (top, up, down, bottom) and "don't count slow torrents".

### Fixed
- Opening a .torrent file now shows the add dialog (name, size, file picker, folder) instead of starting the
  download right away.

## [0.3.1] - 2026-11-27

### Security
- Stricter path validation: any ".." in a user-chosen folder or a torrent's file path is refused, checked before the
  path is cleaned.
- Torrent names and error messages are stripped of control characters before they are written to the log.

### Changed
- Folder names that contain two consecutive dots (for example "Season 1..3") can no longer be used as download
  folders.

## [0.3.0] - 2026-11-27

### Added
- Choose files before downloading: the add dialog fetches the torrent's file list (metadata only, nothing is written
  to disk) and shows sizes, a running total and the free space in the chosen folder.
- Categories with optional folders: sidebar filter, picker when adding, assignment from the context menu and the
  bulk bar, editor in Settings.
- Move files to another folder ("Set location") while keeping seeding, and optionally move completed downloads.
- Connection status in the sidebar (connectable, checking, port not reachable, no network) with fix-it help.
- Native notifications when a download completes and live speeds in the tray tooltip.
- Speed schedule for the alternative limits (time window and weekdays) and a seed time limit.
- Statistics: all-time and session totals and a 24-hour bandwidth chart with a table view.
- Alternative limits can be edited right under the turtle switch; speed-limit fields pick KiB/s, MiB/s or GiB/s.

### Changed
- Magnet links open a qBittorrent-style preview in the main window (name, size and files; the raw link is one
  click away), and Magnetar stays open after adding.
- Only one window: tray Open and magnet links bring the existing window to the front instead of opening another.

### Fixed
- Quitting from the tray now also closes the app window.

### Distribution
- Releases include a source archive with all dependencies for building offline.
- A project website at <https://nikolap994.github.io/magnetar/>.

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
