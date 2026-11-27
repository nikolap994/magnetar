package server

import (
	"errors"
	"net/http"
	"net/url"
	"strings"

	"github.com/nikolap994/magnetar/internal/engine"
)

func (s *Server) handleCreate(w http.ResponseWriter, r *http.Request) {
	var opts engine.CreateOptions
	if err := readJSON(r, &opts); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	job, err := s.eng.CreateTorrent(opts)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	writeJSON(w, job)
}

func (s *Server) handleCreateStatus(w http.ResponseWriter, r *http.Request) {
	job, _, err := s.eng.CreateStatus(r.PathValue("id"))
	if err != nil {
		writeErr(w, http.StatusNotFound, err)
		return
	}
	writeJSON(w, job)
}

// handleCreateDownload serves the finished .torrent file as a download.
func (s *Server) handleCreateDownload(w http.ResponseWriter, r *http.Request) {
	job, data, err := s.eng.CreateStatus(r.PathValue("id"))
	if err != nil {
		writeErr(w, http.StatusNotFound, err)
		return
	}
	if job.State != "done" || len(data) == 0 {
		writeErr(w, http.StatusConflict, errors.New("the torrent is not ready yet"))
		return
	}
	name := strings.Map(func(r rune) rune {
		if r < 32 || strings.ContainsRune(`/\:*?"<>|`, r) {
			return '_'
		}
		return r
	}, job.Name) + ".torrent"
	h := w.Header()
	h.Set("Content-Type", "application/x-bittorrent")
	h.Set("Content-Disposition", "attachment; filename*=UTF-8''"+url.PathEscape(name))
	h.Set("Content-Security-Policy", "default-src 'none'; sandbox")
	_, _ = w.Write(data)
}

func (s *Server) handleCreateCancel(w http.ResponseWriter, r *http.Request) {
	s.eng.CancelCreate(r.PathValue("id"))
	writeJSON(w, map[string]bool{"ok": true})
}
