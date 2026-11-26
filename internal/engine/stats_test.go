package engine

import (
	"testing"
	"time"
)

func TestRecordTransferHistory(t *testing.T) {
	e := &Engine{}
	base := time.Date(2026, 11, 26, 10, 0, 0, 0, time.UTC)
	e.stats.minuteStart = base.Unix()
	for i := range 60 { // one minute at 1 MiB/s down, 256 KiB/s up
		e.recordTransferLocked(base.Add(time.Duration(i)*time.Second), 1<<20, 256<<10)
	}
	e.recordTransferLocked(base.Add(time.Minute), 0, 0)
	if len(e.stats.History) != 1 || e.stats.History[0].Down != 1<<20 || e.stats.History[0].Up != 256<<10 {
		t.Fatalf("first minute = %+v", e.stats.History)
	}
	if e.stats.AllTimeDown != 60<<20 || e.stats.sessionUp != 60*(256<<10) {
		t.Fatalf("totals down=%d up=%d", e.stats.AllTimeDown, e.stats.sessionUp)
	}
	// Magnetar was closed for 5 minutes: those minutes are recorded as zero.
	e.recordTransferLocked(base.Add(7*time.Minute), 0, 0)
	if n := len(e.stats.History); n != 7 {
		t.Fatalf("history length after a gap = %d, want 7", n)
	}
	// Anything older than 24 hours is dropped.
	e.recordTransferLocked(base.Add(30*time.Hour), 0, 0)
	cutoff := base.Add(30*time.Hour).Truncate(time.Minute).Unix() - historyMinutes*60
	for _, s := range e.stats.History {
		if s.T < cutoff {
			t.Fatalf("sample at %d is older than 24 hours", s.T)
		}
	}
	if len(e.stats.History) > historyMinutes {
		t.Fatalf("history has %d samples, max %d", len(e.stats.History), historyMinutes)
	}
	// Negative deltas (a torrent restarted) never reduce totals.
	before := e.stats.AllTimeDown
	e.recordTransferLocked(base.Add(30*time.Hour+time.Second), -500, -500)
	if e.stats.AllTimeDown != before {
		t.Fatal("negative delta changed totals")
	}
}
