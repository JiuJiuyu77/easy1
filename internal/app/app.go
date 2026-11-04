package app

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"easy_proxies/internal/backup"
	"easy_proxies/internal/boxmgr"
	"easy_proxies/internal/builder"
	"easy_proxies/internal/config"
	"easy_proxies/internal/importer"
	"easy_proxies/internal/monitor"
	"easy_proxies/internal/subscription"
)

// Run builds the runtime components from config and blocks until shutdown.
func Run(ctx context.Context, cfg *config.Config) error {
	if cfg == nil {
		return errors.New("application requires config")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	startupStarted := time.Now()

	// Build monitor config
	proxyUsername := cfg.Listener.Username
	proxyPassword := cfg.Listener.Password
	if cfg.Mode == "multi-port" || cfg.Mode == "hybrid" {
		proxyUsername = cfg.MultiPort.Username
		proxyPassword = cfg.MultiPort.Password
	}
	monitorCfg := monitor.Config{
		Enabled:          cfg.ManagementEnabled(),
		Listen:           cfg.Management.Listen,
		ProbeTarget:      cfg.Management.ProbeTarget,
		ProbeTargets:     cfg.Management.ProbeTargets,
		ProbePolicy:      cfg.ProbePolicyOrDefault(),
		Password:         cfg.Management.Password,
		ProxyUsername:    proxyUsername,
		ProxyPassword:    proxyPassword,
		ExternalIP:       cfg.ExternalIP,
		ProbeConcurrency: cfg.ProbeConcurrencyOrDefault(),
		PprofEnabled:     cfg.Management.PprofEnabled,
	}

	// Create the BoxManager. The management UI may start before the proxy core:
	// importing nodes from the WebUI is how a fresh install gets its first pool.
	boxMgr := boxmgr.New(cfg, monitorCfg)
	defer boxMgr.Close()

	monitorStarted := time.Now()
	if err := boxMgr.EnsureMonitor(ctx); err != nil {
		return fmt.Errorf("init monitor server: %w", err)
	}
	monitorDuration := time.Since(monitorStarted)
	if server := boxMgr.MonitorServer(); server != nil {
		server.SetConfig(cfg)
	}

	// Managed node store (the WebUI's candidate/pool database).
	storeStarted := time.Now()
	nodeStore, err := importer.NewStore(filepath.Join(filepath.Dir(cfg.FilePath()), "managed_nodes.json"))
	if err != nil {
		return fmt.Errorf("create node store: %w", err)
	}
	defer nodeStore.Close()
	storeDuration := time.Since(storeStarted)

	// The pool is the single source of truth for sing-box listeners. Filter the
	// configured nodes to pool members before the core starts so the listener
	// count, the WebUI pool count and config.yaml agree. A fresh install (empty
	// pool) keeps whatever the config/subscription produced until the first
	// promote.
	if poolNodes := nodeStore.ListPoolNodes(); len(poolNodes) > 0 {
		poolByRoute := make(map[string]importer.ManagedNode, len(poolNodes))
		for _, pn := range poolNodes {
			if pn.URI != "" {
				poolByRoute[pn.URI+"\x00"+pn.ChainProfileID] = pn
			}
		}
		filtered := make([]config.NodeConfig, 0, len(poolByRoute))
		seen := make(map[string]struct{}, len(poolByRoute))
		for _, n := range cfg.Nodes {
			key := n.URI + "\x00" + n.ChainProfileID
			if pn, ok := poolByRoute[key]; ok {
				if pn.Name != "" {
					n.Name = pn.Name
				}
				filtered = append(filtered, n)
				seen[key] = struct{}{}
			}
		}
		// Include pool entries that have no matching subscription node yet
		// (typed in by hand, or kept while their source is being refreshed).
		for route, pn := range poolByRoute {
			if _, ok := seen[route]; ok {
				continue
			}
			filtered = append(filtered, pn.ToConfigNode())
		}
		cfg.Nodes = filtered
		// Let the rebuild hand out contiguous ports from base_port instead of
		// reusing values that were assigned in a previous run.
		for i := range cfg.Nodes {
			cfg.Nodes[i].Port = 0
		}
		if err := boxMgr.RebuildPortAssignments(); err != nil {
			return fmt.Errorf("rebuild port assignments: %w", err)
		}
		if err := syncPoolRuntimePorts(nodeStore, poolNodes, cfg.Nodes); err != nil {
			return fmt.Errorf("sync rebuilt port assignments: %w", err)
		}
	}

	testerOptions := []importer.TesterOption{
		importer.WithProbeTarget(cfg.Management.ProbeTarget),
		importer.WithTesterTimeout(cfg.SubscriptionRefresh.HealthCheckTimeout),
		importer.WithSkipCertVerify(cfg.SkipCertVerify),
		importer.WithChainProfiles(cfg.ChainProfiles),
		importer.WithChainOutboundBuilder(builder.BuildChainOutbounds),
	}
	if cfg.Mode == "multi-port" || cfg.Mode == "hybrid" {
		testerOptions = append(testerOptions, importer.WithRuntimeProxy(
			cfg.MultiPort.Address,
			cfg.MultiPort.Username,
			cfg.MultiPort.Password,
		))
	}
	tester := importer.NewNodeTester(builder.BuildSingleNodeOutbound, testerOptions...)
	defer tester.Close()

	importSvc := importer.NewService(nodeStore, tester, boxMgr,
		importer.WithRefreshSourceTimeout(cfg.SubscriptionRefresh.Timeout),
	)
	defer func() {
		closeCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		_ = importSvc.Close(closeCtx)
	}()
	if server := boxMgr.MonitorServer(); server != nil {
		server.SetImportService(importSvc)
	}

	// Start the proxy core only when there is something to serve.
	coreStarted := false
	runtimeStarted := time.Now()
	if len(cfg.Nodes) == 0 {
		fmt.Println("No pool nodes configured; WebUI is available for importing nodes.")
	} else {
		if err := boxMgr.Start(ctx); err != nil {
			return fmt.Errorf("start box manager: %w", err)
		}
		coreStarted = true
	}
	boxMgr.SetStartupTimings(map[string]int64{
		"monitor":     monitorDuration.Milliseconds(),
		"state_store": storeDuration.Milliseconds(),
		"runtime":     time.Since(runtimeStarted).Milliseconds(),
		"total":       time.Since(startupStarted).Milliseconds(),
	})

	// Always create SubscriptionManager so WebUI can hot-reload subscription
	// config; with managed import sources it delegates the refresh to the
	// importer instead of loading every subscription node into the runtime.
	subMgr := subscription.New(cfg, boxMgr)
	subMgr.SetSourceRefresher(importSvc)
	defer subMgr.Stop()
	subMgr.Start()

	if server := boxMgr.MonitorServer(); server != nil {
		server.SetSubscriptionRefresher(subMgr)
		server.SetBackupService(backup.NewService(cfg, nodeStore, boxMgr, subMgr, importSvc))
	}

	// Wait for shutdown signal
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	defer signal.Stop(sigCh)

	select {
	case <-ctx.Done():
		fmt.Println("Context cancelled, initiating graceful shutdown...")
	case sig := <-sigCh:
		fmt.Printf("Received %s, initiating graceful shutdown...\n", sig)
	}

	// Create shutdown context with timeout
	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer shutdownCancel()

	fmt.Println("Stopping subscription manager...")
	subMgr.Stop()

	fmt.Println("Stopping import jobs...")
	if err := importSvc.Close(shutdownCtx); err != nil {
		fmt.Printf("Error stopping import jobs: %v\n", err)
	}

	fmt.Println("Stopping box manager...")
	if coreStarted {
		if err := boxMgr.CloseContext(shutdownCtx); err != nil {
			fmt.Printf("Error closing box manager: %v\n", err)
		}
	} else if err := boxMgr.Close(); err != nil {
		fmt.Printf("Error closing box manager: %v\n", err)
	}

	fmt.Println("Waiting for connections to drain...")
	select {
	case <-time.After(2 * time.Second):
		fmt.Println("Graceful shutdown completed")
	case <-shutdownCtx.Done():
		fmt.Println("Shutdown timeout exceeded, forcing exit")
	}
	return nil
}

// syncPoolRuntimePorts writes the ports the runtime actually assigned back into
// the pool database so both views stay identical across restarts.
func syncPoolRuntimePorts(store *importer.Store, poolNodes []importer.ManagedNode, configNodes []config.NodeConfig) error {
	if store == nil || len(poolNodes) == 0 {
		return nil
	}
	byRoute := make(map[string]config.NodeConfig, len(configNodes))
	for _, node := range configNodes {
		byRoute[node.URI+"\x00"+node.ChainProfileID] = node
	}
	updates := make([]importer.ManagedNode, 0)
	for _, node := range poolNodes {
		configured, ok := byRoute[node.URI+"\x00"+node.ChainProfileID]
		if !ok || configured.Port == 0 {
			return errors.New("runtime port missing for pool node")
		}
		if node.Port == configured.Port && node.Name == configured.Name {
			continue
		}
		node.Port = configured.Port
		node.Name = configured.Name
		updates = append(updates, node)
	}
	if len(updates) == 0 {
		return nil
	}
	return store.UpsertNodes(updates)
}
