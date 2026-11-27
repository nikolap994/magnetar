package rss

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/nikolap994/magnetar/internal/config"
	"github.com/nikolap994/magnetar/internal/engine"
	"github.com/nikolap994/magnetar/internal/fsutil"
	"github.com/nikolap994/magnetar/internal/logsafe"
)

const (
	maxFeeds        = 100
	maxRules        = 200
	maxItemsPerFeed = 300
	maxSeen         = 20000
	defaultRefresh  = 30 // minutes
	minRefresh      = 5
)

// Feed is a subscribed feed and its latest articles.
type Feed struct {
	ID          string    `json:"id"`
	URL         string    `json:"url"`
	Title       string    `json:"title"`
	RefreshMin  int       `json:"refreshMin"`
	Enabled     bool      `json:"enabled"`
	LastChecked time.Time `json:"lastChecked,omitempty"`
	LastError   string    `json:"lastError,omitempty"`
	Items       []Item    `json:"items"`
}

// State is everything shown in the RSS window.
type State struct {
	Feeds []Feed `json:"feeds"`
	Rules []Rule `json:"rules"`
}

type stored struct {
	State
	// Seen holds feedID + "\x00" + item key for articles already downloaded.
	Seen map[string]time.Time `json:"seen"`
}

// Manager refreshes feeds on a schedule and applies the rules.
type Manager struct {
	path   string
	eng    *engine.Engine
	client func() *http.Client
	log    *slog.Logger

	mu sync.Mutex
	st stored

	refreshing sync.Mutex
}

// New loads the RSS state from dataDir. client returns the HTTP client to use
// (it follows the proxy settings).
func New(dataDir string, eng *engine.Engine, client func() *http.Client, log *slog.Logger) *Manager {
	m := &Manager{path: filepath.Join(dataDir, "rss.json"), eng: eng, client: client, log: log}
	m.st.Seen = map[string]time.Time{}
	if b, err := os.ReadFile(m.path); err == nil {
		if err := json.Unmarshal(b, &m.st); err != nil {
			log.Warn("could not read RSS settings", "err", err)
		}
	}
	if m.st.Seen == nil {
		m.st.Seen = map[string]time.Time{}
	}
	return m
}

// Run refreshes due feeds until ctx ends.
func (m *Manager) Run(ctx context.Context) {
	t := time.NewTicker(30 * time.Second)
	defer t.Stop()
	m.refresh(ctx, "", false)
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			m.refresh(ctx, "", false)
		}
	}
}

// Get returns a copy of the state.
func (m *Manager) Get() State {
	m.mu.Lock()
	defer m.mu.Unlock()
	b, _ := json.Marshal(m.st.State)
	var s State
	json.Unmarshal(b, &s)
	if s.Feeds == nil {
		s.Feeds = []Feed{}
	}
	if s.Rules == nil {
		s.Rules = []Rule{}
	}
	return s
}

func newID() string {
	b := make([]byte, 8)
	rand.Read(b)
	return hex.EncodeToString(b)
}

func validFeedURL(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || len(raw) > 2000 {
		return "", errors.New("enter the feed's http:// or https:// address")
	}
	return u.String(), nil
}

// AddFeed subscribes to a feed and loads it right away.
func (m *Manager) AddFeed(ctx context.Context, rawURL string) (Feed, error) {
	u, err := validFeedURL(rawURL)
	if err != nil {
		return Feed{}, err
	}
	m.mu.Lock()
	if len(m.st.Feeds) >= maxFeeds {
		m.mu.Unlock()
		return Feed{}, fmt.Errorf("at most %d feeds", maxFeeds)
	}
	for _, f := range m.st.Feeds {
		if f.URL == u {
			m.mu.Unlock()
			return Feed{}, errors.New("this feed is already added")
		}
	}
	f := Feed{ID: newID(), URL: u, RefreshMin: defaultRefresh, Enabled: true, Items: []Item{}}
	m.st.Feeds = append(m.st.Feeds, f)
	m.saveLocked()
	m.mu.Unlock()
	m.refresh(ctx, f.ID, true)
	for _, x := range m.Get().Feeds {
		if x.ID == f.ID {
			return x, nil
		}
	}
	return f, nil
}

