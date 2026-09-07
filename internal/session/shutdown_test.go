package session

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	screenpkg "github.com/ctrl-alt-raccoon/shellmates/internal/screen"
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
		if err := manager.Stop(context.Background(), record.ID); err == nil || !strings.Contains(err.Error(), "backend exit is unconfirmed") {
			t.Fatalf("missing socket certified backend exit: state=%s err=%v", state, err)
		}
	}
}

// Model the socket changing after List but before quit completes, without timing
// or a real Screen process. The underlying fake still returns its control error.
type stopRaceScreen struct {
	*fakeScreen
	afterStop func()
}

func (s *stopRaceScreen) Stop(ctx context.Context, name string) error {
	err := s.fakeScreen.Stop(ctx, name)
	s.afterStop()
	return err
}

func TestStopControlErrorAfterBackendExit(t *testing.T) {
	for _, tc := range []struct {
		name          string
		legacy        bool
		socketRemains bool
		probeFails    bool
		recordMissing bool
	}{
		{name: "socket disappeared before quit"},
		{name: "legacy socket disappeared before quit", legacy: true},
		{name: "socket still present", socketRemains: true},
		{name: "absence probe failed", probeFails: true},
		{name: "finalization failure remains an error", recordMissing: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			store, record := claimedSession(t)
			if tc.legacy {
				// Legacy records without runner/backend PIDs require only the
				// existing socket-absence check; do not invent an acknowledgement.
				record.ShutdownProtocol, record.RunnerPID, record.BackendPID = 0, 0, 0
				if err := store.Save(record); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := store.requestStop(record.ID, time.Now()); err != nil {
				t.Fatal(err)
			}
			if !tc.legacy {
				if _, err := store.Finish(record.ID, 143, "terminated", time.Now()); err != nil {
					t.Fatal(err)
				}
			}
			controlErr := errors.New("Screen control failed")
			probeErr := errors.New("ambiguous Screen listing")
			other := screenpkg.Socket{PID: 31, Name: "unrelated-session", Status: screenpkg.Detached}
			fake := &stopRaceScreen{fakeScreen: &fakeScreen{
				sockets: []screenpkg.Socket{{PID: 30, Name: record.ScreenName, Status: screenpkg.Detached}, other},
				stopErr: controlErr,
			}}
			fake.afterStop = func() {
				if !tc.socketRemains {
					fake.sockets = []screenpkg.Socket{other}
				}
				if tc.probeFails {
					fake.listErr = probeErr
				}
				if tc.recordMissing {
					if err := os.Remove(store.recordPath(record.ID)); err != nil {
						t.Fatal(err)
					}
				}
			}
			manager := Manager{Store: store, Screen: fake, StopTimeout: 100 * time.Millisecond}
			err := manager.Stop(context.Background(), record.ID)
			if fake.stopped != record.ScreenName {
				t.Fatalf("controlled wrong session: %q", fake.stopped)
			}
			if tc.recordMissing {
				if !errors.Is(err, os.ErrNotExist) {
					t.Fatalf("finalization error lost: %v", err)
				}
				return
			}
			finished, loadErr := store.Load(record.ID)
			if loadErr != nil {
				t.Fatal(loadErr)
			}
			if !tc.legacy && (!finished.BackendExited || finished.ExitCode == nil || *finished.ExitCode != 143) {
				t.Fatalf("backend acknowledgement lost: %+v %v", finished, loadErr)
			}
			if tc.socketRemains || tc.probeFails {
				if !errors.Is(err, controlErr) || (tc.probeFails && !errors.Is(err, probeErr)) {
					t.Fatalf("unconfirmed shutdown error lost: %v", err)
				}
				if finished.State != StateStopping || !finished.Active() || finished.EndedAt != nil {
					t.Fatalf("unconfirmed shutdown finalized: %+v", finished)
				}
				if n, _ := manager.Prune(context.Background(), nil, 0, true); n != 0 {
					t.Fatal("unconfirmed shutdown was pruned")
				}
				return
			}
			if err != nil {
				t.Fatalf("independently confirmed shutdown reported failure: %v", err)
			}
			if finished.State != StateStopped || finished.Active() || finished.EndedAt == nil || finished.ScreenPID != 0 || finished.EndReason != "stopped-by-manager" {
				t.Fatalf("confirmed shutdown not finalized: %+v", finished)
			}
			if len(fake.sockets) != 1 || fake.sockets[0] != other {
				t.Fatal("unrelated Screen session changed")
			}
		})
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
