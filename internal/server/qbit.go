package server

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/nikolap994/magnetar/internal/auth"
	"github.com/nikolap994/magnetar/internal/config"
	"github.com/nikolap994/magnetar/internal/engine"
	"github.com/nikolap994/magnetar/internal/logsafe"
	"github.com/nikolap994/magnetar/internal/rss"
)

// A subset of the qBittorrent Web API (v2), so apps such as Sonarr, Radarr,
// Lidarr, Prowlarr and autobrr can use Magnetar as their download client.
// It is off unless enabled in Settings and always needs the web password.

const (
	qbitCookie     = "SID"
	qbitAppVersion = "v4.6.7"
	qbitAPIVersion = "2.9.3"
)

func (s *Server) handleQbit(w http.ResponseWriter, r *http.Request) {
	st := s.store.Get()
	if !st.QBitAPI {
		http.NotFound(w, r)
		return
	}
	path := strings.TrimPrefix(r.URL.Path, "/api/v2/")
	if path == "auth/login" {
		s.qbitLogin(w, r, st)
		return
	}
	if c, err := r.Cookie(qbitCookie); err != nil || !s.keys.ValidSession(c.Value) {
		http.Error(w, "Forbidden", http.StatusForbidden)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, engine.MaxTorrentFileSize*8)
	switch path {
	case "auth/logout":
		//nolint:gosec // Secure is set over HTTPS; plain-HTTP local access cannot use it
		http.SetCookie(w, &http.Cookie{Name: qbitCookie, Value: "", Path: "/", MaxAge: -1, HttpOnly: true,
			SameSite: http.SameSiteStrictMode, Secure: r.TLS != nil})
		qbitOK(w)
	case "app/version":
		_, _ = io.WriteString(w, qbitAppVersion)
	case "app/webapiVersion":
		_, _ = io.WriteString(w, qbitAPIVersion)
	case "app/buildInfo":
		writeJSON(w, map[string]any{"qt": "", "libtorrent": "", "boost": "", "openssl": "", "bitness": 64, "magnetar": engine.Version})
	case "app/preferences":
		writeJSON(w, qbitPreferences(st))
	case "app/defaultSavePath":
		_, _ = io.WriteString(w, st.DownloadDir)
	case "transfer/info":
		g := s.eng.Stats()
		status := "connected"
		if g.Connection == "firewalled" {
			status = "firewalled"
		} else if !g.Online || g.Connection == "offline" || g.Connection == "vpn-down" {
			status = "disconnected"
		}
		writeJSON(w, map[string]any{"dl_info_speed": g.DownRate, "up_info_speed": g.UpRate, "dl_rate_limit": g.DownLimit,
			"up_rate_limit": g.UpLimit, "dht_nodes": g.DHTNodes, "connection_status": status})
	case "torrents/categories":
		out := map[string]any{}
		for _, c := range st.Categories {
			out[c.Name] = map[string]string{"name": c.Name, "savePath": c.SavePath}
		}
		writeJSON(w, out)
	case "torrents/createCategory", "torrents/editCategory":
		s.qbitCategory(w, r, path == "torrents/editCategory")
	case "torrents/removeCategories":
		s.qbitRemoveCategories(w, r)
	case "torrents/tags":
		writeJSON(w, []string{})
	case "torrents/createTags", "torrents/addTags", "torrents/removeTags", "torrents/deleteTags",
		"torrents/setShareLimits", "torrents/toggleFirstLastPiecePrio", "torrents/setSuperSeeding", "torrents/setAutoManagement":
		qbitOK(w) // accepted; Magnetar has no equivalent
	case "torrents/info":
		s.qbitInfo(w, r)
	case "torrents/properties":
		s.qbitProperties(w, r)
	case "torrents/files":
		s.qbitFiles(w, r)
	case "torrents/trackers":
		s.qbitTrackers(w, r)
	case "torrents/add":
		s.qbitAdd(w, r, st)
	default:
		s.qbitAction(w, r, path)
	}
}

func qbitOK(w http.ResponseWriter) { _, _ = io.WriteString(w, "Ok.") }

func qbitFail(w http.ResponseWriter, code int, err error) { http.Error(w, err.Error(), code) }

