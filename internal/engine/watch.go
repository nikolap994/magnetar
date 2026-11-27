package engine

import (
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const watchEvery = 5 * time.Second

// watchLoop adds .torrent files that appear in the watch folder. Polling
// works the same on every system and on network drives.
func (e *Engine) watchLoop() {
	defer e.wg.Done()
	tick := time.NewTicker(watchEvery)
	defer tick.Stop()
	for {
		select {
		case <-e.ctx.Done():
			return
		case <-tick.C:
		}
		s := e.Settings()
		if s.WatchDir != "" {
			e.scanWatchDir(s.WatchDir, s.WatchCategory, s.WatchPaused)
		}
	}
}

// scanWatchDir adds every .torrent file in dir and renames it so it is
// handled once: ".added" on success (or if already in the list), ".invalid"
// otherwise. Files still being written (changed in the last two seconds) wait
// for the next scan.
func (e *Engine) scanWatchDir(dir, category string, paused bool) (added int) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return 0
	}
	for _, de := range entries {
		name := de.Name()
		if !de.Type().IsRegular() || !strings.EqualFold(filepath.Ext(name), ".torrent") {
			continue
		}
		info, err := de.Info()
		if err != nil || time.Since(info.ModTime()) < 2*time.Second {
			continue
		}
		path := filepath.Join(dir, name)
		data, err := readSmallFile(path)
		suffix := ".added"
		if err == nil {
			_, err = e.Add(AddOptions{TorrentFile: data, Category: category, Paused: paused})
		}
		switch {
		case err == nil:
			added++
			e.log.Info("added from watch folder", "file", logSafe(name))
		case errors.Is(err, ErrExists):
		case errors.Is(err, ErrClosed):
			return added
		default:
			suffix = ".invalid"
			e.log.Warn("watch folder", "file", logSafe(name), "err", logSafe(err.Error()))
		}
		if err := os.Rename(path, path+suffix); err != nil {
			e.log.Warn("watch folder rename", "file", logSafe(name), "err", err)
		}
	}
	return added
}

func readSmallFile(path string) ([]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	b, err := io.ReadAll(io.LimitReader(f, MaxTorrentFileSize+1))
	if err != nil {
		return nil, err
	}
	if len(b) > MaxTorrentFileSize {
		return nil, errors.New("torrent file is too large")
	}
	return b, nil
}
