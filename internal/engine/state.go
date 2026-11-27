package engine

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"time"

	"github.com/nikolap994/magnetar/internal/config"
)

// File priorities, as exposed by the API.
const (
	PrioritySkip   = 0
	PriorityNormal = 1
	PriorityHigh   = 2
)

// record is the persisted state of one torrent.
type record struct {
	InfoHash    string     `json:"infoHash"`
	Name        string     `json:"name"`
	Magnet      string     `json:"magnet,omitempty"`
	SavePath    string     `json:"savePath"`
	Paused      bool       `json:"paused"`
	Sequential  bool       `json:"sequential"`
	AddedAt     time.Time  `json:"addedAt"`
	CompletedAt *time.Time `json:"completedAt,omitempty"`
	Downloaded  int64      `json:"downloaded"`
	Uploaded    int64      `json:"uploaded"`
	// Per-file priorities by file index. Missing entries are PriorityNormal.
	FilePriorities map[int]int `json:"filePriorities,omitempty"`
	ExtraTrackers  []string    `json:"extraTrackers,omitempty"`
	Size           int64       `json:"size,omitempty"`
	Category       string      `json:"category,omitempty"`
	Completed      int64       `json:"completed,omitempty"`
	// Forced torrents ignore the queue limits (qBittorrent's "Force start").
	Forced bool `json:"forced,omitempty"`
}

type stateFile struct {
	Version  int       `json:"version"`
	Torrents []*record `json:"torrents"`
}

func (e *Engine) statePath() string   { return filepath.Join(e.dataDir, "session.json") }
func (e *Engine) metainfoDir() string { return filepath.Join(e.dataDir, "torrents") }
func (e *Engine) metainfoPath(hash string) string {
	return filepath.Join(e.metainfoDir(), hash+".torrent")
}

func (e *Engine) loadState() ([]*record, error) {
	b, err := os.ReadFile(e.statePath())
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var sf stateFile
	if err := json.Unmarshal(b, &sf); err != nil {
		return nil, err
	}
	return sf.Torrents, nil
}

// saveStateLocked writes the session. Callers must hold e.mu.
func (e *Engine) saveStateLocked() error {
	sf := stateFile{Version: 1}
	for _, h := range e.order {
		m := e.torrents[h]
		r := *m.rec
		r.Downloaded, r.Uploaded = m.totals()
		sf.Torrents = append(sf.Torrents, &r)
	}
	b, err := json.MarshalIndent(sf, "", "  ")
	if err != nil {
		return err
	}
	return config.WriteFileAtomic(e.statePath(), b, 0o600)
}
