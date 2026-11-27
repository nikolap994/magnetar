package engine

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/anacrolix/torrent/metainfo"
	"github.com/nikolap994/magnetar/internal/config"
	"golang.org/x/time/rate"
	"log/slog"
)

func TestDeleteTorrentDataRefusesUnsafeNames(t *testing.T) {
	base := t.TempDir()
	victim := filepath.Join(filepath.Dir(base), "victim-"+filepath.Base(base))
	if err := os.WriteFile(victim, []byte("keep"), 0o600); err != nil {
		t.Fatal(err)
	}
	defer os.Remove(victim)
	for _, name := range []string{"", ".", "..", "../" + filepath.Base(victim), "a/../../b"} {
		if err := deleteTorrentData(base, &metainfo.Info{Name: name, Length: 1}); err == nil {
			t.Errorf("name %q: expected refusal", name)
		}
	}
	if _, err := os.Stat(victim); err != nil {
		t.Fatal("file outside save path was deleted")
	}
}

func TestDeleteTorrentDataSingleAndMulti(t *testing.T) {
	base := t.TempDir()
	single := filepath.Join(base, "movie.mkv")
	os.WriteFile(single, []byte("x"), 0o600)
	if err := deleteTorrentData(base, &metainfo.Info{Name: "movie.mkv", Length: 1}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(single); !os.IsNotExist(err) {
		t.Fatal("single file not deleted")
	}

	dir := filepath.Join(base, "album")
	os.MkdirAll(filepath.Join(dir, "cd1"), 0o755)
	os.WriteFile(filepath.Join(dir, "cd1", "t.flac"), []byte("x"), 0o600)
	info := &metainfo.Info{Name: "album", Files: []metainfo.FileInfo{{Path: []string{"cd1", "t.flac"}, Length: 1}}}
	if err := deleteTorrentData(base, info); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Fatal("torrent dir not deleted")
	}
}

func TestParseBareHash(t *testing.T) {
	if _, ok := parseBareHash("DD8255ECDC7CA55FB0BBF81323D87062DB1F6D1C"); !ok {
		t.Fatal("valid hash rejected")
	}
	for _, s := range []string{"", "xyz", "dd8255ecdc7ca55fb0bbf81323d87062db1f6d1", "zz8255ecdc7ca55fb0bbf81323d87062db1f6d1c"} {
		if _, ok := parseBareHash(s); ok {
			t.Errorf("%q accepted", s)
		}
	}
}

func TestRatio(t *testing.T) {
	if r := ratio(100, 200, 0); r != 2 {
		t.Fatalf("ratio = %v", r)
	}
	if r := ratio(0, 50, 100); r != 0.5 {
		t.Fatalf("ratio with no download = %v", r)
	}
	if r := ratio(0, 0, 0); r != 0 {
		t.Fatalf("zero ratio = %v", r)
	}
}

func TestSharesData(t *testing.T) {
	e := &Engine{dataDir: t.TempDir(), torrents: map[string]*managed{
		"a": {rec: &record{InfoHash: "a", Name: "Sintel", SavePath: "/dl"}},
		"b": {rec: &record{InfoHash: "b", Name: "Other", SavePath: "/dl"}},
		"c": {rec: &record{InfoHash: "c", Name: "Sintel", SavePath: "/elsewhere"}},
	}}
	if got := e.sharesDataLocked("/dl", "Sintel"); got != "Sintel" {
		t.Fatalf("expected conflict, got %q", got)
	}
	if got := e.sharesDataLocked("/dl", "Unique"); got != "" {
		t.Fatalf("unexpected conflict %q", got)
	}
	if got := e.sharesDataLocked("/other", "Sintel"); got != "" {
		t.Fatalf("different path reported as conflict: %q", got)
	}
}

