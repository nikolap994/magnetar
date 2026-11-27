package engine

import (
	"bytes"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/anacrolix/torrent"
	"github.com/anacrolix/torrent/metainfo"
)

// A preview fetches a torrent's metadata so the user can pick files and a
// folder before anything is downloaded. Storage is never opened for it.

const previewTTL = 20 * time.Minute

type preview struct {
	hash     string
	name     string
	magnet   string
	t        *torrent.Torrent
	metainfo []byte // set once metadata is known
	created  time.Time
	err      string
}

// PreviewFile is one file in a preview.
type PreviewFile struct {
	Index int    `json:"index"`
	Path  string `json:"path"`
	Size  int64  `json:"size"`
}

// PreviewInfo is reported to the add dialog.
type PreviewInfo struct {
	Hash    string        `json:"hash"`
	Name    string        `json:"name"`
	Ready   bool          `json:"ready"`
	Size    int64         `json:"size"`
	Files   []PreviewFile `json:"files"`
	Peers   int           `json:"peers"`
	Error   string        `json:"error,omitempty"`
	Private bool          `json:"private"`
}

// Preview starts fetching metadata for a magnet link, or reads it directly
// from a .torrent file. It returns ErrExists (with the hash) for torrents
// that are already added.
func (e *Engine) Preview(magnet string, torrentFile []byte) (PreviewInfo, error) {
	if len(torrentFile) > 0 {
		mi, err := metainfo.Load(bytes.NewReader(torrentFile))
		if err != nil {
			return PreviewInfo{}, fmt.Errorf("invalid torrent file: %w", err)
		}
		if _, err := mi.UnmarshalInfo(); err != nil {
			return PreviewInfo{}, fmt.Errorf("invalid torrent info: %w", err)
		}
		hash := mi.HashInfoBytes().HexString()
		e.mu.Lock()
		defer e.mu.Unlock()
		if _, ok := e.torrents[hash]; ok {
			return PreviewInfo{Hash: hash}, ErrExists
		}
		p := &preview{hash: hash, metainfo: torrentFile, created: time.Now()}
		e.dropPreviewLocked(hash)
		e.previews[hash] = p
		return previewInfo(p), nil
	}

	magnet = strings.TrimSpace(magnet)
	if !strings.HasPrefix(strings.ToLower(magnet), "magnet:?") {
		h, ok := parseBareHash(magnet)
		if !ok {
			return PreviewInfo{}, errors.New("not a magnet link or info hash")
		}
		magnet = "magnet:?xt=urn:btih:" + h
	}
	spec, err := torrent.TorrentSpecFromMagnetUri(magnet)
	if err != nil {
		return PreviewInfo{}, fmt.Errorf("invalid magnet link: %w", err)
	}
	hash := spec.InfoHash.HexString()

	e.mu.Lock()
	defer e.mu.Unlock()
	if e.cl == nil {
		return PreviewInfo{}, ErrClosed
	}
	if _, ok := e.torrents[hash]; ok {
		return PreviewInfo{Hash: hash}, ErrExists
	}
	if p, ok := e.previews[hash]; ok {
		return previewInfo(p), nil
	}
	spec.Sources = nil
	// Add fallback trackers so a magnet whose own trackers are dead still finds
	// peers (and its metadata) here, instead of waiting on DHT alone.
	if tier := defaultTrackers(e.settings); len(tier) > 0 {
		spec.Trackers = append(spec.Trackers, tier)
	}
	spec.DeferStorageOpen = true // metadata only: never touch the disk
	spec.Storage = e.storageFor(e.settings.DownloadDir)
	t, _, err := e.cl.AddTorrentSpec(spec)
	if err != nil {
		return PreviewInfo{}, err
	}
	t.DisallowDataDownload()
	t.DisallowDataUpload()
	p := &preview{hash: hash, name: spec.DisplayName, magnet: magnet, t: t, created: time.Now()}
	e.previews[hash] = p
	e.wg.Add(1)
	go e.awaitPreview(p)
	return previewInfo(p), nil
}

func (e *Engine) awaitPreview(p *preview) {
	defer e.wg.Done()
	t := p.t
	select {
	case <-t.GotInfo():
	case <-t.Closed():
		return
	case <-e.ctx.Done():
		return
	}
	mi := t.Metainfo()
	var buf bytes.Buffer
	err := mi.Write(&buf)
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.previews[p.hash] != p {
		return
	}
	if err != nil {
		p.err = err.Error()
		return
	}
	p.metainfo = buf.Bytes()
	// Metadata is all we need; leave the swarm until the user confirms.
	p.t = nil
	t.Drop()
}

func previewInfo(p *preview) PreviewInfo {
	out := PreviewInfo{Hash: p.hash, Name: p.name, Error: p.err, Files: []PreviewFile{}}
	if p.t != nil {
		out.Peers = p.t.Stats().TotalPeers
	}
	if p.metainfo == nil {
		return out
	}
	mi, err := metainfo.Load(bytes.NewReader(p.metainfo))
	if err != nil {
		out.Error = err.Error()
		return out
	}
	info, err := mi.UnmarshalInfo()
	if err != nil {
		out.Error = err.Error()
		return out
	}
	out.Ready = true
	out.Name = info.BestName()
	out.Size = info.TotalLength()
	out.Private = info.Private != nil && *info.Private
	for i, f := range info.UpvertedFiles() {
		path := info.BestName()
		if info.IsDir() {
			path = strings.Join(f.BestPath(), "/")
		}
		out.Files = append(out.Files, PreviewFile{Index: i, Path: path, Size: f.Length})
	}
	return out
}

// PreviewStatus reports the progress of a preview.
func (e *Engine) PreviewStatus(hash string) (PreviewInfo, error) {
	e.mu.RLock()
	defer e.mu.RUnlock()
	p, ok := e.previews[strings.ToLower(hash)]
	if !ok {
		return PreviewInfo{}, ErrNotFound
	}
	return previewInfo(p), nil
}

// CancelPreview stops fetching metadata and forgets the preview.
func (e *Engine) CancelPreview(hash string) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.dropPreviewLocked(strings.ToLower(hash))
}

func (e *Engine) dropPreviewLocked(hash string) {
	if p, ok := e.previews[hash]; ok {
		if p.t != nil {
			p.t.Drop()
		}
		delete(e.previews, hash)
	}
}

// takePreviewLocked returns the fetched metainfo and magnet for a confirmed preview.
func (e *Engine) takePreviewLocked(hash string) ([]byte, string, error) {
	p, ok := e.previews[strings.ToLower(hash)]
	if !ok {
		return nil, "", errors.New("preview expired; add the torrent again")
	}
	if p.metainfo == nil {
		return nil, p.magnet, nil // not ready yet: add from the magnet as usual
	}
	delete(e.previews, p.hash)
	return p.metainfo, p.magnet, nil
}

func (e *Engine) expirePreviewsLocked() {
	for h, p := range e.previews {
		if time.Since(p.created) > previewTTL {
			e.dropPreviewLocked(h)
		}
	}
}
