// Package server exposes the engine over a hardened local HTTP API and serves the web UI.
package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"github.com/nikolap994/magnetar/internal/auth"
	"github.com/nikolap994/magnetar/internal/config"
	"github.com/nikolap994/magnetar/internal/engine"
	"github.com/nikolap994/magnetar/internal/rss"
	"github.com/nikolap994/magnetar/internal/update"
)

const (
	sessionCookie = "magnetar_session"
	csrfHeader    = "X-Magnetar-Request"
	maxJSONBody   = 1 << 20
)

// Server is the HTTP front end.
type Server struct {
	eng       *engine.Engine
	store     *config.Store
	keys      *auth.Keyring
	limiter   *auth.Limiter
	log       *slog.Logger
	static    fs.FS
	port      int
	shutdown  func()
	register  func() error
	autoStart func(bool) error
	updater   Updater
	showUI    func(magnet string)
	rss       *rss.Manager
	host      string
	restart   func()
	windows   atomic.Int32 // connected app windows (live event streams)
	mux       *http.ServeMux
}

// Options configure optional server behaviour.
type Options struct {
	Port int
	// Shutdown is invoked when the user quits from the UI.
	Shutdown func()
	// Register makes Magnetar the system handler for magnet links and .torrent files.
	Register func() error
	// AutoStart enables or disables starting Magnetar at sign-in.
	AutoStart func(bool) error
	// Updater checks for and installs new versions (optional).
	Updater Updater
	// ShowUI brings the app window to the front (opening one if needed),
	// optionally with the add dialog for a magnet link.
	ShowUI func(magnet string)
	// RSS is the feed downloader (optional).
	RSS *rss.Manager
	// Host is the address the server listens on (to tell when a restart is needed).
	Host string
	// Restart relaunches Magnetar to apply listen-address changes (optional).
	Restart func()
}

// Updater is the subset of the update package used by the API.
type Updater interface {
	Status() update.Status
	Check(ctx context.Context) (update.Status, error)
	Install(ctx context.Context) error
}

// New builds the server.
func New(eng *engine.Engine, store *config.Store, keys *auth.Keyring, static fs.FS, logger *slog.Logger, o Options) *Server {
	s := &Server{eng: eng, store: store, keys: keys, limiter: auth.NewLimiter(), log: logger, static: static,
		port: o.Port, shutdown: o.Shutdown, register: o.Register, autoStart: o.AutoStart, updater: o.Updater, showUI: o.ShowUI, rss: o.RSS, host: o.Host, restart: o.Restart}
	s.routes()
	return s
}

