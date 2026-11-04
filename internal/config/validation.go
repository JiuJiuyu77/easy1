package config

import (
	"fmt"
	"net"
	"net/netip"
	"strconv"
	"strings"
)

func normalizePoolMode(mode string) (string, error) {
	switch strings.ToLower(strings.TrimSpace(mode)) {
	case "", "sequential", "round-robin", "round_robin":
		return "sequential", nil
	case "rotate":
		// Legacy name for the same "walk the member list" scheduling used by
		// the older management UI; keep accepting it so existing configs load.
		return "sequential", nil
	case "random":
		return "random", nil
	case "balance":
		return "balance", nil
	case "latency":
		return "latency", nil
	default:
		return "", fmt.Errorf("unsupported pool.mode (use sequential, random, balance or latency)")
	}
}

// ValidateManagementSettings validates settings without fetching subscriptions,
// resolving DNS, or probing ports. Zero values retain documented defaults.
func (c *Config) ValidateManagementSettings() error {
	if c == nil {
		return fmt.Errorf("config is nil")
	}
	switch c.Mode {
	case "", "pool", "multi-port", "multi_port", "hybrid":
	default:
		return fmt.Errorf("invalid proxy mode")
	}
	if _, err := normalizePoolMode(c.Pool.Mode); err != nil {
		return err
	}
	switch c.ProbePolicyOrDefault() {
	case "any", "all":
	default:
		return fmt.Errorf("probe_policy must be any or all")
	}
	for _, target := range c.Management.ProbeTargets {
		if err := ValidateProbeTarget(target); err != nil {
			return err
		}
	}
	if c.DNS.Server != "" {
		if strings.ContainsAny(c.DNS.Server, " \t\r\n/") {
			return fmt.Errorf("dns.server must be a bare host or IP")
		}
		if c.DNS.Port < 1 || c.DNS.Port > 65535 {
			return fmt.Errorf("dns.port must be between 1 and 65535")
		}
		valid := false
		for _, strategy := range DNSStrategies {
			if c.DNS.Strategy == strategy {
				valid = true
				break
			}
		}
		if !valid {
			return fmt.Errorf("dns.strategy must be one of %v", DNSStrategies)
		}
	}
	for name, value := range map[string]string{"listener.address": c.Listener.Address, "multi_port.address": c.MultiPort.Address, "geoip.listen": c.GeoIP.Listen} {
		if value != "" {
			if _, err := netip.ParseAddr(value); err != nil {
				return fmt.Errorf("%s must be an IPv4 or IPv6 address", name)
			}
		}
	}
	if c.Management.Listen != "" {
		_, port, err := net.SplitHostPort(c.Management.Listen)
		number, parseErr := strconv.Atoi(port)
		if err != nil || parseErr != nil || number < 1 || number > 65535 {
			return fmt.Errorf("management.listen must be host:port with a port from 1 to 65535")
		}
	}
	if c.Pool.FailureThreshold < 0 || c.Pool.RetryAttempts < 0 {
		return fmt.Errorf("pool failure threshold and retry attempts must not be negative")
	}
	if c.Pool.BlacklistDuration < 0 || c.Pool.TransientCooldown < 0 || c.Pool.RateLimitCooldown < 0 {
		return fmt.Errorf("pool durations must not be negative")
	}
	if c.Management.ProbeConcurrency < 0 || c.Management.ProbeConcurrency > 1024 {
		return fmt.Errorf("management.probe_concurrency must be from 1 to 1024 or 0 for default")
	}
	if c.Sticky.SessionTTL < 0 || c.Sticky.SessionTTL > MaxSessionTTL {
		return fmt.Errorf("sticky.session_ttl must be positive and at most %s", MaxSessionTTL)
	}
	if c.Sticky.MaxSessions < 0 || c.Sticky.MaxSessions > MaxSessionCapacity {
		return fmt.Errorf("sticky.max_sessions must be between 1 and %d", MaxSessionCapacity)
	}
	if c.Sticky.Enabled && (c.Mode == "pool" || c.Mode == "hybrid" || c.Mode == "") {
		listenPort := c.Listener.Port
		if listenPort == 0 {
			listenPort = 2323
		}
		if c.Sticky.Port != 0 && c.Sticky.Port == listenPort {
			return fmt.Errorf("sticky.port conflicts with listener.port")
		}
		if c.Sticky.Port == 0 && listenPort == 65535 {
			return fmt.Errorf("set an explicit sticky.port when listener.port is 65535")
		}
	}
	if c.GeoIP.Enabled && c.GeoIP.Port > 0 && c.GeoIP.Port == c.Sticky.Port && c.Sticky.Enabled {
		return fmt.Errorf("geoip.port conflicts with sticky.port")
	}
	return nil
}

// ValidateProbeTarget accepts an http(s) URL or a host[:port] entry and rejects
// values that cannot be parsed into a reachability check.
func ValidateProbeTarget(target string) error {
	value := strings.TrimSpace(target)
	if value == "" {
		return fmt.Errorf("probe target must not be empty")
	}
	if strings.ContainsAny(value, " \t\r\n") {
		return fmt.Errorf("probe target %q must not contain spaces", value)
	}
	probe := value
	if idx := strings.Index(probe, "://"); idx != -1 {
		scheme := strings.ToLower(probe[:idx])
		if scheme != "http" && scheme != "https" {
			return fmt.Errorf("probe target %q must use http or https", value)
		}
		probe = probe[idx+3:]
	}
	if idx := strings.IndexAny(probe, "/?#"); idx != -1 {
		probe = probe[:idx]
	}
	if probe == "" {
		return fmt.Errorf("probe target %q is missing a host", value)
	}
	if host, port, err := net.SplitHostPort(probe); err == nil {
		if host == "" {
			return fmt.Errorf("probe target %q is missing a host", value)
		}
		if _, convErr := strconv.Atoi(port); convErr != nil || port == "" {
			return fmt.Errorf("probe target %q has an invalid port", value)
		}
	} else if strings.Contains(probe, ":") {
		return fmt.Errorf("probe target %q has an invalid port", value)
	}
	return nil
}
