package boxmgr

import (
	"context"
	"errors"
	"fmt"
	"net"
	"strconv"
	"strings"
	"time"

	"easy_proxies/internal/builder"
	"easy_proxies/internal/config"
	"easy_proxies/internal/monitor"
)

// This file implements the batch node / port / self-check surface that the
// management UI's import, pool and ports pages drive. The single-node CRUD in
// manager.go stays the primitive; these wrappers apply a whole set in one
// transaction (one save, one reload) so importing a subscription does not
// rewrite config.yaml and rebuild the runtime once per node.
//
// They are additive: the optional interfaces the importer type-asserts
// (importer.NodeBatchCreator, NodeBatchUpdater, NodeReorderer,
// NodePortResolver, NodeSnapshotRestorer, NodeRuntimeValidator,
// NodeRuntimeVerifier) are all satisfied by *Manager through this file.

const runtimePortReadyTimeout = 15 * time.Second

// EnsureMonitor starts the management listener (and its node registry) without
// starting the proxy core. The WebUI needs it before the first node exists.
func (m *Manager) EnsureMonitor(ctx context.Context) error {
	return m.ensureMonitor(ctx)
}

// SetStartupTimings records per-stage startup timings for the diagnostics API.
func (m *Manager) SetStartupTimings(timings map[string]int64) {
	cloned := make(map[string]int64, len(timings))
	for stage, milliseconds := range timings {
		cloned[stage] = milliseconds
	}
	m.startupTimings.Store(cloned)
}

// Diagnostics reports a small runtime snapshot for the WebUI diagnostics panel.
func (m *Manager) Diagnostics() map[string]any {
	m.mu.RLock()
	cfgCopy := m.copyConfigLocked()
	running := m.currentBox != nil
	m.mu.RUnlock()

	result := map[string]any{"running": running}
	if cfgCopy == nil {
		return result
	}
	result["mode"] = cfgCopy.Mode
	result["configured_nodes"] = len(cfgCopy.Nodes)
	if cfgCopy.Mode == "multi-port" || cfgCopy.Mode == "hybrid" {
		result["listeners"] = len(cfgCopy.Nodes)
	}
	if value := m.startupTimings.Load(); value != nil {
		if stored, ok := value.(map[string]int64); ok {
			timings := make(map[string]int64, len(stored))
			for stage, milliseconds := range stored {
				timings[stage] = milliseconds
			}
			result["startup_ms"] = timings
		}
	}
	return result
}

// ValidateNode checks a node's URI (and its chain profile, if any) without
// touching the running configuration.
func (m *Manager) ValidateNode(ctx context.Context, node config.NodeConfig) error {
	if ctx != nil {
		if err := ctx.Err(); err != nil {
			return err
		}
	}
	m.mu.RLock()
	cfgCopy := m.copyConfigLocked()
	m.mu.RUnlock()
	return builder.ValidateNodeConfig(cfgCopy, node)
}

// VerifyRuntime waits until every per-node listener in multi-port/hybrid mode
// accepts TCP connections, so the UI can report a real "applied" state instead
// of assuming the reload worked.
func (m *Manager) VerifyRuntime(ctx context.Context) error {
	if ctx == nil {
		ctx = context.Background()
	}
	m.mu.RLock()
	cfgCopy := m.copyConfigLocked()
	m.mu.RUnlock()
	if cfgCopy == nil {
		return errConfigUnavailable
	}
	if cfgCopy.Mode != "multi-port" && cfgCopy.Mode != "hybrid" || len(cfgCopy.Nodes) == 0 {
		return nil
	}
	host := strings.TrimSpace(cfgCopy.MultiPort.Address)
	switch host {
	case "", "0.0.0.0":
		host = "127.0.0.1"
	case "::", "[::]":
		host = "::1"
	}
	pending := make([]uint16, 0, len(cfgCopy.Nodes))
	for _, node := range cfgCopy.Nodes {
		pending = append(pending, node.Port)
	}
	readyCtx, cancel := context.WithTimeout(ctx, runtimePortReadyTimeout)
	defer cancel()
	for len(pending) > 0 {
		pending = unavailableRuntimePorts(readyCtx, host, pending)
		if len(pending) == 0 {
			return nil
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if readyCtx.Err() != nil {
			break
		}
		timer := time.NewTimer(250 * time.Millisecond)
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-readyCtx.Done():
			timer.Stop()
		case <-timer.C:
		}
	}
	samples := make([]string, 0, 8)
	for i, port := range pending {
		if i == 8 {
			break
		}
		if port == 0 {
			samples = append(samples, "未分配")
			continue
		}
		samples = append(samples, strconv.Itoa(int(port)))
	}
	return fmt.Errorf("%d 个运行端口不可用（示例: %s）", len(pending), strings.Join(samples, ", "))
}