func (s *Server) qbitLogin(w http.ResponseWriter, r *http.Request, st config.Settings) {
	ip := clientIP(r)
	if !s.limiter.Allowed(ip) {
		http.Error(w, "Your IP address has been banned after too many failed authentication attempts.", http.StatusForbidden)
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, "Fails.", http.StatusBadRequest)
		return
	}
	if st.PasswordHash == "" || !auth.VerifyPassword(st.PasswordHash, r.PostForm.Get("password")) {
		s.limiter.Fail(ip)
		time.Sleep(300 * time.Millisecond)
		_, _ = io.WriteString(w, "Fails.")
		return
	}
	s.limiter.Reset(ip)
	//nolint:gosec // Secure is set over HTTPS; plain-HTTP local access cannot use it
	http.SetCookie(w, &http.Cookie{Name: qbitCookie, Value: s.keys.NewSession(), Path: "/", HttpOnly: true,
		SameSite: http.SameSiteStrictMode, Secure: r.TLS != nil})
	qbitOK(w)
}

func qbitPreferences(st config.Settings) map[string]any {
	return map[string]any{
		"save_path":                st.DownloadDir,
		"temp_path_enabled":        false,
		"queueing_enabled":         st.QueueEnabled,
		"max_active_downloads":     limitOrMinus(st.MaxActiveDownloads),
		"max_active_uploads":       limitOrMinus(st.MaxActiveUploads),
		"max_active_torrents":      limitOrMinus(st.MaxActiveTorrents),
		"max_ratio_enabled":        st.SeedRatioLimit > 0,
		"max_ratio":                st.SeedRatioLimit,
		"max_seeding_time_enabled": st.SeedTimeLimitMin > 0,
		"max_seeding_time":         st.SeedTimeLimitMin,
		"max_ratio_act":            0, // pause
		"dht":                      st.EnableDHT,
		"pex":                      st.EnablePEX,
		"lsd":                      st.EnableLSD,
		"listen_port":              st.ListenPort,
		"dl_limit":                 st.DownloadLimitKBps * 1024,
		"up_limit":                 st.UploadLimitKBps * 1024,
		"auto_tmm_enabled":         false,
		"create_subfolder_enabled": true,
		"start_paused_enabled":     false,
	}
}

func limitOrMinus(n int) int {
	if n <= 0 {
		return -1
	}
	return n
}

// qbitHashes parses "hash1|hash2" or "all".
func (s *Server) qbitHashes(v string) []string {
	if v == "all" {
		var out []string
		for _, t := range s.eng.List() {
			out = append(out, t.Hash)
		}
		return out
	}
	var out []string
	for _, h := range strings.Split(v, "|") {
		if h = strings.ToLower(strings.TrimSpace(h)); len(h) == 40 {
			out = append(out, h)
		}
	}
	return out
}

func qbitState(t engine.Summary) string {
	done := t.Progress >= 1
	switch t.State {
	case engine.StatePaused:
		if done {
			return "pausedUP"
		}
		return "pausedDL"
	case engine.StateQueued:
		if done {
			return "queuedUP"
		}
		return "queuedDL"
	case engine.StateMetadata:
		return "metaDL"
	case engine.StateChecking:
		if done {
			return "checkingUP"
		}
		return "checkingDL"
	case engine.StateMoving:
		return "moving"
	case engine.StateError:
		return "error"
	case engine.StateSeeding:
		if t.Forced {
			return "forcedUP"
		}
		if t.UpRate > 0 {
			return "uploading"
		}
		return "stalledUP"
	case engine.StateCompleted:
		return "pausedUP"
	case engine.StateStalled:
		return "stalledDL"
	default:
		if t.Forced {
			return "forcedDL"
		}
		return "downloading"
	}
}