func (s *Server) routes() {
	m := http.NewServeMux()
	files := http.FileServerFS(s.static)
	m.Handle("GET /", files)
	m.HandleFunc("GET /auth/launch", s.handleLaunch)
	m.HandleFunc("GET /api/session", s.handleSession)
	m.HandleFunc("POST /api/login", s.handleLogin)
	m.HandleFunc("POST /api/logout", s.handleLogout)

	m.Handle("GET /api/torrents", s.authed(s.handleList))
	m.Handle("POST /api/torrents", s.authedOrLaunch(s.handleAdd))
	m.Handle("GET /api/torrents/{hash}", s.authed(s.handleDetail))
	m.Handle("DELETE /api/torrents/{hash}", s.authed(s.handleRemove))
	m.Handle("POST /api/torrents/{hash}/files", s.authed(s.handleFiles))
	m.Handle("POST /api/torrents/{hash}/sequential", s.authed(s.handleSequential))
	m.Handle("POST /api/torrents/{hash}/trackers", s.authed(s.handleTrackers))
	m.Handle("GET /api/torrents/{hash}/files/{index}/content", s.authed(s.handleFileContent))
	m.Handle("POST /api/torrents/{hash}/files/{index}/open", s.authed(s.handleFileOpen))
	m.Handle("POST /api/torrents/{hash}/files/{index}/reveal", s.authed(s.handleFileReveal))
	m.Handle("POST /api/torrents/{hash}/open-folder", s.authed(s.handleOpenFolder))
	m.Handle("POST /api/actions", s.authed(s.handleActions))
	m.Handle("GET /api/stats", s.authed(s.handleStats))
	m.Handle("GET /api/statistics", s.authed(func(w http.ResponseWriter, r *http.Request) { writeJSON(w, s.eng.Statistics()) }))
	m.Handle("GET /api/settings", s.authed(s.handleGetSettings))
	m.Handle("PUT /api/settings", s.authed(s.handlePutSettings))
	m.Handle("POST /api/password", s.authed(s.handlePassword))
	m.Handle("POST /api/shutdown", s.authed(s.handleShutdown))
	m.Handle("GET /api/fs/dirs", s.authed(s.handleDirs))
	m.Handle("GET /api/fs/free", s.authed(s.handleFree))
	m.HandleFunc("GET /api/v2/", s.handleQbit)
	m.HandleFunc("POST /api/v2/", s.handleQbit)
	m.Handle("GET /api/remote", s.authed(s.handleRemote))
	m.Handle("POST /api/remote", s.authed(s.handleRemoteSet))
	m.Handle("GET /api/import/sources", s.authed(s.handleImportSources))
	m.Handle("POST /api/import/scan", s.authed(s.handleImportScan))
	m.Handle("POST /api/import", s.authed(s.handleImport))
	m.Handle("POST /api/create", s.authed(s.handleCreate))
	m.Handle("GET /api/create/{id}", s.authed(s.handleCreateStatus))
	m.Handle("GET /api/create/{id}/torrent", s.authed(s.handleCreateDownload))
	m.Handle("DELETE /api/create/{id}", s.authed(s.handleCreateCancel))
	m.Handle("GET /api/rss", s.authed(s.handleRSS))
	m.Handle("POST /api/rss/feeds", s.authed(s.handleRSSAddFeed))
	m.Handle("PUT /api/rss/feeds/{id}", s.authed(s.handleRSSUpdateFeed))
	m.Handle("DELETE /api/rss/feeds/{id}", s.authed(s.handleRSSRemoveFeed))
	m.Handle("POST /api/rss/refresh", s.authed(s.handleRSSRefresh))
	m.Handle("PUT /api/rss/rules", s.authed(s.handleRSSRules))
	m.Handle("POST /api/rss/test", s.authed(s.handleRSSTest))
	m.Handle("POST /api/rss/download", s.authed(s.handleRSSDownload))
	m.Handle("GET /api/net/interfaces", s.authed(func(w http.ResponseWriter, r *http.Request) {
		ifs, err := engine.Interfaces()
		if err != nil {
			writeErr(w, http.StatusInternalServerError, err)
			return
		}
		writeJSON(w, ifs)
	}))
	m.Handle("POST /api/preview", s.authed(s.handlePreview))
	m.Handle("GET /api/preview/{hash}", s.authed(s.handlePreviewStatus))
	m.Handle("DELETE /api/preview/{hash}", s.authed(s.handlePreviewCancel))
	m.Handle("POST /api/system/register", s.authed(s.handleRegister))
	m.Handle("POST /api/ui/open", s.authedOrLaunch(s.handleUIOpen))
	m.Handle("GET /api/update", s.authed(s.handleUpdateStatus))
	m.Handle("POST /api/update/check", s.authed(s.handleUpdateCheck))
	m.Handle("POST /api/update/install", s.authed(s.handleUpdateInstall))
	m.Handle("GET /api/events", s.authed(s.handleEvents))
	m.Handle("GET /api/debug", s.authed(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		s.eng.WriteStatus(w)
	}))
	s.mux = m
}

// Handler returns the root handler with all security middleware applied.
func (s *Server) Handler() http.Handler {
	return s.secure(s.mux)
}

