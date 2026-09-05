package setup

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	configpkg "github.com/ctrl-alt-raccoon/shellmates/internal/config"
	"github.com/ctrl-alt-raccoon/shellmates/internal/fssecure"
	screenpkg "github.com/ctrl-alt-raccoon/shellmates/internal/screen"
	"github.com/ctrl-alt-raccoon/shellmates/internal/session"
	"github.com/ctrl-alt-raccoon/shellmates/internal/stateroot"
)

func testInstallLayout(t *testing.T) InstallLayout {
	t.Helper()
	root := t.TempDir()
	return InstallLayout{
		DataDir:  filepath.Join(root, "data"),
		StateDir: filepath.Join(root, "state", "install"),
		BinDir:   filepath.Join(root, "bin"),
	}
}

func testPathsForLayout(layout InstallLayout) configpkg.Paths {
	stateRoot := filepath.Dir(layout.StateDir)
	return configpkg.Paths{
		StateRoot:    stateRoot,
		SessionsDir:  filepath.Join(stateRoot, "sessions"),
		LaunchDir:    filepath.Join(stateRoot, "launch"),
		InstallState: layout.StateDir,
		DataRoot:     layout.DataDir,
	}
}

func testReleaseSource(t *testing.T, root, contents string) string {
	t.Helper()
	path := filepath.Join(root, "source-"+contents)
	if err := os.WriteFile(path, []byte(contents), 0o755); err != nil {
		t.Fatal(err)
	}
	return path
}

func testCurrentInstallJournal(t *testing.T, layout InstallLayout) installJournal {
	t.Helper()
	ledger, err := LoadInstallLedger(ledgerPath(layout))
	if err != nil {
		t.Fatal(err)
	}
	ledgerData, err := os.ReadFile(ledgerPath(layout))
	if err != nil {
		t.Fatal(err)
	}
	journal, err := captureJournal(
		layout,
		ledgerData,
		stagedRelease{
			Digest:      ledger.Releases[ledger.Current],
			Destination: releaseDir(layout, ledger.Current),
		},
		nil,
	)
	if err != nil {
		t.Fatal(err)
	}
	return journal
}

func TestValidateReleaseTag(t *testing.T) {
	valid := []string{
		"v0.0.0",
		"v1.2.3",
		"v1.2.3-rc.1",
		"v1.2.3-alpha-beta+build.7",
	}
	for _, tag := range valid {
		if err := validateReleaseTag(tag); err != nil {
			t.Errorf("validateReleaseTag(%q): %v", tag, err)
		}
	}
	invalid := []string{
		"1.2.3",
		"v01.2.3",
		"v1.02.3",
		"v1.2.03",
		"v1.2.3-01",
		"v1.2.3-",
		"snapshot",
		"v1.2.3/../../bad",
		" v1.2.3",
	}
	for _, tag := range invalid {
		if err := validateReleaseTag(tag); err == nil {
			t.Errorf("validateReleaseTag(%q) unexpectedly succeeded", tag)
		}
	}
}

func TestNormalizeInstallLayoutRejectsOverlappingPaths(t *testing.T) {
	root := t.TempDir()
	_, err := normalizeInstallLayout(InstallLayout{
		DataDir:  filepath.Join(root, "data"),
		StateDir: filepath.Join(root, "data", "state"),
		BinDir:   filepath.Join(root, "bin"),
	})
	if err == nil || !strings.Contains(err.Error(), "overlap") {
		t.Fatalf("overlap error = %v", err)
	}
}

func TestInstallRejectsInvalidTagAndSourceBeforeCreatingLayout(t *testing.T) {
	layout := testInstallLayout(t)
	root := filepath.Dir(layout.DataDir)
	source := testReleaseSource(t, root, "valid")
	if _, err := InstallBinary(source, "1.2.3", layout); err == nil {
		t.Fatal("invalid tag succeeded")
	}
	for _, path := range []string{layout.DataDir, layout.StateDir, layout.BinDir} {
		if _, err := os.Lstat(path); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("invalid tag created %s: %v", path, err)
		}
	}

	link := filepath.Join(root, "source-link")
	if err := os.Symlink(source, link); err != nil {
		t.Fatal(err)
	}
	if _, err := InstallBinary(link, "v1.2.3", layout); err == nil || !strings.Contains(err.Error(), "regular non-symlink") {
		t.Fatalf("symlink source error = %v", err)
	}
	for _, path := range []string{layout.DataDir, layout.StateDir, layout.BinDir} {
		if _, err := os.Lstat(path); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("invalid source created %s: %v", path, err)
		}
	}
}

func TestInstallSameTagRequiresSameDigest(t *testing.T) {
	layout := testInstallLayout(t)
	root := filepath.Dir(layout.DataDir)
	first := testReleaseSource(t, root, "one")
	identical := testReleaseSource(t, root, "one-copy")
	if err := os.WriteFile(identical, []byte("one"), 0o755); err != nil {
		t.Fatal(err)
	}
	different := testReleaseSource(t, root, "two")
	if _, err := InstallBinary(first, "v1.0.0", layout); err != nil {
		t.Fatal(err)
	}
	beforeLedger, err := os.ReadFile(ledgerPath(layout))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := InstallBinary(identical, "v1.0.0", layout); err != nil {
		t.Fatalf("identical reinstall: %v", err)
	}
	afterLedger, err := os.ReadFile(ledgerPath(layout))
	if err != nil {
		t.Fatal(err)
	}
	if string(afterLedger) != string(beforeLedger) {
		t.Fatal("same-version reinstall mutated the ledger")
	}
	if _, err := InstallBinary(different, "v1.0.0", layout); err == nil || !strings.Contains(err.Error(), "different contents") {
		t.Fatalf("different same-tag error = %v", err)
	}
}

