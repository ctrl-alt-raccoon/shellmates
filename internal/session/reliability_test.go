package session

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	screenpkg "github.com/ctrl-alt-raccoon/shellmates/internal/screen"
)

func TestCrashRecoveryRemovesClaimsAndInterruptedWrites(t *testing.T) {
	store := NewStore(t.TempDir())
	record, _ := NewRecord("Crash", "claude", t.TempDir(), time.Now())
	if err := store.Save(record); err != nil {
		t.Fatal(err)
	}
	if err := store.SaveLaunch(LaunchRequest{SessionID: record.ID, Args: []string{"synthetic private prompt"}}); err != nil {
		t.Fatal(err)
	}
	claim := filepath.Join(store.LaunchDir, ".consume-crashed")
	if err := os.Rename(store.launchPath(record.ID), claim); err != nil {
		t.Fatal(err)
	}
	paths := []string{claim, filepath.Join(store.LaunchDir, ".sclaude-interrupted"), filepath.Join(store.SessionsDir, ".sclaude-interrupted")}
	for _, path := range paths[1:] {
		if err := os.WriteFile(path, []byte("synthetic private prompt"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if errs := store.CleanupLaunches(); len(errs) != 0 {
		t.Fatal(errs)
	}
	for _, path := range paths {
		if _, err := os.Lstat(path); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("abandoned file remains: %s: %v", path, err)
		}
	}
	loaded, err := store.Load(record.ID)
	if err != nil || !loaded.Active() {
		t.Fatalf("recovery changed active record: %+v %v", loaded, err)
	}
}

func TestStoreRejectsSymlinkDirectoriesWithoutExternalMutation(t *testing.T) {
	for _, child := range []string{"root", "sessions", "launch"} {
		t.Run(child, func(t *testing.T) {
			root := filepath.Join(t.TempDir(), "state")
			external := t.TempDir()
			if err := os.Chmod(external, 0o755); err != nil {
				t.Fatal(err)
			}
			path := root
			if child != "root" {
				if err := os.Mkdir(root, 0o700); err != nil {
					t.Fatal(err)
				}
				path = filepath.Join(root, child)
			}
			if err := os.Symlink(external, path); err != nil {
				t.Fatal(err)
			}
			store := NewStore(root)
			if err := store.SaveLaunch(LaunchRequest{SessionID: strings.Repeat("a", 32)}); err == nil {
				t.Fatal("symlink accepted")
			}
			info, err := os.Stat(external)
			if err != nil || info.Mode().Perm() != 0o755 {
				t.Fatalf("external mode changed: %v %v", info, err)
			}
			entries, err := os.ReadDir(external)
			if err != nil || len(entries) != 0 {
				t.Fatalf("external writes: %v %v", entries, err)
			}
		})
	}
}

func TestStoreRejectsUnsafeFilesWithoutBlocking(t *testing.T) {
	for _, kind := range []string{"symlink", "hardlink", "fifo", "oversize"} {
		t.Run(kind, func(t *testing.T) {
			store := NewStore(t.TempDir())
			if err := store.Ensure(); err != nil {
				t.Fatal(err)
			}
			id := strings.Repeat("b", 32)
			external := filepath.Join(t.TempDir(), "external")
			if err := os.WriteFile(external, []byte("untouched"), 0o644); err != nil {
				t.Fatal(err)
			}
			for _, path := range []string{store.LockPath, store.recordPath(id), store.launchPath(id)} {
				var err error
				switch kind {
				case "symlink":
					err = os.Symlink(external, path)
				case "hardlink":
					err = os.Link(external, path)
				case "fifo":
					err = syscall.Mkfifo(path, 0o600)
				case "oversize":
					err = os.WriteFile(path, []byte(strings.Repeat("x", maxSessionFileSize+1)), 0o600)
				}
				if err != nil {
					t.Fatal(err)
				}
			}
			if _, err := store.Load(id); err == nil {
				t.Fatal("unsafe record accepted")
			}
			if _, err := store.ConsumeLaunch(id); err == nil {
				t.Fatal("unsafe launch accepted")
			}
			data, err := os.ReadFile(external)
			if err != nil || string(data) != "untouched" {
				t.Fatalf("external content changed: %v", err)
			}
			info, err := os.Stat(external)
			if err != nil || info.Mode().Perm() != 0o644 {
				t.Fatalf("external permissions changed: %v", err)
			}
		})
	}
}

func TestDeleteRechecksLifecycleUnderLock(t *testing.T) {
	store := NewStore(t.TempDir())
	record, _ := NewRecord("Race", "claude", t.TempDir(), time.Now())
	if err := store.Save(record); err != nil {
		t.Fatal(err)
	}
	stale := record
	stale.State = StateStopped
	if err := store.Delete(stale); err == nil {
		t.Fatal("stale snapshot deleted an active record")
	}
}

func TestTerminalSocketBlocksPruneAndAllowsExplicitStop(t *testing.T) {
	store := NewStore(t.TempDir())
	record, _ := NewRecord("Legacy", "claude", t.TempDir(), time.Now().Add(-time.Minute))
	record.State = StateStopped
	if err := store.Save(record); err != nil {
		t.Fatal(err)
	}
	fake := &fakeScreen{sockets: []screenpkg.Socket{{PID: 1234, Name: record.ScreenName, Status: screenpkg.Detached}}}
	manager := Manager{Store: store, Screen: fake}
	if n, err := manager.Prune(context.Background(), nil, 0, true); err == nil || n != 0 {
		t.Fatalf("live terminal record pruned: %d %v", n, err)
	}
	if err := manager.Stop(context.Background(), record.ID); err != nil {
		t.Fatal(err)
	}
	if n, err := manager.Prune(context.Background(), nil, 0, true); err != nil || n != 1 {
		t.Fatalf("confirmed stopped record not pruned: %d %v", n, err)
	}
}

func TestPruneRefusesFailedScreenProbe(t *testing.T) {
	store := NewStore(t.TempDir())
	record, _ := NewRecord("Unknown", "claude", t.TempDir(), time.Now())
	record.State = StateStopped
	if err := store.Save(record); err != nil {
		t.Fatal(err)
	}
	manager := Manager{Store: store, Screen: &fakeScreen{listErr: errors.New("unrecognized socket listing")}}
	if n, err := manager.Prune(context.Background(), nil, 0, true); err == nil || n != 0 {
		t.Fatalf("pruned with unknown liveness: %d %v", n, err)
	}
}

func TestLaunchConsumptionAndCleanupSerialize(t *testing.T) {
	store := NewStore(t.TempDir())
	record, _ := NewRecord("Concurrent claim", "claude", t.TempDir(), time.Now())
	if err := store.Save(record); err != nil {
		t.Fatal(err)
	}
	if err := store.SaveLaunch(LaunchRequest{SessionID: record.ID, Args: []string{"synthetic"}}); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	var consumed int
	var mu sync.Mutex
	for i := 0; i < 12; i++ {
		wg.Add(1)
		go func(consumer bool) {
			defer wg.Done()
			if !consumer {
				if errs := store.CleanupLaunches(); len(errs) != 0 {
					t.Errorf("cleanup: %v", errs)
				}
				return
			}
			request, err := store.ConsumeLaunch(record.ID)
			if errors.Is(err, os.ErrNotExist) {
				return
			}
			if err != nil {
				t.Errorf("consume: %v", err)
				return
			}
			if len(request.Args) != 1 || request.Args[0] != "synthetic" {
				t.Errorf("request corrupted")
			}
			mu.Lock()
			consumed++
			mu.Unlock()
		}(i%2 == 0)
	}
	wg.Wait()
	if consumed != 1 {
		t.Fatalf("consumers=%d", consumed)
	}
	entries, err := os.ReadDir(store.LaunchDir)
	if err != nil || len(entries) != 0 {
		t.Fatalf("launch files remain: %v %v", entries, err)
	}
}
