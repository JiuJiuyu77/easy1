package subscription

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"easy_proxies/internal/config"
	"easy_proxies/internal/importer"
)

// SourceRefresher is the managed-source side of the import service. When the
// WebUI has import sources, the importer owns fetching, testing and pool
// promotion, so the subscription worker hands the periodic tick over to it and
// only mirrors the resulting status. Without sources the worker keeps the
// classic behaviour of loading every subscription node into the runtime.
type SourceRefresher interface {
	StartRefreshSourcesWithPolicy(key string, test204 *bool, siteTargets []string) (string, error)
	GetRefreshJob(jobID string) (importer.SourceRefreshJob, bool)
}

// SetSourceRefresher enables managed-source delegation for periodic refreshes.
func (m *Manager) SetSourceRefresher(refresher SourceRefresher) {
	m.mu.Lock()
	m.sourceRefresher = refresher
	m.mu.Unlock()
}

func (m *Manager) sourceRefresherSnapshot() SourceRefresher {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.sourceRefresher
}

// ApplyRestoredConfig adopts the subscription settings of a restored config so a
// WebDAV/local backup restore also re-arms the refresh worker.
func (m *Manager) ApplyRestoredConfig(restored *config.Config) {
	if restored == nil {
		return
	}
	m.mu.Lock()
	candidate := cloneConfig(m.baseCfg)
	candidate.Subscriptions = append([]string(nil), restored.Subscriptions...)
	candidate.SubscriptionRefresh = restored.SubscriptionRefresh
	m.baseCfg = candidate
	m.version++
	m.mu.Unlock()
	m.Start()
}

// refreshManagedSources runs one importer-managed refresh. handled reports
// whether the importer took ownership of this tick; err is the outcome of that
// refresh (a nil err with handled=false falls through to the classic path).
func (m *Manager) refreshManagedSources(ctx context.Context) (handled bool, err error) {
	refresher := m.sourceRefresherSnapshot()
	if refresher == nil {
		return false, nil
	}
	test204 := m.baseCfg.SubscriptionRefresh.Test204Enabled()
	siteTargets := append([]string(nil), m.baseCfg.SubscriptionRefresh.SiteTargets...)
	jobID, err := refresher.StartRefreshSourcesWithPolicy("", &test204, siteTargets)
	if errors.Is(err, importer.ErrNoRefreshSources) {
		return false, nil
	}
	if err != nil {
		return true, err
	}
	ticker := time.NewTicker(250 * time.Millisecond)
	defer ticker.Stop()
	for {
		job, ok := refresher.GetRefreshJob(jobID)
		if !ok {
			return true, errors.New("managed source refresh job disappeared")
		}
		if job.Status != importer.SourceRefreshJobRunning {
			m.mu.Lock()
			m.status.NodeCount = job.Passed
			m.mu.Unlock()
			switch job.Status {
			case importer.SourceRefreshJobFinished:
				if job.Protected {
					return true, fmt.Errorf("managed source refresh protected previous pool: %s", job.ProtectionReason)
				}
				return true, nil
			case importer.SourceRefreshJobCanceled:
				return true, errors.New("managed source refresh canceled")
			default:
				if strings.TrimSpace(job.Error) != "" {
					return true, fmt.Errorf("managed source refresh failed: %s", job.Error)
				}
				return true, errors.New("managed source refresh failed")
			}
		}
		select {
		case <-ctx.Done():
			return true, ctx.Err()
		case <-ticker.C:
		}
	}
}
