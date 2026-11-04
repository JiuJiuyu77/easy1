package monitor

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"
)

// handleSubscriptionDelete drops one subscription URL from the configuration,
// purges the managed nodes it produced and lets the subscription manager
// re-sync. It is the delete counterpart of handleSubscriptionConfig.
func (s *Server) handleSubscriptionDelete(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", http.MethodPost)
		w.WriteHeader(http.StatusMethodNotAllowed)
		writeJSON(w, map[string]string{"error": "仅支持 POST 请求"})
		return
	}
	var req struct {
		URL string `json:"url"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		w.WriteHeader(http.StatusBadRequest)
		writeJSON(w, map[string]string{"error": "请求格式错误"})
		return
	}
	target := strings.TrimSpace(req.URL)
	if target == "" {
		w.WriteHeader(http.StatusBadRequest)
		writeJSON(w, map[string]string{"error": "url 不能为空"})
		return
	}

	// 1. Drop the URL from cfg.Subscriptions and persist only subscription-owned
	//    fields, leaving node/settings ownership to their own managers.
	s.cfgMu.Lock()
	found := false
	remaining := make([]string, 0)
	enabled := true
	interval := 24 * time.Hour
	if s.cfgSrc != nil {
		for _, u := range s.cfgSrc.Subscriptions {
			if u == target {
				found = true
				continue
			}
			remaining = append(remaining, u)
		}
		enabled = s.cfgSrc.SubscriptionRefresh.Enabled
		if s.cfgSrc.SubscriptionRefresh.Interval > 0 {
			interval = s.cfgSrc.SubscriptionRefresh.Interval
		}
		candidate := s.cfgSrc.SettingsSnapshot()
		candidate.Subscriptions = append([]string(nil), remaining...)
		candidate.SubscriptionRefresh = s.cfgSrc.SubscriptionRefresh
		if err := candidate.SaveSubscriptionSettings(); err != nil {
			s.cfgMu.Unlock()
			writeStatusJSON(w, http.StatusInternalServerError, map[string]any{"error": fmt.Sprintf("保存配置失败: %v", err)})
			return
		}
		s.cfgSrc.Subscriptions = append([]string(nil), remaining...)
	}
	s.cfgMu.Unlock()

	// 2. Purge managed nodes whose import source matches the URL.
	deleted := 0
	if svc := s.importService(); svc != nil {
		n, err := svc.DeleteBySubscription(target)
		if err != nil {
			writeStatusJSON(w, http.StatusInternalServerError, map[string]any{"error": fmt.Sprintf("删除节点失败: %v", err)})
			return
		}
		deleted = n
	}

	// 3. Re-sync the subscription manager so the removed URL's nodes stop being
	//    refreshed into the pool.
	if refresher := s.subscriptionRefresher(); refresher != nil {
		_ = refresher.UpdateConfigAndRefresh(remaining, enabled, interval)
	}

	writeJSON(w, map[string]any{
		"message":         "订阅已删除",
		"url":             target,
		"removed":         found,
		"deleted_nodes":   deleted,
		"remaining_count": len(remaining),
	})
}

// handleLogsClear empties the in-memory console and truncates the log file.
func (s *Server) handleLogsClear(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", http.MethodPost)
		w.WriteHeader(http.StatusMethodNotAllowed)
		writeJSON(w, map[string]string{"error": "仅支持 POST 请求"})
		return
	}
	SharedLogBuffer.Clear()
	s.logger.Print("logs cleared from WebUI")
	s.cfgMu.RLock()
	logFile := ""
	if s.cfgSrc != nil {
		logFile = s.cfgSrc.Log.File
	}
	s.cfgMu.RUnlock()
	if logFile != "" {
		_ = os.WriteFile(logFile, nil, 0o644)
	}
	writeJSON(w, map[string]string{"status": "ok"})
}