func qbitTorrent(t engine.Summary) map[string]any {
	var completion int64 = -1
	var seeding int64
	if t.CompletedAt != nil {
		completion = t.CompletedAt.Unix()
		seeding = int64(time.Since(*t.CompletedAt).Seconds())
	}
	eta := t.ETA
	if eta < 0 {
		eta = 8640000 // qBittorrent's "infinity"
	}
	return map[string]any{
		"hash": t.Hash, "infohash_v1": t.Hash, "name": t.Name, "size": t.Size, "total_size": t.Size,
		"progress": t.Progress, "dlspeed": t.DownRate, "upspeed": t.UpRate, "eta": eta,
		"state": qbitState(t), "category": t.Category, "tags": "",
		"save_path": t.SavePath, "content_path": filepath.Join(t.SavePath, t.Name), "download_path": "",
		"ratio": t.Ratio, "added_on": t.AddedAt.Unix(), "completion_on": completion,
		"amount_left": max(t.Size-t.Completed, 0), "completed": t.Completed,
		"downloaded": t.Downloaded, "uploaded": t.Uploaded,
		"num_seeds": t.Seeds, "num_leechs": max(t.Peers-t.Seeds, 0), "num_complete": t.Seeds, "num_incomplete": max(t.KnownPeers-t.Seeds, 0),
		"seeding_time": seeding, "ratio_limit": -2, "seeding_time_limit": -2, "max_ratio": -1, "max_seeding_time": -1,
		"priority": t.QueuePos, "seq_dl": t.Sequential, "f_l_piece_prio": false, "force_start": t.Forced,
		"auto_tmm": false, "super_seeding": false, "dl_limit": -1, "up_limit": -1,
	}
}

func (s *Server) qbitInfo(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	if r.Method == http.MethodPost {
		_ = r.ParseForm() // a malformed form leaves the fields empty, which is rejected below
		q = r.Form
	}
	var want map[string]bool
	if v := q.Get("hashes"); v != "" {
		want = map[string]bool{}
		for _, h := range s.qbitHashes(v) {
			want[h] = true
		}
	}
	cat, hasCat := q["category"]
	filter := q.Get("filter")
	out := []map[string]any{}
	for _, t := range s.eng.List() {
		if want != nil && !want[t.Hash] {
			continue
		}
		if hasCat && cat[0] != t.Category {
			continue
		}
		st := qbitState(t)
		switch filter {
		case "downloading":
			if t.Progress >= 1 {
				continue
			}
		case "seeding", "completed":
			if t.Progress < 1 {
				continue
			}
		case "paused", "stopped":
			if !strings.HasPrefix(st, "paused") {
				continue
			}
		case "active":
			if t.DownRate == 0 && t.UpRate == 0 {
				continue
			}
		case "errored":
			if st != "error" {
				continue
			}
		}
		out = append(out, qbitTorrent(t))
	}
	writeJSON(w, out)
}

func (s *Server) qbitDetail(w http.ResponseWriter, r *http.Request) (engine.Detail, bool) {
	d, err := s.eng.Detail(strings.ToLower(r.URL.Query().Get("hash")))
	if err != nil {
		http.Error(w, "Not Found", http.StatusNotFound)
		return d, false
	}
	return d, true
}

func (s *Server) qbitProperties(w http.ResponseWriter, r *http.Request) {
	d, ok := s.qbitDetail(w, r)
	if !ok {
		return
	}
	t := qbitTorrent(d.Summary)
	t["piece_size"] = d.PieceSize
	t["pieces_num"] = d.NumPieces
	t["comment"] = d.Comment
	t["total_downloaded"] = d.Downloaded
	t["total_uploaded"] = d.Uploaded
	t["dl_speed"] = d.DownRate
	t["up_speed"] = d.UpRate
	t["share_ratio"] = d.Ratio
	t["addition_date"] = d.AddedAt.Unix()
	t["completion_date"] = t["completion_on"]
	t["is_private"] = d.Private
	writeJSON(w, t)
}

func (s *Server) qbitFiles(w http.ResponseWriter, r *http.Request) {
	d, ok := s.qbitDetail(w, r)
	if !ok {
		return
	}
	out := []map[string]any{}
	for _, f := range d.Files {
		prio := map[int]int{engine.PrioritySkip: 0, engine.PriorityNormal: 1, engine.PriorityHigh: 6}[f.Priority]
		name := f.Path
		if strings.Contains(name, "/") || len(d.Files) > 1 {
			name = d.Name + "/" + f.Path
		}
		out = append(out, map[string]any{"index": f.Index, "name": name, "size": f.Size, "progress": f.Progress,
			"priority": prio, "is_seed": f.Progress >= 1, "availability": -1})
	}
	writeJSON(w, out)
}

