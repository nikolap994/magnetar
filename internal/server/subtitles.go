package server

import (
	"context"
	"errors"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/nikolap994/magnetar/internal/config"
	"github.com/nikolap994/magnetar/internal/engine"
	"github.com/nikolap994/magnetar/internal/subtitles"
)

var (
	hashRe    = regexp.MustCompile(`^[0-9a-f]{40}$`)
	subNameRe = regexp.MustCompile(`^[a-z-]{1,20}-[0-9]{1,12}\.srt$`)
)

// subDir is where downloaded subtitles for one file are kept (never inside
// the torrent's own folder).
func (s *Server) subDir(r *http.Request) (string, int, error) {
	hash, index, err := fileParams(r)
	if err != nil {
		return "", 0, err
	}
	hash = strings.ToLower(hash)
	if !hashRe.MatchString(hash) || s.dataDir == "" {
		return "", 0, errors.New("unknown torrent")
	}
	return filepath.Join(s.dataDir, "subtitles", hash, strconv.Itoa(index)), index, nil
}

func (s *Server) subClient(st config.Settings) *subtitles.Client {
	return &subtitles.Client{HTTP: s.eng.HTTPClient(), APIKey: st.OpenSubtitlesKey, Username: st.OpenSubtitlesUser,
		Password: st.OpenSubtitlesPassword, UserAgent: "Magnetar v" + engine.Version}
}

func (s *Server) handleSubList(w http.ResponseWriter, r *http.Request) {
	dir, _, err := s.subDir(r)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	out := []string{}
	entries, _ := os.ReadDir(dir)
	for _, e := range entries {
		if subNameRe.MatchString(e.Name()) {
			out = append(out, e.Name())
		}
	}
	sort.Strings(out)
	writeJSON(w, map[string]any{"files": out, "configured": s.store.Get().OpenSubtitlesKey != ""})
}

func (s *Server) handleSubSearch(w http.ResponseWriter, r *http.Request) {
	hash, index, err := fileParams(r)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	d, err := s.eng.Detail(hash)
	if err != nil || index >= len(d.Files) {
		writeErr(w, http.StatusNotFound, errors.New("file not found"))
		return
	}
	q := strings.TrimSpace(r.URL.Query().Get("q"))
	if q == "" {
		q = subtitles.Query(path.Base(d.Files[index].Path))
	}
	if len(q) > 200 {
		q = q[:200]
	}
	// The video's hash finds exact matches, but needs its first and last bytes.
	movieHash := ""
	if p, complete, err := s.eng.FilePath(hash, index); err == nil && complete {
		if f, err := os.Open(p); err == nil {
			movieHash, _ = subtitles.MovieHash(f, d.Files[index].Size)
			f.Close()
		}
	}
	st := s.store.Get()
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	res, err := s.subClient(st).Search(ctx, q, st.SubtitleLanguages, movieHash)
	if err != nil {
		writeErr(w, http.StatusBadGateway, err)
		return
	}
	writeJSON(w, map[string]any{"query": q, "results": res})
}

func (s *Server) handleSubDownload(w http.ResponseWriter, r *http.Request) {
	dir, _, err := s.subDir(r)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	var req struct {
		FileID   int    `json:"fileId"`
		Language string `json:"language"`
	}
	if err := readJSON(r, &req); err != nil || req.FileID <= 0 {
		writeErr(w, http.StatusBadRequest, errors.New("choose a subtitle"))
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 60*time.Second)
	defer cancel()
	data, err := s.subClient(s.store.Get()).Download(ctx, req.FileID)
	if err != nil {
		writeErr(w, http.StatusBadGateway, err)
		return
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	name := subtitles.SafeName(req.Language, req.FileID)
	if err := config.WriteFileAtomic(filepath.Join(dir, name), data, 0o600); err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, map[string]string{"name": name})
}

// handleSubFile serves a saved subtitle as WebVTT for the player.
func (s *Server) handleSubFile(w http.ResponseWriter, r *http.Request) {
	dir, _, err := s.subDir(r)
	name := r.PathValue("name")
	if err != nil || !subNameRe.MatchString(name) {
		http.NotFound(w, r)
		return
	}
	data, err := os.ReadFile(filepath.Join(dir, name))
	if err != nil {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "text/vtt; charset=utf-8")
	w.Header().Set("Content-Security-Policy", "default-src 'none'; sandbox")
	_, _ = w.Write(srtToVTT(data))
}