func TestInstallRejectsUnmanagedExistingReleaseDirectory(t *testing.T) {
	layout := testInstallLayout(t)
	root := filepath.Dir(layout.DataDir)
	source := testReleaseSource(t, root, "one")
	destination := releaseDir(layout, "v1.0.0")
	if err := os.MkdirAll(destination, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(destination, "sclaude"), []byte("one"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(destination, "notes"), []byte("user data"), 0o600); err != nil {
		t.Fatal(err)
	}

	if _, err := InstallBinary(source, "v1.0.0", layout); err == nil || !strings.Contains(err.Error(), "not recorded as project-owned") {
		t.Fatalf("unmanaged release error = %v", err)
	}
	if data, err := os.ReadFile(filepath.Join(destination, "notes")); err != nil || string(data) != "user data" {
		t.Fatalf("unmanaged release data changed: %q, %v", data, err)
	}
}

func TestActivationLedgerFailureRestoresPriorState(t *testing.T) {
	layout := testInstallLayout(t)
	root := filepath.Dir(layout.DataDir)
	first := testReleaseSource(t, root, "one")
	second := testReleaseSource(t, root, "two")
	before, err := InstallBinary(first, "v1.0.0", layout)
	if err != nil {
		t.Fatal(err)
	}
	beforeLedger, err := os.ReadFile(ledgerPath(layout))
	if err != nil {
		t.Fatal(err)
	}
	ops := defaultActivationOps()
	ops.writeLedger = func(string, []byte) error { return errors.New("injected ledger failure") }
	if _, err := installBinaryWithOps(second, "v2.0.0", layout, ops); err == nil || !strings.Contains(err.Error(), "injected ledger failure") {
		t.Fatalf("activation error = %v", err)
	}
	target, err := readSymlink(currentPath(layout))
	if err != nil {
		t.Fatal(err)
	}
	if resolveLinkTarget(currentPath(layout), target) != releaseDir(layout, before.Current) {
		t.Fatalf("current target = %q, want prior release", target)
	}
	afterLedger, err := os.ReadFile(ledgerPath(layout))
	if err != nil {
		t.Fatal(err)
	}
	if string(afterLedger) != string(beforeLedger) {
		t.Fatal("ledger was not restored byte-for-byte")
	}
	if _, err := os.Lstat(journalPath(layout)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("journal remains after restoration: %v", err)
	}
}

func TestInstallPersistsJournalBeforePublishingRelease(t *testing.T) {
	layout := testInstallLayout(t)
	root := filepath.Dir(layout.DataDir)
	source := testReleaseSource(t, root, "one")
	beforeInstallReleasePublication = func(release stagedRelease) error {
		data, err := os.ReadFile(journalPath(layout))
		if err != nil {
			return err
		}
		var journal installJournal
		if err := json.Unmarshal(data, &journal); err != nil {
			return err
		}
		if !journal.NewReleaseCreated || journal.NewReleaseStage != release.StagePath ||
			journal.NewCurrent != release.Destination || journal.NewReleaseDigest != release.Digest ||
			journal.NewReleaseIdentity == nil {
			return errors.New("release journal was not durable before publication")
		}
		return errors.New("stop before publication")
	}
	t.Cleanup(func() { beforeInstallReleasePublication = nil })
	if _, err := InstallBinary(source, "v1.0.0", layout); err == nil || !strings.Contains(err.Error(), "stop before publication") {
		t.Fatalf("install error = %v", err)
	}
	beforeInstallReleasePublication = nil
	for _, path := range []string{releaseDir(layout, "v1.0.0"), journalPath(layout)} {
		if _, err := os.Lstat(path); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("rollback artifact remains at %s: %v", path, err)
		}
	}
}

func TestInstallJournalRecoveryRejectsTrailingData(t *testing.T) {
	layout := testInstallLayout(t)
	root := filepath.Dir(layout.DataDir)
	source := testReleaseSource(t, root, "one")
	if _, err := InstallBinary(source, "v1.0.0", layout); err != nil {
		t.Fatal(err)
	}
	journal := testCurrentInstallJournal(t, layout)
	data, err := json.Marshal(journal)
	if err != nil {
		t.Fatal(err)
	}
	data = append(data, []byte("\n{}\n")...)
	if err := os.WriteFile(journalPath(layout), data, 0o600); err != nil {
		t.Fatal(err)
	}

	beforeCurrent, err := os.Readlink(currentPath(layout))
	if err != nil {
		t.Fatal(err)
	}
	if err := recoverInstallJournal(layout); err == nil || !strings.Contains(err.Error(), "trailing data") {
		t.Fatalf("recovery error = %v", err)
	}
	if current, err := os.Readlink(currentPath(layout)); err != nil || current != beforeCurrent {
		t.Fatalf("current changed: %q, %v", current, err)
	}
	if _, err := os.Lstat(journalPath(layout)); err != nil {
		t.Fatalf("invalid journal was removed: %v", err)
	}
}

func TestInstallJournalRecoveryRequiresExactLauncherSnapshots(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(InstallLayout, *installJournal)
	}{
		{
			name: "missing",
			mutate: func(layout InstallLayout, journal *installJournal) {
				delete(journal.PriorLaunchers, stableLauncherPaths(layout)[0])
			},
		},
		{
			name: "extra",
			mutate: func(layout InstallLayout, journal *installJournal) {
				journal.PriorLaunchers[filepath.Join(filepath.Dir(layout.BinDir), "outside")] = linkSnapshot{}
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			layout := testInstallLayout(t)
			root := filepath.Dir(layout.DataDir)
			source := testReleaseSource(t, root, "one")
			if _, err := InstallBinary(source, "v1.0.0", layout); err != nil {
				t.Fatal(err)
			}
			journal := testCurrentInstallJournal(t, layout)
			test.mutate(layout, &journal)
			if err := writeJSONPrivate(journalPath(layout), journal); err != nil {
				t.Fatal(err)
			}

			if err := recoverInstallJournal(layout); err == nil || !strings.Contains(err.Error(), "launcher snapshots") {
				t.Fatalf("recovery error = %v", err)
			}
			if _, err := os.Lstat(journalPath(layout)); err != nil {
				t.Fatalf("invalid journal was removed: %v", err)
			}
		})
	}
}

func TestInstallJournalCompleteRequiresIntactRelease(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(t *testing.T, layout InstallLayout)
	}{
		{
			name: "missing",
			mutate: func(t *testing.T, layout InstallLayout) {
				t.Helper()
				if err := os.RemoveAll(releaseDir(layout, "v1.0.0")); err != nil {
					t.Fatal(err)
				}
			},
		},
		{
			name: "modified",
			mutate: func(t *testing.T, layout InstallLayout) {
				t.Helper()
				if err := os.WriteFile(releaseBinary(layout, "v1.0.0"), []byte("changed"), 0o755); err != nil {
					t.Fatal(err)
				}
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			layout := testInstallLayout(t)
			root := filepath.Dir(layout.DataDir)
			source := testReleaseSource(t, root, "one")
			if _, err := InstallBinary(source, "v1.0.0", layout); err != nil {
				t.Fatal(err)
			}
			journal := testCurrentInstallJournal(t, layout)
			test.mutate(t, layout)
			if installJournalComplete(layout, journal) {
				t.Fatal("damaged release was accepted as committed")
			}
		})
	}
}

