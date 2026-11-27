// Package engine manages the BitTorrent client, torrent lifecycle and persistence.
package engine

import (
	"bytes"
	"compress/gzip"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"math"
	"net"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/anacrolix/torrent"
	"github.com/anacrolix/torrent/iplist"
	"github.com/anacrolix/torrent/metainfo"
	"github.com/anacrolix/torrent/mse"
	"github.com/anacrolix/torrent/storage"
	"golang.org/x/time/rate"

	"github.com/nikolap994/magnetar/internal/config"
	"github.com/nikolap994/magnetar/internal/fsutil"
	"github.com/nikolap994/magnetar/internal/logsafe"
)

// Version is the application version, overridden at build time.
var Version = "0.1.0-dev"

// MaxTorrentFileSize bounds .torrent uploads to keep memory use predictable.
const MaxTorrentFileSize = 16 << 20

var (
	ErrNotFound = errors.New("torrent not found")
	ErrExists   = errors.New("torrent already added")
	ErrClosed   = errors.New("engine is shut down")
)

// Engine owns the torrent client and all managed torrents.
type Engine struct {
	mu       sync.RWMutex
	dataDir  string
	settings config.Settings
	cl       *torrent.Client
	log      *slog.Logger

	completion storage.PieceCompletion
	storages   map[string]storage.ClientImplCloser

	torrents map[string]*managed
	previews map[string]*preview
	order    []string

	downLimiter *rate.Limiter
	upLimiter   *rate.Limiter

	downRate, upRate float64
	blocklistSize    int
	boundIP          net.IP    // address peer traffic is bound to, nil = all
	killSwitch       bool      // bound interface is down; traffic is stopped
	lastIfCheck      time.Time // last lookup of the bound interface
	creates          createJobs

	// Speed schedule: last schedule state applied, to act only on boundaries.
	schedKnown, schedLast bool
	// onSettingsChanged persists settings the engine changes by itself
	// (e.g. the speed schedule).
	onSettingsChanged func(config.Settings)

	stats statsState

	// Connection health: incoming peer connections prove the port is reachable.
	started     time.Time
	incoming    atomic.Int64 // incoming peer connections since the client started
	activeSince time.Time    // when we first had a running torrent with peers

	subsMu sync.Mutex
	subs   map[chan Event]struct{}

	dirty  bool
	ctx    context.Context
	cancel context.CancelFunc
	wg     sync.WaitGroup
}

type managed struct {
	hash metainfo.Hash
	rec  *record
	t    *torrent.Torrent // nil while paused

	baseDown, baseUp int64 // totals from previous sessions
	lastDown, lastUp int64 // last sample of the live torrent's counters
	downRate, upRate float64
	err              string
	seqPieces        map[int]struct{}
	// checkExisting asks for existing files to be verified once metadata
	// arrives, so re-adding a torrent resumes from what is already on disk.
	checkExisting bool
	checking      bool
	moving        bool
	startedAt     time.Time
}

func (m *managed) liveStats() (down, up int64) {
	if m.t == nil {
		return 0, 0
	}
	s := m.t.Stats()
	return s.BytesReadUsefulData.Int64(), s.BytesWrittenData.Int64()
}

func (m *managed) totals() (down, up int64) {
	d, u := m.liveStats()
	return m.baseDown + d, m.baseUp + u
}

// Event is pushed to UI subscribers for notifications.
type Event struct {
	Type    string `json:"type"` // added, completed, removed, error
	Hash    string `json:"hash"`
	Name    string `json:"name"`
	Message string `json:"message,omitempty"`
}

// New starts the engine, restoring the previous session.
func New(s config.Settings, dataDir string, logger *slog.Logger) (*Engine, error) {
	if err := os.MkdirAll(s.DownloadDir, 0o755); err != nil {
		return nil, fmt.Errorf("create download dir: %w", err)
	}
	e := &Engine{
		dataDir:     dataDir,
		settings:    s,
		log:         logger,
		storages:    map[string]storage.ClientImplCloser{},
		torrents:    map[string]*managed{},
		previews:    map[string]*preview{},
		subs:        map[chan Event]struct{}{},
		downLimiter: rate.NewLimiter(rate.Inf, 1<<30),
		upLimiter:   rate.NewLimiter(rate.Inf, 1<<30),
	}
	if err := os.MkdirAll(e.metainfoDir(), 0o700); err != nil {
		return nil, err
	}
	pc, err := storage.NewBoltPieceCompletion(dataDir)
	if err != nil {
		return nil, fmt.Errorf("open piece database (is Magnetar already running?): %w", err)
	}
	e.completion = pc
	e.applyLimits(s)

	cl, err := e.newClientRetry(s)
	if err != nil {
		pc.Close()
		return nil, err
	}
	e.cl = cl
	e.ctx, e.cancel = context.WithCancel(context.Background())
	e.started = time.Now()
	e.loadStats()

	recs, err := e.loadState()
	if err != nil {
		e.log.Warn("could not read previous session, starting fresh", "err", err)
	}
	e.mu.Lock()
	for _, r := range recs {
		h := metainfo.NewHashFromHex(r.InfoHash)
		m := &managed{hash: h, rec: r, baseDown: r.Downloaded, baseUp: r.Uploaded}
		if r.Size == 0 {
			if mi, err := metainfo.LoadFromFile(e.metainfoPath(r.InfoHash)); err == nil {
				if info, err := mi.UnmarshalInfo(); err == nil {
					r.Size = info.TotalLength()
				}
			}
		}
		e.torrents[r.InfoHash] = m
		e.order = append(e.order, r.InfoHash)
	}
	// Start what was running, within the queue limits.
	e.queueLocked(time.Now())
	e.mu.Unlock()

	e.wg.Add(2)
	go e.loop()
	go e.watchLoop()
	return e, nil
}

