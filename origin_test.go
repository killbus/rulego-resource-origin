package main

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func testManager(t *testing.T, retained, resource int64) *originManager {
	t.Helper()
	m, err := newOriginManager(managerConfig{
		Root:             t.TempDir(),
		StaticURLPrefix:  "/resources",
		MaxRetainedBytes: retained,
		MaxResourceBytes: resource,
		MaxTTL:           time.Hour,
		MaxProduction:    time.Minute,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = m.Close() })
	return m
}

func request(key string) acquireRequest {
	return acquireRequest{
		Key: key, Fingerprint: "recipe-v1", TTL: time.Minute,
		MaxBytes: 1024, ProductionTimeout: time.Second,
	}
}

func TestAcquireCommitSharesGeneration(t *testing.T) {
	m := testManager(t, 4096, 2048)
	first, err := m.Acquire(context.Background(), request("video"))
	if err != nil || !first.Produce || first.Descriptor.State != statePending {
		t.Fatalf("first acquire = %#v, %v", first, err)
	}
	waited := make(chan acquireResult, 1)
	errs := make(chan error, 1)
	go func() {
		result, err := m.Acquire(context.Background(), request("video"))
		waited <- result
		errs <- err
	}()

	if err := os.WriteFile(filepath.Join(first.Descriptor.StagingDir, "index.m3u8"), []byte("playlist"), 0o600); err != nil {
		t.Fatal(err)
	}
	ready, err := m.Commit(commitRequest{
		ResourceID: first.Descriptor.ResourceID,
		Generation: first.Descriptor.Generation,
		Entrypoint: "index.m3u8",
	})
	if err != nil {
		t.Fatal(err)
	}
	if ready.State != stateReady || ready.URL != "/resources/"+ready.ResourceID+"/index.m3u8" ||
		ready.Entrypoint != "index.m3u8" || len(ready.Members) != 1 || ready.Members[0] != "index.m3u8" ||
		ready.Size != 8 || ready.PublishedAt == nil || ready.PublishedAt.IsZero() ||
		ready.ExpiresAt == nil || ready.ExpiresAt.IsZero() {
		t.Fatalf("ready descriptor = %#v", ready)
	}
	readyJSON, _ := json.Marshal(ready)
	if strings.Contains(string(readyJSON), `"publishBy"`) {
		t.Fatalf("ready descriptor contains pending deadline: %s", readyJSON)
	}
	readyPath := filepath.Join(m.readyDir, ready.ResourceID, "index.m3u8")
	if payload, err := os.ReadFile(readyPath); err != nil || string(payload) != "playlist" {
		t.Fatalf("published file = %q, %v", payload, err)
	}
	if err := <-errs; err != nil {
		t.Fatal(err)
	}
	second := <-waited
	if second.Produce || second.Descriptor.ResourceID != ready.ResourceID || second.Descriptor.State != stateReady {
		t.Fatalf("shared acquire = %#v", second)
	}
	if _, err := os.Stat(first.Descriptor.StagingDir); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("staging still visible: %v", err)
	}
	resolved, err := m.Resolve(ready.ResourceID, "index.m3u8")
	if err != nil || resolved.URL != ready.URL {
		t.Fatalf("resolve = %#v, %v", resolved, err)
	}
}

