package setup

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/ctrl-alt-raccoon/shellmates/internal/config"
	screenpkg "github.com/ctrl-alt-raccoon/shellmates/internal/screen"
)

func nativeSetupEnvironment(t *testing.T, commands ...string) (string, config.Paths) {
	t.Helper()
	root := t.TempDir()
	bin := filepath.Join(root, "bin")
	if err := os.Mkdir(bin, 0o700); err != nil {
		t.Fatal(err)
	}
	for _, command := range commands {
		if err := os.WriteFile(filepath.Join(bin, command), nil, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("HOME", root)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(root, "config"))
	t.Setenv("XDG_STATE_HOME", filepath.Join(root, "state"))
	t.Setenv("XDG_DATA_HOME", filepath.Join(root, "data"))
	t.Setenv("PATH", bin)
	paths, err := config.DefaultPaths()
	if err != nil {
		t.Fatal(err)
	}
	return bin, paths
}

func TestSetupNativeSelectionsAndDoctor(t *testing.T) {
	for _, selection := range []string{"codex", "claude", "claude,codex", "claudex"} {
		t.Run(selection, func(t *testing.T) {
			commands := append([]string{"screen"}, strings.Split(selection, ",")...)
			bin, paths := nativeSetupEnvironment(t, commands...)
			opts := SetupOptions{Backends: selection, NonInteractive: true, NoModifyPath: true, Output: &bytes.Buffer{}}
			runner := &recordingRunner{}
			result, err := runWorkflow(context.Background(), opts, runner)
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(result.Config.Backends(), strings.Split(selection, ",")) {
				t.Fatalf("backends=%v", result.Config.Backends())
			}
			if len(runner.commands) != 0 || len(runner.outputCommands) != 0 {
				t.Fatalf("native setup invoked services or auth: %v %v", runner.commands, runner.outputCommands)
			}
			if strings.Contains(selection, "codex") && result.Config.RealCodex != filepath.Join(bin, "codex") {
				t.Fatal("wrong Codex executable")
			}
			if selection == "codex" && (result.Config.RealClaude != "" || result.Config.ProxyCredential != "") {
				t.Fatal("Codex-only setup requires Claude/proxy")
			}
			if selection == "claudex" && result.Config.RealClaude != "" {
				t.Fatal("opaque external claudex requires Claude")
			}
			for _, path := range []string{paths.Credential, paths.ManagedSettings} {
				if _, err := os.Stat(path); !os.IsNotExist(err) {
					t.Fatalf("native setup created proxy state: %s %v", path, err)
				}
			}
			loaded, err := LoadRuntime(paths)
			if err != nil || !reflect.DeepEqual(loaded, result.Config) {
				t.Fatalf("round trip: %+v %v", loaded, err)
			}
			screen := &doctorScreenStub{lists: [][]screenpkg.Socket{{{PID: 1234, Name: "native-doctor"}}, nil}}
			report := RunDoctorWithOptions(context.Background(), paths, DoctorOptions{Screen: screen, SessionName: "native-doctor", PollInterval: time.Millisecond, PollTimeout: time.Second})
			if !report.OK() {
				t.Fatalf("doctor: %+v", report)
			}
			for _, check := range report.Checks {
				if strings.Contains(check.Name, "proxy") || strings.Contains(check.Name, "CLIProxy") {
					t.Fatalf("unselected proxy checked: %+v", check)
				}
				if selection == "codex" && check.Name == "Claude Code" {
					t.Fatal("unselected Claude checked")
				}
			}
		})
	}
}

func TestSetupSkipProxyAllowsClaudeOnly(t *testing.T) {
	_, _ = nativeSetupEnvironment(t, "screen", "claude")
	result, err := runWorkflow(context.Background(), SetupOptions{SkipProxy: true, SkipCodex: true, NonInteractive: true, NoModifyPath: true, Output: &bytes.Buffer{}}, &recordingRunner{})
	if err != nil || !reflect.DeepEqual(result.Config.Backends(), []string{"claude"}) {
		t.Fatalf("config=%+v err=%v", result.Config, err)
	}
}

func TestCodexSelectedIsRequiredNotOptional(t *testing.T) {
	_, paths := nativeSetupEnvironment(t, "screen")
	_, err := runWorkflow(context.Background(), SetupOptions{Backends: "codex", NonInteractive: true, NoModifyPath: true, Output: &bytes.Buffer{}}, &recordingRunner{})
	if err == nil || !strings.Contains(err.Error(), "Codex CLI is missing") {
		t.Fatalf("error=%v", err)
	}
	if _, err := os.Stat(paths.ConfigFile); !os.IsNotExist(err) {
		t.Fatal("missing Codex published config")
	}
}

func TestBackendSelectionRejectsContradictions(t *testing.T) {
	for _, opts := range []SetupOptions{
		{Backends: "codex,other"}, {Backends: "codex,codex"}, {Backends: "codex,"},
		{Backends: "codex", SkipCodex: true}, {Backends: "codex", ManagedProxyExplicit: true},
		{Backends: "claude", CodexExecutable: "/bin/codex"},
	} {
		if err := ValidateSetupOptions(opts); err == nil {
			t.Fatalf("contradiction accepted: %+v", opts)
		}
	}
}

func TestCodexDiscoverySkipsProjectLauncherIdentities(t *testing.T) {
	bin, _ := nativeSetupEnvironment(t)
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(self, filepath.Join(bin, "codex")); err != nil {
		t.Fatal(err)
	}
	if _, err := configuredCodexExecutable(""); err == nil {
		t.Fatal("project launcher accepted as Codex")
	}
	if _, err := configuredCodexExecutable(filepath.Join(bin, "codex")); err == nil {
		t.Fatal("explicit project launcher accepted as Codex")
	}
	second := t.TempDir()
	valid := filepath.Join(second, "codex")
	if err := os.WriteFile(valid, nil, 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+second)
	if got, err := configuredCodexExecutable(""); err != nil || got != valid {
		t.Fatalf("discovery=%s %v", got, err)
	}
}

func legacyNativeFixture(t *testing.T, schema int) (InstallLayout, InstallLedger) {
	t.Helper()
	layout := testInstallLayout(t)
	source := testReleaseSource(t, t.TempDir(), "legacy")
	ledger, err := InstallBinary(source, "v0.1.0", layout)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(layout.BinDir, "scodex")
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	delete(ledger.Files, path)
	ledger.SchemaVersion, ledger.CodexReleases = schema, nil
	if schema == 1 {
		ledger.DataDir, ledger.StateDir, ledger.Releases = "", "", nil
	}
	if err := writeLedger(layout, ledger); err != nil {
		t.Fatal(err)
	}
	return layout, ledger
}

func TestNativeLauncherMigratesLegacyInstallAndGuardsRollback(t *testing.T) {
	for _, schema := range []int{1, 2} {
		t.Run(string(rune('0'+schema)), func(t *testing.T) {
			layout, _ := legacyNativeFixture(t, schema)
			before, _ := os.Lstat(filepath.Join(layout.BinDir, "sclaude"))
			ledger, err := InstallBinary(testReleaseSource(t, t.TempDir(), "native"), "v0.2.0", layout)
			if err != nil {
				t.Fatal(err)
			}
			if ledger.SchemaVersion != 3 || !ledger.CodexReleases[ledger.Current] || ledger.CodexReleases[ledger.Previous] {
				t.Fatalf("compatibility=%+v", ledger)
			}
			after, _ := os.Lstat(filepath.Join(layout.BinDir, "sclaude"))
			if !os.SameFile(before, after) {
				t.Fatal("migration rewrote existing launcher")
			}
			if _, err := os.Stat(filepath.Join(layout.BinDir, "scodex")); err != nil {
				t.Fatal(err)
			}
			if _, err := Rollback(layout); err == nil || !strings.Contains(err.Error(), "predates native Codex") {
				t.Fatalf("unsafe legacy rollback accepted: %v", err)
			}
			if _, err := InstallBinary(testReleaseSource(t, t.TempDir(), "newer"), "v0.3.0", layout); err != nil {
				t.Fatal(err)
			}
			if ledger, err = Rollback(layout); err != nil || ledger.Current != "v0.2.0" {
				t.Fatalf("native rollback: %+v %v", ledger, err)
			}
			if err := Uninstall(layout, false); err != nil {
				t.Fatal(err)
			}
			if _, err := os.Lstat(filepath.Join(layout.BinDir, "scodex")); !os.IsNotExist(err) {
				t.Fatal("owned scodex survived uninstall")
			}
		})
	}
}

func TestNativeLauncherMigrationPreservesUnmanagedPath(t *testing.T) {
	for _, schema := range []int{1, 2} {
		layout, _ := legacyNativeFixture(t, schema)
		path := filepath.Join(layout.BinDir, "scodex")
		if err := os.WriteFile(path, []byte("user-owned"), 0o700); err != nil {
			t.Fatal(err)
		}
		before, _ := os.ReadFile(ledgerPath(layout))
		if _, err := InstallBinary(testReleaseSource(t, t.TempDir(), "native"), "v0.2.0", layout); err == nil {
			t.Fatal("unmanaged scodex overwritten")
		}
		after, _ := os.ReadFile(ledgerPath(layout))
		data, _ := os.ReadFile(path)
		if !bytes.Equal(before, after) || string(data) != "user-owned" {
			t.Fatal("failed migration changed legacy state")
		}
	}
}

func TestNativeLauncherMigrationFailureRestoresLegacyState(t *testing.T) {
	layout, _ := legacyNativeFixture(t, 2)
	before, _ := os.ReadFile(ledgerPath(layout))
	ops := defaultActivationOps()
	ops.writeLedger = func(string, []byte) error { return errors.New("injected ledger failure") }
	if _, err := installBinaryWithOps(testReleaseSource(t, t.TempDir(), "native"), "v0.2.0", layout, ops); err == nil {
		t.Fatal("failure ignored")
	}
	after, _ := os.ReadFile(ledgerPath(layout))
	if !bytes.Equal(before, after) {
		t.Fatal("legacy ledger changed")
	}
	if _, err := os.Lstat(filepath.Join(layout.BinDir, "scodex")); !os.IsNotExist(err) {
		t.Fatal("failed migration retained scodex")
	}
	if target, _ := os.Readlink(currentPath(layout)); target != releaseDir(layout, "v0.1.0") {
		t.Fatalf("current=%s", target)
	}
}

func TestLegacyJournalRecoveryDoesNotTouchScodex(t *testing.T) {
	layout, _ := legacyNativeFixture(t, 2)
	journal := testCurrentInstallJournal(t, layout)
	journal.SchemaVersion = 2
	delete(journal.PriorLaunchers, filepath.Join(layout.BinDir, "scodex"))
	journal.NewLedgerDigest = strings.Repeat("0", 64) // exercise restoration, not just commit finalization
	if err := writeJSONPrivate(journalPath(layout), journal); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(layout.BinDir, "scodex")
	if err := os.WriteFile(path, []byte("unmanaged"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := recoverInstallJournal(layout); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(path)
	if string(data) != "unmanaged" {
		t.Fatal("legacy recovery touched scodex")
	}
}

func TestLauncherAppearingDuringActivationIsPreserved(t *testing.T) {
	layout, _ := legacyNativeFixture(t, 2)
	path := filepath.Join(layout.BinDir, "scodex")
	ops := defaultActivationOps()
	ops.switchCurrent = func(target, current string) error {
		if err := os.WriteFile(path, []byte("appeared after preflight"), 0o700); err != nil {
			return err
		}
		return atomicSymlink(target, current)
	}
	if _, err := installBinaryWithOps(testReleaseSource(t, t.TempDir(), "native"), "v0.2.0", layout, ops); err == nil {
		t.Fatal("colliding launcher was overwritten")
	}
	data, _ := os.ReadFile(path)
	if string(data) != "appeared after preflight" {
		t.Fatal("rollback removed unmanaged collision")
	}
}
