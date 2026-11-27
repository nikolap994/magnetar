package engine

import (
	"fmt"
	"path/filepath"
	"time"

	"github.com/nikolap994/magnetar/internal/fsutil"
)

const diskCheckEvery = 10 * time.Second

// checkDiskLocked pauses downloads whose folder is running out of space, so a
// full disk never corrupts files or stalls the whole computer.
func (e *Engine) checkDiskLocked(now time.Time) {
	minFree := uint64(max(e.settings.MinFreeSpaceMB, 0)) << 20
	if minFree == 0 || now.Sub(e.lastDiskCheck) < diskCheckEvery {
		return
	}
	e.lastDiskCheck = now
	free := map[string]uint64{}
	for _, h := range e.order {
		m := e.torrents[h]
		if m.t == nil || m.doneLocked() {
			continue
		}
		dir := filepath.Clean(m.rec.SavePath)
		f, ok := free[dir]
		if !ok {
			var err error
			if f, err = fsutil.FreeSpace(dir); err != nil {
				continue
			}
			free[dir] = f
		}
		if f >= minFree {
			continue
		}
		e.stopLocked(m)
		m.rec.Paused = true
		m.err = fmt.Sprintf("paused: only %d MiB free in %s - free up space, then resume", f>>20, dir)
		e.dirty = true
		e.log.Warn("disk almost full, download paused", "name", logSafe(m.rec.Name), "free_mib", f>>20)
	}
}

// finalPathLocked is where a finished download goes: the folder it was meant
// for when it was kept in the incomplete-downloads folder, otherwise the
// "move completed downloads" folder.
func (e *Engine) finalPathLocked(m *managed) string {
	if m.rec.FinalPath != "" && filepath.Clean(m.rec.FinalPath) != filepath.Clean(m.rec.SavePath) {
		return m.rec.FinalPath
	}
	return ""
}
