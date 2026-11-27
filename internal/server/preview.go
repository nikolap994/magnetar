package server

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"

	"github.com/nikolap994/magnetar/internal/engine"
	"github.com/nikolap994/magnetar/internal/fsutil"
)

// handlePreview starts fetching metadata for a magnet link (JSON {"magnet"})
// or reads a single uploaded .torrent file (multipart field "torrent").
func (s *Server) handlePreview(w http.ResponseWriter, r *http.Request) {
	var magnet string
	var file []byte
	if strings.HasPrefix(r.Header.Get("Content-Type"), "multipart/form-data") {
		r.Body = http.MaxBytesReader(w, r.Body, engine.MaxTorrentFileSize+(1<<20))
		f, _, err := r.FormFile("torrent")
		if err != nil {
			writeErr(w, http.StatusBadRequest, err)
			return
		}
		defer f.Close()
		file, err = io.ReadAll(io.LimitReader(f, engine.MaxTorrentFileSize+1))
		if err != nil || len(file) > engine.MaxTorrentFileSize {
			writeErr(w, http.StatusBadRequest, errors.New("torrent file is too large or unreadable"))
			return
		}
	} else {
		var req struct {
			Magnet string `json:"magnet"`
		}
		if err := readJSON(r, &req); err != nil {
			writeErr(w, http.StatusBadRequest, err)
			return
		}
		magnet = req.Magnet
	}
	info, err := s.eng.Preview(magnet, file)
	if errors.Is(err, engine.ErrExists) {
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.WriteHeader(http.StatusConflict)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "this torrent is already in your list", "hash": info.Hash})
		return
	}
	if err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	writeJSON(w, info)
}

func (s *Server) handlePreviewStatus(w http.ResponseWriter, r *http.Request) {
	info, err := s.eng.PreviewStatus(r.PathValue("hash"))
	if err != nil {
		writeErr(w, http.StatusNotFound, err)
		return
	}
	writeJSON(w, info)
}

func (s *Server) handlePreviewCancel(w http.ResponseWriter, r *http.Request) {
	s.eng.CancelPreview(r.PathValue("hash"))
	writeJSON(w, map[string]bool{"ok": true})
}

// handleFree reports free disk space for a (possibly not yet existing) folder.
func (s *Server) handleFree(w http.ResponseWriter, r *http.Request) {
	free, err := fsutil.FreeSpace(r.URL.Query().Get("path"))
	if err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	writeJSON(w, map[string]uint64{"free": free})
}
