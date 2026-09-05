package setup

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ctrl-alt-raccoon/shellmates/internal/config"
)

func TestRunWorkflowAppliesShellThroughSetupTransaction(t *testing.T) {
	home, binDir, profile := setupExternalWorkflowFixture(t)
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, "config"))
	t.Setenv("XDG_STATE_HOME", filepath.Join(home, "state"))
	t.Setenv("XDG_DATA_HOME", filepath.Join(home, "data"))
	t.Setenv("SHELL", "/bin/zsh")
	t.Setenv("PATH", binDir)

	result, err := RunWorkflow(context.Background(), SetupOptions{
		BinDir:         filepath.Join(home, "installed-bin"),
		NonInteractive: true,
		SkipCodex:      true,
		SkipProxy:      true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !result.ExternalClaudex {
		t.Fatalf("unexpected workflow result: %+v", result)
	}
	data, err := os.ReadFile(profile)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), pathBlockStart) || !strings.Contains(string(data), filepath.Join(home, "installed-bin")) {
		t.Fatalf("shell profile was not transactionally updated:\n%s", data)
	}
}

func TestRunWorkflowShellFailureRollsBackRuntime(t *testing.T) {
	home, binDir, profile := setupExternalWorkflowFixture(t)
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, "config"))
	t.Setenv("XDG_STATE_HOME", filepath.Join(home, "state"))
	t.Setenv("XDG_DATA_HOME", filepath.Join(home, "data"))
	t.Setenv("SHELL", "/bin/zsh")
	t.Setenv("PATH", binDir)

	beforeApplySetupShell = func() error {
		return os.WriteFile(profile, []byte("external change\n"), 0o600)
	}
	t.Cleanup(func() { beforeApplySetupShell = nil })

	_, err := RunWorkflow(context.Background(), SetupOptions{
		BinDir:         filepath.Join(home, "installed-bin"),
		NonInteractive: true,
		SkipCodex:      true,
		SkipProxy:      true,
	})
	if err == nil || !strings.Contains(err.Error(), "changed after preparation") {
		t.Fatalf("workflow error = %v", err)
	}
	paths, pathErr := config.DefaultPaths()
	if pathErr != nil {
		t.Fatal(pathErr)
	}
	if _, statErr := os.Lstat(paths.ConfigFile); !os.IsNotExist(statErr) {
		t.Fatalf("runtime was published after shell failure: %v", statErr)
	}
	data, readErr := os.ReadFile(profile)
	if readErr != nil {
		t.Fatal(readErr)
	}
	if string(data) != "external change\n" {
		t.Fatalf("rollback overwrote the external shell change: %q", data)
	}
}

func TestSetupShellTransactionPreservesSymlinkAndOwnership(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, "config"))
	t.Setenv("XDG_STATE_HOME", filepath.Join(home, "state"))
	t.Setenv("XDG_DATA_HOME", filepath.Join(home, "data"))
	t.Setenv("SHELL", "/bin/zsh")

	binDir := filepath.Join(home, "installed-bin")
	layout, err := DefaultInstallLayout(binDir)
	if err != nil {
		t.Fatal(err)
	}
	source := filepath.Join(home, "sclaude-source")
	if err := os.WriteFile(source, []byte("binary"), 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := InstallBinary(source, "v1.0.0", layout); err != nil {
		t.Fatal(err)
	}

	target := filepath.Join(home, "shared-profile")
	profile := filepath.Join(home, ".zshrc")
	if err := os.WriteFile(target, []byte("export OTHER=1\n"), 0o640); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("shared-profile", profile); err != nil {
		t.Fatal(err)
	}

	prepared, err := prepareSetupShell(SetupOptions{BinDir: binDir})
	if err != nil {
		t.Fatal(err)
	}
	transaction, err := newSetupTransaction(filepath.Join(home, "state", "sclaude"))
	if err != nil {
		t.Fatal(err)
	}
	if err := prepared.addFiles(transaction); err != nil {
		t.Fatal(err)
	}
	if err := transaction.persist(); err != nil {
		t.Fatal(err)
	}
	if err := prepared.apply(transaction); err != nil {
		t.Fatal(err)
	}
	if err := transaction.commit(); err != nil {
		t.Fatal(err)
	}

	link, err := os.Readlink(profile)
	if err != nil {
		t.Fatal(err)
	}
	if link != "shared-profile" {
		t.Fatalf("shell symlink changed to %q", link)
	}
	targetInfo, err := os.Stat(target)
	if err != nil {
		t.Fatal(err)
	}
	if targetInfo.Mode().Perm() != 0o640 {
		t.Fatalf("shell target mode = %o, want 640", targetInfo.Mode().Perm())
	}
	ledger, err := LoadInstallLedger(ledgerPath(layout))
	if err != nil {
		t.Fatal(err)
	}
	ownership, ok := ledger.ShellBlocks[profile]
	if !ok || ownership.Digest == "" || ownership.Created {
		t.Fatalf("shell ownership = %+v, present=%v", ownership, ok)
	}
}

