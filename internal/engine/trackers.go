package engine

import (
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/anacrolix/torrent"

	"github.com/nikolap994/magnetar/internal/config"
)

// TrackerView is a tracker and how it is doing.
type TrackerView struct {
	URL     string    `json:"url"`
	Status  string    `json:"status"` // working, error, updating, waiting, disabled
	Peers   int       `json:"peers"`
	Message string    `json:"message,omitempty"`
	Next    time.Time `json:"next,omitzero"`
	Extra   bool      `json:"extra"` // added by the user (not from the torrent itself)
}

func validTracker(u string) bool {
	lu := strings.ToLower(u)
	return (strings.HasPrefix(lu, "udp://") || strings.HasPrefix(lu, "http://") || strings.HasPrefix(lu, "https://") ||
		strings.HasPrefix(lu, "wss://")) && len(u) <= 2000 && !strings.ContainsAny(u, " \t\r\n")
}

// trackerViewsLocked combines the torrent's trackers with their live status.
func (e *Engine) trackerViewsLocked(m *managed, urls []string) []TrackerView {
	// UDP trackers are announced separately over IPv4 and IPv6 ("udp4://",
	// "udp6://"); show one line per tracker, working if either works.
	status := map[string]torrent.TrackerStatus{}
	if m.t != nil {
		for _, st := range m.t.TrackerStatuses() {
			key := st.URL
			if strings.HasPrefix(key, "udp4://") || strings.HasPrefix(key, "udp6://") {
				key = "udp://" + key[len("udp4://"):]
			}
			st.Error = shortError(st.Error)
			prev, seen := status[key]
			switch {
			case !seen:
				status[key] = st
			case st.Working && !prev.Working:
				status[key] = st
			case st.Working && prev.Working:
				prev.Peers = max(prev.Peers, st.Peers)
				status[key] = prev
			case !prev.Working && prev.Error == "" && st.Error != "":
				// keep "contacting" over an error from the other address family
			}
		}
	}
	out := make([]TrackerView, 0, len(urls))
	for _, u := range urls {
		v := TrackerView{URL: u, Status: "disabled", Extra: slices.Contains(m.rec.ExtraTrackers, u)}
		if m.t != nil {
			v.Status = "updating"
			if s, ok := status[u]; ok {
				v.Peers, v.Next = s.Peers, s.NextAnnounce
				switch {
				case s.Error != "":
					v.Status, v.Message = "error", s.Error
				case s.Working:
					v.Status = "working"
				case s.Other != "":
					v.Status, v.Message = "working", s.Other
				}
			}
		}
		out = append(out, v)
	}
	return out
}

// shortError drops the request URL that HTTP errors repeat in full.
func shortError(e string) string {
	if i := strings.LastIndex(e, "\": "); i >= 0 && strings.HasPrefix(e, "Get \"") {
		e = e[i+3:]
	}
	if i := strings.LastIndex(e, "write error: "); i >= 0 {
		e = e[i+len("write error: "):]
	}
	if len(e) > 160 {
		e = e[:160] + "…"
	}
	return e
}

// RemoveTracker stops using a tracker for a torrent.
func (e *Engine) RemoveTracker(hash, url string) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	m, err := e.get(hash)
	if err != nil {
		return err
	}
	m.rec.ExtraTrackers = slices.DeleteFunc(m.rec.ExtraTrackers, func(u string) bool { return u == url })
	if !slices.Contains(m.rec.RemovedTrackers, url) {
		m.rec.RemovedTrackers = append(m.rec.RemovedTrackers, url)
	}
	if m.t != nil {
		var tiers [][]string
		mi := m.t.Metainfo()
		for _, tier := range mi.UpvertedAnnounceList() {
			tier = slices.DeleteFunc(slices.Clone(tier), func(u string) bool { return u == url })
			if len(tier) > 0 {
				tiers = append(tiers, tier)
			}
		}
		m.t.ModifyTrackers(tiers)
	}
	e.dirty = true
	return nil
}

// filterTiers drops removed trackers from announce tiers.
func filterTiers(tiers [][]string, removed []string) [][]string {
	if len(removed) == 0 {
		return tiers
	}
	var out [][]string
	for _, tier := range tiers {
		tier = slices.DeleteFunc(slices.Clone(tier), func(u string) bool { return slices.Contains(removed, u) })
		if len(tier) > 0 {
			out = append(out, tier)
		}
	}
	return out
}

// defaultTrackers returns the user's list of trackers added to new public
// torrents, when that option is on.
func defaultTrackers(s config.Settings) []string {
	if !s.AddDefaultTrackers {
		return nil
	}
	var out []string
	for _, line := range strings.Split(s.DefaultTrackers, "\n") {
		if u := strings.TrimSpace(line); u != "" && validTracker(u) && !slices.Contains(out, u) {
			out = append(out, u)
		}
	}
	return out
}

// AddPeer connects a torrent to a peer by address ("1.2.3.4:6881" or "[::1]:6881").
func (e *Engine) AddPeer(hash, addr string) error {
	host, port, err := net.SplitHostPort(strings.TrimSpace(addr))
	if err != nil {
		return errors.New("enter the peer as address:port")
	}
	ip := net.ParseIP(host)
	p, err := strconv.Atoi(port)
	if ip == nil || err != nil || p < 1 || p > 65535 {
		return errors.New("enter the peer as an IP address and port, e.g. 203.0.113.5:6881")
	}
	e.mu.RLock()
	defer e.mu.RUnlock()
	m, err := e.get(hash)
	if err != nil {
		return err
	}
	if m.t == nil {
		return ErrNotRunning
	}
	m.t.AddPeers([]torrent.PeerInfo{{Addr: &net.TCPAddr{IP: ip, Port: p}, Source: torrent.PeerSourceDirect, Trusted: true}})
	return nil
}

func (e *Engine) bansPath() string { return filepath.Join(e.dataDir, "banned-peers.json") }

func (e *Engine) loadBans() {
	b, err := os.ReadFile(e.bansPath())
	if err != nil {
		return
	}
	var ips []string
	if json.Unmarshal(b, &ips) == nil {
		e.bans = ips
	}
}

// BanPeers disconnects and blocks peer IP addresses (kept across restarts).
func (e *Engine) BanPeers(ips ...string) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	for _, s := range ips {
		ip := net.ParseIP(strings.TrimSpace(s))
		if ip == nil {
			return fmt.Errorf("not an IP address: %q", s)
		}
		if !slices.Contains(e.bans, ip.String()) {
			e.bans = append(e.bans, ip.String())
		}
		if e.cl != nil {
			e.cl.BanPeerIP(ip)
		}
	}
	return e.saveBansLocked()
}

// Bans lists banned peer addresses.
func (e *Engine) Bans() []string {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return slices.Clone(e.bans)
}

// ClearBans forgets all banned peers (takes effect when the client restarts).
func (e *Engine) ClearBans() error {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.bans = nil
	if err := e.saveBansLocked(); err != nil {
		return err
	}
	return e.restartLocked()
}

func (e *Engine) saveBansLocked() error {
	b, _ := json.Marshal(e.bans)
	return config.WriteFileAtomic(e.bansPath(), b, 0o600)
}

func (e *Engine) applyBansLocked() {
	for _, s := range e.bans {
		if ip := net.ParseIP(s); ip != nil && e.cl != nil {
			e.cl.BanPeerIP(ip)
		}
	}
}