func TestInstallRecoversPublishedReleaseAfterPublicationSyncFailure(t *testing.T) {
	layout := testInstallLayout(t)
	root := filepath.Dir(layout.DataDir)
	source := testReleaseSource(t, root, "one")
	originalSync := syncInstallReleaseDirectory
	calls := 0
	syncInstallReleaseDirectory = func(directory *fssecure.Directory) error {
		calls++
		if calls == 1 {
			return errors.New("injected release sync failure")
		}
		return directory.Sync()
	}
	t.Cleanup(func() { syncInstallReleaseDirectory = originalSync })
	if _, err := InstallBinary(source, "v1.0.0", layout); err == nil || !strings.Contains(err.Error(), "injected release sync failure") {
		t.Fatalf("install error = %v", err)
	}
	syncInstallReleaseDirectory = originalSync
	if _, err := InstallBinary(source, "v1.0.0", layout); err != nil {
		t.Fatalf("retry install: %v", err)
	}
	if _, err := os.Lstat(journalPath(layout)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("journal remains after retry: %v", err)
	}
}

func TestUpdateDoesNotRewriteStableLaunchers(t *testing.T) {
	layout := testInstallLayout(t)
	root := filepath.Dir(layout.DataDir)
	first := testReleaseSource(t, root, "one")
	second := testReleaseSource(t, root, "two")
	if _, err := InstallBinary(first, "v1.0.0", layout); err != nil {
		t.Fatal(err)
	}
	before := map[string]os.FileInfo{}
	for _, path := range stableLauncherPaths(layout) {
		info, err := os.Lstat(path)
		if err != nil {
			t.Fatal(err)
		}
		before[path] = info
	}
	if _, err := InstallBinary(second, "v2.0.0", layout); err != nil {
		t.Fatal(err)
	}
	for path, oldInfo := range before {
		newInfo, err := os.Lstat(path)
		if err != nil {
			t.Fatal(err)
		}
		if !os.SameFile(oldInfo, newInfo) {
			t.Fatalf("stable launcher %s was replaced during update", path)
		}
	}
}

func TestUninstallPreflightsAllOwnershipBeforeMutation(t *testing.T) {
	layout := testInstallLayout(t)
	root := filepath.Dir(layout.DataDir)
	source := testReleaseSource(t, root, "one")
	if _, err := InstallBinary(source, "v1.0.0", layout); err != nil {
		t.Fatal(err)
	}
	modified := stableLauncherPaths(layout)[1]
	if err := os.Remove(modified); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(modified, []byte("user replacement"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := Uninstall(layout, false); err == nil {
		t.Fatal("uninstall accepted a modified launcher")
	}
	if _, err := os.Lstat(stableLauncherPaths(layout)[0]); err != nil {
		t.Fatalf("preflight failure removed the first launcher: %v", err)
	}
	if _, err := os.Lstat(currentPath(layout)); err != nil {
		t.Fatalf("preflight failure removed current: %v", err)
	}
}

func TestUninstallAllowsMissingInactiveHistoricalRelease(t *testing.T) {
	layout := testInstallLayout(t)
	root := filepath.Dir(layout.DataDir)
	for index, tag := range []string{"v1.0.0", "v2.0.0"} {
		source := testReleaseSource(t, root, string(rune('a'+index)))
		if _, err := InstallBinary(source, tag, layout); err != nil {
			t.Fatal(err)
		}
	}
	ledger, err := LoadInstallLedger(ledgerPath(layout))
	if err != nil {
		t.Fatal(err)
	}
	ledger.Releases["v0.9.0"] = strings.Repeat("a", 64)
	if err := writeLedger(layout, ledger); err != nil {
		t.Fatal(err)
	}

	if err := Uninstall(layout, false); err != nil {
		t.Fatalf("uninstall with missing inactive release: %v", err)
	}
}

func TestUninstallRequiresCurrentAndPreviousReleases(t *testing.T) {
	for _, missing := range []string{"current", "previous"} {
		t.Run(missing, func(t *testing.T) {
			layout := testInstallLayout(t)
			root := filepath.Dir(layout.DataDir)
			for index, tag := range []string{"v1.0.0", "v2.0.0"} {
				source := testReleaseSource(t, root, string(rune('a'+index)))
				if _, err := InstallBinary(source, tag, layout); err != nil {
					t.Fatal(err)
				}
			}
			ledger, err := LoadInstallLedger(ledgerPath(layout))
			if err != nil {
				t.Fatal(err)
			}
			tag := ledger.Current
			if missing == "previous" {
				tag = ledger.Previous
			}
			if err := os.RemoveAll(releaseDir(layout, tag)); err != nil {
				t.Fatal(err)
			}

			if err := Uninstall(layout, false); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("uninstall error = %v", err)
			}
			if _, err := os.Lstat(stableLauncherPaths(layout)[0]); err != nil {
				t.Fatalf("preflight failure removed launcher: %v", err)
			}
		})
	}
}

func TestUninstallRejectsModifiedInactiveHistoricalRelease(t *testing.T) {
	layout := testInstallLayout(t)
	root := filepath.Dir(layout.DataDir)
	for index, tag := range []string{"v1.0.0", "v2.0.0"} {
		source := testReleaseSource(t, root, string(rune('a'+index)))
		if _, err := InstallBinary(source, tag, layout); err != nil {
			t.Fatal(err)
		}
	}
	ledger, err := LoadInstallLedger(ledgerPath(layout))
	if err != nil {
		t.Fatal(err)
	}
	historical := "v0.9.0"
	historicalDir := releaseDir(layout, historical)
	if err := os.MkdirAll(historicalDir, 0o755); err != nil {
		t.Fatal(err)
	}
	original := []byte("historical")
	if err := os.WriteFile(filepath.Join(historicalDir, "sclaude"), original, 0o755); err != nil {
		t.Fatal(err)
	}
	ledger.Releases[historical] = hexDigest(original)
	if err := writeLedger(layout, ledger); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(historicalDir, "sclaude"), []byte("modified"), 0o755); err != nil {
		t.Fatal(err)
	}

	if err := Uninstall(layout, false); err == nil || !strings.Contains(err.Error(), "was modified") {
		t.Fatalf("uninstall error = %v", err)
	}
	if _, err := os.Lstat(stableLauncherPaths(layout)[0]); err != nil {
		t.Fatalf("preflight failure removed launcher: %v", err)
	}
}