func TestSetupShellTransactionRollsBackShellWhenLedgerApplyFails(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, "config"))
	t.Setenv("XDG_STATE_HOME", filepath.Join(home, "state"))
	t.Setenv("XDG_DATA_HOME", filepath.Join(home, "data"))
	t.Setenv("SHELL", "/bin/zsh")

	binDir := filepath.Join(home, "installed-bin")
	layout, err := DefaultInstallLayout(binDir)
	if err != nil {
		t.Fatal(err)
	}
	source := filepath.Join(home, "sclaude-source")
	if err := os.WriteFile(source, []byte("binary"), 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := InstallBinary(source, "v1.0.0", layout); err != nil {
		t.Fatal(err)
	}

	profile := filepath.Join(home, ".zshrc")
	originalProfile := "export OTHER=1\n"
	if err := os.WriteFile(profile, []byte(originalProfile), 0o640); err != nil {
		t.Fatal(err)
	}
	ledgerBefore, err := os.ReadFile(ledgerPath(layout))
	if err != nil {
		t.Fatal(err)
	}

	prepared, err := prepareSetupShell(SetupOptions{BinDir: binDir})
	if err != nil {
		t.Fatal(err)
	}
	transaction, err := newSetupTransaction(filepath.Join(home, "state", "sclaude"))
	if err != nil {
		t.Fatal(err)
	}
	if err := prepared.addFiles(transaction); err != nil {
		t.Fatal(err)
	}
	if err := transaction.persist(); err != nil {
		t.Fatal(err)
	}

	primary := errors.New("injected ledger failure")
	beforeApplySetupLedger = func() error { return primary }
	t.Cleanup(func() { beforeApplySetupLedger = nil })

	if err := prepared.apply(transaction); !errors.Is(err, primary) {
		t.Fatalf("apply error = %v", err)
	}
	if err := transaction.abort(primary); !errors.Is(err, primary) {
		t.Fatalf("abort error = %v", err)
	}

	assertFileState(t, profile, originalProfile, 0o640)
	ledgerAfter, err := os.ReadFile(ledgerPath(layout))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(ledgerAfter, ledgerBefore) {
		t.Fatalf("install ledger changed after rollback")
	}
	assertSetupArtifactsAbsent(t, transaction)
}

func TestUpdateShellOwnershipPreservesCreated(t *testing.T) {
	path := filepath.Join(t.TempDir(), ".profile")
	ledger := InstallLedger{ShellBlocks: map[string]ShellBlockOwnership{
		path: {Digest: "old", Created: true},
	}}
	updated, err := updateShellOwnership(ledger, []ShellEdit{{Path: path, Digest: "new"}})
	if err != nil {
		t.Fatal(err)
	}
	if ownership := updated.ShellBlocks[path]; ownership.Digest != "new" || !ownership.Created {
		t.Fatalf("ownership = %+v", ownership)
	}
}

func setupExternalWorkflowFixture(t *testing.T) (string, string, string) {
	t.Helper()
	home := t.TempDir()
	binDir := filepath.Join(home, "path-bin")
	if err := os.MkdirAll(binDir, 0o700); err != nil {
		t.Fatal(err)
	}
	for _, command := range []string{"claude", "claudex", "screen"} {
		if err := os.WriteFile(filepath.Join(binDir, command), nil, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	profile := filepath.Join(home, ".zshrc")
	if err := os.WriteFile(profile, []byte("export OTHER=1\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	return home, binDir, profile
}