func TestMultiMemberPublicationSurvivesRestart(t *testing.T) {
	root := t.TempDir()
	config := managerConfig{
		Root: root, StaticURLPrefix: "/resources", MaxRetainedBytes: 4096,
		MaxResourceBytes: 2048, MaxTTL: time.Hour, MaxProduction: time.Minute,
	}
	m, err := newOriginManager(config)
	if err != nil {
		t.Fatal(err)
	}
	acquired, err := m.Acquire(context.Background(), request("multi-member"))
	if err != nil {
		t.Fatal(err)
	}
	files := map[string]string{
		"index.m3u8":     "playlist",
		"segment-000.ts": "first",
		"segment-001.ts": "second",
	}
	for name, payload := range files {
		if err := os.WriteFile(filepath.Join(acquired.Descriptor.StagingDir, name), []byte(payload), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	ready, err := m.Commit(commitRequest{acquired.Descriptor.ResourceID, acquired.Descriptor.Generation, "segment-001.ts"})
	if err != nil {
		t.Fatal(err)
	}
	if got, want := strings.Join(ready.Members, ","), "index.m3u8,segment-000.ts,segment-001.ts"; got != want {
		t.Fatalf("members = %q, want %q", got, want)
	}
	if resolved, err := m.Resolve(ready.ResourceID, "segment-000.ts"); err != nil || resolved.State != stateReady {
		t.Fatalf("resolve middle member = %#v, %v", resolved, err)
	}
	if err := m.Close(); err != nil {
		t.Fatal(err)
	}

	m, err = newOriginManager(config)
	if err != nil {
		t.Fatal(err)
	}
	defer m.Close()
	if resolved, err := m.Resolve(ready.ResourceID, "index.m3u8"); err != nil || resolved.State != stateReady {
		t.Fatalf("entrypoint after restart = %#v, %v", resolved, err)
	}
}

func TestResolvePendingDoesNotExposeProducerLease(t *testing.T) {
	m := testManager(t, 4096, 2048)
	acquired, err := m.Acquire(context.Background(), request("pending-resolve"))
	if err != nil {
		t.Fatal(err)
	}
	readyPath := filepath.Join(m.readyDir, acquired.Descriptor.ResourceID, "artifact.bin")
	if _, err := os.Stat(readyPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("staged member visible before commit: %v", err)
	}

	resolved, err := m.Resolve(acquired.Descriptor.ResourceID, "")
	if err != nil {
		t.Fatal(err)
	}
	if resolved.State != statePending || resolved.ResourceID != acquired.Descriptor.ResourceID ||
		resolved.PublishBy == nil || resolved.PublishBy.IsZero() {
		t.Fatalf("pending resolve = %#v", resolved)
	}
	if resolved.Generation != "" || resolved.StagingDir != "" || resolved.MaxBytes != 0 {
		t.Fatalf("resolve disclosed producer lease = %#v", resolved)
	}
	resolvedJSON, _ := json.Marshal(resolved)
	for _, field := range []string{`"generation"`, `"stagingDir"`, `"maxBytes"`, `"publishedAt"`, `"expiresAt"`} {
		if strings.Contains(string(resolvedJSON), field) {
			t.Fatalf("pending resolve contains %s: %s", field, resolvedJSON)
		}
	}
}

func TestCommitRejectsUnsafeOrOversizePublication(t *testing.T) {
	t.Run("symlink", func(t *testing.T) {
		m := testManager(t, 4096, 2048)
		result, err := m.Acquire(context.Background(), request("symlink"))
		if err != nil {
			t.Fatal(err)
		}
		outside := filepath.Join(t.TempDir(), "outside")
		if err := os.WriteFile(outside, []byte("secret"), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(outside, filepath.Join(result.Descriptor.StagingDir, "index.m3u8")); err != nil {
			t.Fatal(err)
		}
		_, err = m.Commit(commitRequest{result.Descriptor.ResourceID, result.Descriptor.Generation, "index.m3u8"})
		if errorKind(err) != "invalid_publication" {
			t.Fatalf("commit error = %v", err)
		}
	})

	t.Run("oversize", func(t *testing.T) {
		m := testManager(t, 4096, 2048)
		r := request("oversize")
		r.MaxBytes = 4
		result, err := m.Acquire(context.Background(), r)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(result.Descriptor.StagingDir, "data.bin"), []byte("12345"), 0o600); err != nil {
			t.Fatal(err)
		}
		_, err = m.Commit(commitRequest{result.Descriptor.ResourceID, result.Descriptor.Generation, "data.bin"})
		if errorKind(err) != "invalid_publication" {
			t.Fatalf("commit error = %v", err)
		}
	})

	t.Run("missing entrypoint", func(t *testing.T) {
		m := testManager(t, 4096, 2048)
		result, err := m.Acquire(context.Background(), request("missing-entrypoint"))
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(result.Descriptor.StagingDir, "other.bin"), []byte("data"), 0o600); err != nil {
			t.Fatal(err)
		}
		_, err = m.Commit(commitRequest{result.Descriptor.ResourceID, result.Descriptor.Generation, "missing.bin"})
		if errorKind(err) != "invalid_publication" {
			t.Fatalf("commit error = %v", err)
		}
		if _, err := os.Stat(filepath.Join(m.readyDir, result.Descriptor.ResourceID)); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("invalid publication became visible: %v", err)
		}
	})

	t.Run("no regular files", func(t *testing.T) {
		m := testManager(t, 4096, 2048)
		result, err := m.Acquire(context.Background(), request("no-regular-files"))
		if err != nil {
			t.Fatal(err)
		}
		if err := os.Mkdir(filepath.Join(result.Descriptor.StagingDir, "directory"), 0o700); err != nil {
			t.Fatal(err)
		}
		_, err = m.Commit(commitRequest{result.Descriptor.ResourceID, result.Descriptor.Generation, "directory"})
		if errorKind(err) != "invalid_publication" {
			t.Fatalf("commit error = %v", err)
		}
	})

	t.Run("stale generation and traversal", func(t *testing.T) {
		m := testManager(t, 4096, 2048)
		result, err := m.Acquire(context.Background(), request("stale"))
		if err != nil {
			t.Fatal(err)
		}
		if _, err := m.Commit(commitRequest{result.Descriptor.ResourceID, result.Descriptor.Generation, "../outside"}); errorKind(err) != "invalid_input" {
			t.Fatalf("traversal error = %v", err)
		}
		if _, err := m.Fail(failRequest{result.Descriptor.ResourceID, "00000000000000000000000000000000", "producer_failed"}); errorKind(err) != "stale_generation" {
			t.Fatalf("stale generation error = %v", err)
		}
		if _, err := m.Commit(commitRequest{result.Descriptor.ResourceID, "00000000000000000000000000000000", "artifact.bin"}); errorKind(err) != "stale_generation" {
			t.Fatalf("stale commit error = %v", err)
		}
	})
}