func TestUninstallFailureRestoresPriorState(t *testing.T) {
	layout := testInstallLayout(t)
	root := filepath.Dir(layout.DataDir)
	source := testReleaseSource(t, root, "one")
	if _, err := InstallBinary(source, "v1.0.0", layout); err != nil {
		t.Fatal(err)
	}
	beforeLedger, err := os.ReadFile(ledgerPath(layout))
	if err != nil {
		t.Fatal(err)
	}
	ops := defaultUninstallOps()
	calls := 0
	ops.rename = func(oldPath, newPath string) error {
		calls++
		if calls == 2 {
			return errors.New("injected uninstall failure")
		}
		return os.Rename(oldPath, newPath)
	}
	if err := uninstallWithOps(testPathsForLayout(layout), layout, false, nil, ops); err == nil || !strings.Contains(err.Error(), "injected uninstall failure") {
		t.Fatalf("uninstall error = %v", err)
	}
	for _, path := range append(stableLauncherPaths(layout), currentPath(layout)) {
		if _, err := os.Lstat(path); err != nil {
			t.Fatalf("restored path %s: %v", path, err)
		}
	}
	if _, err := os.Stat(releaseBinary(layout, "v1.0.0")); err != nil {
		t.Fatalf("restored release: %v", err)
	}
	afterLedger, err := os.ReadFile(ledgerPath(layout))
	if err != nil {
		t.Fatal(err)
	}
	if string(afterLedger) != string(beforeLedger) {
		t.Fatal("ledger changed after failed uninstall")
	}
	if _, err := os.Lstat(uninstallJournalPath(layout)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("uninstall journal remains after rollback: %v", err)
	}
}

func TestUninstallRecoversInterruptedTransaction(t *testing.T) {
	layout := testInstallLayout(t)
	root := filepath.Dir(layout.DataDir)
	source := testReleaseSource(t, root, "one")
	if _, err := InstallBinary(source, "v1.0.0", layout); err != nil {
		t.Fatal(err)
	}
	ledger, err := LoadInstallLedger(ledgerPath(layout))
	if err != nil {
		t.Fatal(err)
	}
	journal, prepared, err := prepareUninstall(testPathsForLayout(layout), layout, ledger, false)
	if err != nil {
		t.Fatal(err)
	}
	if err := writeJSONPrivate(uninstallJournalPath(layout), journal); err != nil {
		t.Fatal(err)
	}
	if err := applyPreparedUninstallEntry(prepared[0], defaultUninstallOps()); err != nil {
		t.Fatal(err)
	}
	if _, err := InstallBinary(source, "v1.0.0", layout); err != nil {
		t.Fatalf("install did not recover interrupted uninstall: %v", err)
	}
	if _, err := os.Lstat(journal.Entries[0].Backup); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("backup remains after recovery: %v", err)
	}
	if _, err := os.Lstat(uninstallJournalPath(layout)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("uninstall journal remains after recovery: %v", err)
	}
}

func TestUninstallCommitFailureRestoresPriorState(t *testing.T) {
	layout := testInstallLayout(t)
	root := filepath.Dir(layout.DataDir)
	source := testReleaseSource(t, root, "one")
	if _, err := InstallBinary(source, "v1.0.0", layout); err != nil {
		t.Fatal(err)
	}
	ops := defaultUninstallOps()
	writes := 0
	ops.writeJournal = func(path string, value any) error {
		writes++
		if writes == 2 {
			return errors.New("injected commit failure")
		}
		return writeJSONPrivate(path, value)
	}
	if err := uninstallWithOps(testPathsForLayout(layout), layout, false, nil, ops); err == nil || !strings.Contains(err.Error(), "injected commit failure") {
		t.Fatalf("uninstall error = %v", err)
	}
	if _, err := os.Lstat(currentPath(layout)); err != nil {
		t.Fatalf("current was not restored: %v", err)
	}
	if _, err := os.Stat(releaseBinary(layout, "v1.0.0")); err != nil {
		t.Fatalf("release was not restored: %v", err)
	}
	if _, err := os.Stat(ledgerPath(layout)); err != nil {
		t.Fatalf("ledger was not preserved: %v", err)
	}
}

func TestUninstallCommitDurabilityErrorFinalizesCommittedState(t *testing.T) {
	layout := testInstallLayout(t)
	root := filepath.Dir(layout.DataDir)
	source := testReleaseSource(t, root, "one")
	if _, err := InstallBinary(source, "v1.0.0", layout); err != nil {
		t.Fatal(err)
	}
	ops := defaultUninstallOps()
	writes := 0
	ops.writeJournal = func(path string, value any) error {
		writes++
		if err := writeJSONPrivate(path, value); err != nil {
			return err
		}
		if writes == 2 {
			return errors.New("injected durability failure")
		}
		return nil
	}
	if err := uninstallWithOps(testPathsForLayout(layout), layout, false, nil, ops); err != nil {
		t.Fatalf("committed uninstall did not finalize: %v", err)
	}
	for _, path := range append(stableLauncherPaths(layout), currentPath(layout)) {
		if _, err := os.Lstat(path); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("committed path remains at %s: %v", path, err)
		}
	}
	if _, err := os.Lstat(ledgerPath(layout)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("ledger remains after committed uninstall: %v", err)
	}
}

func TestInstallContinuesAfterCommittedUninstallRecovery(t *testing.T) {
	layout := testInstallLayout(t)
	root := filepath.Dir(layout.DataDir)
	first := testReleaseSource(t, root, "one")
	second := testReleaseSource(t, root, "two")
	if _, err := InstallBinary(first, "v1.0.0", layout); err != nil {
		t.Fatal(err)
	}
	ledger, err := LoadInstallLedger(ledgerPath(layout))
	if err != nil {
		t.Fatal(err)
	}
	journal, prepared, err := prepareUninstall(testPathsForLayout(layout), layout, ledger, false)
	if err != nil {
		t.Fatal(err)
	}
	if err := writeJSONPrivate(uninstallJournalPath(layout), journal); err != nil {
		t.Fatal(err)
	}
	for _, entry := range prepared {
		if err := applyPreparedUninstallEntry(entry, defaultUninstallOps()); err != nil {
			t.Fatal(err)
		}
	}
	journal.Committed = true
	if err := writeJSONPrivate(uninstallJournalPath(layout), journal); err != nil {
		t.Fatal(err)
	}

	installed, err := InstallBinary(second, "v2.0.0", layout)
	if err != nil {
		t.Fatalf("install after committed uninstall recovery: %v", err)
	}
	if installed.Current != "v2.0.0" {
		t.Fatalf("current release = %q", installed.Current)
	}
	if _, err := os.Stat(releaseBinary(layout, "v2.0.0")); err != nil {
		t.Fatalf("new release missing: %v", err)
	}
}

