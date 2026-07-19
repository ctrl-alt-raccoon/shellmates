package session

import (
	"errors"
	"testing"
	"time"
)

func TestRunnerStartCannotOverwriteStopIntent(t *testing.T) {
	store := NewStore(t.TempDir())
	now := time.Now().UTC()
	record, err := NewRecord("Stop race", "claude", t.TempDir(), now)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Save(record); err != nil {
		t.Fatal(err)
	}
	if _, err := store.requestStop(record.ID, now.Add(time.Millisecond)); err != nil {
		t.Fatal(err)
	}
	if _, err := store.MarkStarted(record.ID, 10, 20, now.Add(2*time.Millisecond)); !errors.Is(err, ErrSessionNotRunnable) {
		t.Fatalf("MarkStarted error = %v, want %v", err, ErrSessionNotRunnable)
	}
	loaded, err := store.Load(record.ID)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.State != StateStopping || loaded.EndReason != "stopped-by-manager" || loaded.RunnerPID != 0 || loaded.BackendPID != 0 {
		t.Fatalf("stop intent was overwritten: %+v", loaded)
	}
}

func TestRunnerFinishPreservesStopIntentAndRecordsExit(t *testing.T) {
	store := NewStore(t.TempDir())
	now := time.Now().UTC()
	record, err := NewRecord("Finish race", "claude", t.TempDir(), now)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Save(record); err != nil {
		t.Fatal(err)
	}
	if _, err := store.MarkStarted(record.ID, 10, 20, now.Add(time.Millisecond)); err != nil {
		t.Fatal(err)
	}
	if _, err := store.requestStop(record.ID, now.Add(2*time.Millisecond)); err != nil {
		t.Fatal(err)
	}
	finished, err := store.Finish(record.ID, 143, "terminated", now.Add(3*time.Millisecond))
	if err != nil {
		t.Fatal(err)
	}
	if finished.State != StateStopped || finished.EndReason != "stopped-by-manager" || finished.ExitCode == nil || *finished.ExitCode != 143 || finished.TermSignal != "terminated" {
		t.Fatalf("finish lost stop or exit information: %+v", finished)
	}
}

func TestRunnerFinishAddsExitInfoToManagerTerminalState(t *testing.T) {
	store := NewStore(t.TempDir())
	now := time.Now().UTC()
	record, err := NewRecord("Late finish", "claude", t.TempDir(), now)
	if err != nil {
		t.Fatal(err)
	}
	record.State = StateStopped
	record.EndReason = "stopped-by-manager"
	ended := now.Add(time.Millisecond)
	record.EndedAt = &ended
	if err := store.Save(record); err != nil {
		t.Fatal(err)
	}
	finished, err := store.Finish(record.ID, 0, "", now.Add(2*time.Millisecond))
	if err != nil {
		t.Fatal(err)
	}
	if finished.State != StateStopped || finished.EndReason != "stopped-by-manager" || finished.ExitCode == nil || *finished.ExitCode != 0 {
		t.Fatalf("late finish overwrote terminal state or lost exit: %+v", finished)
	}
}
