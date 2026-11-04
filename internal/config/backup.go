package config

import (
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"easy_proxies/internal/proxychain"

	"gopkg.in/yaml.v3"
)

// Port allocator skip log. Populated when sequential allocation has to advance
// past ports occupied by external processes. Process-wide singleton because
// only one pool runs at a time, and avoids embedding a mutex in Config.
var (
	portSkipsMu sync.RWMutex
	portSkips   []uint16
	portSkipsAt time.Time
)

// RecordPortSkips replaces the latest skipped-port log. Pass nil/empty to clear.
func RecordPortSkips(skips []uint16) {
	portSkipsMu.Lock()
	defer portSkipsMu.Unlock()
	if len(skips) == 0 {
		portSkips = nil
		portSkipsAt = time.Time{}
		return
	}
	portSkips = append(portSkips[:0], skips...)
	portSkipsAt = time.Now()
}

// LastPortSkips returns the latest skipped-port list and its timestamp.
func LastPortSkips() ([]uint16, time.Time) {
	portSkipsMu.RLock()
	defer portSkipsMu.RUnlock()
	if len(portSkips) == 0 {
		return nil, time.Time{}
	}
	out := make([]uint16, len(portSkips))
	copy(out, portSkips)
	return out, portSkipsAt
}

// BackupYAML renders the configuration (with inline nodes) for a backup archive.
func (c *Config) BackupYAML() ([]byte, error) {
	if c == nil {
		return nil, errors.New("config is nil")
	}
	backup := *c
	backup.filePath = ""
	backup.NodesFile = ""
	backup.Subscriptions = append([]string(nil), c.Subscriptions...)
	backup.ChainProfiles = append([]proxychain.Profile(nil), c.ChainProfiles...)
	backup.Nodes = append([]NodeConfig(nil), c.Nodes...)
	return yaml.Marshal(&backup)
}

// DecodeBackupYAML parses a backup config, normalizes settings and validates the
// node payload without touching the runtime.
func DecodeBackupYAML(data []byte, path string) (*Config, error) {
	var restored Config
	if err := yaml.Unmarshal(data, &restored); err != nil {
		return nil, fmt.Errorf("decode backup config: %w", err)
	}
	nodes := append([]NodeConfig(nil), restored.Nodes...)
	restored.Nodes = nil
	restored.NodesFile = ""
	restored.filePath = path
	if err := restored.NormalizeWithPortMap(nil); err != nil {
		return nil, fmt.Errorf("validate backup config: %w", err)
	}
	for i := range nodes {
		nodes[i].Name = strings.TrimSpace(nodes[i].Name)
		nodes[i].URI = strings.TrimSpace(nodes[i].URI)
		if nodes[i].URI == "" {
			return nil, fmt.Errorf("validate backup config: node %d is missing uri", i)
		}
		if nodes[i].Name == "" {
			nodes[i].Name = ExtractNodeName(nodes[i].URI)
		}
		if nodes[i].Name == "" {
			nodes[i].Name = fmt.Sprintf("node-%d", i)
		}
	}
	restored.Nodes = nodes
	return &restored, nil
}

// SaveFull writes the whole configuration (settings + nodes) to disk.
func (c *Config) SaveFull() error {
	if c == nil {
		return errors.New("config is nil")
	}
	if c.filePath == "" {
		return errors.New("config file path is unknown")
	}
	data, err := yaml.Marshal(c)
	if err != nil {
		return fmt.Errorf("encode config: %w", err)
	}
	return writeFileWithLock(c.filePath, data, 0o644)
}
