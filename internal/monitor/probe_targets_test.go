package monitor

import "testing"

func TestSetProbeTargetsParsesEndpoints(t *testing.T) {
	manager, err := NewManager(Config{})
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}
	manager.SetProbeTargets([]string{
		"https://www.gstatic.com/generate_204",
		"custom.example:8080",
		"https://www.gstatic.com/generate_204", // duplicate
		"https://github.com/",
	}, "all", false)

	endpoints := manager.ProbeEndpoints()
	if len(endpoints) != 3 {
		t.Fatalf("expected 3 deduplicated endpoints, got %d: %+v", len(endpoints), endpoints)
	}
	first := endpoints[0]
	if first.Host != "www.gstatic.com" || !first.UseTLS || first.Path != "/generate_204" {
		t.Fatalf("first endpoint parsed wrong: %+v", first)
	}
	second := endpoints[1]
	if second.Host != "custom.example" || second.UseTLS || second.Addr.Port != 8080 || second.Path != "/" {
		t.Fatalf("custom host:port parsed wrong: %+v", second)
	}
	third := endpoints[2]
	if third.Host != "github.com" || third.Path != "/" || !third.UseTLS {
		t.Fatalf("github endpoint parsed wrong: %+v", third)
	}
	if manager.ProbePolicy() != "all" {
		t.Fatalf("policy = %q, want all", manager.ProbePolicy())
	}
}

func TestProbePolicyDefaultsToAny(t *testing.T) {
	manager, err := NewManager(Config{ProbeTargets: []string{"https://example.com/"}})
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}
	if manager.ProbePolicy() != "any" {
		t.Fatalf("default policy = %q, want any", manager.ProbePolicy())
	}
	if len(manager.ProbeEndpoints()) != 1 {
		t.Fatalf("endpoints from config not initialised: %+v", manager.ProbeEndpoints())
	}
}
