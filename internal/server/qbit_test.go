package server

import (
	"encoding/json"
	"io/fs"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/nikolap994/magnetar/internal/auth"
	"github.com/nikolap994/magnetar/internal/config"
	"github.com/nikolap994/magnetar/internal/engine"
)

func qbitServer(t *testing.T, enabled bool) *Server {
	t.Helper()
	dir := t.TempDir()
	store, err := config.Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	s := store.Get()
	s.DownloadDir, s.ListenPort = t.TempDir(), 0
	s.EnableDHT, s.EnableUPnP, s.EnableUTP = false, false, false
	h, _ := auth.HashPassword("sonarr-secret")
	s.PasswordHash, s.QBitAPI = h, enabled
	if err := store.Update(s); err != nil {
		t.Fatal(err)
	}
	eng, err := engine.New(s, t.TempDir(), slog.New(slog.DiscardHandler))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { eng.Close() })
	keys, _ := auth.LoadKeyring(dir)
	var static fs.FS = fstest.MapFS{"index.html": {Data: []byte("<!doctype html>")}}
	return New(eng, store, keys, static, slog.New(slog.DiscardHandler), Options{Port: 8899, Shutdown: func() {}})
}

func qbitDo(s *Server, method, path string, form url.Values, cookie *http.Cookie, mod func(*http.Request)) *httptest.ResponseRecorder {
	var body *strings.Reader
	if form != nil {
		body = strings.NewReader(form.Encode())
	} else {
		body = strings.NewReader("")
	}
	req := httptest.NewRequest(method, path, body)
	req.Host = "127.0.0.1:8899"
	if form != nil {
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	}
	if cookie != nil {
		req.AddCookie(cookie)
	}
	if mod != nil {
		mod(req)
	}
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, req)
	return rec
}

func TestQbitAPIOffByDefault(t *testing.T) {
	s := qbitServer(t, false)
	if rec := qbitDo(s, "POST", "/api/v2/auth/login", url.Values{"username": {"admin"}, "password": {"sonarr-secret"}}, nil, nil); rec.Code != http.StatusNotFound {
		t.Fatalf("got %d", rec.Code)
	}
}

func TestQbitAPIFlow(t *testing.T) {
	s := qbitServer(t, true)
	if rec := qbitDo(s, "GET", "/api/v2/app/version", nil, nil, nil); rec.Code != http.StatusForbidden {
		t.Fatalf("unauthenticated: %d", rec.Code)
	}
	if rec := qbitDo(s, "POST", "/api/v2/auth/login", url.Values{"username": {"admin"}, "password": {"wrong"}}, nil, nil); rec.Body.String() != "Fails." {
		t.Fatalf("wrong password: %q", rec.Body)
	}
	// A browser on another site must not be able to log in or add torrents.
	if rec := qbitDo(s, "POST", "/api/v2/auth/login", url.Values{"password": {"sonarr-secret"}}, nil, func(r *http.Request) {
		r.Header.Set("Origin", "https://evil.example")
	}); rec.Code != http.StatusForbidden {
		t.Fatalf("cross-origin login: %d", rec.Code)
	}
	rec := qbitDo(s, "POST", "/api/v2/auth/login", url.Values{"username": {"admin"}, "password": {"sonarr-secret"}}, nil, nil)
	if rec.Body.String() != "Ok." {
		t.Fatalf("login: %d %q", rec.Code, rec.Body)
	}
	var sid *http.Cookie
	for _, c := range rec.Result().Cookies() {
		if c.Name == "SID" {
			sid = c
		}
	}
	if sid == nil {
		t.Fatal("no SID cookie")
	}
	if rec := qbitDo(s, "GET", "/api/v2/app/webapiVersion", nil, sid, nil); rec.Body.String() != qbitAPIVersion {
		t.Fatalf("version: %q", rec.Body)
	}
	if rec := qbitDo(s, "POST", "/api/v2/torrents/createCategory", url.Values{"category": {"tv-sonarr"}, "savePath": {""}}, sid, nil); rec.Body.String() != "Ok." {
		t.Fatalf("createCategory: %q", rec.Body)
	}
	hash := strings.Repeat("ab", 20)
	rec = qbitDo(s, "POST", "/api/v2/torrents/add", url.Values{"urls": {"magnet:?xt=urn:btih:" + hash + "&dn=Show.S01E01"}, "category": {"tv-sonarr"}, "paused": {"true"}}, sid, nil)
	if rec.Body.String() != "Ok." {
		t.Fatalf("add: %d %q", rec.Code, rec.Body)
	}
	rec = qbitDo(s, "GET", "/api/v2/torrents/info?category=tv-sonarr", nil, sid, nil)
	var list []map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &list); err != nil || len(list) != 1 {
		t.Fatalf("info: %s", rec.Body)
	}
	if list[0]["hash"] != hash || list[0]["state"] != "pausedDL" || list[0]["category"] != "tv-sonarr" {
		t.Fatalf("torrent: %v", list[0])
	}
	if rec := qbitDo(s, "POST", "/api/v2/torrents/delete", url.Values{"hashes": {hash}, "deleteFiles": {"false"}}, sid, nil); rec.Body.String() != "Ok." {
		t.Fatalf("delete: %q", rec.Body)
	}
	if n := len(s.eng.List()); n != 0 {
		t.Fatalf("still %d torrents", n)
	}
	var cats map[string]any
	json.Unmarshal(qbitDo(s, "GET", "/api/v2/torrents/categories", nil, sid, nil).Body.Bytes(), &cats)
	if _, ok := cats["tv-sonarr"]; !ok {
		t.Fatalf("categories: %v", cats)
	}
}
