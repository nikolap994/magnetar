// Package config loads, validates and persists Magnetar's settings.
package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/nikolap994/magnetar/internal/fsutil"
)

// Encryption policies for peer connections (BitTorrent protocol encryption / MSE).
const (
	EncryptionPrefer  = "prefer"  // Try encrypted first, fall back to plaintext.
	EncryptionRequire = "require" // Only talk to peers that support encryption.
	EncryptionAllow   = "allow"   // Prefer plaintext but accept encrypted peers.
)

// Proxy types.
const (
	ProxySOCKS5 = "socks5"
	ProxyHTTP   = "http"
)

func hasControl(s string) bool {
	return strings.IndexFunc(s, func(r rune) bool { return r < 32 || r == 127 }) >= 0
}

// ProxyURL returns the proxy as a URL for HTTP clients, or nil if none is set.
func (s Settings) ProxyURL() *url.URL {
	if s.ProxyType == "" {
		return nil
	}
	u := &url.URL{Scheme: s.ProxyType, Host: net.JoinHostPort(s.ProxyHost, strconv.Itoa(s.ProxyPort))}
	if s.ProxyUsername != "" {
		u.User = url.UserPassword(s.ProxyUsername, s.ProxyPassword)
	}
	return u
}

// Settings are the user-tunable options. Every field is exposed through the web UI.
type Settings struct {
	// Storage
	DownloadDir string `json:"downloadDir"`

	// Network
	ListenPort  int    `json:"listenPort"`
	BindAddress string `json:"bindAddress"` // IP to bind peer traffic to ("" = all). Use your VPN interface IP to keep traffic on the VPN.
	// Network interface (by name, e.g. "wg0" or "NordLynx") to bind peer traffic
	// to. Unlike BindAddress it follows the interface's address across VPN
	// reconnects; while the interface is down all traffic stops (kill switch).
	BindInterface string `json:"bindInterface"`
	EnableDHT     bool   `json:"enableDHT"`
	EnablePEX     bool   `json:"enablePEX"`
	EnableLSD     bool   `json:"enableLSD"`
	EnableUTP     bool   `json:"enableUTP"`
	EnableUPnP    bool   `json:"enableUPnP"`
	DisableIPv6   bool   `json:"disableIPv6"`
	Encryption    string `json:"encryption"`
	AnonymousMode bool   `json:"anonymousMode"` // Hide client name/version from peers and trackers.

	// Proxy: "" (none), "socks5" or "http". Trackers and web seeds always use
	// it; with ProxyPeers (SOCKS5 only) peer connections do too, and UDP
	// features that cannot be proxied (DHT, uTP, UDP trackers) are turned off.
	ProxyType     string `json:"proxyType"`
	ProxyHost     string `json:"proxyHost"`
	ProxyPort     int    `json:"proxyPort"`
	ProxyUsername string `json:"proxyUsername,omitempty"`
	ProxyPassword string `json:"proxyPassword,omitempty"`
	ProxyPeers    bool   `json:"proxyPeers"`

	// Performance
	MaxConnsPerTorrent int   `json:"maxConnsPerTorrent"`
	MaxHalfOpenConns   int   `json:"maxHalfOpenConns"`
	DownloadLimitKBps  int64 `json:"downloadLimitKBps"` // 0 = unlimited
	UploadLimitKBps    int64 `json:"uploadLimitKBps"`   // 0 = unlimited
	// Alternative ("turtle") limits, toggled from the UI or tray.
	AltSpeedEnabled      bool  `json:"altSpeedEnabled"`
	AltDownloadLimitKBps int64 `json:"altDownloadLimitKBps"`
	AltUploadLimitKBps   int64 `json:"altUploadLimitKBps"`
	// Turn alternative limits on automatically between From and To ("HH:MM",
	// may cross midnight) on the selected days (bit 0 = Sunday ... bit 6 = Saturday).
	AltScheduleEnabled bool   `json:"altScheduleEnabled"`
	AltScheduleFrom    string `json:"altScheduleFrom"`
	AltScheduleTo      string `json:"altScheduleTo"`
	AltScheduleDays    int    `json:"altScheduleDays"`
	PieceHashers       int    `json:"pieceHashers"`

	// Queue: at most this many torrents are active at once (0 = no limit);
	// the rest wait as "queued" in list order and start when a slot frees.
	QueueEnabled       bool `json:"queueEnabled"`
	MaxActiveDownloads int  `json:"maxActiveDownloads"`
	MaxActiveUploads   int  `json:"maxActiveUploads"`
	MaxActiveTorrents  int  `json:"maxActiveTorrents"`
	QueueSkipSlow      bool `json:"queueSkipSlow"` // slow torrents do not take a slot

	// Organisation
	Categories []Category `json:"categories"`
	// Move finished downloads to MoveCompletedDir (category folders take precedence).
	MoveCompletedEnabled bool   `json:"moveCompletedEnabled"`
	MoveCompletedDir     string `json:"moveCompletedDir"`
	// Keep unfinished downloads in IncompleteDir and move them to their real
	// folder when they complete.
	IncompleteEnabled bool   `json:"incompleteEnabled"`
	IncompleteDir     string `json:"incompleteDir,omitempty"`
	// Pause downloads when a download folder has less free space than this (MiB, 0 = off).
	MinFreeSpaceMB int64 `json:"minFreeSpaceMB"`
	// Watch folder: .torrent files saved here are added automatically and
	// renamed to .torrent.added (or .torrent.invalid).
	WatchDir      string `json:"watchDir"`
	WatchCategory string `json:"watchCategory,omitempty"`
	WatchPaused   bool   `json:"watchPaused"`

	// Seeding
	SeedAfterComplete bool    `json:"seedAfterComplete"`
	SeedRatioLimit    float64 `json:"seedRatioLimit"`   // 0 = no limit
	SeedTimeLimitMin  int     `json:"seedTimeLimitMin"` // stop seeding after this many minutes; 0 = no limit

	// Security
	BlocklistPath string `json:"blocklistPath"` // P2P/eMule style IP blocklist file.

	// Web UI
	WebHost string `json:"webHost"` // Defaults to 127.0.0.1 (local only).
	WebPort int    `json:"webPort"`
	// DLNA shares downloads with TVs and media players on the network
	// (no password: everyone on the network can watch them).
	DLNAEnabled bool   `json:"dlnaEnabled"`
	DLNAName    string `json:"dlnaName,omitempty"`
	// RemoteHTTPS serves other devices over HTTPS (self-signed) on WebPort+1;
	// plain HTTP then only answers on this computer.
	RemoteHTTPS bool `json:"remoteHTTPS"`
	// Argon2id hash of the web UI password. Required when WebHost is not loopback.
	PasswordHash string `json:"passwordHash,omitempty"`
	// Extra Host header values allowed when serving the UI beyond loopback (DNS rebinding guard).
	AllowedHosts []string `json:"allowedHosts,omitempty"`
	OpenBrowser  bool     `json:"openBrowser"`
	// QBitAPI offers a qBittorrent-compatible API (/api/v2) for apps such as
	// Sonarr and Radarr. It needs the web password.
	QBitAPI bool `json:"qbitAPI"`

	// Desktop
	ShowAddDialog bool `json:"showAddDialog"` // Ask where to save when a magnet link is opened.
	AppWindow     bool `json:"appWindow"`     // Open the UI in its own app window instead of a browser tab.
	TrayIcon      bool `json:"trayIcon"`
	AutoStart     bool `json:"autoStart"`     // Start (minimised) when the user signs in.
	Notifications bool `json:"notifications"` // Native notification when a download completes.

	// When downloads finish
	// WhenDone: "" (nothing), "quit", "sleep" or "shutdown" once nothing is
	// left to download. Only changeable from this computer.
	WhenDone string `json:"whenDone,omitempty"`
	// OnCompleteCommand runs for every finished download (no shell; see
	// docs for placeholders). Only changeable from this computer.
	OnCompleteCommand string `json:"onCompleteCommand,omitempty"`

	// Trackers added to every new public torrent (one per line), for faster starts.
	AddDefaultTrackers bool   `json:"addDefaultTrackers"`
	DefaultTrackers    string `json:"defaultTrackers,omitempty"`

	// Search: Torznab indexers (Prowlarr, Jackett, ...).
	Indexers []Indexer `json:"indexers,omitempty"`

	// Subtitles from OpenSubtitles (the user's own API key; account optional).
	OpenSubtitlesKey      string `json:"openSubtitlesKey,omitempty"`
	OpenSubtitlesUser     string `json:"openSubtitlesUser,omitempty"`
	OpenSubtitlesPassword string `json:"openSubtitlesPassword,omitempty"`
	SubtitleLanguages     string `json:"subtitleLanguages,omitempty"` // e.g. "en,sr"

	// Phone notifications
	NotifyNtfyURL       string `json:"notifyNtfyURL,omitempty"`
	NotifyTelegramToken string `json:"notifyTelegramToken,omitempty"`
	NotifyTelegramChat  string `json:"notifyTelegramChat,omitempty"`
	NotifyWebhookURL    string `json:"notifyWebhookURL,omitempty"`
	NotifyOnError       bool   `json:"notifyOnError"`

	// Updates
	AutoUpdateCheck bool `json:"autoUpdateCheck"` // Check GitHub Releases for new versions once a day.
	// UpdatesPausedUntil hides update offers and skips automatic checks until
	// this time (zero = not paused; PausedForever = until resumed).
	UpdatesPausedUntil time.Time `json:"updatesPausedUntil,omitzero"`
}

