package builder

import (
	"strings"
	"testing"

	"easy_proxies/internal/config"
)

// "all N nodes failed to build" is useless on its own; the message must carry at
// least the first concrete reason so the operator knows what to fix.
func TestBuildErrorIncludesNodeReasons(t *testing.T) {
	cfg := &config.Config{
		Mode:     "pool",
		Listener: config.ListenerConfig{Address: "127.0.0.1", Port: 1234},
		Nodes: []config.NodeConfig{
			{Name: "broken", URI: "vless://@host:443"},
			{Name: "broken2", URI: "not-a-uri"},
		},
	}
	_, err := Build(cfg)
	if err == nil {
		t.Fatal("expected a build error")
	}
	message := err.Error()
	if !strings.Contains(message, "no valid nodes available") {
		t.Fatalf("error lost its summary: %s", message)
	}
	if !strings.Contains(message, "broken") {
		t.Fatalf("error does not name a failing node: %s", message)
	}
	if !strings.Contains(message, ":") || len(message) <= len("no valid nodes available (all 2 nodes failed to build)") {
		t.Fatalf("error carries no reason: %s", message)
	}
}
