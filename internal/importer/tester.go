package importer

import (
	"bytes"
	"context"
	"crypto/sha256"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"reflect"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"
	"unsafe"

	"easy_proxies/internal/proxychain"

	"github.com/sagernet/sing-box"
	"github.com/sagernet/sing-box/adapter"
	C "github.com/sagernet/sing-box/constant"
	"github.com/sagernet/sing-box/include"
	"github.com/sagernet/sing-box/option"
	"github.com/sagernet/sing/common"
	"github.com/sagernet/sing/common/json/badoption"
	M "github.com/sagernet/sing/common/metadata"
)

const (
	DefaultProbeTarget      = "https://www.gstatic.com/generate_204"
	AlternateProbeTarget    = "https://cp.cloudflare.com/generate_204"
	DefaultProbeTimeout     = 5 * time.Second
	probeRounds             = 3
	defaultProbeConcurrency = 32
	defaultProbeRetryDelay  = time.Second
)

type OutboundBuilder func(tag, uri string, skipCertVerify bool) (option.Outbound, error)
type ChainOutboundBuilder func(tag, terminalURI string, hopURIs []string, skipCertVerify bool) ([]option.Outbound, error)

type probeSession interface {
	Probe(ctx context.Context, target string) TestResult
	Close()
}

type probeSessionFactory func(ctx context.Context, node ManagedNode, timeout time.Duration) (probeSession, error)

type permanentProbeError struct {
	cause error
}

func (e *permanentProbeError) Error() string { return e.cause.Error() }
func (e *permanentProbeError) Unwrap() error { return e.cause }

func permanentProbeFailure(err error) error {
	if err == nil {
		return nil
	}
	return &permanentProbeError{cause: err}
}

func retryableProbeFailure(err error) bool {
	if err == nil || errors.Is(err, context.Canceled) {
		return false
	}
	var permanent *permanentProbeError
	return !errors.As(err, &permanent)
}

type NodeTester struct {
	probeTarget          string
	probeTargets         []string
	probePolicy          string
	probeMu              sync.RWMutex
	boxSemOnce           sync.Once
	boxSem               chan struct{}
	ipinfoURL            string
	timeout              time.Duration
	concurrency          int
	skipCertVerify       bool
	buildOutbound        OutboundBuilder
	buildChainOutbounds  ChainOutboundBuilder
	retryDelay           time.Duration
	runtimeProxyAddress  string
	runtimeProxyUsername string
	runtimeProxyPassword string
	probeOverride        func(context.Context, ManagedNode, string, time.Duration) TestResult
	sessionFactory       probeSessionFactory
	batchSem             chan struct{}
	schedulerOnce        sync.Once
	scheduler            *probeScheduler
	runtimeOnce          sync.Once
	runtime              *sharedProbeRuntime
	runtimeErr           error
	fallbacks            atomic.Uint64
	closeOnce            sync.Once
	closeErr             error
	chainMu              sync.RWMutex
	chainProfiles        map[string]proxychain.Profile
	chainSessionID       atomic.Uint64
}

type TesterOption func(*NodeTester)

func NewNodeTester(buildFn OutboundBuilder, opts ...TesterOption) *NodeTester {
	t := &NodeTester{
		probeTarget:   DefaultProbeTarget,
		ipinfoURL:     "https://ipinfo.io/json",
		timeout:       DefaultProbeTimeout,
		concurrency:   defaultProbeConcurrency,
		buildOutbound: buildFn,
		retryDelay:    defaultProbeRetryDelay,
		chainProfiles: make(map[string]proxychain.Profile),
	}
	for _, opt := range opts {
		opt(t)
	}
	if t.concurrency < 1 {
		t.concurrency = 1
	}
	t.sessionFactory = t.newProbeSession
	t.batchSem = make(chan struct{}, t.concurrency)
	return t
}

func WithChainProfiles(profiles []proxychain.Profile) TesterOption {
	return func(t *NodeTester) {
		_ = t.SetChainProfiles(profiles)
	}
}

func WithChainOutboundBuilder(buildFn ChainOutboundBuilder) TesterOption {
	return func(t *NodeTester) {
		t.buildChainOutbounds = buildFn
	}
}

func (t *NodeTester) SetChainProfiles(profiles []proxychain.Profile) error {
	normalized, err := proxychain.NormalizeProfiles(profiles)
	if err != nil {
		return err
	}
	byID := make(map[string]proxychain.Profile, len(normalized))
	for _, profile := range normalized {
		byID[profile.ID] = profile
	}
	t.chainMu.Lock()
	t.chainProfiles = byID
	t.chainMu.Unlock()
	return nil
}

func (t *NodeTester) ChainProfile(id string) (proxychain.Profile, bool) {
	t.chainMu.RLock()
	defer t.chainMu.RUnlock()
	profile, ok := t.chainProfiles[strings.TrimSpace(id)]
	return profile, ok
}

func (t *NodeTester) ChainProfiles() []proxychain.Profile {
	t.chainMu.RLock()
	defer t.chainMu.RUnlock()
	profiles := make([]proxychain.Profile, 0, len(t.chainProfiles))
	for _, profile := range t.chainProfiles {
		profiles = append(profiles, profile)
	}
	sort.Slice(profiles, func(i, j int) bool { return profiles[i].Name < profiles[j].Name })
	return profiles
}

func WithProbeTarget(target string) TesterOption {
	return func(t *NodeTester) {
		if target != "" {
			t.probeTarget = target
		}
	}
}

// WithProbeTargets configures the ordered reachability checks used by node
// tests. policy "all" requires every target to answer, anything else treats one
// answering target as success.
func WithProbeTargets(targets []string, policy string) TesterOption {
	return func(t *NodeTester) {
		cleaned := make([]string, 0, len(targets))
		for _, raw := range targets {
			if value := strings.TrimSpace(raw); value != "" {
				cleaned = append(cleaned, value)
			}
		}
		if len(cleaned) > 0 {
			t.probeTargets = cleaned
		}
		if strings.TrimSpace(policy) != "" {
			t.probePolicy = policy
		}
	}
}

