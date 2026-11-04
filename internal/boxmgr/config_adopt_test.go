package boxmgr

import (
	"testing"

	"easy_proxies/internal/config"
	"easy_proxies/internal/monitor"
)

// The monitor server keeps a *config.Config for the settings API. If a reload
// replaced the manager's pointer instead of updating the object in place, the
// WebUI would keep editing the old configuration and saved settings (mode,
// ports, pool policy) would never reach the runtime.
func TestAdoptConfigLockedUpdatesTheSharedPointer(t *testing.T) {
	shared := &config.Config{Mode: "pool"}
	manager := New(shared, monitor.Config{})

	next := &config.Config{Mode: "multi-port"}
	next.Nodes = []config.NodeConfig{{Name: "n1", URI: "socks5://127.0.0.1:9"}}

	manager.adoptConfigLocked(next)

	if manager.cfg != shared {
		t.Fatal("manager replaced the shared config pointer")
	}
	if shared.Mode != "multi-port" {
		t.Fatalf("shared config mode = %q, want multi-port", shared.Mode)
	}
	if len(shared.Nodes) != 1 {
		t.Fatalf("shared config nodes = %d, want 1", len(shared.Nodes))
	}
}

func TestAdoptConfigLockedHandlesNil(t *testing.T) {
	shared := &config.Config{Mode: "pool"}
	manager := New(shared, monitor.Config{})
	manager.adoptConfigLocked(nil)
	if manager.cfg != shared || shared.Mode != "pool" {
		t.Fatal("nil adoption must leave the config untouched")
	}
}
