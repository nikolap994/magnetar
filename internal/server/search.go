package server

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/nikolap994/magnetar/internal/engine"
	"github.com/nikolap994/magnetar/internal/search"
)

func (s *Server) handleSearch(w http.ResponseWriter, r *http.Request) {
	q := strings.TrimSpace(r.URL.Query().Get("q"))
	if q == "" || len(q) > 200 {
		writeErr(w, http.StatusBadRequest, errors.New("enter what to search for"))
		return
	}
	st := s.store.Get()
	enabled := 0
	for _, ix := range st.Indexers {
		if ix.Enabled {
			enabled++
		}
	}
	if enabled == 0 {
		writeErr(w, http.StatusConflict, errors.New("add a search indexer in Settings → Search first"))
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	writeJSON(w, search.Search(ctx, s.eng.HTTPClient(), st.Indexers, q))
}

// handleSearchOpen turns a search result into something the add dialog can
// show: a magnet link, or a preview prepared from the downloaded .torrent.
func (s *Server) handleSearchOpen(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Link string `json:"link"`
	}
	if err := readJSON(r, &req); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 60*time.Second)
	defer cancel()
	data, magnet, err := search.Fetch(ctx, s.eng.HTTPClient(), req.Link)
	if err != nil {
		writeErr(w, http.StatusBadGateway, err)
		return
	}
	if magnet != "" {
		writeJSON(w, map[string]string{"magnet": magnet})
		return
	}
	info, err := s.eng.Preview("", data)
	if errors.Is(err, engine.ErrExists) {
		writeJSON(w, map[string]string{"exists": info.Hash})
		return
	}
	if err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	writeJSON(w, map[string]string{"preview": info.Hash})
}