func (s *Server) qbitTrackers(w http.ResponseWriter, r *http.Request) {
	d, ok := s.qbitDetail(w, r)
	if !ok {
		return
	}
	out := []map[string]any{}
	for i, u := range d.Trackers {
		out = append(out, map[string]any{"url": u, "status": 2, "tier": i, "num_peers": -1, "num_seeds": -1, "num_leeches": -1, "msg": ""})
	}
	writeJSON(w, out)
}

func formBool(v string) bool { return v == "true" || v == "1" }

func (s *Server) qbitAdd(w http.ResponseWriter, r *http.Request, st config.Settings) {
	if err := r.ParseMultipartForm(32 << 20); err != nil && !errors.Is(err, http.ErrNotMultipart) {
		qbitFail(w, http.StatusBadRequest, err)
		return
	}
	if r.PostForm == nil {
		_ = r.ParseForm() // a malformed form leaves the fields empty, which is rejected below
	}
	f := r.Form
	cat := strings.TrimSpace(f.Get("category"))
	if cat != "" && !st.HasCategory(cat) {
		if err := s.addCategory(cat, ""); err != nil {
			qbitFail(w, http.StatusBadRequest, err)
			return
		}
	}
	base := engine.AddOptions{
		SavePath:   strings.TrimSpace(f.Get("savepath")),
		Category:   cat,
		Paused:     formBool(f.Get("paused")) || formBool(f.Get("stopped")),
		Sequential: formBool(f.Get("sequentialDownload")),
	}
	var adds []engine.AddOptions
	for _, line := range strings.Split(f.Get("urls"), "\n") {
		u := strings.TrimSpace(line)
		if u == "" {
			continue
		}
		o := base
		if strings.HasPrefix(strings.ToLower(u), "magnet:") || len(u) == 40 {
			o.Magnet = u
		} else {
			ctx, cancel := context.WithTimeout(r.Context(), 60*time.Second)
			data, err := rss.Fetch(ctx, s.eng.HTTPClient(), u, engine.MaxTorrentFileSize)
			cancel()
			if err != nil {
				qbitFail(w, http.StatusBadRequest, fmt.Errorf("download %s: %w", u, err))
				return
			}
			o.TorrentFile = data
		}
		adds = append(adds, o)
	}
	if r.MultipartForm != nil {
		for _, fh := range r.MultipartForm.File["torrents"] {
			file, err := fh.Open()
			if err != nil {
				continue
			}
			data, err := io.ReadAll(io.LimitReader(file, engine.MaxTorrentFileSize+1))
			file.Close()
			if err != nil || len(data) > engine.MaxTorrentFileSize {
				qbitFail(w, http.StatusBadRequest, errors.New("torrent file is too large"))
				return
			}
			o := base
			o.TorrentFile = data
			adds = append(adds, o)
		}
	}
	if len(adds) == 0 {
		_, _ = io.WriteString(w, "Fails.")
		return
	}
	failed := 0
	for _, o := range adds {
		if _, err := s.eng.Add(o); err != nil && !errors.Is(err, engine.ErrExists) {
			failed++
			s.log.Warn("add from external app", "err", logsafe.Err(err))
		}
	}
	if failed == len(adds) {
		_, _ = io.WriteString(w, "Fails.")
		return
	}
	qbitOK(w)
}

func (s *Server) addCategory(name, savePath string) error {
	st := s.store.Get()
	if st.HasCategory(name) {
		return nil
	}
	st.Categories = append(st.Categories, config.Category{Name: name, SavePath: savePath})
	if err := st.Validate(); err != nil {
		return err
	}
	if err := s.eng.ApplySettings(st); err != nil {
		return err
	}
	return s.store.Update(st)
}

func (s *Server) qbitCategory(w http.ResponseWriter, r *http.Request, edit bool) {
	_ = r.ParseForm() // a malformed form leaves the fields empty, which is rejected below
	name := strings.TrimSpace(r.Form.Get("category"))
	save := strings.TrimSpace(r.Form.Get("savePath"))
	if name == "" {
		qbitFail(w, http.StatusBadRequest, errors.New("category name is empty"))
		return
	}
	st := s.store.Get()
	if !edit {
		if st.HasCategory(name) {
			qbitFail(w, http.StatusConflict, errors.New("category already exists"))
			return
		}
		if err := s.addCategory(name, save); err != nil {
			qbitFail(w, http.StatusBadRequest, err)
			return
		}
		qbitOK(w)
		return
	}
	found := false
	for i := range st.Categories {
		if st.Categories[i].Name == name {
			st.Categories[i].SavePath, found = save, true
		}
	}
	if !found {
		qbitFail(w, http.StatusConflict, errors.New("category does not exist"))
		return
	}
	if err := st.Validate(); err == nil && s.eng.ApplySettings(st) == nil {
		if err := s.store.Update(st); err != nil {
			qbitFail(w, http.StatusInternalServerError, err)
			return
		}
	}
	qbitOK(w)
}

