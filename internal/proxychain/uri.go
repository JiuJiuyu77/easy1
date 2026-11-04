package proxychain

import (
	"net/url"
	"strings"
)

// NormalizeNodeURI repairs node URIs that were pasted from a chat client or a
// browser address bar:
//   - stray spaces/newlines/zero-width characters inside the URI
//   - percent-encoded base64 payloads (vmess://eyJ2...%2B..., ss://YWVz...%3D)
//   - client-specific "?remarks=" style suffixes glued onto a vmess base64 body
//
// Only the payload between "scheme://" and the first "?"/"#" is unescaped, so
// fragment names and query values keep their original encoding.
func NormalizeNodeURI(raw string) string {
	uri := strings.Map(func(r rune) rune {
		switch r {
		case ' ', '\t', '\n', '\r', '\u00a0', '\u200b', '\u200e', '\u200f', '\ufeff':
			return -1
		}
		return r
	}, strings.TrimSpace(raw))
	schemeEnd := strings.Index(uri, "://")
	if schemeEnd <= 0 {
		return uri
	}
	scheme := strings.ToLower(uri[:schemeEnd])
	prefix := uri[:schemeEnd+3]
	rest := uri[schemeEnd+3:]
	switch scheme {
	case "vmess":
		end := len(rest)
		if i := strings.IndexAny(rest, "?#"); i >= 0 {
			end = i
		}
		payload, suffix := rest[:end], rest[end:]
		if strings.Contains(payload, "%") {
			if decoded, err := url.QueryUnescape(payload); err == nil {
				payload = decoded
			}
		}
		return prefix + payload + suffix
	case "ss", "shadowsocks":
		end := len(rest)
		if i := strings.IndexAny(rest, "?#"); i >= 0 {
			end = i
		}
		payload, suffix := rest[:end], rest[end:]
		if at := strings.LastIndex(payload, "@"); at >= 0 {
			userinfo, hostport := payload[:at], payload[at:]
			if strings.Contains(userinfo, "%") {
				if decoded, err := url.QueryUnescape(userinfo); err == nil {
					userinfo = decoded
				}
			}
			payload = userinfo + hostport
		} else if strings.Contains(payload, "%") {
			if decoded, err := url.QueryUnescape(payload); err == nil {
				payload = decoded
			}
		}
		return prefix + payload + suffix
	}
	return uri
}
