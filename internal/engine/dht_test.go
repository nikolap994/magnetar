package engine

import (
	"io"
	"log/slog"
	"net"
	"testing"

	"github.com/anacrolix/dht/v2"
	"github.com/anacrolix/dht/v2/krpc"

	"github.com/nikolap994/magnetar/internal/config"
)

// The DHT routing table must survive a restart, so peer discovery starts warm
// instead of bootstrapping from scratch each launch.
func TestDHTNodesPersist(t *testing.T) {
	data := t.TempDir()
	s := config.Defaults()
	s.DownloadDir = t.TempDir()
	s.ListenPort = 0
	s.EnableDHT, s.EnableLSD, s.EnableUPnP = true, false, false

	e, err := New(s, data, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	servers := e.cl.DhtServers()
	if len(servers) == 0 {
		t.Skip("no DHT server (no UDP socket in this environment)")
	}
	node := krpc.NodeInfo{
		ID:   krpc.ID{1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15, 16, 17, 18, 19, 20},
		Addr: krpc.NodeAddr{IP: net.IPv4(127, 0, 0, 1), Port: 6881},
	}
	for _, srv := range servers {
		if err := srv.AddNode(node); err != nil {
			t.Fatalf("add node: %v", err)
		}
	}
	e.Close() // saves DHT nodes

	saved, err := dht.ReadNodesFromFile(e.dhtNodesPath())
	if err != nil {
		t.Fatalf("read saved nodes: %v", err)
	}
	if !containsNode(saved, node) {
		t.Fatalf("saved file is missing the node: %+v", saved)
	}

	// A fresh engine over the same data dir must seed its table from the file.
	e2, err := New(s, data, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	defer e2.Close()
	var warm []krpc.NodeInfo
	for _, srv := range e2.cl.DhtServers() {
		if w, ok := srv.(interface {
			Nodes() []krpc.NodeInfo
		}); ok {
			warm = append(warm, w.Nodes()...)
		}
	}
	if !containsNode(warm, node) {
		t.Fatalf("restarted engine did not load the saved node; table=%+v", warm)
	}
}

func containsNode(ns []krpc.NodeInfo, want krpc.NodeInfo) bool {
	for _, n := range ns {
		if n.Addr.IP.Equal(want.Addr.IP) && n.Addr.Port == want.Addr.Port {
			return true
		}
	}
	return false
}
