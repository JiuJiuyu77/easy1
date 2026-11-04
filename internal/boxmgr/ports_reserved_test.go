package boxmgr

import (
	"testing"

	"easy_proxies/internal/config"
	"easy_proxies/internal/monitor"
)

func portTestConfig(mode string, listenerPort uint16, management string, basePort uint16) *config.Config {
	return &config.Config{
		Mode:          mode,
		Listener:      config.ListenerConfig{Address: "127.0.0.1", Port: listenerPort},
		MultiPort:     config.MultiPortConfig{Address: "127.0.0.1", BasePort: basePort},
		Management:    config.ManagementConfig{Listen: management},
		Sticky:        config.StickyConfig{Enabled: true},
		Subscriptions: nil,
	}
}

// A node must never be handed the proxy entry, management, sticky or
// Clash/GeoIP port: bind would fail and, for the management port, take the UI
// down with it.
func TestNextAvailablePortSkipsReservedPorts(t *testing.T) {
	// hybrid listeners the shared entry (24000) and the management API (24001),
	// so the first node port must be 24002 — not one of the two.
	cfg := portTestConfig("hybrid", 24000, "127.0.0.1:24001", 24000)
	manager := New(cfg, monitor.Config{Listen: "127.0.0.1:24001"})

	manager.mu.Lock()
	port := manager.nextAvailablePortLocked()
	manager.mu.Unlock()

	if port != 24002 {
		t.Fatalf("expected 24002 (after the reserved listener/management ports), got %d", port)
	}
}

func TestAssignSequentialPortsSkipsReservedPorts(t *testing.T) {
	// multi-port: no shared entry, so 24010/24012 are fine, but the management
	// API on 24011 must be skipped.
	cfg := portTestConfig("multi-port", 24010, "127.0.0.1:24011", 24010)
	cfg.Nodes = []config.NodeConfig{
		{Name: "a", URI: "socks5://127.0.0.1:9"},
		{Name: "b", URI: "socks5://127.0.0.1:10"},
		{Name: "c", URI: "socks5://127.0.0.1:11"},
	}
	manager := New(cfg, monitor.Config{Listen: "127.0.0.1:24011"})

	manager.mu.Lock()
	err := manager.assignSequentialPortsLocked()
	manager.mu.Unlock()
	if err != nil {
		t.Fatalf("assign: %v", err)
	}

	reserved := map[uint16]bool{24011: true}
	seen := make(map[uint16]bool)
	for _, node := range cfg.Nodes {
		if node.Port == 0 {
			t.Fatalf("node %s got no port", node.Name)
		}
		if reserved[node.Port] {
			t.Fatalf("node %s got reserved port %d", node.Name, node.Port)
		}
		if seen[node.Port] {
			t.Fatalf("duplicate port %d", node.Port)
		}
		seen[node.Port] = true
	}
}

// In pool mode the listener is still reserved, but nodes carry no own port.
func TestAssignSequentialPortsIgnoresPoolMode(t *testing.T) {
	cfg := portTestConfig("pool", 2323, "127.0.0.1:9091", 24000)
	cfg.Nodes = []config.NodeConfig{{Name: "a", URI: "socks5://127.0.0.1:9"}}
	manager := New(cfg, monitor.Config{Listen: "127.0.0.1:9091"})

	manager.mu.Lock()
	err := manager.assignSequentialPortsLocked()
	manager.mu.Unlock()
	if err != nil {
		t.Fatalf("assign in pool mode: %v", err)
	}
	if cfg.Nodes[0].Port != 0 {
		t.Fatalf("pool mode must not assign per-node ports, got %d", cfg.Nodes[0].Port)
	}
}
