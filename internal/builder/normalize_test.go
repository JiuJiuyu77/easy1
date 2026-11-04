package builder

import (
	"encoding/base64"
	"strings"
	"testing"

	"easy_proxies/internal/proxychain"
)

func vmessB64(t *testing.T, extra string) string {
	t.Helper()
	payload := `{"v":"2","ps":"x","add":"example.com","port":"443","id":"11111111-2222-3333-4444-555555555555","aid":"0","net":"tcp","type":"none"` + extra + `}`
	encoded := base64.StdEncoding.EncodeToString([]byte(payload))
	if !strings.ContainsAny(encoded, "+/") {
		// Pad until the alphabet hits a "+"/"/" so the escaping path is real.
		for i := 0; i < 64; i++ {
			candidate := base64.StdEncoding.EncodeToString([]byte(payload + strings.Repeat("x", i)))
			if strings.ContainsAny(candidate, "+/") {
				return candidate
			}
		}
	}
	return encoded
}

func TestNormalizeProxyURILeavesCleanURIs(t *testing.T) {
	for _, uri := range []string{
		"socks5://user:pass@1.2.3.4:1080",
		"vless://11111111-2222-3333-4444-555555555555@example.com:443?security=tls&sni=example.com#name",
		"trojan://pass@example.com:443?sni=example.com",
	} {
		if got := proxychain.NormalizeNodeURI(uri); got != uri {
			t.Fatalf("clean URI changed:\n got %q\nwant %q", got, uri)
		}
	}
}

func TestNormalizeProxyURIStripsWhitespace(t *testing.T) {
	got := proxychain.NormalizeNodeURI("  vmess://abc\tdef\nghi  ")
	if got != "vmess://abcdefghi" {
		t.Fatalf("whitespace not stripped: %q", got)
	}
}

// A base64 body copied out of a browser arrives percent-encoded.
func TestNormalizeProxyURIUnescapesVmessPayload(t *testing.T) {
	body := vmessB64(t, "")
	escaped := strings.NewReplacer("+", "%2B", "/", "%2F", "=", "%3D").Replace(body)
	got := proxychain.NormalizeNodeURI("vmess://" + escaped)
	if got != "vmess://"+body {
		t.Fatalf("percent-encoded payload not restored:\n got %q\nwant %q", got, "vmess://"+body)
	}
	if _, err := buildNodeOutbound("t", got, false); err != nil {
		t.Fatalf("normalized vmess URI must build: %v", err)
	}
}

// Some clients append their own query parameters to the base64 body.
func TestNormalizeProxyURIKeepsVmessSuffixButDoesNotEncodeIt(t *testing.T) {
	body := vmessB64(t, "")
	got := proxychain.NormalizeNodeURI("vmess://" + body + "?remarks=hello")
	if !strings.HasPrefix(got, "vmess://"+body) || !strings.HasSuffix(got, "?remarks=hello") {
		t.Fatalf("unexpected normalization: %q", got)
	}
	if _, err := buildNodeOutbound("t", got, false); err != nil {
		t.Fatalf("vmess URI with client suffix must build: %v", err)
	}
}

func TestNormalizeProxyURIUnescapesShadowsocksUserinfo(t *testing.T) {
	userinfo := base64.StdEncoding.EncodeToString([]byte("aes-256-gcm:p@ss"))
	escaped := strings.NewReplacer("+", "%2B", "/", "%2F", "=", "%3D").Replace(userinfo)
	got := proxychain.NormalizeNodeURI("ss://" + escaped + "@example.com:8388#n")
	want := "ss://" + userinfo + "@example.com:8388#n"
	if got != want {
		t.Fatalf("ss userinfo not restored:\n got %q\nwant %q", got, want)
	}
	if _, err := buildNodeOutbound("t", got, false); err != nil {
		t.Fatalf("normalized ss URI must build: %v", err)
	}
}

func TestNormalizeProxyURIUnescapesLegacyShadowsocksBody(t *testing.T) {
	body := base64.StdEncoding.EncodeToString([]byte("aes-256-gcm:p@ss@example.com:8388"))
	escaped := strings.NewReplacer("+", "%2B", "/", "%2F", "=", "%3D").Replace(body)
	got := proxychain.NormalizeNodeURI("ss://" + escaped)
	if got != "ss://"+body {
		t.Fatalf("legacy ss payload not restored:\n got %q\nwant %q", got, "ss://"+body)
	}
	if _, err := buildNodeOutbound("t", got, false); err != nil {
		t.Fatalf("legacy ss URI must build: %v", err)
	}
}
