package main

import (
	"bytes"
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

// These cases were specified before candidate inspection. Helpers deliberately
// do not depend on the implement worker's tests or new lifecycle interfaces.
type independentTimer struct {
	at time.Time
	ch chan time.Time
}

type independentClock struct {
	mu     sync.Mutex
	at     time.Time
	timers map[*independentTimer]bool
	armed  chan time.Duration
}

func newIndependentClock() *independentClock {
	return &independentClock{at: time.Date(2034, 2, 3, 4, 5, 6, 0, time.UTC), timers: make(map[*independentTimer]bool), armed: make(chan time.Duration, 4096)}
}

func (c *independentClock) now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.at
}

func (c *independentClock) timer(delay time.Duration) (<-chan time.Time, func()) {
	c.mu.Lock()
	timer := &independentTimer{at: c.at.Add(delay), ch: make(chan time.Time, 1)}
	if delay <= 0 {
		timer.ch <- c.at
	} else {
		c.timers[timer] = true
	}
	c.mu.Unlock()
	select {
	case c.armed <- delay:
	default:
	}
	return timer.ch, func() { c.mu.Lock(); delete(c.timers, timer); c.mu.Unlock() }
}

func (c *independentClock) advance(delay time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.at = c.at.Add(delay)
	for timer := range c.timers {
		if !c.at.Before(timer.at) {
			timer.ch <- c.at
			delete(c.timers, timer)
		}
	}
}

func (c *independentClock) drainArms() {
	for {
		select {
		case <-c.armed:
		default:
			return
		}
	}
}

func independentAwait(t *testing.T, assertion string, condition func() bool) {
	t.Helper()
	timeout := time.NewTimer(3 * time.Second)
	defer timeout.Stop()
	tick := time.NewTicker(time.Millisecond)
	defer tick.Stop()
	for !condition() {
		select {
		case <-timeout.C:
			t.Fatal(assertion)
		case <-tick.C:
		}
	}
}

func independentReceive(t *testing.T, ch <-chan struct{}, assertion string) {
	t.Helper()
	select {
	case <-ch:
	case <-time.After(3 * time.Second):
		t.Fatal(assertion)
	}
}

func independentGone(path string) bool {
	_, err := os.Lstat(path)
	return errors.Is(err, os.ErrNotExist)
}

func independentConfig(t *testing.T, clock *independentClock, customize func(*cleanupHooks)) managerConfig {
	t.Helper()
	hooks := defaultCleanupHooks()
	hooks.now, hooks.newTimer = clock.now, clock.timer
	if customize != nil {
		customize(&hooks)
	}
	return managerConfig{Root: t.TempDir(), StaticURLPrefix: "/independent",
		MaxRetainedBytes: 1 << 20, MaxResourceBytes: 4096, MaxTTL: 24 * time.Hour, MaxProduction: 24 * time.Hour, hooks: &hooks}
}

func independentOpen(t *testing.T, config managerConfig) *originManager {
	t.Helper()
	m, err := newOriginManager(config)
	if err != nil {
		t.Fatalf("fixture manager initialization: %v", err)
	}
	t.Cleanup(func() {
		if err := m.Close(); err != nil {
			t.Errorf("fixture close: %v", err)
		}
	})
	return m
}

func independentRequest(key string) acquireRequest {
	return acquireRequest{Key: key, Fingerprint: "independent-v1", TTL: time.Hour, MaxBytes: 1024, ProductionTimeout: time.Hour}
}

func independentAcquire(t *testing.T, m *originManager, request acquireRequest) resourceDescriptor {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	result, err := m.Acquire(ctx, request)
	if err != nil || !result.Produce {
		t.Fatalf("fixture acquire: %#v %v", result, err)
	}
	return result.Descriptor
}

func independentWrite(t *testing.T, path, value string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(value), 0600); err != nil {
		t.Fatal(err)
	}
}

