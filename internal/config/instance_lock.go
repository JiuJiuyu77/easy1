package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// ErrInstanceRunning means another process holds the instance lock for the config.
var ErrInstanceRunning = errors.New("another easy_proxies instance is using this config")

const instanceLockRetryInterval = 500 * time.Millisecond

// InstanceLockSuffix names the per-config instance lock. It must NOT reuse the
// persistence sidecar suffix (".lock"): the instance lock is held for the whole
// process lifetime, so sharing the name would make every config save wait on it.
const InstanceLockSuffix = ".instance.lock"

// AcquireInstanceLock takes an exclusive OS lock on "<config>.instance.lock",
// waiting up to wait for a previous instance to exit. The OS drops the lock if
// the process dies.
func AcquireInstanceLock(configPath string, wait time.Duration) (func(), error) {
	absPath, err := filepath.Abs(configPath)
	if err != nil {
		return nil, fmt.Errorf("resolve config path: %w", err)
	}
	f, err := os.OpenFile(absPath+InstanceLockSuffix, os.O_RDWR|os.O_CREATE, 0o644)
	if err != nil {
		return nil, fmt.Errorf("open instance lock: %w", err)
	}
	deadline := time.Now().Add(wait)
	for {
		locked, err := tryLockFile(f)
		if err != nil {
			_ = f.Close()
			return nil, fmt.Errorf("acquire instance lock: %w", err)
		}
		if locked {
			return func() {
				_ = unlockFile(f)
				_ = f.Close()
			}, nil
		}
		if !time.Now().Before(deadline) {
			_ = f.Close()
			return nil, fmt.Errorf("%w (lock: %s)", ErrInstanceRunning, f.Name())
		}
		time.Sleep(min(instanceLockRetryInterval, time.Until(deadline)))
	}
}