func unavailableRuntimePorts(ctx context.Context, host string, ports []uint16) []uint16 {
	if len(ports) == 0 {
		return nil
	}
	workers := 32
	if len(ports) < workers {
		workers = len(ports)
	}
	jobs := make(chan uint16)
	failures := make(chan uint16, len(ports))
	done := make(chan struct{})
	for i := 0; i < workers; i++ {
		go func() {
			dialer := net.Dialer{Timeout: time.Second}
			for port := range jobs {
				if port == 0 {
					failures <- port
					continue
				}
				conn, err := dialer.DialContext(ctx, "tcp", net.JoinHostPort(host, strconv.Itoa(int(port))))
				if err != nil {
					failures <- port
					continue
				}
				_ = conn.Close()
			}
			done <- struct{}{}
		}()
	}
	go func() {
		defer close(jobs)
		for _, port := range ports {
			select {
			case <-ctx.Done():
				return
			case jobs <- port:
			}
		}
	}()
	for i := 0; i < workers; i++ {
		<-done
	}
	close(failures)
	result := make([]uint16, 0, len(ports))
	for port := range failures {
		result = append(result, port)
	}
	return result
}

// CloseContext shuts the runtime down within the caller's deadline.
func (m *Manager) CloseContext(ctx context.Context) error {
	if ctx == nil {
		return m.Close()
	}
	done := make(chan error, 1)
	go func() { done <- m.Close() }()
	select {
	case err := <-done:
		return err
	case <-ctx.Done():
		return ctx.Err()
	}
}

// CreateNodes adds several nodes in one transaction: one save, and the caller
// decides when to reload.
func (m *Manager) CreateNodes(ctx context.Context, nodes []config.NodeConfig) ([]config.NodeConfig, error) {
	if ctx != nil {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
	}
	if len(nodes) == 0 {
		return nil, nil
	}

	m.lifecycleMu.Lock()
	defer m.lifecycleMu.Unlock()
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.cfg == nil {
		return nil, errConfigUnavailable
	}

	backup := cloneNodes(m.cfg.Nodes)
	created := make([]config.NodeConfig, 0, len(nodes))
	for _, node := range nodes {
		normalized, err := m.prepareNodeLocked(node, "")
		if err != nil {
			m.cfg.Nodes = backup
			return nil, err
		}
		// WebUI-created nodes are explicit user configuration: keep them inline
		// so a subscription refresh cannot overwrite them.
		normalized.Source = config.NodeSourceInline
		m.cfg.Nodes = append(m.cfg.Nodes, normalized)
		created = append(created, normalized)
	}
	if err := m.cfg.Save(); err != nil {
		m.cfg.Nodes = backup
		return nil, fmt.Errorf("save config: %w", err)
	}
	return created, nil
}

// UpdateNodes updates several nodes by current name in one transaction.
func (m *Manager) UpdateNodes(ctx context.Context, nodes map[string]config.NodeConfig) (map[string]config.NodeConfig, error) {
	if ctx != nil {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
	}
	if len(nodes) == 0 {
		return map[string]config.NodeConfig{}, nil
	}

	m.lifecycleMu.Lock()
	defer m.lifecycleMu.Unlock()
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.cfg == nil {
		return nil, errConfigUnavailable
	}

	backup := cloneNodes(m.cfg.Nodes)
	result := make(map[string]config.NodeConfig, len(nodes))
	// Apply in a stable order so a rename chain does not depend on map order.
	names := make([]string, 0, len(nodes))
	for name := range nodes {
		names = append(names, name)
	}
	sortStrings(names)
	for _, name := range names {
		current := strings.TrimSpace(name)
		idx := m.nodeIndexLocked(current)
		if idx == -1 {
			m.cfg.Nodes = backup
			return nil, monitor.ErrNodeNotFound
		}
		normalized, err := m.prepareNodeLocked(nodes[name], current)
		if err != nil {
			m.cfg.Nodes = backup
			return nil, err
		}
		normalized.Source = m.cfg.Nodes[idx].Source
		m.cfg.Nodes[idx] = normalized
		result[current] = normalized
	}
	if err := m.cfg.Save(); err != nil {
		m.cfg.Nodes = backup
		return nil, fmt.Errorf("save config: %w", err)
	}
	return result, nil
}

