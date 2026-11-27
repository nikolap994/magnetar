package engine

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"time"

	"github.com/nikolap994/magnetar/internal/config"
)

const historyMinutes = 24 * 60

// Sample is the average transfer rate over one minute.
type Sample struct {
	T    int64 `json:"t"`    // unix seconds at the start of the minute
	Down int64 `json:"down"` // bytes/s
	Up   int64 `json:"up"`   // bytes/s
}

// lifetime is persisted in stats.json.
type lifetime struct {
	Version      int       `json:"version"`
	AllTimeDown  int64     `json:"allTimeDown"`
	AllTimeUp    int64     `json:"allTimeUp"`
	FirstStarted time.Time `json:"firstStarted"`
	Sessions     int       `json:"sessions"`
	History      []Sample  `json:"history"`
}

type statsState struct {
	lifetime
	sessionDown, sessionUp int64
	minuteStart            int64
	minuteDown, minuteUp   int64
}

func (e *Engine) statsPath() string { return filepath.Join(e.dataDir, "stats.json") }

func (e *Engine) loadStats() {
	e.stats.FirstStarted = time.Now().UTC()
	if b, err := os.ReadFile(e.statsPath()); err == nil {
		var lt lifetime
		if json.Unmarshal(b, &lt) == nil {
			e.stats.lifetime = lt
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		e.log.Warn("read statistics", "err", err)
	}
	if e.stats.FirstStarted.IsZero() {
		e.stats.FirstStarted = time.Now().UTC()
	}
	e.stats.Sessions++
	e.stats.minuteStart = time.Now().Truncate(time.Minute).Unix()
}

func (e *Engine) saveStatsLocked() error {
	if e.frozen.Load() {
		return nil
	}
	b, err := json.Marshal(e.stats.lifetime)
	if err != nil {
		return err
	}
	return config.WriteFileAtomic(e.statsPath(), b, 0o600)
}

// recordTransferLocked adds the bytes moved during the last tick.
func (e *Engine) recordTransferLocked(now time.Time, down, up int64) {
	if down < 0 {
		down = 0
	}
	if up < 0 {
		up = 0
	}
	s := &e.stats
	s.AllTimeDown += down
	s.AllTimeUp += up
	s.sessionDown += down
	s.sessionUp += up
	minute := now.Truncate(time.Minute).Unix()
	if minute != s.minuteStart {
		s.History = append(s.History, Sample{T: s.minuteStart, Down: s.minuteDown / 60, Up: s.minuteUp / 60})
		// Fill minutes where Magnetar was not running so the chart shows gaps as zero.
		for t := s.minuteStart + 60; t < minute && len(s.History) < historyMinutes*2; t += 60 {
			s.History = append(s.History, Sample{T: t})
		}
		cutoff := minute - historyMinutes*60
		i := 0
		for i < len(s.History) && s.History[i].T < cutoff {
			i++
		}
		s.History = append([]Sample(nil), s.History[i:]...)
		s.minuteStart, s.minuteDown, s.minuteUp = minute, 0, 0
	}
	s.minuteDown += down
	s.minuteUp += up
}

// Statistics is the data behind the statistics view.
type Statistics struct {
	AllTimeDown  int64     `json:"allTimeDown"`
	AllTimeUp    int64     `json:"allTimeUp"`
	AllTimeRatio float64   `json:"allTimeRatio"`
	SessionDown  int64     `json:"sessionDown"`
	SessionUp    int64     `json:"sessionUp"`
	SessionStart time.Time `json:"sessionStart"`
	FirstStarted time.Time `json:"firstStarted"`
	Sessions     int       `json:"sessions"`
	Torrents     int       `json:"torrents"`
	Completed    int       `json:"completed"`
	History      []Sample  `json:"history"`
}

// Statistics returns lifetime and session totals and the last 24 hours of bandwidth.
func (e *Engine) Statistics() Statistics {
	e.mu.RLock()
	defer e.mu.RUnlock()
	s := e.stats
	out := Statistics{
		AllTimeDown: s.AllTimeDown, AllTimeUp: s.AllTimeUp,
		AllTimeRatio: FormatRatio(ratio(s.AllTimeDown, s.AllTimeUp, 0)),
		SessionDown:  s.sessionDown, SessionUp: s.sessionUp,
		SessionStart: e.started.UTC(), FirstStarted: s.FirstStarted, Sessions: s.Sessions,
		Torrents: len(e.torrents),
		History:  append([]Sample(nil), s.History...),
	}
	for _, m := range e.torrents {
		if m.rec.CompletedAt != nil {
			out.Completed++
		}
	}
	return out
}