// noCloseCompletion lets several storage instances share one piece database.
type noCloseCompletion struct{ storage.PieceCompletion }

func (noCloseCompletion) Close() error { return nil }

func (e *Engine) storageFor(dir string) storage.ClientImpl {
	dir = filepath.Clean(dir)
	if st, ok := e.storages[dir]; ok {
		return st
	}
	st := storage.NewFileOpts(storage.NewFileClientOpts{
		ClientBaseDir:   dir,
		PieceCompletion: noCloseCompletion{e.completion},
		Logger:          e.log,
	})
	e.storages[dir] = st
	return st
}

func (e *Engine) applyLimits(s config.Settings) {
	down, up := s.EffectiveLimits()
	applyLimit(e.downLimiter, down)
	applyLimit(e.upLimiter, up)
}

func applyLimit(l *rate.Limiter, kbps int64) {
	if kbps <= 0 {
		// The BitTorrent library refuses new connections when the download
		// limiter reports no tokens, and tokens are capped at the burst, so an
		// unlimited limiter still needs a large burst.
		l.SetLimit(rate.Inf)
		l.SetBurst(1 << 30)
		return
	}
	bps := int(kbps * 1024)
	l.SetLimit(rate.Limit(bps))
	l.SetBurst(max(bps, 1<<20))
}

func (e *Engine) newClient(s config.Settings) (*torrent.Client, error) {
	cfg := torrent.NewDefaultClientConfig()
	cfg.Slogger = e.log.With("component", "bittorrent")
	cfg.DataDir = s.DownloadDir
	cfg.DefaultStorage = e.storageFor(s.DownloadDir)
	cfg.ListenPort = s.ListenPort
	cfg.Seed = s.SeedAfterComplete

	// Discovery.
	cfg.NoDHT = !s.EnableDHT
	cfg.DisablePEX = !s.EnablePEX
	if s.EnableLSD {
		cfg.LocalServiceDiscovery = &torrent.LocalServiceDiscoveryConfig{}
	}
	cfg.DisableUTP = !s.EnableUTP
	cfg.NoDefaultPortForwarding = !s.EnableUPnP
	cfg.DisableIPv6 = s.DisableIPv6

	// Binding peer, tracker and DHT traffic to one address. If that address
	// disappears (e.g. the VPN drops) sockets fail instead of leaking traffic
	// over another interface.
	ip, killed, err := bindIP(s)
	if err != nil {
		e.log.Warn("network binding", "err", logsafe.Err(err))
	}
	e.boundIP, e.killSwitch = ip, killed
	if ip != nil {
		applyBind(cfg, ip)
	}
	applyProxy(cfg, s)

	// Protocol encryption.
	switch s.Encryption {
	case config.EncryptionRequire:
		cfg.HeaderObfuscationPolicy = torrent.HeaderObfuscationPolicy{Preferred: true, RequirePreferred: true}
		cfg.CryptoProvides = mse.CryptoMethodRC4
	case config.EncryptionAllow:
		cfg.HeaderObfuscationPolicy = torrent.HeaderObfuscationPolicy{Preferred: false}
	default:
		cfg.HeaderObfuscationPolicy = torrent.HeaderObfuscationPolicy{Preferred: true}
	}

	// Performance tuning.
	cfg.EstablishedConnsPerTorrent = s.MaxConnsPerTorrent
	cfg.HalfOpenConnsPerTorrent = max(s.MaxConnsPerTorrent/2, 10)
	cfg.TotalHalfOpenConns = s.MaxHalfOpenConns
	cfg.TorrentPeersHighWater = 1000
	cfg.TorrentPeersLowWater = 100
	cfg.PieceHashersPerTorrent = s.PieceHashers
	cfg.DialRateLimiter = rate.NewLimiter(50, 50)
	cfg.MaxUnverifiedBytes = 128 << 20
	cfg.DownloadRateLimiter = e.downLimiter
	cfg.UploadRateLimiter = e.upLimiter

	// Identity.
	if s.AnonymousMode {
		cfg.AnonymousMode = true
		cfg.HTTPUserAgent = ""
		cfg.ExtendedHandshakeClientVersion = ""
	} else {
		cfg.HTTPUserAgent = "Magnetar/" + Version
		cfg.ExtendedHandshakeClientVersion = "Magnetar " + Version
		cfg.Bep20 = "-MG0100-"
	}

	// IP blocklist.
	e.blocklistSize = 0
	if s.BlocklistPath != "" {
		bl, n, err := loadBlocklist(s.BlocklistPath)
		if err != nil {
			return nil, fmt.Errorf("load blocklist: %w", err)
		}
		cfg.IPBlocklist = bl
		e.blocklistSize = n
	}

	// Every incoming connection proves the port is reachable. Counted as it
	// happens (short connections are easy to miss when sampling), without
	// taking the engine lock from inside the client.
	e.incoming.Store(0)
	e.activeSince = time.Time{}
	cfg.Callbacks.PeerConnAdded = append(cfg.Callbacks.PeerConnAdded, func(pc *torrent.PeerConn) {
		if pc.Discovery == torrent.PeerSourceIncoming {
			e.incoming.Add(1)
		}
	})
	cl, err := torrent.NewClient(cfg)
	if err != nil {
		return nil, fmt.Errorf("start BitTorrent client: %w", err)
	}
	if d := proxyDialer(s, ip); d != nil && !killed {
		cl.AddDialer(d)
	}
	return cl, nil
}

