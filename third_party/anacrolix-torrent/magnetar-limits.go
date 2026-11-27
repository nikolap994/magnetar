package torrent

// Magnetar patch: per-torrent speed limits, applied on top of the client-wide
// limiters. Limits are kept in atomic pointers so the read and upload paths
// never need the client lock to find them.

import (
	"io"
	"sync/atomic"
	"time"

	"golang.org/x/time/rate"
)

type torrentLimits struct {
	down atomic.Pointer[rate.Limiter]
	up   atomic.Pointer[rate.Limiter]
}

func newTorrentLimiter(bytesPerSecond int) *rate.Limiter {
	if bytesPerSecond <= 0 {
		return nil
	}
	// The burst must hold a whole chunk a peer may request or send.
	return rate.NewLimiter(rate.Limit(bytesPerSecond), max(bytesPerSecond, 256<<10))
}

// SetRateLimits limits this torrent's download and upload speed in bytes per
// second (0 = only the client-wide limits apply).
func (t *Torrent) SetRateLimits(downBytesPerSecond, upBytesPerSecond int) {
	t.limits.down.Store(newTorrentLimiter(downBytesPerSecond))
	t.limits.up.Store(newTorrentLimiter(upBytesPerSecond))
}

// torrentLimitedReader throttles a peer connection's incoming data with its
// torrent's download limiter, if one is set.
type torrentLimitedReader struct {
	t *Torrent
	r io.Reader
}

func (me torrentLimitedReader) Read(b []byte) (int, error) {
	l := me.t.limits.down.Load()
	if l == nil {
		return me.r.Read(b)
	}
	b = b[:min(len(b), l.Burst())]
	n, err := me.r.Read(b)
	if n > 0 {
		if res := l.ReserveN(time.Now(), n); res.OK() {
			time.Sleep(res.Delay())
		}
	}
	return n, err
}

// reserveTorrentUpload takes n bytes from the torrent's upload limiter. It
// returns how long to wait first; a zero delay means the bytes were taken.
func (t *Torrent) reserveTorrentUpload(n int) time.Duration {
	l := t.limits.up.Load()
	if l == nil {
		return 0
	}
	res := l.ReserveN(time.Now(), n)
	if !res.OK() {
		return time.Second
	}
	if d := res.Delay(); d > 0 {
		res.Cancel()
		return d
	}
	return 0
}
