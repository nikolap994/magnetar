package engine

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/anacrolix/torrent/bencode"
	"github.com/anacrolix/torrent/metainfo"

	"github.com/nikolap994/magnetar/internal/config"
)

func TestScanWatchDir(t *testing.T) {
	e := offlineEngine(t, config.Defaults())
	dir := t.TempDir()
	info := metainfo.Info{Name: "a.bin", Length: 1 << 14, PieceLength: 1 << 14, Pieces: make([]byte, 20)}
	ib, _ := bencode.Marshal(info)
	var buf bytes.Buffer
	(&metainfo.MetaInfo{InfoBytes: ib}).Write(&buf)
	old := time.Now().Add(-time.Minute)
	write := func(name string, data []byte) {
		p := filepath.Join(dir, name)
		os.WriteFile(p, data, 0o600)
		os.Chtimes(p, old, old)
	}
	write("good.torrent", buf.Bytes())
	write("bad.torrent", []byte("not bencode"))
	write("notes.txt", []byte("ignore me"))
	fresh := filepath.Join(dir, "fresh.torrent")
	os.WriteFile(fresh, buf.Bytes(), 0o600) // still being written: wait

	if n := e.scanWatchDir(dir, "", true); n != 1 {
		t.Fatalf("added %d, want 1", n)
	}
	for _, name := range []string{"good.torrent.added", "bad.torrent.invalid", "notes.txt", "fresh.torrent"} {
		if _, err := os.Stat(filepath.Join(dir, name)); err != nil {
			t.Errorf("%s: %v", name, err)
		}
	}
	if l := e.List(); len(l) != 1 || l[0].State != StatePaused {
		t.Fatalf("list: %+v", l)
	}
	// The same torrent again counts as handled, not invalid.
	os.Chtimes(fresh, old, old)
	e.scanWatchDir(dir, "", true)
	if _, err := os.Stat(fresh + ".added"); err != nil {
		t.Fatal("duplicate should be marked .added")
	}
}