func WithIPInfoURL(u string) TesterOption {
	return func(t *NodeTester) {
		if u != "" {
			t.ipinfoURL = u
		}
	}
}

func WithTesterTimeout(d time.Duration) TesterOption {
	return func(t *NodeTester) {
		if d > 0 {
			t.timeout = d
		}
	}
}

func WithTesterConcurrency(n int) TesterOption {
	return func(t *NodeTester) {
		if n > 0 {
			t.concurrency = n
		}
	}
}

func WithSkipCertVerify(skip bool) TesterOption {
	return func(t *NodeTester) {
		t.skipCertVerify = skip
	}
}

func WithRuntimeProxy(address, username, password string) TesterOption {
	return func(t *NodeTester) {
		t.runtimeProxyAddress = runtimeProxyHost(address)
		t.runtimeProxyUsername = username
		t.runtimeProxyPassword = password
	}
}

func (t *NodeTester) Test(ctx context.Context, node ManagedNode) (result TestResult) {
	defer recoverTestResult(&result)

	client, closeClient, err := t.clientForNode(ctx, node)
	if err != nil {
		return TestResult{Error: err}
	}
	defer closeClient()

	start := time.Now()
	if err := t.probeWithRetry(ctx, client); err != nil {
		return TestResult{Error: err}
	}
	latency := time.Since(start).Milliseconds()

	countryCtx, cancel := context.WithTimeout(ctx, minDuration(t.timeout/3, 5*time.Second))
	defer cancel()
	countryCode, countryName, _ := t.lookupCountry(countryCtx, client)
	return TestResult{
		LatencyMs:   latency,
		CountryCode: strings.ToUpper(countryCode),
		CountryName: countryName,
	}
}

// currentProbePolicy reports "any" unless "all" was configured.
func (t *NodeTester) currentProbePolicy() string {
	t.probeMu.RLock()
	policy := t.probePolicy
	t.probeMu.RUnlock()
	if strings.EqualFold(strings.TrimSpace(policy), "all") {
		return "all"
	}
	return "any"
}

// probeTargetLabel is what progress events show for the configured targets.
func probeTargetLabel(targets []string, policy string) string {
	switch len(targets) {
	case 0:
		return ""
	case 1:
		return targets[0]
	default:
		mode := "任一通过"
		if strings.EqualFold(policy, "all") {
			mode = "全部通过"
		}
		return fmt.Sprintf("%d 个探测目标（%s）", len(targets), mode)
	}
}

// probeNodeTargets runs one node against every configured target and combines
// the results per policy: "any" succeeds on the first answering target, "all"
// requires every target to answer.
func (t *NodeTester) probeNodeTargets(ctx context.Context, node ManagedNode, targets []string, policy string, timeout time.Duration, probe func(context.Context, ManagedNode, string, time.Duration) TestResult) TestResult {
	if len(targets) == 0 {
		return TestResult{Error: errors.New("没有配置探测目标")}
	}
	requireAll := strings.EqualFold(strings.TrimSpace(policy), "all")
	var firstErr error
	var lastOK TestResult
	worst := int64(0)
	passed := 0
	for _, target := range targets {
		if err := ctx.Err(); err != nil {
			if firstErr == nil {
				firstErr = err
			}
			break
		}
		result := probe(ctx, node, target, timeout)
		if result.Error == nil {
			passed++
			lastOK = result
			if result.LatencyMs > worst {
				worst = result.LatencyMs
			}
			if !requireAll {
				return result
			}
			continue
		}
		if firstErr == nil {
			firstErr = fmt.Errorf("%s: %w", target, result.Error)
		}
		if requireAll {
			return TestResult{Error: firstErr}
		}
	}
	if requireAll && passed == len(targets) {
		lastOK.LatencyMs = worst
		return lastOK
	}
	if firstErr == nil {
		firstErr = errors.New("探测失败")
	}
	if len(targets) > 1 {
		return TestResult{Error: fmt.Errorf("%w（%d 个探测目标全部失败）", firstErr, len(targets))}
	}
	return TestResult{Error: firstErr}
}

func (t *NodeTester) Probe(ctx context.Context, node ManagedNode) (result TestResult) {
	defer recoverTestResult(&result)
	for event := range t.probeBatchWithProgress(ctx, []ManagedNode{node}, nil, probePriorityHigh) {
		return event.Result
	}
	if err := ctx.Err(); err != nil {
		return TestResult{Error: err}
	}
	return TestResult{Error: fmt.Errorf("probe ended without result")}
}

func (t *NodeTester) LookupCountry(ctx context.Context, node ManagedNode) (result TestResult) {
	defer recoverTestResult(&result)

	client, closeClient, err := t.clientForNode(ctx, node)
	if err != nil {
		return TestResult{Error: err}
	}
	defer closeClient()

	countryCode, countryName, err := t.lookupCountry(ctx, client)
	if err != nil {
		return TestResult{Error: err}
	}
	return TestResult{
		CountryCode: strings.ToUpper(countryCode),
		CountryName: countryName,
	}
}

func recoverTestResult(result *TestResult) {
	if r := recover(); r != nil {
		*result = TestResult{Error: fmt.Errorf("node test panic: %v", r)}
	}
}

func (t *NodeTester) clientForNode(ctx context.Context, node ManagedNode) (*http.Client, func(), error) {
	return t.clientForNodeWithTimeout(ctx, node, t.timeout)
}

func (t *NodeTester) clientForNodeWithTimeout(ctx context.Context, node ManagedNode, timeout time.Duration) (*http.Client, func(), error) {
	tag := "test-" + safeTagPart(node.ID)
	outbound, err := t.buildOutbound(tag, node.URI, t.skipCertVerify)
	if err != nil {
		return nil, nil, fmt.Errorf("build outbound: %w", err)
	}
	return t.clientForOutboundWithTimeout(ctx, tag, outbound, timeout)
}