// UpdateFeed changes a feed's name, refresh interval or enabled state.
func (m *Manager) UpdateFeed(id, title string, refreshMin int, enabled bool) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	i := m.feedIndexLocked(id)
	if i < 0 {
		return errors.New("feed not found")
	}
	f := &m.st.Feeds[i]
	if t := clean(title); t != "" && len(t) <= 200 {
		f.Title = t
	}
	f.RefreshMin = max(refreshMin, minRefresh)
	f.Enabled = enabled
	return m.saveLocked()
}

// RemoveFeed unsubscribes.
func (m *Manager) RemoveFeed(id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	i := m.feedIndexLocked(id)
	if i < 0 {
		return errors.New("feed not found")
	}
	m.st.Feeds = slices.Delete(m.st.Feeds, i, i+1)
	prefix := id + "\x00"
	for k := range m.st.Seen {
		if strings.HasPrefix(k, prefix) {
			delete(m.st.Seen, k)
		}
	}
	return m.saveLocked()
}

// SetRules replaces all rules and applies them to the articles already loaded.
func (m *Manager) SetRules(ctx context.Context, rules []Rule) error {
	if len(rules) > maxRules {
		return fmt.Errorf("at most %d rules", maxRules)
	}
	s := m.eng.Settings()
	for i := range rules {
		r := &rules[i]
		if err := r.Validate(); err != nil {
			return err
		}
		if r.ID == "" {
			r.ID = newID()
		}
		if r.Category != "" && !s.HasCategory(r.Category) {
			return fmt.Errorf("rule %q: category %q does not exist", r.Name, r.Category)
		}
		if r.SavePath != "" {
			p, err := fsutil.CleanAbs(r.SavePath)
			if err != nil {
				return fmt.Errorf("rule %q: save folder: %w", r.Name, err)
			}
			r.SavePath = p
		}
	}
	m.mu.Lock()
	// Keep the episode history of rules that already existed.
	old := map[string][]string{}
	for _, r := range m.st.Rules {
		old[r.ID] = r.Episodes
	}
	for i := range rules {
		if len(rules[i].Episodes) == 0 {
			rules[i].Episodes = old[rules[i].ID]
		}
	}
	m.st.Rules = rules
	err := m.saveLocked()
	m.mu.Unlock()
	if err != nil {
		return err
	}
	m.apply(ctx)
	return nil
}

