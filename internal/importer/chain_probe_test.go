package importer

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// The chain probe must accept a real page (GitHub answers 200) instead of the
// "must be 204" rule used for plain node probes.
func probeAgainst(t *testing.T, handler http.HandlerFunc, target string) error {
	t.Helper()
	server := httptest.NewServer(handler)
	defer server.Close()
	url := target
	if url == "" {
		url = server.URL
	}
	return probeChainPage(context.Background(), server.Client(), url)
}

func TestProbeChainPageAccepts200WithMarker(t *testing.T) {
	err := probeAgainst(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("<html><title>GitHub</title></html>"))
	}, "")
	if err != nil {
		t.Fatalf("200 page with github marker rejected: %v", err)
	}
}

func TestProbeChainPageRejectsNon2xx(t *testing.T) {
	for _, code := range []int{http.StatusForbidden, http.StatusBadGateway, http.StatusMovedPermanently} {
		got := probeAgainst(t, func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(code)
			_, _ = w.Write([]byte("github"))
		}, "")
		if got == nil {
			t.Fatalf("HTTP %d should be rejected", code)
		}
		if !strings.Contains(got.Error(), "HTTP") {
			t.Fatalf("HTTP %d error should mention the status: %v", code, got)
		}
	}
}

// 204 is technically 2xx but carries no page, so a chain that only reaches a
// generate_204 endpoint is rejected as well.
func TestProbeChainPageRejectsEmptySuccessBody(t *testing.T) {
	got := probeAgainst(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}, "")
	if got == nil {
		t.Fatal("204 without a page should be rejected")
	}
}

func TestProbeChainPageRejectsHijackedPage(t *testing.T) {
	got := probeAgainst(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("<html>blocked by the network</html>"))
	}, "")
	if got == nil || !strings.Contains(got.Error(), "github") {
		t.Fatalf("page without the marker should be rejected, got %v", got)
	}
}

func TestProbeChainPageDefaultsToGitHub(t *testing.T) {
	if !strings.Contains(chainProbeURL, "github.com") {
		t.Fatalf("chain probe target = %q, want github.com", chainProbeURL)
	}
}