func TestUninstallRetryCompletesCommittedTransaction(t *testing.T) {
	layout := testInstallLayout(t)
	root := filepath.Dir(layout.DataDir)
	source := testReleaseSource(t, root, "one")
	if _, err := InstallBinary(source, "v1.0.0", layout); err != nil {
		t.Fatal(err)
	}
	ledger, err := LoadInstallLedger(ledgerPath(layout))
	if err != nil {
		t.Fatal(err)
	}
	journal, prepared, err := prepareUninstall(testPathsForLayout(layout), layout, ledger, false)
	if err != nil {
		t.Fatal(err)
	}
	if err := writeJSONPrivate(uninstallJournalPath(layout), journal); err != nil {
		t.Fatal(err)
	}
	for _, entry := range prepared {
		if err := applyPreparedUninstallEntry(entry, defaultUninstallOps()); err != nil {
			t.Fatal(err)
		}
	}
	journal.Committed = true
	if err := writeJSONPrivate(uninstallJournalPath(layout), journal); err != nil {
		t.Fatal(err)
	}

	if err := Uninstall(layout, false); err != nil {
		t.Fatalf("retry committed uninstall: %v", err)
	}
}

func TestUninstallInstalledFinalizesCommittedCustomBinWithoutLedgerOrRuntime(t *testing.T) {
	layout, _ := defaultUninstallTestLayout(t)
	customBin := filepath.Join(t.TempDir(), "custom-bin")
	layout.BinDir = customBin
	paths, err := configpkg.DefaultPaths()
	if err != nil {
		t.Fatal(err)
	}
	source := testReleaseSource(t, t.TempDir(), "one")
	if _, err := InstallBinary(source, "v1.0.0", layout); err != nil {
		t.Fatal(err)
	}
	ledger, err := LoadInstallLedger(ledgerPath(layout))
	if err != nil {
		t.Fatal(err)
	}
	journal, prepared, err := prepareUninstall(paths, layout, ledger, false)
	if err != nil {
		t.Fatal(err)
	}
	if err := writeJSONPrivate(uninstallJournalPath(layout), journal); err != nil {
		t.Fatal(err)
	}
	for _, entry := range prepared {
		if err := applyPreparedUninstallEntry(entry, defaultUninstallOps()); err != nil {
			t.Fatal(err)
		}
	}
	journal.Committed = true
	if err := writeJSONPrivate(uninstallJournalPath(layout), journal); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(ledgerPath(layout)); err != nil {
		t.Fatal(err)
	}
	inactiveCalled := false
	if err := UninstallInstalled(paths, false, func() error {
		inactiveCalled = true
		return errors.New("inactivity callback must not run")
	}); err != nil {
		t.Fatalf("finalize committed custom-bin uninstall: %v", err)
	}
	if inactiveCalled {
		t.Fatal("committed recovery invoked inactivity callback")
	}
	for _, path := range append(stableLauncherPaths(layout), currentPath(layout)) {
		if _, err := os.Lstat(path); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("committed path remains at %s: %v", path, err)
		}
	}
}

func TestUninstallInstalledRestoresUncommittedJournalBeforeInactivityProof(t *testing.T) {
	layout, _ := defaultUninstallTestLayout(t)
	paths, err := configpkg.DefaultPaths()
	if err != nil {
		t.Fatal(err)
	}
	source := testReleaseSource(t, t.TempDir(), "one")
	if _, err := InstallBinary(source, "v1.0.0", layout); err != nil {
		t.Fatal(err)
	}
	ledger, err := LoadInstallLedger(ledgerPath(layout))
	if err != nil {
		t.Fatal(err)
	}
	journal, prepared, err := prepareUninstall(paths, layout, ledger, false)
	if err != nil {
		t.Fatal(err)
	}
	if err := writeJSONPrivate(uninstallJournalPath(layout), journal); err != nil {
		t.Fatal(err)
	}
	if err := applyPreparedUninstallEntry(prepared[0], defaultUninstallOps()); err != nil {
		t.Fatal(err)
	}
	callbackErr := errors.New("stop after recovery")
	err = UninstallInstalled(paths, false, func() error {
		if _, statErr := os.Lstat(journal.Entries[0].Path); statErr != nil {
			t.Fatalf("original was not restored before inactivity proof: %v", statErr)
		}
		if _, statErr := os.Lstat(journal.Entries[0].Backup); !errors.Is(statErr, os.ErrNotExist) {
			t.Fatalf("backup remains before inactivity proof: %v", statErr)
		}
		if _, statErr := os.Lstat(uninstallJournalPath(layout)); !errors.Is(statErr, os.ErrNotExist) {
			t.Fatalf("journal remains before inactivity proof: %v", statErr)
		}
		return callbackErr
	})
	if !errors.Is(err, callbackErr) {
		t.Fatalf("uninstall error = %v", err)
	}
	if _, err := os.Lstat(currentPath(layout)); err != nil {
		t.Fatalf("install was not restored: %v", err)
	}
}

type lifecycleScreen struct {
	store session.Store
}

func (screen lifecycleScreen) List(context.Context) ([]screenpkg.Socket, error) {
	return nil, nil
}

func (screen lifecycleScreen) Start(_ context.Context, _, _, _, _, id string) error {
	_, err := screen.store.MarkRunning(id, 42, time.Now())
	return err
}

func (lifecycleScreen) Attach(context.Context, string, string) error {
	return nil
}

func (lifecycleScreen) Stop(context.Context, string) error {
	return nil
}

