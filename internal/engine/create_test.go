package engine

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/anacrolix/torrent/metainfo"

	"github.com/nikolap994/magnetar/internal/config"
)

func TestCreateTorrentAndSeed(t *testing.T) {
	s := config.Defaults()
	s.QueueEnabled = false
	e := offlineEngine(t, s)

	base := t.TempDir()
	root := filepath.Join(base, "Album")
	os.MkdirAll(filepath.Join(root, "cd2"), 0o755)
	os.WriteFile(filepath.Join(root, "01.flac"), bytes.Repeat([]byte("a"), 300000), 0o600)
	os.WriteFile(filepath.Join(root, "cd2", "02.flac"), bytes.Repeat([]byte("b"), 70000), 0o600)
	secret := filepath.Join(base, "secret.txt")
	os.WriteFile(secret, []byte("do not share"), 0o600)
	if err := os.Symlink(secret, filepath.Join(root, "link.txt")); err != nil {
		t.Skip("symlinks not supported:", err)
	}

	if _, err := e.CreateTorrent(CreateOptions{Path: root, Trackers: []string{"ftp://bad"}}); err == nil {
		t.Fatal("bad tracker accepted")
	}
	job, err := e.CreateTorrent(CreateOptions{Path: root, Trackers: []string{"udp://tracker.example:1337/announce"}, Comment: "test", StartSeed: true})
	if err != nil {
		t.Fatal(err)
	}
	var data []byte
	for range 200 {
		var j *CreateJob
		j, data, _ = e.CreateStatus(job.ID)
		if j.State == "error" {
			t.Fatal(j.Error)
		}
		if j.State == "done" && j.Seeding {
			job = j
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if job.State != "done" || len(data) == 0 || job.Magnet == "" {
		t.Fatalf("job %+v", job)
	}
	mi, err := metainfo.Load(bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	info, _ := mi.UnmarshalInfo()
	if len(info.Files) != 2 || info.TotalLength() != 370000 || mi.Announce == "" || mi.Comment != "test" {
		t.Fatalf("info: %d files, %d bytes, announce %q", len(info.Files), info.TotalLength(), mi.Announce)
	}
	// The new torrent seeds from the existing files after verifying them.
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if l := e.List(); len(l) == 1 && l[0].Progress == 1 {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("not seeding: %+v", e.List())
}