func independentPublish(t *testing.T, m *originManager, request acquireRequest, value string) resourceDescriptor {
	t.Helper()
	lease := independentAcquire(t, m, request)
	independentWrite(t, filepath.Join(lease.StagingDir, "data"), value)
	if _, err := m.Commit(commitRequest{ResourceID: lease.ResourceID, Generation: lease.Generation, Entrypoint: "data"}); err != nil {
		t.Fatal(err)
	}
	return lease
}

func independentFail(t *testing.T, m *originManager, lease resourceDescriptor) {
	t.Helper()
	result, err := m.Fail(failRequest{ResourceID: lease.ResourceID, Generation: lease.Generation, Kind: "independent_failure"})
	if err != nil || result.State != stateFailed {
		t.Fatalf("fixture fail: %#v %v", result, err)
	}
}

func independentKind(err error) string {
	var problem *originError
	if errors.As(err, &problem) {
		return problem.Kind
	}
	return ""
}

func independentRead(t *testing.T, path string) []byte {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func TestIndependentTerminalCatalogAndResolveReleased(t *testing.T) {
	clock := newIndependentClock()
	config := independentConfig(t, clock, nil)
	m := independentOpen(t, config)
	var ids []string
	for i := 0; i < 40; i++ {
		lease := independentAcquire(t, m, independentRequest(fmt.Sprintf("history-%02d", i)))
		independentWrite(t, filepath.Join(lease.StagingDir, "partial"), "old")
		independentFail(t, m, lease)
		ids = append(ids, lease.ResourceID)
	}
	// No logical time advance: failed generations have no retention period.
	independentAwait(t, "A1: completed terminal catalog files remain without a retention advance", func() bool {
		for _, id := range ids {
			if !independentGone(filepath.Join(config.Root, "catalog", id+".json")) {
				return false
			}
		}
		return true
	})
	for _, id := range ids {
		got, err := m.Resolve(id, "")
		if err != nil || got.State != stateNotFound {
			t.Errorf("A1: GC did not release Resolve state for %s: %#v %v", id, got, err)
		}
	}
}

// Done is evaluated only once Acquire has bound the existing pending waiter.
// This gives a public-call barrier without peeking into the waiter map.
type independentWaitContext struct {
	context.Context
	bound chan struct{}
	once  sync.Once
}

func (c *independentWaitContext) Done() <-chan struct{} {
	c.once.Do(func() { close(c.bound) })
	return c.Context.Done()
}

func TestIndependentDeliveredWaiterSurvivesGC(t *testing.T) {
	clock := newIndependentClock()
	config := independentConfig(t, clock, nil)
	m := independentOpen(t, config)
	req := independentRequest("waiter")
	lease := independentAcquire(t, m, req)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	wait := &independentWaitContext{Context: ctx, bound: make(chan struct{})}
	type outcome struct {
		result acquireResult
		err    error
	}
	result := make(chan outcome, 1)
	go func() { got, err := m.Acquire(wait, req); result <- outcome{got, err} }()
	independentReceive(t, wait.bound, "waiter never bound before Fail")
	independentFail(t, m, lease)
	var delivered outcome
	select {
	case delivered = <-result:
	case <-time.After(3 * time.Second):
		t.Fatal("A6: terminal waiter not delivered")
	}
	if delivered.err != nil || delivered.result.Produce || delivered.result.Descriptor.State != stateFailed {
		t.Fatalf("A6: waiter terminal result: %#v", delivered)
	}
	independentAwait(t, "A1: delivered waiter incorrectly retains terminal catalog", func() bool { return independentGone(filepath.Join(config.Root, "catalog", lease.ResourceID+".json")) })
	if delivered.result.Descriptor.State != stateFailed {
		t.Fatal("A6: GC changed delivered waiter result")
	}
}

func TestIndependentOldDeleteBarrierPreservesReplacement(t *testing.T) {
	clock := newIndependentClock()
	entered, release := make(chan struct{}), make(chan struct{})
	var enteredOnce, releaseOnce sync.Once
	var target atomic.Value
	target.Store("")
	config := independentConfig(t, clock, func(h *cleanupHooks) {
		h.removeAll = func(path string) error {
			if path == target.Load().(string) {
				enteredOnce.Do(func() { close(entered) })
				<-release
			}
			return os.RemoveAll(path)
		}
	})
	m := independentOpen(t, config)
	t.Cleanup(func() { releaseOnce.Do(func() { close(release) }) })
	req := independentRequest("replace-at-delete")
	old := independentAcquire(t, m, req)
	target.Store(old.StagingDir)
	independentWrite(t, filepath.Join(old.StagingDir, "old"), "retired")
	independentFail(t, m, old)
	independentReceive(t, entered, "old deletion did not reach fixed barrier")
	newLease := independentPublish(t, m, req, "replacement")
	if old.Generation == newLease.Generation {
		t.Fatal("A2: generation was reused")
	}
	catalog := filepath.Join(config.Root, "catalog", old.ResourceID+".json")
	expected := independentRead(t, catalog)
	if _, err := m.Fail(failRequest{ResourceID: old.ResourceID, Generation: old.Generation, Kind: "late"}); independentKind(err) != "stale_generation" {
		t.Fatalf("A2: old Fail token accepted: %v", err)
	}
	if _, err := m.Commit(commitRequest{ResourceID: old.ResourceID, Generation: old.Generation, Entrypoint: "old"}); independentKind(err) != "stale_generation" {
		t.Fatalf("A2: old Commit token accepted: %v", err)
	}
	clock.drainArms()
	releaseOnce.Do(func() { close(release) })
	select {
	case <-clock.armed:
	case <-time.After(3 * time.Second):
		t.Fatal("old cleanup never completed scheduler cycle")
	}
	if !independentGone(old.StagingDir) {
		t.Fatal("A4: old generation payload remains")
	}
	if got := independentRead(t, catalog); !bytes.Equal(got, expected) {
		t.Fatalf("A2: old cleanup changed replacement catalog: %s", got)
	}
	if got := string(independentRead(t, filepath.Join(config.Root, "ready", old.ResourceID, "data"))); got != "replacement" {
		t.Fatalf("A4: replacement bytes changed: %q", got)
	}
	if err := m.Close(); err != nil {
		t.Fatal(err)
	}
	restarted := independentOpen(t, config)
	if got, err := restarted.Resolve(old.ResourceID, ""); err != nil || got.State != stateReady {
		t.Fatalf("A2: replacement not recoverable: %#v %v", got, err)
	}
}

func TestIndependentPendingChildDoesNotRetainParent(t *testing.T) {
	for _, collectParent := range []bool{false, true} {
		t.Run(fmt.Sprintf("parent_gc=%t", collectParent), func(t *testing.T) {
			clock := newIndependentClock()
			var parentPath, childPath atomic.Value
			parentPath.Store("")
			childPath.Store("")
			config := independentConfig(t, clock, func(h *cleanupHooks) {
				h.removeAll = func(path string) error {
					if path == childPath.Load().(string) || (!collectParent && path == parentPath.Load().(string)) {
						return os.ErrPermission
					}
					return os.RemoveAll(path)
				}
			})
			m := independentOpen(t, config)
			parentRequest := independentRequest("parent")
			parentRequest.TTL = time.Second
			parent := independentPublish(t, m, parentRequest, "parent")
			parentPath.Store(parent.StagingDir)
			childRequest := independentRequest("child")
			childRequest.ParentResourceID = parent.ResourceID
			child := independentAcquire(t, m, childRequest)
			childPath.Store(child.StagingDir)
			independentWrite(t, filepath.Join(child.StagingDir, "data"), "child")
			clock.advance(time.Second)
			if _, err := m.Resolve(parent.ResourceID, ""); err != nil {
				t.Fatal(err)
			}
			if collectParent {
				independentAwait(t, "A1/A6: pending child prevented terminal parent catalog GC", func() bool { return independentGone(filepath.Join(config.Root, "catalog", parent.ResourceID+".json")) })
			} else {
				if got, err := m.Resolve(parent.ResourceID, ""); err != nil || got.State != stateExpired {
					t.Fatalf("parent must remain terminal while removal fails: %#v %v", got, err)
				}
			}
			_, err := m.Commit(commitRequest{ResourceID: child.ResourceID, Generation: child.Generation, Entrypoint: "data"})
			if independentKind(err) != "parent_unavailable" {
				t.Fatalf("A6: child Commit error after parent_gc=%t: %v", collectParent, err)
			}
			if got, err := m.Resolve(child.ResourceID, ""); err != nil || got.State != stateFailed || got.FailureKind != "parent_unavailable" {
				t.Fatalf("A6: child terminal outcome changed: %#v %v", got, err)
			}
		})
	}
}

func TestIndependentDeletionProceedsDuringCatalogFailure(t *testing.T) {
	for _, boundary := range []string{"write", "rename"} {
		t.Run(boundary, func(t *testing.T) {
			clock := newIndependentClock()
			var fault atomic.Bool
			config := independentConfig(t, clock, func(h *cleanupHooks) {
				write := h.writeCatalog
				h.writeCatalog = func(f *os.File, payload []byte) error {
					if fault.Load() && boundary == "write" {
						return os.ErrPermission
					}
					return write(f, payload)
				}
				h.rename = func(from, to string) error {
					if fault.Load() && boundary == "rename" && filepath.Base(filepath.Dir(to)) == "catalog" {
						return os.ErrPermission
					}
					return os.Rename(from, to)
				}
			})
			m := independentOpen(t, config)
			req := independentRequest("independent-catalog-fault")
			req.TTL = time.Second
			lease := independentPublish(t, m, req, "bytes")
			catalog := filepath.Join(config.Root, "catalog", lease.ResourceID+".json")
			oldCatalog := independentRead(t, catalog)
			fault.Store(true)
			clock.advance(time.Second)
			if _, err := m.Resolve(lease.ResourceID, ""); err != nil {
				t.Fatal(err)
			}
			independentAwait(t, "A3: catalog failure blocked hidden payload deletion", func() bool {
				return independentGone(filepath.Join(config.Root, "ready", lease.ResourceID)) && independentGone(filepath.Join(config.Root, "trash", lease.ResourceID+"-"+lease.Generation))
			})
			if got := independentRead(t, catalog); !bytes.Equal(got, oldCatalog) {
				t.Fatal("A2: unrepaired catalog was prematurely removed or changed")
			}
			if got, err := m.Resolve(lease.ResourceID, ""); err != nil || got.State != stateExpired || got.URL != "" {
				t.Fatalf("A2: terminal state lost while persistence pending: %#v %v", got, err)
			}
			fault.Store(false)
			// Drive only retry time, never another resource operation.
			independentAwait(t, "A1: repaired terminal catalog was not collected", func() bool {
				clock.advance(time.Second)
				return independentGone(catalog)
			})
		})
	}
}

func TestIndependentNoTrafficRemnantsAndActiveProtection(t *testing.T) {
	clock := newIndependentClock()
	config := independentConfig(t, clock, nil)
	m := independentOpen(t, config)
	activeRequest := independentRequest("active-staging")
	activeRequest.ProductionTimeout = 24 * time.Hour
	pending := independentAcquire(t, m, activeRequest)
	independentWrite(t, filepath.Join(pending.StagingDir, "keep"), "pending")
	readyRequest := independentRequest("active-ready")
	readyRequest.TTL = 24 * time.Hour
	ready := independentPublish(t, m, readyRequest, "ready")
	pendingCatalog := independentRead(t, filepath.Join(config.Root, "catalog", pending.ResourceID+".json"))
	readyCatalog := independentRead(t, filepath.Join(config.Root, "catalog", ready.ResourceID+".json"))
	leftovers := []string{
		filepath.Join(config.Root, "catalog", ".record-independent-leftover"),
		filepath.Join(config.Root, "catalog", "unknown-record"),
		filepath.Join(config.Root, "catalog", "unknown-directory", "nested"),
		filepath.Join(config.Root, "ready", "unknown-file"),
		filepath.Join(config.Root, "ready", "unknown-directory", "nested"),
		filepath.Join(config.Root, "trash", "unknown-file"),
		filepath.Join(config.Root, "trash", "unknown-directory", "nested"),
		filepath.Join(config.Root, "staging", "unknown-file"),
		filepath.Join(config.Root, "staging", pending.ResourceID, strings.Repeat("e", 32), "late-old-generation"),
	}
	for _, path := range leftovers {
		independentWrite(t, path, "leftover")
	}
	// No Acquire/Resolve/Fail/Commit or manager wake calls from here until the
	// worker has removed every remnant. Polling observes disk and drives time.
	independentAwait(t, "A3: no-traffic worker retained unknown/temp/old-generation remnants", func() bool {
		clock.advance(time.Second)
		for _, path := range leftovers {
			if !independentGone(path) {
				return false
			}
		}
		return true
	})
	for _, item := range []struct{ path, value string }{
		{filepath.Join(pending.StagingDir, "keep"), "pending"},
		{filepath.Join(config.Root, "ready", ready.ResourceID, "data"), "ready"},
	} {
		if got := string(independentRead(t, item.path)); got != item.value {
			t.Fatalf("A3: active bytes changed: %s = %q", item.path, got)
		}
	}
	if !bytes.Equal(independentRead(t, filepath.Join(config.Root, "catalog", pending.ResourceID+".json")), pendingCatalog) || !bytes.Equal(independentRead(t, filepath.Join(config.Root, "catalog", ready.ResourceID+".json")), readyCatalog) {
		t.Fatal("A3: scanner changed active catalog")
	}
}

func TestIndependentFailingOldestDoesNotStarveHealthyCleanup(t *testing.T) {
	clock := newIndependentClock()
	var failingPath atomic.Value
	failingPath.Store("")
	var attempts atomic.Int64
	config := independentConfig(t, clock, func(h *cleanupHooks) {
		h.removeAll = func(path string) error {
			if path == failingPath.Load().(string) {
				attempts.Add(1)
				return os.ErrPermission
			}
			return os.RemoveAll(path)
		}
	})
	m := independentOpen(t, config)
	oldest := independentAcquire(t, m, independentRequest("oldest-permanent-failure"))
	independentWrite(t, filepath.Join(oldest.StagingDir, "keep"), "old")
	failingPath.Store(oldest.StagingDir)
	independentFail(t, m, oldest)
	independentAwait(t, "fixture oldest deletion was not attempted", func() bool { return attempts.Load() > 0 })
	var healthy []resourceDescriptor
	for i := 0; i < 65; i++ {
		lease := independentAcquire(t, m, independentRequest(fmt.Sprintf("fairness-%d", i)))
		independentWrite(t, filepath.Join(lease.StagingDir, "data"), "healthy")
		independentFail(t, m, lease)
		healthy = append(healthy, lease)
	}
	independentAwait(t, "A5: oldest failing generation starved healthy deletion beyond two batches", func() bool {
		for _, lease := range healthy {
			if !independentGone(lease.StagingDir) {
				return false
			}
		}
		return true
	})
	if attempts.Load() != 1 {
		t.Fatalf("A5: same-time failure busy loop: attempts=%d", attempts.Load())
	}
	if independentGone(filepath.Join(config.Root, "catalog", oldest.ResourceID+".json")) {
		t.Fatal("A1: catalog GC abandoned failing payload responsibility")
	}
}

func TestIndependentHideReservationSurvivesRepeatedDiscovery(t *testing.T) {
	clock := newIndependentClock()
	var blockHide atomic.Bool
	config := independentConfig(t, clock, func(h *cleanupHooks) {
		h.rename = func(from, to string) error {
			if blockHide.Load() && filepath.Base(filepath.Dir(from)) == "ready" {
				return os.ErrPermission
			}
			return os.Rename(from, to)
		}
	})
	m := independentOpen(t, config)
	req := independentRequest("hide-owned")
	req.TTL = time.Second
	lease := independentPublish(t, m, req, "reserved")
	blockHide.Store(true)
	clock.advance(time.Second)
	if _, err := m.Resolve(lease.ResourceID, ""); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 40; i++ {
		independentWrite(t, filepath.Join(config.Root, "trash", fmt.Sprintf("scan-pressure-%d", i)), "remnant")
	}
	for i := 0; i < 10; i++ {
		clock.advance(time.Second)
		if _, err := m.Acquire(context.Background(), req); independentKind(err) != "conflict" {
			t.Fatalf("A4: hide reservation lost: %v", err)
		}
	}
	if got := string(independentRead(t, filepath.Join(config.Root, "ready", lease.ResourceID, "data"))); got != "reserved" {
		t.Fatal("A4: discovery stole hide-owned ready bytes")
	}
}

func TestIndependentRecoveryClaimedRemnantAndCatalogUnlink(t *testing.T) {
	for _, boundary := range []string{"claimed", "catalog_unlinked"} {
		t.Run(boundary, func(t *testing.T) {
			clock := newIndependentClock()
			var failRemoval atomic.Bool
			config := independentConfig(t, clock, func(h *cleanupHooks) {
				h.removeAll = func(path string) error {
					if failRemoval.Load() {
						return os.ErrPermission
					}
					return os.RemoveAll(path)
				}
			})
			m := independentOpen(t, config)
			valid := independentPublish(t, m, independentRequest("survives-recovery"), "valid")
			pending := independentAcquire(t, m, independentRequest("abandoned-on-recovery"))
			old := independentAcquire(t, m, independentRequest("disk-boundary"))
			independentWrite(t, filepath.Join(old.StagingDir, "residual"), "old")
			failRemoval.Store(true)
			independentFail(t, m, old)
			if err := m.Close(); err != nil {
				t.Fatal(err)
			}
			// Stop all producer/request activity before reconstructing the root.
			// These are disk-state recovery fixtures, not process-kill evidence.
			claimed := filepath.Join(config.Root, "trash", "independent-unique-claimed-remnant")
			if boundary == "claimed" {
				if err := os.Rename(old.StagingDir, claimed); err != nil {
					t.Fatal(err)
				}
			} else {
				if err := os.RemoveAll(old.StagingDir); err != nil {
					t.Fatal(err)
				}
				if err := os.Remove(filepath.Join(config.Root, "catalog", old.ResourceID+".json")); err != nil {
					t.Fatal(err)
				}
			}
			temporary := filepath.Join(config.Root, "catalog", ".record-interrupted-persist")
			independentWrite(t, temporary, "partial")
			failRemoval.Store(false)
			restarted := independentOpen(t, config)
			if got, err := restarted.Resolve(valid.ResourceID, ""); err != nil || got.State != stateReady {
				t.Fatalf("A2: valid ready lost after %s: %#v %v", boundary, got, err)
			}
			if got, err := restarted.Resolve(pending.ResourceID, ""); err != nil || (got.State != stateNotFound && (got.State != stateFailed || got.FailureKind != "abandoned")) {
				t.Fatalf("A6: recovered pending lease remained active: %#v %v", got, err)
			}
			if _, err := restarted.Commit(commitRequest{ResourceID: pending.ResourceID, Generation: pending.Generation, Entrypoint: "data"}); independentKind(err) != "stale_generation" {
				t.Fatalf("A6: abandoned token accepted: %v", err)
			}
			independentAwait(t, "A2/A3: recovery retained claimed/temp/terminal residue", func() bool {
				clock.advance(time.Second)
				return independentGone(claimed) && independentGone(temporary) && independentGone(filepath.Join(config.Root, "catalog", old.ResourceID+".json"))
			})
			if got, err := restarted.Resolve(old.ResourceID, ""); err != nil || got.State != stateNotFound {
				t.Fatalf("A2: removed catalog recovered a resource: %#v %v", got, err)
			}
		})
	}
}

func TestIndependentManagedDirectoryAnomalyFailsStartup(t *testing.T) {
	for _, managed := range []string{"catalog", "staging", "ready", "trash"} {
		t.Run(managed, func(t *testing.T) {
			clock := newIndependentClock()
			config := independentConfig(t, clock, nil)
			m := independentOpen(t, config)
			if err := m.Close(); err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(config.Root, managed)
			if err := os.Rename(path, path+"-saved"); err != nil {
				t.Fatal(err)
			}
			independentWrite(t, path, "abnormal directory component")
			if restarted, err := newOriginManager(config); err == nil {
				_ = restarted.Close()
				t.Fatal("A3: startup accepted non-directory managed component")
			}
		})
	}
}

func TestIndependentSymlinkLeafAndManagedRootSafety(t *testing.T) {
	for _, kind := range []string{"leaf", "managed_directory"} {
		t.Run(kind, func(t *testing.T) {
			clock := newIndependentClock()
			config := independentConfig(t, clock, nil)
			m := independentOpen(t, config)
			external := t.TempDir()
			sentinel := filepath.Join(external, "sentinel")
			independentWrite(t, sentinel, "outside owned root")
			link := filepath.Join(config.Root, "ready", "external-link")
			if kind == "managed_directory" {
				if err := m.Close(); err != nil {
					t.Fatal(err)
				}
				link = filepath.Join(config.Root, "trash")
				if err := os.Rename(link, link+"-saved"); err != nil {
					t.Fatal(err)
				}
			}
			if err := os.Symlink(external, link); err != nil {
				t.Skipf("environment cannot create directory symlink: %v", err)
			}
			// Remove only the link before external TempDir cleanup.
			t.Cleanup(func() { _ = os.Remove(link) })
			if kind == "managed_directory" {
				if restarted, err := newOriginManager(config); err == nil {
					_ = restarted.Close()
					t.Error("A3: startup accepted symlinked managed directory")
				}
			} else {
				independentAwait(t, "A3: no-traffic leaf symlink was not collected", func() bool { clock.advance(time.Second); return independentGone(link) })
			}
			if got := string(independentRead(t, sentinel)); got != "outside owned root" {
				t.Fatal("A3: cleanup changed symlink target outside root")
			}
		})
	}
}

func TestIndependentCatalogTempPersistBarrier(t *testing.T) {
	clock := newIndependentClock()
	entered, release := make(chan struct{}), make(chan struct{})
	var block atomic.Bool
	var enteredOnce, releaseOnce sync.Once
	var temporary atomic.Value
	temporary.Store("")
	config := independentConfig(t, clock, func(h *cleanupHooks) {
		write := h.writeCatalog
		h.writeCatalog = func(f *os.File, data []byte) error {
			if block.Load() {
				temporary.Store(f.Name())
				enteredOnce.Do(func() { close(entered) })
				<-release
			}
			return write(f, data)
		}
	})
	m := independentOpen(t, config)
	t.Cleanup(func() { releaseOnce.Do(func() { close(release) }) })
	block.Store(true)
	type outcome struct {
		result acquireResult
		err    error
	}
	completed := make(chan outcome, 1)
	go func() {
		got, err := m.Acquire(context.Background(), independentRequest("persist-barrier"))
		completed <- outcome{got, err}
	}()
	independentReceive(t, entered, "fixture persist did not reach temp-file barrier")
	clock.advance(time.Minute)
	if independentGone(temporary.Load().(string)) {
		t.Fatal("A4: live catalog temporary removed while writer paused")
	}
	releaseOnce.Do(func() { close(release) })
	var done outcome
	select {
	case done = <-completed:
	case <-time.After(3 * time.Second):
		t.Fatal("A4: persist could not resume")
	}
	if done.err != nil || !done.result.Produce {
		t.Fatalf("A4: scanner interrupted live catalog persist: %#v %v", done.result, done.err)
	}
	data := independentRead(t, filepath.Join(config.Root, "catalog", done.result.Descriptor.ResourceID+".json"))
	var record struct {
		Generation string `json:"generation"`
		State      string `json:"state"`
	}
	if err := json.Unmarshal(data, &record); err != nil || record.Generation != done.result.Descriptor.Generation || record.State != "pending" {
		t.Fatalf("A4: persisted catalog damaged: %s %v", data, err)
	}
}
