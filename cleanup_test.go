package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type cleanupTestTimer struct {
	deadline time.Time
	ch       chan time.Time
}

type cleanupClock struct {
	mu     sync.Mutex
	time   time.Time
	timers map[*cleanupTestTimer]bool
}

func newCleanupClock() *cleanupClock {
	return &cleanupClock{time: time.Date(2030, 1, 1, 0, 0, 0, 0, time.UTC), timers: make(map[*cleanupTestTimer]bool)}
}

func (c *cleanupClock) now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.time
}

func (c *cleanupClock) timer(delay time.Duration) (<-chan time.Time, func()) {
	c.mu.Lock()
	timer := &cleanupTestTimer{deadline: c.time.Add(delay), ch: make(chan time.Time, 1)}
	if delay <= 0 {
		timer.ch <- c.time
	} else {
		c.timers[timer] = true
	}
	c.mu.Unlock()
	return timer.ch, func() {
		c.mu.Lock()
		delete(c.timers, timer)
		c.mu.Unlock()
	}
}

func (c *cleanupClock) advance(delay time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.time = c.time.Add(delay)
	for timer := range c.timers {
		if !c.time.Before(timer.deadline) {
			timer.ch <- c.time
			delete(c.timers, timer)
		}
	}
}

func (c *cleanupClock) hasTimer(deadline time.Time) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	for timer := range c.timers {
		if timer.deadline.Equal(deadline) {
			return true
		}
	}
	return false
}

// Wall time only bounds test failure. All lifecycle and retry deadlines are
// advanced explicitly by cleanupClock, never by sleeping through backoff.
func awaitCleanup(t *testing.T, condition func() bool) {
	t.Helper()
	deadline := time.After(3 * time.Second)
	tick := time.NewTicker(time.Millisecond)
	defer tick.Stop()
	for !condition() {
		select {
		case <-deadline:
			t.Fatal("cleanup condition timed out")
		case <-tick.C:
		}
	}
}

type cleanupFaults struct {
	hide          atomic.Bool
	write         atomic.Bool
	catalogRename atomic.Bool
	remove        atomic.Bool
	hides         atomic.Int64
	deletes       atomic.Int64
	writes        atomic.Int64
}

func (f *cleanupFaults) hooks(clock *cleanupClock) cleanupHooks {
	hooks := defaultCleanupHooks()
	hooks.now, hooks.newTimer = clock.now, clock.timer
	hooks.rename = func(from, to string) error {
		if filepath.Base(filepath.Dir(to)) == "trash" {
			f.hides.Add(1)
			if f.hide.Load() {
				return os.ErrPermission
			}
		}
		if filepath.Ext(to) == ".json" && f.catalogRename.Load() {
			return os.ErrPermission
		}
		return os.Rename(from, to)
	}
	hooks.writeCatalog = func(file *os.File, data []byte) error {
		f.writes.Add(1)
		if f.write.Load() {
			_, _ = file.Write(data[:len(data)/2])
			return errors.New("secret path or URL must never be logged")
		}
		_, err := file.Write(data)
		return err
	}
	hooks.removeAll = func(path string) error {
		f.deletes.Add(1)
		if f.remove.Load() {
			return os.ErrPermission
		}
		return os.RemoveAll(path)
	}
	return hooks
}

func cleanupConfig(t *testing.T, hooks *cleanupHooks) managerConfig {
	t.Helper()
	return managerConfig{Root: t.TempDir(), StaticURLPrefix: "/resources",
		MaxRetainedBytes: 4096, MaxResourceBytes: 2048, MaxTTL: time.Hour, MaxProduction: time.Minute, hooks: hooks}
}

func cleanupManager(t *testing.T, config managerConfig, manual bool) *originManager {
	t.Helper()
	m, err := newOriginManager(config)
	if err != nil {
		t.Fatal(err)
	}
	if manual {
		// Join the real worker before driving individual bounded batches.
		_ = m.Close()
		m.stop = make(chan struct{})
	}
	t.Cleanup(func() { _ = m.Close() })
	return m
}

