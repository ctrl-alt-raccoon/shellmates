package setup

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/ctrl-alt-raccoon/sclaude/internal/config"
)

type serviceTestRunner struct {
	outputs  map[string]serviceTestOutput
	commands [][]string
	runErrs  map[string]error
	onRun    func(context.Context, []string) error
}

type serviceTestOutput struct {
	data []byte
	err  error
}

func prepareServiceTestExecutable(t *testing.T, name string) trustedExecutable {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(path, []byte("#!/bin/sh\nexit 0\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	executable, err := trustExecutablePath(path)
	if err != nil {
		t.Fatal(err)
	}
	return executable
}

func (r *serviceTestRunner) Run(ctx context.Context, name string, args ...string) error {
	command := append([]string{name}, args...)
	r.commands = append(r.commands, command)
	if r.onRun != nil {
		if err := r.onRun(ctx, command); err != nil {
			return err
		}
	}
	return r.runErrs[strings.Join(command, " ")]
}

func (r *serviceTestRunner) Output(_ context.Context, name string, args ...string) ([]byte, error) {
	output := r.outputs[strings.Join(append([]string{name}, args...), " ")]
	return append([]byte(nil), output.data...), output.err
}

func TestPrepareServiceStateCapturesUserSystemdState(t *testing.T) {
	executable := prepareServiceTestExecutable(t, "systemctl")
	runner := &serviceTestRunner{outputs: map[string]serviceTestOutput{
		executable.path() + " --user is-active cliproxyapi.service":  {data: []byte("active\n")},
		executable.path() + " --user is-enabled cliproxyapi.service": {data: []byte("disabled\n"), err: errors.New("exit status 1")},
	}}
	state, err := prepareServiceState(context.Background(), runner, "systemd", false, executable)
	if err != nil {
		t.Fatal(err)
	}
	if !state.active || state.scheduled {
		t.Fatalf("service state = %+v", state)
	}
}

func TestPreparedServiceStateRestoresPriorSystemdState(t *testing.T) {
	executable := prepareServiceTestExecutable(t, "systemctl")
	runner := &serviceTestRunner{}
	state := preparedServiceState{
		manager:          "systemd",
		executable:       executable,
		active:           false,
		scheduled:        false,
		activeTouched:    true,
		scheduledTouched: true,
	}
	if err := state.restore(context.Background(), runner); err != nil {
		t.Fatal(err)
	}
	want := [][]string{
		{executable.path(), "--user", "disable", "cliproxyapi.service"},
	}
	if !reflect.DeepEqual(runner.commands, want) {
		t.Fatalf("restore commands = %q, want %q", runner.commands, want)
	}
}

func TestPreparedServiceStateRestoresHomebrewState(t *testing.T) {
	for _, test := range []struct {
		name        string
		state       preparedServiceState
		wantActions []string
	}{
		{
			name:        "scheduled running",
			state:       preparedServiceState{manager: "brew", active: true, scheduled: true, activeTouched: true, scheduledTouched: true},
			wantActions: []string{"restart"},
		},
		{
			name:        "unscheduled running",
			state:       preparedServiceState{manager: "brew", active: true, scheduled: false, activeTouched: true, scheduledTouched: true},
			wantActions: []string{"stop", "run"},
		},
		{
			name:        "stopped unscheduled",
			state:       preparedServiceState{manager: "brew", active: false, scheduled: false, activeTouched: true, scheduledTouched: true},
			wantActions: []string{"stop"},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			executable := prepareServiceTestExecutable(t, "brew")
			test.state.executable = executable
			runner := &serviceTestRunner{}
			if err := test.state.restore(context.Background(), runner); err != nil {
				t.Fatal(err)
			}
			want := make([][]string, 0, len(test.wantActions))
			for _, action := range test.wantActions {
				want = append(want, []string{executable.path(), "services", action, "cliproxyapi"})
			}
			if !reflect.DeepEqual(runner.commands, want) {
				t.Fatalf("restore commands = %q, want %q", runner.commands, want)
			}
		})
	}
}

func TestPrepareServiceStateRejectsScheduledInactiveHomebrewService(t *testing.T) {
	executable := prepareServiceTestExecutable(t, "brew")
	runner := &serviceTestRunner{outputs: map[string]serviceTestOutput{
		executable.path() + " services list --json": {
			data: []byte(`[{"name":"cliproxyapi","status":"scheduled","running":false,"scheduled":true}]`),
		},
	}}
	_, err := prepareServiceState(context.Background(), runner, "brew", false, executable)
	if err == nil || !strings.Contains(err.Error(), "scheduled but not running") {
		t.Fatalf("prepare error = %v", err)
	}
	if len(runner.commands) != 0 {
		t.Fatalf("service mutation occurred: %q", runner.commands)
	}
}

func TestPreparedServiceStateRejectsExecutableReplacement(t *testing.T) {
	executable := prepareServiceTestExecutable(t, "systemctl")
	replacement := executable.path() + ".replacement"
	if err := os.WriteFile(replacement, []byte("#!/bin/sh\nexit 1\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(replacement, executable.path()); err != nil {
		t.Fatal(err)
	}
	state := preparedServiceState{
		manager:       "systemd",
		executable:    executable,
		activeTouched: true,
	}
	runner := &serviceTestRunner{}
	if err := state.quiesce(context.Background(), runner); err == nil || !strings.Contains(err.Error(), "trusted executable changed") {
		t.Fatalf("quiesce error = %v", err)
	}
	if len(runner.commands) != 0 {
		t.Fatalf("replacement executable was invoked: %q", runner.commands)
	}
}

func TestFinishSetupWorkflowRestoresServiceAndFilesBeforeDurableCommit(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "runtime.json")
	if err := os.WriteFile(target, []byte("before\n"), 0o640); err != nil {
		t.Fatal(err)
	}
	transaction, err := newSetupTransaction(filepath.Join(root, "state"))
	if err != nil {
		t.Fatal(err)
	}
	runtimeConfig := config.Runtime{
		SchemaVersion:   config.SchemaVersion,
		RealClaude:      "/usr/bin/claude",
		ClaudexMode:     "external",
		RealClaudex:     "/usr/bin/claudex",
		ScreenPath:      "/usr/bin/screen",
		CLIProxyService: "none",
	}
	runtimeData, err := config.EncodeRuntime(runtimeConfig)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := transaction.addFile(target, runtimeData, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := transaction.persist(); err != nil {
		t.Fatal(err)
	}

	primary := errors.New("injected commit failure")
	beforeCommitSetupTransaction = func() error { return primary }
	t.Cleanup(func() { beforeCommitSetupTransaction = nil })
	executable := prepareServiceTestExecutable(t, "systemctl")
	runner := &serviceTestRunner{}
	service := preparedServiceState{manager: "systemd", executable: executable, activeTouched: true}
	err = finishSetupWorkflow(context.Background(), transaction, service, runner, 0, runtimeConfig, true)
	if !errors.Is(err, primary) || !strings.Contains(err.Error(), "cannot be rolled back automatically") {
		t.Fatalf("finish error = %v", err)
	}
	assertFileState(t, target, "before\n", 0o640)
	wantCommands := [][]string{{executable.path(), "--user", "stop", "cliproxyapi.service"}}
	if !reflect.DeepEqual(runner.commands, wantCommands) {
		t.Fatalf("restore commands = %q, want %q", runner.commands, wantCommands)
	}
	assertSetupArtifactsAbsent(t, transaction)
}

func TestFinishSetupWorkflowAcceptsMissingJournalOnlyAfterFinalization(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "runtime.json")
	if err := os.WriteFile(target, []byte("before\n"), 0o640); err != nil {
		t.Fatal(err)
	}
	transaction, err := newSetupTransaction(filepath.Join(root, "state"))
	if err != nil {
		t.Fatal(err)
	}
	runtimeConfig := config.Runtime{
		SchemaVersion:   config.SchemaVersion,
		RealClaude:      "/usr/bin/claude",
		ClaudexMode:     "external",
		RealClaudex:     "/usr/bin/claudex",
		ScreenPath:      "/usr/bin/screen",
		CLIProxyService: "none",
	}
	runtimeData, err := config.EncodeRuntime(runtimeConfig)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := transaction.addFile(target, runtimeData, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := transaction.persist(); err != nil {
		t.Fatal(err)
	}

	primary := errors.New("injected post-finalization failure")
	beforeCommitSetupTransaction = func() error {
		if err := transaction.prepareCommittedJournal(); err != nil {
			t.Fatal(err)
		}
		if err := transaction.writeJournal(); err != nil {
			return err
		}
		if err := transaction.finalizeJournal(transaction.journal); err != nil {
			return err
		}
		return primary
	}
	t.Cleanup(func() { beforeCommitSetupTransaction = nil })
	runner := &serviceTestRunner{}
	err = finishSetupWorkflow(context.Background(), transaction, preparedServiceState{manager: "none"}, runner, 0, runtimeConfig, false)
	beforeCommitSetupTransaction = nil
	if err != nil {
		t.Fatalf("missing journal after finalized commit was not accepted: %v", err)
	}
	if len(runner.commands) != 0 {
		t.Fatalf("service state was mutated: %q", runner.commands)
	}
	if transaction.persisted {
		t.Fatal("transaction remained persisted after finalized-state reconciliation")
	}
	if _, err := os.Lstat(transaction.journalPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("journal remains after finalization: %v", err)
	}
	assertFileState(t, target, string(runtimeData), 0o600)
	assertSetupArtifactsAbsent(t, transaction)
}

func TestFinishSetupWorkflowRejectsMissingJournalBeforeFinalization(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "runtime.json")
	if err := os.WriteFile(target, []byte("before\n"), 0o640); err != nil {
		t.Fatal(err)
	}
	transaction, err := newSetupTransaction(filepath.Join(root, "state"))
	if err != nil {
		t.Fatal(err)
	}
	runtimeConfig := config.Runtime{
		SchemaVersion:   config.SchemaVersion,
		RealClaude:      "/usr/bin/claude",
		ClaudexMode:     "external",
		RealClaudex:     "/usr/bin/claudex",
		ScreenPath:      "/usr/bin/screen",
		CLIProxyService: "none",
	}
	runtimeData, err := config.EncodeRuntime(runtimeConfig)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := transaction.addFile(target, runtimeData, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := transaction.persist(); err != nil {
		t.Fatal(err)
	}

	primary := errors.New("injected journal deletion")
	beforeCommitSetupTransaction = func() error {
		if err := transaction.removeJournal(transaction.journal); err != nil {
			return err
		}
		return primary
	}
	t.Cleanup(func() { beforeCommitSetupTransaction = nil })
	runner := &serviceTestRunner{}
	err = finishSetupWorkflow(context.Background(), transaction, preparedServiceState{manager: "none"}, runner, 0, runtimeConfig, false)
	beforeCommitSetupTransaction = nil
	if !errors.Is(err, primary) || !strings.Contains(err.Error(), "artifact") || !strings.Contains(err.Error(), "remains") {
		t.Fatalf("finish error = %v", err)
	}
	if len(runner.commands) != 0 {
		t.Fatalf("service state was mutated: %q", runner.commands)
	}
	if !transaction.persisted {
		t.Fatal("ambiguous transaction was marked complete")
	}
	assertFileState(t, target, string(runtimeData), 0o600)
	entry := transaction.journal.Entries[0]
	for _, artifact := range []string{entry.Backup, entry.Staged} {
		if _, statErr := os.Lstat(artifact); statErr != nil {
			t.Fatalf("artifact %s was not retained: %v", artifact, statErr)
		}
	}
}

func TestFinishSetupWorkflowKeepsCommittedStateWhenFinalizationFails(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "runtime.json")
	if err := os.WriteFile(target, []byte("before\n"), 0o640); err != nil {
		t.Fatal(err)
	}
	transaction, err := newSetupTransaction(filepath.Join(root, "state"))
	if err != nil {
		t.Fatal(err)
	}
	runtimeConfig := config.Runtime{
		SchemaVersion:   config.SchemaVersion,
		RealClaude:      "/usr/bin/claude",
		ClaudexMode:     "external",
		RealClaudex:     "/usr/bin/claudex",
		ScreenPath:      "/usr/bin/screen",
		CLIProxyService: "none",
	}
	runtimeData, err := config.EncodeRuntime(runtimeConfig)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := transaction.addFile(target, runtimeData, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := transaction.persist(); err != nil {
		t.Fatal(err)
	}

	primary := errors.New("injected finalization failure")
	beforeFinalizeSetupTransaction = func() error { return primary }
	t.Cleanup(func() { beforeFinalizeSetupTransaction = nil })
	executable := prepareServiceTestExecutable(t, "systemctl")
	runner := &serviceTestRunner{}
	service := preparedServiceState{manager: "systemd", executable: executable, activeTouched: true}
	err = finishSetupWorkflow(context.Background(), transaction, service, runner, 0, runtimeConfig, true)
	if !errors.Is(err, primary) || strings.Contains(err.Error(), "cannot be rolled back automatically") {
		t.Fatalf("finish error = %v", err)
	}
	if len(runner.commands) != 0 {
		t.Fatalf("committed service state was restored: %q", runner.commands)
	}
	data, err := os.ReadFile(target)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != string(runtimeData) {
		t.Fatalf("committed runtime = %q, want %q", data, runtimeData)
	}
	journal, err := loadSetupJournal(transaction.journalPath)
	if err != nil {
		t.Fatal(err)
	}
	if !journal.Committed {
		t.Fatal("finalization failure lost the durable commit marker")
	}
}

func TestAbortSetupWorkflowReportsOAuthAndRestorationFailure(t *testing.T) {
	executable := prepareServiceTestExecutable(t, "systemctl")
	runner := &serviceTestRunner{runErrs: map[string]error{
		executable.path() + " --user stop cliproxyapi.service": errors.New("restore failed"),
	}}
	service := preparedServiceState{
		manager:       "systemd",
		executable:    executable,
		activeTouched: true,
	}
	primary := errors.New("verification failed")
	err := abortSetupWorkflow(context.Background(), &setupTransaction{}, service, runner, primary, true)
	if !errors.Is(err, primary) || !strings.Contains(err.Error(), "restore failed") || !strings.Contains(err.Error(), "cannot be rolled back automatically") {
		t.Fatalf("abort error = %v", err)
	}
}

func TestAbortSetupWorkflowDoesNotRestoreFilesWhenQuiescenceFails(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "runtime.json")
	if err := os.WriteFile(target, []byte("before\n"), 0o640); err != nil {
		t.Fatal(err)
	}
	transaction, err := newSetupTransaction(filepath.Join(root, "state"))
	if err != nil {
		t.Fatal(err)
	}
	index, err := transaction.addFile(target, []byte("after\n"), 0o600)
	if err != nil {
		t.Fatal(err)
	}
	if err := transaction.persist(); err != nil {
		t.Fatal(err)
	}
	if err := transaction.applyFile(index); err != nil {
		t.Fatal(err)
	}

	quiesceErr := errors.New("stop failed")
	executable := prepareServiceTestExecutable(t, "systemctl")
	runner := &serviceTestRunner{runErrs: map[string]error{
		executable.path() + " --user stop cliproxyapi.service": quiesceErr,
	}}
	service := preparedServiceState{
		manager:       "systemd",
		executable:    executable,
		activeTouched: true,
	}
	primary := errors.New("verification failed")
	err = abortSetupWorkflow(context.Background(), transaction, service, runner, primary, false)
	if !errors.Is(err, primary) || !errors.Is(err, quiesceErr) {
		t.Fatalf("abort error = %v", err)
	}
	assertFileState(t, target, "after\n", 0o600)
	if _, err := loadSetupJournal(transaction.journalPath); err != nil {
		t.Fatalf("setup journal was not retained: %v", err)
	}
}

func TestRollbackRecoveredSetupJournalRestoresServiceAndReportsOAuth(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "target")
	if err := os.WriteFile(target, []byte("before\n"), 0o640); err != nil {
		t.Fatal(err)
	}
	transaction, err := newSetupTransaction(filepath.Join(root, "state"))
	if err != nil {
		t.Fatal(err)
	}
	index, err := transaction.addFile(target, []byte("after\n"), 0o600)
	if err != nil {
		t.Fatal(err)
	}
	executable := prepareServiceTestExecutable(t, "systemctl")
	if err := transaction.setServiceState(preparedServiceState{
		manager:    "systemd",
		executable: executable,
	}); err != nil {
		t.Fatal(err)
	}
	transaction.journal.ServiceActiveTouched = true
	transaction.journal.OAuthAttempted = true
	transaction.journal.OAuthCompleted = true
	if err := transaction.persist(); err != nil {
		t.Fatal(err)
	}
	if err := transaction.applyFile(index); err != nil {
		t.Fatal(err)
	}
	runner := &serviceTestRunner{}
	err = rollbackRecoveredSetupJournal(context.Background(), transaction.journalPath, transaction.journal, runner)
	if err == nil || !strings.Contains(err.Error(), "cannot be rolled back automatically") {
		t.Fatalf("recovery error = %v", err)
	}
	assertFileState(t, target, "before\n", 0o640)
	wantCommands := [][]string{{executable.path(), "--user", "stop", "cliproxyapi.service"}}
	if !reflect.DeepEqual(runner.commands, wantCommands) {
		t.Fatalf("restore commands = %q, want %q", runner.commands, wantCommands)
	}
	assertSetupArtifactsAbsent(t, transaction)
}