// DeleteNodes removes several nodes by name in one transaction.
func (m *Manager) DeleteNodes(ctx context.Context, names []string) error {
	if ctx != nil {
		if err := ctx.Err(); err != nil {
			return err
		}
	}

	m.lifecycleMu.Lock()
	defer m.lifecycleMu.Unlock()
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.cfg == nil {
		return errConfigUnavailable
	}

	nameSet := make(map[string]struct{}, len(names))
	for _, name := range names {
		name = strings.TrimSpace(name)
		if name != "" {
			nameSet[name] = struct{}{}
		}
	}
	if len(nameSet) == 0 {
		return monitor.ErrNodeNotFound
	}

	backup := cloneNodes(m.cfg.Nodes)
	filtered := make([]config.NodeConfig, 0, len(m.cfg.Nodes))
	deleted := 0
	for _, node := range m.cfg.Nodes {
		if _, ok := nameSet[node.Name]; ok {
			deleted++
			continue
		}
		filtered = append(filtered, node)
	}
	if deleted == 0 {
		return monitor.ErrNodeNotFound
	}
	m.cfg.Nodes = filtered
	if err := m.cfg.Save(); err != nil {
		m.cfg.Nodes = backup
		return fmt.Errorf("save config: %w", err)
	}
	return nil
}

// ReorderNodes moves the named nodes to the front in the given order. In
// multi-port/hybrid mode it also reassigns sequential ports so the order the
// operator sees in the pool page maps onto a visible port range.
func (m *Manager) ReorderNodes(ctx context.Context, names []string) error {
	if ctx != nil {
		if err := ctx.Err(); err != nil {
			return err
		}
	}

	m.lifecycleMu.Lock()
	defer m.lifecycleMu.Unlock()
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.cfg == nil {
		return errConfigUnavailable
	}

	backup := cloneNodes(m.cfg.Nodes)
	byName := make(map[string]config.NodeConfig, len(m.cfg.Nodes))
	for _, node := range m.cfg.Nodes {
		byName[node.Name] = node
	}

	reordered := make([]config.NodeConfig, 0, len(m.cfg.Nodes))
	seen := make(map[string]struct{}, len(names))
	for _, name := range names {
		name = strings.TrimSpace(name)
		if name == "" {
			continue
		}
		node, ok := byName[name]
		if !ok {
			continue
		}
		if _, exists := seen[name]; exists {
			continue
		}
		reordered = append(reordered, node)
		seen[name] = struct{}{}
	}
	for _, node := range m.cfg.Nodes {
		if _, ok := seen[node.Name]; ok {
			continue
		}
		reordered = append(reordered, node)
	}
	m.cfg.Nodes = reordered

	if m.cfg.Mode == "multi-port" || m.cfg.Mode == "hybrid" {
		if err := m.assignSequentialPortsLocked(); err != nil {
			m.cfg.Nodes = backup
			return err
		}
	}

	if err := m.cfg.Save(); err != nil {
		m.cfg.Nodes = backup
		return fmt.Errorf("save config: %w", err)
	}
	m.storePortIndexLocked()
	return nil
}

