// Package backup exports Magnetar's settings and session into one zip file
// and restores it, for moving to another computer or keeping a copy.
package backup

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/nikolap994/magnetar/internal/config"
)

const (
	manifestName = "magnetar-backup.json"
	pendingDir   = "restore-pending"
	maxFile      = 64 << 20
	maxTotal     = 2 << 30
	maxEntries   = 100000
)

// dataFiles are copied from the data folder; torrents/*.torrent too.
var dataFiles = []string{"session.json", "rss.json", "stats.json", "banned-peers.json"}

var torrentFile = regexp.MustCompile(`^torrents/[0-9a-f]{40}\.torrent$`)

type manifest struct {
	App     string    `json:"app"`
	Version string    `json:"version"`
	Created time.Time `json:"created"`
}

// Export writes the backup zip.
func Export(w io.Writer, cfgDir, dataDir, version string) error {
	zw := zip.NewWriter(w)
	add := func(name string, data []byte) error {
		f, err := zw.Create(name)
		if err != nil {
			return err
		}
		_, err = f.Write(data)
		return err
	}
	m, _ := json.MarshalIndent(manifest{App: "Magnetar", Version: version, Created: time.Now().UTC()}, "", "  ")
	if err := add(manifestName, m); err != nil {
		return err
	}
	if b, err := os.ReadFile(filepath.Join(cfgDir, "settings.json")); err == nil {
		if err := add("settings.json", b); err != nil {
			return err
		}
	}
	for _, name := range dataFiles {
		if b, err := os.ReadFile(filepath.Join(dataDir, name)); err == nil {
			if err := add(name, b); err != nil {
				return err
			}
		}
	}
	entries, _ := os.ReadDir(filepath.Join(dataDir, "torrents"))
	for _, e := range entries {
		name := "torrents/" + e.Name()
		if !e.Type().IsRegular() || !torrentFile.MatchString(name) {
			continue
		}
		b, err := os.ReadFile(filepath.Join(dataDir, "torrents", e.Name()))
		if err != nil {
			continue
		}
		if err := add(name, b); err != nil {
			return err
		}
	}
	return zw.Close()
}

// allowed reports whether a zip entry belongs in a backup.
func allowed(name string) bool {
	if name == "settings.json" || name == manifestName || torrentFile.MatchString(name) {
		return true
	}
	for _, n := range dataFiles {
		if name == n {
			return true
		}
	}
	return false
}

// Stage checks a backup and stores it to be applied on the next start, so
// nothing running can overwrite the restored files.
func Stage(data []byte, dataDir string) (version string, torrents int, err error) {
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return "", 0, errors.New("this is not a Magnetar backup file")
	}
	if len(zr.File) > maxEntries {
		return "", 0, errors.New("the backup has too many files")
	}
	files := map[string][]byte{}
	var total int64
	for _, f := range zr.File {
		name := path.Clean(f.Name)
		if !allowed(name) {
			continue // ignore anything else
		}
		if f.UncompressedSize64 > maxFile {
			return "", 0, fmt.Errorf("%s is too large", name)
		}
		rc, err := f.Open()
		if err != nil {
			return "", 0, err
		}
		b, err := io.ReadAll(io.LimitReader(rc, maxFile+1))
		rc.Close()
		if err != nil || len(b) > maxFile {
			return "", 0, fmt.Errorf("%s could not be read", name)
		}
		if total += int64(len(b)); total > maxTotal {
			return "", 0, errors.New("the backup is too large")
		}
		files[name] = b
	}
	var m manifest
	if err := json.Unmarshal(files[manifestName], &m); err != nil || m.App != "Magnetar" {
		return "", 0, errors.New("this is not a Magnetar backup file")
	}
	if b, ok := files["settings.json"]; ok {
		s := config.Defaults()
		if err := json.Unmarshal(b, &s); err != nil {
			return "", 0, errors.New("the settings in the backup are damaged")
		}
		if err := s.Validate(); err != nil {
			return "", 0, fmt.Errorf("the settings in the backup are not valid here: %w", err)
		}
	}
	dir := filepath.Join(dataDir, pendingDir)
	if err := os.RemoveAll(dir); err != nil {
		return "", 0, err
	}
	for name, b := range files {
		p := filepath.Join(dir, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
			return "", 0, err
		}
		if err := os.WriteFile(p, b, 0o600); err != nil {
			return "", 0, err
		}
		if strings.HasPrefix(name, "torrents/") {
			torrents++
		}
	}
	return m.Version, torrents, nil
}

// ApplyPending moves a staged backup into place. Call it at startup before
// settings and the session are loaded. It reports whether a backup was applied.
func ApplyPending(cfgDir, dataDir string) (bool, error) {
	dir := filepath.Join(dataDir, pendingDir)
	if _, err := os.Stat(filepath.Join(dir, manifestName)); err != nil {
		return false, nil
	}
	if b, err := os.ReadFile(filepath.Join(dir, "settings.json")); err == nil {
		if err := config.WriteFileAtomic(filepath.Join(cfgDir, "settings.json"), b, 0o600); err != nil {
			return false, err
		}
	}
	for _, name := range dataFiles {
		if b, err := os.ReadFile(filepath.Join(dir, name)); err == nil {
			if err := config.WriteFileAtomic(filepath.Join(dataDir, name), b, 0o600); err != nil {
				return false, err
			}
		}
	}
	entries, _ := os.ReadDir(filepath.Join(dir, "torrents"))
	if len(entries) > 0 {
		if err := os.MkdirAll(filepath.Join(dataDir, "torrents"), 0o700); err != nil {
			return false, err
		}
	}
	for _, e := range entries {
		if !torrentFile.MatchString("torrents/" + e.Name()) {
			continue
		}
		b, err := os.ReadFile(filepath.Join(dir, "torrents", e.Name()))
		if err != nil {
			continue
		}
		if err := config.WriteFileAtomic(filepath.Join(dataDir, "torrents", e.Name()), b, 0o600); err != nil {
			return false, err
		}
	}
	return true, os.RemoveAll(dir)
}
