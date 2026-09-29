package main

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// checkConfig builds a manager configuration for deterministic scanner turns.
// The worker is joined immediately so background scheduling cannot race the
// manually driven batches below.
func checkConfig(t *testing.T, customize func(*cleanupHooks)) managerConfig {
	t.Helper()
	hooks := defaultCleanupHooks()
	if customize != nil {
		customize(&hooks)
	}
	return managerConfig{Root: t.TempDir(), StaticURLPrefix: "/check",
		MaxRetainedBytes: 1 << 20, MaxResourceBytes: 4096, MaxTTL: time.Hour,
		MaxProduction: time.Hour, hooks: &hooks, Diagnostic: func(cleanupEvent) {}}
}

func checkManual(t *testing.T, config managerConfig) *originManager {
	t.Helper()
	m, err := newOriginManager(config)
	if err != nil {
		t.Fatal(err)
	}
	_ = m.Close()
	// Join the worker first, then close any cursor the joined loop left open
	// before the same root starts a new manager or the test ends.
	m.stop = make(chan struct{})
	t.Cleanup(func() {
		_ = m.Close()
		m.mu.Lock()
		m.closeResidueCursors()
		m.mu.Unlock()
	})
	return m
}

func checkScan(t *testing.T, m *originManager) {
	t.Helper()
	m.scanNext = time.Time{}
	m.scanResidueBatch()
}

func checkClaimCount(t *testing.T, m *originManager) int {
	t.Helper()
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.garbage)
}

func checkScanUntilClaim(t *testing.T, m *originManager) {
	t.Helper()
	for turn := 0; turn < 8; turn++ {
		m.mu.Lock()
		m.scanRoot = 0
		m.mu.Unlock()
		for i := 0; i < 4; i++ {
			checkScan(t, m)
			if checkClaimCount(t, m) > 0 {
				return
			}
		}
	}
	t.Fatal("scan did not claim residue within one full rotation set")
}

