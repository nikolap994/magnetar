package server

import (
	"context"
	"net/http"
	"sync"
	"time"

	"github.com/nikolap994/magnetar/internal/geoip"
)

type geoManager struct {
	db  *geoip.DB
	mu  sync.Mutex
	on  bool
	dl  bool // downloading
	err string
}

// applyGeo turns peer-country lookups on or off to match the setting. When on,
// it loads the cached database at once and downloads a fresh one in the
// background if it is missing or old.
func (s *Server) applyGeo(enabled bool) {
	if s.geo == nil || s.dataDir == "" || s.eng == nil {
		return
	}
	g := s.geo
	g.mu.Lock()
	g.on = enabled
	g.err = ""
	g.mu.Unlock()
	if !enabled {
		s.eng.SetCountryLookup(nil)
		return
	}
	if g.db.Ready() || g.db.Load(s.dataDir) == nil {
		s.eng.SetCountryLookup(g.db.Lookup)
	}
	if geoip.Stale(s.dataDir) {
		go s.downloadGeo()
	}
}

func (s *Server) downloadGeo() {
	g := s.geo
	g.mu.Lock()
	if g.dl {
		g.mu.Unlock()
		return
	}
	g.dl = true
	g.mu.Unlock()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	err := g.db.Download(ctx, s.eng.HTTPClient(), s.dataDir)
	g.mu.Lock()
	g.dl = false
	if err != nil {
		g.err = err.Error()
	}
	on := g.on
	g.mu.Unlock()
	if err == nil && on {
		s.eng.SetCountryLookup(g.db.Lookup)
	}
}

func (s *Server) handleGeoIP(w http.ResponseWriter, r *http.Request) {
	if s.geo == nil {
		writeJSON(w, map[string]any{"enabled": false})
		return
	}
	g := s.geo
	g.mu.Lock()
	out := map[string]any{"enabled": g.on, "ready": g.db.Ready(), "downloading": g.dl, "error": g.err}
	g.mu.Unlock()
	writeJSON(w, out)
}