func TestHasExistingDataIgnoresTraversal(t *testing.T) {
	root := t.TempDir()
	save := filepath.Join(root, "dl")
	os.MkdirAll(save, 0o755)
	// A file outside the save folder that a malicious torrent tries to probe.
	if err := os.WriteFile(filepath.Join(root, "secret.txt"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	evil := &metainfo.Info{Name: "pack", Files: []metainfo.FileInfo{{Path: []string{"..", "..", "secret.txt"}, Length: 1}}}
	if hasExistingData(save, evil) {
		t.Fatal("followed a '..' path from torrent metadata outside the save folder")
	}
	evilSingle := &metainfo.Info{Name: "../secret.txt", Length: 1}
	if hasExistingData(save, evilSingle) {
		t.Fatal("followed a '..' torrent name outside the save folder")
	}

	good := &metainfo.Info{Name: "pack", Files: []metainfo.FileInfo{{Path: []string{"a.bin"}, Length: 1}}}
	if hasExistingData(save, good) {
		t.Fatal("reported data that does not exist")
	}
	os.MkdirAll(filepath.Join(save, "pack"), 0o755)
	os.WriteFile(filepath.Join(save, "pack", "a.bin.part"), []byte("x"), 0o600)
	if !hasExistingData(save, good) {
		t.Fatal("missed an existing partial file")
	}
}

func TestConnectionHealth(t *testing.T) {
	e := &Engine{started: time.Now().Add(-time.Hour)}
	e.settings.EnableDHT = true
	online := GlobalStats{Online: true, DHTNodes: 50, Peers: 3}
	if got := e.connectionLocked(online); got != "idle" {
		t.Fatalf("nothing active = %q, want idle", got)
	}
	e.activeSince = time.Now()
	if got := e.connectionLocked(online); got != "checking" {
		t.Fatalf("just became active = %q, want checking", got)
	}
	e.activeSince = time.Now().Add(-10 * time.Minute)
	if got := e.connectionLocked(online); got != "firewalled" {
		t.Fatalf("no incoming after 10 min = %q, want firewalled", got)
	}
	e.incoming.Store(1)
	if got := e.connectionLocked(online); got != "connectable" {
		t.Fatalf("with incoming = %q, want connectable", got)
	}
	if got := e.connectionLocked(GlobalStats{Online: true}); got != "offline" {
		t.Fatalf("no DHT nodes or peers = %q, want offline", got)
	}
	if got := e.connectionLocked(GlobalStats{}); got != "offline" {
		t.Fatalf("client down = %q, want offline", got)
	}
}

func TestSpeedScheduleBoundaries(t *testing.T) {
	s := config.Defaults() // 09:00–17:00 Mon–Fri
	s.AltScheduleEnabled = true
	e := &Engine{settings: s, log: slog.New(slog.DiscardHandler),
		downLimiter: rate.NewLimiter(rate.Inf, 1), upLimiter: rate.NewLimiter(rate.Inf, 1)}
	saved := 0
	e.onSettingsChanged = func(config.Settings) { saved++ }
	mon := func(h, m int) time.Time { return time.Date(2026, 11, 23, h, m, 0, 0, time.Local) } // a Monday

	e.applyScheduleLocked(mon(8, 0))
	if e.settings.AltSpeedEnabled {
		t.Fatal("alternative limits on before the window")
	}
	e.applyScheduleLocked(mon(9, 0))
	if !e.settings.AltSpeedEnabled || saved != 1 {
		t.Fatalf("not switched on at the start (alt=%v saved=%d)", e.settings.AltSpeedEnabled, saved)
	}
	if e.downLimiter.Limit() != rate.Limit(s.AltDownloadLimitKBps*1024) {
		t.Fatalf("alternative download limit not applied: %v", e.downLimiter.Limit())
	}
	// The user turns it off manually: respected until the next boundary.
	e.settings.AltSpeedEnabled = false
	e.applyScheduleLocked(mon(12, 0))
	if e.settings.AltSpeedEnabled {
		t.Fatal("manual override was undone inside the window")
	}
	e.applyScheduleLocked(mon(17, 0))
	e.applyScheduleLocked(mon(18, 0))
	if e.settings.AltSpeedEnabled {
		t.Fatal("still on after the window")
	}
}

func TestLogSafe(t *testing.T) {
	if got := logSafe("evil\nlevel=ERROR msg=fake\r\x1b[31m"); strings.ContainsAny(got, "\n\r\x1b") {
		t.Fatalf("control characters left: %q", got)
	}
}

func TestIsExecutable(t *testing.T) {
	for _, n := range []string{"setup.EXE", "run.bat", "x.ps1", "a.sh", "page.html", "img.svg", "x.lnk", "t.msi.part"} {
		if !IsExecutable(n) {
			t.Errorf("%s should be treated as executable", n)
		}
	}
	for _, n := range []string{"movie.mkv", "song.flac", "readme.txt", "photo.jpg", "archive.zip"} {
		if IsExecutable(n) {
			t.Errorf("%s should be openable", n)
		}
	}
}