// newClientRetry starts a client, retrying while the previous client's
// sockets are released and finally falling back to a random listen port.
func (e *Engine) newClientRetry(s config.Settings) (*torrent.Client, error) {
	var err error
	for range 10 {
		var cl *torrent.Client
		if cl, err = e.newClient(s); err == nil {
			return cl, nil
		}
		if !isAddrInUse(err) {
			return nil, err
		}
		time.Sleep(300 * time.Millisecond)
	}
	if s.ListenPort != 0 {
		e.log.Warn("configured listen port is busy, using a random port")
		s.ListenPort = 0
		return e.newClient(s)
	}
	return nil, err
}

func isAddrInUse(err error) bool {
	return err != nil && (errors.Is(err, syscall.EADDRINUSE) ||
		strings.Contains(strings.ToLower(err.Error()), "address already in use") ||
		strings.Contains(err.Error(), "Only one usage of each socket address"))
}

func loadBlocklist(path string) (*iplist.IPList, int, error) {
	path, err := fsutil.CleanAbs(path)
	if err != nil {
		return nil, 0, err
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, 0, err
	}
	defer f.Close()
	r := io.LimitReader(f, 512<<20)
	if strings.HasSuffix(path, ".gz") {
		gz, err := gzip.NewReader(r)
		if err != nil {
			return nil, 0, err
		}
		defer gz.Close()
		r = io.LimitReader(gz, 1<<30)
	}
	l, err := iplist.NewFromReader(r)
	if err != nil {
		return nil, 0, err
	}
	return l, l.NumRanges(), nil
}

// AddOptions describe a new torrent.
type AddOptions struct {
	Magnet      string
	TorrentFile []byte
	// PreviewHash adds a torrent whose metadata was fetched with Preview.
	PreviewHash string
	SavePath    string
	Paused      bool
	Sequential  bool
	// FilePriorities by file index; missing entries are PriorityNormal.
	FilePriorities map[int]int
	// Category to assign; its folder is used when SavePath is empty.
	Category string
	// Totals carried over from another client when importing.
	Downloaded, Uploaded int64
}

// Add adds a torrent from a magnet link or .torrent file contents.
func (e *Engine) Add(opts AddOptions) (string, error) {
	var (
		spec *torrent.TorrentSpec
		err  error
		mi   *metainfo.MetaInfo
	)
	if opts.PreviewHash != "" {
		e.mu.Lock()
		file, magnet, perr := e.takePreviewLocked(opts.PreviewHash)
		e.mu.Unlock()
		if perr != nil {
			return "", perr
		}
		opts.TorrentFile, opts.Magnet = file, magnet
	}
	for i, p := range opts.FilePriorities {
		if i < 0 || p < PrioritySkip || p > PriorityHigh {
			return "", errors.New("invalid file priority")
		}
	}
	switch {
	case len(opts.TorrentFile) > 0:
		if len(opts.TorrentFile) > MaxTorrentFileSize {
			return "", errors.New("torrent file is too large")
		}
		mi, err = metainfo.Load(bytes.NewReader(opts.TorrentFile))
		if err != nil {
			return "", fmt.Errorf("invalid torrent file: %w", err)
		}
		if _, err := mi.UnmarshalInfo(); err != nil {
			return "", fmt.Errorf("invalid torrent info: %w", err)
		}
		spec, err = torrent.TorrentSpecFromMetaInfoErr(mi)
	case opts.Magnet != "":
		magnet := strings.TrimSpace(opts.Magnet)
		if !strings.HasPrefix(strings.ToLower(magnet), "magnet:?") {
			if h, ok := parseBareHash(magnet); ok {
				magnet = "magnet:?xt=urn:btih:" + h
			} else {
				return "", errors.New("not a magnet link or info hash")
			}
		}
		opts.Magnet = magnet
		spec, err = torrent.TorrentSpecFromMagnetUri(magnet)
	default:
		return "", errors.New("provide a magnet link or a torrent file")
	}
	if err != nil {
		return "", fmt.Errorf("invalid torrent: %w", err)
	}

	cur := e.Settings()
	if opts.Category != "" && !cur.HasCategory(opts.Category) {
		return "", fmt.Errorf("unknown category %q", opts.Category)
	}
	savePath := cur.DownloadDir
	if p := cur.CategoryPath(opts.Category); p != "" {
		savePath = p
	}
	if opts.SavePath != "" {
		p, err := fsutil.CleanAbs(opts.SavePath)
		if err != nil {
			return "", fmt.Errorf("save path: %w", err)
		}
		savePath = p
	}
	if err := os.MkdirAll(savePath, 0o755); err != nil {
		return "", fmt.Errorf("create save path: %w", err)
	}

	hash := spec.InfoHash.HexString()
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.cl == nil {
		return "", ErrClosed
	}
	if existing, ok := e.torrents[hash]; ok {
		// Merge trackers like other clients do, so re-adding is not an error for the user.
		if existing.t != nil {
			existing.t.AddTrackers(spec.Trackers)
		}
		return hash, ErrExists
	}
	if mi != nil {
		if err := config.WriteFileAtomic(e.metainfoPath(hash), opts.TorrentFile, 0o600); err != nil {
			return "", fmt.Errorf("save torrent file: %w", err)
		}
	}
	name := spec.DisplayName
	var size int64 // known up front for .torrent files, so paused torrents show it
	if mi != nil {
		if info, err := mi.UnmarshalInfo(); err == nil {
			name = info.BestName()
			size = info.TotalLength()
		}
	}
	if name == "" {
		name = hash
	}
	rec := &record{
		InfoHash:   hash,
		Name:       name,
		Size:       size,
		Magnet:     opts.Magnet,
		SavePath:   savePath,
		Paused:     opts.Paused,
		Sequential: opts.Sequential,
		Category:   opts.Category,
		AddedAt:    time.Now().UTC(),
	}
	for i, p := range opts.FilePriorities {
		if p != PriorityNormal {
			if rec.FilePriorities == nil {
				rec.FilePriorities = map[int]int{}
			}
			rec.FilePriorities[i] = p
		}
	}
	m := &managed{hash: spec.InfoHash, rec: rec, checkExisting: true,
		baseDown: max(opts.Downloaded, 0), baseUp: max(opts.Uploaded, 0)}
	e.torrents[hash] = m
	e.order = append(e.order, hash)
	if !opts.Paused && e.slotForNewLocked(m) {
		if err := e.startLocked(m); err != nil {
			delete(e.torrents, hash)
			e.order = e.order[:len(e.order)-1]
			return "", err
		}
	}
	e.dirty = true
	e.publish(Event{Type: "added", Hash: hash, Name: name})
	return hash, nil
}