func TestSessionCreationWinsLifecycleAdmissionBeforeUninstall(t *testing.T) {
	layout := testInstallLayout(t)
	paths := testPathsForLayout(layout)
	source := testReleaseSource(t, filepath.Dir(layout.DataDir), "one")
	if _, err := InstallBinary(source, "v1.0.0", layout); err != nil {
		t.Fatal(err)
	}
	store := session.NewStore(paths.StateRoot)
	creatorLocked := make(chan struct{})
	releaseCreator := make(chan struct{})
	manager := session.Manager{
		Store:      store,
		Screen:     lifecycleScreen{store: store},
		BinaryPath: "/bin/sclaude",
		AdmissionHooks: stateroot.Hooks{AfterLock: func(string) error {
			close(creatorLocked)
			<-releaseCreator
			return nil
		}},
	}
	createDone := make(chan error, 1)
	go func() {
		_, err := manager.Create(context.Background(), "Creation Wins", "claude", t.TempDir(), nil, true)
		createDone <- err
	}()
	select {
	case <-creatorLocked:
	case <-time.After(5 * time.Second):
		t.Fatal("creator did not acquire lifecycle admission")
	}
	uninstallStarted := make(chan struct{})
	uninstallDone := make(chan error, 1)
	go func() {
		close(uninstallStarted)
		uninstallDone <- uninstallWithOps(paths, layout, false, func() error {
			records, errs := store.List()
			if len(errs) > 0 {
				return errors.Join(errs...)
			}
			for _, record := range records {
				if record.Active() {
					return errors.New("active session blocks uninstall")
				}
			}
			return nil
		}, defaultUninstallOps())
	}()
	<-uninstallStarted
	select {
	case err := <-uninstallDone:
		t.Fatalf("uninstall completed before creator released admission: %v", err)
	case <-time.After(100 * time.Millisecond):
	}
	close(releaseCreator)
	if err := <-createDone; err != nil {
		t.Fatalf("create session: %v", err)
	}
	if err := <-uninstallDone; err == nil || !strings.Contains(err.Error(), "active session") {
		t.Fatalf("uninstall error = %v", err)
	}
	for _, path := range append(stableLauncherPaths(layout), currentPath(layout)) {
		if _, err := os.Lstat(path); err != nil {
			t.Fatalf("installed artifact changed at %s: %v", path, err)
		}
	}
}

func TestPurgeWinsLifecycleAdmissionBeforeStaleSessionCreation(t *testing.T) {
	layout, _ := defaultUninstallTestLayout(t)
	paths, err := configpkg.DefaultPaths()
	if err != nil {
		t.Fatal(err)
	}
	source := testReleaseSource(t, t.TempDir(), "one")
	if _, err := InstallBinary(source, "v1.0.0", layout); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(paths.ConfigFile), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(paths.ConfigFile, []byte("runtime\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	purgeLocked := make(chan struct{})
	releasePurge := make(chan struct{})
	beforeUninstallStatePurge = func(string) error {
		close(purgeLocked)
		<-releasePurge
		return nil
	}
	t.Cleanup(func() { beforeUninstallStatePurge = nil })
	purgeDone := make(chan error, 1)
	go func() {
		purgeDone <- uninstallWithOps(paths, layout, true, func() error { return nil }, defaultUninstallOps())
	}()
	select {
	case <-purgeLocked:
	case <-time.After(5 * time.Second):
		t.Fatal("purge did not acquire lifecycle admission")
	}
	store := session.NewStore(paths.StateRoot)
	creatorWaiting := make(chan struct{}, 1)
	manager := session.Manager{
		Store:      store,
		Screen:     lifecycleScreen{store: store},
		BinaryPath: "/bin/sclaude",
		AdmissionHooks: stateroot.Hooks{BeforeFlock: func(string) error {
			creatorWaiting <- struct{}{}
			return nil
		}},
		AdmitCreate: func(context.Context) error {
			if _, err := os.Lstat(paths.ConfigFile); errors.Is(err, os.ErrNotExist) {
				return errors.New("runtime removed")
			}
			return errors.New("runtime unexpectedly remained")
		},
	}
	createDone := make(chan error, 1)
	go func() {
		_, err := manager.Create(context.Background(), "Purge Wins", "claude", t.TempDir(), nil, true)
		createDone <- err
	}()
	select {
	case <-creatorWaiting:
	case <-time.After(5 * time.Second):
		t.Fatal("creator did not reach lifecycle admission")
	}
	close(releasePurge)
	if err := <-purgeDone; err != nil {
		t.Fatalf("purge: %v", err)
	}
	if err := <-createDone; err == nil || !strings.Contains(err.Error(), "runtime removed") {
		t.Fatalf("create error = %v", err)
	}
	for _, path := range []string{paths.SessionsDir, paths.LaunchDir, filepath.Join(paths.StateRoot, "lock")} {
		if _, err := os.Lstat(path); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("stale creation recreated state at %s: %v", path, err)
		}
	}
}

func TestUninstallJournalFailsClosed(t *testing.T) {
	tests := []struct {
		name    string
		prepare func(t *testing.T, path string, journal uninstallJournal)
		want    string
	}{
		{
			name: "corrupt",
			prepare: func(t *testing.T, path string, _ uninstallJournal) {
				t.Helper()
				if err := os.WriteFile(path, []byte("{broken\n"), 0o600); err != nil {
					t.Fatal(err)
				}
			},
			want: "parse uninstall transaction journal",
		},
		{
			name: "symlink",
			prepare: func(t *testing.T, path string, journal uninstallJournal) {
				t.Helper()
				target := filepath.Join(t.TempDir(), "journal.json")
				if err := writeJSONPrivate(target, journal); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(target, path); err != nil {
					t.Fatal(err)
				}
			},
			want: "too many levels of symbolic links",
		},
		{
			name: "mismatched layout",
			prepare: func(t *testing.T, path string, journal uninstallJournal) {
				t.Helper()
				journal.Layout.DataDir = filepath.Join(t.TempDir(), "other-data")
				if err := writeJSONPrivate(path, journal); err != nil {
					t.Fatal(err)
				}
			},
			want: "data/state paths do not match",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			layout := testInstallLayout(t)
			paths := testPathsForLayout(layout)
			if err := os.MkdirAll(layout.StateDir, 0o700); err != nil {
				t.Fatal(err)
			}
			if err := os.Chmod(paths.StateRoot, 0o700); err != nil {
				t.Fatal(err)
			}
			journal := uninstallJournal{
				SchemaVersion: uninstallJournalSchema,
				Layout:        layout,
				Token:         strings.Repeat("a", 32),
				Committed:     true,
			}
			test.prepare(t, uninstallJournalPath(layout), journal)
			called := false
			err := uninstallWithOps(paths, layout, false, func() error {
				called = true
				return nil
			}, defaultUninstallOps())
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("uninstall error = %v", err)
			}
			if called {
				t.Fatal("invalid journal reached inactivity proof")
			}
		})
	}
}

func defaultUninstallTestLayout(t *testing.T) (InstallLayout, string) {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, "config"))
	t.Setenv("XDG_STATE_HOME", filepath.Join(home, "state"))
	t.Setenv("XDG_DATA_HOME", filepath.Join(home, "data"))
	layout, err := DefaultInstallLayout(filepath.Join(home, "bin"))
	if err != nil {
		t.Fatal(err)
	}
	return layout, filepath.Join(home, "state", "sclaude")
}

