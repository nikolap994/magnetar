package server

import (
	"errors"
	"io"
	"net/http"
	"time"

	"github.com/nikolap994/magnetar/internal/backup"
	"github.com/nikolap994/magnetar/internal/engine"
)

const maxBackupUpload = 512 << 20

// handleBackup downloads settings, torrents, RSS rules, statistics and bans
// as one zip. Only on this computer: the file contains saved passwords.
func (s *Server) handleBackup(w http.ResponseWriter, r *http.Request) {
	if err := localOnly(r); err != nil {
		writeErr(w, http.StatusForbidden, errors.New("backups can only be made on the computer running Magnetar"))
		return
	}
	if s.cfgDir == "" {
		writeErr(w, http.StatusNotImplemented, errors.New("backups are not available"))
		return
	}
	name := "magnetar-backup-" + time.Now().Format("2006-01-02") + ".zip"
	w.Header().Set("Content-Type", "application/zip")
	w.Header().Set("Content-Disposition", `attachment; filename="`+name+`"`)
	if err := backup.Export(w, s.cfgDir, s.dataDir, engine.Version); err != nil {
		s.log.Warn("backup", "err", err)
	}
}

// handleRestore stages an uploaded backup and restarts Magnetar to apply it.
func (s *Server) handleRestore(w http.ResponseWriter, r *http.Request) {
	if err := localOnly(r); err != nil {
		writeErr(w, http.StatusForbidden, errors.New("backups can only be restored on the computer running Magnetar"))
		return
	}
	if s.dataDir == "" {
		writeErr(w, http.StatusNotImplemented, errors.New("backups are not available"))
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxBackupUpload)
	f, _, err := r.FormFile("backup")
	if err != nil {
		writeErr(w, http.StatusBadRequest, errors.New("choose a backup file"))
		return
	}
	defer f.Close()
	data, err := io.ReadAll(f)
	if err != nil {
		writeErr(w, http.StatusBadRequest, errors.New("the backup is too large or unreadable"))
		return
	}
	version, n, err := backup.Stage(data, s.dataDir)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	// From here on nothing may overwrite the files the backup restores.
	s.eng.Freeze()
	if s.rss != nil {
		s.rss.Freeze()
	}
	// From here on nothing may overwrite the files the backup restores.
	s.eng.Freeze()
	if s.rss != nil {
		s.rss.Freeze()
	}
	restarting := s.restart != nil
	writeJSON(w, map[string]any{"version": version, "torrents": n, "restarting": restarting})
	if restarting {
		go func() {
			time.Sleep(500 * time.Millisecond)
			s.restart()
		}()
	}
}
