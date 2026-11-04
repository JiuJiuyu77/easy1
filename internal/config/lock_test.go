package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

const testNodeURI = "vless://11111111-2222-3333-4444-555555555555@example.com:443"

func newLockTestConfig(t *testing.T, dir string) *Config {
	t.Helper()
	path := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(path, []byte("mode: pool\nnodes: []\n"), 0o644); err != nil {
		t.Fatalf("seed config: %v", err)
	}
	cfg := &Config{Mode: "pool", filePath: path}
	cfg.Nodes = []NodeConfig{{Name: "n1", URI: testNodeURI, Source: NodeSourceInline}}
	return cfg
}

// A read-only sidecar lock file must not turn every save into an error. This is
// exactly what happens when someone "prevents locking" with chmod 444 or a
// read-only mount; writes fall back to the destination inode lock instead.
func TestSaveSucceedsWhenLockSidecarIsReadOnly(t *testing.T) {
	dir := t.TempDir()
	cfg := newLockTestConfig(t, dir)

	sidecar := cfg.filePath + ".lock"
	if err := os.WriteFile(sidecar, nil, 0o444); err != nil {
		t.Fatalf("create read-only sidecar: %v", err)
	}

	if err := cfg.Save(); err != nil {
		t.Fatalf("save with read-only lock sidecar: %v", err)
	}
	data, err := os.ReadFile(cfg.filePath)
	if err != nil {
		t.Fatalf("read config: %v", err)
	}
	if !strings.Contains(string(data), "n1") {
		t.Fatalf("node was not persisted: %s", data)
	}
}

// Saves must finish instead of waiting forever on a lock somebody else holds.
func TestConcurrentSavesFinishPromptly(t *testing.T) {
	dir := t.TempDir()
	newLockTestConfig(t, dir)

	var wait sync.WaitGroup
	errs := make(chan error, 16)
	for i := 0; i < 16; i++ {
		wait.Add(1)
		go func(index int) {
			defer wait.Done()
			cfg := &Config{Mode: "pool", filePath: filepath.Join(dir, "config.yaml")}
			cfg.Nodes = []NodeConfig{{
				Name:   fmt.Sprintf("n%d", index),
				URI:    testNodeURI,
				Source: NodeSourceInline,
			}}
			if err := cfg.Save(); err != nil {
				errs <- err
			}
		}(i)
	}

	done := make(chan struct{})
	go func() { wait.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(20 * time.Second):
		t.Fatal("concurrent saves blocked: a writer is waiting on the config lock forever")
	}
	close(errs)
	for err := range errs {
		t.Fatalf("concurrent save failed: %v", err)
	}
}

// A held sidecar lock must surface as an error within the wait budget rather
// than an HTTP request that never returns.
func TestHeldLockReturnsErrorInsteadOfBlockingForever(t *testing.T) {
	if testing.Short() {
		t.Skip("waits for the lock budget")
	}
	dir := t.TempDir()
	cfg := newLockTestConfig(t, dir)

	file, err := os.OpenFile(cfg.filePath+".lock", os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		t.Fatalf("open sidecar: %v", err)
	}
	defer file.Close()
	if err := lockFile(file); err != nil {
		t.Fatalf("take sidecar lock: %v", err)
	}

	started := time.Now()
	saveErr := cfg.Save()
	if saveErr == nil {
		t.Fatal("save with a held lock should report a timeout")
	}
	if elapsed := time.Since(started); elapsed > persistenceLockWait+5*time.Second {
		t.Fatalf("save waited %s, expected an error after ~%s", elapsed, persistenceLockWait)
	}
	if !strings.Contains(saveErr.Error(), "占用") && !strings.Contains(saveErr.Error(), "超时") {
		t.Fatalf("unexpected error: %v", saveErr)
	}
}

// The instance lock is held for the whole process lifetime. It must not share
// the persistence sidecar name, or every save waits on it until it times out.
func TestInstanceLockDoesNotBlockConfigSaves(t *testing.T) {
	dir := t.TempDir()
	cfg := newLockTestConfig(t, dir)

	release, err := AcquireInstanceLock(cfg.filePath, 0)
	if err != nil {
		t.Fatalf("acquire instance lock: %v", err)
	}
	defer release()

	started := time.Now()
	if err := cfg.Save(); err != nil {
		t.Fatalf("save while the instance lock is held: %v", err)
	}
	if elapsed := time.Since(started); elapsed > 2*time.Second {
		t.Fatalf("save waited %s: the instance lock is probably sharing the sidecar name", elapsed)
	}

	// A second instance must still be rejected while the first holds the lock.
	if _, err := AcquireInstanceLock(cfg.filePath, 200*time.Millisecond); err == nil {
		t.Fatal("second instance lock acquisition should fail")
	}
}
