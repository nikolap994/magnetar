package server

import (
	"encoding/base64"
	"errors"
	"net"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"rsc.io/qr"

	"github.com/nikolap994/magnetar/internal/config"
)

type remoteURL struct {
	URL string `json:"url"`
	QR  string `json:"qr"` // PNG as a data: URL
}

type remoteStatus struct {
	Enabled         bool        `json:"enabled"`
	PasswordSet     bool        `json:"passwordSet"`
	RestartRequired bool        `json:"restartRequired"`
	CanRestart      bool        `json:"canRestart"`
	URLs            []remoteURL `json:"urls"`
	HTTPS           bool        `json:"https"`
	Fingerprint     string      `json:"fingerprint,omitempty"`
	CanFirewall     bool        `json:"canFirewall"`
}

// lanAddresses lists this computer's private IPv4 addresses (home and office
// networks), which phones on the same network can reach.
func lanAddresses() []string {
	var out []string
	ifs, _ := net.Interfaces()
	for _, ifc := range ifs {
		if ifc.Flags&net.FlagUp == 0 || ifc.Flags&net.FlagLoopback != 0 || virtualAdapter(ifc.Name) {
			continue
		}
		addrs, _ := ifc.Addrs()
		for _, a := range addrs {
			ipn, ok := a.(*net.IPNet)
			if !ok || ipn.IP.To4() == nil || !ipn.IP.IsPrivate() {
				continue
			}
			out = append(out, ipn.IP.String())
		}
	}
	sort.Strings(out)
	return out
}

// virtualAdapter reports container and virtual-machine networks, which a
// phone cannot reach.
func virtualAdapter(name string) bool {
	n := strings.ToLower(name)
	for _, p := range []string{"docker", "br-", "veth", "virbr", "vmnet", "vboxnet", "vethernet", "podman", "cni", "flannel", "tailscale", "zt"} {
		if strings.HasPrefix(n, p) {
			return true
		}
	}
	return false
}

func qrDataURL(text string) string {
	c, err := qr.Encode(text, qr.M)
	if err != nil {
		return ""
	}
	c.Scale = 6
	return "data:image/png;base64," + base64.StdEncoding.EncodeToString(c.PNG())
}

func (s *Server) remoteStatus() remoteStatus {
	st := s.store.Get()
	rs := remoteStatus{
		Enabled:     !config.IsLoopback(st.WebHost),
		PasswordSet: st.PasswordHash != "",
		CanRestart:  s.restart != nil,
		URLs:        []remoteURL{},
	}
	rs.RestartRequired = st.WebHost != s.host || (rs.Enabled && st.RemoteHTTPS != (s.httpsPort > 0))
	rs.HTTPS, rs.Fingerprint = s.httpsPort > 0, s.httpsFP
	rs.CanFirewall = s.firewall != nil
	if rs.Enabled {
		for _, ip := range lanAddresses() {
			u := "http://" + net.JoinHostPort(ip, strconv.Itoa(s.port))
			if s.httpsPort > 0 {
				u = "https://" + net.JoinHostPort(ip, strconv.Itoa(s.httpsPort))
			}
			rs.URLs = append(rs.URLs, remoteURL{URL: u, QR: qrDataURL(u)})
		}
	}
	return rs
}

func (s *Server) handleRemote(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, s.remoteStatus())
}

// handleRemoteSet turns access from other devices on or off. Turning it on
// needs a password; Magnetar restarts to listen on the new address.
func (s *Server) handleRemoteSet(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Enabled bool  `json:"enabled"`
		Restart bool  `json:"restart"`
		HTTPS   *bool `json:"https"`
	}
	if err := readJSON(r, &req); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	st := s.store.Get()
	if req.Enabled && st.PasswordHash == "" {
		writeErr(w, http.StatusBadRequest, errors.New("set a password first, so only you can open Magnetar from other devices"))
		return
	}
	if req.HTTPS != nil {
		st.RemoteHTTPS = *req.HTTPS
	}
	if req.Enabled {
		st.WebHost = "0.0.0.0"
	} else {
		st.WebHost = "127.0.0.1"
	}
	if err := st.Validate(); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	if err := s.store.Update(st); err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	rs := s.remoteStatus()
	writeJSON(w, rs)
	if req.Restart && rs.RestartRequired && s.restart != nil {
		go func() {
			time.Sleep(300 * time.Millisecond) // let the response reach the browser
			s.restart()
		}()
	}
}
