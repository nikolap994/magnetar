package engine

import (
	"math"
	"slices"
	"sort"
	"time"

	"github.com/anacrolix/dht/v2"
	"github.com/anacrolix/torrent"
	"github.com/anacrolix/torrent/metainfo"
)

// Torrent states reported to the UI.
const (
	StateDownloading = "downloading"
	StateSeeding     = "seeding"
	StateCompleted   = "completed"
	StatePaused      = "paused"
	StateMetadata    = "metadata"
	StateStalled     = "stalled"
	StateError       = "error"
	StateChecking    = "checking"
	StateMoving      = "moving"
	StateQueued      = "queued"
)

// Summary is a row in the torrent list.
type Summary struct {
	Hash        string     `json:"hash"`
	Name        string     `json:"name"`
	State       string     `json:"state"`
	Size        int64      `json:"size"`
	Completed   int64      `json:"completed"`
	Progress    float64    `json:"progress"`
	DownRate    int64      `json:"downRate"`
	UpRate      int64      `json:"upRate"`
	Downloaded  int64      `json:"downloaded"`
	Uploaded    int64      `json:"uploaded"`
	Ratio       float64    `json:"ratio"`
	ETA         int64      `json:"eta"` // seconds, -1 unknown
	Peers       int        `json:"peers"`
	Seeds       int        `json:"seeds"`
	KnownPeers  int        `json:"knownPeers"`
	SavePath    string     `json:"savePath"`
	AddedAt     time.Time  `json:"addedAt"`
	CompletedAt *time.Time `json:"completedAt,omitempty"`
	Sequential  bool       `json:"sequential"`
	Category    string     `json:"category,omitempty"`
	Error       string     `json:"error,omitempty"`
	Forced      bool       `json:"forced,omitempty"`
	Tags        []string   `json:"tags,omitempty"`
	DownLimit   int64      `json:"downLimit,omitempty"` // bytes/s, 0 = none
	UpLimit     int64      `json:"upLimit,omitempty"`
	QueuePos    int        `json:"queuePos"` // 1-based position in the queue (list order)
}

func (e *Engine) summaryLocked(m *managed) Summary {
	s := Summary{
		Hash:        m.rec.InfoHash,
		Name:        m.rec.Name,
		SavePath:    m.rec.SavePath,
		AddedAt:     m.rec.AddedAt,
		CompletedAt: m.rec.CompletedAt,
		Sequential:  m.rec.Sequential,
		Category:    m.rec.Category,
		DownRate:    int64(m.downRate),
		UpRate:      int64(m.upRate),
		ETA:         -1,
		Error:       m.err,
		Forced:      m.rec.Forced,
		Tags:        m.rec.Tags,
		DownLimit:   m.rec.DownLimitKBps * 1024,
		UpLimit:     m.rec.UpLimitKBps * 1024,
	}
	s.Downloaded, s.Uploaded = m.totals()
	t := m.t
	if t != nil {
		if t.Info() != nil {
			s.Size = t.Length()
			s.Completed = t.BytesCompleted()
		}
		st := t.Stats()
		s.Peers = st.ActivePeers
		s.Seeds = st.ConnectedSeeders
		s.KnownPeers = st.TotalPeers
	} else {
		// Paused: report what we last knew without loading metadata.
		s.Size = m.rec.Size
		s.Completed = m.rec.Completed
		if m.rec.CompletedAt != nil {
			s.Completed = s.Size
			s.Progress = 1
		}
	}
	if s.Size > 0 {
		s.Progress = float64(s.Completed) / float64(s.Size)
	}
	s.Ratio = FormatRatio(ratio(s.Downloaded, s.Uploaded, s.Size))

	switch {
	case m.moving:
		s.State = StateMoving
	case m.err != "":
		s.State = StateError
	case t == nil && m.queuedLocked():
		s.State = StateQueued
	case t == nil:
		s.State = StatePaused
	case t.Info() == nil:
		s.State = StateMetadata
	case m.checking:
		s.State = StateChecking
	case s.Completed >= s.Size:
		if e.settings.SeedAfterComplete {
			s.State = StateSeeding
		} else {
			s.State = StateCompleted
		}
	case s.DownRate > 0:
		s.State = StateDownloading
	case s.Peers == 0:
		s.State = StateStalled
	default:
		s.State = StateDownloading
	}
	if s.State == StateDownloading && s.DownRate > 0 {
		s.ETA = (s.Size - s.Completed) / s.DownRate
	}
	return s
}

// List returns all torrents in insertion order.
func (e *Engine) List() []Summary {
	e.mu.RLock()
	defer e.mu.RUnlock()
	out := make([]Summary, 0, len(e.order))
	for i, h := range e.order {
		s := e.summaryLocked(e.torrents[h])
		s.QueuePos = i + 1
		out = append(out, s)
	}
	return out
}