// secure applies headers, Host validation (DNS-rebinding defence) and CSRF checks.
func (s *Server) secure(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("Content-Security-Policy", "default-src 'self'; img-src 'self' data:; style-src 'self'; script-src 'self'; connect-src 'self'; frame-ancestors 'none'; base-uri 'none'; form-action 'self'; object-src 'none'")
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("X-Frame-Options", "DENY")
		h.Set("Referrer-Policy", "no-referrer")
		h.Set("Cross-Origin-Opener-Policy", "same-origin")
		h.Set("Cross-Origin-Resource-Policy", "same-origin")
		h.Set("Permissions-Policy", "camera=(), microphone=(), geolocation=(), interest-cohort=()")
		if strings.HasPrefix(r.URL.Path, "/api/") || strings.HasPrefix(r.URL.Path, "/auth/") {
			h.Set("Cache-Control", "no-store")
		}

		if !s.hostAllowed(r.Host) {
			http.Error(w, "host not allowed", http.StatusMisdirectedRequest)
			return
		}
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			// The qBittorrent-compatible API is used by apps, not browsers: it
			// cannot carry the custom header, so browsers are kept out by
			// rejecting any cross-site request (they always send Origin or
			// Sec-Fetch-Site on cross-site POSTs).
			if strings.HasPrefix(r.URL.Path, "/api/v2/") {
				if fs := r.Header.Get("Sec-Fetch-Site"); fs != "" && fs != "same-origin" && fs != "none" {
					http.Error(w, "cross-origin request blocked", http.StatusForbidden)
					return
				}
				if o := r.Header.Get("Origin"); o != "" && !s.originAllowed(o, r.Host) {
					http.Error(w, "cross-origin request blocked", http.StatusForbidden)
					return
				}
				next.ServeHTTP(w, r)
				return
			}
			// Custom header cannot be set cross-origin without a CORS preflight, which we never grant.
			if r.Header.Get(csrfHeader) != "1" {
				http.Error(w, "missing request header", http.StatusForbidden)
				return
			}
			if o := r.Header.Get("Origin"); o != "" && !s.originAllowed(o, r.Host) {
				http.Error(w, "cross-origin request blocked", http.StatusForbidden)
				return
			}
		}
		next.ServeHTTP(w, r)
	})
}

func (s *Server) hostAllowed(host string) bool {
	h, p, err := net.SplitHostPort(host)
	if err != nil {
		h, p = host, ""
	}
	if p != "" && p != strconv.Itoa(s.port) {
		return false
	}
	h = strings.ToLower(strings.Trim(h, "[]"))
	if h == "localhost" || h == "127.0.0.1" || h == "::1" {
		return true
	}
	st := s.store.Get()
	if !config.IsLoopback(st.WebHost) {
		if ip := net.ParseIP(h); ip != nil {
			return true // direct IP access on LAN; rebinding requires a hostname
		}
	}
	for _, a := range st.AllowedHosts {
		if strings.EqualFold(a, h) {
			return true
		}
	}
	return false
}

func (s *Server) originAllowed(origin, host string) bool {
	u, err := url.Parse(origin)
	if err != nil {
		return false
	}
	return strings.EqualFold(u.Host, host)
}

func (s *Server) isAuthed(r *http.Request) bool {
	c, err := r.Cookie(sessionCookie)
	return err == nil && s.keys.ValidSession(c.Value)
}

func (s *Server) authed(h http.HandlerFunc) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !s.isAuthed(r) {
			writeErr(w, http.StatusUnauthorized, errors.New("not signed in"))
			return
		}
		h(w, r)
	})
}

// authedOrLaunch also accepts a launch token, so a second process (e.g. a
// magnet link opened from the browser) can hand torrents to the running one.
func (s *Server) authedOrLaunch(h http.HandlerFunc) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if s.isAuthed(r) {
			h(w, r)
			return
		}
		if tok, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Launch "); ok && s.keys.ConsumeLaunchToken(tok) {
			h(w, r)
			return
		}
		writeErr(w, http.StatusUnauthorized, errors.New("not signed in"))
	})
}