func parseBareHash(s string) (string, bool) {
	s = strings.ToLower(strings.TrimSpace(s))
	if len(s) != 40 {
		return "", false
	}
	for _, c := range s {
		if !strings.ContainsRune("0123456789abcdef", c) {
			return "", false
		}
	}
	return s, true
}

// startLocked adds the torrent to the live client. Callers hold e.mu.
func (e *Engine) startLocked(m *managed) error {
	var spec *torrent.TorrentSpec
	var err error
	if mi, lerr := metainfo.LoadFromFile(e.metainfoPath(m.rec.InfoHash)); lerr == nil {
		spec, err = torrent.TorrentSpecFromMetaInfoErr(mi)
	} else if m.rec.Magnet != "" {
		spec, err = torrent.TorrentSpecFromMagnetUri(m.rec.Magnet)
	} else {
		spec = &torrent.TorrentSpec{}
		spec.InfoHash = m.hash
	}
	if err != nil {
		return err
	}
	// Privacy: never fetch metadata from arbitrary HTTP sources named in a
	// magnet link; peers and trackers are enough.
	spec.Sources = nil
	spec.Storage = e.storageFor(m.rec.SavePath)
	if len(m.rec.ExtraTrackers) > 0 {
		spec.Trackers = append(spec.Trackers, m.rec.ExtraTrackers)
	}
	t, _, err := e.cl.AddTorrentSpec(spec)
	if err != nil {
		return err
	}
	if m.rec.Name != "" && m.rec.Name != m.rec.InfoHash {
		t.SetDisplayName(m.rec.Name)
	}
	m.t = t
	m.err = ""
	m.startedAt = time.Now()
	m.lastDown, m.lastUp = 0, 0
	m.seqPieces = nil
	e.wg.Add(1)
	go e.awaitInfo(m, t)
	return nil
}

// awaitInfo finishes setup once metadata is available.
func (e *Engine) awaitInfo(m *managed, t *torrent.Torrent) {
	defer e.wg.Done()
	select {
	case <-t.GotInfo():
	case <-t.Closed():
		return
	case <-e.ctx.Done():
		return
	}
	path := e.metainfoPath(m.hash.HexString())
	if _, err := os.Stat(path); errors.Is(err, os.ErrNotExist) {
		mi := t.Metainfo()
		var buf bytes.Buffer
		if err := mi.Write(&buf); err == nil {
			if err := config.WriteFileAtomic(path, buf.Bytes(), 0o600); err != nil {
				e.log.Warn("save metainfo", "err", err)
			}
		}
	}
	e.mu.Lock()
	if m.t != t {
		e.mu.Unlock()
		return // paused or removed meanwhile
	}
	m.rec.Name = t.Name()
	m.rec.Size = t.Length()
	e.applyFilePrioritiesLocked(m)
	e.dirty = true
	check := m.checkExisting && hasExistingData(m.rec.SavePath, t.Info())
	m.checkExisting = false
	if check {
		m.checking = true
		t.DisallowDataDownload()
	}
	e.mu.Unlock()

	if !check {
		return
	}
	// Hash whatever is already on disk before downloading,
	// so only missing or damaged pieces are fetched.
	e.log.Info("checking existing files", "name", logSafe(t.Name()))
	err := t.VerifyDataContext(e.ctx)
	e.mu.Lock()
	m.checking = false
	if m.t == t {
		t.AllowDataDownload()
	}
	e.mu.Unlock()
	if err != nil && !errors.Is(err, context.Canceled) {
		e.log.Warn("verify existing data", "name", logSafe(t.Name()), "err", logSafe(err.Error()))
	}
}

