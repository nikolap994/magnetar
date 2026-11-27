package server

import (
	"errors"
	"net/http"
	"strings"

	"github.com/nikolap994/magnetar/internal/config"
	"github.com/nikolap994/magnetar/internal/engine"
	"github.com/nikolap994/magnetar/internal/fsutil"
	"github.com/nikolap994/magnetar/internal/importer"
)

func (s *Server) handleImportSources(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, map[string]any{"sources": importer.Detect(), "clients": importer.Names})
}

type importReq struct {
	Client string   `json:"client"`
	Dir    string   `json:"dir"`
	Hashes []string `json:"hashes"` // import only these (empty = all)
}

func (s *Server) loadImport(w http.ResponseWriter, r *http.Request) (importReq, []importer.Torrent, bool) {
	var req importReq
	if err := readJSON(r, &req); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return req, nil, false
	}
	if _, ok := importer.Names[req.Client]; !ok {
		writeErr(w, http.StatusBadRequest, errors.New("choose qBittorrent, Transmission or uTorrent"))
		return req, nil, false
	}
	dir, err := fsutil.CleanAbs(req.Dir)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return req, nil, false
	}
	ts, err := importer.Load(req.Client, dir)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return req, nil, false
	}
	return req, ts, true
}

func (s *Server) handleImportScan(w http.ResponseWriter, r *http.Request) {
	_, ts, ok := s.loadImport(w, r)
	if !ok {
		return
	}
	have := map[string]bool{}
	for _, t := range s.eng.List() {
		have[t.Hash] = true
	}
	type row struct {
		importer.Torrent
		Exists bool `json:"exists"`
	}
	out := make([]row, 0, len(ts))
	for _, t := range ts {
		out = append(out, row{Torrent: t, Exists: have[t.Hash]})
	}
	writeJSON(w, out)
}

// handleImport adds the other client's torrents. Missing categories are
// created; existing files are verified and only missing pieces downloaded.
func (s *Server) handleImport(w http.ResponseWriter, r *http.Request) {
	req, ts, ok := s.loadImport(w, r)
	if !ok {
		return
	}
	want := map[string]bool{}
	for _, h := range req.Hashes {
		want[strings.ToLower(h)] = true
	}
	// Create categories the torrents use.
	st := s.store.Get()
	changed := false
	for _, t := range ts {
		if c := t.Category; c != "" && len(c) <= 40 && !st.HasCategory(c) && len(st.Categories) < 100 {
			st.Categories = append(st.Categories, config.Category{Name: c})
			changed = true
		}
	}
	if changed {
		if err := st.Validate(); err == nil {
			if err := s.eng.ApplySettings(st); err == nil {
				s.store.Update(st)
			}
		}
	}
	cur := s.store.Get()
	res := struct {
		Added    int      `json:"added"`
		Existing int      `json:"existing"`
		Failed   []string `json:"failed"`
	}{Failed: []string{}}
	for _, t := range ts {
		if len(want) > 0 && !want[t.Hash] {
			continue
		}
		cat := t.Category
		if !cur.HasCategory(cat) {
			cat = ""
		}
		_, err := s.eng.Add(engine.AddOptions{TorrentFile: t.TorrentFile, SavePath: t.SavePath, Paused: t.Paused,
			FilePriorities: t.FilePriorities, Category: cat, Downloaded: t.Downloaded, Uploaded: t.Uploaded})
		switch {
		case err == nil:
			res.Added++
		case errors.Is(err, engine.ErrExists):
			res.Existing++
		default:
			res.Failed = append(res.Failed, t.Name+": "+err.Error())
		}
	}
	writeJSON(w, res)
}