func TestPurgeRetainsAdmissionAnchorWhileAnotherSetupWaits(t *testing.T) {
	layout, stateRoot := defaultUninstallTestLayout(t)
	paths, err := configpkg.DefaultPaths()
	if err != nil {
		t.Fatal(err)
	}
	if paths.StateRoot != stateRoot || paths.InstallState != layout.StateDir {
		t.Fatalf("runtime paths do not match test layout: %+v, %+v", paths, layout)
	}
	for _, path := range []string{
		paths.SessionsDir,
		paths.LaunchDir,
		filepath.Join(paths.StateRoot, "lock"),
		paths.InstallState,
	} {
		if err := os.MkdirAll(path, 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(path, "owned"), []byte("owned\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Chmod(stateRoot, 0o700); err != nil {
		t.Fatal(err)
	}
	sentinel := filepath.Join(stateRoot, "unrelated-sentinel")
	if err := os.WriteFile(sentinel, []byte("preserve\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	journal := uninstallJournal{
		SchemaVersion: uninstallJournalSchema,
		Layout:        layout,
		Token:         strings.Repeat("a", 32),
		Purge:         true,
		Committed:     true,
	}
	if err := writeJSONPrivate(uninstallJournalPath(layout), journal); err != nil {
		t.Fatal(err)
	}
	beforeInfo, err := os.Lstat(stateRoot)
	if err != nil {
		t.Fatal(err)
	}

	type lockResult struct {
		lock *setupAdmissionLock
		err  error
	}
	waiterReady := make(chan struct{}, 1)
	waiterDone := make(chan lockResult, 1)
	beforeUninstallStatePurge = func(path string) error {
		if path != stateRoot {
			t.Fatalf("purge path = %q, want %q", path, stateRoot)
		}
		beforeSetupAdmissionFlock = func(lockPath string) error {
			if lockPath != stateRoot {
				return nil
			}
			waiterReady <- struct{}{}
			return nil
		}
		go func() {
			lock, lockErr := acquireSetupAdmissionLock(stateRoot, true)
			waiterDone <- lockResult{lock: lock, err: lockErr}
		}()
		select {
		case <-waiterReady:
			return nil
		case <-time.After(5 * time.Second):
			return errors.New("concurrent setup did not reach the admission flock")
		}
	}
	t.Cleanup(func() {
		beforeUninstallStatePurge = nil
		beforeSetupAdmissionFlock = nil
	})

	if err := uninstallWithOps(testPathsForLayout(layout), layout, true, nil, defaultUninstallOps()); err != nil {
		t.Fatal(err)
	}
	beforeUninstallStatePurge = nil
	beforeSetupAdmissionFlock = nil

	var waiter lockResult
	select {
	case waiter = <-waiterDone:
	case <-time.After(5 * time.Second):
		t.Fatal("concurrent setup did not acquire the retained admission anchor")
	}
	if waiter.lock != nil {
		defer waiter.lock.release()
	}
	if waiter.err != nil {
		t.Fatalf("concurrent setup admission error = %v", waiter.err)
	}
	afterInfo, err := os.Lstat(stateRoot)
	if err != nil {
		t.Fatal(err)
	}
	if !os.SameFile(beforeInfo, afterInfo) {
		t.Fatal("purge replaced the setup admission anchor inode")
	}
	if afterInfo.Mode() != os.ModeDir|0o700 {
		t.Fatalf("state root mode = %v, want drwx------", afterInfo.Mode())
	}
	if waiter.lock == nil || !os.SameFile(beforeInfo, waiter.lock.directory.Info()) {
		t.Fatal("concurrent setup acquired a different admission anchor inode")
	}
	for _, path := range []string{
		paths.SessionsDir,
		paths.LaunchDir,
		filepath.Join(paths.StateRoot, "lock"),
		paths.InstallState,
	} {
		if _, statErr := os.Lstat(path); !errors.Is(statErr, os.ErrNotExist) {
			t.Fatalf("purged state child remains at %s: %v", path, statErr)
		}
	}
	data, err := os.ReadFile(sentinel)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "preserve\n" {
		t.Fatalf("sentinel contents = %q", data)
	}
}

func TestPurgeRejectsStateRootReplacementAfterAdmission(t *testing.T) {
	layout, stateRoot := defaultUninstallTestLayout(t)
	root := filepath.Dir(stateRoot)
	if err := os.MkdirAll(layout.StateDir, 0o700); err != nil {
		t.Fatal(err)
	}
	journal := uninstallJournal{
		SchemaVersion: uninstallJournalSchema,
		Layout:        layout,
		Token:         strings.Repeat("a", 32),
		Purge:         true,
		Committed:     true,
	}
	if err := writeJSONPrivate(uninstallJournalPath(layout), journal); err != nil {
		t.Fatal(err)
	}
	original := filepath.Join(root, "original-state-root")
	beforeUninstallStatePurge = func(path string) error {
		if path != stateRoot {
			t.Fatalf("purge path = %q, want %q", path, stateRoot)
		}
		if err := os.Rename(stateRoot, original); err != nil {
			return err
		}
		return os.Mkdir(stateRoot, 0o700)
	}
	t.Cleanup(func() { beforeUninstallStatePurge = nil })

	err := uninstallWithOps(testPathsForLayout(layout), layout, true, nil, defaultUninstallOps())
	beforeUninstallStatePurge = nil
	if err == nil || !strings.Contains(err.Error(), "changed while holding") {
		t.Fatalf("purge error = %v", err)
	}
	if _, statErr := os.Lstat(stateRoot); statErr != nil {
		t.Fatalf("replacement state root was removed: %v", statErr)
	}
	if _, statErr := os.Lstat(original); statErr != nil {
		t.Fatalf("original state root was removed: %v", statErr)
	}
}

func TestPurgeRefusesPendingSetupTransaction(t *testing.T) {
	layout, stateRoot := defaultUninstallTestLayout(t)
	if err := ensureInstallDirectories(layout); err != nil {
		t.Fatal(err)
	}
	journal := setupJournal{
		SchemaVersion: setupJournalSchema,
		Token:         strings.Repeat("b", 32),
		Entries: []setupJournalEntry{{
			Path:        filepath.Join(filepath.Dir(stateRoot), "target"),
			Backup:      setupBackupPath(filepath.Join(filepath.Dir(stateRoot), "target"), strings.Repeat("b", 32)),
			Staged:      setupStagedPath(filepath.Join(filepath.Dir(stateRoot), "target"), strings.Repeat("b", 32)),
			Replacement: setupReplacementPath(filepath.Join(filepath.Dir(stateRoot), "target"), strings.Repeat("b", 32)),
			AfterMode:   0o600,
			AfterDigest: strings.Repeat("c", 64),
		}},
	}
	if err := writeSetupJournal(setupJournalPath(stateRoot), journal); err != nil {
		t.Fatal(err)
	}

	err := uninstallWithOps(testPathsForLayout(layout), layout, true, nil, defaultUninstallOps())
	if err == nil || !strings.Contains(err.Error(), "requires recovery before uninstall") {
		t.Fatalf("purge error = %v", err)
	}
	if _, statErr := os.Lstat(setupJournalPath(stateRoot)); statErr != nil {
		t.Fatalf("pending setup journal was removed: %v", statErr)
	}
}

func TestPurgeLockCoversRuntimeStateRoot(t *testing.T) {
	layout := testInstallLayout(t)
	stateRoot := filepath.Dir(layout.StateDir)
	if lockPath(layout) != filepath.Dir(stateRoot) {
		t.Fatalf("lock path %q does not guard state root %q", lockPath(layout), stateRoot)
	}
	if !pathsOverlap(stateRoot, layout.StateDir) {
		t.Fatalf("test layout state root %q does not contain install state %q", stateRoot, layout.StateDir)
	}
}

func TestInstallLockRejectsPathReplacementAfterAcquisition(t *testing.T) {
	layout := testInstallLayout(t)
	lockDir := lockPath(layout)
	if err := os.MkdirAll(lockDir, 0o700); err != nil {
		t.Fatal(err)
	}
	original := lockDir + "-original"
	afterInstallAdmissionLock = func(path string) error {
		if path != lockDir {
			return nil
		}
		if err := os.Rename(lockDir, original); err != nil {
			return err
		}
		return os.Mkdir(lockDir, 0o700)
	}
	t.Cleanup(func() { afterInstallAdmissionLock = nil })
	called := false
	err := withInstallLock(layout, func() error {
		called = true
		return nil
	})
	afterInstallAdmissionLock = nil
	if err == nil || !strings.Contains(err.Error(), "changed while acquiring") {
		t.Fatalf("lock error = %v", err)
	}
	if called {
		t.Fatal("lock callback ran after lock path replacement")
	}
}

func TestInstallLockRejectsSymlinkedLockDirectory(t *testing.T) {
	layout := testInstallLayout(t)
	if err := os.RemoveAll(lockPath(layout)); err != nil {
		t.Fatal(err)
	}
	target := t.TempDir()
	if err := os.Symlink(target, lockPath(layout)); err != nil {
		t.Fatal(err)
	}
	if err := withInstallLock(layout, func() error { return nil }); err == nil || !strings.Contains(err.Error(), "not a symlink") {
		t.Fatalf("symlinked lock error = %v", err)
	}
}

func TestThirdInstallPrunesDurableOwnershipAndUninstalls(t *testing.T) {
	layout := testInstallLayout(t)
	root := filepath.Dir(layout.DataDir)
	for index, tag := range []string{"v1.0.0", "v2.0.0", "v3.0.0"} {
		source := testReleaseSource(t, root, string(rune('a'+index)))
		if _, err := InstallBinary(source, tag, layout); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := os.Lstat(releaseDir(layout, "v1.0.0")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("old release remains: %v", err)
	}
	ledger, err := LoadInstallLedger(ledgerPath(layout))
	if err != nil {
		t.Fatal(err)
	}
	if _, exists := ledger.Releases["v1.0.0"]; exists {
		t.Fatalf("pruned release remains in ledger: %+v", ledger.Releases)
	}
	if err := Uninstall(layout, false); err != nil {
		t.Fatalf("uninstall after pruning: %v", err)
	}
}

func TestPruneCleanupFailureLeavesRecoverableJournal(t *testing.T) {
	layout := testInstallLayout(t)
	root := filepath.Dir(layout.DataDir)
	for index, tag := range []string{"v1.0.0", "v2.0.0"} {
		source := testReleaseSource(t, root, string(rune('a'+index)))
		if _, err := InstallBinary(source, tag, layout); err != nil {
			t.Fatal(err)
		}
	}
	third := testReleaseSource(t, root, "c")
	ops := defaultActivationOps()
	removed := false
	ops.removeRelease = func(artifact installReleaseArtifact) error {
		if !removed {
			removed = true
			return errors.New("injected prune cleanup failure")
		}
		return removeOwnedReleaseDirectory(artifact.Path, artifact.Digest, artifact.Identity)
	}
	result, err := installBinaryWithOps(third, "v3.0.0", layout, ops)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Warnings) == 0 || !strings.Contains(result.Warnings[0], "injected prune cleanup failure") {
		t.Fatalf("warnings = %v", result.Warnings)
	}
	if _, err := os.Lstat(journalPath(layout)); err != nil {
		t.Fatalf("recovery journal missing: %v", err)
	}
	ledger, err := LoadInstallLedger(ledgerPath(layout))
	if err != nil {
		t.Fatal(err)
	}
	if _, exists := ledger.Releases["v1.0.0"]; exists {
		t.Fatalf("stale ownership remains: %+v", ledger.Releases)
	}
	if _, err := InstallBinary(third, "v3.0.0", layout); err != nil {
		t.Fatalf("recovery retry: %v", err)
	}
	if _, err := os.Lstat(releaseDir(layout, "v1.0.0")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("old release remains after recovery: %v", err)
	}
	if _, err := os.Lstat(journalPath(layout)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("journal remains after recovery: %v", err)
	}
}

func TestPrunePreservesUnrecordedReleaseDirectory(t *testing.T) {
	layout := testInstallLayout(t)
	root := filepath.Dir(layout.DataDir)
	for index, tag := range []string{"v1.0.0", "v2.0.0", "v3.0.0"} {
		source := testReleaseSource(t, root, string(rune('a'+index)))
		if _, err := InstallBinary(source, tag, layout); err != nil {
			t.Fatal(err)
		}
	}
	unrecorded := releaseDir(layout, "v9.9.9")
	if err := os.MkdirAll(unrecorded, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(unrecorded, "notes"), []byte("user data"), 0o600); err != nil {
		t.Fatal(err)
	}
	fourth := testReleaseSource(t, root, "d")
	if _, err := InstallBinary(fourth, "v4.0.0", layout); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(unrecorded, "notes")); err != nil {
		t.Fatalf("unrecorded release directory was pruned: %v", err)
	}
}
