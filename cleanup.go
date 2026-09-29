package main

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"time"
)

const (
	cleanupBaseDelay = time.Second
	cleanupMaxDelay  = time.Minute
	cleanupBatchSize = 32
)

// These hooks cover only retirement, catalog publication and scheduler time.
// Set once at construction; tests must synchronize any state captured by them.
type cleanupHooks struct {
	remove       func(string) error
	rename       func(string, string) error
	removeAll    func(string) error
	writeCatalog func(*os.File, []byte) error
	now          func() time.Time
	newTimer     func(time.Duration) (<-chan time.Time, func())
}

func defaultCleanupHooks() cleanupHooks {
	return cleanupHooks{
		rename: os.Rename, remove: os.Remove, removeAll: os.RemoveAll, now: time.Now,
		writeCatalog: func(f *os.File, data []byte) error {
			_, err := f.Write(data)
			return err
		},
		newTimer: func(delay time.Duration) (<-chan time.Time, func()) {
			timer := time.NewTimer(delay)
			return timer.C, func() { timer.Stop() }
		},
	}
}

// No paths, producer input or raw filesystem errors cross the logging boundary.
type cleanupEvent struct {
	OldestWait time.Duration
	ResourceID string
	Generation string
	Operation  string
	ErrorClass string
	Attempts   uint64
	Pending    int
	Recovered  bool
}

type cleanupWork struct {
	created time.Time
	record  *originRecord
	trash   string
	staging string
	hide    bool
	persist bool
	// Physical cleanup and catalog persistence have independent deadlines:
	// a full disk must not make catalog writes a prerequisite for deletion.
	next        time.Time
	catalogNext time.Time
	removed     bool
	failures    uint64
	inflight    bool
}

func (m *originManager) queueCleanupLocked(record *originRecord, hide, persist bool) *cleanupWork {
	key := record.ResourceID + "-" + record.Generation
	if work := m.garbage[key]; work != nil {
		return work
	}
	now := m.now().UTC()
	work := &cleanupWork{
		record: record, hide: hide, persist: persist, next: now, catalogNext: now, created: now,
		trash:   filepath.Join(m.trashDir, key),
		staging: filepath.Join(m.stagingDir, record.ResourceID, record.Generation),
	}
	m.garbage[key] = work
	m.signal()
	return work
}

func (m *originManager) readyReservedLocked(id string) bool {
	for _, work := range m.garbage {
		if work.record.ResourceID == id && work.hide {
			return true
		}
	}
	return false
}

// Only this step may access ready/<id> or the current catalog. It is serialized
// with publication. Once hidden, deletion only touches generation-owned paths.
func (m *originManager) prepareCleanupLocked(work *cleanupWork) error {
	if err := m.checkManagedPaths(); err != nil {
		m.cleanupFailedLocked(work, "paths", err)
		return err
	}
	if work.hide {
		err := m.hooks.rename(filepath.Join(m.readyDir, work.record.ResourceID), work.trash)
		if err != nil && !errors.Is(err, fs.ErrNotExist) {
			m.cleanupFailedLocked(work, "hide", err)
			return err
		}
		work.hide = false
	}
	if work.persist && !m.now().Before(work.catalogNext) {
		// A successfully persisted acquire replaces this pointer. Do not write
		// an old terminal snapshot over that newer generation.
		if m.records[work.record.ResourceID] == work.record {
			if err := m.persistLocked(work.record); err != nil {
				m.cleanupFailedLocked(work, "catalog", err)
				return err
			}
		}
		work.persist = false
	}
	return nil
}

func (m *originManager) cleanupFailedLocked(work *cleanupWork, operation string, err error) {
	work.failures++
	delay := cleanupBaseDelay
	for n := uint64(1); n < work.failures && delay < cleanupMaxDelay; n++ {
		delay *= 2
	}
	if delay > cleanupMaxDelay {
		delay = cleanupMaxDelay
	}
	if operation == "paths" {
		work.catalogNext = m.now().UTC().Add(delay)
	}
	if operation == "catalog" {
		work.catalogNext = m.now().UTC().Add(delay)
	} else {
		work.next = m.now().UTC().Add(delay)
	}
	m.cleanupEventLocked(work, operation, err, false)
}

func (work *cleanupWork) deadline() time.Time {
	if work.hide || !work.persist {
		return work.next
	}
	// A completed deletion must not leave its past deadline waking the worker
	// in a tight loop while metadata is still waiting for its own retry.
	if work.removed || work.catalogNext.Before(work.next) {
		return work.catalogNext
	}
	return work.next
}