// hasExistingData reports whether any of the torrent's files (or their
// partial ".part" files) already exist under savePath.
func hasExistingData(savePath string, info *metainfo.Info) bool {
	if info == nil {
		return false
	}
	// File names come from torrent metadata, which is untrusted: never look
	// outside the save folder.
	exists := func(elems ...string) bool {
		p, err := fsutil.Within(savePath, elems...)
		if err != nil {
			return false
		}
		return fsutil.RegularFileExists(p) || fsutil.RegularFileExists(p+".part")
	}
	if !info.IsDir() {
		return exists(info.BestName())
	}
	for _, f := range info.UpvertedFiles() {
		if exists(append([]string{info.BestName()}, f.BestPath()...)...) {
			return true
		}
	}
	return false
}

func (e *Engine) applyFilePrioritiesLocked(m *managed) {
	if m.t == nil || m.t.Info() == nil {
		return
	}
	for i, f := range m.t.Files() {
		p, ok := m.rec.FilePriorities[i]
		if !ok {
			p = PriorityNormal
		}
		f.SetPriority(toPiecePriority(p))
	}
}

func toPiecePriority(p int) torrent.PiecePriority {
	switch p {
	case PrioritySkip:
		return torrent.PiecePriorityNone
	case PriorityHigh:
		return torrent.PiecePriorityHigh
	default:
		return torrent.PiecePriorityNormal
	}
}

func (e *Engine) get(hash string) (*managed, error) {
	m, ok := e.torrents[strings.ToLower(hash)]
	if !ok {
		return nil, ErrNotFound
	}
	return m, nil
}

// stopLocked drops the torrent from the client, keeping its record.
func (e *Engine) stopLocked(m *managed) {
	if m.t == nil {
		return
	}
	m.baseDown, m.baseUp = m.totals()
	t := m.t
	if t.Info() != nil {
		m.rec.Size = t.Length()
		m.rec.Completed = t.BytesCompleted()
	}
	m.t = nil
	m.downRate, m.upRate = 0, 0
	t.Drop()
}

// Pause stops all activity for the torrents.
func (e *Engine) Pause(hashes ...string) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	for _, h := range hashes {
		m, err := e.get(h)
		if err != nil {
			return err
		}
		e.stopLocked(m)
		m.rec.Paused = true
		m.rec.Forced = false
	}
	e.dirty = true
	return nil
}

// Resume restarts paused torrents.
func (e *Engine) Resume(hashes ...string) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.cl == nil {
		return ErrClosed
	}
	var errs []error
	for _, h := range hashes {
		m, err := e.get(h)
		if err != nil {
			return err
		}
		m.rec.Paused = false
		if m.t == nil && e.slotForNewLocked(m) {
			m.err = ""
			if err := e.startLocked(m); err != nil {
				m.err = err.Error()
				errs = append(errs, err)
				continue
			}
		}
	}
	e.dirty = true
	return errors.Join(errs...)
}

// PauseAll pauses every running torrent.
func (e *Engine) PauseAll() error {
	return e.Pause(e.hashes()...)
}

// ResumeAll resumes every paused torrent.
func (e *Engine) ResumeAll() error {
	return e.Resume(e.hashes()...)
}

func (e *Engine) hashes() []string {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return slices.Clone(e.order)
}

// Remove deletes torrents, optionally deleting their downloaded data.
func (e *Engine) Remove(deleteData bool, hashes ...string) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	var errs []error
	for _, h := range hashes {
		m, err := e.get(h)
		if err != nil {
			return err
		}
		if m.moving {
			errs = append(errs, fmt.Errorf("%q is being moved; remove it when the move has finished", m.rec.Name))
			continue
		}
		var info *metainfo.Info
		if m.t != nil && m.t.Info() != nil {
			info = m.t.Info()
		} else if mi, err := metainfo.LoadFromFile(e.metainfoPath(m.rec.InfoHash)); err == nil {
			if i, err := mi.UnmarshalInfo(); err == nil {
				info = &i
			}
		}
		e.stopLocked(m)
		delete(e.torrents, m.rec.InfoHash)
		e.order = slices.DeleteFunc(e.order, func(s string) bool { return s == m.rec.InfoHash })
		os.Remove(e.metainfoPath(m.rec.InfoHash))
		if deleteData && info != nil {
			if other := e.sharesDataLocked(m.rec.SavePath, info.BestName()); other != "" {
				errs = append(errs, fmt.Errorf("kept files of %q: they are also used by %q", m.rec.Name, other))
			} else if err := deleteTorrentData(m.rec.SavePath, info); err != nil {
				errs = append(errs, err)
			}
		}
		e.publish(Event{Type: "removed", Hash: m.rec.InfoHash, Name: m.rec.Name})
	}
	e.dirty = true
	return errors.Join(errs...)
}

