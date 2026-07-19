package setup

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ctrl-alt-raccoon/sclaude/internal/config"
	"github.com/ctrl-alt-raccoon/sclaude/internal/fssecure"
)

func TestAcquireSetupAdmissionLockRejectsStateRootReplacement(t *testing.T) {
	root := t.TempDir()
	stateRoot := filepath.Join(root, "state")
	if err := os.Mkdir(stateRoot, 0o700); err != nil {
		t.Fatal(err)
	}
	original := filepath.Join(root, "original-state")
	afterSetupAdmissionLock = func(path string) error {
		if path != stateRoot {
			t.Fatalf("locked path = %q, want %q", path, stateRoot)
		}
		if err := os.Rename(stateRoot, original); err != nil {
			return err
		}
		return os.Mkdir(stateRoot, 0o700)
	}
	t.Cleanup(func() { afterSetupAdmissionLock = nil })

	lock, err := acquireSetupAdmissionLock(stateRoot, true)
	if lock != nil {
		_ = lock.release()
	}
	if err == nil || !strings.Contains(err.Error(), "changed while acquiring") {
		t.Fatalf("admission error = %v", err)
	}
}

func TestSetupAdmissionLockRejectsStateRootReplacementAfterAcquisition(t *testing.T) {
	root := t.TempDir()
	stateRoot := filepath.Join(root, "state")
	if err := os.Mkdir(stateRoot, 0o700); err != nil {
		t.Fatal(err)
	}
	lock, err := acquireSetupAdmissionLock(stateRoot, false)
	if err != nil {
		t.Fatal(err)
	}
	defer lock.release()
	original := filepath.Join(root, "original-state")
	if err := os.Rename(stateRoot, original); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(stateRoot, 0o700); err != nil {
		t.Fatal(err)
	}

	if _, err := lock.loadJournal(); err == nil || !strings.Contains(err.Error(), "changed while holding") {
		t.Fatalf("load journal error = %v", err)
	}
	journal := setupJournal{SchemaVersion: setupJournalSchema, Token: strings.Repeat("a", 32)}
	if err := lock.writeJournal(journal); err == nil || !strings.Contains(err.Error(), "changed while holding") {
		t.Fatalf("write journal error = %v", err)
	}
	if err := lock.removeJournal(journal); err == nil || !strings.Contains(err.Error(), "changed while holding") {
		t.Fatalf("remove journal error = %v", err)
	}
	if _, err := newSetupTransactionLocked(lock); err == nil || !strings.Contains(err.Error(), "changed while holding") {
		t.Fatalf("new transaction error = %v", err)
	}
	if _, statErr := os.Lstat(filepath.Join(stateRoot, "setup-transaction.json")); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("replacement state root was mutated: %v", statErr)
	}
}

func TestAcquireSetupAdmissionLockRejectsNonprivateStateRoot(t *testing.T) {
	for _, mode := range []os.FileMode{0o755, 0o777} {
		t.Run(mode.String(), func(t *testing.T) {
			stateRoot := filepath.Join(t.TempDir(), "state")
			if err := os.Mkdir(stateRoot, mode); err != nil {
				t.Fatal(err)
			}
			if err := os.Chmod(stateRoot, mode); err != nil {
				t.Fatal(err)
			}
			lock, err := acquireSetupAdmissionLock(stateRoot, true)
			if lock != nil {
				_ = lock.release()
			}
			if err == nil || !strings.Contains(err.Error(), "mode 0700") {
				t.Fatalf("admission error = %v", err)
			}
		})
	}
}

func TestNewSetupTransactionRejectsExistingJournal(t *testing.T) {
	stateRoot := filepath.Join(t.TempDir(), "state")
	journalPath := setupJournalPath(stateRoot)
	if err := os.MkdirAll(filepath.Dir(journalPath), 0o700); err != nil {
		t.Fatal(err)
	}
	original := []byte("existing journal\n")
	if err := os.WriteFile(journalPath, original, 0o600); err != nil {
		t.Fatal(err)
	}

	_, err := newSetupTransaction(stateRoot)
	if err == nil || !strings.Contains(err.Error(), "journal already exists") {
		t.Fatalf("new setup transaction error = %v", err)
	}
	got, readErr := os.ReadFile(journalPath)
	if readErr != nil {
		t.Fatal(readErr)
	}
	if string(got) != string(original) {
		t.Fatalf("journal changed to %q", got)
	}
}

