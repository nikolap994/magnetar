package config

import (
	"os"
	"path/filepath"
	"testing"
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
