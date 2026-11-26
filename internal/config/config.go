// Package config loads, validates and persists Magnetar's settings.
package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/nikolap994/magnetar/internal/fsutil"
)

// Encryption policies for peer connections (BitTorrent protocol encryption / MSE).
const (
	EncryptionPrefer  = "prefer"  // Try encrypted first, fall back to plaintext.
	EncryptionRequire = "require" // Only talk to peers that support encryption.
	EncryptionAllow   = "allow"   // Prefer plaintext but accept encrypted peers.
)

// Settings are the user-tunable options. Every field is exposed through the web UI.
type Settings struct {
	// Storage
	DownloadDir string `json:"downloadDir"`

	// Network
	ListenPort    int    `json:"listenPort"`
	BindAddress   string `json:"bindAddress"` // IP to bind peer traffic to ("" = all). Use your VPN interface IP to keep traffic on the VPN.
	EnableDHT     bool   `json:"enableDHT"`
	EnablePEX     bool   `json:"enablePEX"`
	EnableLSD     bool   `json:"enableLSD"`
	EnableUTP     bool   `json:"enableUTP"`
	EnableUPnP    bool   `json:"enableUPnP"`
	DisableIPv6   bool   `json:"disableIPv6"`
	Encryption    string `json:"encryption"`
	AnonymousMode bool   `json:"anonymousMode"` // Hide client name/version from peers and trackers.

	// Performance
	MaxConnsPerTorrent int   `json:"maxConnsPerTorrent"`
	MaxHalfOpenConns   int   `json:"maxHalfOpenConns"`
	DownloadLimitKBps  int64 `json:"downloadLimitKBps"` // 0 = unlimited
	UploadLimitKBps    int64 `json:"uploadLimitKBps"`   // 0 = unlimited
	// Alternative ("turtle") limits, toggled from the UI or tray like qBittorrent.
	AltSpeedEnabled      bool  `json:"altSpeedEnabled"`
	AltDownloadLimitKBps int64 `json:"altDownloadLimitKBps"`
	AltUploadLimitKBps   int64 `json:"altUploadLimitKBps"`
	PieceHashers         int   `json:"pieceHashers"`

	// Organisation
	Categories []Category `json:"categories"`
	// Move finished downloads to MoveCompletedDir (category folders take precedence).
	MoveCompletedEnabled bool   `json:"moveCompletedEnabled"`
	MoveCompletedDir     string `json:"moveCompletedDir"`

	// Seeding
	SeedAfterComplete bool    `json:"seedAfterComplete"`
	SeedRatioLimit    float64 `json:"seedRatioLimit"` // 0 = no limit

	// Security
	BlocklistPath string `json:"blocklistPath"` // P2P/eMule style IP blocklist file.

	// Web UI
	WebHost string `json:"webHost"` // Defaults to 127.0.0.1 (local only).
	WebPort int    `json:"webPort"`
	// Argon2id hash of the web UI password. Required when WebHost is not loopback.
	PasswordHash string `json:"passwordHash,omitempty"`
	// Extra Host header values allowed when serving the UI beyond loopback (DNS rebinding guard).
	AllowedHosts []string `json:"allowedHosts,omitempty"`
	OpenBrowser  bool     `json:"openBrowser"`

	// Desktop
	ShowAddDialog bool `json:"showAddDialog"` // Ask where to save when a magnet link is opened.
	AppWindow     bool `json:"appWindow"`     // Open the UI in its own app window instead of a browser tab.
	TrayIcon      bool `json:"trayIcon"`
	AutoStart     bool `json:"autoStart"` // Start (minimised) when the user signs in.

	// Updates
	AutoUpdateCheck bool `json:"autoUpdateCheck"` // Check GitHub Releases for new versions once a day.
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
		MaxConnsPerTorrent: 80,
		MaxHalfOpenConns:   200,
		PieceHashers:       4,
		SeedAfterComplete:  true,
		SeedRatioLimit:     0,
		WebHost:            "127.0.0.1",
		WebPort:            8899,
		OpenBrowser:        true,

		AltDownloadLimitKBps: 1024,
		AltUploadLimitKBps:   256,
		ShowAddDialog:        true,
		AppWindow:            true,
		TrayIcon:             true,
		AutoUpdateCheck:      true,
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
	if s.MoveCompletedEnabled && s.MoveCompletedDir == "" {
		errs = append(errs, errors.New("choose a folder for completed downloads"))
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