// PausedForever marks updates as paused until the user resumes them.
var PausedForever = time.Date(9999, 1, 1, 0, 0, 0, 0, time.UTC)

// UpdatesPaused reports whether update offers are paused at now.
func (s Settings) UpdatesPaused(now time.Time) bool {
	return !s.UpdatesPausedUntil.IsZero() && now.Before(s.UpdatesPausedUntil)
}

// Indexer is a Torznab search endpoint.
type Indexer struct {
	Name    string `json:"name"`
	URL     string `json:"url"`
	APIKey  string `json:"apiKey,omitempty"`
	Enabled bool   `json:"enabled"`
}

// Category groups torrents and optionally gives them their own folder.
type Category struct {
	Name     string `json:"name"`
	SavePath string `json:"savePath,omitempty"`
}

// CategoryPath returns the folder of a category, or "" if it has none.
func (s Settings) CategoryPath(name string) string {
	for _, c := range s.Categories {
		if c.Name == name {
			return c.SavePath
		}
	}
	return ""
}

// HasCategory reports whether name is a configured category.
func (s Settings) HasCategory(name string) bool {
	for _, c := range s.Categories {
		if c.Name == name {
			return true
		}
	}
	return false
}

// Defaults returns sensible, secure defaults.
func Defaults() Settings {
	home, _ := os.UserHomeDir()
	return Settings{
		DownloadDir:        filepath.Join(home, "Downloads", "Magnetar"),
		ListenPort:         51413,
		EnableDHT:          true,
		EnablePEX:          true,
		EnableLSD:          false,
		EnableUTP:          true,
		EnableUPnP:         true,
		Encryption:         EncryptionPrefer,
		MaxConnsPerTorrent: 100,
		MaxHalfOpenConns:   500,
		PieceHashers:       4,
		MinFreeSpaceMB:     1024,
		QueueEnabled:       true,
		MaxActiveDownloads: 3,
		MaxActiveUploads:   3,
		MaxActiveTorrents:  5,
		QueueSkipSlow:      true,
		SeedAfterComplete:  true,
		SeedRatioLimit:     0,
		WebHost:            "127.0.0.1",
		WebPort:            8899,
		OpenBrowser:        true,

		AltDownloadLimitKBps: 1024,
		AltUploadLimitKBps:   256,
		AltScheduleFrom:      "09:00",
		AltScheduleTo:        "17:00",
		AltScheduleDays:      0b0111110, // Monday–Friday
		ShowAddDialog:        true,
		AppWindow:            true,
		TrayIcon:             true,
		AutoUpdateCheck:      true,
		Notifications:        true,
	}
}

