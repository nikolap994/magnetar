package server

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"time"
)

func (s *Server) handleRemoveTracker(w http.ResponseWriter, r *http.Request) {
	var req struct {
		URL string `json:"url"`
	}
	if err := readJSON(r, &req); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	if err := s.eng.RemoveTracker(r.PathValue("hash"), req.URL); err != nil {
		writeErr(w, statusFor(err), err)
		return
	}
	writeJSON(w, map[string]bool{"ok": true})
}

func (s *Server) handleAddPeer(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Addr string `json:"addr"`
	}
	if err := readJSON(r, &req); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	if err := s.eng.AddPeer(r.PathValue("hash"), req.Addr); err != nil {
		writeErr(w, statusFor(err), err)
		return
	}
	writeJSON(w, map[string]bool{"ok": true})
}

func (s *Server) handleBan(w http.ResponseWriter, r *http.Request) {
	var req struct {
		IPs []string `json:"ips"`
	}
	if err := readJSON(r, &req); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	if err := s.eng.BanPeers(req.IPs...); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	writeJSON(w, map[string]bool{"ok": true})
}

func (s *Server) handleClearBans(w http.ResponseWriter, r *http.Request) {
	if err := s.eng.ClearBans(); err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, map[string]bool{"ok": true})
}

// publicTrackerList is a widely used, regularly updated list of public
// trackers; it is only fetched when the user asks for it.
const publicTrackerList = "https://raw.githubusercontent.com/ngosang/trackerslist/master/trackers_best.txt"

func (s *Server) handleFetchTrackerList(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, publicTrackerList, nil)
	resp, err := s.eng.HTTPClient().Do(req)
	if err != nil {
		writeErr(w, http.StatusBadGateway, errors.New("could not download the tracker list"))
		return
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		writeErr(w, http.StatusBadGateway, errors.New("could not download the tracker list"))
		return
	}
	b, _ := io.ReadAll(io.LimitReader(resp.Body, 256<<10))
	var list []string
	for _, line := range strings.Split(string(b), "\n") {
		if u := strings.TrimSpace(line); u != "" && !strings.ContainsAny(u, " \t") && strings.Contains(u, "://") {
			list = append(list, u)
		}
	}
	writeJSON(w, map[string]any{"trackers": list})
}