// Matches lists the loaded article titles a rule would download (for the editor).
func (m *Manager) Matches(r Rule) ([]string, error) {
	if err := r.Validate(); err != nil {
		return nil, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	out := []string{}
	for _, f := range m.st.Feeds {
		if !r.AppliesTo(f.ID) {
			continue
		}
		for _, it := range f.Items {
			if r.Matches(it.Title) && len(out) < 200 {
				out = append(out, it.Title)
			}
		}
	}
	return out, nil
}

// Download adds one article by hand.
func (m *Manager) Download(ctx context.Context, feedID, key string) error {
	m.mu.Lock()
	var item *Item
	if i := m.feedIndexLocked(feedID); i >= 0 {
		for j := range m.st.Feeds[i].Items {
			if m.st.Feeds[i].Items[j].Key == key {
				it := m.st.Feeds[i].Items[j]
				item = &it
			}
		}
	}
	m.mu.Unlock()
	if item == nil {
		return errors.New("article not found")
	}
	if err := m.add(ctx, *item, Rule{}); err != nil && !errors.Is(err, engine.ErrExists) {
		return err
	}
	m.mu.Lock()
	m.st.Seen[feedID+"\x00"+key] = time.Now()
	m.saveLocked()
	m.mu.Unlock()
	return nil
}

// Refresh loads one feed (or all when id is "") now.
func (m *Manager) Refresh(ctx context.Context, id string) { m.refresh(ctx, id, true) }

func (m *Manager) refresh(ctx context.Context, id string, force bool) {
	m.refreshing.Lock()
	defer m.refreshing.Unlock()
	m.mu.Lock()
	var due []Feed
	for _, f := range m.st.Feeds {
		if id != "" && f.ID != id {
			continue
		}
		if force || (f.Enabled && time.Since(f.LastChecked) >= time.Duration(max(f.RefreshMin, minRefresh))*time.Minute) {
			due = append(due, f)
		}
	}
	m.mu.Unlock()
	if len(due) == 0 {
		return
	}
	for _, f := range due {
		fctx, cancel := context.WithTimeout(ctx, 30*time.Second)
		data, err := Fetch(fctx, m.client(), f.URL, MaxFeedSize)
		cancel()
		var title string
		var items []Item
		if err == nil {
			title, items, err = Parse(data)
		}
		m.mu.Lock()
		if i := m.feedIndexLocked(f.ID); i >= 0 {
			cur := &m.st.Feeds[i]
			cur.LastChecked = time.Now().UTC()
			if err != nil {
				cur.LastError = err.Error()
			} else {
				cur.LastError = ""
				if cur.Title == "" {
					cur.Title = title
				}
				if len(items) > maxItemsPerFeed {
					items = items[:maxItemsPerFeed]
				}
				cur.Items = items
			}
		}
		m.saveLocked()
		m.mu.Unlock()
		if err != nil {
			m.log.Warn("RSS feed", "url", logsafe.String(f.URL), "err", logsafe.Err(err))
		}
	}
	m.apply(ctx)
}

type job struct {
	feedID string
	item   Item
	rule   Rule
	ep     string
}

// apply downloads new articles that match an enabled rule.
func (m *Manager) apply(ctx context.Context) {
	m.mu.Lock()
	var jobs []job
	planned := map[string]bool{}
	for ri := range m.st.Rules {
		r := &m.st.Rules[ri]
		if !r.Enabled {
			continue
		}
		for _, f := range m.st.Feeds {
			if !r.AppliesTo(f.ID) {
				continue
			}
			for _, it := range f.Items {
				key := f.ID + "\x00" + it.Key
				if _, seen := m.st.Seen[key]; seen || planned[key] || !r.Matches(it.Title) {
					continue
				}
				ep := ""
				if r.SmartEpisodes {
					if ep = Episode(it.Title); ep != "" && (slices.Contains(r.Episodes, ep) || planned[r.ID+ep]) {
						continue
					}
				}
				planned[key] = true
				if ep != "" {
					planned[r.ID+ep] = true
				}
				jobs = append(jobs, job{feedID: f.ID, item: it, rule: *r, ep: ep})
			}
		}
	}
	m.mu.Unlock()

	for _, j := range jobs {
		err := m.add(ctx, j.item, j.rule)
		if err != nil && !errors.Is(err, engine.ErrExists) {
			m.log.Warn("RSS download", "title", logsafe.String(j.item.Title), "err", logsafe.Err(err))
			continue // try again on the next refresh
		}
		m.log.Info("RSS download", "rule", logsafe.String(j.rule.Name), "title", logsafe.String(j.item.Title))
		m.mu.Lock()
		m.st.Seen[j.feedID+"\x00"+j.item.Key] = time.Now().UTC()
		if j.ep != "" {
			for i := range m.st.Rules {
				if m.st.Rules[i].ID == j.rule.ID && !slices.Contains(m.st.Rules[i].Episodes, j.ep) {
					m.st.Rules[i].Episodes = append(m.st.Rules[i].Episodes, j.ep)
				}
			}
		}
		m.saveLocked()
		m.mu.Unlock()
	}
}

func (m *Manager) add(ctx context.Context, it Item, r Rule) error {
	opts := engine.AddOptions{Category: r.Category, SavePath: r.SavePath, Paused: r.Paused}
	if isMagnet(it.Torrent) {
		opts.Magnet = it.Torrent
	} else {
		fctx, cancel := context.WithTimeout(ctx, 60*time.Second)
		data, err := Fetch(fctx, m.client(), it.Torrent, engine.MaxTorrentFileSize)
		cancel()
		if err != nil {
			return fmt.Errorf("download .torrent: %w", err)
		}
		opts.TorrentFile = data
	}
	_, err := m.eng.Add(opts)
	return err
}

func (m *Manager) feedIndexLocked(id string) int {
	for i, f := range m.st.Feeds {
		if f.ID == id {
			return i
		}
	}
	return -1
}

func (m *Manager) saveLocked() error {
	if len(m.st.Seen) > maxSeen {
		// Forget the oldest entries; feeds rarely keep articles that long.
		type kv struct {
			k string
			t time.Time
		}
		all := make([]kv, 0, len(m.st.Seen))
		for k, t := range m.st.Seen {
			all = append(all, kv{k, t})
		}
		slices.SortFunc(all, func(a, b kv) int { return a.t.Compare(b.t) })
		for _, e := range all[:len(all)-maxSeen] {
			delete(m.st.Seen, e.k)
		}
	}
	b, err := json.MarshalIndent(m.st, "", "  ")
	if err != nil {
		return err
	}
	return config.WriteFileAtomic(m.path, b, 0o600)
}
