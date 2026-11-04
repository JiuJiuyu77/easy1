package importer

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"easy_proxies/internal/subfetch"
)

// sourceUserAgents are the client strings tried, in order, when a source has no
// remembered user agent. Providers commonly serve a real subscription only to
// known clients and an error page to anything else.
var sourceUserAgents = []string{
	"clash-verge/v2.0.4",
	"clash.meta/v1.19.10",
	"v2rayNG/1.9.16",
	"Shadowrocket/2.2.30",
	subfetch.DefaultUserAgent,
}

// LooksLikeSubscription reports whether body plausibly contains nodes rather
// than an HTML error page or an empty response.
func looksLikeSubscription(body []byte) bool {
	text := strings.TrimSpace(string(body))
	if len(text) < 8 {
		return false
	}
	lower := strings.ToLower(text)
	if strings.HasPrefix(lower, "<!doctype") || strings.HasPrefix(lower, "<html") {
		return false
	}
	if strings.Contains(lower, "<html") && !strings.Contains(lower, "://") {
		return false
	}
	if strings.Contains(text, "://") || strings.Contains(lower, "proxies:") {
		return true
	}
	// Base64-wrapped payloads decode to a URI list.
	trimmed := strings.Join(strings.Fields(text), "")
	for _, encoding := range []*base64.Encoding{base64.StdEncoding, base64.RawStdEncoding, base64.URLEncoding, base64.RawURLEncoding} {
		if decoded, err := encoding.DecodeString(trimmed); err == nil && strings.Contains(string(decoded), "://") {
			return true
		}
	}
	return false
}

// parseSubscriptionUserinfo reads the quota headers providers attach.
func parseSubscriptionUserinfo(header http.Header) *SubscriptionInfo {
	raw := strings.TrimSpace(header.Get("Subscription-Userinfo"))
	if raw == "" {
		raw = strings.TrimSpace(header.Get("Subscription-UserInfo"))
	}
	if raw == "" {
		return nil
	}
	info := &SubscriptionInfo{Updated: time.Now()}
	seen := false
	for _, part := range strings.Split(raw, ";") {
		key, value, ok := strings.Cut(strings.TrimSpace(part), "=")
		if !ok {
			continue
		}
		number, err := strconv.ParseInt(strings.TrimSpace(value), 10, 64)
		if err != nil {
			continue
		}
		switch strings.ToLower(strings.TrimSpace(key)) {
		case "upload":
			info.Upload = number
			seen = true
		case "download":
			info.Download = number
			seen = true
		case "total":
			info.Total = number
			seen = true
		case "expire":
			if number > 0 {
				info.Expire = time.Unix(number, 0)
				seen = true
			}
		}
	}
	if !seen {
		return nil
	}
	return info
}

// FetchDiagnosis turns a fetch failure into something the operator can act on.
func FetchDiagnosis(err error) string {
	if err == nil {
		return ""
	}
	var statusErr *subfetch.HTTPStatusError
	if errors.As(err, &statusErr) {
		switch statusErr.Code {
		case http.StatusUnauthorized, http.StatusForbidden:
			return fmt.Sprintf("HTTP %d：机场拒绝了请求，可能是客户端 UA 限制、需要登录或订阅已失效（已自动尝试常见客户端 UA）", statusErr.Code)
		case http.StatusNotFound, http.StatusGone:
			return fmt.Sprintf("HTTP %d：订阅地址不存在或已失效", statusErr.Code)
		case http.StatusTooManyRequests:
			return "HTTP 429：请求过于频繁，被机场限流，稍后重试或降低刷新频率"
		}
		if statusErr.Code >= 500 {
			return fmt.Sprintf("HTTP %d：机场服务端异常", statusErr.Code)
		}
		return statusErr.Error()
	}
	message := strings.ToLower(err.Error())
	switch {
	case strings.Contains(message, "certificate"), strings.Contains(message, "x509"):
		return "TLS 证书错误：订阅站点证书不受信任（可开启 skip_cert_verify 或换可用出口拉取）"
	case strings.Contains(message, "timeout"), strings.Contains(message, "deadline exceeded"):
		return "拉取超时：订阅域名可能被限制，建议用前置代理/池节点拉取"
	case errors.Is(err, subfetch.ErrBodyTooLarge):
		return "订阅内容超过 10MB，已拒绝"
	}
	return err.Error()
}

// fetchSubscriptionWithProfiles fetches a subscription, trying the remembered
// user agent first and then the known clients, and records what worked.
func (s *Service) fetchSubscriptionWithProfiles(ctx context.Context, rawURL string, ref NodeSourceRef, opts subfetch.Options) (subfetch.Result, NodeSourceRef, error) {
	candidates := make([]string, 0, len(sourceUserAgents)+1)
	if agent := strings.TrimSpace(ref.UserAgent); agent != "" {
		candidates = append(candidates, agent)
	}
	for _, agent := range sourceUserAgents {
		if agent == ref.UserAgent {
			continue
		}
		candidates = append(candidates, agent)
	}
	headers := map[string]string{}
	for key, value := range ref.Headers {
		headers[key] = value
	}

	var lastErr error
	for _, agent := range candidates {
		attempt := opts
		attempt.UserAgent = agent
		attempt.Headers = headers
		result, err := subfetch.FetchWithMeta(ctx, rawURL, attempt)
		if err != nil {
			lastErr = err
			if ctx.Err() != nil {
				return subfetch.Result{}, ref, err
			}
			continue
		}
		if !looksLikeSubscription(result.Body) {
			lastErr = errors.New("返回内容不是订阅（可能是 HTML 错误页，试试换 UA 或加 flag=clash）")
			// Content-based failures are worth another UA; errors are too.
			continue
		}
		ref.UserAgent = agent
		return result, ref, nil
	}
	if lastErr == nil {
		lastErr = errors.New("没有可用的拉取客户端")
	}
	return subfetch.Result{}, ref, fmt.Errorf("%s", FetchDiagnosis(lastErr))
}
