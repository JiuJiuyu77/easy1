package importer

import (
	"testing"

	"easy_proxies/internal/config"
)

// Operator-defined sites must join the built-in ones for verification and the
// connectivity page, and be normalised (dedup, host/port derivation).
func TestSetCustomSitesExtendsTargets(t *testing.T) {
	defer SetCustomSites(nil)

	SetCustomSites([]config.CustomSite{
		{ID: "mine", Name: "我的站点", URL: "https://example.com/health"},
		{ID: "mine", Name: "dup", URL: "https://dup.example.com/"},
		{ID: "", URL: "https://noname.example.com/"},
	})

	targets := ConnectivityTargets()
	var custom []ConnectivityTarget
	for _, target := range targets {
		if target.Custom {
			custom = append(custom, target)
		}
	}
	if len(custom) != 1 {
		t.Fatalf("expected exactly one deduplicated custom site, got %d: %+v", len(custom), custom)
	}
	site := custom[0]
	if site.ID != "mine" || site.Name != "我的站点" || site.Host != "example.com" || site.Port != 443 {
		t.Fatalf("custom site normalised wrong: %+v", site)
	}
	if _, ok := connectivityTargetByID("mine"); !ok {
		t.Fatal("custom site not resolvable by id")
	}
	if _, err := NormalizeVerificationPolicy(nil, []string{"mine"}); err != nil {
		t.Fatalf("custom site rejected by the verification policy: %v", err)
	}

	SetCustomSites(nil)
	for _, target := range ConnectivityTargets() {
		if target.Custom {
			t.Fatal("custom sites not cleared")
		}
	}
}
