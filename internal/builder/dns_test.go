package builder

import (
	"testing"

	"easy_proxies/internal/config"

	C "github.com/sagernet/sing-box/constant"
	"github.com/sagernet/sing-box/option"
)

func TestBuildDNSOptions(t *testing.T) {
	cfg := &config.Config{DNS: config.DNSConfig{
		Server:          "223.5.5.5",
		Port:            5353,
		FallbackServers: []string{"8.8.8.8", "tls://dns.google"},
		Strategy:        "ipv4_only",
	}}
	dns := buildDNSOptions(cfg)
	if dns == nil {
		t.Fatal("DNS options not built for a configured server")
	}
	if len(dns.Servers) != 3 {
		t.Fatalf("expected primary + 2 fallbacks, got %d", len(dns.Servers))
	}
	first, ok := dns.Servers[0].Options.(*option.RemoteDNSServerOptions)
	if !ok {
		t.Fatalf("primary server options type = %T", dns.Servers[0].Options)
	}
	if first.Server != "223.5.5.5" || first.ServerPort != 5353 {
		t.Fatalf("primary address = %s:%d", first.Server, first.ServerPort)
	}
	if dns.Servers[0].Type != C.DNSTypeUDP {
		t.Fatalf("primary transport = %q", dns.Servers[0].Type)
	}
	if dns.Servers[0].Tag != "dns-primary" || dns.Final != "dns-primary" {
		t.Fatalf("tag/final = %q / %q", dns.Servers[0].Tag, dns.Final)
	}
	// A scheme-qualified fallback keeps its own transport and default port.
	third, _ := dns.Servers[2].Options.(*option.RemoteDNSServerOptions)
	if third == nil || third.Server != "dns.google" || third.ServerPort != 853 {
		t.Fatalf("tls fallback = %+v", third)
	}
	if dns.Servers[2].Type != C.DNSTypeTLS {
		t.Fatalf("tls fallback transport = %q", dns.Servers[2].Type)
	}
	if dns.Strategy != option.DomainStrategy(C.DomainStrategyIPv4Only) {
		t.Fatalf("strategy not mapped: %v", dns.Strategy)
	}
}

func TestBuildDNSOptionsEmpty(t *testing.T) {
	if dns := buildDNSOptions(&config.Config{}); dns != nil {
		t.Fatalf("no DNS server should produce no DNS options, got %+v", dns)
	}
}
