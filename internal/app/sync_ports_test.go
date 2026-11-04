package app

import (
	"path/filepath"
	"testing"

	"easy_proxies/internal/config"
	"easy_proxies/internal/importer"
)

// A pool entry without a runtime port used to abort startup with
// "runtime port missing for pool node". It must be skipped instead.
func TestSyncPoolRuntimePortsSkipsUnsyncedEntries(t *testing.T) {
	store, err := importer.NewStore(filepath.Join(t.TempDir(), "managed_nodes.json"))
	if err != nil {
		t.Fatalf("store: %v", err)
	}
	defer store.Close()

	pool := []importer.ManagedNode{{ID: "n1", URI: "socks5://127.0.0.1:9", Name: "n1", Port: 24000}}

	if err := syncPoolRuntimePorts(store, pool, nil); err != nil {
		t.Fatalf("missing config node must not fail the sync: %v", err)
	}
	if err := syncPoolRuntimePorts(store, pool, []config.NodeConfig{{URI: "socks5://127.0.0.1:9", Name: "n1"}}); err != nil {
		t.Fatalf("zero runtime port must not fail the sync: %v", err)
	}
	// A matching pair still syncs the port back into the store.
	cfgNodes := []config.NodeConfig{{URI: "socks5://127.0.0.1:9", Name: "n1", Port: 24005}}
	if err := syncPoolRuntimePorts(store, pool, cfgNodes); err != nil {
		t.Fatalf("sync: %v", err)
	}
	nodes := store.ListPoolNodes()
	for _, node := range nodes {
		if node.ID == "n1" && node.Port != 24005 {
			t.Fatalf("pool port not updated: %d", node.Port)
		}
	}
}
