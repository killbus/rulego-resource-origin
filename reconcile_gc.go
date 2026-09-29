package main

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// A cursor retains at most the top directory and one staging ID directory.
// Readdir is bounded; no snapshot of the complete inventory is retained.
type residueCursor struct {
	root   *os.File
	child  *os.File
	id     string
	failed bool
}

func directoryPath(path string) error {
	return validateDirectoryPath(path, false)
}

// Validate parents first, including before MkdirAll can traverse them. Missing
// components are allowed only during creation; existing links never are.
func validateDirectoryPath(path string, allowMissing bool) error {
	parent := filepath.Dir(path)
	if parent != path {
		if err := validateDirectoryPath(parent, allowMissing); err != nil {
			return err
		}
	}
	info, err := os.Lstat(path)
	if allowMissing && errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("unsafe managed directory")
	}
	return nil
}

func (m *originManager) checkManagedPaths() error {
	for _, p := range []string{m.catalogDir, m.stagingDir, m.readyDir, m.trashDir} {
		if err := directoryPath(p); err != nil {
			return err
		}
	}
	return nil
}

func (m *originManager) removeStaging(path string) error {
	if err := directoryPath(filepath.Dir(path)); err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil
		}
		return err
	}
	return m.hooks.removeAll(path)
}

// Completion is attested by this work, never inferred from an absent map entry.
// Pointer validation and unlink share the publication critical section.
func (m *originManager) collectTerminalLocked(work *cleanupWork) error {
	r := work.record
	if m.records[r.ResourceID] != r || (r.State != stateFailed && r.State != stateExpired) {
		return nil
	}
	if !work.removed || work.persist || work.hide || work.inflight || m.waiters[r.ResourceID] != nil {
		return errors.New("terminal cleanup incomplete")
	}
	if err := m.hooks.remove(filepath.Join(m.catalogDir, r.ResourceID+".json")); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	delete(m.records, r.ResourceID)
	return nil
}

func (m *originManager) closeResidueCursors() {
	for i := range m.residue {
		c := &m.residue[i]
		if c.child != nil {
			_ = c.child.Close()
		}
		if c.root != nil {
			_ = c.root.Close()
		}
		*c = residueCursor{}
	}
}

// Each turn rotates the root, so a large staging inventory cannot starve catalog.
// Observation and claim are under mu, including temporary catalog observations.
func (m *originManager) scanResidueBatch() {
	m.mu.Lock()
	defer m.mu.Unlock()
	now := m.now().UTC()
	if now.Before(m.scanNext) {
		return
	}
	m.scanNext = now.Add(cleanupBaseDelay)
	index := m.scanRoot
	m.scanRoot = (m.scanRoot + 1) % len(m.residue)
	diagnostic := m.scanErrors[index]
	if diagnostic == nil {
		diagnostic = &cleanupWork{record: &originRecord{}, created: now}
	}
	if now.Before(diagnostic.next) {
		return
	}
	before := diagnostic.failures
	c := &m.residue[index]
	completed := false
	defer func() {
		if diagnostic.failures != before {
			c.failed = true
			m.scanErrors[index] = diagnostic
		} else if completed && !c.failed {
			m.scanErrors[index] = nil
		}
	}()
	if err := m.checkManagedPaths(); err != nil {
		m.closeResidueCursors()
		m.cleanupFailedLocked(diagnostic, "scan_paths", err)
		return
	}
	roots := []string{m.catalogDir, m.readyDir, m.stagingDir, m.trashDir}
	if c.root == nil {
		c.failed = false
		f, err := os.Open(roots[index])
		if err != nil {
			m.cleanupFailedLocked(diagnostic, "scan", err)
			return
		}
		c.root = f
	}
	for n := 0; n < cleanupBatchSize; n++ {
		f := c.root
		if c.child != nil {
			f = c.child
		}
		entries, err := f.Readdirnames(1)
		if len(entries) == 0 {
			if err != nil && err != io.EOF {
				m.cleanupFailedLocked(diagnostic, "scan", err)
			}
			if c.child != nil {
				_ = c.child.Close()
				c.child = nil
				_ = os.Remove(filepath.Join(m.stagingDir, c.id))
				c.id = ""
				continue
			}
			_ = c.root.Close()
			c.root = nil
			completed = true
			break
		}
		name := entries[0]
		path := filepath.Join(roots[index], name)
		if c.child != nil {
			path = filepath.Join(m.stagingDir, c.id, name)
			if err := directoryPath(filepath.Dir(path)); err != nil {
				_ = c.child.Close()
				c.child = nil
				continue
			}
			if r := m.records[c.id]; r != nil && r.State == statePending && r.Generation == name {
				continue
			}
		} else if index == 2 {
			info, err := os.Lstat(path)
			if err != nil {
				continue
			}
			if info.IsDir() && info.Mode()&os.ModeSymlink == 0 {
				child, err := os.Open(path)
				if err != nil {
					m.cleanupFailedLocked(diagnostic, "scan", err)
					continue
				}
				c.child = child
				c.id = name
				continue
			}
			if r := m.records[name]; r != nil && r.State == statePending {
				continue
			}
		} else if index == 1 {
			r := m.records[name]
			if (r != nil && (r.State == stateReady || r.State == statePending)) || m.readyReservedLocked(name) {
				continue
			}
		} else if index == 0 {
			if strings.HasSuffix(name, ".json") && m.records[strings.TrimSuffix(name, ".json")] != nil {
				continue
			}
		}
		owned := false
		for _, w := range m.garbage {
			if w.trash == path || w.staging == path {
				owned = true
				break
			}
		}
		if owned {
			continue
		}
		if err := m.claimResidueLocked(path); err != nil {
			m.cleanupFailedLocked(diagnostic, "claim", err)
		}
	}
}

func (m *originManager) claimResidueLocked(path string) error {
	// MkdirTemp reserves a globally unique destination; rename goes inside it.
	quarantine, err := os.MkdirTemp(m.trashDir, ".gc-")
	if err != nil {
		return err
	}
	if err = m.hooks.rename(path, filepath.Join(quarantine, "payload")); err != nil {
		_ = os.Remove(quarantine)
		if errors.Is(err, fs.ErrNotExist) {
			return nil
		}
		return err
	}
	key := filepath.Base(quarantine)
	now := m.now().UTC()
	m.garbage[key+"-"] = &cleanupWork{record: &originRecord{ResourceID: key}, trash: quarantine, next: now, created: now}
	return nil
}
