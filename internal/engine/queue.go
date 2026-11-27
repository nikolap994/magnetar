package engine

import (
	"errors"
	"slices"
	"time"
)

// Torrents that move less than slowRate after slowGrace do not take up a
// queue slot when "don't count slow torrents" is on.
const (
	slowRate  = 2 << 10 // bytes per second
	slowGrace = 60 * time.Second
)

// Queue moves for MoveQueue.
const (
	QueueTop    = "top"
	QueueUp     = "up"
	QueueDown   = "down"
	QueueBottom = "bottom"
)

// queuedLocked reports whether a torrent is waiting for a queue slot: not
// paused by the user, not running and not failed.
func (m *managed) queuedLocked() bool {
	return m.t == nil && !m.rec.Paused && m.err == "" && !m.moving
}

// doneLocked reports whether a torrent has everything it wants.
func (m *managed) doneLocked() bool {
	if m.t != nil && m.t.Info() != nil {
		return m.t.BytesMissing() == 0
	}
	return m.rec.CompletedAt != nil
}

type queueCount struct{ down, up, total int }

func within(n, limit int) bool { return limit <= 0 || n < limit }

// hasSlot reports whether one more downloading (or seeding) torrent fits.
func (e *Engine) hasSlot(c queueCount, done bool) bool {
	s := e.settings
	if !within(c.total, s.MaxActiveTorrents) {
		return false
	}
	if done {
		return within(c.up, s.MaxActiveUploads)
	}
	return within(c.down, s.MaxActiveDownloads)
}

// countsLocked reports whether a running torrent occupies a queue slot.
func (e *Engine) countsLocked(m *managed, now time.Time) bool {
	if m.rec.Forced || m.moving || m.checking {
		return false
	}
	if m.doneLocked() && !e.settings.SeedAfterComplete {
		return false
	}
	if e.settings.QueueSkipSlow && now.Sub(m.startedAt) > slowGrace {
		if m.doneLocked() {
			return m.upRate >= slowRate
		}
		return m.downRate >= slowRate || m.upRate >= slowRate
	}
	return true
}

// queueLocked starts and stops torrents so that no more than the configured
// number are active, in queue order (the list order). Torrents beyond the
// limits wait as "queued" and start by themselves when a slot frees up.
func (e *Engine) queueLocked(now time.Time) {
	if e.cl == nil {
		return
	}
	if !e.settings.QueueEnabled {
		for _, h := range e.order {
			if m := e.torrents[h]; m.queuedLocked() {
				e.startQueuedLocked(m)
			}
		}
		return
	}
	// One pass in queue order: a torrent higher in the queue gets a slot
	// before one further down, even if the lower one is already running.
	var c queueCount
	take := func(done bool) {
		c.total++
		if done {
			c.up++
		} else {
			c.down++
		}
	}
	for _, h := range e.order {
		m := e.torrents[h]
		switch {
		case m.t != nil:
			if !e.countsLocked(m, now) {
				continue
			}
			done := m.doneLocked()
			if !e.hasSlot(c, done) {
				e.stopLocked(m)
				e.dirty = true
				continue
			}
			take(done)
		case m.queuedLocked():
			done := m.doneLocked()
			if m.rec.Forced {
				e.startQueuedLocked(m)
			} else if e.hasSlot(c, done) && e.startQueuedLocked(m) {
				take(done)
			}
		}
	}
}

func (e *Engine) startQueuedLocked(m *managed) bool {
	if err := e.startLocked(m); err != nil {
		m.err = err.Error()
		e.log.Warn("start torrent", "name", logSafe(m.rec.Name), "err", logSafe(err.Error()))
		return false
	}
	e.dirty = true
	return true
}

// slotForNewLocked reports whether a newly added or resumed torrent may
// start right away.
func (e *Engine) slotForNewLocked(m *managed) bool {
	if !e.settings.QueueEnabled || m.rec.Forced {
		return true
	}
	now := time.Now()
	var c queueCount
	for _, h := range e.order {
		o := e.torrents[h]
		if o == m || o.t == nil || !e.countsLocked(o, now) {
			continue
		}
		c.total++
		if o.doneLocked() {
			c.up++
		} else {
			c.down++
		}
	}
	return e.hasSlot(c, m.doneLocked())
}

// ForceStart starts torrents regardless of the queue limits (or puts them
// back under the queue's control).
func (e *Engine) ForceStart(on bool, hashes ...string) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	for _, h := range hashes {
		m, err := e.get(h)
		if err != nil {
			return err
		}
		m.rec.Forced = on
		if on {
			m.rec.Paused = false
			m.err = ""
		}
	}
	e.dirty = true
	e.queueLocked(time.Now())
	return nil
}

// MoveQueue changes the queue position of torrents (top, up, down, bottom).
// The queue order is the list order.
func (e *Engine) MoveQueue(where string, hashes ...string) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	sel := map[string]bool{}
	for _, h := range hashes {
		if _, err := e.get(h); err != nil {
			return err
		}
		sel[h] = true
	}
	order := e.order
	switch where {
	case QueueTop, QueueBottom:
		var picked, rest []string
		for _, h := range order {
			if sel[h] {
				picked = append(picked, h)
			} else {
				rest = append(rest, h)
			}
		}
		if where == QueueTop {
			order = append(picked, rest...)
		} else {
			order = append(rest, picked...)
		}
	case QueueUp:
		order = slices.Clone(order)
		for i := 1; i < len(order); i++ {
			if sel[order[i]] && !sel[order[i-1]] {
				order[i], order[i-1] = order[i-1], order[i]
			}
		}
	case QueueDown:
		order = slices.Clone(order)
		for i := len(order) - 2; i >= 0; i-- {
			if sel[order[i]] && !sel[order[i+1]] {
				order[i], order[i+1] = order[i+1], order[i]
			}
		}
	default:
		return errors.New("unknown queue move")
	}
	e.order = order
	e.dirty = true
	e.queueLocked(time.Now())
	return nil
}
