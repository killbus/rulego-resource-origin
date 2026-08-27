package main

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"sync"
	"time"
)

const catalogVersion = 1
const ownerMarker = ".resource-origin.json"

type resourceState string

const (
	statePending  resourceState = "pending"
	stateReady    resourceState = "ready"
	stateFailed   resourceState = "failed"
	stateExpired  resourceState = "expired"
	stateNotFound resourceState = "not_found"
)

type managerConfig struct {
	Root             string
	StaticURLPrefix  string
	MaxRetainedBytes int64
	MaxResourceBytes int64
	MaxTTL           time.Duration
	MaxProduction    time.Duration
}

type acquireRequest struct {
	Key               string
	Fingerprint       string
	ParentResourceID  string
	TTL               time.Duration
	MaxBytes          int64
	ProductionTimeout time.Duration
}

type commitRequest struct {
	ResourceID string
	Generation string
	Entrypoint string
}

type failRequest struct {
	ResourceID string
	Generation string
	Kind       string
}

type resourceDescriptor struct {
	State       resourceState `json:"state"`
	ResourceID  string        `json:"resourceId"`
	Generation  string        `json:"generation,omitempty"`
	StagingDir  string        `json:"stagingDir,omitempty"`
	PublishBy   *time.Time    `json:"publishBy,omitempty"`
	MaxBytes    int64         `json:"maxBytes,omitempty"`
	Entrypoint  string        `json:"entrypoint,omitempty"`
	Members     []string      `json:"members,omitempty"`
	Size        int64         `json:"size,omitempty"`
	PublishedAt *time.Time    `json:"publishedAt,omitempty"`
	ExpiresAt   *time.Time    `json:"expiresAt,omitempty"`
	URL         string        `json:"url,omitempty"`
	FailureKind string        `json:"failureKind,omitempty"`
}

type acquireResult struct {
	Descriptor resourceDescriptor
	Produce    bool
}

type originRecord struct {
	Version          int           `json:"version"`
	ResourceID       string        `json:"resourceId"`
	ParentResourceID string        `json:"parentResourceId,omitempty"`
	State            resourceState `json:"state"`
	Generation       string        `json:"generation,omitempty"`
	Entrypoint       string        `json:"entrypoint,omitempty"`
	Members          []string      `json:"members,omitempty"`
	Size             int64         `json:"size,omitempty"`
	MaxBytes         int64         `json:"maxBytes"`
	TTLMillis        int64         `json:"ttlMs"`
	ProductionMillis int64         `json:"productionTimeoutMs"`
	CreatedAt        time.Time     `json:"createdAt"`
	PublishBy        time.Time     `json:"publishBy"`
	PublishedAt      time.Time     `json:"publishedAt,omitempty"`
	ExpiresAt        time.Time     `json:"expiresAt,omitempty"`
	FailureKind      string        `json:"failureKind,omitempty"`
}

type originError struct {
	Kind string
	Err  error
}

func (e *originError) Error() string { return e.Kind + ": " + e.Err.Error() }
func (e *originError) Unwrap() error { return e.Err }

func problem(kind, message string) error {
	return &originError{Kind: kind, Err: errors.New(message)}
}

type originManager struct {
	mu               sync.Mutex
	root             string
	catalogDir       string
	stagingDir       string
	readyDir         string
	trashDir         string
	staticURLPrefix  string
	maxRetainedBytes int64
	maxResourceBytes int64
	maxTTL           time.Duration
	maxProduction    time.Duration
	records          map[string]*originRecord
	waiters          map[string]*publicationWaiter
	retainedBytes    int64
	wake             chan struct{}
	stop             chan struct{}
	done             chan struct{}
	closeOnce        sync.Once
	now              func() time.Time
}

type publicationWaiter struct {
	done   chan struct{}
	result resourceDescriptor
}