func (t *NodeTester) clientForOutboundWithTimeout(ctx context.Context, tag string, outbound option.Outbound, timeout time.Duration) (*http.Client, func(), error) {
	// Hold a slot for the lifetime of the instance so a wide batch cannot spawn
	// dozens of listeners at once.
	sem := t.testBoxSlot()
	select {
	case sem <- struct{}{}:
	case <-ctx.Done():
		return nil, nil, ctx.Err()
	}
	releaseSlot := func() { <-sem }
	instance, port, err := t.startProxyBoxRetry(ctx, tag, outbound)
	if err != nil {
		releaseSlot()
		return nil, nil, err
	}

	proxyURL, err := url.Parse(fmt.Sprintf("http://127.0.0.1:%d", port))
	if err != nil {
		_ = instance.Close()
		return nil, nil, err
	}
	transport := &http.Transport{
		Proxy: http.ProxyURL(proxyURL),
		// Same rule as newDirectProbeSession: probes judge reachability, not the
		// target's identity. Airports re-sign TLS, so verifying here turned
		// healthy nodes into "TLS 校验失败".
		TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
	}
	client := &http.Client{
		Timeout:   timeout,
		Transport: transport,
	}
	return client, func() {
		transport.CloseIdleConnections()
		_ = instance.Close()
		releaseSlot()
	}, nil
}

type httpProbeSession struct {
	tester    *NodeTester
	client    *http.Client
	closeOnce sync.Once
	close     func()
}

func (s *httpProbeSession) Probe(ctx context.Context, target string) TestResult {
	start := time.Now()
	if err := s.tester.probeTargetURL(ctx, s.client, target); err != nil {
		return TestResult{Error: err}
	}
	return TestResult{LatencyMs: time.Since(start).Milliseconds()}
}

func (s *httpProbeSession) Close() {
	s.closeOnce.Do(s.close)
}

func (t *NodeTester) newBoxProbeSession(ctx context.Context, node ManagedNode, timeout time.Duration) (probeSession, error) {
	client, closeClient, err := t.clientForNodeWithTimeout(ctx, node, timeout)
	if err != nil {
		return nil, err
	}
	return &httpProbeSession{tester: t, client: client, close: closeClient}, nil
}

type directProbeSession struct {
	tester    *NodeTester
	outbound  adapter.Outbound
	client    *http.Client
	transport *http.Transport
	closeOnce sync.Once
	cleanup   func()
}

func newDirectProbeSession(tester *NodeTester, outbound adapter.Outbound, timeout time.Duration) *directProbeSession {
	transport := &http.Transport{
		DialContext: func(ctx context.Context, network, address string) (net.Conn, error) {
			return outbound.DialContext(ctx, network, M.ParseSocksaddr(address))
		},
		// Probing answers "can this node reach the target at all". Airports and
		// middleboxes routinely re-sign traffic, so verifying the *target's*
		// certificate here would fail healthy nodes; only the node's own
		// outbound TLS honours skip_cert_verify.
		TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
	}
	return &directProbeSession{
		tester:    tester,
		outbound:  outbound,
		client:    &http.Client{Transport: transport, Timeout: timeout},
		transport: transport,
	}
}

func (s *directProbeSession) Probe(ctx context.Context, target string) TestResult {
	start := time.Now()
	if err := s.tester.probeTargetURL(ctx, s.client, target); err != nil {
		return TestResult{Error: err}
	}
	return TestResult{LatencyMs: time.Since(start).Milliseconds()}
}

func (s *directProbeSession) Close() {
	s.closeOnce.Do(func() {
		s.transport.CloseIdleConnections()
		if s.cleanup != nil {
			s.cleanup()
		} else {
			_ = common.Close(s.outbound)
		}
	})
}

func (t *NodeTester) sharedRuntime() (*sharedProbeRuntime, error) {
	t.runtimeOnce.Do(func() {
		t.runtime, t.runtimeErr = newSharedProbeRuntime()
	})
	return t.runtime, t.runtimeErr
}

func (t *NodeTester) newProbeSession(ctx context.Context, node ManagedNode, timeout time.Duration) (probeSession, error) {
	if t.buildOutbound == nil {
		return nil, permanentProbeFailure(fmt.Errorf("outbound builder is not configured"))
	}
	tag := "test-" + safeTagPart(node.ID)
	if node.ChainProfileID != "" {
		if t.buildChainOutbounds == nil {
			return nil, permanentProbeFailure(fmt.Errorf("chain outbound builder is not configured"))
		}
		profile, ok := t.ChainProfile(node.ChainProfileID)
		if !ok || !profile.Enabled {
			return nil, permanentProbeFailure(fmt.Errorf("chain profile %q is unavailable", node.ChainProfileID))
		}
		hopURIs := make([]string, 0, len(profile.Hops))
		for _, hop := range profile.Hops {
			hopURIs = append(hopURIs, hop.URI)
		}
		tag = fmt.Sprintf("%s-%d", tag, t.chainSessionID.Add(1))
		configs, err := t.buildChainOutbounds(tag, node.URI, hopURIs, t.skipCertVerify)
		if err != nil {
			return nil, permanentProbeFailure(err)
		}
		runtime, runtimeErr := t.sharedRuntime()
		if runtimeErr != nil {
			return nil, runtimeErr
		}
		outbound, cleanup, err := runtime.BuildChain(configs)
		if err != nil {
			return nil, err
		}
		session := newDirectProbeSession(t, outbound, timeout)
		session.cleanup = cleanup
		return session, nil
	}
	config, err := t.buildOutbound(tag, node.URI, t.skipCertVerify)
	if err != nil {
		return nil, permanentProbeFailure(fmt.Errorf("build outbound: %w", err))
	}
	runtime, runtimeErr := t.sharedRuntime()
	if runtimeErr == nil {
		outbound, buildErr := runtime.Build(config)
		if buildErr == nil {
			return newDirectProbeSession(t, outbound, timeout), nil
		}
		runtimeErr = buildErr
	}

	client, closeClient, fallbackErr := t.clientForOutboundWithTimeout(ctx, tag, config, timeout)
	if fallbackErr != nil {
		return nil, fmt.Errorf("shared probe runtime: %v; compatibility fallback: %w", runtimeErr, fallbackErr)
	}
	t.fallbacks.Add(1)
	return &httpProbeSession{tester: t, client: client, close: closeClient}, nil
}