func publishCleanup(t *testing.T, m *originManager, key string) (resourceDescriptor, resourceDescriptor) {
	t.Helper()
	acquired, err := m.Acquire(context.Background(), request(key))
	if err != nil || !acquired.Produce {
		t.Fatalf("acquire: %#v, %v", acquired, err)
	}
	if err := os.WriteFile(filepath.Join(acquired.Descriptor.StagingDir, "data"), []byte("payload"), 0o600); err != nil {
		t.Fatal(err)
	}
	ready, err := m.Commit(commitRequest{acquired.Descriptor.ResourceID, acquired.Descriptor.Generation, "data"})
	if err != nil {
		t.Fatal(err)
	}
	return acquired.Descriptor, ready
}

func retireCleanup(t *testing.T, m *originManager, id string) {
	t.Helper()
	m.mu.Lock()
	m.records[id].ExpiresAt = m.now().Add(-time.Second)
	m.mu.Unlock()
	got, err := m.Resolve(id, "")
	if err != nil || got.State != stateExpired || got.URL != "" {
		t.Fatalf("retired resolve: %#v, %v", got, err)
	}
}

func assertGone(t *testing.T, path string) {
	t.Helper()
	if _, err := os.Lstat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("path remains %s: %v", path, err)
	}
}

func assertCleanupCount(t *testing.T, m *originManager, pending int, retained int64) {
	t.Helper()
	m.mu.Lock()
	defer m.mu.Unlock()
	if len(m.garbage) != pending || m.retainedBytes != retained {
		t.Fatalf("pending=%d retained=%d, want %d/%d", len(m.garbage), m.retainedBytes, pending, retained)
	}
}

func TestCleanupRetriesWithoutLiveRecordsOrTraffic(t *testing.T) {
	clock, faults := newCleanupClock(), &cleanupFaults{}
	hooks := faults.hooks(clock)
	config := cleanupConfig(t, &hooks)
	events := make(chan cleanupEvent, 8)
	config.Diagnostic = func(event cleanupEvent) { events <- event }
	m := cleanupManager(t, config, false)
	lease, ready := publishCleanup(t, m, "idle")
	faults.remove.Store(true)
	awaitCleanup(t, func() bool { return clock.hasTimer(clock.now().Add(time.Second)) })
	clock.advance(time.Minute) // Expiry worker, not Resolve or incoming traffic.
	awaitCleanup(t, func() bool { return clock.hasTimer(clock.now().Add(time.Second)) })
	assertCleanupCount(t, m, 1, 0)
	trash := filepath.Join(m.trashDir, lease.ResourceID+"-"+lease.Generation)
	if _, err := os.Stat(filepath.Join(trash, "data")); err != nil {
		t.Fatal(err)
	}
	assertGone(t, filepath.Join(m.readyDir, ready.ResourceID))
	faults.remove.Store(false)
	clock.advance(time.Second) // Only the retired work deadline can wake it.
	awaitCleanup(t, func() bool { m.mu.Lock(); defer m.mu.Unlock(); return len(m.garbage) == 0 })
	assertGone(t, trash)
	assertGone(t, filepath.Join(trash, "data"))
	awaitCleanup(t, func() bool { return len(events) == 2 })
	failed, recovered := <-events, <-events
	if failed.Operation != "trash" || failed.ErrorClass != "permission" || !recovered.Recovered {
		t.Fatalf("events: %#v %#v", failed, recovered)
	}
	if err := m.Close(); err != nil {
		t.Fatal(err)
	}
	clock.mu.Lock()
	defer clock.mu.Unlock()
	if len(clock.timers) != 0 {
		t.Fatal("Close leaked timers")
	}
}

