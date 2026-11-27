package engine

import (
	"bytes"
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/anacrolix/torrent/bencode"
	"github.com/anacrolix/torrent/metainfo"

	"github.com/nikolap994/magnetar/internal/fsutil"
)

// CreateOptions describe a new torrent.
type CreateOptions struct {
	Path        string   `json:"path"`        // file or folder to share
	Trackers    []string `json:"trackers"`    // announce URLs, one tier each
	WebSeeds    []string `json:"webSeeds"`    // optional HTTP sources
	PieceLength int64    `json:"pieceLength"` // 0 = automatic
	Private     bool     `json:"private"`
	Comment     string   `json:"comment"`
	Source      string   `json:"source"` // optional "source" tag some private trackers require
	StartSeed   bool     `json:"startSeeding"`
}

// CreateJob reports the progress of building a torrent.
type CreateJob struct {
	ID       string  `json:"id"`
	Name     string  `json:"name"`
	State    string  `json:"state"` // hashing, done, error
	Progress float64 `json:"progress"`
	Size     int64   `json:"size"`
	Files    int     `json:"files"`
	Error    string  `json:"error,omitempty"`
	Hash     string  `json:"hash,omitempty"`
	Magnet   string  `json:"magnet,omitempty"`
	Seeding  bool    `json:"seeding,omitempty"`

	data   []byte
	done   atomic.Int64
	cancel context.CancelFunc
}

type createJobs struct {
	mu   sync.Mutex
	jobs map[string]*CreateJob
}

const maxCreateFiles = 100000

// validTrackerURL accepts http(s), udp and wss announce URLs.
func validTrackerURL(s string) bool {
	u, err := url.Parse(s)
	if err != nil || u.Host == "" || len(s) > 2000 {
		return false
	}
	switch u.Scheme {
	case "http", "https", "udp", "ws", "wss":
		return true
	}
	return false
}