// chainProbeURL is what a chain profile is validated against. GitHub is used
// instead of a generate_204 endpoint: reaching a tiny 204 proves very little
// about a relay chain, while GitHub exercises DNS, TCP, TLS and a real page —
// the same target the import page offers as a site check.
const chainProbeURL = "https://github.com/"

// probeChainTarget fetches target through the given client (which dials through
// the chain) and requires a 2xx page carrying the github marker.
func probeChainPage(ctx context.Context, client *http.Client, target string) error {
	if strings.TrimSpace(target) == "" {
		target = chainProbeURL
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	if err != nil {
		return err
	}
	req.Header.Set("User-Agent", "Mozilla/5.0 (compatible; easy_proxies chain probe)")
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("GitHub 前置探测失败: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("GitHub 前置探测返回 HTTP %d（期望 2xx）", resp.StatusCode)
	}
	body, readErr := io.ReadAll(io.LimitReader(resp.Body, 256<<10))
	if len(body) == 0 {
		if readErr != nil {
			return fmt.Errorf("读取 GitHub 响应失败: %w", readErr)
		}
		return errors.New("GitHub 前置探测返回了空响应")
	}
	if !bytes.Contains(bytes.ToLower(body), []byte("github")) {
		return errors.New("GitHub 前置探测拿到的页面不含 github 标记：可能被劫持或返回了错误页")
	}
	return nil
}

func (t *NodeTester) TestChainProfile(ctx context.Context, profile proxychain.Profile) TestResult {
	if t.buildChainOutbounds == nil {
		return TestResult{Error: fmt.Errorf("chain outbound builder is not configured")}
	}
	normalized, err := proxychain.NormalizeProfile(profile)
	if err != nil {
		return TestResult{Error: err}
	}
	hopURIs := make([]string, 0, len(normalized.Hops))
	for _, hop := range normalized.Hops {
		hopURIs = append(hopURIs, hop.URI)
	}
	tag := fmt.Sprintf("chain-baseline-%s-%d", safeTagPart(normalized.ID), t.chainSessionID.Add(1))
	configs, err := t.buildChainOutbounds(tag, hopURIs[len(hopURIs)-1], hopURIs[:len(hopURIs)-1], t.skipCertVerify)
	if err != nil {
		return TestResult{Error: err}
	}
	runtime, err := t.sharedRuntime()
	if err != nil {
		return TestResult{Error: err}
	}
	outbound, cleanup, err := runtime.BuildChain(configs)
	if err != nil {
		return TestResult{Error: err}
	}
	session := newDirectProbeSession(t, outbound, t.timeout)
	session.cleanup = cleanup
	defer session.Close()
	start := time.Now()
	if err := probeChainPage(ctx, session.client, chainProbeURL); err != nil {
		return TestResult{Error: err}
	}
	return TestResult{LatencyMs: time.Since(start).Milliseconds()}
}

func (t *NodeTester) FetchViaChain(ctx context.Context, profile proxychain.Profile, rawURL string, headers http.Header, timeout time.Duration) ([]byte, error) {
	if t.buildChainOutbounds == nil {
		return nil, fmt.Errorf("chain outbound builder is not configured")
	}
	if timeout <= 0 {
		timeout = t.timeout
	}
	normalized, err := proxychain.NormalizeProfile(profile)
	if err != nil {
		return nil, err
	}
	hopURIs := make([]string, 0, len(normalized.Hops))
	for _, hop := range normalized.Hops {
		hopURIs = append(hopURIs, hop.URI)
	}
	tag := fmt.Sprintf("chain-fetch-%s-%d", safeTagPart(normalized.ID), t.chainSessionID.Add(1))
	configs, err := t.buildChainOutbounds(tag, hopURIs[len(hopURIs)-1], hopURIs[:len(hopURIs)-1], t.skipCertVerify)
	if err != nil {
		return nil, err
	}
	runtime, err := t.sharedRuntime()
	if err != nil {
		return nil, err
	}
	outbound, cleanup, err := runtime.BuildChain(configs)
	if err != nil {
		return nil, err
	}
	session := newDirectProbeSession(t, outbound, timeout)
	session.cleanup = cleanup
	defer session.Close()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, err
	}
	if headers != nil {
		req.Header = headers.Clone()
	}
	resp, err := session.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("subscription returned HTTP %d", resp.StatusCode)
	}
	const maxBody = 10 << 20
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxBody+1))
	if err != nil {
		return nil, err
	}
	if len(body) > maxBody {
		return nil, fmt.Errorf("subscription response exceeds 10MB")
	}
	return body, nil
}

func (t *NodeTester) CompatibilityFallbacks() uint64 {
	return t.fallbacks.Load()
}

func (t *NodeTester) Close() error {
	t.closeOnce.Do(func() {
		t.probeScheduler().Close()
		if t.runtime != nil {
			t.closeErr = t.runtime.Close()
		}
	})
	return t.closeErr
}

func (t *NodeTester) ProbeSchedulerStats() ProbeSchedulerStats {
	return t.probeScheduler().Stats()
}

func (t *NodeTester) TestBatch(ctx context.Context, nodes []ManagedNode) <-chan NodeTestEvent {
	return t.runBatch(ctx, nodes, t.Test)
}

