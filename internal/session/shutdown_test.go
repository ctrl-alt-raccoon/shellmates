package session

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	screenpkg "github.com/ctrl-alt-raccoon/sclaude/internal/screen"
)

func claimedSession(t *testing.T) (Store, Record) {
	t.Helper()
	store := NewStore(t.TempDir())
	record, err := NewRecord("Shutdown", "claude", t.TempDir(), time.Now().Add(-time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Save(record); err != nil {
		t.Fatal(err)
	}
	if _, err := store.ClaimRunner(record.ID, 10, time.Now()); err != nil {
		t.Fatal(err)
	}
	record, err = store.MarkStarted(record.ID, 10, 20, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	return store, record
}

func TestShutdownWaitsForAcknowledgementBeforeClosingScreen(t *testing.T) {
	store, record := claimedSession(t)
	fake := &fakeScreen{sockets: []screenpkg.Socket{{PID: 30, Name: record.ScreenName, Status: screenpkg.Detached}}}
	manager := Manager{Store: store, Screen: fake, StopTimeout: 40 * time.Millisecond}
	if err := manager.Stop(context.Background(), record.ID); err == nil || !strings.Contains(err.Error(), "backend exit is unconfirmed") {
		t.Fatalf("stop=%v", err)
	}
	if fake.stopped != "" {
		t.Fatal("Screen closed before backend acknowledgement")
	}
	pending, err := store.Load(record.ID)
	if err != nil || pending.State != StateStopping || pending.EndedAt != nil || !pending.ExitUnconfirmed() {
		t.Fatalf("pending=%+v %v", pending, err)
	}
	finished, err := store.Finish(record.ID, 143, "terminated", time.Now())
	if err != nil || finished.State != StateStopping || !finished.BackendExited {
		t.Fatalf("ack=%+v %v", finished, err)
	}
	if err := manager.Stop(context.Background(), record.ID); err != nil {
		t.Fatal(err)
	}
	finished, err = store.Load(record.ID)
	if err != nil || finished.Active() || finished.ExitUnconfirmed() || fake.stopped != record.ScreenName {
		t.Fatalf("finished=%+v %v", finished, err)
	}
}

func TestMissingSocketNeverSubstitutesForBackendExit(t *testing.T) {
	for _, state := range []State{StateRunning, StateStopping, StateStopped, StateFailed} {
		store, record := claimedSession(t)
		record.State = state
		if err := store.Save(record); err != nil {
			t.Fatal(err)
		}
		manager := Manager{Store: store, Screen: &fakeScreen{}, StopTimeout: time.Millisecond}
		records, warnings := manager.List(context.Background())
		if len(records) != 1 || records[0].State != state || len(warnings) == 0 {
			t.Fatalf("state=%s records=%+v warnings=%v", state, records, warnings)
		}
		if n, err := manager.Prune(context.Background(), nil, 0, true); n != 0 || err == nil {
			t.Fatalf("pruned uncertain exit: %d %v", n, err)
		}
		if err := store.Delete(record); err == nil {
			t.Fatal("direct delete bypassed exit guard")
		}
	}
}

func TestRunnerClaimAndStopAreSerialized(t *testing.T) {
	store := NewStore(t.TempDir())
	record, _ := NewRecord("Before fork", "claude", t.TempDir(), time.Now())
	if err := store.Save(record); err != nil {
		t.Fatal(err)
	}
	if _, err := store.requestStop(record.ID, time.Now()); err != nil {
		t.Fatal(err)
	}
	if _, err := store.ClaimRunner(record.ID, 10, time.Now()); !errors.Is(err, ErrSessionNotRunnable) {
		t.Fatalf("claim=%v", err)
	}
	store, record = claimedSession(t)
	if _, err := store.ClaimRunner(record.ID, 11, time.Now()); !errors.Is(err, ErrSessionNotRunnable) {
		t.Fatalf("second claim=%v", err)
	}
}

func TestLegacyPIDIsNotShutdownAuthority(t *testing.T) {
	store := NewStore(t.TempDir())
	record, _ := NewRecord("Legacy", "claude", t.TempDir(), time.Now().Add(-time.Minute))
	record.State, record.BackendPID = StateRunning, os.Getpid()
	if err := store.Save(record); err != nil {
		t.Fatal(err)
	}
	fake := &fakeScreen{sockets: []screenpkg.Socket{{PID: 30, Name: record.ScreenName, Status: screenpkg.Detached}}}
	manager := Manager{Store: store, Screen: fake, StopTimeout: time.Millisecond}
	if err := manager.Stop(context.Background(), record.ID); err == nil {
		t.Fatal("legacy socket disappearance treated as backend exit")
	}
	if n, err := manager.Prune(context.Background(), nil, 0, true); n != 0 || err == nil {
		t.Fatalf("legacy prune=%d %v", n, err)
	}
}

func TestRunnerCanClaimScreenReconciledStartup(t *testing.T) {
	store := NewStore(t.TempDir())
	record, _ := NewRecord("Reconciled", "claude", t.TempDir(), time.Now())
	record.State = StateRunning // Screen socket was observed before runner fork.
	if err := store.Save(record); err != nil {
		t.Fatal(err)
	}
	if _, err := store.ClaimRunner(record.ID, 10, time.Now()); err != nil {
		t.Fatal(err)
	}
	if _, err := store.MarkStarted(record.ID, 10, 20, time.Now()); err != nil {
		t.Fatal(err)
	}
}
