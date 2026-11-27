package server

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/nikolap994/magnetar/internal/engine"
	"github.com/nikolap994/magnetar/internal/notify"
)

func (s *Server) handleFinish(w http.ResponseWriter, r *http.Request) {
	out := map[string]any{"action": ""}
	if s.finish != nil {
		if a, at := s.finish.Pending(); a != "" {
			out["action"], out["at"] = a, at
		}
	}
	writeJSON(w, out)
}

func (s *Server) handleFinishCancel(w http.ResponseWriter, r *http.Request) {
	if s.finish != nil {
		s.finish.Cancel()
		s.eng.Broadcast(engine.Event{Type: "finish", Message: ""})
	}
	writeJSON(w, map[string]bool{"ok": true})
}

// handleNotifyTest sends a test message with the saved notification settings.
func (s *Server) handleNotifyTest(w http.ResponseWriter, r *http.Request) {
	st := s.store.Get()
	if !notify.Configured(st) {
		writeErr(w, http.StatusBadRequest, errors.New("set up ntfy, Telegram or a webhook first and save the settings"))
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	if err := notify.Send(ctx, s.eng.HTTPClient(), st, "Magnetar", "Notifications work. You'll hear from Magnetar when downloads finish."); err != nil {
		writeErr(w, http.StatusBadGateway, err)
		return
	}
	writeJSON(w, map[string]bool{"ok": true})
}