func (t *NodeTester) ProbeBatch(ctx context.Context, nodes []ManagedNode) <-chan NodeTestEvent {
	return t.ProbeBatchWithProgress(ctx, nodes, nil)
}

func (t *NodeTester) ProbeBatchWithProgress(ctx context.Context, nodes []ManagedNode, onRound func(ProbeRoundProgress)) <-chan NodeTestEvent {
	return t.probeBatchWithProgress(ctx, nodes, onRound, probePriorityNormal)
}

func (t *NodeTester) probeBatchWithProgress(ctx context.Context, nodes []ManagedNode, onRound func(ProbeRoundProgress), priority probePriority) <-chan NodeTestEvent {
	events := make(chan NodeTestEvent)
	go func() {
		defer close(events)
		sessions := make(map[string]probeSession)
		var sessionsMu sync.Mutex
		defer func() {
			sessionsMu.Lock()
			defer sessionsMu.Unlock()
			for _, session := range sessions {
				session.Close()
			}
		}()
		closeSession := func(key string) {
			sessionsMu.Lock()
			session := sessions[key]
			delete(sessions, key)
			sessionsMu.Unlock()
			if session != nil {
				session.Close()
			}
		}
		probe := t.probeOverride
		if probe == nil {
			probe = func(nodeCtx context.Context, node ManagedNode, target string, timeout time.Duration) TestResult {
				key := node.ID + "\x00" + node.URI
				sessionsMu.Lock()
				session := sessions[key]
				sessionsMu.Unlock()
				if session == nil {
					created, err := t.sessionFactory(ctx, node, timeout)
					if err != nil {
						return TestResult{Error: err}
					}
					sessionsMu.Lock()
					if existing := sessions[key]; existing != nil {
						session = existing
						sessionsMu.Unlock()
						created.Close()
					} else {
						sessions[key] = created
						session = created
						sessionsMu.Unlock()
					}
				}
				result := session.Probe(nodeCtx, target)
				if result.Error == nil {
					result = t.probeRuntimeListener(nodeCtx, node, target, timeout, result)
				}
				if result.Error == nil || !retryableProbeFailure(result.Error) {
					closeSession(key)
				}
				return result
			}
		}
		pending := append([]ManagedNode(nil), nodes...)
		targets := t.effectiveProbeTargets()
		policy := t.currentProbePolicy()
		target := probeTargetLabel(targets, policy)
		for round := 1; round <= probeRounds && len(pending) > 0; round++ {
			if round > 1 {
				select {
				case <-ctx.Done():
					return
				case <-time.After(t.retryDelay):
				}
			}
			concurrency := probeRoundConcurrency(t.concurrency, round, len(pending))
			timeout := probeRoundTimeout(t.timeout, round)
			roundTotal := len(pending)
			roundCompleted := 0
			progressStep := max(1, (roundTotal+19)/20)
			reportProgress := func() {
				if onRound != nil {
					onRound(ProbeRoundProgress{
						Round: round, Rounds: probeRounds, Completed: roundCompleted, Total: roundTotal,
						Pending: roundTotal - roundCompleted, Target: target, Concurrency: concurrency,
					})
				}
			}
			reportProgress()
			next := make([]ManagedNode, 0, len(pending))
			byID := make(map[string]ManagedNode, len(pending))
			for _, node := range pending {
				byID[node.ID] = node
			}
			for event := range t.runProbeBatchWithConcurrency(ctx, pending, concurrency, timeout, target, priority, func(nodeCtx context.Context, node ManagedNode) TestResult {
				// Every node is checked against the configured target list; the
				// policy decides whether one or all of them must answer.
				return t.probeNodeTargets(nodeCtx, node, targets, policy, timeout, probe)
			}) {
				roundCompleted++
				if roundCompleted == roundTotal || roundCompleted%progressStep == 0 {
					reportProgress()
				}
				if event.Result.Error != nil && round < probeRounds && retryableProbeFailure(event.Result.Error) {
					if node, ok := byID[event.NodeID]; ok {
						next = append(next, node)
					}
					continue
				}
				if node, ok := byID[event.NodeID]; ok {
					closeSession(node.ID + "\x00" + node.URI)
				}
				select {
				case events <- event:
				case <-ctx.Done():
					return
				}
			}
			pending = next
		}
	}()
	return events
}

func (t *NodeTester) probeRuntimeListener(ctx context.Context, node ManagedNode, target string, timeout time.Duration, result TestResult) TestResult {
	if t.runtimeProxyAddress == "" || !node.InPool && node.State != StateInPool {
		return result
	}
	if node.Port == 0 {
		// No runtime port yet (not promoted, or the pool is rebuilding). The
		// direct probe already answered, so skip the extra check instead of
		// failing a healthy node.
		return result
	}

	proxyURL := &url.URL{
		Scheme: "http",
		Host:   net.JoinHostPort(t.runtimeProxyAddress, fmt.Sprintf("%d", node.Port)),
	}
	if t.runtimeProxyUsername != "" || t.runtimeProxyPassword != "" {
		proxyURL.User = url.UserPassword(t.runtimeProxyUsername, t.runtimeProxyPassword)
	}
	transport := &http.Transport{
		Proxy: http.ProxyURL(proxyURL),
		// See newDirectProbeSession: reachability probes never verify the
		// target's certificate.
		TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
	}
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport, Timeout: timeout}
	start := time.Now()
	if err := t.probeTargetURL(ctx, client, target); err != nil {
		return TestResult{Error: fmt.Errorf("runtime listener %s: %w", proxyURL.Host, err)}
	}
	result.LatencyMs = time.Since(start).Milliseconds()
	return result
}

func runtimeProxyHost(address string) string {
	host := strings.Trim(strings.TrimSpace(address), "[]")
	if host == "" {
		return ""
	}
	if parsed, err := netip.ParseAddr(host); err == nil && parsed.IsUnspecified() {
		if parsed.Is6() {
			return "::1"
		}
		return "127.0.0.1"
	}
	return host
}

