package main

import (
	"context"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"
)

func TestLifecycleGCFailureThenSupersession(t *testing.T) {
	clock := newCleanupClock()
	hooks := defaultCleanupHooks()
	hooks.now = clock.now
	hooks.newTimer = clock.timer
	var fail atomic.Bool
	hooks.remove = func(path string) error {
		if fail.Load() {
			return os.ErrPermission
		}
		return os.Remove(path)
	}
	m := cleanupManager(t, cleanupConfig(t, &hooks), true)
	a, err := m.Acquire(context.Background(), request("gc-retry"))
	if err != nil {
		t.Fatal(err)
	}
	fail.Store(true)
	if _, err = m.Fail(failRequest{a.Descriptor.ResourceID, a.Descriptor.Generation, "test"}); err != nil {
		t.Fatal(err)
	}
	m.cleanupBatch()
	assertCleanupCount(t, m, 1, 0)
	old := m.garbage[a.Descriptor.ResourceID+"-"+a.Descriptor.Generation]
	if old.next != clock.now().Add(time.Second) {
		t.Fatal("GC retry has no backoff")
	}
	b, err := m.Acquire(context.Background(), request("gc-retry"))
	if err != nil {
		t.Fatal(err)
	}
	fail.Store(false)
	clock.advance(time.Second)
	m.cleanupBatch()
	got := readCleanupRecord(t, m, b.Descriptor.ResourceID)
	if got.Generation != b.Descriptor.Generation || got.State != statePending {
		t.Fatal("old GC removed new catalog")
	}
	if _, err = m.Fail(failRequest{a.Descriptor.ResourceID, a.Descriptor.Generation, "test"}); err == nil {
		t.Fatal("old token accepted")
	}
}

func TestLifecycleTerminalCollected(t *testing.T) {
	m := testManager(t, 4096, 2048)
	a, err := m.Acquire(context.Background(), request("gc"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = m.Fail(failRequest{ResourceID: a.Descriptor.ResourceID, Generation: a.Descriptor.Generation, Kind: "test"}); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(4 * time.Second)
	for time.Now().Before(deadline) {
		d, err := m.Resolve(a.Descriptor.ResourceID, "")
		if err != nil {
			t.Fatal(err)
		}
		if d.State == stateNotFound {
			if _, err := os.Stat(filepath.Join(m.catalogDir, d.ResourceID+".json")); !os.IsNotExist(err) {
				t.Fatalf("catalog retained: %v", err)
			}
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("terminal record retained after cleanup")
}

func TestLifecycleIdleResidueCollected(t *testing.T) {
	m := testManager(t, 4096, 2048)
	paths := []string{filepath.Join(m.catalogDir, ".record-orphan"), filepath.Join(m.readyDir, "unknown"), filepath.Join(m.stagingDir, "unknown"), filepath.Join(m.trashDir, "unknown")}
	for _, p := range paths {
		if err := os.WriteFile(p, []byte("residue"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	deadline := time.Now().Add(6 * time.Second)
	for time.Now().Before(deadline) {
		remaining := false
		for _, p := range paths {
			if _, err := os.Lstat(p); !os.IsNotExist(err) {
				remaining = true
			}
		}
		if !remaining {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("idle managed residue retained")
}
