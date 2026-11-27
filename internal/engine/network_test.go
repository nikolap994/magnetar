package engine

import (
	"net"
	"testing"

	"github.com/anacrolix/torrent"

	"github.com/nikolap994/magnetar/internal/config"
)

func TestBindIPKillSwitch(t *testing.T) {
	s := config.Defaults()
	s.BindInterface = "magnetar-test-missing0"
	ip, killed, err := bindIP(s)
	if err == nil || !killed || !ip.Equal(killSwitchIP) {
		t.Fatalf("missing interface must engage the kill switch: ip=%v killed=%v err=%v", ip, killed, err)
	}

	s.BindInterface, s.BindAddress = "", "10.8.0.2"
	if ip, killed, _ := bindIP(s); killed || !ip.Equal(net.ParseIP("10.8.0.2")) {
		t.Fatalf("bind address: %v %v", ip, killed)
	}
	s.BindAddress = ""
	if ip, _, _ := bindIP(s); ip != nil {
		t.Fatalf("no binding expected, got %v", ip)
	}
}

func TestApplyProxyPeersDisablesUDP(t *testing.T) {
	s := config.Defaults()
	s.ProxyType, s.ProxyHost, s.ProxyPort, s.ProxyPeers = config.ProxySOCKS5, "127.0.0.1", 1080, true
	cfg := torrent.NewDefaultClientConfig()
	applyProxy(cfg, s)
	if cfg.HTTPProxy == nil || cfg.DialForPeerConns || !cfg.NoDHT || !cfg.DisableUTP {
		t.Fatal("proxying peers must route HTTP through the proxy and turn off direct/UDP traffic")
	}
	if _, err := cfg.TrackerListenPacket("udp", ""); err == nil {
		t.Fatal("UDP trackers must be refused")
	}
	if proxyDialer(s, nil) == nil {
		t.Fatal("expected a SOCKS5 peer dialer")
	}
	s.ProxyPeers = false
	if proxyDialer(s, nil) != nil {
		t.Fatal("no peer dialer without ProxyPeers")
	}
}