// sharesDataLocked returns the name of another torrent stored at the same
// path, so removing one torrent never deletes files another one still uses.
func (e *Engine) sharesDataLocked(savePath, root string) string {
	for _, o := range e.torrents {
		if filepath.Clean(o.rec.SavePath) != filepath.Clean(savePath) {
			continue
		}
		name := o.rec.Name
		if o.t != nil && o.t.Info() != nil {
			name = o.t.Info().BestName()
		} else if mi, err := metainfo.LoadFromFile(e.metainfoPath(o.rec.InfoHash)); err == nil {
			if i, err := mi.UnmarshalInfo(); err == nil {
				name = i.BestName()
			}
		}
		if name == root {
			return o.rec.Name
		}
	}
	return ""
}

// deleteTorrentData removes a torrent's files, refusing anything outside savePath.
func deleteTorrentData(savePath string, info *metainfo.Info) error {
	name := info.BestName()
	if name == "" || name == "." || name == ".." || strings.ContainsAny(name, `/\`) {
		return fmt.Errorf("refusing to delete data for unsafe torrent name %q", name)
	}
	target, err := fsutil.Within(savePath, name)
	if err != nil || target == filepath.Clean(savePath) {
		return fmt.Errorf("refusing to delete data outside %q", savePath)
	}
	// Do not follow a symlink planted at the torrent root.
	if fi, err := os.Lstat(target); err == nil && fi.Mode()&os.ModeSymlink != 0 {
		return os.Remove(target)
	}
	if info.IsDir() {
		return os.RemoveAll(target)
	}
	err1 := os.Remove(target)
	err2 := os.Remove(target + ".part")
	if errors.Is(err1, os.ErrNotExist) && errors.Is(err2, os.ErrNotExist) {
		return nil
	}
	if err1 != nil && !errors.Is(err1, os.ErrNotExist) {
		return err1
	}
	return nil
}

// Recheck re-verifies all pieces on disk.
func (e *Engine) Recheck(hash string) error {
	e.mu.RLock()
	m, err := e.get(hash)
	var t *torrent.Torrent
	if err == nil {
		t = m.t
	}
	e.mu.RUnlock()
	if err != nil {
		return err
	}
	if t == nil || t.Info() == nil {
		return errors.New("torrent must be running with metadata to recheck")
	}
	e.wg.Add(1)
	go func() {
		defer e.wg.Done()
		if err := t.VerifyDataContext(e.ctx); err != nil && !errors.Is(err, context.Canceled) {
			e.log.Warn("recheck", "err", err)
		}
	}()
	return nil
}

// SetSequential toggles in-order downloading (useful for streaming media).
func (e *Engine) SetSequential(hash string, on bool) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	m, err := e.get(hash)
	if err != nil {
		return err
	}
	m.rec.Sequential = on
	if !on {
		e.clearSequentialLocked(m)
	}
	e.dirty = true
	return nil
}

func (e *Engine) clearSequentialLocked(m *managed) {
	if m.t == nil {
		m.seqPieces = nil
		return
	}
	for i := range m.seqPieces {
		m.t.Piece(i).SetPriority(torrent.PiecePriorityNone)
	}
	m.seqPieces = nil
}

// SetFilePriority sets priorities for file indices of a torrent.
func (e *Engine) SetFilePriority(hash string, indices []int, prio int) error {
	if prio < PrioritySkip || prio > PriorityHigh {
		return errors.New("invalid priority")
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	m, err := e.get(hash)
	if err != nil {
		return err
	}
	if m.rec.FilePriorities == nil {
		m.rec.FilePriorities = map[int]int{}
	}
	for _, i := range indices {
		if i < 0 || i > 1<<20 {
			return errors.New("invalid file index")
		}
		if prio == PriorityNormal {
			delete(m.rec.FilePriorities, i)
		} else {
			m.rec.FilePriorities[i] = prio
		}
	}
	e.applyFilePrioritiesLocked(m)
	e.dirty = true
	return nil
}

// AddTrackers adds tracker URLs to a torrent.
func (e *Engine) AddTrackers(hash string, urls []string) error {
	var clean []string
	for _, u := range urls {
		u = strings.TrimSpace(u)
		if u == "" {
			continue
		}
		lu := strings.ToLower(u)
		if !strings.HasPrefix(lu, "udp://") && !strings.HasPrefix(lu, "http://") &&
			!strings.HasPrefix(lu, "https://") && !strings.HasPrefix(lu, "wss://") {
			return fmt.Errorf("unsupported tracker URL %q", u)
		}
		clean = append(clean, u)
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	m, err := e.get(hash)
	if err != nil {
		return err
	}
	for _, u := range clean {
		if !slices.Contains(m.rec.ExtraTrackers, u) {
			m.rec.ExtraTrackers = append(m.rec.ExtraTrackers, u)
		}
	}
	if m.t != nil {
		m.t.AddTrackers([][]string{clean})
	}
	e.dirty = true
	return nil
}

// Settings returns the active settings.
func (e *Engine) Settings() config.Settings {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return e.settings
}

// ApplySettings applies new settings, restarting the network stack if needed.
func (e *Engine) ApplySettings(s config.Settings) error {
	dir, err := fsutil.CleanAbs(s.DownloadDir)
	if err != nil {
		return fmt.Errorf("download directory: %w", err)
	}
	s.DownloadDir = dir
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("create download dir: %w", err)
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	old := e.settings
	e.applyLimits(s)
	e.settings = s
	if !needsRestart(old, s) {
		if old.MaxConnsPerTorrent != s.MaxConnsPerTorrent {
			for _, m := range e.torrents {
				if m.t != nil {
					m.t.SetMaxEstablishedConns(s.MaxConnsPerTorrent)
				}
			}
		}
		return nil
	}
	if err := e.restartLocked(); err != nil {
		// Roll back so transfers keep running with the previous settings.
		e.settings = old
		e.applyLimits(old)
		if e.cl == nil {
			if rerr := e.restartLocked(); rerr != nil {
				e.log.Error("could not restore previous network settings", "err", rerr)
			}
		}
		return err
	}
	return nil
}

func needsRestart(a, b config.Settings) bool {
	return a.DownloadDir != b.DownloadDir || a.ListenPort != b.ListenPort ||
		a.BindAddress != b.BindAddress || a.BindInterface != b.BindInterface ||
		a.ProxyType != b.ProxyType || a.ProxyHost != b.ProxyHost || a.ProxyPort != b.ProxyPort ||
		a.ProxyUsername != b.ProxyUsername || a.ProxyPassword != b.ProxyPassword || a.ProxyPeers != b.ProxyPeers ||
		a.EnableDHT != b.EnableDHT ||
		a.EnablePEX != b.EnablePEX || a.EnableLSD != b.EnableLSD ||
		a.EnableUTP != b.EnableUTP || a.EnableUPnP != b.EnableUPnP ||
		a.DisableIPv6 != b.DisableIPv6 || a.Encryption != b.Encryption ||
		a.AnonymousMode != b.AnonymousMode || a.MaxHalfOpenConns != b.MaxHalfOpenConns ||
		a.MaxConnsPerTorrent != b.MaxConnsPerTorrent || a.PieceHashers != b.PieceHashers ||
		a.SeedAfterComplete != b.SeedAfterComplete || a.BlocklistPath != b.BlocklistPath
}

func (e *Engine) restartLocked() error {
	var running []*managed
	for _, h := range e.order {
		m := e.torrents[h]
		if m.t != nil {
			running = append(running, m)
			e.stopLocked(m)
		}
	}
	if e.cl != nil {
		old := e.cl
		old.Close()
		select {
		case <-old.Closed():
		case <-time.After(5 * time.Second):
		}
		e.cl = nil
	}
	cl, err := e.newClientRetry(e.settings)
	if err != nil {
		return err
	}
	e.cl = cl
	var errs []error
	for _, m := range running {
		if err := e.startLocked(m); err != nil {
			m.err = err.Error()
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

// loop samples rates, enforces seeding rules and persists state.
func (e *Engine) loop() {
	defer e.wg.Done()
	tick := time.NewTicker(time.Second)
	defer tick.Stop()
	saveEvery := 0
	for {
		select {
		case <-e.ctx.Done():
			return
		case <-tick.C:
		}
		e.mu.Lock()
		e.tickLocked()
		e.queueLocked(time.Now())
		e.checkInterfaceLocked(time.Now())
		e.expirePreviewsLocked()
		saveEvery++
		if e.dirty || saveEvery >= 30 {
			if err := e.saveStateLocked(); err != nil {
				e.log.Warn("save session", "err", err)
			}
			if err := e.saveStatsLocked(); err != nil {
				e.log.Warn("save statistics", "err", err)
			}
			e.dirty = false
			saveEvery = 0
		}
		e.mu.Unlock()
	}
}

const rateSmoothing = 0.4

// SetSettingsHook registers fn to persist settings the engine changes itself.
func (e *Engine) SetSettingsHook(fn func(config.Settings)) {
	e.mu.Lock()
	e.onSettingsChanged = fn
	e.mu.Unlock()
}

// applyScheduleLocked turns alternative limits on or off when the schedule
// crosses a boundary. Manual toggles stay until the next boundary.
func (e *Engine) applyScheduleLocked(now time.Time) {
	if !e.settings.AltScheduleEnabled {
		e.schedKnown = false
		return
	}
	in := e.settings.InAltSchedule(now)
	if e.schedKnown && in == e.schedLast {
		return
	}
	e.schedKnown, e.schedLast = true, in
	if e.settings.AltSpeedEnabled == in {
		return
	}
	e.settings.AltSpeedEnabled = in
	e.applyLimits(e.settings)
	e.log.Info("speed schedule", "alternative", in)
	if e.onSettingsChanged != nil {
		e.onSettingsChanged(e.settings)
	}
}

func (e *Engine) tickLocked() {
	e.applyScheduleLocked(time.Now())
	var gDown, gUp float64
	var tickDown, tickUp int64
	for _, h := range e.order {
		m := e.torrents[h]
		if m.t == nil {
			continue
		}
		d, u := m.liveStats()
		m.downRate = smooth(m.downRate, float64(d-m.lastDown))
		m.upRate = smooth(m.upRate, float64(u-m.lastUp))
		tickDown += d - m.lastDown
		tickUp += u - m.lastUp
		m.lastDown, m.lastUp = d, u
		gDown += m.downRate
		if e.activeSince.IsZero() && m.t.Stats().ActivePeers > 0 {
			e.activeSince = time.Now()
		}
		gUp += m.upRate

		info := m.t.Info()
		if info == nil {
			continue
		}
		complete := m.t.BytesMissing() == 0
		if complete && m.rec.CompletedAt == nil {
			now := time.Now().UTC()
			m.rec.CompletedAt = &now
			e.dirty = true
			e.publish(Event{Type: "completed", Hash: h, Name: m.rec.Name})
			if dest := e.completedDestLocked(m); dest != "" {
				go func(h, dest string) {
					if err := e.Move(dest, h); err != nil {
						e.log.Warn("move completed download", "err", err)
					}
				}(h, dest)
			}
		}
		if !complete && m.rec.Sequential {
			e.sequentialLocked(m)
		}
		if complete && e.settings.SeedTimeLimitMin > 0 && m.rec.CompletedAt != nil &&
			time.Since(*m.rec.CompletedAt) > time.Duration(e.settings.SeedTimeLimitMin)*time.Minute {
			e.stopLocked(m)
			m.rec.Paused = true
			e.dirty = true
			continue
		}
		if complete && e.settings.SeedRatioLimit > 0 {
			down, up := m.totals()
			if ratio(down, up, info.TotalLength()) >= e.settings.SeedRatioLimit {
				e.stopLocked(m)
				m.rec.Paused = true
				e.dirty = true
			}
		}
	}
	e.downRate, e.upRate = gDown, gUp
	e.recordTransferLocked(time.Now(), tickDown, tickUp)
}

func smooth(prev, sample float64) float64 {
	if sample < 0 {
		sample = 0
	}
	v := prev + rateSmoothing*(sample-prev)
	if v < 1 {
		return 0
	}
	return v
}

func ratio(down, up, size int64) float64 {
	base := max(down, 0)
	if base == 0 {
		base = size
	}
	if base == 0 {
		return 0
	}
	return float64(up) / float64(base)
}

const sequentialWindow = 24

// sequentialLocked raises the priority of the next pieces in file order.
func (e *Engine) sequentialLocked(m *managed) {
	t := m.t
	want := map[int]struct{}{}
	for i, f := range t.Files() {
		if p, ok := m.rec.FilePriorities[i]; ok && p == PrioritySkip {
			continue
		}
		for p := f.BeginPieceIndex(); p < f.EndPieceIndex() && len(want) < sequentialWindow; p++ {
			if _, dup := want[p]; dup {
				continue
			}
			if !t.PieceState(p).Complete {
				want[p] = struct{}{}
			}
		}
		if len(want) >= sequentialWindow {
			break
		}
	}
	for p := range m.seqPieces {
		if _, ok := want[p]; !ok {
			t.Piece(p).SetPriority(torrent.PiecePriorityNone)
		}
	}
	for p := range want {
		if _, ok := m.seqPieces[p]; !ok {
			t.Piece(p).SetPriority(torrent.PiecePriorityReadahead)
		}
	}
	m.seqPieces = want
}

// completedDestLocked is where a finished torrent should be moved, if anywhere.
func (e *Engine) completedDestLocked(m *managed) string {
	if !e.settings.MoveCompletedEnabled || e.settings.MoveCompletedDir == "" {
		return ""
	}
	dest := e.settings.MoveCompletedDir
	if filepath.Clean(m.rec.SavePath) == filepath.Clean(dest) {
		return ""
	}
	return dest
}

// Subscribe returns a channel of events; call the returned func to unsubscribe.
func (e *Engine) Subscribe() (<-chan Event, func()) {
	ch := make(chan Event, 32)
	e.subsMu.Lock()
	e.subs[ch] = struct{}{}
	e.subsMu.Unlock()
	return ch, func() {
		e.subsMu.Lock()
		if _, ok := e.subs[ch]; ok {
			delete(e.subs, ch)
			close(ch)
		}
		e.subsMu.Unlock()
	}
}

func (e *Engine) publish(ev Event) {
	e.subsMu.Lock()
	defer e.subsMu.Unlock()
	for ch := range e.subs {
		select {
		case ch <- ev:
		default: // slow subscriber; drop
		}
	}
}

// Broadcast sends an event to all connected windows (e.g. UI commands).
func (e *Engine) Broadcast(ev Event) { e.publish(ev) }

// AnnounceShutdown tells connected windows that Magnetar is quitting so they
// can close themselves.
func (e *Engine) AnnounceShutdown() {
	e.publish(Event{Type: "shutdown"})
}

// Close saves state and shuts down the client.
func (e *Engine) Close() error {
	e.cancel()
	e.mu.Lock()
	err := e.saveStateLocked()
	if serr := e.saveStatsLocked(); serr != nil {
		e.log.Warn("save statistics", "err", serr)
	}
	if e.cl != nil {
		e.cl.Close()
		e.cl = nil
	}
	e.mu.Unlock()
	e.wg.Wait()
	for _, st := range e.storages {
		st.Close()
	}
	e.completion.Close()
	return err
}

// FormatRatio is shared with the API layer.
func FormatRatio(r float64) float64 {
	if math.IsInf(r, 0) || math.IsNaN(r) {
		return 0
	}
	return math.Round(r*1000) / 1000
}

// WriteStatus dumps the BitTorrent client's internal state for diagnostics.
func (e *Engine) WriteStatus(w io.Writer) {
	e.mu.RLock()
	cl := e.cl
	e.mu.RUnlock()
	if cl != nil {
		cl.WriteStatus(w)
	}
}

// logSafe removes line breaks and other control characters from untrusted
// text (torrent names come from metadata) before it is written to the log.
func logSafe(s string) string { return logsafe.String(s) }
