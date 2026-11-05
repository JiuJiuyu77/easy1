package monitor

import (
	"strings"
	"testing"
)

// Every asset reference must carry the build id, and the shell must expose it,
// otherwise an upgrade can serve stale JS next to new HTML.
func TestInjectBuildStampsShell(t *testing.T) {
	page := []byte(`<html><head><title>x</title><link rel="stylesheet" href="/assets/css/base.css"><script src="/assets/js/core.js" defer></script></head></html>`)
	out := string(injectBuild(page, "abc1234"))

	if !strings.Contains(out, `window.__BUILD__="abc1234"`) {
		t.Fatalf("build not injected: %s", out)
	}
	if !strings.Contains(out, `/assets/css/base.css?v=abc1234`) || !strings.Contains(out, `/assets/js/core.js?v=abc1234`) {
		t.Fatalf("asset urls not versioned: %s", out)
	}
	if strings.Contains(out, ".css\"") || strings.Contains(out, ".js\"") {
		t.Fatalf("unversioned asset reference left: %s", out)
	}
}

func TestInjectBuildWithoutBuildIsNoop(t *testing.T) {
	page := []byte(`<html><head></head></html>`)
	if got := string(injectBuild(page, "")); got != string(page) {
		t.Fatalf("empty build must not rewrite the shell: %s", got)
	}
}
