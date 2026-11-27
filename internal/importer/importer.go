// Package importer reads the torrents of other BitTorrent clients
// (qBittorrent, Transmission, uTorrent/BitTorrent) so they can be moved to
// Magnetar with their folders, file choices and statistics.
package importer

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"

	"github.com/anacrolix/torrent/bencode"
	"github.com/anacrolix/torrent/metainfo"
)

// Clients that can be imported.
const (
	QBittorrent  = "qbittorrent"
	Transmission = "transmission"
	UTorrent     = "utorrent"
)

// Names are shown in the UI.
var Names = map[string]string{QBittorrent: "qBittorrent", Transmission: "Transmission", UTorrent: "uTorrent / BitTorrent"}

const (
	maxTorrents    = 20000
	maxTorrentSize = 50 << 20
	maxResumeSize  = 200 << 20 // uTorrent keeps every torrent in one resume.dat
)

// Source is a client installation found on this computer.
type Source struct {
	Client string `json:"client"`
	Name   string `json:"name"`
	Dir    string `json:"dir"`
	Count  int    `json:"count"`
}

// Torrent is one torrent read from another client.
type Torrent struct {
	Name           string      `json:"name"`
	Hash           string      `json:"hash"`
	SavePath       string      `json:"savePath"`
	Paused         bool        `json:"paused"`
	Category       string      `json:"category,omitempty"`
	Size           int64       `json:"size"`
	Downloaded     int64       `json:"downloaded"`
	Uploaded       int64       `json:"uploaded"`
	FilePriorities map[int]int `json:"-"` // 0 skip, 1 normal, 2 high
	TorrentFile    []byte      `json:"-"`
}

// Detect looks for the default data folders of supported clients.
func Detect() []Source {
	out := []Source{}
	for _, c := range []string{QBittorrent, Transmission, UTorrent} {
		for _, dir := range defaultDirs(c) {
			if ts, err := Load(c, dir); err == nil && len(ts) > 0 {
				out = append(out, Source{Client: c, Name: Names[c], Dir: dir, Count: len(ts)})
				break
			}
		}
	}
	return out
}

func defaultDirs(client string) []string {
	home, _ := os.UserHomeDir()
	local := os.Getenv("LOCALAPPDATA")
	roaming := os.Getenv("APPDATA")
	cfg, _ := os.UserConfigDir()
	var dirs []string
	switch client {
	case QBittorrent:
		if runtime.GOOS == "windows" {
			dirs = append(dirs, filepath.Join(local, "qBittorrent", "BT_backup"))
		} else {
			dirs = append(dirs,
				filepath.Join(home, ".local", "share", "qBittorrent", "BT_backup"),
				filepath.Join(home, ".local", "share", "data", "qBittorrent", "BT_backup"),
				filepath.Join(home, ".var", "app", "org.qbittorrent.qBittorrent", "data", "qBittorrent", "BT_backup"),
				filepath.Join(home, "Library", "Application Support", "qBittorrent", "BT_backup"))
		}
	case Transmission:
		if runtime.GOOS == "windows" {
			dirs = append(dirs, filepath.Join(local, "transmission"), filepath.Join(roaming, "transmission"))
		} else {
			dirs = append(dirs,
				filepath.Join(cfg, "transmission"),
				filepath.Join(cfg, "transmission-daemon"),
				filepath.Join(home, ".var", "app", "com.transmissionbt.Transmission", "config", "transmission"),
				filepath.Join(home, "Library", "Application Support", "Transmission"))
		}
	case UTorrent:
		if runtime.GOOS == "windows" {
			dirs = append(dirs, filepath.Join(roaming, "uTorrent"), filepath.Join(roaming, "BitTorrent"))
		}
	}
	return dirs
}

// Load reads all torrents of a client from its data folder.
func Load(client, dir string) ([]Torrent, error) {
	if fi, err := os.Stat(dir); err != nil || !fi.IsDir() {
		return nil, fmt.Errorf("folder not found: %s", dir)
	}
	switch client {
	case QBittorrent:
		return loadQBittorrent(dir)
	case Transmission:
		return loadTransmission(dir)
	case UTorrent:
		return loadUTorrent(dir)
	}
	return nil, fmt.Errorf("unknown client %q", client)
}

