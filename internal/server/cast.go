package server

import (
	"context"
	"errors"
	"net/http"
	"time"
)

// CastDevice is a TV (UPnP media renderer) Magnetar can cast a video to.
type CastDevice struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

// Caster discovers TVs on the network and plays a torrent file on one.
type Caster interface {
	CastDevices(ctx context.Context) ([]CastDevice, error)
	Cast(ctx context.Context, device, hash, title string, index int) error
}

func (s *Server) handleCastDevices(w http.ResponseWriter, r *http.Request) {
	if s.cast == nil {
		writeErr(w, http.StatusBadRequest, errors.New("turn on \"Play on your TV\" to cast"))
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 8*time.Second)
	defer cancel()
	devices, err := s.cast.CastDevices(ctx)
	if err != nil {
		writeErr(w, http.StatusBadGateway, err)
		return
	}
	if devices == nil {
		devices = []CastDevice{}
	}
	writeJSON(w, map[string]any{"devices": devices})
}

func (s *Server) handleCast(w http.ResponseWriter, r *http.Request) {
	if s.cast == nil {
		writeErr(w, http.StatusBadRequest, errors.New("turn on \"Play on your TV\" to cast"))
		return
	}
	var req struct {
		Device string `json:"device"`
		Hash   string `json:"hash"`
		Title  string `json:"title"`
		Index  int    `json:"index"`
	}
	if err := readJSON(r, &req); err != nil || req.Device == "" || !hashRe.MatchString(req.Hash) {
		writeErr(w, http.StatusBadRequest, errors.New("choose a TV and a file"))
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 12*time.Second)
	defer cancel()
	if err := s.cast.Cast(ctx, req.Device, req.Hash, req.Title, req.Index); err != nil {
		writeErr(w, http.StatusBadGateway, err)
		return
	}
	writeJSON(w, map[string]bool{"ok": true})
}
