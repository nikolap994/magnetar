package engine

import (
	"path/filepath"

	"github.com/anacrolix/dht/v2"
	"github.com/anacrolix/dht/v2/krpc"
	"github.com/anacrolix/torrent"
)

func (e *Engine) dhtNodesPath() string { return filepath.Join(e.dataDir, "dht-nodes.dat") }

// loadDHTNodes seeds the DHT routing tables from the nodes saved last time, so
// peer discovery starts warm instead of bootstrapping from scratch on every
// launch (which otherwise means far fewer peers for the first several minutes).
func (e *Engine) loadDHTNodes(cl *torrent.Client) {
	nodes, err := dht.ReadNodesFromFile(e.dhtNodesPath())
	if err != nil || len(nodes) == 0 {
		return
	}
	for _, s := range cl.DhtServers() {
		for _, ni := range nodes {
			_ = s.AddNode(ni)
		}
	}
}

// saveDHTNodes writes the current DHT routing tables so the next run starts warm.
func (e *Engine) saveDHTNodes(cl *torrent.Client) {
	if cl == nil {
		return
	}
	seen := make(map[string]struct{})
	var all []krpc.NodeInfo
	for _, s := range cl.DhtServers() {
		w, ok := s.(torrent.AnacrolixDhtServerWrapper)
		if !ok {
			continue
		}
		for _, ni := range w.Nodes() {
			k := ni.Addr.String()
			if _, dup := seen[k]; dup {
				continue
			}
			seen[k] = struct{}{}
			all = append(all, ni)
		}
	}
	if len(all) == 0 {
		return
	}
	if err := dht.WriteNodesToFile(all, e.dhtNodesPath()); err != nil {
		e.log.Warn("save DHT nodes", "err", err)
	}
}