func TestParentExpiryAndRestartReconciliation(t *testing.T) {
	root := t.TempDir()
	config := managerConfig{
		Root: root, StaticURLPrefix: "/resources", MaxRetainedBytes: 4096,
		MaxResourceBytes: 2048, MaxTTL: time.Hour, MaxProduction: time.Minute,
	}
	m, err := newOriginManager(config)
	if err != nil {
		t.Fatal(err)
	}
	parentResult, err := m.Acquire(context.Background(), request("parent"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(parentResult.Descriptor.StagingDir, "manifest"), []byte("p"), 0o600); err != nil {
		t.Fatal(err)
	}
	parent, err := m.Commit(commitRequest{parentResult.Descriptor.ResourceID, parentResult.Descriptor.Generation, "manifest"})
	if err != nil {
		t.Fatal(err)
	}
	childRequest := request("child")
	childRequest.ParentResourceID = parent.ResourceID
	childRequest.TTL = 2 * time.Minute
	childResult, err := m.Acquire(context.Background(), childRequest)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(childResult.Descriptor.StagingDir, "segment"), []byte("c"), 0o600); err != nil {
		t.Fatal(err)
	}
	child, err := m.Commit(commitRequest{childResult.Descriptor.ResourceID, childResult.Descriptor.Generation, "segment"})
	if err != nil {
		t.Fatal(err)
	}
	if child.ExpiresAt == nil || parent.ExpiresAt == nil || !child.ExpiresAt.Equal(*parent.ExpiresAt) {
		t.Fatalf("child expiry %v exceeds parent %v", child.ExpiresAt, parent.ExpiresAt)
	}
	if err := m.Close(); err != nil {
		t.Fatal(err)
	}

	unrelated := filepath.Join(root, "unrelated")
	if err := os.WriteFile(unrelated, []byte("keep"), 0o600); err != nil {
		t.Fatal(err)
	}
	m2, err := newOriginManager(config)
	if err != nil {
		t.Fatal(err)
	}
	if got, err := m2.Resolve(parent.ResourceID, "manifest"); err != nil || got.State != stateReady || got.URL != parent.URL {
		t.Fatalf("ready resource after restart = %#v, %v", got, err)
	}
	m2.mu.Lock()
	m2.records[parent.ResourceID].ExpiresAt = time.Now().Add(-time.Second)
	m2.records[child.ResourceID].ExpiresAt = time.Now().Add(-time.Second)
	m2.mu.Unlock()
	m2.sweep()
	if got, err := m2.Resolve(parent.ResourceID, ""); err != nil || got.State != stateExpired {
		t.Fatalf("expired parent = %#v, %v", got, err)
	}
	if _, err := m2.Acquire(context.Background(), childRequest); errorKind(err) != "parent_unavailable" {
		t.Fatalf("child acquire after parent expiry = %v", err)
	}
	if _, err := os.Stat(unrelated); err != nil {
		t.Fatalf("unrelated file changed: %v", err)
	}
	_ = m2.Close()
}