// ResolveNodePorts maps node keys (stable node key and, for chain-free nodes,
// the raw URI) to their currently assigned local ports.
func (m *Manager) ResolveNodePorts(ctx context.Context, nodeURIs []string) (map[string]uint16, error) {
	if ctx != nil {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	if m.cfg == nil {
		return nil, errConfigUnavailable
	}
	ports := make(map[string]uint16, len(m.cfg.Nodes))
	for i := range m.cfg.Nodes {
		node := m.cfg.Nodes[i]
		if node.URI == "" || node.Port == 0 {
			continue
		}
		ports[node.NodeKey()] = node.Port
		if node.ChainProfileID == "" {
			ports[node.URI] = node.Port
		}
	}
	result := make(map[string]uint16, len(nodeURIs))
	for _, uri := range nodeURIs {
		if port, ok := ports[uri]; ok {
			result[uri] = port
		}
	}
	return result, nil
}

// RestoreConfigNodes replaces the whole node set (the pool decides which nodes
// should be live) and reloads the runtime.
func (m *Manager) RestoreConfigNodes(ctx context.Context, nodes []config.NodeConfig) error {
	if ctx != nil {
		if err := ctx.Err(); err != nil {
			return err
		}
	}

	m.lifecycleMu.Lock()
	defer m.lifecycleMu.Unlock()
	m.mu.Lock()
	if m.cfg == nil {
		m.mu.Unlock()
		return errConfigUnavailable
	}
	backup := cloneNodes(m.cfg.Nodes)
	m.cfg.Nodes = cloneNodes(nodes)
	if m.cfg.Mode == "multi-port" || m.cfg.Mode == "hybrid" {
		if err := m.assignSequentialPortsLocked(); err != nil {
			m.cfg.Nodes = backup
			m.mu.Unlock()
			return err
		}
	}
	if err := m.cfg.Save(); err != nil {
		m.cfg.Nodes = backup
		m.mu.Unlock()
		return fmt.Errorf("save restored nodes: %w", err)
	}
	m.storePortIndexLocked()
	cfgCopy := m.copyConfigLocked()
	var portMap map[string]uint16
	if m.cfg != nil {
		portMap = m.cfg.BuildPortMap()
	}
	m.mu.Unlock()

	if len(cfgCopy.Nodes) == 0 {
		// Nothing to serve: keep the previous runtime untouched rather than
		// tearing the listener down on an empty import result.
		return nil
	}
	return m.reloadWithPortMapLocked(cfgCopy, portMap)
}

// RebuildPortAssignments reassigns contiguous ports from multi_port.base_port in
// node order (used after pool reordering or a restore).
func (m *Manager) RebuildPortAssignments() error {
	m.lifecycleMu.Lock()
	defer m.lifecycleMu.Unlock()
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.cfg == nil {
		return errConfigUnavailable
	}
	if m.cfg.Mode != "multi-port" && m.cfg.Mode != "hybrid" {
		return nil
	}
	backup := cloneNodes(m.cfg.Nodes)
	if err := m.assignSequentialPortsLocked(); err != nil {
		m.cfg.Nodes = backup
		return err
	}
	if err := m.cfg.Save(); err != nil {
		m.cfg.Nodes = backup
		return fmt.Errorf("save rebuilt config: %w", err)
	}
	m.storePortIndexLocked()
	return nil
}

// ApplyRestoredConfig adopts a full configuration (backup restore) and reloads.
func (m *Manager) ApplyRestoredConfig(ctx context.Context, restored *config.Config) error {
	if restored == nil {
		return errors.New("restored config is nil")
	}
	if ctx != nil {
		if err := ctx.Err(); err != nil {
			return err
		}
	}

	m.lifecycleMu.Lock()
	defer m.lifecycleMu.Unlock()
	m.mu.Lock()
	if m.cfg == nil {
		m.mu.Unlock()
		return errConfigUnavailable
	}
	m.cfg = restored
	m.applyConfigSettings(restored)
	if err := m.cfg.Save(); err != nil {
		m.mu.Unlock()
		return fmt.Errorf("save restored config: %w", err)
	}
	m.storePortIndexLocked()
	cfgCopy := m.copyConfigLocked()
	portMap := restored.BuildPortMap()
	m.mu.Unlock()
	return m.reloadWithPortMapLocked(cfgCopy, portMap)
}

// storePortIndexLocked refreshes the URI → port index used by ResolveNodePorts
// and by the ports page. Callers must hold m.mu.
func (m *Manager) storePortIndexLocked() {
	if m.cfg == nil {
		return
	}
	ports := make(map[string]uint16, len(m.cfg.Nodes))
	for _, node := range m.cfg.Nodes {
		if node.URI == "" || node.Port == 0 {
			continue
		}
		ports[node.NodeKey()] = node.Port
		if node.ChainProfileID == "" {
			ports[node.URI] = node.Port
		}
	}
	m.portIndex.Store(ports)
}

// assignSequentialPortsLocked hands out contiguous ports from
// multi_port.base_port in node order. Ports the manager already holds are not
// probed again; only genuinely new candidates are checked against the OS.
// Callers must hold m.mu.
func (m *Manager) assignSequentialPortsLocked() error {
	base := m.cfg.MultiPort.BasePort
	if base == 0 {
		base = 24000
	}
	address := m.cfg.MultiPort.Address
	if address == "" {
		address = "0.0.0.0"
	}
	mine := make(map[uint16]struct{}, len(m.cfg.Nodes))
	for _, node := range m.cfg.Nodes {
		if node.Port > 0 {
			mine[node.Port] = struct{}{}
		}
	}
	assigned := make(map[uint16]struct{}, len(m.cfg.Nodes))
	cursor := uint32(base)
	for i := range m.cfg.Nodes {
		m.cfg.Nodes[i].Port = 0
		found := false
		for cursor <= 65535 {
			candidate := uint16(cursor)
			cursor++
			if candidate == 0 {
				continue
			}
			if _, taken := assigned[candidate]; taken {
				continue
			}
			if m.cfg.Mode == "hybrid" && candidate == m.cfg.Listener.Port {
				continue
			}
			if _, held := mine[candidate]; !held && !config.IsPortAvailable(address, candidate) {
				continue
			}
			m.cfg.Nodes[i].Port = candidate
			assigned[candidate] = struct{}{}
			found = true
			break
		}
		if !found {
			return fmt.Errorf("no available ports found starting from %d", base)
		}
		if m.cfg.Nodes[i].Username == "" {
			m.cfg.Nodes[i].Username = m.cfg.MultiPort.Username
			m.cfg.Nodes[i].Password = m.cfg.MultiPort.Password
		}
	}
	return nil
}

// sortStrings sorts in place without pulling sort into every call site.
func sortStrings(values []string) {
	for i := 1; i < len(values); i++ {
		for j := i; j > 0 && values[j] < values[j-1]; j-- {
			values[j], values[j-1] = values[j-1], values[j]
		}
	}
}
