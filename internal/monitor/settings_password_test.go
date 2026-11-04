package monitor

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"easy_proxies/internal/config"
)

// The settings API must let the UI change the management password without ever
// shipping the current one to the browser, and omitting the field must keep it.
func TestManagementPasswordSemantics(t *testing.T) {
	cfg := proxySessionTestConfig()
	cfg.Management = config.ManagementConfig{Listen: "0.0.0.0:9091", Password: "old-secret"}
	cfg.MultiPort = config.MultiPortConfig{Address: "0.0.0.0", BasePort: 24000}
	// Settings are persisted, so the file must exist on disk.
	configPath := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(configPath, []byte("mode: pool\nnodes: []\n"), 0o644); err != nil {
		t.Fatalf("seed config: %v", err)
	}
	cfg.SetFilePath(configPath)
	server, _ := newProxySessionTestServer(t, cfg)

	// A password is set, so every management call needs a session token.
	authed := func(method, path, body string) *httptest.ResponseRecorder {
		t.Helper()
		session, err := server.createSession()
		if err != nil {
			t.Fatalf("create session: %v", err)
		}
		request := httptest.NewRequest(method, "http://dashboard.example:9091"+path, strings.NewReader(body))
		request.Header.Set("Authorization", "Bearer "+session.Token)
		recorder := httptest.NewRecorder()
		server.srv.Handler.ServeHTTP(recorder, request)
		return recorder
	}

	// GET never returns the plaintext, only whether one is set.
	w := authed(http.MethodGet, "/api/settings", "")
	if w.Code != http.StatusOK {
		t.Fatalf("GET settings = %d", w.Code)
	}
	got := decodeProxyResponse(t, w)
	management, _ := got["management"].(map[string]any)
	if management == nil {
		t.Fatalf("missing management section: %v", got)
	}
	if _, leaked := management["password"]; leaked {
		t.Fatalf("GET /api/settings leaked the management password: %v", management)
	}
	if management["has_password"] != true {
		t.Fatalf("has_password = %v, want true", management["has_password"])
	}

	// Omitting the password keeps the current one.
	body := fmt.Sprintf(`{"management":{"listen":%q,"probe_concurrency":32}}`, cfg.Management.Listen)
	w = authed(http.MethodPut, "/api/settings", body)
	if w.Code != http.StatusOK {
		t.Fatalf("PUT without password = %d %s", w.Code, w.Body.String())
	}
	if server.cfgSrc.Management.Password != "old-secret" {
		t.Fatalf("omitting the field changed the password to %q", server.cfgSrc.Management.Password)
	}

	// An explicit empty string clears it.
	w = authed(http.MethodPut, "/api/settings",
		fmt.Sprintf(`{"management":{"listen":%q,"probe_concurrency":32,"password":""}}`, cfg.Management.Listen))
	if w.Code != http.StatusOK {
		t.Fatalf("PUT clearing password = %d %s", w.Code, w.Body.String())
	}
	if server.cfgSrc.Management.Password != "" {
		t.Fatalf("password not cleared: %q", server.cfgSrc.Management.Password)
	}

	// A value sets it.
	w = authed(http.MethodPut, "/api/settings",
		fmt.Sprintf(`{"management":{"listen":%q,"probe_concurrency":32,"password":"new-secret"}}`, cfg.Management.Listen))
	if w.Code != http.StatusOK {
		t.Fatalf("PUT setting password = %d %s", w.Code, w.Body.String())
	}
	if server.cfgSrc.Management.Password != "new-secret" {
		t.Fatalf("password not updated: %q", server.cfgSrc.Management.Password)
	}
	if server.validateSession("") {
		t.Fatal("empty session token authenticated while a password is set")
	}
}
