package server

import (
	"errors"
	"net/http"

	"github.com/nikolap994/magnetar/internal/rss"
)

func (s *Server) rssOr501(w http.ResponseWriter) bool {
	if s.rss == nil {
		writeErr(w, http.StatusNotImplemented, errors.New("RSS is not available"))
		return false
	}
	return true
}

func (s *Server) handleRSS(w http.ResponseWriter, r *http.Request) {
	if s.rssOr501(w) {
		writeJSON(w, s.rss.Get())
	}
}

func (s *Server) handleRSSAddFeed(w http.ResponseWriter, r *http.Request) {
	if !s.rssOr501(w) {
		return
	}
	var req struct {
		URL string `json:"url"`
	}
	if err := readJSON(r, &req); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	f, err := s.rss.AddFeed(r.Context(), req.URL)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	writeJSON(w, f)
}

func (s *Server) handleRSSUpdateFeed(w http.ResponseWriter, r *http.Request) {
	if !s.rssOr501(w) {
		return
	}
	var req struct {
		Title      string `json:"title"`
		RefreshMin int    `json:"refreshMin"`
		Enabled    bool   `json:"enabled"`
	}
	if err := readJSON(r, &req); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	if err := s.rss.UpdateFeed(r.PathValue("id"), req.Title, req.RefreshMin, req.Enabled); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	writeJSON(w, map[string]bool{"ok": true})
}

func (s *Server) handleRSSRemoveFeed(w http.ResponseWriter, r *http.Request) {
	if !s.rssOr501(w) {
		return
	}
	if err := s.rss.RemoveFeed(r.PathValue("id")); err != nil {
		writeErr(w, http.StatusNotFound, err)
		return
	}
	writeJSON(w, map[string]bool{"ok": true})
}

func (s *Server) handleRSSRefresh(w http.ResponseWriter, r *http.Request) {
	if !s.rssOr501(w) {
		return
	}
	var req struct {
		ID string `json:"id"`
	}
	if err := readJSON(r, &req); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	s.rss.Refresh(r.Context(), req.ID)
	writeJSON(w, s.rss.Get())
}

func (s *Server) handleRSSRules(w http.ResponseWriter, r *http.Request) {
	if !s.rssOr501(w) {
		return
	}
	var rules []rss.Rule
	if err := readJSON(r, &rules); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	if err := s.rss.SetRules(r.Context(), rules); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	writeJSON(w, s.rss.Get())
}

func (s *Server) handleRSSTest(w http.ResponseWriter, r *http.Request) {
	if !s.rssOr501(w) {
		return
	}
	var rule rss.Rule
	if err := readJSON(r, &rule); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	titles, err := s.rss.Matches(rule)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	writeJSON(w, titles)
}

func (s *Server) handleRSSDownload(w http.ResponseWriter, r *http.Request) {
	if !s.rssOr501(w) {
		return
	}
	var req struct {
		Feed string `json:"feed"`
		Key  string `json:"key"`
	}
	if err := readJSON(r, &req); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	if err := s.rss.Download(r.Context(), req.Feed, req.Key); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	writeJSON(w, map[string]bool{"ok": true})
}