func (s *Server) setSession(w http.ResponseWriter, r *http.Request) {
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookie,
		Value:    s.keys.NewSession(),
		Path:     "/",
		MaxAge:   int(auth.SessionTTL.Seconds()),
		HttpOnly: true,
		Secure:   r.TLS != nil,
		SameSite: http.SameSiteStrictMode,
	})
}

func (s *Server) handleLaunch(w http.ResponseWriter, r *http.Request) {
	if !s.keys.ConsumeLaunchToken(r.URL.Query().Get("token")) {
		http.Redirect(w, r, "/#expired", http.StatusSeeOther)
		return
	}
	s.setSession(w, r)
	target := "/"
	if m := r.URL.Query().Get("magnet"); m != "" {
		target = "/#popup-add=" + url.QueryEscape(m)
	}
	http.Redirect(w, r, target, http.StatusSeeOther)
}

func (s *Server) handleSession(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, map[string]any{
		"authenticated": s.isAuthed(r),
		"passwordSet":   s.store.Get().PasswordHash != "",
		"version":       engine.Version,
	})
}

func clientIP(r *http.Request) string {
	h, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return h
}

func (s *Server) handleLogin(w http.ResponseWriter, r *http.Request) {
	ip := clientIP(r)
	if !s.limiter.Allowed(ip) {
		writeErr(w, http.StatusTooManyRequests, errors.New("too many attempts, try again in a few minutes"))
		return
	}
	var req struct {
		Password string `json:"password"`
	}
	if err := readJSON(r, &req); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	hash := s.store.Get().PasswordHash
	if hash == "" || !auth.VerifyPassword(hash, req.Password) {
		s.limiter.Fail(ip)
		time.Sleep(300 * time.Millisecond)
		writeErr(w, http.StatusUnauthorized, errors.New("incorrect password"))
		return
	}
	s.limiter.Reset(ip)
	s.setSession(w, r)
	writeJSON(w, map[string]bool{"ok": true})
}

func (s *Server) handleLogout(w http.ResponseWriter, r *http.Request) {
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookie,
		Value:    "",
		Path:     "/",
		MaxAge:   -1,
		HttpOnly: true,
		Secure:   r.TLS != nil, // same as the session cookie; plain-HTTP localhost cannot use Secure
		SameSite: http.SameSiteStrictMode,
	})
	writeJSON(w, map[string]bool{"ok": true})
}

func (s *Server) handleList(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, s.eng.List())
}

func (s *Server) handleStats(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, s.eng.Stats())
}

