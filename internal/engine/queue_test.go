package engine

import (
	"fmt"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/anacrolix/torrent/metainfo"

	"github.com/nikolap994/magnetar/internal/config"
)

func offlineEngine(t *testing.T, s config.Settings) *Engine {
	t.Helper()
	s.DownloadDir = t.TempDir()
	s.ListenPort = 0
	s.EnableDHT, s.EnableLSD, s.EnableUPnP, s.EnableUTP = false, false, false, false
	e, err := New(s, t.TempDir(), slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { e.Close() })
	return e
}

func states(e *Engine) (running, queued []string) {
	for _, s := range e.List() {
		switch s.State {
		case StateQueued:
			queued = append(queued, s.Hash)
		case StatePaused:
		default:
			running = append(running, s.Hash)
		}
	}
	return
}

func TestQueueLimitsActiveDownloads(t *testing.T) {
	s := config.Defaults()
	s.MaxActiveDownloads, s.MaxActiveTorrents = 2, 0
	e := offlineEngine(t, s)
	var hashes []string
	for i := range 5 {
		h := fmt.Sprintf("%040x", i+1)
		if _, err := e.Add(AddOptions{Magnet: "magnet:?xt=urn:btih:" + h}); err != nil {
			t.Fatal(err)
		}
		hashes = append(hashes, h)
	}
	run, q := states(e)
	if len(run) != 2 || len(q) != 3 || run[0] != hashes[0] || run[1] != hashes[1] {
		t.Fatalf("want first two running, three queued; got running=%v queued=%v", run, q)
	}

	// Moving the last torrent to the top gives it a slot; the second waits.
	if err := e.MoveQueue(QueueTop, hashes[4]); err != nil {
		t.Fatal(err)
	}
	run, _ = states(e)
	if len(run) != 2 || run[0] != hashes[4] || run[1] != hashes[0] {
		t.Fatalf("after move to top: running=%v", run)
	}

	// Force start ignores the limit; pausing frees a slot for the next one.
	if err := e.ForceStart(true, hashes[3]); err != nil {
		t.Fatal(err)
	}
	if run, _ = states(e); len(run) != 3 {
		t.Fatalf("force start: running=%v", run)
	}
	if err := e.Pause(hashes[4]); err != nil {
		t.Fatal(err)
	}
	e.mu.Lock()
	e.queueLocked(time.Now())
	e.mu.Unlock()
	run, q = states(e)
	if len(run) != 3 || len(q) != 1 {
		t.Fatalf("after pause: running=%v queued=%v", run, q)
	}

	// Without the queue everything that is not paused runs.
	ns := e.Settings()
	ns.QueueEnabled = false
	if err := e.ApplySettings(ns); err != nil {
		t.Fatal(err)
	}
	e.mu.Lock()
	e.queueLocked(time.Now())
	e.mu.Unlock()
	if run, q = states(e); len(run) != 4 || len(q) != 0 {
		t.Fatalf("queue off: running=%v queued=%v", run, q)
	}
}

func TestCompletedIn(t *testing.T) {
	info := &metainfo.Info{PieceLength: 10, Length: 35, Pieces: make([]byte, 4*20)}
	have := []bool{true, false, true, true}
	// file spans bytes 5..27: piece 0 (5 bytes), piece 1 (missing), piece 2 (8 bytes)
	if got := completedIn(info, have, 5, 23); got != 13 {
		t.Fatalf("got %d, want 13", got)
	}
	// last, short piece: bytes 30..34
	if got := completedIn(info, have, 30, 5); got != 5 {
		t.Fatalf("got %d, want 5", got)
	}
}