func (s *Server) qbitRemoveCategories(w http.ResponseWriter, r *http.Request) {
	_ = r.ParseForm() // a malformed form leaves the fields empty, which is rejected below
	drop := map[string]bool{}
	for _, c := range strings.Split(r.Form.Get("categories"), "\n") {
		drop[strings.TrimSpace(c)] = true
	}
	st := s.store.Get()
	var keep []config.Category
	for _, c := range st.Categories {
		if !drop[c.Name] {
			keep = append(keep, c)
		}
	}
	st.Categories = keep
	if err := st.Validate(); err == nil && s.eng.ApplySettings(st) == nil {
		if err := s.store.Update(st); err != nil {
			qbitFail(w, http.StatusInternalServerError, err)
			return
		}
	}
	qbitOK(w)
}

// qbitAction handles the torrent actions that take a "hashes" form field.
func (s *Server) qbitAction(w http.ResponseWriter, r *http.Request, path string) {
	if r.Method != http.MethodPost {
		http.NotFound(w, r)
		return
	}
	_ = r.ParseForm() // a malformed form leaves the fields empty, which is rejected below
	f := r.Form
	hashes := s.qbitHashes(f.Get("hashes"))
	var err error
	switch path {
	case "torrents/pause", "torrents/stop":
		err = s.eng.Pause(hashes...)
	case "torrents/resume", "torrents/start":
		err = s.eng.Resume(hashes...)
	case "torrents/delete":
		err = s.eng.Remove(formBool(f.Get("deleteFiles")), hashes...)
	case "torrents/recheck":
		for _, h := range hashes {
			err = errors.Join(err, s.eng.Recheck(h))
		}
	case "torrents/setCategory":
		cat := strings.TrimSpace(f.Get("category"))
		if cat != "" && !s.store.Get().HasCategory(cat) {
			qbitFail(w, http.StatusConflict, errors.New("category does not exist"))
			return
		}
		err = s.eng.SetCategory(cat, hashes...)
	case "torrents/setLocation":
		err = s.eng.Move(f.Get("location"), hashes...)
	case "torrents/setForceStart":
		err = s.eng.ForceStart(formBool(f.Get("value")), hashes...)
	case "torrents/topPrio":
		err = s.eng.MoveQueue(engine.QueueTop, hashes...)
	case "torrents/bottomPrio":
		err = s.eng.MoveQueue(engine.QueueBottom, hashes...)
	case "torrents/increasePrio":
		err = s.eng.MoveQueue(engine.QueueUp, hashes...)
	case "torrents/decreasePrio":
		err = s.eng.MoveQueue(engine.QueueDown, hashes...)
	case "torrents/toggleSequentialDownload":
		for _, h := range hashes {
			if d, derr := s.eng.Detail(h); derr == nil {
				err = errors.Join(err, s.eng.SetSequential(h, !d.Sequential))
			}
		}
	case "torrents/filePrio":
		hash := strings.ToLower(f.Get("hash"))
		var idx []int
		for _, v := range strings.Split(f.Get("id"), "|") {
			if i, e := strconv.Atoi(v); e == nil {
				idx = append(idx, i)
			}
		}
		p, _ := strconv.Atoi(f.Get("priority"))
		prio := engine.PriorityNormal
		switch {
		case p == 0:
			prio = engine.PrioritySkip
		case p >= 6:
			prio = engine.PriorityHigh
		}
		err = s.eng.SetFilePriority(hash, idx, prio)
	default:
		http.NotFound(w, r)
		return
	}
	if errors.Is(err, engine.ErrNotFound) {
		http.Error(w, "Not Found", http.StatusNotFound)
		return
	}
	if err != nil {
		qbitFail(w, http.StatusBadRequest, err)
		return
	}
	qbitOK(w)
}