// Validate checks values and normalises them in place.
func (s *Settings) Validate() error {
	var errs []error
	if dir, err := fsutil.CleanAbs(s.DownloadDir); err != nil {
		errs = append(errs, fmt.Errorf("download directory: %w", err))
	} else {
		s.DownloadDir = dir
	}
	if s.ListenPort < 0 || s.ListenPort > 65535 {
		errs = append(errs, errors.New("listen port must be between 0 and 65535"))
	}
	if s.WebPort < 1 || s.WebPort > 65535 {
		errs = append(errs, errors.New("web port must be between 1 and 65535"))
	}
	s.BindAddress = strings.TrimSpace(s.BindAddress)
	if s.BindAddress != "" && net.ParseIP(s.BindAddress) == nil {
		errs = append(errs, errors.New("bind address must be an IP address"))
	}
	s.BindInterface = strings.TrimSpace(s.BindInterface)
	if len(s.BindInterface) > 64 || hasControl(s.BindInterface) {
		errs = append(errs, errors.New("network interface name is not valid"))
	}
	if s.BindInterface != "" && s.BindAddress != "" {
		errs = append(errs, errors.New("bind either to a network interface or to an IP address, not both"))
	}
	s.ProxyHost = strings.TrimSpace(s.ProxyHost)
	switch s.ProxyType {
	case "":
	case ProxySOCKS5, ProxyHTTP:
		if s.ProxyHost == "" || len(s.ProxyHost) > 253 || hasControl(s.ProxyHost) || strings.ContainsAny(s.ProxyHost, "/@ ") {
			errs = append(errs, errors.New("proxy host is not valid"))
		}
		if s.ProxyPort < 1 || s.ProxyPort > 65535 {
			errs = append(errs, errors.New("proxy port must be between 1 and 65535"))
		}
		if len(s.ProxyUsername) > 255 || len(s.ProxyPassword) > 255 || hasControl(s.ProxyUsername) || hasControl(s.ProxyPassword) {
			errs = append(errs, errors.New("proxy username or password is not valid"))
		}
	default:
		errs = append(errs, fmt.Errorf("unknown proxy type %q", s.ProxyType))
	}
	if s.ProxyType != ProxySOCKS5 {
		s.ProxyPeers = false
	}
	switch s.Encryption {
	case EncryptionPrefer, EncryptionRequire, EncryptionAllow:
	case "":
		s.Encryption = EncryptionPrefer
	default:
		errs = append(errs, fmt.Errorf("unknown encryption policy %q", s.Encryption))
	}
	if s.MaxConnsPerTorrent < 1 || s.MaxConnsPerTorrent > 2000 {
		errs = append(errs, errors.New("max connections per torrent must be between 1 and 2000"))
	}
	if s.MaxHalfOpenConns < 1 || s.MaxHalfOpenConns > 5000 {
		errs = append(errs, errors.New("max half-open connections must be between 1 and 5000"))
	}
	if s.PieceHashers < 1 || s.PieceHashers > 64 {
		errs = append(errs, errors.New("piece hashers must be between 1 and 64"))
	}
	for _, n := range []int{s.MaxActiveDownloads, s.MaxActiveUploads, s.MaxActiveTorrents} {
		if n < 0 || n > 10000 {
			errs = append(errs, errors.New("queue limits must be between 0 (no limit) and 10000"))
			break
		}
	}
	if s.DownloadLimitKBps < 0 || s.UploadLimitKBps < 0 || s.AltDownloadLimitKBps < 0 || s.AltUploadLimitKBps < 0 {
		errs = append(errs, errors.New("speed limits cannot be negative"))
	}
	seen := map[string]bool{}
	for i, c := range s.Categories {
		c.Name = strings.TrimSpace(c.Name)
		switch {
		case c.Name == "" || len(c.Name) > 40:
			errs = append(errs, errors.New("category names must be 1–40 characters"))
		case strings.IndexFunc(c.Name, func(r rune) bool { return r < 32 || r == 127 }) >= 0:
			errs = append(errs, fmt.Errorf("category %q contains control characters", c.Name))
		case seen[strings.ToLower(c.Name)]:
			errs = append(errs, fmt.Errorf("duplicate category %q", c.Name))
		}
		seen[strings.ToLower(c.Name)] = true
		if c.SavePath != "" {
			p, err := fsutil.CleanAbs(c.SavePath)
			if err != nil {
				errs = append(errs, fmt.Errorf("category %q folder: %w", c.Name, err))
			}
			c.SavePath = p
		}
		s.Categories[i] = c
	}
	if len(s.Categories) > 100 {
		errs = append(errs, errors.New("at most 100 categories are allowed"))
	}
	if s.MoveCompletedDir != "" {
		p, err := fsutil.CleanAbs(s.MoveCompletedDir)
		if err != nil {
			errs = append(errs, fmt.Errorf("completed downloads folder: %w", err))
		}
		s.MoveCompletedDir = p
	}
	if len(s.DefaultTrackers) > 100000 {
		errs = append(errs, errors.New("the tracker list is too long"))
	}
	if len(s.Indexers) > 50 {
		errs = append(errs, errors.New("at most 50 search indexers"))
	}
	for i := range s.Indexers {
		ix := &s.Indexers[i]
		ix.Name, ix.URL, ix.APIKey = strings.TrimSpace(ix.Name), strings.TrimSpace(ix.URL), strings.TrimSpace(ix.APIKey)
		pu, err := url.Parse(ix.URL)
		if err != nil || (pu.Scheme != "https" && pu.Scheme != "http") || pu.Host == "" || pu.User != nil || len(ix.URL) > 2000 {
			errs = append(errs, fmt.Errorf("search indexer %q: enter its Torznab address (http or https)", ix.Name))
		}
		if ix.Name == "" && pu != nil {
			ix.Name = pu.Host
		}
		if len(ix.Name) > 60 || len(ix.APIKey) > 200 || hasControl(ix.Name) || hasControl(ix.APIKey) {
			errs = append(errs, fmt.Errorf("search indexer %q: name or API key is not valid", ix.Name))
		}
	}
	switch s.WhenDone {
	case "", "quit", "sleep", "shutdown":
	default:
		errs = append(errs, fmt.Errorf("unknown action %q for when downloads finish", s.WhenDone))
	}
	if len(s.OnCompleteCommand) > 4000 || strings.ContainsAny(s.OnCompleteCommand, "\x00\n\r") {
		errs = append(errs, errors.New("the command to run must be a single line"))
	}
	for name, u := range map[string]*string{"ntfy address": &s.NotifyNtfyURL, "webhook address": &s.NotifyWebhookURL} {
		*u = strings.TrimSpace(*u)
		if *u == "" {
			continue
		}
		pu, err := url.Parse(*u)
		if err != nil || (pu.Scheme != "https" && pu.Scheme != "http") || pu.Host == "" || len(*u) > 2000 {
			errs = append(errs, fmt.Errorf("%s must be an http(s) address", name))
		}
	}
	s.NotifyTelegramToken = strings.TrimSpace(s.NotifyTelegramToken)
	s.NotifyTelegramChat = strings.TrimSpace(s.NotifyTelegramChat)
	if len(s.NotifyTelegramToken) > 200 || len(s.NotifyTelegramChat) > 100 || hasControl(s.NotifyTelegramToken) || hasControl(s.NotifyTelegramChat) {
		errs = append(errs, errors.New("the Telegram bot token or chat ID is not valid"))
	}
	s.OpenSubtitlesKey, s.OpenSubtitlesUser = strings.TrimSpace(s.OpenSubtitlesKey), strings.TrimSpace(s.OpenSubtitlesUser)
	s.SubtitleLanguages = strings.ToLower(strings.ReplaceAll(s.SubtitleLanguages, " ", ""))
	for _, v := range []string{s.OpenSubtitlesKey, s.OpenSubtitlesUser, s.OpenSubtitlesPassword, s.SubtitleLanguages} {
		if len(v) > 200 || hasControl(v) {
			errs = append(errs, errors.New("the subtitle settings are not valid"))
			break
		}
	}
	s.DLNAName = strings.TrimSpace(s.DLNAName)
	if len(s.DLNAName) > 60 || hasControl(s.DLNAName) {
		errs = append(errs, errors.New("the TV name must be up to 60 characters"))
	}
	if s.IncompleteDir != "" {
		p, err := fsutil.CleanAbs(s.IncompleteDir)
		if err != nil {
			errs = append(errs, fmt.Errorf("incomplete downloads folder: %w", err))
		}
		s.IncompleteDir = p
	}
	if s.IncompleteEnabled && s.IncompleteDir == "" {
		errs = append(errs, errors.New("choose a folder for incomplete downloads"))
	}
	if s.MinFreeSpaceMB < 0 || s.MinFreeSpaceMB > 1<<30 {
		errs = append(errs, errors.New("minimum free space must be 0 (off) or a positive size"))
	}
	if s.WatchDir != "" {
		p, err := fsutil.CleanAbs(s.WatchDir)
		if err != nil {
			errs = append(errs, fmt.Errorf("watch folder: %w", err))
		}
		s.WatchDir = p
	}
	if s.WatchCategory != "" && !s.HasCategory(s.WatchCategory) {
		errs = append(errs, fmt.Errorf("watch folder category %q does not exist", s.WatchCategory))
	}
	if s.MoveCompletedEnabled && s.MoveCompletedDir == "" {
		errs = append(errs, errors.New("choose a folder for completed downloads"))
	}
	if _, ok := ParseClock(s.AltScheduleFrom); !ok {
		errs = append(errs, errors.New("schedule start must be a time like 09:00"))
	}
	if _, ok := ParseClock(s.AltScheduleTo); !ok {
		errs = append(errs, errors.New("schedule end must be a time like 17:00"))
	}
	if s.AltScheduleDays < 0 || s.AltScheduleDays > 127 {
		errs = append(errs, errors.New("invalid schedule days"))
	}
	if s.AltScheduleEnabled && s.AltScheduleDays == 0 {
		errs = append(errs, errors.New("choose at least one day for the speed schedule"))
	}
	if s.SeedTimeLimitMin < 0 {
		errs = append(errs, errors.New("seed time limit cannot be negative"))
	}
	if s.SeedRatioLimit < 0 {
		errs = append(errs, errors.New("seed ratio limit cannot be negative"))
	}
	if s.BlocklistPath != "" {
		if p, err := fsutil.CleanAbs(s.BlocklistPath); err != nil {
			errs = append(errs, fmt.Errorf("blocklist path: %w", err))
		} else {
			s.BlocklistPath = p
		}
	}
	if s.WebHost == "" {
		s.WebHost = "127.0.0.1"
	}
	if net.ParseIP(s.WebHost) == nil && s.WebHost != "localhost" {
		errs = append(errs, errors.New("web host must be an IP address or localhost"))
	}
	if s.QBitAPI && s.PasswordHash == "" {
		s.QBitAPI = false // it can never be used without a password
	}
	if !IsLoopback(s.WebHost) && s.PasswordHash == "" {
		errs = append(errs, errors.New("a password must be set before exposing the web UI beyond this computer"))
	}
	return errors.Join(errs...)
}

