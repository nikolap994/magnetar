package torrent

// Magnetar patch: exported tracker status and peer banning.

import (
	"net"
	"sort"
	"time"
)

// TrackerStatus describes one tracker of a torrent.
type TrackerStatus struct {
	URL          string
	Working      bool      // last announce succeeded
	Peers        int       // peers returned by the last successful announce
	Error        string    // last error, if the last announce failed
	LastAnnounce time.Time // last successful announce
	NextAnnounce time.Time // when the next regular announce is due
	Other        string    // status text for non-HTTP/UDP trackers (WebSocket)
}

// TrackerStatuses reports the state of every tracker announcer.
func (t *Torrent) TrackerStatuses() []TrackerStatus {
	t.cl.rLock()
	defer t.cl.rUnlock()
	out := make([]TrackerStatus, 0, len(t.trackerAnnouncers))
	for _, ta := range t.trackerAnnouncers {
		st := TrackerStatus{URL: ta.URL().String()}
		if r, ok := ta.(torrentRegularTrackerAnnouncer); ok && r.getAnnounceState != nil {
			s := r.getAnnounceState()
			st.LastAnnounce, st.Peers = s.lastOk.Completed, s.lastOk.NumPeers
			if !s.lastOk.Completed.IsZero() {
				st.NextAnnounce = s.lastOk.Completed.Add(s.lastOk.Interval)
			}
			if s.Err != nil {
				st.Error = s.Err.Error()
			} else {
				st.Working = !s.lastOk.Completed.IsZero()
			}
		} else {
			st.Other = ta.statusLine()
		}
		out = append(out, st)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].URL < out[j].URL })
	return out
}

// BanPeerIP disconnects every peer with this address and refuses it from now
// on (until the client restarts).
func (cl *Client) BanPeerIP(ip net.IP) {
	cl.lock()
	defer cl.unlock()
	cl.banPeerIP(ip)
}
