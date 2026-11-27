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

// SwarmHealth describes how much of a torrent the connected peers can supply.
type SwarmHealth struct {
	// Copies is the number of complete copies among connected peers (the
	// "distributed copies" figure): the lowest piece availability plus the
	// share of pieces that are more available than that.
	Copies float64
	// Missing is the number of wanted pieces we lack that no connected peer
	// has: while it is above zero the download cannot finish with these peers.
	Missing int
}

// Health reports the swarm health; ok is false until the metadata is known.
func (t *Torrent) Health() (h SwarmHealth, ok bool) {
	t.cl.rLock()
	defer t.cl.rUnlock()
	if !t.haveInfo() || t.numPieces() == 0 {
		return h, false
	}
	n := t.numPieces()
	low, above := -1, 0
	avail := make([]int, n)
	for i := range n {
		a := t.piece(i).availability()
		avail[i] = a
		if low < 0 || a < low {
			low = a
		}
	}
	for i := range n {
		if avail[i] > low {
			above++
		}
		if avail[i] == 0 && !t.pieceComplete(i) && t.wantPieceIndex(i) {
			h.Missing++
		}
	}
	h.Copies = float64(low) + float64(above)/float64(n)
	return h, true
}
