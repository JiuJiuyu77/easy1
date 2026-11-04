package monitor

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// The management UI is embedded with go:embed; these regressions keep the
// bundle servable: the page, its css/js, the icons, and the guards that stop the
// asset route from becoming a file reader.
func TestManagementUIAssetsAreServed(t *testing.T) {
	s, _ := newProxySessionTestServer(t, proxySessionTestConfig())

	recorder := httptest.NewRecorder()
	s.handleIndex(recorder, httptest.NewRequest(http.MethodGet, "/", nil))
	if recorder.Code != http.StatusOK {
		t.Fatalf("index status = %d", recorder.Code)
	}
	body := recorder.Body.String()
	if !strings.Contains(body, `id="view"`) || !strings.Contains(body, "/assets/js/core.js") {
		t.Fatalf("index is not the multi-page bundle: %.120q", body)
	}

	for path, wantType := range map[string]string{
		"/assets/js/core.js":           "application/javascript",
		"/assets/css/base.css":         "text/css",
		"/assets/js/pages/sessions.js": "application/javascript",
	} {
		recorder = httptest.NewRecorder()
		s.handleAsset(recorder, httptest.NewRequest(http.MethodGet, path, nil))
		if recorder.Code != http.StatusOK {
			t.Fatalf("%s status = %d", path, recorder.Code)
		}
		if got := recorder.Header().Get("Content-Type"); !strings.Contains(got, wantType) {
			t.Fatalf("%s content type = %q", path, got)
		}
		if recorder.Body.Len() == 0 {
			t.Fatalf("%s served an empty body", path)
		}
	}

	for _, path := range []string{"/assets/../config.yaml", "/assets/js/missing.js", "/assets/logo.exe"} {
		recorder = httptest.NewRecorder()
		s.handleAsset(recorder, httptest.NewRequest(http.MethodGet, path, nil))
		if recorder.Code != http.StatusNotFound {
			t.Fatalf("%s status = %d, want 404", path, recorder.Code)
		}
	}

	recorder = httptest.NewRecorder()
	s.handleFavicon(recorder, httptest.NewRequest(http.MethodGet, "/favicon.svg", nil))
	if recorder.Code != http.StatusOK || !strings.Contains(recorder.Header().Get("Content-Type"), "svg") {
		t.Fatalf("favicon not served: %d %q", recorder.Code, recorder.Header().Get("Content-Type"))
	}
	recorder = httptest.NewRecorder()
	s.handleLogo(recorder, httptest.NewRequest(http.MethodGet, "/logo.png", nil))
	if recorder.Code != http.StatusOK || recorder.Body.Len() == 0 {
		t.Fatalf("logo not served: %d", recorder.Code)
	}
}

func TestPprofOnlyOnLoopbackListeners(t *testing.T) {
	for listen, want := range map[string]bool{
		"127.0.0.1:9091": true,
		"localhost:9091": true,
		"[::1]:9091":     true,
		"0.0.0.0:9091":   false,
		":9091":          false,
	} {
		if got := isLoopbackListen(listen); got != want {
			t.Fatalf("isLoopbackListen(%q) = %v, want %v", listen, got, want)
		}
	}
}