func (t *NodeTester) probeScheduler() *probeScheduler {
	t.schedulerOnce.Do(func() {
		t.scheduler = newProbeScheduler(t.concurrency, probeQueueCapacity(t.concurrency))
	})
	return t.scheduler
}

func probeQueueCapacity(concurrency int) int {
	capacity := concurrency * 4
	if capacity < 256 {
		capacity = 256
	}
	if capacity > 4096 {
		capacity = 4096
	}
	return capacity
}

func probeTaskKey(node ManagedNode, target string) string {
	return fmt.Sprintf("%x", sha256.Sum256([]byte(node.ID+"\x00"+node.URI+"\x00health\x00"+target)))
}

func (t *NodeTester) runProbeBatchWithConcurrency(
	ctx context.Context,
	nodes []ManagedNode,
	concurrency int,
	timeout time.Duration,
	target string,
	priority probePriority,
	fn func(context.Context, ManagedNode) TestResult,
) <-chan NodeTestEvent {
	events := make(chan NodeTestEvent)
	go func() {
		defer close(events)
		if concurrency < 1 {
			concurrency = 1
		}
		if concurrency > len(nodes) {
			concurrency = len(nodes)
		}
		if concurrency == 0 {
			return
		}
		jobs := make(chan ManagedNode)
		var workers sync.WaitGroup
		for i := 0; i < concurrency; i++ {
			workers.Add(1)
			go func() {
				defer workers.Done()
				for node := range jobs {
					resultCh, err := t.probeScheduler().Submit(
						ctx,
						probeTaskKey(node, target),
						priority,
						timeout*2+1500*time.Millisecond,
						func(taskCtx context.Context) TestResult { return fn(taskCtx, node) },
					)
					result := TestResult{Error: err}
					if err == nil {
						select {
						case <-ctx.Done():
							return
						case result = <-resultCh:
						}
					}
					select {
					case events <- NodeTestEvent{NodeID: node.ID, Result: result}:
					case <-ctx.Done():
						return
					}
				}
			}()
		}
		for _, node := range nodes {
			select {
			case <-ctx.Done():
				close(jobs)
				workers.Wait()
				return
			case jobs <- node:
			}
		}
		close(jobs)
		workers.Wait()
	}()
	return events
}

func (t *NodeTester) CountryBatch(ctx context.Context, nodes []ManagedNode) <-chan NodeTestEvent {
	return t.runBatch(ctx, nodes, t.LookupCountry)
}

func (t *NodeTester) runBatch(ctx context.Context, nodes []ManagedNode, fn func(context.Context, ManagedNode) TestResult) <-chan NodeTestEvent {
	return t.runBatchWithConcurrency(ctx, nodes, t.concurrency, t.timeout, fn)
}

func (t *NodeTester) runBatchWithConcurrency(ctx context.Context, nodes []ManagedNode, concurrency int, timeout time.Duration, fn func(context.Context, ManagedNode) TestResult) <-chan NodeTestEvent {
	events := make(chan NodeTestEvent)
	go func() {
		defer close(events)
		if concurrency < 1 {
			concurrency = 1
		}
		sem := make(chan struct{}, concurrency)
		var wg sync.WaitGroup
		for _, node := range nodes {
			select {
			case <-ctx.Done():
				wg.Wait()
				return
			case sem <- struct{}{}:
			}
			wg.Add(1)
			go func(node ManagedNode) {
				defer wg.Done()
				defer func() { <-sem }()
				if t.batchSem != nil {
					select {
					case <-ctx.Done():
						return
					case t.batchSem <- struct{}{}:
					}
					defer func() { <-t.batchSem }()
				}
				nodeCtx, cancel := context.WithTimeout(ctx, timeout*2+1500*time.Millisecond)
				defer cancel()
				result := safeTestResult(func() TestResult {
					return fn(nodeCtx, node)
				})
				event := NodeTestEvent{NodeID: node.ID, Result: result}
				select {
				case events <- event:
				case <-ctx.Done():
				}
			}(node)
		}
		wg.Wait()
	}()
	return events
}

func (t *NodeTester) probeOnce(ctx context.Context, node ManagedNode, target string, timeout time.Duration) TestResult {
	client, closeClient, err := t.clientForNodeWithTimeout(ctx, node, timeout)
	if err != nil {
		return TestResult{Error: err}
	}
	defer closeClient()
	start := time.Now()
	if err := t.probeTargetURL(ctx, client, target); err != nil {
		return TestResult{Error: err}
	}
	return TestResult{LatencyMs: time.Since(start).Milliseconds()}
}

func probeRoundConcurrency(base, round, pending int) int {
	if base < 1 {
		base = 1
	}
	if pending >= base*4 {
		return base
	}
	for i := 1; i < round; i++ {
		if base > 2 {
			base = max(2, base/2)
		}
	}
	return base
}

func probeRoundTimeout(base time.Duration, _ int) time.Duration {
	if base <= 0 {
		base = DefaultProbeTimeout
	}
	return base
}

func safeTestResult(fn func() TestResult) (result TestResult) {
	defer recoverTestResult(&result)
	return fn()
}

// testBoxConcurrency caps how many throwaway sing-box instances may exist at
// once. Every node test starts one, and binding a fresh ephemeral port for each
// of them exhausts the local port range on big batches ("bind: cannot assign
// requested address"), which used to fail every remaining node.
const testBoxConcurrency = 8

func (t *NodeTester) testBoxSlot() chan struct{} {
	t.boxSemOnce.Do(func() {
		t.boxSem = make(chan struct{}, testBoxConcurrency)
	})
	return t.boxSem
}