// EffectiveLimits returns the download/upload limits in KiB/s currently in force.
func (s Settings) EffectiveLimits() (down, up int64) {
	if s.AltSpeedEnabled {
		return s.AltDownloadLimitKBps, s.AltUploadLimitKBps
	}
	return s.DownloadLimitKBps, s.UploadLimitKBps
}

// ParseClock parses "HH:MM" into minutes after midnight.
func ParseClock(s string) (int, bool) {
	var h, m int
	if len(s) != 5 || s[2] != ':' {
		return 0, false
	}
	if _, err := fmt.Sscanf(s, "%02d:%02d", &h, &m); err != nil || h > 23 || m > 59 || h < 0 || m < 0 {
		return 0, false
	}
	return h*60 + m, true
}

// InAltSchedule reports whether t falls inside the alternative-speed schedule.
func (s Settings) InAltSchedule(t time.Time) bool {
	from, ok1 := ParseClock(s.AltScheduleFrom)
	to, ok2 := ParseClock(s.AltScheduleTo)
	if !ok1 || !ok2 || from == to {
		return false
	}
	now := t.Hour()*60 + t.Minute()
	day := func(d time.Weekday) bool { return s.AltScheduleDays&(1<<uint(d)) != 0 }
	if from < to {
		return day(t.Weekday()) && now >= from && now < to
	}
	// Overnight window, e.g. 22:00–06:00: the part after midnight belongs to the previous day.
	if now >= from {
		return day(t.Weekday())
	}
	if now < to {
		return day((t.Weekday() + 6) % 7)
	}
	return false
}