func TestRestartAbandonsPendingAndRemovesOwnedOrphans(t *testing.T) {
	root := t.TempDir()
	config := managerConfig{
		Root: root, StaticURLPrefix: "/resources", MaxRetainedBytes: 4096,
		MaxResourceBytes: 2048, MaxTTL: time.Hour, MaxProduction: time.Minute,
	}
	m, err := newOriginManager(config)
	if err != nil {
		t.Fatal(err)
	}
	pending, err := m.Acquire(context.Background(), request("abandoned"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(pending.Descriptor.StagingDir, "partial"), []byte("partial"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := m.Close(); err != nil {
		t.Fatal(err)
	}

	readyOrphan := filepath.Join(root, "ready", "orphan", "data")
	trashOrphan := filepath.Join(root, "trash", "expired", "data")
	for _, path := range []string{readyOrphan, trashOrphan} {
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte("orphan"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	unrelated := filepath.Join(root, "unrelated")
	if err := os.WriteFile(unrelated, []byte("keep"), 0o600); err != nil {
		t.Fatal(err)
	}

	m, err = newOriginManager(config)
	if err != nil {
		t.Fatal(err)
	}
	defer m.Close()
	resolved, err := m.Resolve(pending.Descriptor.ResourceID, "")
	if err != nil || resolved.State != stateFailed || resolved.FailureKind != "abandoned" {
		t.Fatalf("abandoned resolve = %#v, %v", resolved, err)
	}
	for _, path := range []string{pending.Descriptor.StagingDir, readyOrphan, trashOrphan} {
		if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("owned orphan remains at %s: %v", path, err)
		}
	}
	if payload, err := os.ReadFile(unrelated); err != nil || string(payload) != "keep" {
		t.Fatalf("unrelated file = %q, %v", payload, err)
	}
}

func TestFailureAndProductionTimeoutWakeWaiters(t *testing.T) {
	m := testManager(t, 4096, 2048)
	first, err := m.Acquire(context.Background(), request("failure"))
	if err != nil {
		t.Fatal(err)
	}
	waited := make(chan acquireResult, 1)
	go func() {
		result, _ := m.Acquire(context.Background(), request("failure"))
		waited <- result
	}()
	select {
	case result := <-waited:
		t.Fatalf("pending waiter returned early: %#v", result)
	case <-time.After(10 * time.Millisecond):
	}
	failed, err := m.Fail(failRequest{first.Descriptor.ResourceID, first.Descriptor.Generation, "producer_failed"})
	if err != nil || failed.State != stateFailed {
		t.Fatalf("fail = %#v, %v", failed, err)
	}
	if got := <-waited; got.Produce || got.Descriptor.State != stateFailed {
		t.Fatalf("waiter result = %#v", got)
	}

	timed, err := m.Acquire(context.Background(), request("timeout"))
	if err != nil {
		t.Fatal(err)
	}
	m.mu.Lock()
	m.records[timed.Descriptor.ResourceID].PublishBy = time.Now().Add(-time.Second)
	m.mu.Unlock()
	resolved, err := m.Resolve(timed.Descriptor.ResourceID, "")
	if err != nil || resolved.State != stateFailed || resolved.FailureKind != "production_timeout" {
		t.Fatalf("timeout resolve = %#v, %v", resolved, err)
	}
}

func TestFailedGenerationWaiterDoesNotJoinRetry(t *testing.T) {
	m := testManager(t, 4096, 2048)
	first, err := m.Acquire(context.Background(), request("retry"))
	if err != nil {
		t.Fatal(err)
	}
	ctx := &observedContext{Context: context.Background(), entered: make(chan struct{})}
	result := make(chan acquireResult, 1)
	go func() {
		got, _ := m.Acquire(ctx, request("retry"))
		result <- got
	}()
	<-ctx.entered

	m.mu.Lock()
	record := m.records[first.Descriptor.ResourceID]
	if err := m.failLocked(record, "producer_failed"); err != nil {
		m.mu.Unlock()
		t.Fatal(err)
	}
	retry := *record
	retry.State = statePending
	retry.Generation = "11111111111111111111111111111111"
	retry.FailureKind = ""
	retry.PublishBy = time.Now().Add(time.Minute)
	m.records[retry.ResourceID] = &retry
	m.waiters[retry.ResourceID] = &publicationWaiter{done: make(chan struct{})}
	m.mu.Unlock()

	select {
	case got := <-result:
		if got.Descriptor.State != stateFailed || got.Descriptor.FailureKind != "producer_failed" {
			t.Fatalf("waiter followed retry generation: %#v", got)
		}
	case <-time.After(time.Second):
		t.Fatal("waiter blocked on retry generation")
	}
}

type observedContext struct {
	context.Context
	entered chan struct{}
	once    sync.Once
}

func (c *observedContext) Done() <-chan struct{} {
	c.once.Do(func() { close(c.entered) })
	return c.Context.Done()
}

func TestAcquireRejectsDifferentPolicyForLiveIdentity(t *testing.T) {
	m := testManager(t, 4096, 2048)
	first := request("identity")
	if _, err := m.Acquire(context.Background(), first); err != nil {
		t.Fatal(err)
	}
	different := first
	different.MaxBytes--
	if _, err := m.Acquire(context.Background(), different); errorKind(err) != "conflict" {
		t.Fatalf("policy conflict = %v", err)
	}
}

func TestRetainedLimitAndOwnedRoot(t *testing.T) {
	m := testManager(t, 10, 10)
	firstRequest := request("first")
	firstRequest.MaxBytes = 10
	first, err := m.Acquire(context.Background(), firstRequest)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(first.Descriptor.StagingDir, "data"), []byte("123456"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := m.Commit(commitRequest{first.Descriptor.ResourceID, first.Descriptor.Generation, "data"}); err != nil {
		t.Fatal(err)
	}
	secondRequest := request("second")
	secondRequest.MaxBytes = 10
	second, err := m.Acquire(context.Background(), secondRequest)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(second.Descriptor.StagingDir, "data"), []byte("12345"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := m.Commit(commitRequest{second.Descriptor.ResourceID, second.Descriptor.Generation, "data"}); errorKind(err) != "storage_limit" {
		t.Fatalf("retained limit error = %v", err)
	}

	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "unrelated"), []byte("keep"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err = newOriginManager(managerConfig{
		Root: root, StaticURLPrefix: "/resources", MaxRetainedBytes: 10,
		MaxResourceBytes: 10, MaxTTL: time.Minute, MaxProduction: time.Minute,
	})
	if errorKind(err) != "invalid_config" {
		t.Fatalf("non-empty root error = %v", err)
	}
}

func TestDecodeOperationIsStrict(t *testing.T) {
	operation, value, err := decodeOperation(`{"operation":"resolve","resourceId":"abc","member":"index.m3u8"}`)
	if err != nil || operation != "resolve" || value.(resolvePayload).Member != "index.m3u8" {
		t.Fatalf("decode = %q, %#v, %v", operation, value, err)
	}
	if _, _, err := decodeOperation(`{"operation":"resolve","resourceId":"abc","unexpected":true}`); err == nil {
		t.Fatal("unknown field accepted")
	}
	if _, _, err := decodeOperation(`{"operation":"unknown"}`); err == nil {
		t.Fatal("unknown operation accepted")
	}
	if _, _, err := decodeOperation(`{"operation":"acquire","key":"k","fingerprint":"f","ttlMs":9223372036854775807,"maxBytes":1,"productionTimeoutMs":1}`); err == nil {
		t.Fatal("overflowing duration accepted")
	}
}

func TestResolveUnknownResource(t *testing.T) {
	m := testManager(t, 4096, 2048)
	id := strings.Repeat("0", sha256.Size*2)
	resolved, err := m.Resolve(id, "")
	if err != nil || resolved.State != stateNotFound || resolved.ResourceID != id || resolved.URL != "" {
		t.Fatalf("unknown resolve = %#v, %v", resolved, err)
	}
}

func errorKind(err error) string {
	var target *originError
	if errors.As(err, &target) {
		return target.Kind
	}
	return ""
}