// startProxyBoxRetry tolerates transient listener failures: port exhaustion and
// address-in-use clear up as sibling instances are closed.
func (t *NodeTester) startProxyBoxRetry(ctx context.Context, tag string, outbound option.Outbound) (*box.Box, uint16, error) {
	const attempts = 4
	var lastErr error
	for attempt := 1; attempt <= attempts; attempt++ {
		instance, port, err := startProxyBox(ctx, tag, outbound)
		if err == nil {
			return instance, port, nil
		}
		lastErr = err
		if !isTransientBindError(err) || ctx.Err() != nil {
			return nil, 0, err
		}
		if attempt == attempts {
			break
		}
		select {
		case <-ctx.Done():
			return nil, 0, ctx.Err()
		case <-time.After(time.Duration(attempt) * 150 * time.Millisecond):
		}
	}
	return nil, 0, lastErr
}

// isTransientBindError reports bind failures that are worth retrying.
func isTransientBindError(err error) bool {
	if err == nil {
		return false
	}
	message := strings.ToLower(err.Error())
	return strings.Contains(message, "cannot assign requested address") ||
		strings.Contains(message, "address already in use") ||
		strings.Contains(message, "too many open files")
}

func startProxyBox(ctx context.Context, outboundTag string, outbound option.Outbound) (*box.Box, uint16, error) {
	addr := badoption.Addr(netipAddr127())
	inboundTag := "test-in-" + safeTagPart(outboundTag)
	opts := option.Options{
		Log: &option.LogOptions{Level: "error"},
		Inbounds: []option.Inbound{
			{
				Type: C.TypeMixed,
				Tag:  inboundTag,
				Options: &option.HTTPMixedInboundOptions{
					ListenOptions: option.ListenOptions{
						Listen:     &addr,
						ListenPort: 0,
					},
				},
			},
		},
		Outbounds: []option.Outbound{outbound},
		Route:     &option.RouteOptions{Final: outboundTag},
	}
	instance, err := box.New(box.Options{Context: include.Context(ctx), Options: opts})
	if err != nil {
		return nil, 0, fmt.Errorf("create test box: %w", err)
	}
	if err := instance.Start(); err != nil {
		_ = instance.Close()
		return nil, 0, fmt.Errorf("start test box: %w", err)
	}
	port, err := boxInboundPort(instance, inboundTag)
	if err != nil {
		_ = instance.Close()
		return nil, 0, err
	}
	return instance, port, nil
}

func (t *NodeTester) probe(ctx context.Context, client *http.Client) error {
	targets := t.effectiveProbeTargets()
	t.probeMu.RLock()
	policy := t.probePolicy
	t.probeMu.RUnlock()
	return evaluateProbePolicy(targets, policy, func(probeCtx context.Context, target string) error {
		return t.probeTargetURL(probeCtx, client, target)
	}, ctx)
}

// SetProbeTargets swaps the probe list/policy at runtime so a settings change
// applies to the next test instead of waiting for a restart.
func (t *NodeTester) SetProbeTargets(targets []string, policy string) {
	cleaned := make([]string, 0, len(targets))
	for _, raw := range targets {
		if value := strings.TrimSpace(raw); value != "" {
			cleaned = append(cleaned, value)
		}
	}
	t.probeMu.Lock()
	t.probeTargets = cleaned
	if strings.TrimSpace(policy) != "" {
		t.probePolicy = policy
	}
	t.probeMu.Unlock()
}

// effectiveProbeTargets returns the configured list, or the legacy single target.
func (t *NodeTester) effectiveProbeTargets() []string {
	t.probeMu.RLock()
	targets := append([]string(nil), t.probeTargets...)
	policy := t.probePolicy
	t.probeMu.RUnlock()
	_ = policy
	if len(targets) > 0 {
		return targets
	}
	if strings.TrimSpace(t.probeTarget) != "" {
		return []string{t.probeTarget}
	}
	return nil
}

// evaluateProbePolicy runs one probe per target. With policy "all" every target
// must answer; otherwise the first success wins. It reports the most useful
// error when nothing passes.
func evaluateProbePolicy(targets []string, policy string, probe func(context.Context, string) error, ctx context.Context) error {
	if len(targets) == 0 {
		return errors.New("没有配置探测目标")
	}
	requireAll := strings.EqualFold(strings.TrimSpace(policy), "all")
	var firstErr error
	passed := 0
	for _, target := range targets {
		err := probe(ctx, target)
		if err == nil {
			passed++
			if !requireAll {
				return nil
			}
			continue
		}
		if firstErr == nil {
			firstErr = fmt.Errorf("%s: %w", target, err)
		}
		if requireAll {
			return firstErr
		}
		if ctx.Err() != nil {
			return firstErr
		}
	}
	if requireAll && passed == len(targets) {
		return nil
	}
	if firstErr != nil {
		if len(targets) > 1 {
			return fmt.Errorf("%w（%d 个探测目标全部失败）", firstErr, len(targets))
		}
		return firstErr
	}
	return errors.New("探测失败")
}

func (t *NodeTester) probeTargetURL(ctx context.Context, client *http.Client, target string) error {
	u, err := normalizeProbeURL(target)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return err
	}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("probe request failed: %w", err)
	}
	defer resp.Body.Close()
	io.Copy(io.Discard, resp.Body)
	// 204 endpoints are the classic probe, but custom targets may answer 200
	// (or another 2xx). Anything else means the request did not reach the site.
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("probe got HTTP %d, expected 2xx", resp.StatusCode)
	}
	return nil
}

func (t *NodeTester) probeWithRetry(ctx context.Context, client *http.Client) error {
	err := t.probe(ctx, client)
	if err == nil || ctx.Err() != nil {
		return err
	}
	timer := time.NewTimer(250 * time.Millisecond)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return err
	case <-timer.C:
	}
	if retryErr := t.probe(ctx, client); retryErr != nil {
		return fmt.Errorf("%s; retry failed: %w", classifyProbeError(err), retryErr)
	}
	return nil
}