func TestCheckScannerBatchBoundAndFairness(t *testing.T) {
	m := checkManual(t, checkConfig(t, nil))
	orphanID := strings.Repeat("c", 64)
	paths := make([]string, 0, 71)
	for i := 0; i < 35; i++ {
		paths = append(paths, filepath.Join(m.catalogDir, fmt.Sprintf("leftover-%02d", i)))
	}
	for i := 0; i < 35; i++ {
		paths = append(paths, filepath.Join(m.readyDir, fmt.Sprintf("stale-%02d", i)))
	}
	// A valid-ID catalog leaf without any in-memory record is still residue.
	paths = append(paths, filepath.Join(m.catalogDir, orphanID+".json"))
	for _, path := range paths {
		if err := os.WriteFile(path, []byte("residue"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	for turn := 0; ; turn++ {
		before := checkClaimCount(t, m)
		checkScan(t, m)
		claimed := checkClaimCount(t, m) - before
		if claimed > 32 {
			t.Fatalf("A5: scanner claimed %d candidates in one turn", claimed)
		}
		m.cleanupBatch()
		remaining := 0
		for _, path := range paths {
			if _, err := os.Lstat(path); !os.IsNotExist(err) {
				remaining++
			}
		}
		if remaining == 0 {
			if turn == 0 {
				t.Fatal("scanner claimed the entire inventory in one turn")
			}
			break
		}
		if turn > 200 {
			t.Fatalf("A5: %d residue paths were not drained in bounded turns", remaining)
		}
	}
	if got, err := m.Resolve(orphanID, ""); err != nil || got.State != stateNotFound {
		t.Fatalf("A3: orphan valid-ID catalog recovered a resource: %#v %v", got, err)
	}
}

func TestCheckHideReservationProtectsMissingRecord(t *testing.T) {
	m := checkManual(t, checkConfig(t, nil))
	owned := strings.Repeat("d", 64)
	readyPath := filepath.Join(m.readyDir, owned)
	if err := os.MkdirAll(readyPath, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(readyPath, "data"), []byte("reserved"), 0o600); err != nil {
		t.Fatal(err)
	}
	// A hide reservation with no in-memory record still owns the ready path.
	m.mu.Lock()
	m.garbage[owned+"-missing"] = &cleanupWork{
		record:  &originRecord{ResourceID: owned, Generation: strings.Repeat("e", 32), State: stateExpired},
		hide:    true,
		created: m.now(),
	}
	m.mu.Unlock()
	deadline := time.Now().Add(750 * time.Millisecond)
	for time.Now().Before(deadline) {
		checkScan(t, m)
		data, err := os.ReadFile(filepath.Join(readyPath, "data"))
		if err != nil {
			t.Fatalf("A4: scanner claimed hide-owned ready without a record: %v", err)
		}
		if string(data) != "reserved" {
			t.Fatalf("A4: hide-owned ready bytes changed: %q", data)
		}
		time.Sleep(time.Millisecond)
	}
}

// A failed partial pass stays pending through its later successful EOF.
// Only a subsequent entirely healthy pass can clear that diagnostic.
func TestCheckScanErrorsFailureOnly(t *testing.T) {
	for _, failFirst := range []bool{false, true} {
		name := "healthy"
		if failFirst {
			name = "failure_then_clean"
		}
		t.Run(name, func(t *testing.T) {
			m := checkManual(t, checkConfig(t, nil))
			for i := 0; i < 40; i++ {
				if err := os.WriteFile(filepath.Join(m.catalogDir, fmt.Sprintf("stale-%02d", i)), []byte("residue"), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			reject := failFirst
			m.hooks.rename = func(from, to string) error {
				if reject {
					return os.ErrPermission
				}
				return os.Rename(from, to)
			}
			scan := func() {
				m.mu.Lock()
				m.scanRoot = 0
				m.scanNext = time.Time{}
				if work := m.scanErrors[0]; work != nil {
					work.next = time.Time{}
				}
				m.mu.Unlock()
				m.scanResidueBatch()
			}
			scan() // First 32 entries: all fail or all succeed.
			if m.residue[0].root == nil {
				t.Fatal("fixture did not leave a partial scan")
			}
			work := m.scanErrors[0]
			if !failFirst {
				if work != nil {
					t.Fatal("healthy partial scan registered backlog")
				}
				scan() // Remaining eight entries and EOF.
				if m.scanErrors[0] != nil || m.residue[0].root != nil {
					t.Fatal("healthy full pass retained backlog or cursor")
				}
				return
			}
			if work == nil || work.failures != 32 {
				t.Fatalf("first batch did not record every failed claim: %#v", work)
			}
			if len(m.cleanupEvents) == 0 || m.cleanupEvents[0].Pending < 1 {
				t.Fatal("first failure diagnostic omitted its own pending work")
			}
			reject = false
			scan() // Same pass reaches EOF without any further failure.
			if m.residue[0].root != nil {
				t.Fatal("second batch did not reach EOF")
			}
			if m.scanErrors[0] != work || work.failures != 32 {
				t.Fatal("earlier failed batch backlog cleared at same-pass successful EOF")
			}
			scan() // Next full pass claims the 32 formerly failing entries.
			scan() // EOF, with no failure anywhere in this new pass.
			if m.scanErrors[0] != nil || m.residue[0].root != nil {
				t.Fatal("subsequent clean full pass did not clear recovered backlog")
			}
		})
	}
}

func TestCheckLateRecreationAndQuarantineRestart(t *testing.T) {
	config := checkConfig(t, nil)
	m := checkManual(t, config)
	oldID := strings.Repeat("f", 64)
	residue := filepath.Join(m.stagingDir, oldID, strings.Repeat("0", 32))
	if err := os.MkdirAll(residue, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(residue, "late"), []byte("v1"), 0o600); err != nil {
		t.Fatal(err)
	}
	checkScan(t, m)
	checkScanUntilClaim(t, m)
	quarantine := checkQuarantine(t, m)
	if quarantine == "" {
		t.Fatal("A3: old staging residue was not claimed")
	}
	m.cleanupBatch()
	if _, err := os.Lstat(residue); !os.IsNotExist(err) {
		t.Fatalf("A3: claimed residue retained: %v", err)
	}
	// A late producer recreates the identical retired path after isolation.
	if err := os.MkdirAll(residue, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(residue, "late"), []byte("v2"), 0o600); err != nil {
		t.Fatal(err)
	}
	checkScanUntilClaim(t, m)
	m.cleanupBatch()
	if _, err := os.Lstat(residue); !os.IsNotExist(err) {
		t.Fatal("A3: recreated old-generation residue retained")
	}
	// A claimed quarantine that survives manager stop is rediscovered by the
	// next startup, modeling an interruption between claim and deletion.
	m.closeResidueCursors() // The stopped fixture reopened cursors during manual scans.
	m2 := checkManual(t, config)
	target := filepath.Join(m2.stagingDir, oldID, strings.Repeat("1", 32))
	if err := os.MkdirAll(target, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(target, "late"), []byte("v3"), 0o600); err != nil {
		t.Fatal(err)
	}
	checkScanUntilClaim(t, m2)
	quarantine = checkQuarantine(t, m2)
	// Deliberately drop the in-memory work by closing without draining.
	_ = m2.Close()
	m2.mu.Lock()
	m2.closeResidueCursors()
	m2.mu.Unlock()
	if _, err := os.Lstat(quarantine); err != nil {
		t.Fatalf("A3: claimed quarantine missing before restart: %v", err)
	}
	restarted := checkManual(t, config)
	if _, err := os.Lstat(quarantine); !os.IsNotExist(err) {
		t.Fatalf("A3: restart retained isolated quarantine: %v", err)
	}
	_ = restarted
}

func checkQuarantine(t *testing.T, m *originManager) string {
	t.Helper()
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, work := range m.garbage {
		if work.staging == "" && work.trash != "" {
			return work.trash
		}
	}
	return ""
}

func TestCheckScanFailureDiagnostics(t *testing.T) {
	m := checkManual(t, checkConfig(t, nil))
	residue := filepath.Join(m.catalogDir, "diagnostic-leftover")
	if err := os.WriteFile(residue, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	m.hooks.rename = func(from, to string) error {
		if from == residue {
			return os.ErrPermission
		}
		return os.Rename(from, to)
	}
	checkScan(t, m)
	m.mu.Lock()
	if len(m.cleanupEvents) == 0 {
		m.mu.Unlock()
		t.Fatal("A5: first scan failure produced no diagnostic")
	}
	if first := m.cleanupEvents[0]; first.Pending < 1 {
		m.mu.Unlock()
		t.Fatalf("A5: first scan failure counted no pending work: %#v", first)
	}
	work := m.scanErrors[0]
	m.mu.Unlock()
	if work == nil {
		t.Fatal("A5: failing scan kept no retry state")
	}
	// Drive a second failure past the manager-wide event rate limit.
	m.mu.Lock()
	m.scanRoot = 0
	work.next = time.Time{}
	work.created = m.now().Add(-2 * time.Hour)
	m.nextDiagnostic = time.Time{}
	m.mu.Unlock()
	checkScan(t, m)
	m.mu.Lock()
	defer m.mu.Unlock()
	if len(m.cleanupEvents) < 2 {
		t.Fatalf("A5: repeated scan failure not diagnosed: %d", len(m.cleanupEvents))
	}
	last := m.cleanupEvents[len(m.cleanupEvents)-1]
	if last.OldestWait <= 0 {
		t.Fatalf("A5: oldest backlog age not reported: %#v", last)
	}
	if last.Pending < 1 {
		t.Fatalf("A5: scan backlog not counted as pending: %#v", last)
	}
	if last.ErrorClass != "permission" {
		t.Fatalf("A5: wrong error class: %#v", last)
	}
}

// C10: payload deletion runs outside the mutex, so a same-ID Acquire can
// replace the record between removal and terminal collection. The pointer
// recheck inside that critical section is the only protection for the new
// generation's catalog.
func TestCheckGCSupersessionPointerGuard(t *testing.T) {
	entered, release := make(chan struct{}), make(chan struct{})
	var enteredOnce, releaseOnce sync.Once
	var target atomic.Value
	target.Store("")
	m := checkManual(t, checkConfig(t, func(h *cleanupHooks) {
		h.removeAll = func(path string) error {
			if path == target.Load().(string) {
				enteredOnce.Do(func() { close(entered) })
				<-release
			}
			return os.RemoveAll(path)
		}
	}))
	t.Cleanup(func() { releaseOnce.Do(func() { close(release) }) })
	lease, err := m.Acquire(context.Background(), request("guard-barrier"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(lease.Descriptor.StagingDir, "old"), []byte("old"), 0o600); err != nil {
		t.Fatal(err)
	}
	target.Store(lease.Descriptor.StagingDir)
	if _, err = m.Fail(failRequest{ResourceID: lease.Descriptor.ResourceID, Generation: lease.Descriptor.Generation, Kind: "test"}); err != nil {
		t.Fatal(err)
	}
	batched := make(chan struct{})
	go func() { m.cleanupBatch(); close(batched) }()
	independentReceive(t, entered, "old deletion did not reach the barrier")
	replacement, err := m.Acquire(context.Background(), request("guard-barrier"))
	if err != nil || !replacement.Produce {
		t.Fatalf("A2: replacement acquire during old deletion: %#v %v", replacement, err)
	}
	if replacement.Descriptor.Generation == lease.Descriptor.Generation {
		t.Fatal("A2: generation was reused")
	}
	// Deliver the replacement waiter before releasing the barrier. Otherwise
	// the waiter-delivery precondition alone masks the pointer race window.
	if err := os.WriteFile(filepath.Join(replacement.Descriptor.StagingDir, "new"), []byte("new"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := m.Commit(commitRequest{ResourceID: replacement.Descriptor.ResourceID, Generation: replacement.Descriptor.Generation, Entrypoint: "new"}); err != nil {
		t.Fatalf("A2: replacement commit failed during old deletion: %v", err)
	}
	releaseOnce.Do(func() { close(release) })
	select {
	case <-batched:
	case <-time.After(3 * time.Second):
		t.Fatal("old cleanup did not finish after release")
	}
	catalog := filepath.Join(m.catalogDir, lease.Descriptor.ResourceID+".json")
	if got, err := os.ReadFile(catalog); err != nil || !bytes.Contains(got, []byte(replacement.Descriptor.Generation)) {
		t.Fatalf("A2: old GC damaged replacement catalog: %q %v", got, err)
	}
	if got, err := m.Resolve(lease.Descriptor.ResourceID, ""); err != nil || got.State != stateReady {
		t.Fatalf("A2: replacement record lost to old GC: %#v %v", got, err)
	}
	if _, err := m.Fail(failRequest{ResourceID: lease.Descriptor.ResourceID, Generation: lease.Descriptor.Generation, Kind: "test"}); err == nil {
		t.Fatal("A2: stale token accepted after guarded GC")
	}
}

// C11: a live catalog temporary is observed only under the publication mutex,
// so a scanner turn racing an in-flight persist cannot claim it.
func TestCheckScannerCannotClaimLivePersistTemp(t *testing.T) {
	entered, release := make(chan struct{}), make(chan struct{})
	var enteredOnce, releaseOnce sync.Once
	m := checkManual(t, checkConfig(t, func(h *cleanupHooks) {
		write := h.writeCatalog
		h.writeCatalog = func(f *os.File, data []byte) error {
			enteredOnce.Do(func() { close(entered) })
			<-release
			return write(f, data)
		}
	}))
	t.Cleanup(func() { releaseOnce.Do(func() { close(release) }) })
	acquired := make(chan error, 1)
	go func() {
		_, err := m.Acquire(context.Background(), request("persist-temp"))
		acquired <- err
	}()
	independentReceive(t, entered, "fixture persist did not reach the barrier")
	scanned := make(chan struct{})
	go func() { m.scanResidueBatch(); close(scanned) }()
	releaseOnce.Do(func() { close(release) })
	if err := <-acquired; err != nil {
		t.Fatal(err)
	}
	select {
	case <-scanned:
	case <-time.After(3 * time.Second):
		t.Fatal("scanner did not finish after persist released")
	}
	if checkClaimCount(t, m) != 0 {
		t.Fatal("A4: scanner claimed during an in-flight catalog persist")
	}
}
