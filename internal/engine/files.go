package engine

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path"
	"strings"

	"github.com/anacrolix/torrent"

	"github.com/nikolap994/magnetar/internal/fsutil"
)

// ErrNotRunning is returned when a file is requested from a paused torrent
// or one whose metadata is not known yet.
var ErrNotRunning = errors.New("resume the torrent first; its files are not available while it is paused or fetching metadata")

// FileStream is an open, seekable view of one file in a running torrent.
// Reading it prioritises the pieces ahead of the read position, so media can
// play (and seek) while the torrent is still downloading. Only verified
// pieces are returned.
type FileStream struct {
	torrent.Reader
	Name   string // base name of the file
	Length int64
}

// OpenFile opens file index of a running torrent for streaming.
func (e *Engine) OpenFile(ctx context.Context, hash string, index int) (*FileStream, error) {
	e.mu.RLock()
	m, err := e.get(hash)
	var t *torrent.Torrent
	if err == nil {
		t = m.t
	}
	e.mu.RUnlock()
	if err != nil {
		return nil, err
	}
	if t == nil || t.Info() == nil {
		return nil, ErrNotRunning
	}
	files := t.Files()
	if index < 0 || index >= len(files) {
		return nil, ErrNotFound
	}
	f := files[index]
	r := f.NewReader()
	r.SetContext(ctx)
	// Read ahead about one percent of the file, between 2 and 32 MiB, so playback stays smooth.
	r.SetReadahead(min(max(f.Length()/100, 2<<20), 32<<20))
	return &FileStream{Reader: r, Name: path.Base(f.DisplayPath()), Length: f.Length()}, nil
}

// FilePath returns the on-disk path of a file and whether it is complete.
// The path is confined to the torrent's save folder.
func (e *Engine) FilePath(hash string, index int) (string, bool, error) {
	e.mu.RLock()
	defer e.mu.RUnlock()
	m, err := e.get(hash)
	if err != nil {
		return "", false, err
	}
	info := e.infoLocked(m)
	if info == nil {
		return "", false, ErrNotRunning
	}
	files := info.UpvertedFiles()
	if index < 0 || index >= len(files) {
		return "", false, ErrNotFound
	}
	elems := []string{info.BestName()}
	if info.IsDir() {
		elems = append(elems, files[index].BestPath()...)
	}
	p, err := fsutil.Within(m.rec.SavePath, elems...)
	if err != nil {
		return "", false, err
	}
	if fsutil.RegularFileExists(p) {
		complete := m.t == nil || m.t.Files()[index].BytesCompleted() == files[index].Length
		return p, complete, nil
	}
	if fsutil.RegularFileExists(p + ".part") {
		return p + ".part", false, nil
	}
	return "", false, fmt.Errorf("%w: the file is not on disk yet", os.ErrNotExist)
}

// FolderPath returns the folder that holds a torrent's files.
func (e *Engine) FolderPath(hash string) (string, error) {
	e.mu.RLock()
	defer e.mu.RUnlock()
	m, err := e.get(hash)
	if err != nil {
		return "", err
	}
	if info := e.infoLocked(m); info != nil && info.IsDir() {
		if p, err := fsutil.Within(m.rec.SavePath, info.BestName()); err == nil {
			if fi, err := os.Stat(p); err == nil && fi.IsDir() {
				return p, nil
			}
		}
	}
	return fsutil.CleanAbs(m.rec.SavePath)
}

// IsExecutable reports whether a file name looks like a program or script,
// which Magnetar never opens directly (torrent content is untrusted).
func IsExecutable(name string) bool {
	ext := strings.ToLower(path.Ext(strings.TrimSuffix(name, ".part")))
	switch ext {
	case ".exe", ".com", ".bat", ".cmd", ".msi", ".msp", ".msix", ".appx", ".scr", ".pif", ".cpl",
		".ps1", ".psm1", ".vbs", ".vbe", ".js", ".jse", ".wsf", ".wsh", ".hta", ".lnk", ".url",
		".reg", ".jar", ".dll", ".sys", ".inf", ".sh", ".bash", ".zsh", ".run", ".bin", ".appimage",
		".desktop", ".deb", ".rpm", ".py", ".pl", ".rb", ".command", ".app", ".dmg", ".pkg",
		".iso", ".img", ".vhd", ".vhdx", ".html", ".htm", ".svg", ".xhtml", ".mht", ".chm":
		return true
	}
	return false
}