func classifyProbeError(err error) string {
	if err == nil {
		return ""
	}
	var ne net.Error
	if errors.As(err, &ne) && ne.Timeout() {
		return "probe timeout"
	}
	lower := strings.ToLower(err.Error())
	if strings.Contains(lower, "timeout") || strings.Contains(lower, "deadline exceeded") {
		return "probe timeout"
	}
	if strings.Contains(lower, "certificate") || strings.Contains(lower, "x509") {
		// The target certificate is no longer verified by probes, so this comes
		// from the node's own TLS handshake with its server.
		return "节点 TLS 证书不被信任（如为自签/过期，可在设置里开启「跳过证书验证」）"
	}
	return err.Error()
}

func normalizeProbeURL(target string) (string, error) {
	target = strings.TrimSpace(target)
	if target == "" {
		target = DefaultProbeTarget
	}

	if !strings.HasPrefix(target, "http://") && !strings.HasPrefix(target, "https://") {
		// Plain HTTP on port 80 is the first thing middleboxes rewrite or rate
		// limit, so default to TLS unless the caller explicitly asked for :80.
		if strings.HasSuffix(target, ":80") {
			target = "http://" + target
		} else {
			target = "https://" + target
		}
	}

	u, err := url.Parse(target)
	if err != nil {
		return "", err
	}
	if u.Host == "" {
		return "", fmt.Errorf("invalid probe target %q", target)
	}
	// Only the known generate_204 endpoints get the implicit path; custom URLs
	// keep "/" (a GitHub target would 404 on /generate_204).
	if (u.Path == "" || u.Path == "/") && isGenerate204Host(u.Hostname()) {
		u.Path = "/generate_204"
	}
	return u.String(), nil
}

func isGenerate204Host(host string) bool {
	host = strings.ToLower(host)
	return strings.Contains(host, "gstatic.com") || strings.Contains(host, "cloudflare.com")
}

func (t *NodeTester) lookupIPInfo(ctx context.Context, client *http.Client) (string, string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, t.ipinfoURL, nil)
	if err != nil {
		return "", "", err
	}
	resp, err := client.Do(req)
	if err != nil {
		return "", "", fmt.Errorf("ipinfo: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		io.Copy(io.Discard, resp.Body)
		return "", "", fmt.Errorf("ipinfo status %d", resp.StatusCode)
	}
	var data struct {
		Country     string `json:"country"`
		CountryName string `json:"country_name"`
		City        string `json:"city"`
		Region      string `json:"region"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&data); err != nil {
		return "", "", fmt.Errorf("decode ipinfo: %w", err)
	}
	name := data.CountryName
	if name == "" {
		name = data.City
	}
	if name == "" {
		name = data.Region
	}
	if name == "" {
		name = strings.ToUpper(data.Country)
	}
	return data.Country, name, nil
}

func (t *NodeTester) lookupCountry(ctx context.Context, client *http.Client) (string, string, error) {
	if code, name, err := t.lookupIPInfo(ctx, client); err == nil {
		return code, name, nil
	}
	if code, name, err := lookupCountryJSON(ctx, client, "http://ip-api.com/json/?fields=status,countryCode,country", func(data map[string]any) (string, string, error) {
		if strings.EqualFold(fmt.Sprint(data["status"]), "success") {
			return fmt.Sprint(data["countryCode"]), fmt.Sprint(data["country"]), nil
		}
		return "", "", fmt.Errorf("ip-api status %v", data["status"])
	}); err == nil {
		return code, name, nil
	}
	return lookupCountryJSON(ctx, client, "https://api.country.is", func(data map[string]any) (string, string, error) {
		code := fmt.Sprint(data["country"])
		if code == "" || code == "<nil>" {
			return "", "", fmt.Errorf("country.is missing country")
		}
		return code, strings.ToUpper(code), nil
	})
}

func lookupCountryJSON(ctx context.Context, client *http.Client, endpoint string, parse func(map[string]any) (string, string, error)) (string, string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return "", "", err
	}
	resp, err := client.Do(req)
	if err != nil {
		return "", "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		io.Copy(io.Discard, resp.Body)
		return "", "", fmt.Errorf("%s status %d", endpoint, resp.StatusCode)
	}
	var data map[string]any
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&data); err != nil {
		return "", "", err
	}
	code, name, err := parse(data)
	return strings.ToUpper(code), name, err
}

func boxInboundPort(instance *box.Box, tag string) (uint16, error) {
	in, ok := instance.Inbound().Get(tag)
	if !ok {
		return 0, fmt.Errorf("test inbound %s not found", tag)
	}
	v := reflect.ValueOf(in)
	if v.Kind() == reflect.Pointer {
		v = v.Elem()
	}
	field := v.FieldByName("listener")
	if !field.IsValid() || !field.CanAddr() {
		return 0, fmt.Errorf("test inbound listener unavailable")
	}
	listenerValue := reflect.NewAt(field.Type(), unsafe.Pointer(field.UnsafeAddr())).Elem()
	tcpGetter, ok := listenerValue.Interface().(interface{ TCPListener() net.Listener })
	if !ok {
		return 0, fmt.Errorf("test inbound listener unsupported")
	}
	tcpListener := tcpGetter.TCPListener()
	if tcpListener == nil {
		return 0, fmt.Errorf("test inbound tcp listener unavailable")
	}
	tcpAddr, ok := tcpListener.Addr().(*net.TCPAddr)
	if !ok {
		return 0, fmt.Errorf("test inbound address %s is not tcp", tcpListener.Addr())
	}
	return uint16(tcpAddr.Port), nil
}

func safeTagPart(s string) string {
	if len(s) > 24 {
		s = s[:24]
	}
	s = strings.Map(func(r rune) rune {
		if r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '-' || r == '_' {
			return r
		}
		return '-'
	}, s)
	if s == "" {
		return "node"
	}
	return s
}

func netipAddr127() netip.Addr {
	return netip.AddrFrom4([4]byte{127, 0, 0, 1})
}

func minDuration(a, b time.Duration) time.Duration {
	if a < b {
		return a
	}
	return b
}
