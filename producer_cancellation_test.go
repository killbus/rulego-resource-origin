package main

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestProducerCancellationWakesWaiterAndAllowsRetry(t *testing.T) {
	m := testManager(t, 4096, 2048)
	req := request("canceled-producer")
	req.ProductionTimeout = time.Minute
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	first, err := m.Acquire(ctx, req)
	if err != nil || !first.Produce {
		t.Fatalf("acquire = %#v, %v", first, err)
	}
	waiter, _ := startCommitWaiter(t, m, req)
	cancel()
	assertFailedWaiter(t, waiter, "producer_canceled")
	second, err := m.Acquire(context.Background(), req)
	if err != nil || !second.Produce || second.Descriptor.Generation == first.Descriptor.Generation {
		t.Fatalf("retry = %#v, %v", second, err)
	}
	_, err = m.Commit(commitRequest{first.Descriptor.ResourceID, first.Descriptor.Generation, "artifact.bin"})
	if errorKind(err) != "stale_generation" {
		t.Fatalf("canceled commit = %v", err)
	}
}

func TestWaiterCancellationDoesNotRetireProducer(t *testing.T) {
	m := testManager(t, 4096, 2048)
	req := request("canceled-waiter")
	req.ProductionTimeout = time.Minute
	producer, stopProducer := context.WithCancel(context.Background())
	defer stopProducer()
	first, err := m.Acquire(producer, req)
	if err != nil {
		t.Fatal(err)
	}
	waiter, cancel := context.WithCancel(context.Background())
	defer cancel()
	observed := &observedContext{Context: waiter, entered: make(chan struct{})}
	finished := make(chan error, 1)
	go func() { _, err := m.Acquire(observed, req); finished <- err }()
	select {
	case <-observed.entered:
	case <-time.After(3 * time.Second):
		t.Fatal("waiter did not enter")
	}
	cancel()
	select {
	case err := <-finished:
		if errorKind(err) != "wait_timeout" || !errors.Is(err, context.Canceled) {
			t.Fatalf("waiter error = %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("waiter did not cancel")
	}
	writeStagedFile(t, first.Descriptor, "artifact.bin", "ready")
	ready, err := m.Commit(commitRequest{first.Descriptor.ResourceID, first.Descriptor.Generation, "artifact.bin"})
	if err != nil || ready.State != stateReady {
		t.Fatalf("producer commit = %#v, %v", ready, err)
	}
}

func TestCanceledAcquireDoesNotReserveGeneration(t *testing.T) {
	m := testManager(t, 4096, 2048)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	req := request("already-canceled")
	result, err := m.Acquire(ctx, req)
	if errorKind(err) != "producer_canceled" || !errors.Is(err, context.Canceled) || result.Produce {
		t.Fatalf("acquire = %#v, %v", result, err)
	}
	resolved, err := m.Resolve(resourceID(req.Key, req.Fingerprint), "")
	if err != nil || resolved.State != stateNotFound {
		t.Fatalf("resolve = %#v, %v", resolved, err)
	}
}

func TestCancellationDuringAcquirePersistenceIsObserved(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	hooks := defaultCleanupHooks()
	hooks.writeCatalog = func(f *os.File, data []byte) error {
		cancel()
		_, err := f.Write(data)
		return err
	}
	m := cleanupManager(t, cleanupConfig(t, &hooks), true)
	req := request("canceled-during-acquire")
	req.ProductionTimeout = time.Minute
	first, err := m.Acquire(ctx, req)
	if err != nil || !first.Produce {
		t.Fatalf("acquire = %#v, %v", first, err)
	}
	// The context ended before the watch was registered. AfterFunc must still
	// terminalize the lease without requiring a producer node to execute.
	awaitCleanup(t, func() bool {
		resolved, err := m.Resolve(first.Descriptor.ResourceID, "")
		return err == nil && resolved.State == stateFailed && resolved.FailureKind == "producer_canceled"
	})
}

func TestProducerCancellationCommitRace(t *testing.T) {
	m := testManager(t, 4096, 2048)
	for range 30 {
		req := request("commit-race")
		req.ProductionTimeout = time.Minute
		ctx, cancel := context.WithCancel(context.Background())
		first, err := m.Acquire(ctx, req)
		if err != nil || !first.Produce {
			cancel()
			t.Fatalf("acquire = %#v, %v", first, err)
		}
		writeStagedFile(t, first.Descriptor, "artifact.bin", "ready")
		waiter, _ := startCommitWaiter(t, m, req)
		m.mu.Lock()
		record := m.records[first.Descriptor.ResourceID]
		m.mu.Unlock()
		start := make(chan struct{})
		committed := make(chan error, 1)
		go func() {
			<-start
			_, err := m.Commit(commitRequest{first.Descriptor.ResourceID, first.Descriptor.Generation, "artifact.bin"})
			committed <- err
		}()
		close(start)
		cancel()
		err = <-committed
		// Drain a callback even if AfterFunc already started before disarming.
		m.cancelProduction(record)
		select {
		case got := <-waiter:
			if err == nil {
				if got.Descriptor.State != stateReady {
					t.Fatalf("successful commit lost to late cancel: %#v", got)
				}
				resolved, resolveErr := m.Resolve(record.ResourceID, "")
				if resolveErr != nil || resolved.State != stateReady {
					t.Fatalf("ready lost: %#v, %v", resolved, resolveErr)
				}
				m.mu.Lock()
				err = m.expireLocked(record, m.now())
				m.mu.Unlock()
				if err != nil {
					t.Fatal(err)
				}
			} else if errorKind(err) != "stale_generation" || got.Descriptor.FailureKind != "producer_canceled" {
				t.Fatalf("commit=%v waiter=%#v", err, got)
			}
		case <-time.After(3 * time.Second):
			t.Fatal("commit/cancel did not wake waiter")
		}
	}
}

func TestLateProducerCancellationCannotRetireReplacement(t *testing.T) {
	m := testManager(t, 4096, 2048)
	req := request("replacement")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	first, err := m.Acquire(ctx, req)
	if err != nil {
		t.Fatal(err)
	}
	m.mu.Lock()
	old := m.records[first.Descriptor.ResourceID]
	m.mu.Unlock()
	if _, err := m.Fail(failRequest{first.Descriptor.ResourceID, first.Descriptor.Generation, "producer_failed"}); err != nil {
		t.Fatal(err)
	}
	second, err := m.Acquire(context.Background(), req)
	if err != nil || !second.Produce {
		t.Fatalf("retry = %#v, %v", second, err)
	}
	cancel()
	m.cancelProduction(old)
	writeStagedFile(t, second.Descriptor, "artifact.bin", "replacement")
	ready, err := m.Commit(commitRequest{second.Descriptor.ResourceID, second.Descriptor.Generation, "artifact.bin"})
	if err != nil || ready.State != stateReady {
		t.Fatalf("replacement commit = %#v, %v", ready, err)
	}
}

func TestProducerCancellationNotifiesDespiteCatalogFailure(t *testing.T) {
	hooks := defaultCleanupHooks()
	var reject atomic.Bool
	hooks.writeCatalog = func(f *os.File, data []byte) error {
		if reject.Load() {
			return fs.ErrPermission
		}
		_, err := f.Write(data)
		return err
	}
	m := cleanupManager(t, cleanupConfig(t, &hooks), true)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	req := request("catalog-failure")
	first, err := m.Acquire(ctx, req)
	if err != nil {
		t.Fatal(err)
	}
	waiter, _ := startCommitWaiter(t, m, req)
	reject.Store(true)
	cancel()
	assertFailedWaiter(t, waiter, "producer_canceled")
	m.mu.Lock()
	work := m.garbage[first.Descriptor.ResourceID+"-"+first.Descriptor.Generation]
	if work == nil || !work.persist {
		m.mu.Unlock()
		t.Fatal("catalog retry responsibility lost")
	}
	m.mu.Unlock()
	reject.Store(false)
	second, err := m.Acquire(context.Background(), req)
	if err != nil || !second.Produce || second.Descriptor.Generation == first.Descriptor.Generation {
		t.Fatalf("retry = %#v, %v", second, err)
	}
}

// Exercise AfterFunc registration/disarming without timing assumptions. Callbacks
// can be retained deliberately to model one already scheduled at terminalization.
type productionContext struct {
	context.Context
	mu       sync.Mutex
	callback func()
	stopped  bool
	done     chan struct{}
}

func (c *productionContext) Done() <-chan struct{} { return c.done }
func (c *productionContext) AfterFunc(f func()) func() bool {
	c.mu.Lock()
	c.callback = f
	c.mu.Unlock()
	return func() bool { c.mu.Lock(); defer c.mu.Unlock(); c.stopped = true; return true }
}

func TestProductionWatchDetachedOnTerminalAndClose(t *testing.T) {
	for _, terminal := range []string{"commit", "fail", "timeout", "close"} {
		t.Run(terminal, func(t *testing.T) {
			m := testManager(t, 4096, 2048)
			ctx := &productionContext{Context: context.Background(), done: make(chan struct{})}
			first, err := m.Acquire(ctx, request(terminal))
			if err != nil {
				t.Fatal(err)
			}
			switch terminal {
			case "commit":
				writeStagedFile(t, first.Descriptor, "artifact.bin", "ready")
				_, err = m.Commit(commitRequest{first.Descriptor.ResourceID, first.Descriptor.Generation, "artifact.bin"})
			case "fail":
				_, err = m.Fail(failRequest{first.Descriptor.ResourceID, first.Descriptor.Generation, "producer_failed"})
			case "timeout":
				m.mu.Lock()
				m.records[first.Descriptor.ResourceID].PublishBy = time.Now().Add(-time.Minute)
				m.mu.Unlock()
				m.sweep()
			case "close":
				err = m.Close()
			}
			if err != nil {
				t.Fatal(err)
			}
			ctx.mu.Lock()
			stopped, callback := ctx.stopped, ctx.callback
			ctx.mu.Unlock()
			if !stopped || callback == nil {
				t.Fatal("production watch was not registered and detached")
			}
			if terminal == "close" {
				m.mu.Lock()
				record := m.records[first.Descriptor.ResourceID]
				m.mu.Unlock()
				m.cancelProduction(record)
				m.mu.Lock()
				state := record.State
				m.mu.Unlock()
				if state != statePending {
					t.Fatalf("callback mutated closed manager: %s", state)
				}
			}
		})
	}
}
