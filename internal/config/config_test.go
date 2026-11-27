package config

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestDefaultsValid(t *testing.T) {
	s := Defaults()
	if err := s.Validate(); err != nil {
		t.Fatal(err)
	}
	if !IsLoopback(s.WebHost) {
		t.Fatal("web UI must default to loopback")
	}
}

func TestRemoteRequiresPassword(t *testing.T) {
	s := Defaults()
	s.WebHost = "0.0.0.0"
	if err := s.Validate(); err == nil {
		t.Fatal("expected error exposing UI without a password")
	}
	s.PasswordHash = "$argon2id$..."
	if err := s.Validate(); err != nil {
		t.Fatal(err)
	}
}

func TestValidateRejects(t *testing.T) {
	cases := map[string]func(*Settings){
		"relative dir":  func(s *Settings) { s.DownloadDir = "downloads" },
		"bad port":      func(s *Settings) { s.ListenPort = 70000 },
		"bad bind":      func(s *Settings) { s.BindAddress = "not-an-ip" },
		"bad crypto":    func(s *Settings) { s.Encryption = "rot13" },
		"neg limit":     func(s *Settings) { s.DownloadLimitKBps = -1 },
		"zero conns":    func(s *Settings) { s.MaxConnsPerTorrent = 0 },
		"rel blocklist": func(s *Settings) { s.BlocklistPath = "list.p2p" },
		"web hostname":  func(s *Settings) { s.WebHost = "example.com" },
	}
	for name, mut := range cases {
		t.Run(name, func(t *testing.T) {
			s := Defaults()
			mut(&s)
			if err := s.Validate(); err == nil {
				t.Fatal("expected validation error")
			}
		})
	}
}

func TestStorePersistsWithPrivatePerms(t *testing.T) {
	dir := t.TempDir()
	st, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	s := st.Get()
	s.ListenPort = 40000
	if err := st.Update(s); err != nil {
		t.Fatal(err)
	}
	st2, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	if st2.Get().ListenPort != 40000 {
		t.Fatal("setting not persisted")
	}
	fi, err := os.Stat(filepath.Join(dir, "settings.json"))
	if err != nil {
		t.Fatal(err)
	}
	if os.PathSeparator == '/' && fi.Mode().Perm() != 0o600 {
		t.Fatalf("settings.json has mode %v, want 0600", fi.Mode().Perm())
	}
}

func TestAltSchedule(t *testing.T) {
	s := Defaults() // 09:00–17:00, Monday–Friday
	s.AltScheduleEnabled = true
	at := func(day time.Weekday, hh, mm int) time.Time {
		// 2026-11-22 is a Sunday.
		return time.Date(2026, 11, 22+int(day), hh, mm, 0, 0, time.Local)
	}
	cases := []struct {
		t    time.Time
		want bool
	}{
		{at(time.Monday, 9, 0), true},
		{at(time.Monday, 16, 59), true},
		{at(time.Monday, 17, 0), false},
		{at(time.Monday, 8, 59), false},
		{at(time.Saturday, 12, 0), false},
	}
	for _, c := range cases {
		if got := s.InAltSchedule(c.t); got != c.want {
			t.Errorf("%v: got %v, want %v", c.t, got, c.want)
		}
	}
	// Overnight: Friday 22:00 → Saturday 06:00, only Friday selected.
	s.AltScheduleFrom, s.AltScheduleTo, s.AltScheduleDays = "22:00", "06:00", 1<<uint(time.Friday)
	if !s.InAltSchedule(at(time.Friday, 23, 0)) || !s.InAltSchedule(at(time.Saturday, 5, 59)) {
		t.Error("overnight window not applied")
	}
	if s.InAltSchedule(at(time.Saturday, 23, 0)) || s.InAltSchedule(at(time.Friday, 5, 0)) {
		t.Error("overnight window applied on the wrong day")
	}
}

func TestParseClock(t *testing.T) {
	for in, want := range map[string]int{"00:00": 0, "09:30": 570, "23:59": 1439} {
		if got, ok := ParseClock(in); !ok || got != want {
			t.Errorf("ParseClock(%q) = %d, %v", in, got, ok)
		}
	}
	for _, bad := range []string{"", "9:30", "24:00", "12:60", "ab:cd", "12-30"} {
		if _, ok := ParseClock(bad); ok {
			t.Errorf("ParseClock(%q) accepted", bad)
		}
	}
}

func TestProxyAndInterfaceValidation(t *testing.T) {
	s := Defaults()
	s.ProxyType, s.ProxyHost, s.ProxyPort = ProxyHTTP, "proxy.local", 3128
	s.ProxyUsername, s.ProxyPassword, s.ProxyPeers = "u", "p@ss", true
	if err := s.Validate(); err != nil {
		t.Fatal(err)
	}
	if s.ProxyPeers {
		t.Fatal("HTTP proxies cannot carry peer connections")
	}
	if u := s.ProxyURL(); u.String() != "http://u:p%40ss@proxy.local:3128" {
		t.Fatalf("proxy url %s", u)
	}
	for _, bad := range []func(*Settings){
		func(s *Settings) { s.ProxyType = "ftp" },
		func(s *Settings) { s.ProxyHost = "user@host" },
		func(s *Settings) { s.ProxyPort = 0 },
		func(s *Settings) { s.BindInterface, s.BindAddress = "wg0", "10.0.0.2" },
		func(s *Settings) { s.BindInterface = "wg\n0" },
	} {
		c := s
		bad(&c)
		if err := c.Validate(); err == nil {
			t.Fatalf("expected validation error for %+v", c)
		}
	}
}
