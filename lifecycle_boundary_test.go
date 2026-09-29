package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func boundaryConfig(root string) managerConfig {
	return managerConfig{Root: root, StaticURLPrefix: "/resources", MaxRetainedBytes: 4096, MaxResourceBytes: 2048, MaxTTL: time.Hour, MaxProduction: time.Minute}
}

func TestLifecycleStartupCatalogClassification(t *testing.T) {
	for _, kind := range []string{"unknown_file", "unknown_dir", "id_dir", "corrupt_record", "record_link", "unknown_link"} {
		t.Run(kind, func(t *testing.T) {
			root := t.TempDir()
			config := boundaryConfig(root)
			m, err := newOriginManager(config)
			if err != nil {
				t.Fatal(err)
			}
			_ = m.Close()
			name := "unknown.json"
			if kind == "id_dir" || kind == "corrupt_record" || kind == "record_link" {
				name = strings.Repeat("a", 64) + ".json"
			}
			path := filepath.Join(root, "catalog", name)
			outside := filepath.Join(t.TempDir(), "keep")
			if err := os.WriteFile(outside, []byte("external sentinel"), 0600); err != nil {
				t.Fatal(err)
			}
			switch kind {
			case "unknown_dir", "id_dir":
				err = os.Mkdir(path, 0700)
			case "record_link", "unknown_link":
				if err = os.Symlink(outside, path); err != nil {
					t.Skipf("symlink unavailable: %v", err)
				}
			default:
				err = os.WriteFile(path, []byte("invalid JSON"), 0600)
			}
			if err != nil {
				t.Fatal(err)
			}
			restarted, err := newOriginManager(config)
			if kind == "corrupt_record" || kind == "record_link" {
				if err == nil {
					_ = restarted.Close()
					t.Fatal("invalid real record accepted")
				}
			} else {
				if err != nil {
					t.Fatal(err)
				}
				_ = restarted.Close()
				if _, err := os.Lstat(path); !os.IsNotExist(err) {
					t.Fatalf("residue retained: %v", err)
				}
			}
			data, err := os.ReadFile(outside)
			if err != nil || string(data) != "external sentinel" {
				t.Fatal("external target changed", err)
			}
		})
	}
}

func TestLifecycleRejectsLinkedAncestorBeforeCreation(t *testing.T) {
	outside := t.TempDir()
	parent := t.TempDir()
	link := filepath.Join(parent, "linked")
	if err := os.Symlink(outside, link); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	m, err := newOriginManager(boundaryConfig(filepath.Join(link, "must-not-create")))
	if err == nil {
		_ = m.Close()
		t.Fatal("linked ancestor accepted")
	}
	entries, err := os.ReadDir(outside)
	if err != nil || len(entries) != 0 {
		t.Fatalf("startup wrote through ancestor: %v %v", entries, err)
	}
}

func TestLifecycleValidatesAllManagedDirectoriesBeforeCreation(t *testing.T) {
	root, outside := t.TempDir(), t.TempDir()
	if err := ensureOwnedRoot(root); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(root, "trash")); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	m, err := newOriginManager(boundaryConfig(root))
	if err == nil {
		_ = m.Close()
		t.Fatal("linked managed directory accepted")
	}
	for _, name := range []string{"catalog", "staging", "ready"} {
		if _, err := os.Lstat(filepath.Join(root, name)); !os.IsNotExist(err) {
			t.Fatalf("created %s before validating trash: %v", name, err)
		}
	}
}

func TestLifecycleScanFailureBackoffAcrossPasses(t *testing.T) {
	now := time.Date(2035, 1, 1, 0, 0, 0, 0, time.UTC)
	hooks := defaultCleanupHooks()
	hooks.now = func() time.Time { return now }
	config := boundaryConfig(t.TempDir())
	config.hooks = &hooks
	m, err := newOriginManager(config)
	if err != nil {
		t.Fatal(err)
	}
	_ = m.Close()
	defer m.closeResidueCursors()
	residue := filepath.Join(m.catalogDir, "orphan")
	if err := os.WriteFile(residue, []byte("x"), 0600); err != nil {
		t.Fatal(err)
	}
	attempts := 0
	m.hooks.rename = func(from, to string) error {
		if from == residue {
			attempts++
			return os.ErrPermission
		}
		return os.Rename(from, to)
	}
	for _, delay := range []time.Duration{time.Second, 2 * time.Second, 4 * time.Second, 8 * time.Second, 16 * time.Second, 32 * time.Second, time.Minute, time.Minute} {
		previous := attempts
		for n := 0; n < 3 && attempts == previous; n++ {
			m.scanRoot = 0
			m.scanNext = time.Time{}
			m.scanResidueBatch()
		}
		if attempts != previous+1 {
			t.Fatalf("failed path was not revisited: %d", attempts)
		}
		work := m.scanErrors[0]
		if work == nil || work.next.Sub(now) != delay {
			t.Fatalf("scan backoff reset, want %s: %#v", delay, work)
		}
		m.scanRoot = 0
		m.scanNext = time.Time{}
		m.scanResidueBatch()
		if attempts != previous+1 {
			t.Fatal("scan retried before deadline")
		}
		now = work.next
	}
}