func (s *Server) handleAdd(w http.ResponseWriter, r *http.Request) {
	ct := r.Header.Get("Content-Type")
	var results []map[string]string
	add := func(opts engine.AddOptions, label string) {
		hash, err := s.eng.Add(opts)
		res := map[string]string{"source": label, "hash": hash}
		if err != nil {
			res["error"] = err.Error()
		}
		results = append(results, res)
	}
	if strings.HasPrefix(ct, "multipart/form-data") {
		r.Body = http.MaxBytesReader(w, r.Body, 64*engine.MaxTorrentFileSize)
		if err := r.ParseMultipartForm(8 << 20); err != nil {
			writeErr(w, http.StatusBadRequest, fmt.Errorf("upload: %w", err))
			return
		}
		defer r.MultipartForm.RemoveAll()
		base := engine.AddOptions{
			SavePath:   r.FormValue("savePath"),
			Category:   r.FormValue("category"),
			Paused:     r.FormValue("paused") == "true",
			Sequential: r.FormValue("sequential") == "true",
		}
		for _, fh := range r.MultipartForm.File["torrents"] {
			if fh.Size > engine.MaxTorrentFileSize {
				results = append(results, map[string]string{"source": fh.Filename, "error": "file too large"})
				continue
			}
			f, err := fh.Open()
			if err != nil {
				results = append(results, map[string]string{"source": fh.Filename, "error": err.Error()})
				continue
			}
			b, err := io.ReadAll(io.LimitReader(f, engine.MaxTorrentFileSize+1))
			f.Close()
			if err != nil {
				results = append(results, map[string]string{"source": fh.Filename, "error": err.Error()})
				continue
			}
			o := base
			o.TorrentFile = b
			add(o, fh.Filename)
		}
		for _, mag := range splitMagnets(r.FormValue("magnets")) {
			o := base
			o.Magnet = mag
			add(o, mag)
		}
	} else {
		var req struct {
			Magnets     []string `json:"magnets"`
			Magnet      string   `json:"magnet"`
			PreviewHash string   `json:"previewHash"`
			Category    string   `json:"category"`
			SkipFiles   []int    `json:"skipFiles"`
			SavePath    string   `json:"savePath"`
			Paused      bool     `json:"paused"`
			Sequential  bool     `json:"sequential"`
		}
		if err := readJSON(r, &req); err != nil {
			writeErr(w, http.StatusBadRequest, err)
			return
		}
		if req.PreviewHash != "" {
			prios := map[int]int{}
			for _, i := range req.SkipFiles {
				prios[i] = engine.PrioritySkip
			}
			add(engine.AddOptions{PreviewHash: req.PreviewHash, FilePriorities: prios, SavePath: req.SavePath, Category: req.Category,
				Paused: req.Paused, Sequential: req.Sequential}, req.PreviewHash)
		}
		mags := req.Magnets
		mags = append(mags, splitMagnets(req.Magnet)...)
		for _, mag := range mags {
			add(engine.AddOptions{Magnet: mag, SavePath: req.SavePath, Category: req.Category, Paused: req.Paused, Sequential: req.Sequential}, mag)
		}
	}
	if len(results) == 0 {
		writeErr(w, http.StatusBadRequest, errors.New("nothing to add"))
		return
	}
	writeJSON(w, results)
}

func splitMagnets(s string) []string {
	var out []string
	for _, line := range strings.Split(s, "\n") {
		if line = strings.TrimSpace(line); line != "" {
			out = append(out, line)
		}
	}
	return out
}

func (s *Server) handleDetail(w http.ResponseWriter, r *http.Request) {
	d, err := s.eng.Detail(r.PathValue("hash"))
	if err != nil {
		writeErr(w, statusFor(err), err)
		return
	}
	writeJSON(w, d)
}

func (s *Server) handleRemove(w http.ResponseWriter, r *http.Request) {
	err := s.eng.Remove(r.URL.Query().Get("deleteData") == "true", r.PathValue("hash"))
	if err != nil {
		writeErr(w, statusFor(err), err)
		return
	}
	writeJSON(w, map[string]bool{"ok": true})
}

func (s *Server) handleActions(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Action     string   `json:"action"`
		Hashes     []string `json:"hashes"`
		DeleteData bool     `json:"deleteData"`
		Category   string   `json:"category"`
		Path       string   `json:"path"`
	}
	if err := readJSON(r, &req); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	if len(req.Hashes) == 0 {
		writeErr(w, http.StatusBadRequest, errors.New("no torrents selected"))
		return
	}
	var err error
	switch req.Action {
	case "pause":
		err = s.eng.Pause(req.Hashes...)
	case "resume":
		err = s.eng.Resume(req.Hashes...)
	case "remove":
		err = s.eng.Remove(req.DeleteData, req.Hashes...)
	case "category":
		err = s.eng.SetCategory(req.Category, req.Hashes...)
	case "move":
		err = s.eng.Move(req.Path, req.Hashes...)
	case "forceStart":
		err = s.eng.ForceStart(true, req.Hashes...)
	case "unforce":
		err = s.eng.ForceStart(false, req.Hashes...)
	case "queueTop", "queueUp", "queueDown", "queueBottom":
		err = s.eng.MoveQueue(strings.ToLower(strings.TrimPrefix(req.Action, "queue")), req.Hashes...)
	case "recheck":
		for _, h := range req.Hashes {
			err = errors.Join(err, s.eng.Recheck(h))
		}
	default:
		writeErr(w, http.StatusBadRequest, fmt.Errorf("unknown action %q", req.Action))
		return
	}
	if err != nil {
		writeErr(w, statusFor(err), err)
		return
	}
	writeJSON(w, map[string]bool{"ok": true})
}

