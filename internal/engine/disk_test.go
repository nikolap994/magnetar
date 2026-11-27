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

// A download kept in the incomplete folder moves to its real folder when
// it completes.
func TestIncompleteFolderMovesOnCompletion(t *testing.T) {
	src := t.TempDir()
	os.WriteFile(filepath.Join(src, "file.bin"), bytes.Repeat([]byte("x"), 50000), 0o600)
	info := metainfo.Info{PieceLength: 1 << 14}
	if err := info.BuildFromFilePath(filepath.Join(src, "file.bin")); err != nil {
		t.Fatal(err)
	}
	ib, _ := bencode.Marshal(info)
	var buf bytes.Buffer
	(&metainfo.MetaInfo{InfoBytes: ib}).Write(&buf)

	s := config.Defaults()
	s.QueueEnabled = false
	s.IncompleteEnabled = true
	s.IncompleteDir = t.TempDir()
	e := offlineEngine(t, s)
	final := t.TempDir()
	// Pretend the data has already arrived in the incomplete folder.
	os.WriteFile(filepath.Join(s.IncompleteDir, "file.bin"), bytes.Repeat([]byte("x"), 50000), 0o600)
	h, err := e.Add(AddOptions{TorrentFile: buf.Bytes(), SavePath: final})
	if err != nil {
		t.Fatal(err)
	}
	if l := e.List(); l[0].SavePath != filepath.Clean(s.IncompleteDir) {
		t.Fatalf("should download into the incomplete folder, got %s", l[0].SavePath)
	}
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		if l := e.List(); l[0].SavePath == final && l[0].State != StateMoving {
			if _, err := os.Stat(filepath.Join(final, "file.bin")); err != nil {
				t.Fatal("file not moved")
			}
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatalf("not moved: %+v (%s)", e.List(), h)
}

func TestLowDiskPausesDownloads(t *testing.T) {
	s := config.Defaults()
	s.QueueEnabled = false
	s.MinFreeSpaceMB = 1 << 29 // far more than any disk has free
	e := offlineEngine(t, s)
	if _, err := e.Add(AddOptions{Magnet: "magnet:?xt=urn:btih:" + "ab" + "000000000000000000000000000000000000ab"}); err != nil {
		t.Fatal(err)
	}
	e.mu.Lock()
	e.checkDiskLocked(time.Now())
	e.mu.Unlock()
	l := e.List()
	if l[0].State != StateError || l[0].Error == "" {
		t.Fatalf("want paused with a reason, got %+v", l[0])
	}
}