func TestServiceStateFromJournalUsesPersistedExecutableIdentity(t *testing.T) {
	executable := prepareServiceTestExecutable(t, "systemctl")
	otherDir := t.TempDir()
	other := filepath.Join(otherDir, "systemctl")
	if err := os.WriteFile(other, []byte("#!/bin/sh\nexit 0\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", otherDir)
	state, err := serviceStateFromJournal(setupJournal{
		ServiceManager:          "systemd",
		ServiceExecutable:       executable.path(),
		ServiceExecutableDigest: executable.digest,
		ServiceActiveTouched:    true,
	})
	if err != nil {
		t.Fatal(err)
	}
	runner := &serviceTestRunner{}
	if err := state.quiesce(context.Background(), runner); err != nil {
		t.Fatal(err)
	}
	want := [][]string{{executable.path(), "--user", "stop", "cliproxyapi.service"}}
	if !reflect.DeepEqual(runner.commands, want) {
		t.Fatalf("recovery commands = %q, want %q", runner.commands, want)
	}
}

func TestServiceStateFromJournalRejectsChangedExecutable(t *testing.T) {
	executable := prepareServiceTestExecutable(t, "systemctl")
	if err := os.WriteFile(executable.path(), []byte("#!/bin/sh\nexit 1\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	_, err := serviceStateFromJournal(setupJournal{
		ServiceManager:          "systemd",
		ServiceExecutable:       executable.path(),
		ServiceExecutableDigest: executable.digest,
	})
	if err == nil || !strings.Contains(err.Error(), "changed since setup preparation") {
		t.Fatalf("recovery error = %v", err)
	}
}

func TestRollbackRecoveredSetupJournalReportsAttemptedOAuth(t *testing.T) {
	root := t.TempDir()
	transaction, err := newSetupTransaction(filepath.Join(root, "state"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := transaction.addFile(filepath.Join(root, "target"), []byte("after\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	transaction.journal.ServiceManager = "none"
	transaction.journal.OAuthAttempted = true
	if err := transaction.persist(); err != nil {
		t.Fatal(err)
	}
	err = rollbackRecoveredSetupJournal(context.Background(), transaction.journalPath, transaction.journal, &serviceTestRunner{})
	if err == nil || !strings.Contains(err.Error(), "authentication was attempted") || strings.Contains(err.Error(), "authentication completed") {
		t.Fatalf("recovery error = %v", err)
	}
	assertSetupArtifactsAbsent(t, transaction)
}

func TestRollbackRecoveredSetupJournalRestoresFilesBeforeServiceRestart(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "target")
	if err := os.WriteFile(target, []byte("before\n"), 0o640); err != nil {
		t.Fatal(err)
	}
	transaction, err := newSetupTransaction(filepath.Join(root, "state"))
	if err != nil {
		t.Fatal(err)
	}
	index, err := transaction.addFile(target, []byte("after\n"), 0o600)
	if err != nil {
		t.Fatal(err)
	}
	executable := prepareServiceTestExecutable(t, "systemctl")
	if err := transaction.setServiceState(preparedServiceState{
		manager:    "systemd",
		executable: executable,
		active:     true,
	}); err != nil {
		t.Fatal(err)
	}
	transaction.journal.ServiceActiveTouched = true
	if err := transaction.persist(); err != nil {
		t.Fatal(err)
	}
	if err := transaction.applyFile(index); err != nil {
		t.Fatal(err)
	}

	runner := &serviceTestRunner{
		onRun: func(_ context.Context, command []string) error {
			if strings.Join(command, " ") != executable.path()+" --user restart cliproxyapi.service" {
				return nil
			}
			data, err := os.ReadFile(target)
			if err != nil {
				return err
			}
			if string(data) != "before\n" {
				return fmt.Errorf("service restart observed target %q", data)
			}
			return nil
		},
	}
	if err := rollbackRecoveredSetupJournal(context.Background(), transaction.journalPath, transaction.journal, runner); err != nil {
		t.Fatal(err)
	}
	wantCommands := [][]string{
		{executable.path(), "--user", "stop", "cliproxyapi.service"},
		{executable.path(), "--user", "restart", "cliproxyapi.service"},
	}
	if !reflect.DeepEqual(runner.commands, wantCommands) {
		t.Fatalf("restore commands = %q, want %q", runner.commands, wantCommands)
	}
	assertFileState(t, target, "before\n", 0o640)
	assertSetupArtifactsAbsent(t, transaction)
}

func TestRollbackRecoveredSetupJournalRejectsMutationAfterServiceRestore(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "target")
	if err := os.WriteFile(target, []byte("before\n"), 0o640); err != nil {
		t.Fatal(err)
	}
	transaction, err := newSetupTransaction(filepath.Join(root, "state"))
	if err != nil {
		t.Fatal(err)
	}
	index, err := transaction.addFile(target, []byte("after\n"), 0o600)
	if err != nil {
		t.Fatal(err)
	}
	executable := prepareServiceTestExecutable(t, "systemctl")
	if err := transaction.setServiceState(preparedServiceState{
		manager:    "systemd",
		executable: executable,
		active:     true,
	}); err != nil {
		t.Fatal(err)
	}
	transaction.journal.ServiceActiveTouched = true
	if err := transaction.persist(); err != nil {
		t.Fatal(err)
	}
	if err := transaction.applyFile(index); err != nil {
		t.Fatal(err)
	}

	runner := &serviceTestRunner{
		onRun: func(_ context.Context, command []string) error {
			if strings.Join(command, " ") != executable.path()+" --user restart cliproxyapi.service" {
				return nil
			}
			return os.WriteFile(target, []byte("external\n"), 0o640)
		},
	}
	err = rollbackRecoveredSetupJournal(context.Background(), transaction.journalPath, transaction.journal, runner)
	if err == nil || !strings.Contains(err.Error(), "does not match the original state") {
		t.Fatalf("recovery error = %v", err)
	}
	assertFileState(t, target, "external\n", 0o640)
	if _, loadErr := loadSetupJournal(transaction.journalPath); loadErr != nil {
		t.Fatalf("setup journal was not retained: %v", loadErr)
	}
}

func TestRollbackRecoveredSetupJournalRetainsJournalUntilServiceRestoreSucceeds(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "target")
	if err := os.WriteFile(target, []byte("before\n"), 0o640); err != nil {
		t.Fatal(err)
	}
	transaction, err := newSetupTransaction(filepath.Join(root, "state"))
	if err != nil {
		t.Fatal(err)
	}
	index, err := transaction.addFile(target, []byte("after\n"), 0o600)
	if err != nil {
		t.Fatal(err)
	}
	executable := prepareServiceTestExecutable(t, "systemctl")
	if err := transaction.setServiceState(preparedServiceState{
		manager:    "systemd",
		executable: executable,
		active:     true,
	}); err != nil {
		t.Fatal(err)
	}
	transaction.journal.ServiceActiveTouched = true
	transaction.journal.ServiceScheduledTouched = true
	if err := transaction.persist(); err != nil {
		t.Fatal(err)
	}
	if err := transaction.applyFile(index); err != nil {
		t.Fatal(err)
	}

	restoreErr := errors.New("disable failed")
	firstRunner := &serviceTestRunner{runErrs: map[string]error{
		executable.path() + " --user disable cliproxyapi.service": restoreErr,
	}}
	err = rollbackRecoveredSetupJournal(context.Background(), transaction.journalPath, transaction.journal, firstRunner)
	if !errors.Is(err, restoreErr) {
		t.Fatalf("first recovery error = %v", err)
	}
	wantFirstCommands := [][]string{
		{executable.path(), "--user", "stop", "cliproxyapi.service"},
		{executable.path(), "--user", "disable", "cliproxyapi.service"},
	}
	if !reflect.DeepEqual(firstRunner.commands, wantFirstCommands) {
		t.Fatalf("first recovery commands = %q, want %q", firstRunner.commands, wantFirstCommands)
	}
	assertFileState(t, target, "before\n", 0o640)
	if _, err := loadSetupJournal(transaction.journalPath); err != nil {
		t.Fatalf("setup journal was not retained: %v", err)
	}

	secondRunner := &serviceTestRunner{}
	if err := rollbackRecoveredSetupJournal(context.Background(), transaction.journalPath, transaction.journal, secondRunner); err != nil {
		t.Fatal(err)
	}
	wantCommands := [][]string{
		{executable.path(), "--user", "stop", "cliproxyapi.service"},
		{executable.path(), "--user", "disable", "cliproxyapi.service"},
		{executable.path(), "--user", "restart", "cliproxyapi.service"},
	}
	if !reflect.DeepEqual(secondRunner.commands, wantCommands) {
		t.Fatalf("retry commands = %q, want %q", secondRunner.commands, wantCommands)
	}
	assertSetupArtifactsAbsent(t, transaction)
}

func TestAbortSetupWorkflowUsesIndependentCleanupContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	var observedErr error
	runner := &serviceTestRunner{
		onRun: func(commandCtx context.Context, _ []string) error {
			observedErr = commandCtx.Err()
			return nil
		},
	}
	service := preparedServiceState{
		manager:       "systemd",
		executable:    prepareServiceTestExecutable(t, "systemctl"),
		activeTouched: true,
	}
	primary := errors.New("verification failed")
	err := abortSetupWorkflow(ctx, &setupTransaction{}, service, runner, primary, false)
	if !errors.Is(err, primary) {
		t.Fatalf("abort error = %v", err)
	}
	if observedErr != nil {
		t.Fatalf("cleanup context error = %v", observedErr)
	}
}

func TestParseBrewServiceState(t *testing.T) {
	for _, test := range []struct {
		name          string
		data          string
		wantActive    bool
		wantScheduled bool
	}{
		{
			name:          "running unscheduled",
			data:          `[{"name":"other","status":"started"},{"name":"cliproxyapi","status":"started","running":true,"scheduled":false}]`,
			wantActive:    true,
			wantScheduled: false,
		},
		{
			name:          "started defaults scheduled",
			data:          `[{"name":"cliproxyapi","status":"started"}]`,
			wantActive:    true,
			wantScheduled: true,
		},
		{
			name:          "scheduled inactive",
			data:          `[{"name":"cliproxyapi","status":"scheduled","running":false}]`,
			wantActive:    false,
			wantScheduled: true,
		},
		{
			name: "stopped unscheduled",
			data: `[{"name":"cliproxyapi","status":"none"}]`,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			active, scheduled, err := parseBrewServiceState([]byte(test.data))
			if err != nil {
				t.Fatal(err)
			}
			if active != test.wantActive || scheduled != test.wantScheduled {
				t.Fatalf("state = active:%v scheduled:%v, want active:%v scheduled:%v", active, scheduled, test.wantActive, test.wantScheduled)
			}
		})
	}
}
