package server

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"path"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/nikolap994/magnetar/internal/desktop"
	"github.com/nikolap994/magnetar/internal/engine"
)

// Kinds of files that can be previewed inside Magnetar. Anything else is
// never served: torrent content is untrusted and must not be able to run as
// a web page in Magnetar's origin.
var previewTypes = map[string]string{
	// video
	".mp4": "video/mp4", ".m4v": "video/mp4", ".webm": "video/webm", ".mkv": "video/x-matroska",
	".mov": "video/quicktime", ".ogv": "video/ogg",
	// audio
	".mp3": "audio/mpeg", ".m4a": "audio/mp4", ".aac": "audio/aac", ".flac": "audio/flac",
	".ogg": "audio/ogg", ".oga": "audio/ogg", ".opus": "audio/ogg", ".wav": "audio/wav",
	// images (no SVG: it can contain scripts)
	".png": "image/png", ".jpg": "image/jpeg", ".jpeg": "image/jpeg", ".gif": "image/gif",
	".webp": "image/webp", ".avif": "image/avif", ".bmp": "image/bmp",
	// text
	".txt": "text/plain; charset=utf-8", ".nfo": "text/plain; charset=utf-8", ".md": "text/plain; charset=utf-8",
	".log": "text/plain; charset=utf-8", ".srt": "text/plain; charset=utf-8", ".vtt": "text/vtt; charset=utf-8",
	".ass": "text/plain; charset=utf-8", ".ssa": "text/plain; charset=utf-8", ".cue": "text/plain; charset=utf-8",
	".sfv": "text/plain; charset=utf-8", ".md5": "text/plain; charset=utf-8", ".sha256": "text/plain; charset=utf-8",
}

// PreviewType returns the MIME type used to preview a file, or "" if it cannot be previewed.
func PreviewType(name string) string {
	return previewTypes[strings.ToLower(path.Ext(name))]
}

func fileParams(r *http.Request) (string, int, error) {
	i, err := strconv.Atoi(r.PathValue("index"))
	if err != nil || i < 0 {
		return "", 0, errors.New("invalid file index")
	}
	return r.PathValue("hash"), i, nil
}

// handleFileContent streams a file for the built-in player and quick look,
// with HTTP range support so seeking works while downloading.
func (s *Server) handleFileContent(w http.ResponseWriter, r *http.Request) {
	hash, index, err := fileParams(r)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	f, err := s.eng.OpenFile(r.Context(), hash, index)
	if err != nil {
		writeErr(w, fileStatus(err), err)
		return
	}
	defer f.Close()

	ctype := PreviewType(f.Name)
	if ctype == "" {
		writeErr(w, http.StatusUnsupportedMediaType, errors.New("this kind of file cannot be previewed; use Open or Show in folder"))
		return
	}
	h := w.Header()
	// Even if a browser were tricked into rendering this response as a page,
	// it is sandboxed and may not load or run anything.
	h.Set("Content-Security-Policy", "default-src 'none'; sandbox")
	h.Set("Content-Disposition", "inline; filename*=UTF-8''"+url.PathEscape(f.Name))

	if r.URL.Query().Get("format") == "vtt" && strings.EqualFold(path.Ext(f.Name), ".srt") {
		data, err := io.ReadAll(io.LimitReader(f, 4<<20))
		if err != nil {
			writeErr(w, http.StatusInternalServerError, err)
			return
		}
		h.Set("Content-Type", "text/vtt; charset=utf-8")
		_, _ = w.Write(srtToVTT(data))
		return
	}
	h.Set("Content-Type", ctype)
	http.ServeContent(w, r, f.Name, time.Time{}, f)
}

var srtTiming = regexp.MustCompile(`(\d{2}:\d{2}:\d{2}),(\d{3})`)

// srtToVTT converts SubRip subtitles to WebVTT, which browsers understand.
func srtToVTT(srt []byte) []byte {
	srt = bytes.TrimPrefix(srt, []byte("\xef\xbb\xbf"))
	var out bytes.Buffer
	out.WriteString("WEBVTT\n\n")
	sc := bufio.NewScanner(bytes.NewReader(srt))
	sc.Buffer(make([]byte, 64<<10), 1<<20)
	for sc.Scan() {
		line := strings.TrimRight(sc.Text(), "\r")
		if strings.Contains(line, "-->") {
			line = srtTiming.ReplaceAllString(line, "$1.$2")
		}
		out.WriteString(line)
		out.WriteByte('\n')
	}
	return out.Bytes()
}

// localOnly allows desktop actions only for requests from this computer.
func localOnly(r *http.Request) error {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return errors.New("unknown client")
	}
	if ip := net.ParseIP(host); ip == nil || !ip.IsLoopback() {
		return errors.New("files can only be opened on the computer running Magnetar")
	}
	return nil
}

func (s *Server) handleFileOpen(w http.ResponseWriter, r *http.Request) {
	if err := localOnly(r); err != nil {
		writeErr(w, http.StatusForbidden, err)
		return
	}
	hash, index, err := fileParams(r)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	p, complete, err := s.eng.FilePath(hash, index)
	if err != nil {
		writeErr(w, fileStatus(err), err)
		return
	}
	if engine.IsExecutable(p) {
		writeErr(w, http.StatusForbidden, errors.New("for your safety, programs, scripts and web pages from torrents are not opened directly; use Show in folder and check the file first"))
		return
	}
	if !complete {
		writeErr(w, http.StatusConflict, errors.New("the file is still downloading; use Play to stream it in Magnetar"))
		return
	}
	if err := desktop.OpenPath(p); err != nil {
		writeErr(w, http.StatusInternalServerError, fmt.Errorf("open: %w", err))
		return
	}
	writeJSON(w, map[string]bool{"ok": true})
}

func (s *Server) handleFileReveal(w http.ResponseWriter, r *http.Request) {
	if err := localOnly(r); err != nil {
		writeErr(w, http.StatusForbidden, err)
		return
	}
	hash, index, err := fileParams(r)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	p, _, err := s.eng.FilePath(hash, index)
	if err != nil {
		writeErr(w, fileStatus(err), err)
		return
	}
	if err := desktop.RevealPath(p); err != nil {
		writeErr(w, http.StatusInternalServerError, fmt.Errorf("show in folder: %w", err))
		return
	}
	writeJSON(w, map[string]bool{"ok": true})
}

func (s *Server) handleOpenFolder(w http.ResponseWriter, r *http.Request) {
	if err := localOnly(r); err != nil {
		writeErr(w, http.StatusForbidden, err)
		return
	}
	p, err := s.eng.FolderPath(r.PathValue("hash"))
	if err != nil {
		writeErr(w, fileStatus(err), err)
		return
	}
	if err := desktop.OpenPath(p); err != nil {
		writeErr(w, http.StatusInternalServerError, fmt.Errorf("open folder: %w", err))
		return
	}
	writeJSON(w, map[string]bool{"ok": true})
}

func fileStatus(err error) int {
	switch {
	case errors.Is(err, engine.ErrNotFound):
		return http.StatusNotFound
	case errors.Is(err, engine.ErrNotRunning):
		return http.StatusConflict
	default:
		return http.StatusBadRequest
	}
}
