package boxmgr

import (
	"net"
	"testing"

	"easy_proxies/internal/config"
	"easy_proxies/internal/monitor"
)

// A port held by another process must be skipped when handing out a node port.
func TestNextAvailablePortSkipsPortsOwnedByOtherProcesses(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Skipf("cannot listen for the fixture: %v", err)
	}
	defer listener.Close()
	taken := uint16(listener.Addr().(*net.TCPAddr).Port)
	if taken >= 65534 {
		t.Skip("fixture port too high")
	}

	cfg := &config.Config{
		Mode:       "multi-port",
		Listener:   config.ListenerConfig{Address: "127.0.0.1", Port: 1234},
		MultiPort:  config.MultiPortConfig{Address: "127.0.0.1", BasePort: taken},
		Management: config.ManagementConfig{Listen: "127.0.0.1:9091"},
	}
	manager := New(cfg, monitor.Config{Listen: "127.0.0.1:9091"})

	manager.mu.Lock()
	port := manager.nextAvailablePortLocked()
	manager.mu.Unlock()

	if port == taken {
		t.Fatalf("allocator handed out a busy port %d", taken)
	}
}
