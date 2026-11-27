package main

import (
	"fmt"
	"os"
	"slices"
	"strconv"
	"strings"

	"github.com/nikolap994/magnetar/internal/auth"
	"github.com/nikolap994/magnetar/internal/config"
)

// applyEnv lets containers and service managers configure Magnetar without
// the UI:
//
//	MAGNETAR_PASSWORD      web password (only set when none exists yet)
//	MAGNETAR_WEB_HOST      listen address of the web interface, e.g. 0.0.0.0
//	MAGNETAR_WEB_PORT      web interface port
//	MAGNETAR_DOWNLOAD_DIR  default download folder (only replaces the built-in default)
//	MAGNETAR_PEER_PORT     BitTorrent listening port
//	MAGNETAR_ALLOWED_HOSTS host names the interface is reached by (reverse proxy), comma-separated
func applyEnv(store *config.Store) error {
	s := store.Get()
	changed := false
	if pw := os.Getenv("MAGNETAR_PASSWORD"); pw != "" && s.PasswordHash == "" {
		if len(pw) < 8 {
			return fmt.Errorf("MAGNETAR_PASSWORD must have at least 8 characters")
		}
		h, err := auth.HashPassword(pw)
		if err != nil {
			return err
		}
		s.PasswordHash, changed = h, true
	}
	if v := os.Getenv("MAGNETAR_WEB_HOST"); v != "" && v != s.WebHost {
		s.WebHost, changed = v, true
	}
	if v := os.Getenv("MAGNETAR_DOWNLOAD_DIR"); v != "" && s.DownloadDir == config.Defaults().DownloadDir {
		s.DownloadDir, changed = v, true
	}
	for name, dst := range map[string]*int{"MAGNETAR_WEB_PORT": &s.WebPort, "MAGNETAR_PEER_PORT": &s.ListenPort} {
		if v := os.Getenv(name); v != "" {
			n, err := strconv.Atoi(v)
			if err != nil {
				return fmt.Errorf("%s: %w", name, err)
			}
			if *dst != n {
				*dst, changed = n, true
			}
		}
	}
	if v := os.Getenv("MAGNETAR_ALLOWED_HOSTS"); v != "" {
		var hosts []string
		for _, h := range strings.Split(v, ",") {
			if h = strings.TrimSpace(h); h != "" {
				hosts = append(hosts, h)
			}
		}
		if !slices.Equal(hosts, s.AllowedHosts) {
			s.AllowedHosts, changed = hosts, true
		}
	}
	if !changed {
		return nil
	}
	if err := s.Validate(); err != nil {
		return fmt.Errorf("settings from environment: %w", err)
	}
	return store.Update(s)
}