func (m *originManager) cleanupEventLocked(work *cleanupWork, operation string, err error, recovered bool) {
	now := m.now().UTC()
	// A manager-wide bound prevents a large failing inventory from flooding
	// the host. Pending is queue inventory, NOT a physical byte measurement.
	if m.diagnostic == nil || now.Before(m.nextDiagnostic) || len(m.cleanupEvents) >= cleanupBatchSize {
		return
	}
	m.nextDiagnostic = now.Add(cleanupBaseDelay)
	class := "filesystem"
	switch {
	case err == nil:
		class = ""
	case errors.Is(err, fs.ErrPermission):
		class = "permission"
	case errors.Is(err, fs.ErrNotExist):
		class = "not_found"
	}
	var oldest time.Duration
	pendingCount := len(m.garbage)
	for _, pending := range m.scanErrors {
		if pending != nil {
			pendingCount++
			if age := now.Sub(pending.created); age > oldest {
				oldest = age
			}
		}
	}
	for _, pending := range m.garbage {
		if age := now.Sub(pending.created); age > oldest {
			oldest = age
		}
	}
	m.cleanupEvents = append(m.cleanupEvents, cleanupEvent{
		OldestWait: oldest,
		ResourceID: work.record.ResourceID, Generation: work.record.Generation,
		Operation: operation, ErrorClass: class, Attempts: work.failures,
		Pending: pendingCount, Recovered: recovered,
	})
}

func (m *originManager) emitCleanupEvents() {
	m.mu.Lock()
	events := m.cleanupEvents
	m.cleanupEvents = nil
	m.mu.Unlock()
	for _, event := range events {
		m.diagnostic(event)
	}
}

// One worker, at most 32 oldest-deadline entries per batch. Failed work moves
// into the future, so a permanently failing entry cannot monopolize a batch.
func (m *originManager) cleanupBatch() {
	m.mu.Lock()
	now := m.now().UTC()
	batch := make([]*cleanupWork, 0, cleanupBatchSize)
	for _, work := range m.garbage {
		if !work.inflight && !now.Before(work.deadline()) {
			// Keep only the oldest 32, even for a very large inventory.
			i := sort.Search(len(batch), func(i int) bool {
				if batch[i].deadline().Equal(work.deadline()) {
					return batch[i].trash >= work.trash
				}
				return batch[i].deadline().After(work.deadline())
			})
			if i < cleanupBatchSize {
				batch = slices.Insert(batch, i, work)
				if len(batch) > cleanupBatchSize {
					batch = batch[:cleanupBatchSize]
				}
			}
		}
	}
	m.mu.Unlock()
	for _, work := range batch {
		select {
		case <-m.stop:
			return
		default:
		}
		m.mu.Lock()
		// Catalog errors are already scheduled and diagnosed. Once hidden,
		// bytes can be removed even if persistence failed: on restart an old
		// elapsed ready record without ready bytes reconciles as expired.
		prepareErr := m.prepareCleanupLocked(work)
		if prepareErr != nil && (work.hide || m.checkManagedPaths() != nil) {
			m.mu.Unlock()
			continue
		}
		if work.hide {
			m.mu.Unlock()
			continue
		}
		work.inflight = true
		remove := !work.removed && !m.now().Before(work.next)
		m.mu.Unlock()
		var err error
		operation := "trash"
		if remove {
			err = m.hooks.removeAll(work.trash)
			if err == nil && work.staging != "" {
				operation = "staging"
				err = m.removeStaging(work.staging)
			}
		}
		m.mu.Lock()
		work.inflight = false
		if err != nil {
			m.cleanupFailedLocked(work, operation, err)
		} else if remove {
			work.removed = true
			// Remove only an empty ID container while serialized with Acquire.
			// Never recursively delete staging/<id>, which can hold a new lease.
			if work.staging != "" {
				_ = os.Remove(filepath.Dir(work.staging))
			}
		}
		if work.removed && !work.persist {
			if err := m.collectTerminalLocked(work); err != nil {
				m.cleanupFailedLocked(work, "gc", err)
				m.mu.Unlock()
				continue
			}
			delete(m.garbage, work.record.ResourceID+"-"+work.record.Generation)
			if work.failures != 0 {
				m.cleanupEventLocked(work, "cleanup", nil, true)
			}
		}
		m.mu.Unlock()
	}
}