func (s *Server) handleFiles(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Indices  []int `json:"indices"`
		Priority int   `json:"priority"`
	}
	if err := readJSON(r, &req); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	if err := s.eng.SetFilePriority(r.PathValue("hash"), req.Indices, req.Priority); err != nil {
		writeErr(w, statusFor(err), err)
		return
	}
	writeJSON(w, map[string]bool{"ok": true})
}

func (s *Server) handleSequential(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Enabled bool `json:"enabled"`
	}
	if err := readJSON(r, &req); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	if err := s.eng.SetSequential(r.PathValue("hash"), req.Enabled); err != nil {
		writeErr(w, statusFor(err), err)
		return
	}
	writeJSON(w, map[string]bool{"ok": true})
}

func (s *Server) handleTrackers(w http.ResponseWriter, r *http.Request) {
	var req struct {
		URLs []string `json:"urls"`
	}
	if err := readJSON(r, &req); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	if err := s.eng.AddTrackers(r.PathValue("hash"), req.URLs); err != nil {
		writeErr(w, statusFor(err), err)
		return
	}
	writeJSON(w, map[string]bool{"ok": true})
}

// publicSettings hides secrets from the UI.
type publicSettings struct {
	config.Settings
	PasswordHash string `json:"passwordHash,omitempty"`
	PasswordSet  bool   `json:"passwordSet"`
	// The proxy password is never sent back to the browser.
	ProxyPassword    string `json:"proxyPassword,omitempty"`
	ProxyPasswordSet bool   `json:"proxyPasswordSet"`
}

func (s *Server) handleGetSettings(w http.ResponseWriter, r *http.Request) {
	st := s.store.Get()
	writeJSON(w, publicSettings{Settings: st, PasswordSet: st.PasswordHash != "", ProxyPasswordSet: st.ProxyPassword != ""})
}

func (s *Server) handlePutSettings(w http.ResponseWriter, r *http.Request) {
	cur := s.store.Get()
	next := cur
	if err := readJSON(r, &next); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	next.PasswordHash = cur.PasswordHash // only changed via /api/password
	// An empty proxy password keeps the saved one (the UI never receives it);
	// clearing the username or the proxy removes it.
	if next.ProxyPassword == "" && next.ProxyUsername != "" && next.ProxyUsername == cur.ProxyUsername && next.ProxyHost == cur.ProxyHost {
		next.ProxyPassword = cur.ProxyPassword
	}
	if next.ProxyType == "" || next.ProxyUsername == "" {
		next.ProxyPassword = ""
	}
	if err := next.Validate(); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	if next.AutoStart != cur.AutoStart && s.autoStart != nil {
		if err := s.autoStart(next.AutoStart); err != nil {
			writeErr(w, http.StatusInternalServerError, fmt.Errorf("autostart: %w", err))
			return
		}
	}
	if err := s.eng.ApplySettings(next); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	if err := s.store.Update(next); err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	restart := cur.WebHost != next.WebHost || cur.WebPort != next.WebPort
	writeJSON(w, map[string]bool{"ok": true, "restartRequired": restart})
}

