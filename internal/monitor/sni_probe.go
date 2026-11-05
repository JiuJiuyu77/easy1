package monitor

import (
	"bufio"
	"context"
	"crypto/tls"
	"encoding/base64"
	"errors"
	"fmt"
	"net"
	"net/http"
	"strings"
	"time"
)

// sniProbeResult is one hop's TLS handshake outcome.
type sniProbeResult struct {
	OK bool `json:"ok"`
	// Unavailable means the hop could not be attempted at all (no listener,
	// refused, bad CONNECT) — the comparison is then inconclusive.
	Unavailable bool   `json:"unavailable,omitempty"`
	Millis      int64  `json:"ms"`
	Error       string `json:"error,omitempty"`
	PeerAddr    string `json:"peer,omitempty"`
}

// handleSNIProbe answers "is this SNI reset locally, or further out?" by making
// the same handshake twice: once straight from this process, once through the
// proxy listener (the path the app uses). No openssl/curl needed, which matters
// because the runtime image ships neither.
//
// GET /api/probes/sni?target=example.com[:443]&sni=example.com
func (s *Server) handleSNIProbe(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	target := strings.TrimSpace(r.URL.Query().Get("target"))
	if target == "" {
		writeStatusJSON(w, http.StatusBadRequest, map[string]any{"error": "缺少 target 参数"})
		return
	}
	sni := strings.TrimSpace(r.URL.Query().Get("sni"))
	host, port := splitTarget(target, "443")
	if sni == "" {
		sni = host
	}
	dialAddr := net.JoinHostPort(host, port)

	ctx, cancel := context.WithTimeout(r.Context(), 12*time.Second)
	defer cancel()

	direct := probeTLSHandshake(ctx, dialAddr, sni)
	viaPanel := probeTLSViaListener(ctx, s.proxyListenerAddress(), s.proxyListenerAuth(), dialAddr, sni)

	verdict := "未知"
	switch {
	case viaPanel.Unavailable:
		verdict = "代理入口不可用，无法比对（确认池内有节点且为 pool/hybrid 模式）"
	case direct.OK && !viaPanel.OK:
		verdict = "经面板这一跳被重置：链路其他位置（客户端→面板）或面板入口被中间层拦截"
	case !direct.OK && viaPanel.OK:
		verdict = "本机直连失败但经面板成功：出口正常，直连链路受限"
	case !direct.OK && !viaPanel.OK:
		verdict = "两跳都失败：目标不可达或本机出网受限（与面板规则无关）"
	default:
		verdict = "两跳均成功：未复现拦截"
	}

	writeJSON(w, map[string]any{
		"target":    dialAddr,
		"sni":       sni,
		"direct":    direct,
		"via_panel": viaPanel,
		"verdict":   verdict,
	})
}

// splitTarget returns host/port with a default port when none is given.
func splitTarget(target, defaultPort string) (string, string) {
	host, port, err := net.SplitHostPort(target)
	if err == nil && host != "" && port != "" {
		return host, port
	}
	return strings.Trim(target, "[]"), defaultPort
}

func probeTLSHandshake(ctx context.Context, dialAddr, sni string) sniProbeResult {
	start := time.Now()
	dialer := &net.Dialer{Timeout: 8 * time.Second}
	conn, err := dialer.DialContext(ctx, "tcp", dialAddr)
	if err != nil {
		return sniProbeResult{Millis: time.Since(start).Milliseconds(), Error: classifySNIError(err)}
	}
	defer conn.Close()
	return tlsHandshake(ctx, conn, sni, start)
}

func tlsHandshake(ctx context.Context, conn net.Conn, sni string, start time.Time) sniProbeResult {
	tlsConn := tls.Client(conn, &tls.Config{ServerName: sni, MinVersion: tls.VersionTLS12, InsecureSkipVerify: true})
	if deadline, ok := ctx.Deadline(); ok {
		_ = tlsConn.SetDeadline(deadline)
	}
	err := tlsConn.Handshake()
	millis := time.Since(start).Milliseconds()
	if err != nil {
		return sniProbeResult{Millis: millis, Error: classifySNIError(err), PeerAddr: conn.RemoteAddr().String()}
	}
	_ = tlsConn.Close()
	return sniProbeResult{OK: true, Millis: millis, PeerAddr: conn.RemoteAddr().String()}
}

