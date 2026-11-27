package engine

import (
	"errors"
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