func (s *Server) handlePassword(w http.ResponseWriter, r *http.Request) {
	ip := clientIP(r)
	if !s.limiter.Allowed(ip) {
		writeErr(w, http.StatusTooManyRequests, errors.New("too many attempts, try again later"))
		return
	}
	var req struct {
		Current string `json:"current"`
		New     string `json:"new"`
		Remove  bool   `json:"remove"`
	}
	if err := readJSON(r, &req); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	st := s.store.Get()
	if st.PasswordHash != "" && !auth.VerifyPassword(st.PasswordHash, req.Current) {
		s.limiter.Fail(ip)
		writeErr(w, http.StatusUnauthorized, errors.New("current password is incorrect"))
		return
	}
	if req.Remove {
		if !config.IsLoopback(st.WebHost) {
			writeErr(w, http.StatusBadRequest, errors.New("a password is required while the UI is reachable from the network"))
			return
		}
		st.PasswordHash = ""
	} else {
		h, err := auth.HashPassword(req.New)
		if err != nil {
			writeErr(w, http.StatusBadRequest, err)
			return
		}
		st.PasswordHash = h
	}
	if err := s.store.Update(st); err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, map[string]bool{"ok": true})
}

func (s *Server) handleShutdown(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, map[string]bool{"ok": true})
	go func() {
		time.Sleep(200 * time.Millisecond)
		s.shutdown()
	}()
}

// handleEvents streams list + stats snapshots and notifications via Server-Sent Events.
func (s *Server) handleEvents(w http.ResponseWriter, r *http.Request) {
	fl, ok := w.(http.Flusher)
	if !ok {
		writeErr(w, http.StatusInternalServerError, errors.New("streaming unsupported"))
		return
	}
	rc := http.NewResponseController(w)
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")
	events, cancel := s.eng.Subscribe()
	defer cancel()
	s.windows.Add(1)
	defer s.windows.Add(-1)

	send := func(event string, v any) error {
		b, err := json.Marshal(v)
		if err != nil {
			return err
		}
		rc.SetWriteDeadline(time.Now().Add(10 * time.Second))
		if _, err := fmt.Fprintf(w, "event: %s\ndata: %s\n\n", event, b); err != nil {
			return err
		}
		fl.Flush()
		return nil
	}
	snapshot := func() error {
		snap := map[string]any{"torrents": s.eng.List(), "stats": s.eng.Stats()}
		if s.updater != nil {
			snap["update"] = s.updater.Status()
		}
		return send("snapshot", snap)
	}
	if err := snapshot(); err != nil {
		return
	}
	tick := time.NewTicker(time.Second)
	defer tick.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case ev, ok := <-events:
			if !ok {
				return
			}
			if err := send("notify", ev); err != nil {
				return
			}
		case <-tick.C:
			if !s.isAuthed(r) {
				return
			}
			if err := snapshot(); err != nil {
				return
			}
		}
	}
}

// WindowConnected reports whether an app window is open and listening.
func (s *Server) WindowConnected() bool { return s.windows.Load() > 0 }

// handleUIOpen shows the app (one window only), optionally with the add
// dialog for a magnet link. Used by the tray and by a second Magnetar
// process started for a magnet link.
//
// A .torrent file (multipart field "torrent") is read into a preview first,
// so the add dialog shows its files and asks before anything downloads.
func (s *Server) handleUIOpen(w http.ResponseWriter, r *http.Request) {
	if s.showUI == nil {
		writeErr(w, http.StatusNotImplemented, errors.New("no desktop window in this mode"))
		return
	}
	if strings.HasPrefix(r.Header.Get("Content-Type"), "multipart/form-data") {
		r.Body = http.MaxBytesReader(w, r.Body, engine.MaxTorrentFileSize+(1<<20))
		f, _, err := r.FormFile("torrent")
		if err != nil {
			writeErr(w, http.StatusBadRequest, err)
			return
		}
		defer f.Close()
		data, err := io.ReadAll(io.LimitReader(f, engine.MaxTorrentFileSize+1))
		if err != nil || len(data) > engine.MaxTorrentFileSize {
			writeErr(w, http.StatusBadRequest, errors.New("torrent file is too large or unreadable"))
			return
		}
		info, err := s.eng.Preview("", data)
		switch {
		case errors.Is(err, engine.ErrExists):
			s.showUI("")
		case err != nil:
			writeErr(w, http.StatusBadRequest, err)
			return
		default:
			s.showUI(PreviewTarget(info.Hash))
		}
		writeJSON(w, map[string]bool{"ok": true})
		return
	}
	var req struct {
		Magnet string `json:"magnet"`
	}
	if err := readJSON(r, &req); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	if req.Magnet != "" && !strings.HasPrefix(strings.ToLower(req.Magnet), "magnet:?") {
		writeErr(w, http.StatusBadRequest, errors.New("not a magnet link"))
		return
	}
	s.showUI(req.Magnet)
	writeJSON(w, map[string]bool{"ok": true})
}

