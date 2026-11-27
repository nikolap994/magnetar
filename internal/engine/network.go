package engine

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"sort"
	"time"

	"github.com/anacrolix/torrent"
	"github.com/anacrolix/torrent/dialer"
	"golang.org/x/net/proxy"

	"github.com/nikolap994/magnetar/internal/config"
)

// killSwitchIP is what peer traffic is bound to while the chosen network
// interface is down: nothing bound to loopback can reach the internet, so
// transfers stop instead of leaking over another connection.
var killSwitchIP = net.IPv4(127, 0, 0, 1)

// interfaceCheckEvery is how often the bound interface is looked up again.
const interfaceCheckEvery = 3 * time.Second

// Interface describes a network interface the user can bind to.
type Interface struct {
	Name      string   `json:"name"`
	Addresses []string `json:"addresses"`
	Up        bool     `json:"up"`
}

// Interfaces lists the computer's network interfaces that have an address,
// skipping loopback.
func Interfaces() ([]Interface, error) {
	ifs, err := net.Interfaces()
	if err != nil {
		return nil, err
	}
	var out []Interface
	for _, ifc := range ifs {
		if ifc.Flags&net.FlagLoopback != 0 {
			continue
		}
		v := Interface{Name: ifc.Name, Up: ifc.Flags&net.FlagUp != 0 && ifc.Flags&net.FlagRunning != 0}
		addrs, _ := ifc.Addrs()
		for _, a := range addrs {
			if ipn, ok := a.(*net.IPNet); ok && !ipn.IP.IsLinkLocalUnicast() {
				v.Addresses = append(v.Addresses, ipn.IP.String())
			}
		}
		if len(v.Addresses) > 0 {
			out = append(out, v)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

// interfaceIP returns the address to bind to on the named interface,
// preferring IPv4 (most VPNs hand out IPv4 and many trackers are IPv4 only).
func interfaceIP(name string) (net.IP, error) {
	ifc, err := net.InterfaceByName(name)
	if err != nil {
		return nil, fmt.Errorf("network interface %q not found", name)
	}
	if ifc.Flags&net.FlagUp == 0 || ifc.Flags&net.FlagRunning == 0 {
		return nil, fmt.Errorf("network interface %q is down", name)
	}
	addrs, err := ifc.Addrs()
	if err != nil {
		return nil, err
	}
	var v6 net.IP
	for _, a := range addrs {
		ipn, ok := a.(*net.IPNet)
		if !ok || ipn.IP.IsLinkLocalUnicast() || ipn.IP.IsLoopback() {
			continue
		}
		if ipn.IP.To4() != nil {
			return ipn.IP.To4(), nil
		}
		if v6 == nil {
			v6 = ipn.IP
		}
	}
	if v6 != nil {
		return v6, nil
	}
	return nil, fmt.Errorf("network interface %q has no address", name)
}

// bindIP decides which address peer traffic is bound to: the configured IP,
// the bound interface's current IP, the kill-switch address when that
// interface is gone, or nil for all interfaces.
func bindIP(s config.Settings) (ip net.IP, killed bool, err error) {
	switch {
	case s.BindInterface != "":
		ip, err := interfaceIP(s.BindInterface)
		if err != nil {
			return killSwitchIP, true, err
		}
		return ip, false, nil
	case s.BindAddress != "":
		return net.ParseIP(s.BindAddress), false, nil
	}
	return nil, false, nil
}

// applyBind restricts every socket the client opens to ip.
func applyBind(cfg *torrent.ClientConfig, ip net.IP) {
	host := ip.String()
	cfg.ListenHost = func(string) string { return host }
	if ip.To4() != nil {
		cfg.DisableIPv6 = true
	} else {
		cfg.DisableIPv4 = true
	}
	d := &net.Dialer{LocalAddr: &net.TCPAddr{IP: ip}, Timeout: 30 * time.Second}
	cfg.HTTPDialContext = d.DialContext
	cfg.TrackerDialContext = d.DialContext
	cfg.TrackerListenPacket = func(network, _ string) (net.PacketConn, error) {
		return net.ListenPacket(network, net.JoinHostPort(host, "0"))
	}
	cfg.NoDefaultPortForwarding = true
	cfg.LocalServiceDiscovery = nil
}

var errUDPProxied = errors.New("UDP trackers are disabled while peer traffic goes through the proxy")

// applyProxy sends tracker and web seed requests through the proxy and, for
// SOCKS5 with ProxyPeers, peer connections too. Anything that cannot go
// through the proxy is turned off rather than sent directly.
func applyProxy(cfg *torrent.ClientConfig, s config.Settings) {
	u := s.ProxyURL()
	if u == nil {
		return
	}
	cfg.HTTPProxy = http.ProxyURL(u)
	if !s.ProxyPeers {
		return
	}
	cfg.DialForPeerConns = false
	cfg.NoDHT = true
	cfg.DisableUTP = true
	cfg.NoDefaultPortForwarding = true
	cfg.LocalServiceDiscovery = nil
	cfg.TrackerListenPacket = func(string, string) (net.PacketConn, error) { return nil, errUDPProxied }
}

// proxyDialer returns the SOCKS5 dialer for peer connections, or nil.
func proxyDialer(s config.Settings, bind net.IP) dialer.T {
	if s.ProxyType != config.ProxySOCKS5 || !s.ProxyPeers {
		return nil
	}
	var auth *proxy.Auth
	if s.ProxyUsername != "" {
		auth = &proxy.Auth{User: s.ProxyUsername, Password: s.ProxyPassword}
	}
	d := dialer.NewSocks5(s.ProxyURL().Host, auth)
	if bind != nil {
		d.Forward = dialer.WithNetwork{Network: "tcp", Dialer: &net.Dialer{LocalAddr: &net.TCPAddr{IP: bind}, Timeout: 30 * time.Second}}
	}
	return d
}

// checkInterfaceLocked restarts the client when the bound interface comes
// up, goes down or changes address (VPN reconnects often change it).
func (e *Engine) checkInterfaceLocked(now time.Time) {
	if e.settings.BindInterface == "" || e.cl == nil || now.Sub(e.lastIfCheck) < interfaceCheckEvery {
		return
	}
	e.lastIfCheck = now
	ip, killed, _ := bindIP(e.settings)
	if killed == e.killSwitch && ip.Equal(e.boundIP) {
		return
	}
	if killed {
		e.log.Warn("bound network interface is gone, stopping all traffic", "interface", e.settings.BindInterface)
	} else {
		e.log.Info("bound network interface is up", "interface", e.settings.BindInterface, "ip", ip.String())
	}
	if err := e.restartLocked(); err != nil {
		e.log.Error("restart after interface change", "err", err)
	}
}

// HTTPClient returns a client for Magnetar's own web requests (RSS feeds and
// .torrent downloads) that follows the network settings: the proxy, and the
// bound interface or address. While the kill switch is on it cannot connect.
func (e *Engine) HTTPClient() *http.Client {
	e.mu.RLock()
	s, ip, killed := e.settings, e.boundIP, e.killSwitch
	e.mu.RUnlock()
	d := &net.Dialer{Timeout: 30 * time.Second}
	if ip != nil {
		d.LocalAddr = &net.TCPAddr{IP: ip}
	}
	tr := &http.Transport{
		DialContext:           d.DialContext,
		TLSHandshakeTimeout:   15 * time.Second,
		ResponseHeaderTimeout: 30 * time.Second,
		MaxIdleConns:          4,
		IdleConnTimeout:       60 * time.Second,
	}
	if u := s.ProxyURL(); u != nil {
		tr.Proxy = http.ProxyURL(u)
	}
	if killed {
		tr.DialContext = func(context.Context, string, string) (net.Conn, error) {
			return nil, errors.New("the VPN connection is down")
		}
	}
	return &http.Client{Transport: tr, Timeout: 90 * time.Second}
}
