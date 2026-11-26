package engine

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"

	"github.com/anacrolix/torrent/metainfo"

	"github.com/nikolap994/magnetar/internal/fsutil"
)

// SetCategory assigns a category ("" removes it) without moving any files.
func (e *Engine) SetCategory(category string, hashes ...string) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	if category != "" && !e.settings.HasCategory(category) {
		return fmt.Errorf("unknown category %q", category)
	}
	for _, h := range hashes {
		m, err := e.get(h)
		if err != nil {
			return err
		}
		m.rec.Category = category
	}
	e.dirty = true
	return nil
}

// Move relocates torrents' data to dest in the background ("Set location").
// Running torrents are paused while their files move and resumed afterwards.
func (e *Engine) Move(dest string, hashes ...string) error {
	dest, err := fsutil.CleanAbs(dest)
	if err != nil {
		return fmt.Errorf("destination: %w", err)
	}
	if err := os.MkdirAll(dest, 0o755); err != nil {
		return fmt.Errorf("create destination: %w", err)
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	for _, h := range hashes {
		m, err := e.get(h)
		if err != nil {
			return err
		}
		if m.moving {
			continue
		}
		if filepath.Clean(m.rec.SavePath) == dest {
			continue
		}
		info := e.infoLocked(m)
		if info == nil { // no metadata yet, so nothing on disk to move
			m.rec.SavePath = dest
			e.restartIfRunningLocked(m)
			e.dirty = true
			continue
		}
		if other := e.sharesDataLockedExcept(m, m.rec.SavePath, info.BestName()); other != "" {
			return fmt.Errorf("cannot move %q: its files are also used by %q", m.rec.Name, other)
		}
		wasRunning := m.t != nil
		e.stopLocked(m)
		m.moving = true
		m.err = ""
		src := m.rec.SavePath
		e.wg.Add(1)
		go e.finishMove(m, src, dest, info, wasRunning)
	}
	return nil
}

func (e *Engine) finishMove(m *managed, src, dest string, info *metainfo.Info, wasRunning bool) {
	defer e.wg.Done()
	err := moveTorrentData(src, dest, info)
	e.mu.Lock()
	defer e.mu.Unlock()
	m.moving = false
	if err != nil {
		m.err = "move failed: " + err.Error()
		e.log.Warn("move torrent", "name", m.rec.Name, "err", err)
	} else {
		m.rec.SavePath = dest
		e.publish(Event{Type: "moved", Hash: m.rec.InfoHash, Name: m.rec.Name, Message: dest})
	}
	e.dirty = true
	if wasRunning && e.cl != nil && e.torrents[m.rec.InfoHash] == m {
		if err := e.startLocked(m); err != nil {
			m.err = err.Error()
		}
	}
}

func (e *Engine) restartIfRunningLocked(m *managed) {
	if m.t == nil {
		return
	}
	e.stopLocked(m)
	if err := e.startLocked(m); err != nil {
		m.err = err.Error()
	}
}

// infoLocked returns the torrent's metadata from the live torrent or the saved .torrent file.
func (e *Engine) infoLocked(m *managed) *metainfo.Info {
	if m.t != nil && m.t.Info() != nil {
		return m.t.Info()
	}
	if mi, err := metainfo.LoadFromFile(e.metainfoPath(m.rec.InfoHash)); err == nil {
		if i, err := mi.UnmarshalInfo(); err == nil {
			return &i
		}
	}
	return nil
}

func (e *Engine) sharesDataLockedExcept(self *managed, savePath, root string) string {
	for _, o := range e.torrents {
		if o == self || filepath.Clean(o.rec.SavePath) != filepath.Clean(savePath) {
			continue
		}
		if info := e.infoLocked(o); info != nil && info.BestName() == root {
			return o.rec.Name
		}
	}
	return ""
}

// moveTorrentData moves a torrent's file or folder (and a single file's
// .part sibling) from src to dest, copying across devices when needed.
func moveTorrentData(src, dest string, info *metainfo.Info) error {
	name := info.BestName()
	from, err := fsutil.Within(src, name)
	if err != nil || from == filepath.Clean(src) {
		return fmt.Errorf("unsafe torrent name %q", name)
	}
	to, err := fsutil.Within(dest, name)
	if err != nil || to == filepath.Clean(dest) {
		return fmt.Errorf("unsafe torrent name %q", name)
	}
	items := [][2]string{{from, to}}
	if !info.IsDir() {
		items = append(items, [2]string{from + ".part", to + ".part"})
	}
	moved := 0
	for _, it := range items {
		if _, err := os.Lstat(it[0]); errors.Is(err, fs.ErrNotExist) {
			continue
		}
		if _, err := os.Lstat(it[1]); err == nil {
			return fmt.Errorf("%q already exists in the destination", filepath.Base(it[1]))
		}
		if err := moveItem(it[0], it[1]); err != nil {
			return err
		}
		moved++
	}
	_ = moved // nothing on disk yet is fine: the torrent just downloads to the new place
	return nil
}

func moveItem(from, to string) error {
	err := os.Rename(from, to)
	if err == nil || !isCrossDevice(err) {
		return err
	}
	if err := copyTree(from, to); err != nil {
		os.RemoveAll(to)
		return err
	}
	return os.RemoveAll(from)
}

func isCrossDevice(err error) bool {
	var le *os.LinkError
	if !errors.As(err, &le) {
		return false
	}
	if errors.Is(le.Err, syscall.EXDEV) {
		return true
	}
	// ERROR_NOT_SAME_DEVICE (17) on Windows.
	return runtime.GOOS == "windows" && strings.Contains(strings.ToLower(le.Err.Error()), "different disk drive")
}

// copyTree copies a file or directory without following symlinks.
func copyTree(from, to string) error {
	return filepath.WalkDir(from, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(from, p)
		if err != nil {
			return err
		}
		target := filepath.Join(to, rel)
		info, err := d.Info()
		if err != nil {
			return err
		}
		switch {
		case d.IsDir():
			return os.MkdirAll(target, info.Mode().Perm()|0o700)
		case info.Mode().IsRegular():
			return copyFile(p, target, info.Mode().Perm())
		default:
			return nil // skip symlinks and special files
		}
	})
}

func copyFile(from, to string, perm fs.FileMode) error {
	in, err := os.Open(from)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(to, os.O_CREATE|os.O_EXCL|os.O_WRONLY, perm|0o600)
	if err != nil {
		return err
	}
	_, cerr := io.Copy(out, in)
	if err := out.Close(); cerr == nil {
		cerr = err
	}
	return cerr
}
