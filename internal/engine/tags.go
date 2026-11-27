package engine

import (
	"errors"
	"net"
	"slices"
	"sort"
	"strings"
)

const (
	maxTags   = 20 // per torrent
	maxTagLen = 40
)

// CleanTags trims, de-duplicates and validates tag names.
func CleanTags(tags []string) ([]string, error) {
	var out []string
	for _, t := range tags {
		t = strings.TrimSpace(t)
		if t == "" {
			continue
		}
		if len(t) > maxTagLen || strings.ContainsAny(t, ",\n\r\t") || strings.IndexFunc(t, func(r rune) bool { return r < 32 || r == 127 }) >= 0 {
			return nil, errors.New("tags must be 1-40 characters without commas or control characters")
		}
		if !slices.ContainsFunc(out, func(x string) bool { return strings.EqualFold(x, t) }) {
			out = append(out, t)
		}
	}
	if len(out) > maxTags {
		return nil, errors.New("at most 20 tags per torrent")
	}
	sort.Slice(out, func(i, j int) bool { return strings.ToLower(out[i]) < strings.ToLower(out[j]) })
	return out, nil
}

// EditTags adds and removes tags on torrents.
func (e *Engine) EditTags(add, remove []string, hashes ...string) error {
	add, err := CleanTags(add)
	if err != nil {
		return err
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	for _, h := range hashes {
		m, err := e.get(h)
		if err != nil {
			return err
		}
		tags := slices.DeleteFunc(slices.Clone(m.rec.Tags), func(t string) bool {
			return slices.ContainsFunc(remove, func(r string) bool { return strings.EqualFold(strings.TrimSpace(r), t) })
		})
		if tags, err = CleanTags(append(tags, add...)); err != nil {
			return err
		}
		m.rec.Tags = tags
	}
	e.dirty = true
	return nil
}

// Tags lists every tag in use, sorted.
func (e *Engine) Tags() []string {
	e.mu.RLock()
	defer e.mu.RUnlock()
	seen := map[string]string{}
	for _, m := range e.torrents {
		for _, t := range m.rec.Tags {
			if _, ok := seen[strings.ToLower(t)]; !ok {
				seen[strings.ToLower(t)] = t
			}
		}
	}
	out := make([]string, 0, len(seen))
	for _, t := range seen {
		out = append(out, t)
	}
	sort.Slice(out, func(i, j int) bool { return strings.ToLower(out[i]) < strings.ToLower(out[j]) })
	return out
}

// maxTorrentLimitKBps is 10 GiB/s: anything above means "no limit" in practice.
const maxTorrentLimitKBps = 10 << 20

// SetTorrentLimits sets per-torrent download and upload limits in KiB/s
// (0 removes the limit); they apply on top of the global limits.
func (e *Engine) SetTorrentLimits(downKBps, upKBps int64, hashes ...string) error {
	if downKBps < 0 || upKBps < 0 || downKBps > maxTorrentLimitKBps || upKBps > maxTorrentLimitKBps {
		return errors.New("speed limits must be between 0 (no limit) and 10 GiB/s")
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	for _, h := range hashes {
		m, err := e.get(h)
		if err != nil {
			return err
		}
		m.rec.DownLimitKBps, m.rec.UpLimitKBps = downKBps, upKBps
		if m.t != nil {
			m.t.SetRateLimits(int(downKBps*1024), int(upKBps*1024))
		}
	}
	e.dirty = true
	return nil
}

// reportErrorsLocked publishes an "error" event once each time a torrent
// fails, so notifications can tell the user.
func (e *Engine) reportErrorsLocked() {
	for _, h := range e.order {
		m := e.torrents[h]
		switch {
		case m.err != "" && !m.errReported:
			m.errReported = true
			e.publish(Event{Type: "error", Hash: h, Name: m.rec.Name, Message: m.err})
		case m.err == "":
			m.errReported = false
		}
	}
}

// AllDone reports whether nothing is left to download: no torrent that is
// running or queued still misses data it wants.
func (e *Engine) AllDone() bool {
	e.mu.RLock()
	defer e.mu.RUnlock()
	for _, h := range e.order {
		m := e.torrents[h]
		if m.rec.Paused || m.err != "" {
			continue
		}
		if !m.doneLocked() {
			return false
		}
	}
	return true
}

// Finished returns what a finish command needs to know about a torrent.
func (e *Engine) Finished(hash string) (name, savePath, category string, tags []string, ok bool) {
	e.mu.RLock()
	defer e.mu.RUnlock()
	m, err := e.get(hash)
	if err != nil {
		return "", "", "", nil, false
	}
	return m.rec.Name, m.rec.SavePath, m.rec.Category, slices.Clone(m.rec.Tags), true
}

// SetCountryLookup sets an optional function that maps a peer IP to its ISO
// country code (used to show flags in the Peers tab); nil disables it.
func (e *Engine) SetCountryLookup(fn func(ip net.IP) string) {
	e.mu.Lock()
	e.countryOf = fn
	e.mu.Unlock()
}
