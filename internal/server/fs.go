package server

import (
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
)

type dirListing struct {
	Path   string   `json:"path"`
	Parent string   `json:"parent"`
	Dirs   []string `json:"dirs"`
	Roots  []string `json:"roots"`
	Sep    string   `json:"sep"`
}

// handleDirs lists sub-directories so the UI can offer a folder picker
// (browsers cannot reveal absolute local paths). Only directory names are
// returned, never file names or contents.
func (s *Server) handleDirs(w http.ResponseWriter, r *http.Request) {
	p := strings.TrimSpace(r.URL.Query().Get("path"))
	if p == "" {
		p = s.store.Get().DownloadDir
		if _, err := os.Stat(p); err != nil {
			p, _ = os.UserHomeDir()
		}
	}
	if !filepath.IsAbs(p) {
		writeErr(w, http.StatusBadRequest, errors.New("path must be absolute"))
		return
	}
	p = filepath.Clean(p)
	entries, err := os.ReadDir(p)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	out := dirListing{Path: p, Dirs: []string{}, Roots: roots(), Sep: string(filepath.Separator)}
	if parent := filepath.Dir(p); parent != p {
		out.Parent = parent
	}
	for _, e := range entries {
		name := e.Name()
		if strings.HasPrefix(name, ".") || strings.HasPrefix(name, "$") {
			continue
		}
		isDir := e.IsDir()
		if e.Type()&os.ModeSymlink != 0 {
			if fi, err := os.Stat(filepath.Join(p, name)); err == nil {
				isDir = fi.IsDir()
			}
		}
		if isDir {
			out.Dirs = append(out.Dirs, name)
		}
		if len(out.Dirs) >= 2000 {
			break
		}
	}
	sort.Slice(out.Dirs, func(i, j int) bool { return strings.ToLower(out.Dirs[i]) < strings.ToLower(out.Dirs[j]) })
	writeJSON(w, out)
}

// roots returns quick-access locations: home, Downloads and, on Windows, drives.
func roots() []string {
	var r []string
	if home, err := os.UserHomeDir(); err == nil {
		r = append(r, home)
		if fi, err := os.Stat(filepath.Join(home, "Downloads")); err == nil && fi.IsDir() {
			r = append(r, filepath.Join(home, "Downloads"))
		}
	}
	if runtime.GOOS == "windows" {
		for c := 'C'; c <= 'Z'; c++ {
			d := string(c) + `:\`
			if _, err := os.Stat(d); err == nil {
				r = append(r, d)
			}
		}
	} else {
		r = append(r, "/")
		for _, m := range []string{"/media", "/mnt"} {
			if fi, err := os.Stat(m); err == nil && fi.IsDir() {
				r = append(r, m)
			}
		}
	}
	return r
}
