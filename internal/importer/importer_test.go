package importer

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/anacrolix/torrent/bencode"
	"github.com/anacrolix/torrent/metainfo"
)

func makeTorrent(t *testing.T, name string) ([]byte, string) {
	t.Helper()
	info := metainfo.Info{Name: name, PieceLength: 1 << 14, Pieces: make([]byte, 20),
		Files: []metainfo.FileInfo{{Path: []string{"a.mkv"}, Length: 100}, {Path: []string{"b.srt"}, Length: 10}, {Path: []string{"c.nfo"}, Length: 5}}}
	ib, err := bencode.Marshal(info)
	if err != nil {
		t.Fatal(err)
	}
	mi := metainfo.MetaInfo{InfoBytes: ib}
	var buf bytes.Buffer
	mi.Write(&buf)
	return buf.Bytes(), mi.HashInfoBytes().HexString()
}

func write(t *testing.T, path string, v any) {
	t.Helper()
	os.MkdirAll(filepath.Dir(path), 0o755)
	var b []byte
	switch x := v.(type) {
	case []byte:
		b = x
	default:
		var err error
		if b, err = bencode.Marshal(v); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(path, b, 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestQBittorrent(t *testing.T) {
	dir := t.TempDir()
	tb, hash := makeTorrent(t, "Show")
	write(t, filepath.Join(dir, hash+".torrent"), tb)
	write(t, filepath.Join(dir, hash+".fastresume"), map[string]any{
		"qBt-savePath": "/data/tv", "save_path": "/old", "paused": 1, "qBt-category": "TV",
		"file_priority": []int{1, 0, 7}, "total_uploaded": 500, "total_downloaded": 115,
	})
	// A magnet whose metadata never arrived is skipped.
	write(t, filepath.Join(dir, "ffffffffffffffffffffffffffffffffffffffff.fastresume"), map[string]any{"save_path": "/x"})

	ts, err := Load(QBittorrent, dir)
	if err != nil || len(ts) != 1 {
		t.Fatalf("%v %v", ts, err)
	}
	g := ts[0]
	if g.Hash != hash || g.SavePath != "/data/tv" || !g.Paused || g.Category != "TV" || g.Uploaded != 500 || g.Size != 115 {
		t.Fatalf("%+v", g)
	}
	if g.FilePriorities[1] != 0 || g.FilePriorities[2] != 2 || len(g.FilePriorities) != 2 {
		t.Fatalf("priorities %v", g.FilePriorities)
	}
}

func TestQBittorrentInfoInResume(t *testing.T) {
	dir := t.TempDir()
	tb, hash := makeTorrent(t, "Magnet")
	mi, _ := metainfo.Load(bytes.NewReader(tb))
	write(t, filepath.Join(dir, hash+".fastresume"), map[string]any{"save_path": "/dl", "info": mi.InfoBytes})
	ts, err := Load(QBittorrent, dir)
	if err != nil || len(ts) != 1 || ts[0].Hash != hash {
		t.Fatalf("%+v %v", ts, err)
	}
}

func TestTransmission(t *testing.T) {
	dir := t.TempDir()
	tb, _ := makeTorrent(t, "Distro")
	write(t, filepath.Join(dir, "torrents", "Distro.abcd.torrent"), tb)
	write(t, filepath.Join(dir, "resume", "Distro.abcd.resume"), map[string]any{
		"destination": "/srv/iso", "paused": 0, "uploaded": 9, "downloaded": 3, "dnd": []int{0, 1, 0}, "priority": []int{1, 0, 0}, "labels": []string{"Linux"},
	})
	ts, err := Load(Transmission, dir)
	if err != nil || len(ts) != 1 {
		t.Fatalf("%v %v", ts, err)
	}
	g := ts[0]
	if g.SavePath != "/srv/iso" || g.Paused || g.Category != "Linux" || g.FilePriorities[0] != 2 || g.FilePriorities[1] != 0 {
		t.Fatalf("%+v", g)
	}
}

func TestUTorrent(t *testing.T) {
	dir := t.TempDir()
	tb, hash := makeTorrent(t, "Album")
	write(t, filepath.Join(dir, "Album.torrent"), tb)
	write(t, filepath.Join(dir, "resume.dat"), map[string]any{
		".fileguard": "x",
		"rec":        []int{1},
		"Album.torrent": map[string]any{
			"path": filepath.Join("/music", "Album"), "started": 0, "label": "Music",
			"prio": string([]byte{8, 0, 15}), "uploaded": 7, "downloaded": 115,
		},
	})
	ts, err := Load(UTorrent, dir)
	if err != nil || len(ts) != 1 {
		t.Fatalf("%v %v", ts, err)
	}
	g := ts[0]
	if g.Hash != hash || g.SavePath != filepath.Dir(filepath.Join("/music", "Album")) || !g.Paused || g.Category != "Music" || g.FilePriorities[1] != 0 || g.FilePriorities[2] != 2 {
		t.Fatalf("%+v", g)
	}
}

func TestLoadMissingFolder(t *testing.T) {
	if _, err := Load(QBittorrent, filepath.Join(t.TempDir(), "nope")); err == nil {
		t.Fatal("expected an error")
	}
}