func TestCleanupPartialDeletionAndNewGeneration(t *testing.T) {
	clock, faults := newCleanupClock(), &cleanupFaults{}
	hooks := faults.hooks(clock)
	partial := true
	hooks.removeAll = func(path string) error {
		if partial && filepath.Base(filepath.Dir(path)) == "trash" {
			_ = os.Remove(filepath.Join(path, "data"))
			return os.ErrPermission
		}
		return os.RemoveAll(path)
	}
	m := cleanupManager(t, cleanupConfig(t, &hooks), true)
	lease, _ := publishCleanup(t, m, "same-id")
	retireCleanup(t, m, lease.ResourceID)
	m.cleanupBatch()
	trash := filepath.Join(m.trashDir, lease.ResourceID+"-"+lease.Generation)
	assertGone(t, filepath.Join(trash, "data"))
	if _, err := os.Stat(trash); err != nil {
		t.Fatal(err)
	}
	newLease, ready := publishCleanup(t, m, "same-id")
	if newLease.Generation == lease.Generation {
		t.Fatal("generation reused")
	}
	unrelated := filepath.Join(m.root, "keep")
	if err := os.WriteFile(unrelated, []byte("keep"), 0o600); err != nil {
		t.Fatal(err)
	}
	partial = false
	clock.advance(time.Second)
	m.cleanupBatch()
	assertGone(t, trash)
	assertCleanupCount(t, m, 0, ready.Size)
	if data, err := os.ReadFile(filepath.Join(m.readyDir, ready.ResourceID, "data")); err != nil || string(data) != "payload" {
		t.Fatalf("new generation: %q %v", data, err)
	}
	if _, err := os.Stat(unrelated); err != nil {
		t.Fatal(err)
	}
}

func TestCleanupRenameFailureReservesOnlyItsIdentity(t *testing.T) {
	clock, faults := newCleanupClock(), &cleanupFaults{}
	hooks := faults.hooks(clock)
	m := cleanupManager(t, cleanupConfig(t, &hooks), true)
	lease, _ := publishCleanup(t, m, "blocked")
	faults.hide.Store(true)
	retireCleanup(t, m, lease.ResourceID)
	assertCleanupCount(t, m, 1, 0)
	if _, err := os.Stat(filepath.Join(m.readyDir, lease.ResourceID)); err != nil {
		t.Fatal("expected physically visible old path", err)
	}
	for i := 0; i < 100; i++ {
		m.sweep()
		m.cleanupBatch()
		if _, err := m.Acquire(context.Background(), request("blocked")); errorKind(err) != "conflict" {
			t.Fatalf("reacquire: %v", err)
		}
	}
	if faults.hides.Load() != 1 {
		t.Fatal("rename busy loop")
	}
	_, healthy := publishCleanup(t, m, "healthy")
	faults.hide.Store(false)
	clock.advance(time.Second)
	m.cleanupBatch()
	assertGone(t, filepath.Join(m.readyDir, lease.ResourceID))
	_, newer := publishCleanup(t, m, "blocked")
	assertCleanupCount(t, m, 0, healthy.Size+newer.Size)
}

func TestCleanupCatalogFailureRecoveryAndSupersession(t *testing.T) {
	for _, boundary := range []string{"write", "rename"} {
		for _, supersede := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/supersede=%t", boundary, supersede), func(t *testing.T) {
				clock, faults := newCleanupClock(), &cleanupFaults{}
				hooks := faults.hooks(clock)
				m := cleanupManager(t, cleanupConfig(t, &hooks), true)
				lease, _ := publishCleanup(t, m, "catalog")
				fault := &faults.write
				if boundary == "rename" {
					fault = &faults.catalogRename
				}
				fault.Store(true)
				retireCleanup(t, m, lease.ResourceID)
				assertCleanupCount(t, m, 1, 0)
				assertGone(t, filepath.Join(m.readyDir, lease.ResourceID))
				// Partial catalog writes must not replace the old valid v1 JSON.
				old := readCleanupRecord(t, m, lease.ResourceID)
				if old.State != stateReady || old.Version != 1 {
					t.Fatalf("old catalog: %#v", old)
				}
				fault.Store(false)
				wantState, wantGeneration, retained := stateExpired, lease.Generation, int64(0)
				if supersede {
					newLease, ready := publishCleanup(t, m, "catalog")
					wantState, wantGeneration, retained = stateReady, newLease.Generation, ready.Size
				}
				clock.advance(time.Second)
				m.cleanupBatch()
				got := originRecord{}
				if supersede {
					got = readCleanupRecord(t, m, lease.ResourceID)
				} else {
					assertGone(t, filepath.Join(m.catalogDir, lease.ResourceID+".json"))
					wantState, wantGeneration = "", ""
				}
				if got.State != wantState || got.Generation != wantGeneration {
					t.Fatalf("stale catalog overwrite: %#v", got)
				}
				assertCleanupCount(t, m, 0, retained)
				assertGone(t, filepath.Join(m.trashDir, lease.ResourceID+"-"+lease.Generation))
			})
		}
	}
}