func TestPersistRejectsJournalInsertedAfterPreparation(t *testing.T) {
	root := t.TempDir()
	stateRoot := filepath.Join(root, "state")
	transaction, err := newSetupTransaction(stateRoot)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := transaction.addFile(filepath.Join(root, "target"), []byte("after\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(stateRoot, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(transaction.journalPath, []byte("external journal\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	err = transaction.persist()
	if err == nil {
		t.Fatal("persist replaced a journal inserted after preparation")
	}
	assertFileState(t, transaction.journalPath, "external journal\n", 0o600)
	if transaction.persisted {
		t.Fatal("failed transaction was marked persisted")
	}
}

func TestApplyRejectsStagedArtifactInsertedAfterPreparation(t *testing.T) {
	root := t.TempDir()
	transaction, err := newSetupTransaction(filepath.Join(root, "state"))
	if err != nil {
		t.Fatal(err)
	}
	index, err := transaction.addFile(filepath.Join(root, "target"), []byte("after\n"), 0o600)
	if err != nil {
		t.Fatal(err)
	}
	if err := transaction.persist(); err != nil {
		t.Fatal(err)
	}
	staged := transaction.journal.Entries[index].Staged
	if err := os.WriteFile(staged, []byte("external artifact\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	err = transaction.applyFile(index)
	if err == nil || !strings.Contains(err.Error(), "stage") {
		t.Fatalf("apply error = %v", err)
	}
	assertFileState(t, staged, "external artifact\n", 0o600)
	if _, statErr := os.Lstat(transaction.journal.Entries[index].Path); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("target was published after staged artifact collision: %v", statErr)
	}
}

func TestSetupTransactionCommitsFiles(t *testing.T) {
	root := t.TempDir()
	stateRoot := filepath.Join(root, "state")
	existing := filepath.Join(root, "config", "existing.json")
	created := filepath.Join(root, "config", "created.json")
	if err := os.MkdirAll(filepath.Dir(existing), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(existing, []byte("old\n"), 0o640); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(existing, 0o640); err != nil {
		t.Fatal(err)
	}

	transaction, err := newSetupTransaction(stateRoot)
	if err != nil {
		t.Fatal(err)
	}
	first, err := transaction.addFile(existing, []byte("new\n"), 0o600)
	if err != nil {
		t.Fatal(err)
	}
	second, err := transaction.addFile(created, []byte("created\n"), 0o600)
	if err != nil {
		t.Fatal(err)
	}
	if err := transaction.persist(); err != nil {
		t.Fatal(err)
	}
	if err := transaction.applyFile(first); err != nil {
		t.Fatal(err)
	}
	if err := transaction.applyFile(second); err != nil {
		t.Fatal(err)
	}
	if err := transaction.commit(); err != nil {
		t.Fatal(err)
	}

	assertFileState(t, existing, "new\n", 0o600)
	assertFileState(t, created, "created\n", 0o600)
	assertSetupArtifactsAbsent(t, transaction)
}

func TestSetupTransactionAbortRollsBackInReverse(t *testing.T) {
	root := t.TempDir()
	existing := filepath.Join(root, "existing")
	created := filepath.Join(root, "created")
	if err := os.WriteFile(existing, []byte("original\n"), 0o640); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(existing, 0o640); err != nil {
		t.Fatal(err)
	}

	transaction, err := newSetupTransaction(filepath.Join(root, "state"))
	if err != nil {
		t.Fatal(err)
	}
	first, err := transaction.addFile(existing, []byte("updated\n"), 0o600)
	if err != nil {
		t.Fatal(err)
	}
	second, err := transaction.addFile(created, []byte("created\n"), 0o600)
	if err != nil {
		t.Fatal(err)
	}
	if err := transaction.persist(); err != nil {
		t.Fatal(err)
	}
	if err := transaction.applyFile(first); err != nil {
		t.Fatal(err)
	}
	if err := transaction.applyFile(second); err != nil {
		t.Fatal(err)
	}
	primary := errors.New("injected failure")
	if err := transaction.abort(primary); !errors.Is(err, primary) {
		t.Fatalf("abort error = %v", err)
	}

	assertFileState(t, existing, "original\n", 0o640)
	if _, err := os.Lstat(created); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("created target remains after rollback: %v", err)
	}
	assertSetupArtifactsAbsent(t, transaction)
}

func TestApplyRuntimeSecuresExistingParent(t *testing.T) {
	root := t.TempDir()
	parent := filepath.Join(root, "config")
	if err := os.Mkdir(parent, 0o755); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(parent, "runtime.json")
	runtimeConfig := testExternalRuntimeConfig()
	data, err := config.EncodeRuntime(runtimeConfig)
	if err != nil {
		t.Fatal(err)
	}
	transaction, err := newSetupTransaction(filepath.Join(root, "state"))
	if err != nil {
		t.Fatal(err)
	}
	index, err := transaction.addFile(target, data, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	if err := transaction.persist(); err != nil {
		t.Fatal(err)
	}
	if err := transaction.applyRuntime(index, runtimeConfig); err != nil {
		t.Fatal(err)
	}
	assertDirectoryMode(t, parent, 0o700)
}

func TestApplyRuntimeNoopStillSecuresParent(t *testing.T) {
	root := t.TempDir()
	parent := filepath.Join(root, "config")
	if err := os.Mkdir(parent, 0o755); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(parent, "runtime.json")
	runtimeConfig := testExternalRuntimeConfig()
	data, err := config.EncodeRuntime(runtimeConfig)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(target, data, 0o600); err != nil {
		t.Fatal(err)
	}
	transaction, err := newSetupTransaction(filepath.Join(root, "state"))
	if err != nil {
		t.Fatal(err)
	}
	index, err := transaction.addFile(target, data, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	if err := transaction.persist(); err != nil {
		t.Fatal(err)
	}
	if err := transaction.applyRuntime(index, runtimeConfig); err != nil {
		t.Fatal(err)
	}
	assertDirectoryMode(t, parent, 0o700)
	assertFileState(t, target, string(data), 0o600)
}

func TestApplyFilePreservesExistingParentMode(t *testing.T) {
	root := t.TempDir()
	parent := filepath.Join(root, "profile-home")
	if err := os.Mkdir(parent, 0o755); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(parent, ".profile")
	transaction, err := newSetupTransaction(filepath.Join(root, "state"))
	if err != nil {
		t.Fatal(err)
	}
	index, err := transaction.addFile(target, []byte("managed\n"), 0o600)
	if err != nil {
		t.Fatal(err)
	}
	if err := transaction.persist(); err != nil {
		t.Fatal(err)
	}
	if err := transaction.applyFile(index); err != nil {
		t.Fatal(err)
	}
	assertDirectoryMode(t, parent, 0o755)
}

func TestRecoverSetupTransactionRollsBackUncommittedJournal(t *testing.T) {
	root := t.TempDir()
	stateRoot := filepath.Join(root, "state")
	target := filepath.Join(root, "target")
	if err := os.WriteFile(target, []byte("before\n"), 0o640); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(target, 0o640); err != nil {
		t.Fatal(err)
	}

	transaction, err := newSetupTransaction(stateRoot)
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

	if err := recoverSetupTransaction(stateRoot); err != nil {
		t.Fatal(err)
	}
	assertFileState(t, target, "before\n", 0o640)
	assertSetupArtifactsAbsent(t, transaction)
}

func TestLoadRuntimeRejectsUncommittedSetupTransaction(t *testing.T) {
	root := t.TempDir()
	paths := config.Paths{
		ConfigFile: filepath.Join(root, "runtime.json"),
		StateRoot:  filepath.Join(root, "state"),
	}
	before := config.Runtime{
		SchemaVersion:   config.SchemaVersion,
		RealClaude:      "/usr/bin/claude",
		ClaudexMode:     "external",
		RealClaudex:     "/usr/bin/claudex",
		ScreenPath:      "/usr/bin/screen",
		CLIProxyService: "none",
	}
	if err := config.Save(paths.ConfigFile, before); err != nil {
		t.Fatal(err)
	}
	after := before
	after.RealClaude = "/opt/claude"
	afterData, err := config.EncodeRuntime(after)
	if err != nil {
		t.Fatal(err)
	}
	transaction, err := newSetupTransaction(paths.StateRoot)
	if err != nil {
		t.Fatal(err)
	}
	index, err := transaction.addFile(paths.ConfigFile, afterData, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	if err := transaction.persist(); err != nil {
		t.Fatal(err)
	}
	if err := transaction.applyRuntime(index, after); err != nil {
		t.Fatal(err)
	}

	_, err = LoadRuntime(paths)
	if err == nil || !strings.Contains(err.Error(), "interrupted setup transaction requires recovery") {
		t.Fatalf("load error = %v", err)
	}
	loaded, err := config.Load(paths.ConfigFile)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.RealClaude != after.RealClaude {
		t.Fatalf("runtime was mutated during rejection: %+v", loaded)
	}
	if _, err := loadSetupJournal(transaction.journalPath); err != nil {
		t.Fatalf("setup journal was not retained: %v", err)
	}
}

func TestLoadRuntimeFinalizesCommittedSetupTransaction(t *testing.T) {
	root := t.TempDir()
	paths := config.Paths{
		ConfigFile: filepath.Join(root, "runtime.json"),
		StateRoot:  filepath.Join(root, "state"),
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
	transaction, err := newSetupTransaction(paths.StateRoot)
	if err != nil {
		t.Fatal(err)
	}
	index, err := transaction.addFile(paths.ConfigFile, runtimeData, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	if err := transaction.persist(); err != nil {
		t.Fatal(err)
	}
	if err := transaction.applyRuntime(index, runtimeConfig); err != nil {
		t.Fatal(err)
	}
	if err := transaction.prepareCommittedJournal(); err != nil {
		t.Fatal(err)
	}
	if err := writeJSONPrivate(transaction.journalPath, transaction.journal); err != nil {
		t.Fatal(err)
	}

	loaded, err := LoadRuntime(paths)
	if err != nil {
		t.Fatal(err)
	}
	if loaded != runtimeConfig {
		t.Fatalf("runtime = %+v, want %+v", loaded, runtimeConfig)
	}
	assertSetupArtifactsAbsent(t, transaction)
}

func TestRecoverSetupTransactionFinalizesCommittedJournal(t *testing.T) {
	root := t.TempDir()
	stateRoot := filepath.Join(root, "state")
	target := filepath.Join(root, "target")
	if err := os.WriteFile(target, []byte("before\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	transaction, err := newSetupTransaction(stateRoot)
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
	if err := transaction.prepareCommittedJournal(); err != nil {
		t.Fatal(err)
	}
	if err := writeJSONPrivate(transaction.journalPath, transaction.journal); err != nil {
		t.Fatal(err)
	}

	if err := recoverSetupTransaction(stateRoot); err != nil {
		t.Fatal(err)
	}
	assertFileState(t, target, "after\n", 0o600)
	assertSetupArtifactsAbsent(t, transaction)
}

func TestRecoverCommittedSetupTransactionAfterArtifactCleanup(t *testing.T) {
	root := t.TempDir()
	stateRoot := filepath.Join(root, "state")
	target := filepath.Join(root, "target")
	if err := os.WriteFile(target, []byte("before\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	transaction, err := newSetupTransaction(stateRoot)
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
	if err := transaction.prepareCommittedJournal(); err != nil {
		t.Fatal(err)
	}
	if err := transaction.writeJournal(); err != nil {
		t.Fatal(err)
	}
	for _, artifact := range setupEntryArtifacts(
		transaction.journal.Entries[index],
	) {
		if err := os.Remove(artifact); err != nil {
			t.Fatal(err)
		}
	}

	if err := recoverSetupTransaction(stateRoot); err != nil {
		t.Fatal(err)
	}
	assertFileState(t, target, "after\n", 0o600)
	assertSetupArtifactsAbsent(t, transaction)
}

func TestRecoverCommittedSetupTransactionAfterPartialArtifactCleanup(t *testing.T) {
	root := t.TempDir()
	stateRoot := filepath.Join(root, "state")
	targets := []string{
		filepath.Join(root, "first"),
		filepath.Join(root, "second"),
	}
	for _, target := range targets {
		if err := os.WriteFile(target, []byte("before\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	transaction, err := newSetupTransaction(stateRoot)
	if err != nil {
		t.Fatal(err)
	}
	for _, target := range targets {
		index, err := transaction.addFile(
			target,
			[]byte("after\n"),
			0o600,
		)
		if err != nil {
			t.Fatal(err)
		}
		if index != len(transaction.files)-1 {
			t.Fatalf("setup file index = %d", index)
		}
	}
	if err := transaction.persist(); err != nil {
		t.Fatal(err)
	}
	for index := range transaction.files {
		if err := transaction.applyFile(index); err != nil {
			t.Fatal(err)
		}
	}
	if err := transaction.prepareCommittedJournal(); err != nil {
		t.Fatal(err)
	}
	if err := transaction.writeJournal(); err != nil {
		t.Fatal(err)
	}
	for _, artifact := range setupEntryArtifacts(
		transaction.journal.Entries[0],
	) {
		if err := os.Remove(artifact); err != nil {
			t.Fatal(err)
		}
	}

	if err := recoverSetupTransaction(stateRoot); err != nil {
		t.Fatal(err)
	}
	for _, target := range targets {
		assertFileState(t, target, "after\n", 0o600)
	}
	assertSetupArtifactsAbsent(t, transaction)
}

func TestRecoverSetupTransactionRollsBackUnderInstallLock(t *testing.T) {
	root := t.TempDir()
	stateRoot := filepath.Join(root, "state")
	lock := filepath.Join(root, "install-lock")
	target := filepath.Join(root, "profile")
	if err := os.WriteFile(target, []byte("before\n"), 0o640); err != nil {
		t.Fatal(err)
	}

	transaction, err := newSetupTransaction(stateRoot)
	if err != nil {
		t.Fatal(err)
	}
	if err := transaction.setInstallLock(lock); err != nil {
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

	if err := recoverSetupTransaction(stateRoot); err != nil {
		t.Fatal(err)
	}
	assertFileState(t, target, "before\n", 0o640)
	assertDirectoryMode(t, lock, 0o700)
	assertSetupArtifactsAbsent(t, transaction)
}

func TestRecoverSetupTransactionFinalizesUnderInstallLock(t *testing.T) {
	root := t.TempDir()
	stateRoot := filepath.Join(root, "state")
	lock := filepath.Join(root, "install-lock")
	target := filepath.Join(root, "profile")
	if err := os.WriteFile(target, []byte("before\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	transaction, err := newSetupTransaction(stateRoot)
	if err != nil {
		t.Fatal(err)
	}
	if err := transaction.setInstallLock(lock); err != nil {
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
	if err := transaction.prepareCommittedJournal(); err != nil {
		t.Fatal(err)
	}
	if err := writeJSONPrivate(transaction.journalPath, transaction.journal); err != nil {
		t.Fatal(err)
	}

	if err := recoverSetupTransaction(stateRoot); err != nil {
		t.Fatal(err)
	}
	assertFileState(t, target, "after\n", 0o600)
	assertDirectoryMode(t, lock, 0o700)
	assertSetupArtifactsAbsent(t, transaction)
}

func TestFinalizeSetupJournalRejectsParentReplacement(t *testing.T) {
	root := t.TempDir()
	parent := filepath.Join(root, "config")
	if err := os.Mkdir(parent, 0o700); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(parent, "target")
	if err := os.WriteFile(target, []byte("before\n"), 0o600); err != nil {
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
	if err := transaction.prepareCommittedJournal(); err != nil {
		t.Fatal(err)
	}
	if err := transaction.writeJournal(); err != nil {
		t.Fatal(err)
	}
	originalParent := filepath.Join(root, "original-config")
	beforeFinalizeSetupTransaction = func() error {
		if err := os.Rename(parent, originalParent); err != nil {
			return err
		}
		return os.Mkdir(parent, 0o700)
	}
	t.Cleanup(func() { beforeFinalizeSetupTransaction = nil })

	err = transaction.finalizeJournal(transaction.journal)
	beforeFinalizeSetupTransaction = nil
	if err == nil || !strings.Contains(err.Error(), "parent") || !strings.Contains(err.Error(), "changed during finalization") {
		t.Fatalf("finalization error = %v", err)
	}
	entry := transaction.journal.Entries[0]
	assertFileState(t, filepath.Join(originalParent, "target"), "after\n", 0o600)
	for _, artifact := range []string{entry.Backup, entry.Staged} {
		if _, statErr := os.Lstat(filepath.Join(originalParent, filepath.Base(artifact))); statErr != nil {
			t.Fatalf("original artifact was removed after refused finalization: %v", statErr)
		}
	}
	if _, statErr := os.Lstat(transaction.journalPath); statErr != nil {
		t.Fatalf("journal was removed after refused finalization: %v", statErr)
	}
	if _, statErr := os.Lstat(filepath.Join(parent, "target")); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("replacement parent was mutated: %v", statErr)
	}
}

func TestFinalizeSetupJournalRejectsTargetReplacement(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "target")
	if err := os.WriteFile(target, []byte("before\n"), 0o600); err != nil {
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
	if err := transaction.prepareCommittedJournal(); err != nil {
		t.Fatal(err)
	}
	if err := transaction.writeJournal(); err != nil {
		t.Fatal(err)
	}
	beforeFinalizeSetupTransaction = func() error {
		replacement := filepath.Join(root, "replacement")
		if err := os.WriteFile(replacement, []byte("external\n"), 0o600); err != nil {
			return err
		}
		return os.Rename(replacement, target)
	}
	t.Cleanup(func() { beforeFinalizeSetupTransaction = nil })

	err = transaction.finalizeJournal(transaction.journal)
	beforeFinalizeSetupTransaction = nil
	if err == nil || !strings.Contains(err.Error(), "target") || !strings.Contains(err.Error(), "changed during finalization") {
		t.Fatalf("finalization error = %v", err)
	}
	assertFileState(t, target, "external\n", 0o600)
	entry := transaction.journal.Entries[0]
	for _, artifact := range []string{entry.Backup, entry.Staged} {
		if _, statErr := os.Lstat(artifact); statErr != nil {
			t.Fatalf("artifact was removed after refused finalization: %v", statErr)
		}
	}
	if _, statErr := os.Lstat(transaction.journalPath); statErr != nil {
		t.Fatalf("journal was removed after refused finalization: %v", statErr)
	}
}

func TestRecoverSetupTransactionRefusesUnsafeInstallLock(t *testing.T) {
	root := t.TempDir()
	stateRoot := filepath.Join(root, "state")
	lockTarget := filepath.Join(root, "lock-target")
	lock := filepath.Join(root, "install-lock")
	target := filepath.Join(root, "profile")
	if err := os.Mkdir(lockTarget, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("lock-target", lock); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(target, []byte("before\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	transaction, err := newSetupTransaction(stateRoot)
	if err != nil {
		t.Fatal(err)
	}
	if err := transaction.setInstallLock(lock); err != nil {
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

	err = recoverSetupTransaction(stateRoot)
	if err == nil || !strings.Contains(err.Error(), "install lock path must be a directory, not a symlink") {
		t.Fatalf("recovery error = %v", err)
	}
	assertFileState(t, target, "after\n", 0o600)
	if _, statErr := os.Lstat(transaction.journalPath); statErr != nil {
		t.Fatalf("journal was removed after refused recovery: %v", statErr)
	}
}

func TestRecoverSetupTransactionRefusesExternalTargetChange(t *testing.T) {
	root := t.TempDir()
	stateRoot := filepath.Join(root, "state")
	target := filepath.Join(root, "target")
	if err := os.WriteFile(target, []byte("before\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	transaction, err := newSetupTransaction(stateRoot)
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
	if err := os.WriteFile(target, []byte("external\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	err = recoverSetupTransaction(stateRoot)
	if err == nil || !strings.Contains(err.Error(), "changed outside") {
		t.Fatalf("recovery error = %v", err)
	}
	assertFileState(t, target, "external\n", 0o600)
	if _, err := os.Lstat(transaction.journalPath); err != nil {
		t.Fatalf("journal was removed after refused recovery: %v", err)
	}
}

func TestLoadSetupJournalRejectsMalformedDocuments(t *testing.T) {
	root := t.TempDir()
	token := strings.Repeat("a", 32)
	target := filepath.Join(root, "target")
	base := setupJournal{
		SchemaVersion: setupJournalSchema,
		Token:         token,
		Entries: []setupJournalEntry{{
			Path:        target,
			Backup:      setupBackupPath(target, token),
			Staged:      setupStagedPath(target, token),
			Replacement: setupReplacementPath(target, token),
			AfterMode:   0o600,
			AfterDigest: hexDigest([]byte("after")),
		}},
	}

	for _, test := range []struct {
		name string
		data func(t *testing.T) []byte
		want string
	}{
		{
			name: "unknown field",
			data: func(t *testing.T) []byte {
				return []byte(`{"schema_version":2,"token":"` + token + `","committed":false,"entries":[{"path":"` + target + `","backup":"` + setupBackupPath(target, token) + `","staged":"` + setupStagedPath(target, token) + `","replacement":"` + setupReplacementPath(target, token) + `","existed":false,"after_mode":384,"after_digest":"` + hexDigest([]byte("after")) + `","unknown":true}]}`)
			},
			want: "unknown field",
		},
		{
			name: "trailing data",
			data: func(t *testing.T) []byte {
				data, err := json.Marshal(base)
				if err != nil {
					t.Fatal(err)
				}
				return append(data, []byte(" {}")...)
			},
			want: "trailing data",
		},
		{
			name: "relative target",
			data: func(t *testing.T) []byte {
				invalid := base
				invalid.Entries = append([]setupJournalEntry(nil), base.Entries...)
				invalid.Entries[0].Path = "relative"
				invalid.Entries[0].Backup = setupBackupPath("relative", token)
				invalid.Entries[0].Staged = setupStagedPath("relative", token)
				data, err := json.Marshal(invalid)
				if err != nil {
					t.Fatal(err)
				}
				return data
			},
			want: "target path is invalid",
		},
		{
			name: "relative install lock",
			data: func(t *testing.T) []byte {
				invalid := base
				invalid.InstallLock = "relative"
				data, err := json.Marshal(invalid)
				if err != nil {
					t.Fatal(err)
				}
				return data
			},
			want: "install lock path is invalid",
		},
		{
			name: "root install lock",
			data: func(t *testing.T) []byte {
				invalid := base
				invalid.InstallLock = string(filepath.Separator)
				data, err := json.Marshal(invalid)
				if err != nil {
					t.Fatal(err)
				}
				return data
			},
			want: "install lock path is invalid",
		},
		{
			name: "install lock repeats target",
			data: func(t *testing.T) []byte {
				invalid := base
				invalid.InstallLock = target
				data, err := json.Marshal(invalid)
				if err != nil {
					t.Fatal(err)
				}
				return data
			},
			want: "repeats path",
		},
		{
			name: "service state without manager",
			data: func(t *testing.T) []byte {
				invalid := base
				invalid.ServiceActiveTouched = true
				data, err := json.Marshal(invalid)
				if err != nil {
					t.Fatal(err)
				}
				return data
			},
			want: "service state without a manager",
		},
		{
			name: "invalid service manager",
			data: func(t *testing.T) []byte {
				invalid := base
				invalid.ServiceManager = "auto"
				data, err := json.Marshal(invalid)
				if err != nil {
					t.Fatal(err)
				}
				return data
			},
			want: "service manager is invalid",
		},
		{
			name: "external manager touched",
			data: func(t *testing.T) []byte {
				invalid := base
				invalid.ServiceManager = "none"
				invalid.ServiceActiveTouched = true
				data, err := json.Marshal(invalid)
				if err != nil {
					t.Fatal(err)
				}
				return data
			},
			want: "external service state is invalid",
		},
		{
			name: "automatic manager missing executable",
			data: func(t *testing.T) []byte {
				invalid := base
				invalid.ServiceManager = "systemd"
				data, err := json.Marshal(invalid)
				if err != nil {
					t.Fatal(err)
				}
				return data
			},
			want: "service executable identity is invalid",
		},
		{
			name: "automatic manager invalid executable digest",
			data: func(t *testing.T) []byte {
				invalid := base
				invalid.ServiceManager = "systemd"
				invalid.ServiceExecutable = filepath.Join(root, "systemctl")
				invalid.ServiceExecutableDigest = "invalid"
				data, err := json.Marshal(invalid)
				if err != nil {
					t.Fatal(err)
				}
				return data
			},
			want: "service executable identity is invalid",
		},
		{
			name: "completed OAuth without attempt",
			data: func(t *testing.T) []byte {
				invalid := base
				invalid.ServiceManager = "none"
				invalid.OAuthCompleted = true
				data, err := json.Marshal(invalid)
				if err != nil {
					t.Fatal(err)
				}
				return data
			},
			want: "completed OAuth without an attempt",
		},
		{
			name: "forged artifact",
			data: func(t *testing.T) []byte {
				invalid := base
				invalid.Entries = append([]setupJournalEntry(nil), base.Entries...)
				invalid.Entries[0].Backup = filepath.Join(root, "other")
				data, err := json.Marshal(invalid)
				if err != nil {
					t.Fatal(err)
				}
				return data
			},
			want: "artifact path is invalid",
		},
		{
			name: "special after mode",
			data: func(t *testing.T) []byte {
				invalid := base
				invalid.Entries = append([]setupJournalEntry(nil), base.Entries...)
				invalid.Entries[0].AfterMode = uint32(os.ModeSetuid | 0o600)
				data, err := json.Marshal(invalid)
				if err != nil {
					t.Fatal(err)
				}
				return data
			},
			want: "after mode is invalid",
		},
		{
			name: "special prior mode",
			data: func(t *testing.T) []byte {
				invalid := base
				invalid.Entries = append([]setupJournalEntry(nil), base.Entries...)
				invalid.Entries[0].Existed = true
				invalid.Entries[0].Mode = uint32(os.ModeSetgid | 0o600)
				invalid.Entries[0].BeforeDigest = hexDigest([]byte("before"))
				data, err := json.Marshal(invalid)
				if err != nil {
					t.Fatal(err)
				}
				return data
			},
			want: "prior state is invalid",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "journal.json")
			if err := os.WriteFile(path, test.data(t), 0o600); err != nil {
				t.Fatal(err)
			}
			if _, err := loadSetupJournal(path); err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("load error = %v", err)
			}
		})
	}
}

func TestReconcileCommitErrorFinalizesDurableCommit(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "target")
	if err := os.WriteFile(target, []byte("before\n"), 0o600); err != nil {
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
	if err := transaction.prepareCommittedJournal(); err != nil {
		t.Fatal(err)
	}
	if err := writeJSONPrivate(transaction.journalPath, transaction.journal); err != nil {
		t.Fatal(err)
	}

	if err := transaction.reconcileCommitError(errors.New("injected commit error")); err != nil {
		t.Fatalf("reconcile error = %v", err)
	}
	assertFileState(t, target, "after\n", 0o600)
	assertSetupArtifactsAbsent(t, transaction)
}

func TestReconcileCommitErrorRejectsMissingJournalWithoutCommittedTargets(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "target")
	if err := os.WriteFile(target, []byte("before\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	transaction, err := newSetupTransaction(filepath.Join(root, "state"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := transaction.addFile(target, []byte("after\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := transaction.persist(); err != nil {
		t.Fatal(err)
	}
	if err := transaction.removeJournal(transaction.journal); err != nil {
		t.Fatal(err)
	}

	primary := errors.New("injected commit error")
	err = transaction.reconcileCommitError(primary)
	if !errors.Is(err, primary) || !strings.Contains(err.Error(), "not in the committed final state") {
		t.Fatalf("reconcile error = %v", err)
	}
	if !transaction.persisted {
		t.Fatal("ambiguous transaction was marked complete")
	}
	assertFileState(t, target, "before\n", 0o600)
}

func TestReconcileCommitErrorRollsBackDurableUncommittedState(t *testing.T) {
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
	if err := transaction.persist(); err != nil {
		t.Fatal(err)
	}
	if err := transaction.applyFile(index); err != nil {
		t.Fatal(err)
	}

	primary := errors.New("injected commit error")
	if err := transaction.reconcileCommitError(primary); !errors.Is(err, primary) {
		t.Fatalf("reconcile error = %v", err)
	}
	assertFileState(t, target, "before\n", 0o640)
	assertSetupArtifactsAbsent(t, transaction)
}

func TestReconcileCommitErrorKeepsDurableCommittedConflict(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "target")
	if err := os.WriteFile(target, []byte("before\n"), 0o600); err != nil {
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
	if err := transaction.prepareCommittedJournal(); err != nil {
		t.Fatal(err)
	}
	if err := writeJSONPrivate(transaction.journalPath, transaction.journal); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(target, []byte("external\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	primary := errors.New("injected commit error")
	err = transaction.reconcileCommitError(primary)
	if !errors.Is(err, primary) || !strings.Contains(err.Error(), "does not match the journal") {
		t.Fatalf("reconcile error = %v", err)
	}
	assertFileState(t, target, "external\n", 0o600)
	if _, statErr := os.Lstat(transaction.journalPath); statErr != nil {
		t.Fatalf("journal was removed after committed conflict: %v", statErr)
	}
}

func TestRecoverLegacySetupJournalRestoresExistingTarget(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "target")
	if err := os.WriteFile(target, []byte("before\n"), 0o600); err != nil {
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
	prepared := transaction.files[index]
	entry := transaction.journal.Entries[index]
	parent, err := openSetupParent(target, false)
	if err != nil {
		t.Fatal(err)
	}
	if err := parent.WriteAtomic(filepath.Base(entry.Staged), prepared.before, os.FileMode(entry.Mode)); err != nil {
		parent.Close()
		t.Fatal(err)
	}
	if err := parent.WriteAtomic(filepath.Base(entry.Backup), prepared.before, os.FileMode(entry.Mode)); err != nil {
		parent.Close()
		t.Fatal(err)
	}
	if err := parent.WriteAtomic(filepath.Base(entry.Replacement), prepared.after, os.FileMode(entry.AfterMode)); err != nil {
		parent.Close()
		t.Fatal(err)
	}
	if err := parent.Unlink(filepath.Base(entry.Path)); err != nil {
		parent.Close()
		t.Fatal(err)
	}
	if err := parent.Link(filepath.Base(entry.Replacement), filepath.Base(entry.Path)); err != nil {
		parent.Close()
		t.Fatal(err)
	}
	if err := parent.Close(); err != nil {
		t.Fatal(err)
	}
	transaction.journal.SchemaVersion = legacySetupJournalSchema
	for index := range transaction.journal.Entries {
		transaction.journal.Entries[index].FinalIdentity = nil
		transaction.journal.Entries[index].BackupIdentity = nil
		transaction.journal.Entries[index].StagedIdentity = nil
	}
	if err := writeJSONPrivate(transaction.journalPath, transaction.journal); err != nil {
		t.Fatal(err)
	}

	if err := recoverSetupTransaction(filepath.Join(root, "state")); err != nil {
		t.Fatal(err)
	}
	assertFileState(t, target, "before\n", 0o600)
	assertSetupArtifactsAbsent(t, transaction)
}

func TestSetupTransactionPreservesReplacementInsertedAtPublication(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "target")
	if err := os.WriteFile(target, []byte("before\n"), 0o600); err != nil {
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
	beforeSetupFilePublication = func(path string) error {
		if path != target {
			return nil
		}
		replacement := filepath.Join(root, "external")
		if err := os.WriteFile(replacement, []byte("external\n"), 0o600); err != nil {
			return err
		}
		return os.Rename(replacement, target)
	}
	t.Cleanup(func() { beforeSetupFilePublication = nil })

	err = transaction.applyFile(index)
	beforeSetupFilePublication = nil
	if err == nil || !strings.Contains(err.Error(), "changed during publication") {
		t.Fatalf("apply error = %v", err)
	}
	assertFileState(t, target, "external\n", 0o600)
	assertFileState(t, transaction.journal.Entries[0].Staged, "after\n", 0o600)
}

func TestSetupTransactionRejectsStagedArtifactSubstitution(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "target")
	if err := os.WriteFile(target, []byte("before\n"), 0o600); err != nil {
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
	entry := transaction.journal.Entries[0]
	beforeSetupFilePublication = func(path string) error {
		if path != target {
			return nil
		}
		replacement := filepath.Join(root, "external-stage")
		if err := os.WriteFile(replacement, []byte("external\n"), 0o600); err != nil {
			return err
		}
		return os.Rename(replacement, entry.Staged)
	}
	t.Cleanup(func() { beforeSetupFilePublication = nil })

	err = transaction.applyFile(index)
	beforeSetupFilePublication = nil
	if err == nil || !strings.Contains(err.Error(), "publication marker") {
		t.Fatalf("apply error = %v", err)
	}
	assertFileState(t, target, "before\n", 0o600)
	assertFileState(t, entry.Staged, "external\n", 0o600)
}

func TestSetupTransactionRejectsBackupArtifactSubstitution(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "target")
	if err := os.WriteFile(target, []byte("before\n"), 0o600); err != nil {
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
	entry := transaction.journal.Entries[0]
	if err := transaction.applyFile(index); err != nil {
		t.Fatal(err)
	}
	replacement := filepath.Join(root, "external-backup")
	if err := os.WriteFile(replacement, []byte("external\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(replacement, entry.Replacement); err != nil {
		t.Fatal(err)
	}
	err = transaction.rollbackFiles()
	if err == nil || !strings.Contains(err.Error(), "setup original") {
		t.Fatalf("rollback error = %v", err)
	}
	assertFileState(t, target, "after\n", 0o600)
}

func TestSetupTransactionRejectsPublicationMarkerSubstitution(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "target")
	if err := os.WriteFile(target, []byte("before\n"), 0o600); err != nil {
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
	entry := transaction.journal.Entries[0]
	if err := os.Remove(entry.Staged); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(entry.Staged, []byte("after\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	err = transaction.rollbackFiles()
	if err == nil || !strings.Contains(err.Error(), "no ownership marker") {
		t.Fatalf("rollback error = %v", err)
	}
	assertFileState(t, target, "after\n", 0o600)
}

func TestSetupTransactionRejectsExactAfterRaceWithoutRollbackOwnership(t *testing.T) {
	for _, test := range []struct {
		name    string
		existed bool
	}{
		{name: "existing target", existed: true},
		{name: "absent target"},
	} {
		t.Run(test.name, func(t *testing.T) {
			root := t.TempDir()
			target := filepath.Join(root, "target")
			if test.existed {
				if err := os.WriteFile(target, []byte("before\n"), 0o640); err != nil {
					t.Fatal(err)
				}
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
			afterSetupFileInitialRevalidation = func(path string) error {
				if path != target {
					return nil
				}
				return os.WriteFile(target, []byte("after\n"), 0o600)
			}
			t.Cleanup(func() { afterSetupFileInitialRevalidation = nil })

			err = transaction.applyFile(index)
			if err == nil || !strings.Contains(err.Error(), "changed after preparation") {
				t.Fatalf("apply error = %v", err)
			}
			wantMode := os.FileMode(0o600)
			if test.existed {
				wantMode = 0o640
			}
			assertFileState(t, target, "after\n", wantMode)
			if _, statErr := os.Lstat(transaction.journalPath); statErr != nil {
				t.Fatalf("journal was removed after refused apply: %v", statErr)
			}
		})
	}
}

func TestSetupRuntimeRejectsExactAfterRaceWithoutRollbackOwnership(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "runtime.json")
	runtimeConfig := config.Runtime{
		SchemaVersion:   config.SchemaVersion,
		RealClaude:      "/usr/bin/claude",
		ClaudexMode:     "external",
		RealClaudex:     "/usr/bin/claudex",
		ScreenPath:      "/usr/bin/screen",
		CLIProxyService: "none",
	}
	after, err := config.EncodeRuntime(runtimeConfig)
	if err != nil {
		t.Fatal(err)
	}
	transaction, err := newSetupTransaction(filepath.Join(root, "state"))
	if err != nil {
		t.Fatal(err)
	}
	index, err := transaction.addFile(target, after, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	if err := transaction.persist(); err != nil {
		t.Fatal(err)
	}
	afterSetupFileInitialRevalidation = func(path string) error {
		if path != target {
			return nil
		}
		return os.WriteFile(target, after, 0o600)
	}
	t.Cleanup(func() { afterSetupFileInitialRevalidation = nil })

	err = transaction.applyRuntime(index, runtimeConfig)
	if err == nil || !strings.Contains(err.Error(), "changed after preparation") {
		t.Fatalf("apply error = %v", err)
	}
	assertFileState(t, target, string(after), 0o600)
	if _, statErr := os.Lstat(transaction.journalPath); statErr != nil {
		t.Fatalf("journal was removed after refused apply: %v", statErr)
	}
}

func TestRollbackPreservesReplacementInsertedAtRestoreBoundary(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "target")
	if err := os.WriteFile(target, []byte("before\n"), 0o600); err != nil {
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
	beforeSetupFileRollback = func(path string) error {
		if path != target {
			return nil
		}
		replacement := filepath.Join(root, "external")
		if err := os.WriteFile(replacement, []byte("external\n"), 0o600); err != nil {
			return err
		}
		return os.Rename(replacement, target)
	}
	t.Cleanup(func() { beforeSetupFileRollback = nil })

	err = transaction.rollbackFiles()
	beforeSetupFileRollback = nil
	if err == nil || !strings.Contains(err.Error(), "changed during rollback") {
		t.Fatalf("rollback error = %v", err)
	}
	assertFileState(t, target, "external\n", 0o600)
}

func TestRollbackPreservesReplacementInsertedAtDeleteBoundary(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "target")
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
	beforeSetupFileRollback = func(path string) error {
		if path != target {
			return nil
		}
		replacement := filepath.Join(root, "external")
		if err := os.WriteFile(replacement, []byte("external\n"), 0o600); err != nil {
			return err
		}
		return os.Rename(replacement, target)
	}
	t.Cleanup(func() { beforeSetupFileRollback = nil })

	err = transaction.rollbackFiles()
	beforeSetupFileRollback = nil
	if err == nil {
		t.Fatal("rollback accepted an externally replaced target")
	}
	assertFileState(t, target, "external\n", 0o600)
}

func TestRollbackCreatedTargetRevalidatesBeforeRemoval(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "target")
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
	entry := transaction.journal.Entries[0]
	beforeSetupCreatedTargetRemoval = func(path string) error {
		if path != target {
			return nil
		}
		return os.WriteFile(target, []byte("external\n"), 0o600)
	}
	t.Cleanup(func() { beforeSetupCreatedTargetRemoval = nil })

	err = transaction.rollbackFiles()
	beforeSetupCreatedTargetRemoval = nil
	if err == nil || !strings.Contains(err.Error(), "was replaced before removal") {
		t.Fatalf("rollback error = %v", err)
	}
	assertFileState(t, target, "external\n", 0o600)
	assertFileState(t, entry.Staged, "after\n", 0o600)
	assertFileState(t, entry.Replacement, "after\n", 0o600)
}

func TestRollbackRejectsArtifactSubstitutionBeforeCleanup(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "target")
	if err := os.WriteFile(target, []byte("before\n"), 0o600); err != nil {
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
	entry := transaction.journal.Entries[0]
	beforeSetupArtifactRemoval = func(path string) error {
		if path != entry.Backup {
			return nil
		}
		replacement := filepath.Join(root, "external-artifact")
		if err := os.WriteFile(replacement, []byte("external\n"), 0o600); err != nil {
			return err
		}
		if err := os.Rename(replacement, path); err != nil {
			return err
		}
		return errors.New("injected artifact substitution")
	}
	t.Cleanup(func() { beforeSetupArtifactRemoval = nil })

	err = transaction.rollbackFiles()
	beforeSetupArtifactRemoval = nil
	if err == nil || !strings.Contains(err.Error(), "injected artifact substitution") {
		t.Fatalf("rollback error = %v", err)
	}
	assertFileState(t, target, "before\n", 0o600)
	assertFileState(t, entry.Backup, "external\n", 0o600)
}

func TestRecoverCreatedTargetRejectsSameStateStagedSubstitution(t *testing.T) {
	root := t.TempDir()
	stateRoot := filepath.Join(root, "state")
	target := filepath.Join(root, "target")
	transaction, err := newSetupTransaction(stateRoot)
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
	beforeSetupFilePublication = func(path string) error {
		if path != target {
			return nil
		}
		return errors.New("simulated crash before publication")
	}
	t.Cleanup(func() { beforeSetupFilePublication = nil })
	if err := transaction.applyFile(index); err == nil || !strings.Contains(
		err.Error(),
		"simulated crash before publication",
	) {
		t.Fatalf("apply error = %v", err)
	}
	beforeSetupFilePublication = nil

	journal, err := loadSetupJournal(transaction.journalPath)
	if err != nil {
		t.Fatal(err)
	}
	entry := journal.Entries[index]
	if entry.StagedIdentity == nil || entry.FinalIdentity != nil {
		t.Fatalf("staged journal identity = %#v, final = %#v", entry.StagedIdentity, entry.FinalIdentity)
	}
	foreign := filepath.Join(root, "foreign-stage")
	if err := os.WriteFile(foreign, []byte("after\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(foreign, entry.Staged); err != nil {
		t.Fatal(err)
	}

	err = recoverSetupTransaction(stateRoot)
	if err == nil || !strings.Contains(err.Error(), "recorded identity") {
		t.Fatalf("recovery error = %v", err)
	}
	if _, statErr := os.Lstat(target); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("target exists after refused recovery: %v", statErr)
	}
	assertFileState(t, entry.Staged, "after\n", 0o600)
	if _, statErr := os.Lstat(transaction.journalPath); statErr != nil {
		t.Fatalf("journal was removed after refused recovery: %v", statErr)
	}
}

func TestRecoverRejectsSameStateArtifactSubstitution(t *testing.T) {
	root := t.TempDir()
	stateRoot := filepath.Join(root, "state")
	target := filepath.Join(root, "target")
	if err := os.WriteFile(target, []byte("before\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	transaction, err := newSetupTransaction(stateRoot)
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
	entry := transaction.journal.Entries[index]
	foreign := filepath.Join(root, "foreign-artifact")
	if err := os.WriteFile(foreign, []byte("before\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(foreign, entry.Backup); err != nil {
		t.Fatal(err)
	}

	err = recoverSetupTransaction(stateRoot)
	if err == nil || !strings.Contains(
		err.Error(),
		"ownership marker",
	) {
		t.Fatalf("recovery error = %v", err)
	}
	assertFileState(t, target, "after\n", 0o600)
	assertFileState(t, entry.Backup, "before\n", 0o600)
	if _, statErr := os.Lstat(transaction.journalPath); statErr != nil {
		t.Fatalf("journal was removed after refused recovery: %v", statErr)
	}
}

func TestFinalizeRejectsSameStateArtifactSubstitution(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "target")
	if err := os.WriteFile(target, []byte("before\n"), 0o600); err != nil {
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
	if err := transaction.prepareCommittedJournal(); err != nil {
		t.Fatal(err)
	}
	if err := transaction.writeJournal(); err != nil {
		t.Fatal(err)
	}
	entry := transaction.journal.Entries[index]
	foreign := filepath.Join(root, "foreign-artifact")
	if err := os.WriteFile(foreign, []byte("before\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(foreign, entry.Backup); err != nil {
		t.Fatal(err)
	}

	err = transaction.finalizeJournal(transaction.journal)
	if err == nil || !strings.Contains(
		err.Error(),
		"recorded identity",
	) {
		t.Fatalf("finalization error = %v", err)
	}
	assertFileState(t, target, "after\n", 0o600)
	assertFileState(t, entry.Backup, "before\n", 0o600)
	if _, statErr := os.Lstat(transaction.journalPath); statErr != nil {
		t.Fatalf("journal was removed after refused finalization: %v", statErr)
	}
}

func TestFinalizeReconcilesArtifactUnlinkBeforeSyncFailure(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "target")
	if err := os.WriteFile(target, []byte("before\n"), 0o600); err != nil {
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
	if err := transaction.prepareCommittedJournal(); err != nil {
		t.Fatal(err)
	}
	if err := transaction.writeJournal(); err != nil {
		t.Fatal(err)
	}
	originalRemove := removeSetupRegularOutcome
	failed := false
	removeSetupRegularOutcome = func(parent *fssecure.Directory, name string, expected os.FileInfo, beforeRemove func() error) (bool, error) {
		removed, removeErr := parent.RemoveRegularOutcome(name, expected, beforeRemove)
		if removeErr == nil && !failed && strings.Contains(name, ".sclaude-") {
			failed = true
			return true, errors.New("injected directory sync failure after artifact unlink")
		}
		return removed, removeErr
	}
	t.Cleanup(func() { removeSetupRegularOutcome = originalRemove })

	if err := transaction.finalizeJournal(transaction.journal); err != nil {
		t.Fatal(err)
	}
	removeSetupRegularOutcome = originalRemove
	if !failed {
		t.Fatal("artifact removal failure seam did not run")
	}
	assertFileState(t, target, "after\n", 0o600)
	assertSetupArtifactsAbsent(t, transaction)
}

func TestRemoveJournalRetriesSyncAfterUnlinkSyncFailure(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "target")
	transaction, err := newSetupTransaction(filepath.Join(root, "state"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := transaction.addFile(target, []byte("after\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := transaction.persist(); err != nil {
		t.Fatal(err)
	}

	primary := errors.New("injected initial directory sync failure")
	originalRemove := removeSetupRegularOutcome
	originalSync := syncSetupDirectory
	removeCalls := 0
	syncCalls := 0
	removeSetupRegularOutcome = func(parent *fssecure.Directory, name string, expected os.FileInfo, beforeRemove func() error) (bool, error) {
		removeCalls++
		if beforeRemove != nil {
			if err := beforeRemove(); err != nil {
				return false, err
			}
		}
		current, err := parent.InspectRegular(name)
		if err != nil {
			return false, err
		}
		if current == nil || !os.SameFile(expected, current) {
			return false, errors.New("target changed before removal")
		}
		if err := parent.Unlink(name); err != nil {
			return false, err
		}
		return true, primary
	}
	syncSetupDirectory = func(parent *fssecure.Directory) error {
		syncCalls++
		return parent.Sync()
	}
	t.Cleanup(func() {
		removeSetupRegularOutcome = originalRemove
		syncSetupDirectory = originalSync
	})

	if err := transaction.removeJournal(transaction.journal); err != nil {
		t.Fatal(err)
	}
	removeSetupRegularOutcome = originalRemove
	syncSetupDirectory = originalSync
	if removeCalls != 1 {
		t.Fatalf("remove calls = %d, want 1", removeCalls)
	}
	if syncCalls != 1 {
		t.Fatalf("retry sync calls = %d, want 1", syncCalls)
	}
	if _, err := os.Lstat(transaction.journalPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("journal remains after reconciled removal: %v", err)
	}
}

func TestFinalizeRejectsArtifactSubstitutionBeforeCleanup(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "target")
	if err := os.WriteFile(target, []byte("before\n"), 0o600); err != nil {
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
	if err := transaction.prepareCommittedJournal(); err != nil {
		t.Fatal(err)
	}
	if err := transaction.writeJournal(); err != nil {
		t.Fatal(err)
	}
	entry := transaction.journal.Entries[0]
	beforeSetupArtifactRemoval = func(path string) error {
		if path != entry.Backup {
			return nil
		}
		replacement := filepath.Join(root, "external-artifact")
		if err := os.WriteFile(replacement, []byte("external\n"), 0o600); err != nil {
			return err
		}
		if err := os.Rename(replacement, path); err != nil {
			return err
		}
		return errors.New("injected artifact substitution")
	}
	t.Cleanup(func() { beforeSetupArtifactRemoval = nil })

	err = transaction.finalizeJournal(transaction.journal)
	beforeSetupArtifactRemoval = nil
	if err == nil || !strings.Contains(err.Error(), "injected artifact substitution") {
		t.Fatalf("finalization error = %v", err)
	}
	assertFileState(t, target, "after\n", 0o600)
	assertFileState(t, entry.Backup, "external\n", 0o600)
	if _, statErr := os.Lstat(transaction.journalPath); statErr != nil {
		t.Fatalf("journal was removed after refused finalization: %v", statErr)
	}
}

func TestFinalizeRejectsExactStateTargetReplacementBeforeJournalRemoval(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "target")
	if err := os.WriteFile(target, []byte("before\n"), 0o600); err != nil {
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
	if err := transaction.prepareCommittedJournal(); err != nil {
		t.Fatal(err)
	}
	if err := transaction.writeJournal(); err != nil {
		t.Fatal(err)
	}
	beforeSetupJournalRemoval = func(string) error {
		replacement := filepath.Join(root, "external-target")
		if err := os.WriteFile(replacement, []byte("after\n"), 0o600); err != nil {
			return err
		}
		return os.Rename(replacement, target)
	}
	t.Cleanup(func() { beforeSetupJournalRemoval = nil })

	err = transaction.finalizeJournal(transaction.journal)
	beforeSetupJournalRemoval = nil
	if err == nil || !strings.Contains(err.Error(), "changed during journal removal") {
		t.Fatalf("finalization error = %v", err)
	}
	assertFileState(t, target, "after\n", 0o600)
	if _, statErr := os.Lstat(transaction.journalPath); statErr != nil {
		t.Fatalf("journal was removed after target substitution: %v", statErr)
	}
}

func TestFinalizeRejectsJournalSubstitutionBeforeRemoval(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "target")
	if err := os.WriteFile(target, []byte("before\n"), 0o600); err != nil {
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
	if err := transaction.prepareCommittedJournal(); err != nil {
		t.Fatal(err)
	}
	if err := transaction.writeJournal(); err != nil {
		t.Fatal(err)
	}
	beforeSetupJournalRemoval = func(string) error {
		replacement := filepath.Join(root, "external-journal")
		if err := writeJSONPrivate(replacement, transaction.journal); err != nil {
			return err
		}
		return os.Rename(replacement, transaction.journalPath)
	}
	t.Cleanup(func() { beforeSetupJournalRemoval = nil })

	err = transaction.finalizeJournal(transaction.journal)
	beforeSetupJournalRemoval = nil
	if err == nil || !strings.Contains(err.Error(), "journal changed before removal") {
		t.Fatalf("finalization error = %v", err)
	}
	if _, statErr := os.Lstat(transaction.journalPath); statErr != nil {
		t.Fatalf("substituted journal was removed: %v", statErr)
	}
	assertFileState(t, target, "after\n", 0o600)
}

func TestRollbackCreatedTargetRequiresOwnershipMarker(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "target")
	transaction, err := newSetupTransaction(filepath.Join(root, "state"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := transaction.addFile(target, []byte("after\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := transaction.persist(); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(target, []byte("after\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	primary := errors.New("injected failure")
	err = transaction.abort(primary)
	if !errors.Is(err, primary) || !strings.Contains(err.Error(), "no ownership marker") {
		t.Fatalf("abort error = %v", err)
	}
	assertFileState(t, target, "after\n", 0o600)
	if _, statErr := os.Lstat(transaction.journalPath); statErr != nil {
		t.Fatalf("journal was removed after refused rollback: %v", statErr)
	}
}

func TestApplyFailureDoesNotRollbackExternalExactAfterReplacement(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "target")
	if err := os.WriteFile(target, []byte("before\n"), 0o600); err != nil {
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
	afterSetupFileInitialRevalidation = func(path string) error {
		if path != target {
			return nil
		}
		external := filepath.Join(root, "external")
		if err := os.WriteFile(external, []byte("after\n"), 0o600); err != nil {
			return err
		}
		return os.Rename(external, target)
	}
	t.Cleanup(func() { afterSetupFileInitialRevalidation = nil })

	applyErr := transaction.applyFile(index)
	if applyErr == nil || !strings.Contains(applyErr.Error(), "changed after preparation") {
		t.Fatalf("apply error = %v", applyErr)
	}
	afterSetupFileInitialRevalidation = nil
	primary := errors.New("injected failure")
	err = transaction.abort(primary)
	if !errors.Is(err, primary) || !strings.Contains(err.Error(), "no ownership marker") {
		t.Fatalf("abort error = %v", err)
	}
	assertFileState(t, target, "after\n", 0o600)
	if _, statErr := os.Lstat(transaction.journalPath); statErr != nil {
		t.Fatalf("journal was removed after refused rollback: %v", statErr)
	}
}

func TestRollbackReplacedTargetRequiresOwnershipMarker(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "target")
	if err := os.WriteFile(target, []byte("before\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	transaction, err := newSetupTransaction(filepath.Join(root, "state"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := transaction.addFile(target, []byte("after\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := transaction.persist(); err != nil {
		t.Fatal(err)
	}
	entry := transaction.journal.Entries[0]
	if err := writePrivateFileSynced(entry.Backup, []byte("before\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	external := filepath.Join(root, "external")
	if err := os.WriteFile(external, []byte("after\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(external, target); err != nil {
		t.Fatal(err)
	}

	primary := errors.New("injected failure")
	err = transaction.abort(primary)
	if !errors.Is(err, primary) || !strings.Contains(err.Error(), "no ownership marker") {
		t.Fatalf("abort error = %v", err)
	}
	assertFileState(t, target, "after\n", 0o600)
	if _, statErr := os.Lstat(transaction.journalPath); statErr != nil {
		t.Fatalf("journal was removed after refused rollback: %v", statErr)
	}
}

func TestSetupTransactionDoesNotMutateReplacementParent(t *testing.T) {
	root := t.TempDir()
	parent := filepath.Join(root, "config")
	if err := os.Mkdir(parent, 0o700); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(parent, "target")
	if err := os.WriteFile(target, []byte("before\n"), 0o600); err != nil {
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
	originalParent := filepath.Join(root, "original-config")
	afterSetupParentOpen = func(path string) error {
		if path != parent {
			return nil
		}
		if err := os.Rename(parent, originalParent); err != nil {
			return err
		}
		return os.Mkdir(parent, 0o700)
	}
	t.Cleanup(func() { afterSetupParentOpen = nil })

	err = transaction.applyFile(index)
	afterSetupParentOpen = nil
	if err == nil || !strings.Contains(err.Error(), "parent") || !strings.Contains(err.Error(), "changed after preparation") {
		t.Fatalf("apply error = %v", err)
	}
	if _, statErr := os.Lstat(filepath.Join(parent, "target")); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("replacement parent was mutated: %v", statErr)
	}
	assertFileState(t, filepath.Join(originalParent, "target"), "before\n", 0o600)
}

func TestSetupTransactionRevalidatesPreparedTarget(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "target")
	if err := os.WriteFile(target, []byte("before\n"), 0o600); err != nil {
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
	if err := os.WriteFile(target, []byte("external\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := transaction.applyFile(index); err == nil || !strings.Contains(err.Error(), "changed after preparation") {
		t.Fatalf("apply error = %v", err)
	}
	assertFileState(t, target, "external\n", 0o600)
}

func TestSetupTransactionRejectsUnsafeTargets(t *testing.T) {
	root := t.TempDir()
	transaction, err := newSetupTransaction(filepath.Join(root, "state"))
	if err != nil {
		t.Fatal(err)
	}

	if _, err := transaction.addFile(filepath.Join(root, "mode"), []byte("after"), os.ModeSetuid|0o600); err == nil || !strings.Contains(err.Error(), "permission bits") {
		t.Fatalf("special-mode add error = %v", err)
	}

	t.Run("existing special mode", func(t *testing.T) {
		target := filepath.Join(root, "special")
		if err := os.WriteFile(target, []byte("before"), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := os.Chmod(target, os.ModeSetuid|0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := transaction.addFile(target, []byte("after"), 0o600); err == nil || !strings.Contains(err.Error(), "unsupported mode") {
			t.Fatalf("existing special-mode add error = %v", err)
		}
	})

	t.Run("symlink", func(t *testing.T) {
		target := filepath.Join(root, "real")
		link := filepath.Join(root, "link")
		if err := os.WriteFile(target, []byte("data"), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink("real", link); err != nil {
			t.Fatal(err)
		}
		if _, err := transaction.addFile(link, []byte("after"), 0o600); err == nil || !strings.Contains(err.Error(), "regular non-symlink") {
			t.Fatalf("add error = %v", err)
		}
	})

	t.Run("directory", func(t *testing.T) {
		target := filepath.Join(root, "directory")
		if err := os.Mkdir(target, 0o700); err != nil {
			t.Fatal(err)
		}
		if _, err := transaction.addFile(target, []byte("after"), 0o600); err == nil || !strings.Contains(err.Error(), "regular non-symlink") {
			t.Fatalf("add error = %v", err)
		}
	})
}

func testExternalRuntimeConfig() config.Runtime {
	return config.Runtime{
		SchemaVersion:   config.SchemaVersion,
		RealClaude:      "/usr/bin/claude",
		ClaudexMode:     "external",
		RealClaudex:     "/usr/bin/claudex",
		ScreenPath:      "/usr/bin/screen",
		CLIProxyService: "none",
	}
}

func assertFileState(t *testing.T, path, want string, mode os.FileMode) {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != want {
		t.Fatalf("%s contents = %q, want %q", path, data, want)
	}
	info, err := os.Lstat(path)
	if err != nil {
		t.Fatal(err)
	}
	if !info.Mode().IsRegular() || info.Mode().Perm() != mode {
		t.Fatalf("%s mode = %v, want %o regular", path, info.Mode(), mode)
	}
}

func assertDirectoryMode(t *testing.T, path string, mode os.FileMode) {
	t.Helper()
	info, err := os.Lstat(path)
	if err != nil {
		t.Fatal(err)
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 || info.Mode().Perm() != mode {
		t.Fatalf("%s mode = %v, want %o non-symlink directory", path, info.Mode(), mode)
	}
}

func assertSetupArtifactsAbsent(t *testing.T, transaction *setupTransaction) {
	t.Helper()
	for _, path := range append([]string{transaction.journalPath}, setupTransactionArtifacts(transaction)...) {
		if _, err := os.Lstat(path); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("setup transaction artifact remains at %s: %v", path, err)
		}
	}
}

func setupTransactionArtifacts(transaction *setupTransaction) []string {
	paths := make([]string, 0, len(transaction.journal.Entries)*3)
	for _, entry := range transaction.journal.Entries {
		paths = append(paths, setupEntryArtifacts(entry)...)
	}
	return paths
}