// CreateTorrent starts hashing in the background and returns the job.
func (e *Engine) CreateTorrent(opts CreateOptions) (*CreateJob, error) {
	root, err := fsutil.CleanAbs(opts.Path)
	if err != nil {
		return nil, err
	}
	st, err := os.Lstat(root)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", root, errors.Unwrap(err))
	}
	if st.Mode()&os.ModeSymlink != 0 || (!st.IsDir() && !st.Mode().IsRegular()) {
		return nil, errors.New("choose a regular file or folder")
	}
	var trackers []string
	for _, t := range opts.Trackers {
		if t = strings.TrimSpace(t); t == "" {
			continue
		}
		if !validTrackerURL(t) {
			return nil, fmt.Errorf("not a tracker address: %q", t)
		}
		trackers = append(trackers, t)
	}
	var seeds []string
	for _, s := range opts.WebSeeds {
		if s = strings.TrimSpace(s); s == "" {
			continue
		}
		if u, err := url.Parse(s); err != nil || (u.Scheme != "http" && u.Scheme != "https") {
			return nil, fmt.Errorf("web seeds must be http(s) addresses: %q", s)
		}
		seeds = append(seeds, s)
	}
	if opts.PieceLength != 0 && (opts.PieceLength < 16<<10 || opts.PieceLength > 64<<20 || opts.PieceLength&(opts.PieceLength-1) != 0) {
		return nil, errors.New("piece size must be a power of two between 16 KiB and 64 MiB")
	}
	if len(opts.Comment) > 2000 || len(opts.Source) > 200 {
		return nil, errors.New("comment or source is too long")
	}

	info := metainfo.Info{Name: filepath.Base(root)}
	var total int64
	if st.IsDir() {
		err = filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			// Symlinks are skipped so nothing outside the folder is shared.
			if d.Type()&fs.ModeSymlink != 0 || d.IsDir() {
				return nil
			}
			if !d.Type().IsRegular() {
				return nil
			}
			fi, err := d.Info()
			if err != nil {
				return err
			}
			rel, err := filepath.Rel(root, p)
			if err != nil {
				return err
			}
			if len(info.Files) >= maxCreateFiles {
				return fmt.Errorf("more than %d files", maxCreateFiles)
			}
			info.Files = append(info.Files, metainfo.FileInfo{Path: strings.Split(rel, string(filepath.Separator)), Length: fi.Size()})
			total += fi.Size()
			return nil
		})
		if err != nil {
			return nil, err
		}
		if len(info.Files) == 0 {
			return nil, errors.New("the folder has no files")
		}
		sort.Slice(info.Files, func(i, j int) bool {
			return strings.Join(info.Files[i].Path, "/") < strings.Join(info.Files[j].Path, "/")
		})
	} else {
		info.Length = st.Size()
		total = st.Size()
	}
	if total == 0 {
		return nil, errors.New("there is nothing to share (all files are empty)")
	}
	info.PieceLength = opts.PieceLength
	if info.PieceLength == 0 {
		info.PieceLength = autoPieceLength(total)
	}
	if opts.Private {
		t := true
		info.Private = &t
	}
	info.Source = strings.TrimSpace(opts.Source)

	ctx, cancel := context.WithCancel(e.ctx)
	job := &CreateJob{ID: newJobID(), Name: info.Name, State: "hashing", Size: total, Files: max(len(info.Files), 1), cancel: cancel}
	e.creates.mu.Lock()
	if e.creates.jobs == nil {
		e.creates.jobs = map[string]*CreateJob{}
	}
	e.creates.jobs[job.ID] = job
	e.creates.mu.Unlock()

	e.wg.Add(1)
	go func() {
		defer e.wg.Done()
		defer cancel()
		err := info.GeneratePieces(func(fi metainfo.FileInfo) (io.ReadCloser, error) {
			p := root
			if len(info.Files) > 0 {
				p = filepath.Join(append([]string{root}, fi.Path...)...)
			}
			f, err := os.Open(p)
			if err != nil {
				return nil, err
			}
			return &countingReader{ctx: ctx, f: f, n: &job.done}, nil
		})
		var data []byte
		var mi metainfo.MetaInfo
		if err == nil {
			mi = metainfo.MetaInfo{CreatedBy: "Magnetar " + Version, CreationDate: time.Now().Unix(), Comment: opts.Comment, UrlList: seeds}
			if len(trackers) > 0 {
				mi.Announce = trackers[0]
				for _, t := range trackers {
					mi.AnnounceList = append(mi.AnnounceList, []string{t})
				}
			}
			if mi.InfoBytes, err = bencode.Marshal(info); err == nil {
				var buf bytes.Buffer
				if err = mi.Write(&buf); err == nil {
					data = buf.Bytes()
				}
			}
		}
		e.creates.mu.Lock()
		if err != nil {
			job.State, job.Error = "error", err.Error()
			if ctx.Err() != nil {
				job.Error = "cancelled"
			}
			e.creates.mu.Unlock()
			return
		}
		job.data = data
		job.Hash = mi.HashInfoBytes().HexString()
		if mag, err := mi.MagnetV2(); err == nil {
			job.Magnet = mag.String()
		}
		job.State, job.Progress = "done", 1
		e.creates.mu.Unlock()

		if opts.StartSeed {
			_, err := e.Add(AddOptions{TorrentFile: data, SavePath: filepath.Dir(root)})
			e.creates.mu.Lock()
			if err != nil && !errors.Is(err, ErrExists) {
				job.Error = "created, but could not start seeding: " + err.Error()
			} else {
				job.Seeding = true
			}
			e.creates.mu.Unlock()
		}
	}()
	return job.snapshot(), nil
}

// CreateStatus returns the progress of a job.
func (e *Engine) CreateStatus(id string) (*CreateJob, []byte, error) {
	e.creates.mu.Lock()
	defer e.creates.mu.Unlock()
	j, ok := e.creates.jobs[id]
	if !ok {
		return nil, nil, ErrNotFound
	}
	if j.State == "hashing" && j.Size > 0 {
		j.Progress = min(float64(j.done.Load())/float64(j.Size), 0.999)
	}
	return j.snapshot(), j.data, nil
}

// CancelCreate stops a running job and forgets it.
func (e *Engine) CancelCreate(id string) {
	e.creates.mu.Lock()
	defer e.creates.mu.Unlock()
	if j, ok := e.creates.jobs[id]; ok {
		j.cancel()
		delete(e.creates.jobs, id)
	}
}

func (j *CreateJob) snapshot() *CreateJob {
	c := &CreateJob{ID: j.ID, Name: j.Name, State: j.State, Progress: j.Progress, Size: j.Size, Files: j.Files,
		Error: j.Error, Hash: j.Hash, Magnet: j.Magnet, Seeding: j.Seeding}
	return c
}

// autoPieceLength aims for roughly 1000-2000 pieces, between 16 KiB and 16 MiB.
func autoPieceLength(total int64) int64 {
	pl := int64(16 << 10)
	for pl < 16<<20 && total/pl > 2000 {
		pl *= 2
	}
	return pl
}

type countingReader struct {
	ctx context.Context
	f   *os.File
	n   *atomic.Int64
}

func (r *countingReader) Read(p []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	n, err := r.f.Read(p)
	r.n.Add(int64(n))
	return n, err
}

func (r *countingReader) Close() error { return r.f.Close() }

func newJobID() string {
	var b [8]byte
	_, _ = rand.Read(b[:])
	return fmt.Sprintf("%x", b)
}