func TestCleanupDeletionDoesNotWaitForCatalogRecovery(t *testing.T) {
	for _, boundary := range []string{"write", "rename"} {
		t.Run(boundary, func(t *testing.T) {
			clock, faults := newCleanupClock(), &cleanupFaults{}
			hooks := faults.hooks(clock)
			m := cleanupManager(t, cleanupConfig(t, &hooks), true)
			lease, ready := publishCleanup(t, m, "catalog-stays-broken")
			fault := &faults.write
			if boundary == "rename" {
				fault = &faults.catalogRename
			}
			fault.Store(true)
			beforeWrites := faults.writes.Load()
			clock.advance(ready.ExpiresAt.Sub(clock.now()))
			m.sweep()
			m.cleanupBatch()
			assertGone(t, filepath.Join(m.readyDir, lease.ResourceID))
			assertGone(t, filepath.Join(m.trashDir, lease.ResourceID+"-"+lease.Generation))
			assertGone(t, lease.StagingDir)
			assertCleanupCount(t, m, 1, 0) // Only metadata remains pending.
			work := m.garbage[lease.ResourceID+"-"+lease.Generation]
			if !work.removed || !work.persist || work.hide || !work.deadline().Equal(clock.now().Add(time.Second)) {
				t.Fatalf("physical deletion blocked by metadata: %#v", work)
			}
			old := readCleanupRecord(t, m, lease.ResourceID)
			if old.State != stateReady || old.Version != 1 || clock.now().Before(old.ExpiresAt) {
				t.Fatalf("expected elapsed v1 ready catalog: %#v", old)
			}
			deletes := faults.deletes.Load()
			for i := 0; i < 100; i++ {
				m.cleanupBatch()
			}
			if faults.writes.Load() != beforeWrites+1 || faults.deletes.Load() != deletes {
				t.Fatal("metadata-only work retried before its deadline")
			}
			clock.advance(time.Second)
			m.cleanupBatch() // Catalog is STILL broken; deletion stays complete.
			if faults.writes.Load() != beforeWrites+2 || faults.deletes.Load() != deletes ||
				!work.deadline().Equal(clock.now().Add(2*time.Second)) {
				t.Fatal("metadata backoff did not remain independent of deletion")
			}
			if got, err := m.Resolve(lease.ResourceID, ""); err != nil || got.State != stateExpired || got.URL != "" {
				t.Fatalf("expired bytes advertised during metadata failure: %#v %v", got, err)
			}
			fault.Store(false)
			clock.advance(2 * time.Second)
			m.cleanupBatch()
			assertCleanupCount(t, m, 0, 0)
			assertGone(t, filepath.Join(m.catalogDir, lease.ResourceID+".json"))
			if faults.deletes.Load() != deletes {
				t.Fatal("metadata repair repeated completed deletion")
			}
		})
	}
}

func readCleanupRecord(t *testing.T, m *originManager, id string) originRecord {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(m.catalogDir, id+".json"))
	if err != nil {
		t.Fatal(err)
	}
	var record originRecord
	if err := json.Unmarshal(data, &record); err != nil {
		t.Fatal(err)
	}
	return record
}

