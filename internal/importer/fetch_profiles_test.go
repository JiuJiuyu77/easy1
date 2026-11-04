package importer

import (
	"encoding/base64"
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"

	"easy_proxies/internal/subfetch"
)

func TestLooksLikeSubscription(t *testing.T) {
	cases := map[string]bool{
		"vless://uuid@example.com:443#a":                                            true,
		base64.StdEncoding.EncodeToString([]byte("vless://uuid@example.com:443#a")): true,
		"proxies:\n  - name: a\n    type: ss":                                       true,
		"<!DOCTYPE html><html>blocked</html>":                                       false,
		"<html><body>Forbidden</body></html>":                                       false,
		"":                                                                          false,
		"   ":                                                                       false,
	}
	for body, want := range cases {
		if got := looksLikeSubscription([]byte(body)); got != want {
			t.Fatalf("looksLikeSubscription(%q) = %v, want %v", body, got, want)
		}
	}
}

func TestParseSubscriptionUserinfo(t *testing.T) {
	header := http.Header{}
	header.Set("Subscription-Userinfo", "upload=1024; download=2048; total=1048576; expire=1893456000")
	info := parseSubscriptionUserinfo(header)
	if info == nil {
		t.Fatal("userinfo not parsed")
	}
	if info.Upload != 1024 || info.Download != 2048 || info.Total != 1048576 {
		t.Fatalf("unexpected traffic: %+v", info)
	}
	if info.Expire.IsZero() || !info.Expire.After(time.Now()) {
		t.Fatalf("unexpected expiry: %v", info.Expire)
	}
	if info.UsedPercent() <= 0 {
		t.Fatalf("used percent = %v", info.UsedPercent())
	}
	if parseSubscriptionUserinfo(http.Header{}) != nil {
		t.Fatal("missing header must not produce info")
	}
}

func TestFetchDiagnosis(t *testing.T) {
	cases := []struct {
		err  error
		want string
	}{
		{&subfetch.HTTPStatusError{Code: http.StatusForbidden}, "UA 限制"},
		{&subfetch.HTTPStatusError{Code: http.StatusTooManyRequests}, "限流"},
		{&subfetch.HTTPStatusError{Code: http.StatusNotFound}, "失效"},
		{errors.New("x509: certificate signed by unknown authority"), "证书"},
		{errors.New("context deadline exceeded"), "超时"},
	}
	for _, test := range cases {
		got := FetchDiagnosis(test.err)
		if !strings.Contains(got, test.want) {
			t.Fatalf("diagnosis(%v) = %q, missing %q", test.err, got, test.want)
		}
	}
}

func TestSubscriptionInfoExhausted(t *testing.T) {
	info := SubscriptionInfo{Upload: 100, Download: 100, Total: 200}
	if !info.Exhausted(time.Now()) {
		t.Fatal("full quota should count as exhausted")
	}
	expired := SubscriptionInfo{Total: 0, Expire: time.Now().Add(-time.Hour)}
	if !expired.Exhausted(time.Now()) {
		t.Fatal("expired subscription should count as exhausted")
	}
}
