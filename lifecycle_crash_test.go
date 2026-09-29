package main

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// The crash windows below are selected by real filesystem mutations inside a
// separate go test process, never by wall-clock timing. The helper performs
// the mutation first, writes a barrier file and then parks until the parent
// kills the process. Wall time only bounds failure detection.
const (
	crashBarrierWait = 15 * time.Second
	crashKillWait    = 45 * time.Second
)

func TestLifecycleCrashHelperWorker(t *testing.T) {
	root := os.Getenv("CRASH_ROOT")
	mode := os.Getenv("CRASH_MODE")
	barrier := os.Getenv("CRASH_BARRIER")
	if root == "" || mode == "" || barrier == "" {
		t.Skip("crash helper requires CRASH_ROOT, CRASH_MODE and CRASH_BARRIER")
	}
	hooks := defaultCleanupHooks()
	signal := func() {
		if err := os.WriteFile(barrier, []byte(mode), 0o644); err != nil {
			os.Exit(3)
		}
	}
	// Quarantine claims rename residue into a unique trash/.gc-* container.
	// No other publication path uses that destination, so this hook observes
	// exactly the claim window: after the rename, before deletion is queued.
	hooks.rename = func(from, to string) error {
		err := os.Rename(from, to)
		if err == nil && mode == "claim" && strings.HasPrefix(filepath.Base(filepath.Dir(to)), ".gc-") {
			signal()
			crashAwaitKill()
		}
		return err
	}
	// collectTerminalLocked is the only hooks.remove caller; it unlinks the
	// terminal catalog JSON immediately before deleting the memory record.
	hooks.remove = func(path string) error {
		err := os.Remove(path)
		if err == nil && mode == "unlink" && strings.HasSuffix(path, ".json") {
			signal()
			crashAwaitKill()
		}
		return err
	}
	config := crashManagerConfig(root)
	config.hooks = &hooks
	m, err := newOriginManager(config)
	if err != nil {
		t.Fatal(err)
	}
	defer m.Close()
	ctx := context.Background()
	valid, err := m.Acquire(ctx, request("crash-valid"))
	if err != nil || !valid.Produce {
		t.Fatalf("acquire valid: %#v, %v", valid, err)
	}
	if err := os.WriteFile(filepath.Join(valid.Descriptor.StagingDir, "data"), []byte("payload"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := m.Commit(commitRequest{valid.Descriptor.ResourceID, valid.Descriptor.Generation, "data"}); err != nil {
		t.Fatal(err)
	}
	switch mode {
	case "claim":
		// A ready payload without a catalog record is owned residue. The
		// periodic scanner claims it by renaming it into quarantine.
		residueID := resourceID("crash-claim", "recipe-v1")
		if err := os.WriteFile(filepath.Join(m.readyDir, residueID), []byte("stale"), 0o600); err != nil {
			t.Fatal(err)
		}
	case "unlink":
		lease, err := m.Acquire(ctx, request("crash-unlink"))
		if err != nil || !lease.Produce {
			t.Fatalf("acquire terminal: %#v, %v", lease, err)
		}
		if err := os.WriteFile(filepath.Join(lease.Descriptor.StagingDir, "data"), []byte("payload"), 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := m.Fail(failRequest{
			ResourceID: lease.Descriptor.ResourceID,
			Generation: lease.Descriptor.Generation,
			Kind:       "producer_error",
		}); err != nil {
			t.Fatal(err)
		}
	default:
		t.Fatalf("unknown crash mode %q", mode)
	}
	// If the crash window never fired, park bounded and self-terminate so a
	// broken child can never hang CI.
	crashAwaitKill()
}

func crashAwaitKill() {
	// The parent kills the process while it parks here. If the kill never
	// arrives, self-terminate so the child cannot outlive the test run.
	<-time.After(crashKillWait)
	os.Exit(3)
}

func crashManagerConfig(root string) managerConfig {
	return managerConfig{
		Root:             root,
		StaticURLPrefix:  "/resources",
		MaxRetainedBytes: 4096,
		MaxResourceBytes: 2048,
		MaxTTL:           time.Hour,
		MaxProduction:    time.Minute,
	}
}

type crashProcess struct {
	cmd    *exec.Cmd
	output *crashOutput
	waited bool
}

type crashOutput struct {
	mu    sync.Mutex
	buf   bytes.Buffer
	limit int
}

func (o *crashOutput) Write(payload []byte) (int, error) {
	o.mu.Lock()
	defer o.mu.Unlock()
	if room := o.limit - o.buf.Len(); room <= 0 {
		// Discard but accept the payload so the helper never sees a short write.
		return len(payload), nil
	} else if len(payload) > room {
		o.buf.Write(payload[:room])
		return len(payload), nil
	}
	return o.buf.Write(payload)
}

func (o *crashOutput) String() string {
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.buf.String()
}

func startCrashHelper(t *testing.T, root, mode, barrier string) *crashProcess {
	t.Helper()
	cmd := exec.Command(os.Args[0], "-test.run", "^TestLifecycleCrashHelperWorker$", "-test.count=1")
	cmd.Env = append(os.Environ(),
		"CRASH_ROOT="+root,
		"CRASH_MODE="+mode,
		"CRASH_BARRIER="+barrier,
	)
	output := &crashOutput{limit: 1 << 16}
	cmd.Stdout, cmd.Stderr = output, output
	if err := cmd.Start(); err != nil {
		t.Fatalf("start crash helper: %v", err)
	}
	process := &crashProcess{cmd: cmd, output: output}
	t.Cleanup(process.stop)
	return process
}

func (p *crashProcess) stop() {
	if p.waited {
		return
	}
	if p.cmd.Process != nil {
		_ = p.cmd.Process.Kill()
	}
	_ = p.cmd.Wait()
	p.waited = true
}

func (p *crashProcess) diagnostics() string {
	return p.output.String()
}

func awaitCrashBarrier(t *testing.T, barrier, mode string, diagnostics func() string) {
	t.Helper()
	deadline := time.Now().Add(crashBarrierWait)
	for {
		payload, err := os.ReadFile(barrier)
		if err == nil && string(payload) == mode {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("crash helper did not reach %q window: %s", mode, diagnostics())
		}
		time.Sleep(2 * time.Millisecond)
	}
}

func killCrashHelper(t *testing.T, process *crashProcess) {
	t.Helper()
	if err := process.cmd.Process.Kill(); err != nil {
		t.Fatalf("kill crash helper: %v", err)
	}
	err := process.cmd.Wait()
	process.waited = true
	if err == nil {
		t.Fatalf("crash helper exited cleanly, expected killed process: %s", process.diagnostics())
	}
}

func prepareCrashRoot(t *testing.T) (root, barrier string) {
	t.Helper()
	base := t.TempDir()
	root = filepath.Join(base, "origin")
	if err := os.Mkdir(root, 0o700); err != nil {
		t.Fatal(err)
	}
	return root, filepath.Join(base, "barrier")
}

// The helper reaches the window after the quarantine claim rename and before
// the quarantined payload deletion is queued. A killed process leaves the
// unique trash container behind; the restarted manager owns the residue and
// must finish recovery without losing the valid ready generation.
func TestLifecycleCrashAfterQuarantineClaimRestartsClean(t *testing.T) {
	root, barrier := prepareCrashRoot(t)
	process := startCrashHelper(t, root, "claim", barrier)
	awaitCrashBarrier(t, barrier, "claim", process.diagnostics)
	killCrashHelper(t, process)

	residueID := resourceID("crash-claim", "recipe-v1")
	validID := resourceID("crash-valid", "recipe-v1")
	m, err := newOriginManager(crashManagerConfig(root))
	if err != nil {
		t.Fatalf("restart after quarantine crash: %v", err)
	}
	t.Cleanup(func() { _ = m.Close() })
	assertCleanupCount(t, m, 0, 7)
	if got, err := m.Resolve(validID, ""); err != nil || got.State != stateReady || got.URL == "" {
		t.Fatalf("valid ready lost after crash: %#v %v", got, err)
	}
	payload, err := os.ReadFile(filepath.Join(root, "ready", validID, "data"))
	if err != nil || string(payload) != "payload" {
		t.Fatalf("valid ready bytes lost: %q %v", payload, err)
	}
	assertGone(t, filepath.Join(root, "ready", residueID))
	entries, err := os.ReadDir(filepath.Join(root, "trash"))
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("quarantine retained after restart: %d entries", len(entries))
	}
	lease, err := m.Acquire(context.Background(), request("crash-claim"))
	if err != nil || !lease.Produce {
		t.Fatalf("acquire recycled identity: %#v, %v", lease, err)
	}
	if lease.Descriptor.ResourceID != residueID {
		t.Fatalf("identity changed after residue cleanup: %s", lease.Descriptor.ResourceID)
	}
	if err := os.WriteFile(filepath.Join(lease.Descriptor.StagingDir, "data"), []byte("payload"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := m.Commit(commitRequest{residueID, lease.Descriptor.Generation, "data"}); err != nil {
		t.Fatal(err)
	}
	if got, err := m.Resolve(residueID, "data"); err != nil || got.State != stateReady {
		t.Fatalf("recycled generation unusable: %#v %v", got, err)
	}
	payload, err = os.ReadFile(filepath.Join(m.readyDir, residueID, "data"))
	if err != nil || string(payload) != "payload" {
		t.Fatalf("recycled ready bytes lost: %q %v", payload, err)
	}
	// Join the manager before reopening the same root. The new publication
	// must survive recovery with the exact generation and payload intact.
	if err := m.Close(); err != nil {
		t.Fatal(err)
	}
	restarted, err := newOriginManager(crashManagerConfig(root))
	if err != nil {
		t.Fatalf("second restart after quarantine recovery: %v", err)
	}
	t.Cleanup(func() { _ = restarted.Close() })
	if got, err := restarted.Resolve(residueID, ""); err != nil || got.State != stateReady {
		t.Fatalf("replacement lost on second restart: %#v %v", got, err)
	}
	catalog, err := os.ReadFile(filepath.Join(restarted.catalogDir, residueID+".json"))
	if err != nil || !bytes.Contains(catalog, []byte(lease.Descriptor.Generation)) {
		t.Fatalf("replacement catalog generation lost: %q %v", catalog, err)
	}
	replacementPayload, err := os.ReadFile(filepath.Join(restarted.readyDir, residueID, "data"))
	if err != nil || string(replacementPayload) != "payload" {
		t.Fatalf("replacement payload lost: %q %v", replacementPayload, err)
	}
	if got, err := restarted.Resolve(validID, ""); err != nil || got.State != stateReady || got.URL == "" {
		t.Fatalf("valid ready lost on second restart: %#v %v", got, err)
	}
	restartedPayload, err := os.ReadFile(filepath.Join(restarted.readyDir, validID, "data"))
	if err != nil || string(restartedPayload) != "payload" {
		t.Fatalf("valid ready bytes lost on second restart: %q %v", restartedPayload, err)
	}
}

// The helper reaches the window after the terminal catalog unlink and before
// the in-memory record deletion. The catalog no longer describes the failed
// generation, so a restart must report not_found and must never resurrect the
// failed state.
func TestLifecycleCrashAfterCatalogUnlinkRestartsClean(t *testing.T) {
	root, barrier := prepareCrashRoot(t)
	process := startCrashHelper(t, root, "unlink", barrier)
	awaitCrashBarrier(t, barrier, "unlink", process.diagnostics)
	killCrashHelper(t, process)

	validID := resourceID("crash-valid", "recipe-v1")
	terminalID := resourceID("crash-unlink", "recipe-v1")
	m, err := newOriginManager(crashManagerConfig(root))
	if err != nil {
		t.Fatalf("restart after unlink crash: %v", err)
	}
	t.Cleanup(func() { _ = m.Close() })
	assertCleanupCount(t, m, 0, 7)
	assertGone(t, filepath.Join(root, "catalog", terminalID+".json"))
	assertGone(t, filepath.Join(root, "staging", terminalID))
	if got, err := m.Resolve(terminalID, ""); err != nil || got.State != stateNotFound {
		t.Fatalf("terminal record resurrected after unlink crash: %#v %v", got, err)
	}
	if got, err := m.Resolve(validID, ""); err != nil || got.State != stateReady || got.URL == "" {
		t.Fatalf("valid ready lost after crash: %#v %v", got, err)
	}
	payload, err := os.ReadFile(filepath.Join(root, "ready", validID, "data"))
	if err != nil || string(payload) != "payload" {
		t.Fatalf("valid ready bytes lost: %q %v", payload, err)
	}
	entries, err := os.ReadDir(filepath.Join(root, "trash"))
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("unexpected trash after restart: %d entries", len(entries))
	}
	lease, err := m.Acquire(context.Background(), request("crash-unlink"))
	if err != nil || !lease.Produce {
		t.Fatalf("acquire recycled identity: %#v, %v", lease, err)
	}
	if lease.Descriptor.ResourceID != terminalID {
		t.Fatalf("identity changed after unlink: %s", lease.Descriptor.ResourceID)
	}
	if err := os.WriteFile(filepath.Join(lease.Descriptor.StagingDir, "data"), []byte("payload"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := m.Commit(commitRequest{terminalID, lease.Descriptor.Generation, "data"}); err != nil {
		t.Fatal(err)
	}
	if got, err := m.Resolve(terminalID, "data"); err != nil || got.State != stateReady {
		t.Fatalf("recycled generation unusable: %#v %v", got, err)
	}
	payload, err = os.ReadFile(filepath.Join(m.readyDir, terminalID, "data"))
	if err != nil || string(payload) != "payload" {
		t.Fatalf("recycled ready bytes lost: %q %v", payload, err)
	}
	// Join the manager before reopening the same root. The new publication
	// must survive recovery with the exact generation and payload intact.
	if err := m.Close(); err != nil {
		t.Fatal(err)
	}
	restarted, err := newOriginManager(crashManagerConfig(root))
	if err != nil {
		t.Fatalf("second restart after unlink recovery: %v", err)
	}
	t.Cleanup(func() { _ = restarted.Close() })
	if got, err := restarted.Resolve(terminalID, ""); err != nil || got.State != stateReady {
		t.Fatalf("replacement lost on second restart: %#v %v", got, err)
	}
	catalog, err := os.ReadFile(filepath.Join(restarted.catalogDir, terminalID+".json"))
	if err != nil || !bytes.Contains(catalog, []byte(lease.Descriptor.Generation)) {
		t.Fatalf("replacement catalog generation lost: %q %v", catalog, err)
	}
	replacementPayload, err := os.ReadFile(filepath.Join(restarted.readyDir, terminalID, "data"))
	if err != nil || string(replacementPayload) != "payload" {
		t.Fatalf("replacement payload lost: %q %v", replacementPayload, err)
	}
	if got, err := restarted.Resolve(validID, ""); err != nil || got.State != stateReady || got.URL == "" {
		t.Fatalf("valid ready lost on second restart: %#v %v", got, err)
	}
	restartedPayload, err := os.ReadFile(filepath.Join(restarted.readyDir, validID, "data"))
	if err != nil || string(restartedPayload) != "payload" {
		t.Fatalf("valid ready bytes lost on second restart: %q %v", restartedPayload, err)
	}
}