// PreviewTarget names a prepared preview (from a .torrent file) for the add
// dialog, in place of a magnet link.
func PreviewTarget(hash string) string { return "preview:" + hash }

func (s *Server) handleRegister(w http.ResponseWriter, r *http.Request) {
	if s.register == nil {
		writeErr(w, http.StatusNotImplemented, errors.New("not supported on this system"))
		return
	}
	if err := s.register(); err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, map[string]bool{"ok": true})
}

func (s *Server) handleUpdateStatus(w http.ResponseWriter, r *http.Request) {
	if s.updater == nil {
		writeErr(w, http.StatusNotImplemented, errors.New("updates are not available in this build"))
		return
	}
	writeJSON(w, s.updater.Status())
}

func (s *Server) handleUpdateCheck(w http.ResponseWriter, r *http.Request) {
	if s.updater == nil {
		writeErr(w, http.StatusNotImplemented, errors.New("updates are not available in this build"))
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	st, err := s.updater.Check(ctx)
	if err != nil {
		writeErr(w, http.StatusBadGateway, fmt.Errorf("could not check for updates: %w", err))
		return
	}
	writeJSON(w, st)
}

// handleUpdateInstall starts the download in the background; progress and
// errors are reported through the status in live snapshots.
func (s *Server) handleUpdateInstall(w http.ResponseWriter, r *http.Request) {
	if s.updater == nil {
		writeErr(w, http.StatusNotImplemented, errors.New("updates are not available in this build"))
		return
	}
	st := s.updater.Status()
	if !st.CanInstall {
		writeErr(w, http.StatusConflict, errors.New("no installable update is available"))
		return
	}
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
		defer cancel()
		if err := s.updater.Install(ctx); err != nil {
			s.log.Warn("update failed", "err", err)
		}
	}()
	writeJSON(w, map[string]bool{"ok": true})
}

func statusFor(err error) int {
	switch {
	case errors.Is(err, engine.ErrNotFound):
		return http.StatusNotFound
	case errors.Is(err, engine.ErrExists):
		return http.StatusConflict
	default:
		return http.StatusBadRequest
	}
}

func readJSON(r *http.Request, v any) error {
	dec := json.NewDecoder(io.LimitReader(r.Body, maxJSONBody))
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		return fmt.Errorf("invalid request body: %w", err)
	}
	return nil
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	json.NewEncoder(w).Encode(v)
}

func writeErr(w http.ResponseWriter, code int, err error) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
}

// Serve runs the HTTP server on ln until ctx is cancelled.
func (s *Server) Serve(ctx context.Context, ln net.Listener) error {
	srv := &http.Server{
		Handler:           s.Handler(),
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       5 * time.Minute,
		IdleTimeout:       2 * time.Minute,
		MaxHeaderBytes:    64 << 10,
		ErrorLog:          slog.NewLogLogger(s.log.Handler(), slog.LevelWarn),
	}
	go func() {
		<-ctx.Done()
		sctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		srv.Shutdown(sctx)
	}()
	if err := srv.Serve(ln); !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}