func TestCleanupBackoffBatchFairnessAndDiagnostics(t *testing.T) {
	clock, faults := newCleanupClock(), &cleanupFaults{}
	hooks := faults.hooks(clock)
	config := cleanupConfig(t, &hooks)
	var events []cleanupEvent
	config.Diagnostic = func(event cleanupEvent) { events = append(events, event) }
	m := cleanupManager(t, config, true)
	for i := 0; i < cleanupBatchSize+1; i++ {
		lease, _ := publishCleanup(t, m, fmt.Sprintf("batch-%d", i))
		retireCleanup(t, m, lease.ResourceID)
	}
	faults.remove.Store(true)
	m.cleanupBatch()
	if faults.deletes.Load() != cleanupBatchSize {
		t.Fatalf("batch size: %d", faults.deletes.Load())
	}
	m.cleanupBatch()
	if faults.deletes.Load() != cleanupBatchSize+1 {
		t.Fatal("oldest remaining entry starved")
	}
	for i := 0; i < 100; i++ {
		m.cleanupBatch()
	}
	if faults.deletes.Load() != cleanupBatchSize+1 {
		t.Fatal("failure busy loop")
	}
	m.emitCleanupEvents()
	if len(events) != 1 || events[0].Pending != cleanupBatchSize+1 {
		t.Fatalf("unbounded diagnostics: %#v", events)
	}
	for _, delay := range []time.Duration{time.Second, 2 * time.Second, 4 * time.Second, 8 * time.Second, 16 * time.Second, 32 * time.Second, time.Minute, time.Minute} {
		for _, work := range m.garbage {
			if got := work.next.Sub(clock.now()); got != delay {
				t.Fatalf("backoff=%v, want %v", got, delay)
			}
		}
		clock.advance(delay)
		m.cleanupBatch()
		m.cleanupBatch()
		m.emitCleanupEvents()
	}
	// A healthy generation is processed despite the failed inventory.
	faults.remove.Store(false)
	lease, _ := publishCleanup(t, m, "healthy-after-failures")
	retireCleanup(t, m, lease.ResourceID)
	m.cleanupBatch()
	assertGone(t, filepath.Join(m.trashDir, lease.ResourceID+"-"+lease.Generation))
	if len(events) != 9 {
		t.Fatalf("diagnostic count: %d", len(events))
	}
}

func TestCleanupRestartAtRetirementBoundaries(t *testing.T) {
	for _, boundary := range []string{"before_hide", "after_hide", "after_catalog", "after_delete_before_catalog", "after_delete_before_catalog_rename", "partial_delete", "terminal_with_ready"} {
		t.Run(boundary, func(t *testing.T) {
			clock, faults := newCleanupClock(), &cleanupFaults{}
			hooks := faults.hooks(clock)
			config := cleanupConfig(t, &hooks)
			m := cleanupManager(t, config, true)
			lease, _ := publishCleanup(t, m, "restart-old")
			_, healthy := publishCleanup(t, m, "restart-valid")
			unrelated := filepath.Join(m.root, "unrelated")
			if err := os.WriteFile(unrelated, []byte("keep"), 0o600); err != nil {
				t.Fatal(err)
			}
			// Persist the elapsed ready record as it would exist before a crash.
			record := m.records[lease.ResourceID]
			record.ExpiresAt = clock.now().Add(-time.Second)
			if err := m.persistLocked(record); err != nil {
				t.Fatal(err)
			}
			switch boundary {
			case "before_hide":
				faults.hide.Store(true)
			case "after_hide", "after_delete_before_catalog":
				faults.write.Store(true)
			case "after_delete_before_catalog_rename":
				faults.catalogRename.Store(true)
			case "terminal_with_ready":
				record.State = stateExpired
				if err := m.persistLocked(record); err != nil {
					t.Fatal(err)
				}
			}
			retireCleanup(t, m, lease.ResourceID)
			trash := filepath.Join(m.trashDir, lease.ResourceID+"-"+lease.Generation)
			if boundary == "after_delete_before_catalog" || boundary == "after_delete_before_catalog_rename" {
				m.cleanupBatch()
				assertGone(t, trash)
				if got := readCleanupRecord(t, m, lease.ResourceID); got.State != stateReady {
					t.Fatalf("expected unrepaired catalog at crash: %#v", got)
				}
			}
			if boundary == "partial_delete" {
				if err := os.Remove(filepath.Join(trash, "data")); err != nil {
					t.Fatal(err)
				}
			}
			faults.hide.Store(false)
			faults.write.Store(false)
			faults.catalogRename.Store(false)
			// Discard all in-memory retry state; reconstruct from catalog v1.
			restarted := cleanupManager(t, config, true)
			assertCleanupCount(t, restarted, 0, healthy.Size)
			assertGone(t, trash)
			assertGone(t, filepath.Join(m.readyDir, lease.ResourceID))
			if got, err := restarted.Resolve(lease.ResourceID, ""); err != nil || got.State != stateNotFound || got.URL != "" {
				t.Fatalf("expired restored: %#v %v", got, err)
			}
			if got, err := restarted.Resolve(healthy.ResourceID, ""); err != nil || got.State != stateReady {
				t.Fatalf("valid ready lost: %#v %v", got, err)
			}
			if data, err := os.ReadFile(unrelated); err != nil || string(data) != "keep" {
				t.Fatal("unrelated file removed", err)
			}
		})
	}
}

