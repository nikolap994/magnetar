# Changelog

All notable changes to this project are documented here. The format follows
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/) and the project uses [Semantic Versioning](https://semver.org/).

## [Unreleased]

## [0.6.0] - 2026-11-27

### Added
- Find subtitles for a video in the built-in player through OpenSubtitles, using your own API key; saved subtitles
  are remembered and offered automatically next time.
- Allow Magnetar through Windows Defender Firewall with one approval, from the connection help and phone-access
  settings. A blocked firewall is a common reason torrents find no peers and phones cannot connect.

### Changed
- Fresh torrents connect to peers much faster, so large public torrents start downloading quickly instead of
  trickling in a few connections at a time.
- A magnet link clicked while the interface is open in an ordinary browser tab now brings Magnetar's own window to
  the front.

### Fixed
- Updating Magnetar no longer recreates a desktop shortcut you had deleted.
- The HTTPS toggle is no longer crowded against the QR code in phone-access settings.

## [0.5.0] - 2026-11-27

### Added
- Built-in search through your own indexers (Prowlarr, Jackett and other Torznab sources): results from all of
  them at once, sorted by seeders, added through the usual preview. Magnetar includes no search sources.
- Play downloads on your TV: Magnetar can appear as a media server (DLNA) on your home network, so TVs, consoles
  and players like VLC can browse and play videos, music and photos, also while they download. Off by default.
- Tags: give torrents several labels, filter by them in the sidebar, and set them when adding.
- Per-torrent speed limits on top of the global limits.
- When downloads finish: quit Magnetar, put the computer to sleep or shut it down (with a minute to cancel), and
  optionally run a program for every finished download.
- Phone notifications through ntfy, Telegram or a webhook (Discord, Slack, Home Assistant) when downloads finish
  or have problems.
- Trackers tab with each tracker's status, removing trackers, and optional extra trackers for new public torrents
  (with a current public list one click away).
- Ban peers and connect to a peer by address.
- Swarm health: availability of each download and a warning when some pieces are not available from any peer yet.
- Disk protection: downloads pause with a clear message when free space runs low (1 GiB by default).
- An incomplete-downloads folder: unfinished files are kept apart and moved when they complete.
- Backup and restore of settings, the torrent list, RSS rules, statistics and banned peers.
- HTTPS for phone access with a self-signed certificate and its fingerprint shown for checking.
- Optionally offer to add magnet links you copy.

## [0.4.0] - 2026-11-27

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
- RSS feeds with automatic download rules: word or regular-expression filters, exclusions, a smart episode
  filter that takes each episode only once, and per-rule category, folder and paused state.
- Create torrents from a file or folder, with trackers, web seeds, piece size and private flag, then save the
  .torrent file, copy its magnet link or start seeding right away.
- Import your torrents from qBittorrent, Transmission or uTorrent with their folders, file choices, categories and
  statistics; files already on disk keep seeding.
- Watch folder: .torrent files saved into it are added automatically.
- Phone access: one switch opens Magnetar to devices on your home network (password required), with a QR code to
  scan, and a phone-friendly layout.
- qBittorrent-compatible API (opt-in) so Sonarr, Radarr, Prowlarr and similar apps can use Magnetar.
- macOS app for Apple silicon and Intel, and a Docker image for servers and NAS devices.
- Pause updates for a week, two weeks, a month or until you resume them (off by default).
- Clearer connection status: "Ready" when nothing is active, faster detection of an unreachable port.
- File type icons everywhere, and a folder tree in the add dialog with folder sizes and whole-folder selection.
- Compact list view.
- Folder tree in the Files tab, with priorities for whole folders and a filter for big torrents. Paused and queued
  torrents show their files too.

### Performance
- Speed limits no longer cut torrents off from peers: while a download limit was reached, no new peer connections
  were allowed, so limited downloads used only web seeds and seeding torrents could not gain peers. Limits stay exact.
- Downloads are written with regular file I/O: the same speed, and memory use stays flat (about 70 MiB while
  downloading a 790 MB file, instead of growing to its size). Files changed by other programs can no longer crash
  Magnetar.

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
- Magnet links open a preview in the main window (name, size and files; the raw link is one
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