func newOriginManager(config managerConfig) (*originManager, error) {
	if strings.TrimSpace(config.Root) == "" || strings.TrimSpace(config.StaticURLPrefix) == "" {
		return nil, problem("invalid_config", "root and static URL prefix are required")
	}
	if config.MaxRetainedBytes <= 0 || config.MaxResourceBytes <= 0 ||
		config.MaxResourceBytes > config.MaxRetainedBytes || config.MaxTTL <= 0 || config.MaxProduction <= 0 {
		return nil, problem("invalid_config", "resource, retention, TTL, and production limits must be positive and coherent")
	}
	root, err := filepath.Abs(config.Root)
	if err != nil {
		return nil, fmt.Errorf("canonicalize root: %w", err)
	}
	if err := os.MkdirAll(root, 0o700); err != nil {
		return nil, fmt.Errorf("create root: %w", err)
	}
	root, err = filepath.EvalSymlinks(root)
	if err != nil {
		return nil, fmt.Errorf("resolve root: %w", err)
	}
	if err := ensureOwnedRoot(root); err != nil {
		return nil, err
	}
	m := &originManager{
		root:             root,
		catalogDir:       filepath.Join(root, "catalog"),
		stagingDir:       filepath.Join(root, "staging"),
		readyDir:         filepath.Join(root, "ready"),
		trashDir:         filepath.Join(root, "trash"),
		staticURLPrefix:  strings.TrimRight(config.StaticURLPrefix, "/"),
		maxRetainedBytes: config.MaxRetainedBytes,
		maxResourceBytes: config.MaxResourceBytes,
		maxTTL:           config.MaxTTL,
		maxProduction:    config.MaxProduction,
		records:          make(map[string]*originRecord),
		waiters:          make(map[string]*publicationWaiter),
		wake:             make(chan struct{}, 1),
		stop:             make(chan struct{}),
		done:             make(chan struct{}),
		now:              time.Now,
	}
	for _, dir := range []string{m.catalogDir, m.stagingDir, m.readyDir, m.trashDir} {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return nil, fmt.Errorf("create origin directory: %w", err)
		}
	}
	if err := m.reconcile(); err != nil {
		return nil, err
	}
	go m.expiryLoop()
	return m, nil
}

func (m *originManager) Close() error {
	m.closeOnce.Do(func() { close(m.stop) })
	<-m.done
	return nil
}

func resourceID(key, fingerprint string) string {
	h := sha256.New()
	var length [8]byte
	for _, value := range []string{key, fingerprint} {
		binary.BigEndian.PutUint64(length[:], uint64(len(value)))
		_, _ = h.Write(length[:])
		_, _ = h.Write([]byte(value))
	}
	return hex.EncodeToString(h.Sum(nil))
}