func TestCleanupStartupDeletionRemainsFailFast(t *testing.T) {
	clock, faults := newCleanupClock(), &cleanupFaults{}
	hooks := faults.hooks(clock)
	config := cleanupConfig(t, &hooks)
	m := cleanupManager(t, config, true)
	lease, _ := publishCleanup(t, m, "startup")
	retireCleanup(t, m, lease.ResourceID)
	faults.remove.Store(true)
	if restarted, err := newOriginManager(config); err == nil {
		_ = restarted.Close()
		t.Fatal("startup ignored deletion failure")
	}
	faults.remove.Store(false)
	restarted := cleanupManager(t, config, true)
	assertCleanupCount(t, restarted, 0, 0)
}

func TestCleanupPendingTimeoutPersistenceAndNewStaging(t *testing.T) {
	clock, faults := newCleanupClock(), &cleanupFaults{}
	hooks := faults.hooks(clock)
	m := cleanupManager(t, cleanupConfig(t, &hooks), true)
	old, err := m.Acquire(context.Background(), request("pending"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(old.Descriptor.StagingDir, "partial"), []byte("old"), 0o600); err != nil {
		t.Fatal(err)
	}
	faults.write.Store(true)
	clock.advance(time.Second)
	m.sweep()
	assertCleanupCount(t, m, 1, 0)
	if got, err := m.Resolve(old.Descriptor.ResourceID, ""); err != nil || got.State != stateFailed || got.FailureKind != "production_timeout" {
		t.Fatalf("timeout: %#v %v", got, err)
	}
	faults.write.Store(false)
	r := request("pending")
	r.ProductionTimeout = time.Minute
	newLease, err := m.Acquire(context.Background(), r)
	if err != nil {
		t.Fatal(err)
	}
	newFile := filepath.Join(newLease.Descriptor.StagingDir, "new")
	if err := os.WriteFile(newFile, []byte("new"), 0o600); err != nil {
		t.Fatal(err)
	}
	faults.remove.Store(true)
	clock.advance(time.Second)
	m.cleanupBatch()
	faults.remove.Store(false)
	clock.advance(2 * time.Second)
	m.cleanupBatch()
	assertGone(t, old.Descriptor.StagingDir)
	if data, err := os.ReadFile(newFile); err != nil || string(data) != "new" {
		t.Fatalf("new staging deleted: %q %v", data, err)
	}
	if got := readCleanupRecord(t, m, old.Descriptor.ResourceID); got.Generation != newLease.Descriptor.Generation || got.State != statePending {
		t.Fatalf("stale pending snapshot: %#v", got)
	}
	assertCleanupCount(t, m, 0, 0)
}

func TestCleanupFailedAcquireDoesNotLeakStaging(t *testing.T) {
	clock, faults := newCleanupClock(), &cleanupFaults{}
	hooks := faults.hooks(clock)
	m := cleanupManager(t, cleanupConfig(t, &hooks), true)
	faults.write.Store(true)
	if _, err := m.Acquire(context.Background(), request("failed-acquire")); err == nil {
		t.Fatal("expected catalog failure")
	}
	assertCleanupCount(t, m, 1, 0)
	faults.remove.Store(true)
	m.cleanupBatch()
	faults.write.Store(false)
	faults.remove.Store(false)
	_, ready := publishCleanup(t, m, "failed-acquire")
	clock.advance(time.Second)
	m.cleanupBatch()
	assertCleanupCount(t, m, 0, ready.Size)
	assertGone(t, filepath.Join(m.stagingDir, ready.ResourceID))
}

func TestCleanupSlowDeleteAllowsPublicationAndCloseJoins(t *testing.T) {
	clock := newCleanupClock()
	hooks := defaultCleanupHooks()
	hooks.now, hooks.newTimer = clock.now, clock.timer
	started, release := make(chan struct{}), make(chan struct{})
	var once sync.Once
	hooks.removeAll = func(path string) error {
		if filepath.Base(filepath.Dir(path)) == "trash" {
			once.Do(func() { close(started) })
			<-release
		}
		return os.RemoveAll(path)
	}
	m := cleanupManager(t, cleanupConfig(t, &hooks), false)
	// Always unblock before the manager cleanup, even on a failed assertion.
	var releaseOnce sync.Once
	t.Cleanup(func() { releaseOnce.Do(func() { close(release) }) })
	lease, _ := publishCleanup(t, m, "slow")
	retireCleanup(t, m, lease.ResourceID)
	select {
	case <-started:
	case <-time.After(3 * time.Second):
		t.Fatal("delete did not start")
	}
	progress := make(chan error, 1)
	go func() {
		acquired, err := m.Acquire(context.Background(), request("unrelated"))
		if err == nil {
			err = os.WriteFile(filepath.Join(acquired.Descriptor.StagingDir, "data"), []byte("ok"), 0o600)
		}
		if err == nil {
			_, err = m.Commit(commitRequest{acquired.Descriptor.ResourceID, acquired.Descriptor.Generation, "data"})
		}
		if err == nil {
			_, err = m.Resolve(acquired.Descriptor.ResourceID, "")
		}
		progress <- err
	}()
	select {
	case err := <-progress:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("recursive delete held publication mutex")
	}
	closed := make(chan struct{})
	go func() { _ = m.Close(); close(closed) }()
	awaitCleanup(t, func() bool {
		select {
		case <-m.stop:
			return true
		default:
			return false
		}
	})
	select {
	case <-closed:
		t.Fatal("Close returned with deletion still running")
	default:
	}
	releaseOnce.Do(func() { close(release) })
	select {
	case <-closed:
	case <-time.After(3 * time.Second):
		t.Fatal("Close failed to join")
	}
	assertGone(t, filepath.Join(m.trashDir, lease.ResourceID+"-"+lease.Generation))
}

func TestCleanupCloseCancelsQueuedRetries(t *testing.T) {
	clock, faults := newCleanupClock(), &cleanupFaults{}
	hooks := faults.hooks(clock)
	m := cleanupManager(t, cleanupConfig(t, &hooks), false)
	lease, _ := publishCleanup(t, m, "shutdown")
	faults.remove.Store(true)
	retireCleanup(t, m, lease.ResourceID)
	awaitCleanup(t, func() bool { return clock.hasTimer(clock.now().Add(time.Second)) })
	_ = m.Close()
	before := faults.deletes.Load()
	clock.advance(time.Hour)
	m.signal()
	if faults.deletes.Load() != before {
		t.Fatal("cleanup continued after Close")
	}
	assertCleanupCount(t, m, 1, 0)
}

func TestCleanupFailedCommitRollbackRetiresOwnedReadyTree(t *testing.T) {
	clock, faults := newCleanupClock(), &cleanupFaults{}
	hooks := faults.hooks(clock)
	rename := hooks.rename
	hooks.rename = func(from, to string) error {
		if filepath.Base(filepath.Dir(from)) == "ready" && strings.Contains(to, string(filepath.Separator)+"staging"+string(filepath.Separator)) {
			return os.ErrPermission
		}
		return rename(from, to)
	}
	m := cleanupManager(t, cleanupConfig(t, &hooks), true)
	lease, err := m.Acquire(context.Background(), request("rollback"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(lease.Descriptor.StagingDir, "data"), []byte("old"), 0o600); err != nil {
		t.Fatal(err)
	}
	faults.write.Store(true)
	faults.hide.Store(true)
	if _, err := m.Commit(commitRequest{lease.Descriptor.ResourceID, lease.Descriptor.Generation, "data"}); err == nil {
		t.Fatal("commit succeeded with catalog failure")
	}
	if got, err := m.Resolve(lease.Descriptor.ResourceID, ""); err != nil || got.State != stateFailed || got.URL != "" {
		t.Fatalf("failed publication visible: %#v %v", got, err)
	}
	if _, err := m.Acquire(context.Background(), request("rollback")); errorKind(err) != "conflict" {
		t.Fatalf("reserved identity: %v", err)
	}
	faults.write.Store(false)
	faults.hide.Store(false)
	clock.advance(time.Second)
	m.cleanupBatch()
	assertCleanupCount(t, m, 0, 0)
	assertGone(t, filepath.Join(m.readyDir, lease.Descriptor.ResourceID))
	_, _ = publishCleanup(t, m, "rollback")
}
