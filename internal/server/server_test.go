package server

import (
	"io/fs"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/nikolap994/magnetar/internal/auth"
	"github.com/nikolap994/magnetar/internal/config"
)

func newTestServer(t *testing.T) (*Server, *auth.Keyring) {
	t.Helper()
	dir := t.TempDir()
	store, err := config.Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	keys, err := auth.LoadKeyring(dir)
	if err != nil {
		t.Fatal(err)
	}
	var static fs.FS = fstest.MapFS{"index.html": {Data: []byte("<!doctype html>")}}
	return New(nil, store, keys, static, slog.New(slog.DiscardHandler), Options{Port: 8899, Shutdown: func() {}}), keys
}

func do(s *Server, method, path string, mod func(*http.Request)) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, strings.NewReader("{}"))
	req.Host = "127.0.0.1:8899"
	if mod != nil {
		mod(req)
	}
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, req)
	return rec
}

func TestSecurityHeaders(t *testing.T) {
	s, _ := newTestServer(t)
	rec := do(s, "GET", "/", nil)
	for _, h := range []string{"Content-Security-Policy", "X-Frame-Options", "X-Content-Type-Options", "Referrer-Policy"} {
		if rec.Header().Get(h) == "" {
			t.Errorf("missing %s", h)
		}
	}
}

func TestDNSRebindingBlocked(t *testing.T) {
	s, _ := newTestServer(t)
	for _, host := range []string{"evil.example:8899", "attacker.com", "127.0.0.1:9999"} {
		rec := do(s, "GET", "/api/session", func(r *http.Request) { r.Host = host })
		if rec.Code != http.StatusMisdirectedRequest {
			t.Errorf("host %q: got %d", host, rec.Code)
		}
	}
	for _, host := range []string{"localhost:8899", "127.0.0.1:8899", "[::1]:8899"} {
		rec := do(s, "GET", "/api/session", func(r *http.Request) { r.Host = host })
		if rec.Code != http.StatusOK {
			t.Errorf("host %q: got %d", host, rec.Code)
		}
	}
}

func TestCSRFProtection(t *testing.T) {
	s, _ := newTestServer(t)
	if rec := do(s, "POST", "/api/logout", nil); rec.Code != http.StatusForbidden {
		t.Fatalf("POST without header: got %d", rec.Code)
	}
	rec := do(s, "POST", "/api/logout", func(r *http.Request) {
		r.Header.Set(csrfHeader, "1")
		r.Header.Set("Origin", "https://evil.example")
	})
	if rec.Code != http.StatusForbidden {
		t.Fatalf("cross-origin POST: got %d", rec.Code)
	}
	rec = do(s, "POST", "/api/logout", func(r *http.Request) { r.Header.Set(csrfHeader, "1") })
	if rec.Code != http.StatusOK {
		t.Fatalf("same-origin POST: got %d", rec.Code)
	}
}

func TestAPIRequiresAuth(t *testing.T) {
	s, _ := newTestServer(t)
	for _, p := range []string{"/api/torrents", "/api/settings", "/api/stats", "/api/events", "/api/debug"} {
		if rec := do(s, "GET", p, nil); rec.Code != http.StatusUnauthorized {
			t.Errorf("%s: got %d", p, rec.Code)
		}
	}
}

func TestLaunchFlow(t *testing.T) {
	s, keys := newTestServer(t)
	tok := keys.NewLaunchToken()
	rec := do(s, "GET", "/auth/launch?token="+tok, nil)
	if rec.Code != http.StatusSeeOther || rec.Header().Get("Location") != "/" {
		t.Fatalf("launch: %d %s", rec.Code, rec.Header().Get("Location"))
	}
	cookie := rec.Result().Cookies()
	if len(cookie) != 1 || !cookie[0].HttpOnly || cookie[0].SameSite != http.SameSiteStrictMode {
		t.Fatalf("bad session cookie: %+v", cookie)
	}
	rec = do(s, "GET", "/auth/launch?token="+tok, nil)
	if rec.Header().Get("Location") != "/#expired" {
		t.Fatal("launch token reusable")
	}
	rec = do(s, "GET", "/api/session", func(r *http.Request) { r.AddCookie(cookie[0]) })
	if !strings.Contains(rec.Body.String(), `"authenticated":true`) {
		t.Fatalf("session not authenticated: %s", rec.Body.String())
	}
}

func TestLoginWithoutPasswordFails(t *testing.T) {
	s, _ := newTestServer(t)
	rec := do(s, "POST", "/api/login", func(r *http.Request) {
		r.Header.Set(csrfHeader, "1")
		r.Body = http.NoBody
	})
	if rec.Code == http.StatusOK {
		t.Fatal("login succeeded with no password configured")
	}
}

func TestPreviewTypesAreSafe(t *testing.T) {
	for _, name := range []string{"x.html", "x.htm", "x.svg", "x.js", "x.exe", "x.xhtml", "x"} {
		if PreviewType(name) != "" {
			t.Errorf("%s must never be served for preview", name)
		}
	}
	for name, want := range map[string]string{"Movie.MP4": "video/mp4", "a.flac": "audio/flac", "p.jpg": "image/jpeg", "s.srt": "text/plain; charset=utf-8"} {
		if got := PreviewType(name); got != want {
			t.Errorf("PreviewType(%q) = %q, want %q", name, got, want)
		}
	}
}

func TestSrtToVTT(t *testing.T) {
	in := "\xef\xbb\xbf1\r\n00:00:01,500 --> 00:00:04,000\r\nHello, world\r\n"
	got := string(srtToVTT([]byte(in)))
	want := "WEBVTT\n\n1\n00:00:01.500 --> 00:00:04.000\nHello, world\n"
	if got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

func TestDesktopActionsAreLocalOnly(t *testing.T) {
	r := httptest.NewRequest("POST", "/", nil)
	r.RemoteAddr = "192.168.1.20:5000"
	if localOnly(r) == nil {
		t.Fatal("remote client allowed to open files")
	}
	r.RemoteAddr = "127.0.0.1:5000"
	if localOnly(r) != nil {
		t.Fatal("local client refused")
	}
}