// GlobalStats are shown in the status bar.
type GlobalStats struct {
	DownRate      int64  `json:"downRate"`
	UpRate        int64  `json:"upRate"`
	DownLimit     int64  `json:"downLimit"`
	UpLimit       int64  `json:"upLimit"`
	AltSpeed      bool   `json:"altSpeed"`
	Torrents      int    `json:"torrents"`
	Active        int    `json:"active"`
	Peers         int    `json:"peers"`
	DHTNodes      int    `json:"dhtNodes"`
	ListenPort    int    `json:"listenPort"`
	Encryption    string `json:"encryption"`
	BlocklistSize int    `json:"blocklistSize"`
	BindAddress   string `json:"bindAddress,omitempty"`
	BindInterface string `json:"bindInterface,omitempty"`
	// KillSwitch is true while the bound interface is down and traffic is stopped.
	KillSwitch bool   `json:"killSwitch,omitempty"`
	Proxy      string `json:"proxy,omitempty"` // "socks5" / "http" when a proxy is used
	ProxyPeers bool   `json:"proxyPeers,omitempty"`
	Version    string `json:"version"`
	Online     bool   `json:"online"`
	// Connection is "connectable", "firewalled", "offline", "vpn-down", "idle" or "checking".
	Connection string `json:"connection"`
	UPnP       bool   `json:"upnp"`
}

// Stats returns client-wide statistics.
func (e *Engine) Stats() GlobalStats {
	e.mu.RLock()
	defer e.mu.RUnlock()
	g := GlobalStats{
		DownRate:      int64(e.downRate),
		UpRate:        int64(e.upRate),
		AltSpeed:      e.settings.AltSpeedEnabled,
		Torrents:      len(e.torrents),
		Encryption:    e.settings.Encryption,
		BlocklistSize: e.blocklistSize,
		BindAddress:   e.settings.BindAddress,
		BindInterface: e.settings.BindInterface,
		KillSwitch:    e.killSwitch,
		Proxy:         e.settings.ProxyType,
		ProxyPeers:    e.settings.ProxyPeers,
		Version:       Version,
	}
	down, up := e.settings.EffectiveLimits()
	g.DownLimit, g.UpLimit = down*1024, up*1024
	for _, m := range e.torrents {
		if m.t != nil {
			g.Active++
			g.Peers += m.t.Stats().ActivePeers
		}
	}
	if e.cl != nil {
		g.Online = true
		g.UPnP = e.settings.EnableUPnP && e.boundIP == nil && !e.settings.ProxyPeers
		if e.boundIP != nil && !e.killSwitch {
			g.BindAddress = e.boundIP.String()
		}
		g.ListenPort = e.cl.LocalPort()
		for _, s := range e.cl.DhtServers() {
			if st, ok := s.Stats().(dht.ServerStats); ok {
				g.DHTNodes += st.GoodNodes
			}
		}
	}
	g.Connection = e.connectionLocked(g)
	return g
}

// firewalledAfter is how long we wait for an incoming connection while
// actively transferring before warning that the port seems unreachable.
const firewalledAfter = 3 * time.Minute

func (e *Engine) connectionLocked(g GlobalStats) string {
	switch {
	case e.killSwitch:
		return "vpn-down"
	case !g.Online || (e.settings.EnableDHT && g.DHTNodes == 0 && g.Peers == 0 && time.Since(e.started) > time.Minute):
		return "offline"
	case e.incoming.Load() > 0:
		return "connectable"
	case e.activeSince.IsZero():
		return "idle" // nothing exchanges data yet, so there is nothing to measure
	case !e.activeSince.IsZero() && time.Since(e.activeSince) > firewalledAfter:
		return "firewalled"
	default:
		return "checking"
	}
}

// FileView describes one file in a torrent.
type FileView struct {
	Index     int     `json:"index"`
	Path      string  `json:"path"`
	Size      int64   `json:"size"`
	Completed int64   `json:"completed"`
	Progress  float64 `json:"progress"`
	Priority  int     `json:"priority"`
}

// PeerView describes one connected peer.
type PeerView struct {
	Addr     string  `json:"addr"`
	Client   string  `json:"client"`
	Network  string  `json:"network"`
	Source   string  `json:"source"`
	Progress float64 `json:"progress"`
	DownRate int64   `json:"downRate"`
	UpRate   int64   `json:"upRate"`
	Incoming bool    `json:"incoming"`
}

// Detail is the full view of one torrent.
type Detail struct {
	Summary
	Comment   string     `json:"comment,omitempty"`
	CreatedBy string     `json:"createdBy,omitempty"`
	CreatedAt *time.Time `json:"createdAt,omitempty"`
	PieceSize int64      `json:"pieceSize"`
	NumPieces int        `json:"numPieces"`
	Private   bool       `json:"private"`
	Magnet    string     `json:"magnet"`
	Files     []FileView `json:"files"`
	PeerList  []PeerView `json:"peerList"`
	Trackers  []string   `json:"trackers"`
	// TrackerList has each tracker's live status.
	TrackerList []TrackerView `json:"trackerList"`
	PieceMap    []int         `json:"pieceMap"` // downsampled: 0 missing … 100 complete, 255 checking
	WebSeeds    int           `json:"webSeeds"`
}

const pieceMapBuckets = 240

