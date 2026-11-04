package config

import "testing"

func TestNormalizeDNS(t *testing.T) {
	cfg := &Config{DNS: DNSConfig{Server: " 8.8.8.8 ", FallbackServers: []string{"8.8.8.8", " ", "1.1.1.1", "1.1.1.1"}, Strategy: "weird"}}
	cfg.normalizeDNS()
	if cfg.DNS.Server != "8.8.8.8" {
		t.Fatalf("server not trimmed: %q", cfg.DNS.Server)
	}
	if cfg.DNS.Port != 53 {
		t.Fatalf("port default = %d", cfg.DNS.Port)
	}
	if cfg.DNS.Strategy != "prefer_ipv4" {
		t.Fatalf("strategy default = %q", cfg.DNS.Strategy)
	}
	if len(cfg.DNS.FallbackServers) != 1 || cfg.DNS.FallbackServers[0] != "1.1.1.1" {
		t.Fatalf("fallbacks not deduplicated against primary: %v", cfg.DNS.FallbackServers)
	}
}

func TestNormalizeDNSWithoutServerDropsFallbacks(t *testing.T) {
	cfg := &Config{DNS: DNSConfig{FallbackServers: []string{"8.8.8.8"}}}
	cfg.normalizeDNS()
	if len(cfg.DNS.FallbackServers) != 0 {
		t.Fatalf("fallbacks without a primary server should be dropped: %v", cfg.DNS.FallbackServers)
	}
}