func newGeneration() (string, error) {
	var value [16]byte
	if _, err := rand.Read(value[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(value[:]), nil
}

func (m *originManager) Acquire(ctx context.Context, request acquireRequest) (acquireResult, error) {
	if strings.TrimSpace(request.Key) == "" || len(request.Key) > 1024 ||
		strings.TrimSpace(request.Fingerprint) == "" || len(request.Fingerprint) > 1024 {
		return acquireResult{}, problem("invalid_input", "key and fingerprint are required")
	}
	if request.TTL <= 0 || request.TTL > m.maxTTL || request.MaxBytes <= 0 ||
		request.MaxBytes > m.maxResourceBytes || request.ProductionTimeout <= 0 ||
		request.ProductionTimeout > m.maxProduction {
		return acquireResult{}, problem("invalid_input", "requested limits exceed origin policy")
	}
	id := resourceID(request.Key, request.Fingerprint)
	if request.ParentResourceID == id {
		return acquireResult{}, problem("invalid_input", "resource cannot be its own parent")
	}

	for {
		m.mu.Lock()
		now := m.now().UTC()
		record := m.records[id]
		if record != nil {
			if record.State != stateFailed && record.State != stateExpired &&
				(record.ParentResourceID != request.ParentResourceID || record.MaxBytes != request.MaxBytes ||
					record.TTLMillis != request.TTL.Milliseconds() || record.ProductionMillis != request.ProductionTimeout.Milliseconds()) {
				m.mu.Unlock()
				return acquireResult{}, problem("conflict", "resource identity is active with different publication limits")
			}
			if record.State == stateReady && !now.Before(record.ExpiresAt) {
				if err := m.expireLocked(record, now); err != nil {
					m.mu.Unlock()
					return acquireResult{}, err
				}
			}
			if record.State == statePending && !now.Before(record.PublishBy) {
				if err := m.failLocked(record, "production_timeout"); err != nil {
					m.mu.Unlock()
					return acquireResult{}, err
				}
			}
			switch record.State {
			case stateReady:
				descriptor := m.descriptor(record, "")
				m.mu.Unlock()
				return acquireResult{Descriptor: descriptor}, nil
			case statePending:
				wait := m.waiters[id]
				if wait == nil {
					m.mu.Unlock()
					return acquireResult{}, problem("internal", "pending resource has no waiter")
				}
				m.mu.Unlock()
				select {
				case <-ctx.Done():
					return acquireResult{}, &originError{Kind: "wait_timeout", Err: ctx.Err()}
				case <-wait.done:
					return acquireResult{Descriptor: wait.result}, nil
				}
			case stateFailed, stateExpired:
			}
		}
		if err := m.validateParentLocked(request.ParentResourceID, now); err != nil {
			m.mu.Unlock()
			return acquireResult{}, err
		}
		generation, err := newGeneration()
		if err != nil {
			m.mu.Unlock()
			return acquireResult{}, fmt.Errorf("create generation: %w", err)
		}
		staging := filepath.Join(m.stagingDir, id, generation)
		if err := os.MkdirAll(staging, 0o700); err != nil {
			m.mu.Unlock()
			return acquireResult{}, fmt.Errorf("create staging directory: %w", err)
		}
		record = &originRecord{
			Version:          catalogVersion,
			ResourceID:       id,
			ParentResourceID: request.ParentResourceID,
			State:            statePending,
			Generation:       generation,
			MaxBytes:         request.MaxBytes,
			TTLMillis:        request.TTL.Milliseconds(),
			ProductionMillis: request.ProductionTimeout.Milliseconds(),
			CreatedAt:        now,
			PublishBy:        now.Add(request.ProductionTimeout),
		}
		if err := m.persistLocked(record); err != nil {
			_ = os.RemoveAll(filepath.Join(m.stagingDir, id))
			m.mu.Unlock()
			return acquireResult{}, err
		}
		m.records[id] = record
		m.waiters[id] = &publicationWaiter{done: make(chan struct{})}
		descriptor := m.descriptor(record, "")
		m.mu.Unlock()
		m.signal()
		return acquireResult{Descriptor: descriptor, Produce: true}, nil
	}
}

func (m *originManager) Commit(request commitRequest) (resourceDescriptor, error) {
	entrypoint, err := validMember(request.Entrypoint)
	if err != nil {
		return resourceDescriptor{}, problem("invalid_input", "invalid entrypoint")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	record, err := m.currentGenerationLocked(request.ResourceID, request.Generation)
	if err != nil {
		return resourceDescriptor{}, err
	}
	now := m.now().UTC()
	if !now.Before(record.PublishBy) {
		if err := m.failLocked(record, "production_timeout"); err != nil {
			return resourceDescriptor{}, err
		}
		return resourceDescriptor{}, problem("production_timeout", "production deadline passed")
	}
	if err := m.sweepLocked(now); err != nil {
		return resourceDescriptor{}, err
	}
	if err := m.validateParentLocked(record.ParentResourceID, now); err != nil {
		_ = m.failLocked(record, "parent_unavailable")
		return resourceDescriptor{}, err
	}
	staging := filepath.Join(m.stagingDir, record.ResourceID, record.Generation)
	members, size, err := scanRegularFiles(staging, record.MaxBytes)
	if err != nil {
		return resourceDescriptor{}, &originError{Kind: "invalid_publication", Err: err}
	}
	if _, found := slices.BinarySearch(members, entrypoint); !found {
		return resourceDescriptor{}, problem("invalid_publication", "entrypoint is not a staged regular file")
	}
	if size > m.maxResourceBytes || size > m.maxRetainedBytes-m.retainedBytes {
		return resourceDescriptor{}, problem("storage_limit", "publication exceeds retained-byte limit")
	}
	ready := filepath.Join(m.readyDir, record.ResourceID)
	if _, err := os.Lstat(ready); err == nil {
		return resourceDescriptor{}, problem("conflict", "ready resource path already exists")
	} else if !errors.Is(err, fs.ErrNotExist) {
		return resourceDescriptor{}, fmt.Errorf("inspect ready path: %w", err)
	}
	if err := os.Rename(staging, ready); err != nil {
		return resourceDescriptor{}, fmt.Errorf("publish resource: %w", err)
	}
	previous := *record
	record.State = stateReady
	record.Entrypoint = entrypoint
	record.Members = members
	record.Size = size
	record.PublishedAt = now
	record.ExpiresAt = now.Add(time.Duration(record.TTLMillis) * time.Millisecond)
	if parent := m.records[record.ParentResourceID]; parent != nil && record.ExpiresAt.After(parent.ExpiresAt) {
		record.ExpiresAt = parent.ExpiresAt
	}
	record.FailureKind = ""
	if err := m.persistLocked(record); err != nil {
		*record = previous
		_ = os.Rename(ready, staging)
		return resourceDescriptor{}, err
	}
	m.retainedBytes += size
	_ = os.Remove(filepath.Dir(staging))
	m.notifyLocked(record)
	m.signal()
	return m.descriptor(record, ""), nil
}

func (m *originManager) Fail(request failRequest) (resourceDescriptor, error) {
	if !validFailureKind(request.Kind) {
		return resourceDescriptor{}, problem("invalid_input", "invalid failure kind")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	record, err := m.currentGenerationLocked(request.ResourceID, request.Generation)
	if err != nil {
		return resourceDescriptor{}, err
	}
	if err := m.failLocked(record, request.Kind); err != nil {
		return resourceDescriptor{}, err
	}
	return m.descriptor(record, ""), nil
}

func (m *originManager) Resolve(resourceIDValue, memberValue string) (resourceDescriptor, error) {
	if !validHex(resourceIDValue, sha256.Size*2) {
		return resourceDescriptor{}, problem("invalid_input", "invalid resource ID")
	}
	member := ""
	var err error
	if memberValue != "" {
		member, err = validMember(memberValue)
		if err != nil {
			return resourceDescriptor{}, problem("invalid_input", "invalid member")
		}
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	record := m.records[resourceIDValue]
	if record == nil {
		return resourceDescriptor{State: stateNotFound, ResourceID: resourceIDValue}, nil
	}
	now := m.now().UTC()
	if record.State == stateReady && !now.Before(record.ExpiresAt) {
		if err := m.expireLocked(record, now); err != nil {
			return resourceDescriptor{}, err
		}
	}
	if record.State == statePending && !now.Before(record.PublishBy) {
		if err := m.failLocked(record, "production_timeout"); err != nil {
			return resourceDescriptor{}, err
		}
	}
	if member != "" && record.State == stateReady {
		if _, found := slices.BinarySearch(record.Members, member); !found {
			return resourceDescriptor{State: stateNotFound, ResourceID: resourceIDValue}, nil
		}
	}
	descriptor := m.descriptor(record, member)
	if descriptor.State == statePending {
		// Generation and stagingDir form the producer lease returned only by
		// acquire. A read-only resolve reports lifecycle state without granting
		// another caller the ability to publish that generation.
		descriptor.Generation = ""
		descriptor.StagingDir = ""
		descriptor.MaxBytes = 0
	}
	return descriptor, nil
}

func (m *originManager) validateParentLocked(parentID string, now time.Time) error {
	if parentID == "" {
		return nil
	}
	if !validHex(parentID, sha256.Size*2) {
		return problem("invalid_input", "invalid parent resource ID")
	}
	parent := m.records[parentID]
	if parent == nil {
		return problem("parent_unavailable", "parent resource not found")
	}
	if parent.State == stateReady && !now.Before(parent.ExpiresAt) {
		if err := m.expireLocked(parent, now); err != nil {
			return err
		}
	}
	if parent.State != stateReady {
		return problem("parent_unavailable", "parent resource is not ready")
	}
	return nil
}

func (m *originManager) currentGenerationLocked(resourceIDValue, generation string) (*originRecord, error) {
	if !validHex(resourceIDValue, sha256.Size*2) || !validHex(generation, 32) {
		return nil, problem("invalid_input", "invalid resource ID or generation")
	}
	record := m.records[resourceIDValue]
	if record == nil || record.State != statePending || record.Generation != generation {
		return nil, problem("stale_generation", "generation is not current")
	}
	return record, nil
}

func (m *originManager) descriptor(record *originRecord, member string) resourceDescriptor {
	d := resourceDescriptor{
		State:       record.State,
		ResourceID:  record.ResourceID,
		Entrypoint:  record.Entrypoint,
		Members:     append([]string(nil), record.Members...),
		Size:        record.Size,
		FailureKind: record.FailureKind,
	}
	if record.State == statePending {
		d.Generation = record.Generation
		d.StagingDir = filepath.Join(m.stagingDir, record.ResourceID, record.Generation)
		d.PublishBy = &record.PublishBy
		d.MaxBytes = record.MaxBytes
	}
	if record.State == stateReady {
		d.PublishedAt = &record.PublishedAt
		d.ExpiresAt = &record.ExpiresAt
		if member == "" {
			member = record.Entrypoint
		}
		d.URL = m.memberURL(record.ResourceID, member)
	}
	return d
}

func (m *originManager) memberURL(resourceIDValue, member string) string {
	parts := strings.Split(member, "/")
	for i := range parts {
		parts[i] = url.PathEscape(parts[i])
	}
	return m.staticURLPrefix + "/" + resourceIDValue + "/" + strings.Join(parts, "/")
}

func (m *originManager) failLocked(record *originRecord, kind string) error {
	previous := *record
	record.State = stateFailed
	record.FailureKind = kind
	record.Entrypoint = ""
	record.Members = nil
	record.Size = 0
	record.PublishedAt = time.Time{}
	record.ExpiresAt = time.Time{}
	if err := m.persistLocked(record); err != nil {
		*record = previous
		return err
	}
	_ = os.RemoveAll(filepath.Join(m.stagingDir, record.ResourceID))
	m.notifyLocked(record)
	return nil
}

func (m *originManager) expireLocked(record *originRecord, now time.Time) error {
	if record.State != stateReady {
		return nil
	}
	ready := filepath.Join(m.readyDir, record.ResourceID)
	trash := filepath.Join(m.trashDir, record.ResourceID+"-"+record.Generation)
	_ = os.RemoveAll(trash)
	if err := os.Rename(ready, trash); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("hide expired resource: %w", err)
	}
	m.retainedBytes -= record.Size
	if m.retainedBytes < 0 {
		m.retainedBytes = 0
	}
	record.State = stateExpired
	record.FailureKind = ""
	record.ExpiresAt = now
	if err := m.persistLocked(record); err != nil {
		return err
	}
	_ = os.RemoveAll(trash)
	return nil
}

func (m *originManager) notifyLocked(record *originRecord) {
	if wait := m.waiters[record.ResourceID]; wait != nil {
		wait.result = m.descriptor(record, "")
		close(wait.done)
		delete(m.waiters, record.ResourceID)
	}
}

func (m *originManager) persistLocked(record *originRecord) error {
	payload, err := json.Marshal(record)
	if err != nil {
		return fmt.Errorf("encode catalog record: %w", err)
	}
	payload = append(payload, '\n')
	temporary, err := os.CreateTemp(m.catalogDir, ".record-*")
	if err != nil {
		return fmt.Errorf("create catalog temporary: %w", err)
	}
	name := temporary.Name()
	defer os.Remove(name)
	if err = temporary.Chmod(0o600); err == nil {
		_, err = temporary.Write(payload)
	}
	if err == nil {
		err = temporary.Sync()
	}
	closeErr := temporary.Close()
	if err == nil {
		err = closeErr
	}
	if err != nil {
		return fmt.Errorf("write catalog record: %w", err)
	}
	if err := os.Rename(name, filepath.Join(m.catalogDir, record.ResourceID+".json")); err != nil {
		return fmt.Errorf("publish catalog record: %w", err)
	}
	return nil
}

func (m *originManager) reconcile() error {
	entries, err := os.ReadDir(m.catalogDir)
	if err != nil {
		return fmt.Errorf("read catalog: %w", err)
	}
	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".json" {
			continue
		}
		if entry.Type()&os.ModeSymlink != 0 {
			return problem("invalid_catalog", "catalog record cannot be a symlink")
		}
		payload, err := os.ReadFile(filepath.Join(m.catalogDir, entry.Name()))
		if err != nil {
			return fmt.Errorf("read catalog record: %w", err)
		}
		var record originRecord
		if err := json.Unmarshal(payload, &record); err != nil || record.Version != catalogVersion ||
			record.ResourceID+".json" != entry.Name() || !validHex(record.ResourceID, sha256.Size*2) {
			return problem("invalid_catalog", "catalog contains an invalid record")
		}
		copyRecord := record
		m.records[record.ResourceID] = &copyRecord
	}

	now := m.now().UTC()
	for _, record := range m.records {
		switch record.State {
		case statePending:
			if err := m.failLocked(record, "abandoned"); err != nil {
				return err
			}
		case stateReady:
			if !now.Before(record.ExpiresAt) {
				if err := m.expireLocked(record, now); err != nil {
					return err
				}
				continue
			}
			members, size, err := scanRegularFiles(filepath.Join(m.readyDir, record.ResourceID), record.MaxBytes)
			_, entrypointFound := slices.BinarySearch(members, record.Entrypoint)
			if err != nil || size != record.Size || !equalStrings(members, record.Members) || !entrypointFound {
				record.State = stateFailed
				record.FailureKind = "invalid_persisted_resource"
				record.Entrypoint = ""
				record.Members = nil
				record.Size = 0
				record.PublishedAt = time.Time{}
				record.ExpiresAt = time.Time{}
				_ = os.RemoveAll(filepath.Join(m.readyDir, record.ResourceID))
				if err := m.persistLocked(record); err != nil {
					return err
				}
				continue
			}
			m.retainedBytes += size
		case stateFailed, stateExpired:
			_ = os.RemoveAll(filepath.Join(m.stagingDir, record.ResourceID))
			_ = os.RemoveAll(filepath.Join(m.readyDir, record.ResourceID))
		default:
			return problem("invalid_catalog", "catalog contains an unknown state")
		}
	}
	if m.retainedBytes > m.maxRetainedBytes {
		return problem("storage_limit", "persisted resources exceed retained-byte limit")
	}
	if err := m.removeOrphanDirectories(m.stagingDir, statePending); err != nil {
		return err
	}
	if err := m.removeOrphanDirectories(m.readyDir, stateReady); err != nil {
		return err
	}
	trashEntries, err := os.ReadDir(m.trashDir)
	if err != nil {
		return fmt.Errorf("read trash directory: %w", err)
	}
	for _, entry := range trashEntries {
		if err := os.RemoveAll(filepath.Join(m.trashDir, entry.Name())); err != nil {
			return fmt.Errorf("remove abandoned trash entry: %w", err)
		}
	}
	return nil
}

func (m *originManager) removeOrphanDirectories(root string, state resourceState) error {
	entries, err := os.ReadDir(root)
	if err != nil {
		return err
	}
	for _, entry := range entries {
		record := m.records[entry.Name()]
		if !entry.IsDir() || record == nil || record.State != state {
			if err := os.RemoveAll(filepath.Join(root, entry.Name())); err != nil {
				return err
			}
		}
	}
	return nil
}

func (m *originManager) signal() {
	select {
	case m.wake <- struct{}{}:
	default:
	}
}

func (m *originManager) expiryLoop() {
	defer close(m.done)
	for {
		m.mu.Lock()
		var next time.Time
		for _, record := range m.records {
			deadline := time.Time{}
			switch record.State {
			case statePending:
				deadline = record.PublishBy
			case stateReady:
				deadline = record.ExpiresAt
			}
			if !deadline.IsZero() && (next.IsZero() || deadline.Before(next)) {
				next = deadline
			}
		}
		m.mu.Unlock()
		var timer <-chan time.Time
		if !next.IsZero() {
			delay := time.Until(next)
			if delay < 0 {
				delay = 0
			}
			timer = time.After(delay)
		}
		select {
		case <-m.stop:
			return
		case <-m.wake:
			continue
		case <-timer:
			m.sweep()
		}
	}
}

func (m *originManager) sweep() {
	m.mu.Lock()
	defer m.mu.Unlock()
	_ = m.sweepLocked(m.now().UTC())
}

func (m *originManager) sweepLocked(now time.Time) error {
	for _, record := range m.records {
		if record.State == statePending && !now.Before(record.PublishBy) {
			if err := m.failLocked(record, "production_timeout"); err != nil {
				return err
			}
		}
		if record.State == stateReady && !now.Before(record.ExpiresAt) {
			if err := m.expireLocked(record, now); err != nil {
				return err
			}
		}
	}
	return nil
}

func ensureOwnedRoot(root string) error {
	marker := filepath.Join(root, ownerMarker)
	payload, err := os.ReadFile(marker)
	if err == nil {
		var value struct {
			Version int `json:"version"`
		}
		if json.Unmarshal(payload, &value) != nil || value.Version != catalogVersion {
			return problem("invalid_config", "root has an invalid ownership marker")
		}
		return nil
	}
	if !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("read ownership marker: %w", err)
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		return fmt.Errorf("inspect root: %w", err)
	}
	if len(entries) != 0 {
		return problem("invalid_config", "refusing to claim a non-empty root")
	}
	return os.WriteFile(marker, []byte("{\"version\":1}\n"), 0o600)
}

func scanRegularFiles(root string, limit int64) ([]string, int64, error) {
	var members []string
	var total int64
	err := filepath.WalkDir(root, func(current string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if current == root {
			return nil
		}
		if entry.Type()&os.ModeSymlink != 0 {
			return errors.New("symlinks are not allowed")
		}
		if entry.IsDir() {
			return nil
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() {
			return errors.New("only regular files may be published")
		}
		relative, err := filepath.Rel(root, current)
		if err != nil {
			return err
		}
		member := filepath.ToSlash(relative)
		if _, err := validMember(member); err != nil {
			return err
		}
		if info.Size() > limit-total {
			return errors.New("publication exceeds resource byte limit")
		}
		total += info.Size()
		members = append(members, member)
		return nil
	})
	if err != nil {
		return nil, 0, err
	}
	if len(members) == 0 {
		return nil, 0, errors.New("publication contains no files")
	}
	sort.Strings(members)
	return members, total, nil
}

func validMember(value string) (string, error) {
	if value == "" || value == "." || strings.Contains(value, "\\") || !fs.ValidPath(value) {
		return "", errors.New("invalid relative member path")
	}
	return value, nil
}

func validFailureKind(value string) bool {
	if value == "" || len(value) > 64 {
		return false
	}
	for _, r := range value {
		if (r < 'a' || r > 'z') && (r < '0' || r > '9') && r != '_' && r != '-' {
			return false
		}
	}
	return true
}

func validHex(value string, length int) bool {
	if len(value) != length {
		return false
	}
	_, err := hex.DecodeString(value)
	return err == nil
}

func equalStrings(left, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	for i := range left {
		if left[i] != right[i] {
			return false
		}
	}
	return true
}