// probeTLSViaListener performs a CONNECT through our own proxy listener, then a
// TLS handshake with the requested SNI — exactly what the app does.
func probeTLSViaListener(ctx context.Context, listenerAddr, auth string, dialAddr, sni string) sniProbeResult {
	if listenerAddr == "" {
		return sniProbeResult{Unavailable: true, Error: "未找到代理监听地址（pool/hybrid 模式才有共享入口）"}
	}
	start := time.Now()
	dialer := &net.Dialer{Timeout: 8 * time.Second}
	conn, err := dialer.DialContext(ctx, "tcp", listenerAddr)
	if err != nil {
		return sniProbeResult{Unavailable: true, Millis: time.Since(start).Milliseconds(), Error: "连接代理入口失败: " + classifySNIError(err)}
	}
	defer conn.Close()
	if deadline, ok := ctx.Deadline(); ok {
		_ = conn.SetDeadline(deadline)
	}
	request := fmt.Sprintf("CONNECT %s HTTP/1.1\r\nHost: %s\r\n", dialAddr, dialAddr)
	if auth != "" {
		request += "Proxy-Authorization: Basic " + base64.StdEncoding.EncodeToString([]byte(auth)) + "\r\n"
	}
	request += "\r\n"
	if _, err := conn.Write([]byte(request)); err != nil {
		return sniProbeResult{Millis: time.Since(start).Milliseconds(), Error: "写 CONNECT 失败: " + classifySNIError(err)}
	}
	reader := bufio.NewReader(conn)
	status, err := reader.ReadString('\n')
	if err != nil {
		return sniProbeResult{Millis: time.Since(start).Milliseconds(), Error: "读 CONNECT 响应失败: " + classifySNIError(err)}
	}
	if !strings.Contains(status, " 200") {
		return sniProbeResult{Unavailable: true, Millis: time.Since(start).Milliseconds(), Error: "代理拒绝 CONNECT: " + strings.TrimSpace(status)}
	}
	// Drain the blank line ending the CONNECT response headers.
	for {
		line, readErr := reader.ReadString('\n')
		if readErr != nil {
			return sniProbeResult{Millis: time.Since(start).Milliseconds(), Error: "CONNECT 响应头不完整"}
		}
		if strings.TrimSpace(line) == "" {
			break
		}
	}
	return tlsHandshake(ctx, conn, sni, start)
}

func classifySNIError(err error) string {
	if err == nil {
		return ""
	}
	if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
		return "超时"
	}
	message := strings.ToLower(err.Error())
	switch {
	case strings.Contains(message, "reset by peer"):
		return "连接被重置（RST）"
	case strings.Contains(message, "connection refused"):
		return "端口未开放（连接被拒绝）"
	case strings.Contains(message, "no such host"):
		return "域名解析失败"
	case strings.Contains(message, "certificate"), strings.Contains(message, "x509"):
		return "证书错误（探测已忽略校验，说明是握手阶段其他问题）"
	case strings.Contains(message, "timeout"):
		return "超时"
	}
	return err.Error()
}

// proxyListenerAddress is the local address of the shared proxy entry.
func (s *Server) proxyListenerAddress() string {
	s.cfgMu.RLock()
	defer s.cfgMu.RUnlock()
	if s.cfgSrc == nil {
		return ""
	}
	mode := s.cfgSrc.Mode
	if mode == "" {
		mode = "pool"
	}
	if mode != "pool" && mode != "hybrid" {
		return ""
	}
	port := s.cfgSrc.Listener.Port
	if port == 0 {
		return ""
	}
	address := strings.TrimSpace(s.cfgSrc.Listener.Address)
	if address == "" || address == "0.0.0.0" || address == "::" {
		address = "127.0.0.1"
	}
	return net.JoinHostPort(address, fmt.Sprintf("%d", port))
}

func (s *Server) proxyListenerAuth() string {
	s.cfgMu.RLock()
	defer s.cfgMu.RUnlock()
	if s.cfgSrc == nil {
		return ""
	}
	username := s.cfgSrc.Listener.Username
	if username == "" {
		return ""
	}
	return username + ":" + s.cfgSrc.Listener.Password
}