func readLimited(path string, limit int64) ([]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	b, err := io.ReadAll(io.LimitReader(f, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(b)) > limit {
		return nil, errors.New("file is too large")
	}
	return b, nil
}

// fill sets name, hash and size from the .torrent file.
func (t *Torrent) fill() error {
	mi, err := metainfo.Load(bytes.NewReader(t.TorrentFile))
	if err != nil {
		return err
	}
	info, err := mi.UnmarshalInfo()
	if err != nil {
		return err
	}
	t.Hash = mi.HashInfoBytes().HexString()
	t.Name = info.BestName()
	t.Size = info.TotalLength()
	return nil
}

// --- qBittorrent: BT_backup/<hash>.torrent + <hash>.fastresume (libtorrent) ---

type qbResume struct {
	SavePath     string `bencode:"save_path"`
	QBSavePath   string `bencode:"qBt-savePath"`
	Paused       int    `bencode:"paused"`
	QBPaused     int    `bencode:"qBt-paused"`
	StopWhenDone int    `bencode:"stop_when_ready"`
	Category     string `bencode:"qBt-category"`
	FilePriority []int  `bencode:"file_priority"`
	Uploaded     int64  `bencode:"total_uploaded"`
	Downloaded   int64  `bencode:"total_downloaded"`
	// libtorrent 2 may store the info dictionary here for magnet-added torrents.
	Info bencode.Bytes `bencode:"info"`
}

func loadQBittorrent(dir string) ([]Torrent, error) {
	resumes, _ := filepath.Glob(filepath.Join(dir, "*.fastresume"))
	var out []Torrent
	for _, rp := range resumes {
		if len(out) >= maxTorrents {
			break
		}
		base := strings.TrimSuffix(rp, ".fastresume")
		rb, err := readLimited(rp, maxTorrentSize)
		if err != nil {
			continue
		}
		var r qbResume
		if bencode.Unmarshal(rb, &r) != nil {
			continue
		}
		t := Torrent{Category: strings.TrimSpace(r.Category), Downloaded: r.Downloaded, Uploaded: r.Uploaded,
			Paused: r.Paused != 0 || r.QBPaused != 0}
		t.SavePath = r.QBSavePath
		if t.SavePath == "" {
			t.SavePath = r.SavePath
		}
		if tb, err := readLimited(base+".torrent", maxTorrentSize); err == nil {
			t.TorrentFile = tb
		} else if len(r.Info) > 0 {
			// A minimal .torrent: just the info dictionary (trackers come from DHT/PEX).
			var buf bytes.Buffer
			buf.WriteString("d4:info")
			buf.Write(r.Info)
			buf.WriteByte('e')
			t.TorrentFile = buf.Bytes()
		} else {
			continue // metadata never arrived in qBittorrent either
		}
		if t.fill() != nil {
			continue
		}
		t.FilePriorities = libtorrentPriorities(r.FilePriority)
		out = append(out, t)
	}
	return sorted(out), nil
}

// libtorrentPriorities maps 0..7 (0 = skip, 1 normal ... 7 top) to Magnetar's.
func libtorrentPriorities(p []int) map[int]int {
	var m map[int]int
	for i, v := range p {
		var prio int
		switch {
		case v <= 0:
			prio = 0
		case v >= 6:
			prio = 2
		default:
			continue // normal
		}
		if m == nil {
			m = map[int]int{}
		}
		m[i] = prio
	}
	return m
}

// --- Transmission: torrents/*.torrent + resume/*.resume ---

type trResume struct {
	Destination string   `bencode:"destination"`
	Paused      int      `bencode:"paused"`
	Uploaded    int64    `bencode:"uploaded"`
	Downloaded  int64    `bencode:"downloaded"`
	DND         []int    `bencode:"dnd"`
	Priority    []int    `bencode:"priority"`
	Labels      []string `bencode:"labels"`
}

func loadTransmission(dir string) ([]Torrent, error) {
	files, _ := filepath.Glob(filepath.Join(dir, "torrents", "*.torrent"))
	var out []Torrent
	for _, tp := range files {
		if len(out) >= maxTorrents {
			break
		}
		tb, err := readLimited(tp, maxTorrentSize)
		if err != nil {
			continue
		}
		t := Torrent{TorrentFile: tb}
		if t.fill() != nil {
			continue
		}
		base := strings.TrimSuffix(filepath.Base(tp), ".torrent")
		var r trResume
		for _, rp := range []string{filepath.Join(dir, "resume", base+".resume"), filepath.Join(dir, "resume", t.Hash+".resume")} {
			if rb, err := readLimited(rp, maxTorrentSize); err == nil && bencode.Unmarshal(rb, &r) == nil {
				break
			}
		}
		if r.Destination == "" {
			continue
		}
		t.SavePath, t.Paused, t.Uploaded, t.Downloaded = r.Destination, r.Paused != 0, r.Uploaded, r.Downloaded
		if len(r.Labels) > 0 {
			t.Category = strings.TrimSpace(r.Labels[0])
		}
		for i := range max(len(r.DND), len(r.Priority)) {
			prio := 1
			if i < len(r.Priority) && r.Priority[i] > 0 {
				prio = 2
			}
			if i < len(r.DND) && r.DND[i] != 0 {
				prio = 0
			}
			if prio != 1 {
				if t.FilePriorities == nil {
					t.FilePriorities = map[int]int{}
				}
				t.FilePriorities[i] = prio
			}
		}
		out = append(out, t)
	}
	return sorted(out), nil
}

// --- uTorrent / BitTorrent: resume.dat ---

type utEntry struct {
	Path       string `bencode:"path"`
	Started    int    `bencode:"started"`
	Label      string `bencode:"label"`
	Prio       string `bencode:"prio"`
	Uploaded   int64  `bencode:"uploaded"`
	Downloaded int64  `bencode:"downloaded"`
}

func loadUTorrent(dir string) ([]Torrent, error) {
	rb, err := readLimited(filepath.Join(dir, "resume.dat"), maxResumeSize)
	if err != nil {
		return nil, err
	}
	var all map[string]bencode.Bytes
	if err := bencode.Unmarshal(rb, &all); err != nil {
		return nil, fmt.Errorf("resume.dat: %w", err)
	}
	var out []Torrent
	for key, raw := range all {
		if len(out) >= maxTorrents || !strings.HasSuffix(strings.ToLower(key), ".torrent") {
			continue
		}
		var e utEntry
		if bencode.Unmarshal(raw, &e) != nil || e.Path == "" {
			continue
		}
		tp := key
		if !filepath.IsAbs(tp) {
			tp = filepath.Join(dir, key)
		}
		tb, err := readLimited(tp, maxTorrentSize)
		if err != nil {
			continue
		}
		t := Torrent{TorrentFile: tb, Category: strings.TrimSpace(e.Label), Uploaded: e.Uploaded, Downloaded: e.Downloaded,
			Paused: e.Started == 0}
		if t.fill() != nil {
			continue
		}
		// uTorrent stores the file (single-file torrents) or the content
		// folder itself; Magnetar wants the folder that contains it.
		t.SavePath = filepath.Dir(e.Path)
		for i := 0; i < len(e.Prio); i++ {
			prio := 1
			switch {
			case e.Prio[i] == 0 || e.Prio[i] == 128:
				prio = 0
			case e.Prio[i] >= 12:
				prio = 2
			}
			if prio != 1 {
				if t.FilePriorities == nil {
					t.FilePriorities = map[int]int{}
				}
				t.FilePriorities[i] = prio
			}
		}
		out = append(out, t)
	}
	return sorted(out), nil
}

func sorted(ts []Torrent) []Torrent {
	sort.Slice(ts, func(i, j int) bool { return strings.ToLower(ts[i].Name) < strings.ToLower(ts[j].Name) })
	return ts
}