// Detail returns everything known about one torrent.
func (e *Engine) Detail(hash string) (Detail, error) {
	e.mu.RLock()
	defer e.mu.RUnlock()
	m, err := e.get(hash)
	if err != nil {
		return Detail{}, err
	}
	d := Detail{Summary: e.summaryLocked(m), Files: []FileView{}, PeerList: []PeerView{}, Trackers: []string{}}
	t := m.t
	if t == nil {
		e.stoppedDetailLocked(m, &d)
		return d, nil
	}
	mi := t.Metainfo()
	// The library synthesises these fields for magnet-only torrents; only show real ones.
	if fromFile, err := metainfo.LoadFromFile(e.metainfoPath(m.rec.InfoHash)); err == nil && fromFile.Comment != "dynamic metainfo from client" {
		d.Comment = fromFile.Comment
		d.CreatedBy = fromFile.CreatedBy
		mi.CreationDate = fromFile.CreationDate
	} else {
		mi.CreationDate = 0
	}
	if mi.CreationDate > 0 {
		ts := time.Unix(mi.CreationDate, 0).UTC()
		d.CreatedAt = &ts
	}
	seen := map[string]bool{}
	for _, tier := range mi.UpvertedAnnounceList() {
		for _, u := range tier {
			if !seen[u] {
				seen[u] = true
				d.Trackers = append(d.Trackers, u)
			}
		}
	}
	for _, u := range m.rec.ExtraTrackers {
		if !seen[u] {
			seen[u] = true
			d.Trackers = append(d.Trackers, u)
		}
	}
	d.Trackers = slices.DeleteFunc(d.Trackers, func(u string) bool { return slices.Contains(m.rec.RemovedTrackers, u) })
	d.TrackerList = e.trackerViewsLocked(m, d.Trackers)
	if mag, err := mi.MagnetV2(); err == nil {
		d.Magnet = mag.String()
	} else {
		d.Magnet = m.rec.Magnet
	}
	d.WebSeeds = len(t.WebseedPeerConns())

	if info := t.Info(); info != nil {
		d.PieceSize = info.PieceLength
		d.NumPieces = t.NumPieces()
		d.Private = info.Private != nil && *info.Private
		for i, f := range t.Files() {
			fv := FileView{Index: i, Path: f.DisplayPath(), Size: f.Length(), Completed: f.BytesCompleted(), Priority: PriorityNormal}
			if p, ok := m.rec.FilePriorities[i]; ok {
				fv.Priority = p
			}
			if fv.Size > 0 {
				fv.Progress = float64(fv.Completed) / float64(fv.Size)
			} else {
				fv.Progress = 1
			}
			d.Files = append(d.Files, fv)
		}
		d.PieceMap = pieceMap(t)
	}

	numPieces := max(t.NumPieces(), 1)
	for _, pc := range t.PeerConns() {
		st := pc.Stats()
		pv := PeerView{
			Addr:     pc.RemoteAddr.String(),
			Network:  pc.Network,
			Source:   sourceName(pc.Discovery),
			DownRate: int64(st.DownloadRate),
			UpRate:   int64(st.LastWriteUploadRate),
			Progress: math.Min(1, float64(st.RemotePieceCount)/float64(numPieces)),
			Incoming: pc.Discovery == torrent.PeerSourceIncoming,
		}
		if v, ok := pc.PeerClientName.Load().(string); ok {
			pv.Client = v
		}
		d.PeerList = append(d.PeerList, pv)
	}
	sort.Slice(d.PeerList, func(i, j int) bool {
		return d.PeerList[i].DownRate+d.PeerList[i].UpRate > d.PeerList[j].DownRate+d.PeerList[j].UpRate
	})
	return d, nil
}

func sourceName(s torrent.PeerSource) string {
	switch s {
	case torrent.PeerSourceTracker:
		return "Tracker"
	case torrent.PeerSourceIncoming:
		return "Incoming"
	case torrent.PeerSourceDhtGetPeers, torrent.PeerSourceDhtAnnouncePeer:
		return "DHT"
	case torrent.PeerSourcePex:
		return "PEX"
	case torrent.PeerSourceLPD:
		return "LSD"
	case torrent.PeerSourceDirect:
		return "Manual"
	case torrent.PeerSourceUtHolepunch:
		return "Holepunch"
	default:
		return string(s)
	}
}

// pieceMap downsamples piece completion into fixed buckets for the UI bar.
func pieceMap(t *torrent.Torrent) []int {
	n := t.NumPieces()
	if n == 0 {
		return nil
	}
	buckets := min(n, pieceMapBuckets)
	out := make([]int, buckets)
	complete := make([]int, buckets)
	total := make([]int, buckets)
	checking := make([]bool, buckets)
	i := 0
	for _, run := range t.PieceStateRuns() {
		for k := 0; k < run.Length; k++ {
			b := i * buckets / n
			total[b]++
			if run.Complete {
				complete[b]++
			}
			if run.Hashing || run.QueuedForHash {
				checking[b] = true
			}
			i++
		}
	}
	for b := range out {
		switch {
		case checking[b]:
			out[b] = 255
		case total[b] > 0:
			out[b] = complete[b] * 100 / total[b]
		}
	}
	return out
}
