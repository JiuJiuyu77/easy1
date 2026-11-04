package config

import "testing"

func TestNormalizeProbeTargetsDefaultsAndDedup(t *testing.T) {
	if got := normalizeProbeTargets("", nil); len(got) == 0 || got[0] != DefaultProbeTargets[0] {
		t.Fatalf("empty config should fall back to defaults, got %v", got)
	}

	got := normalizeProbeTargets("  https://legacy.example/  ", []string{
		"https://legacy.example/", "  ", "https://custom.example/health", "https://custom.example/health",
	})
	if len(got) != 2 {
		t.Fatalf("expected legacy+custom deduplicated, got %v", got)
	}
	if got[0] != "https://legacy.example/" || got[1] != "https://custom.example/health" {
		t.Fatalf("unexpected order/content: %v", got)
	}
}

func TestNormalizeProbePolicy(t *testing.T) {
	cases := map[string]string{"": "any", "any": "any", "ALL": "all", " all ": "all", "weird": "any"}
	for input, want := range cases {
		if got := normalizeProbePolicy(input); got != want {
			t.Fatalf("normalizeProbePolicy(%q) = %q, want %q", input, got, want)
		}
	}
}

func TestValidateProbeTarget(t *testing.T) {
	valid := []string{
		"https://www.gstatic.com/generate_204",
		"http://example.com",
		"www.apple.com:80",
		"1.1.1.1:443",
		"https://example.com/health?x=1",
	}
	for _, target := range valid {
		if err := ValidateProbeTarget(target); err != nil {
			t.Fatalf("valid target %q rejected: %v", target, err)
		}
	}
	invalid := []string{"", "   ", "ftp://example.com", "https://", "has space.com", "example.com:abc"}
	for _, target := range invalid {
		if err := ValidateProbeTarget(target); err == nil {
			t.Fatalf("invalid target %q accepted", target)
		}
	}
}

// A custom target list must survive normalization and stay non-comparable-safe.
func TestProbeTargetsSurviveNormalization(t *testing.T) {
	cfg := &Config{Management: ManagementConfig{
		ProbeTargets: []string{"https://custom.example/health"},
		ProbePolicy:  "all",
	}}
	if err := cfg.normalize(); err != nil {
		t.Fatalf("normalize: %v", err)
	}
	if len(cfg.Management.ProbeTargets) != 1 || cfg.Management.ProbeTargets[0] != "https://custom.example/health" {
		t.Fatalf("custom targets lost: %v", cfg.Management.ProbeTargets)
	}
	if cfg.ProbePolicyOrDefault() != "all" {
		t.Fatalf("policy lost: %q", cfg.ProbePolicyOrDefault())
	}
}