// IsLoopback reports whether host only accepts local connections.
func IsLoopback(host string) bool {
	if host == "localhost" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

// Store persists settings as JSON in the config directory with owner-only permissions.
type Store struct {
	mu   sync.RWMutex
	path string
	cur  Settings
}

// Dirs resolves the config and data directories, creating them with 0700 permissions.
func Dirs(override string) (configDir, dataDir string, err error) {
	if override != "" {
		configDir, dataDir = override, override
	} else {
		base, err := os.UserConfigDir()
		if err != nil {
			return "", "", err
		}
		configDir = filepath.Join(base, "magnetar")
		dataDir = configDir
		if xdg, err := userDataDir(); err == nil {
			dataDir = filepath.Join(xdg, "magnetar")
		}
	}
	for _, d := range []string{configDir, dataDir} {
		if err := os.MkdirAll(d, 0o700); err != nil {
			return "", "", err
		}
	}
	return configDir, dataDir, nil
}

func userDataDir() (string, error) {
	if v := os.Getenv("XDG_DATA_HOME"); v != "" {
		return v, nil
	}
	if os.Getenv("APPDATA") != "" || os.Getenv("HOME") == "" {
		return os.UserConfigDir()
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	if _, err := os.Stat(filepath.Join(home, "Library")); err == nil {
		return filepath.Join(home, "Library", "Application Support"), nil
	}
	return filepath.Join(home, ".local", "share"), nil
}

// Load reads settings from dir/settings.json, falling back to defaults.
func Load(dir string) (*Store, error) {
	st := &Store{path: filepath.Join(dir, "settings.json"), cur: Defaults()}
	b, err := os.ReadFile(st.path)
	switch {
	case errors.Is(err, os.ErrNotExist):
		if err := st.cur.Validate(); err != nil {
			return nil, err
		}
		return st, st.write(st.cur)
	case err != nil:
		return nil, err
	}
	s := Defaults()
	if err := json.Unmarshal(b, &s); err != nil {
		return nil, fmt.Errorf("parse %s: %w", st.path, err)
	}
	if err := s.Validate(); err != nil {
		return nil, fmt.Errorf("invalid settings in %s: %w", st.path, err)
	}
	st.cur = s
	return st, nil
}

// Get returns a copy of the current settings.
func (st *Store) Get() Settings {
	st.mu.RLock()
	defer st.mu.RUnlock()
	s := st.cur
	s.AllowedHosts = append([]string(nil), st.cur.AllowedHosts...)
	s.Categories = append([]Category(nil), st.cur.Categories...)
	return s
}

// Update validates and saves new settings.
func (st *Store) Update(s Settings) error {
	if err := s.Validate(); err != nil {
		return err
	}
	st.mu.Lock()
	defer st.mu.Unlock()
	if err := st.write(s); err != nil {
		return err
	}
	st.cur = s
	return nil
}

func (st *Store) write(s Settings) error {
	b, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	return WriteFileAtomic(st.path, b, 0o600)
}

// WriteFileAtomic writes data to a temp file and renames it over path.
func WriteFileAtomic(path string, data []byte, perm os.FileMode) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+".tmp*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Chmod(perm); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}
