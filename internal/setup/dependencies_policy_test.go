package setup

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

type dependencyRecordingRunner struct {
	commands [][]string
	onRun    func([]string) error
}

func (r *dependencyRecordingRunner) Run(_ context.Context, name string, args ...string) error {
	command := append([]string{name}, args...)
	r.commands = append(r.commands, command)
	if r.onRun != nil {
		return r.onRun(command)
	}
	return nil
}

func TestCheckDependenciesMarksCodexOptional(t *testing.T) {
	for _, dependency := range CheckDependencies() {
		if dependency.Command == "codex" {
			if dependency.Required {
				t.Fatal("Codex CLI dependency is required")
			}
			return
		}
	}
	t.Fatal("Codex CLI dependency was not reported")
}

func TestValidateSetupOptions(t *testing.T) {
	tests := []struct {
		name    string
		opts    SetupOptions
		wantErr string
	}{
		{name: "defaults"},
		{name: "dry-run noninteractive", opts: SetupOptions{DryRun: true, NonInteractive: true}},
		{name: "yes noninteractive", opts: SetupOptions{Yes: true, NonInteractive: true}},
		{name: "managed proxy and skip", opts: SetupOptions{ManagedProxyExplicit: true, SkipProxy: true}, wantErr: "cannot combine --skip-proxy"},
		{name: "system service auto", opts: SetupOptions{CLIProxyService: "auto", CLIProxySystemService: true}, wantErr: "requires --proxy-service systemd"},
		{name: "system service brew", opts: SetupOptions{CLIProxyService: "brew", CLIProxySystemService: true}, wantErr: "requires --proxy-service systemd"},
		{name: "invalid service", opts: SetupOptions{CLIProxyService: "launchd"}, wantErr: "unsupported CLIProxyAPI service manager"},
		{name: "custom paths with none", opts: SetupOptions{CLIProxyExecutable: "/tmp/proxy", CLIProxyConfig: "/tmp/config.yaml", CLIProxyService: "none"}},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := ValidateSetupOptions(test.opts)
			if test.wantErr == "" {
				if err != nil {
					t.Fatalf("ValidateSetupOptions() error = %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), test.wantErr) {
				t.Fatalf("ValidateSetupOptions() error = %v, want containing %q", err, test.wantErr)
			}
		})
	}
}

func TestValidateSetupOptionsRejectsCustomBrewPaths(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	for name, opts := range map[string]SetupOptions{
		"executable": {CLIProxyExecutable: "/tmp/proxy", CLIProxyService: "brew"},
		"config":     {CLIProxyConfig: "/tmp/config.yaml", CLIProxyService: "brew"},
	} {
		t.Run(name, func(t *testing.T) {
			err := ValidateSetupOptions(opts)
			if err == nil || !strings.Contains(err.Error(), "Homebrew") {
				t.Fatalf("ValidateSetupOptions() error = %v", err)
			}
		})
	}
}

func TestRunDependencySetupNonInteractiveDoesNotRunSudo(t *testing.T) {
	sudoPath, aptGetPath := installFakeAdministratorCommands(t)
	runner := &dependencyRecordingRunner{}
	var output bytes.Buffer
	err := runDependencySetup(context.Background(), SetupOptions{
		NonInteractive: true,
		Output:         &output,
	}, linuxTestPlatform(), runner, dependenciesWithMissing("screen"))
	if err == nil {
		t.Fatal("runDependencySetup() succeeded with missing system packages")
	}
	command := linuxPackageInstallCommand(sudoPath, aptGetPath, []string{"screen"})
	for _, want := range []string{"screen", command, "setup did not run"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("error %q does not contain %q", err, want)
		}
	}
	if len(runner.commands) != 0 {
		t.Fatalf("commands = %q, want none", runner.commands)
	}
}

func TestRunDependencySetupYesStillRequiresSudoConsent(t *testing.T) {
	sudoPath, aptGetPath := installFakeAdministratorCommands(t)
	runner := &dependencyRecordingRunner{}
	var output bytes.Buffer
	err := runDependencySetup(context.Background(), SetupOptions{
		Yes:         true,
		Input:       strings.NewReader("no\n"),
		Output:      &output,
		ErrorOutput: io.Discard,
	}, linuxTestPlatform(), runner, dependenciesWithMissing("screen"))
	if err == nil || !strings.Contains(err.Error(), "setup did not run") {
		t.Fatalf("error = %v", err)
	}
	command := linuxPackageInstallCommand(sudoPath, aptGetPath, []string{"screen"})
	if !strings.Contains(output.String(), command) {
		t.Fatalf("confirmation did not include administrator command: %q", output.String())
	}
	if len(runner.commands) != 0 {
		t.Fatalf("commands = %q, want none", runner.commands)
	}
}

func TestRunDependencySetupRunsSudoOnlyAfterDedicatedConsent(t *testing.T) {
	sudoPath, aptGetPath := installFakeAdministratorCommands(t)
	runner := &dependencyRecordingRunner{}
	var output bytes.Buffer
	err := runDependencySetup(context.Background(), SetupOptions{
		Yes:         true,
		Input:       strings.NewReader("yes\n"),
		Output:      &output,
		ErrorOutput: io.Discard,
	}, linuxTestPlatform(), runner, dependenciesWithMissing("screen"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(output.String(), "administrator command") {
		t.Fatalf("missing dedicated administrator confirmation: %q", output.String())
	}
	want := [][]string{
		{sudoPath, aptGetPath, "update"},
		{sudoPath, aptGetPath, "install", "-y", "screen"},
	}
	if !reflect.DeepEqual(runner.commands, want) {
		t.Fatalf("commands = %q, want %q", runner.commands, want)
	}
}

func TestRunDependencySetupDryRunDoesNotInvokeSudo(t *testing.T) {
	sudoPath, aptGetPath := installFakeAdministratorCommands(t)
	runner := &dependencyRecordingRunner{}
	var output bytes.Buffer
	err := runDependencySetup(context.Background(), SetupOptions{
		DryRun: true,
		Output: &output,
	}, linuxTestPlatform(), runner, dependenciesWithMissing("screen"))
	if err != nil {
		t.Fatal(err)
	}
	command := linuxPackageInstallCommand(sudoPath, aptGetPath, []string{"screen"})
	if !strings.Contains(output.String(), command) {
		t.Fatalf("dry-run output = %q", output.String())
	}
	if len(runner.commands) != 0 {
		t.Fatalf("commands = %q, want none", runner.commands)
	}
}

func TestRunDependencySetupRejectsReplacedAptGetAfterConsent(t *testing.T) {
	_, aptGetPath := installFakeAdministratorCommands(t)
	input := &replaceExecutableAfterRead{
		Reader: strings.NewReader("yes\n"),
		path:   aptGetPath,
	}
	runner := &dependencyRecordingRunner{}
	err := runDependencySetup(context.Background(), SetupOptions{
		Input:       input,
		Output:      io.Discard,
		ErrorOutput: io.Discard,
	}, linuxTestPlatform(), runner, dependenciesWithMissing("screen"))
	if err == nil || !strings.Contains(err.Error(), "changed after preparation") {
		t.Fatalf("error = %v", err)
	}
	if len(runner.commands) != 0 {
		t.Fatalf("commands = %q, want none", runner.commands)
	}
}

func TestRunDependencySetupRejectsReplacedSudoBeforeInstall(t *testing.T) {
	sudoPath, _ := installFakeAdministratorCommands(t)
	replaced := false
	runner := &dependencyRecordingRunner{onRun: func(command []string) error {
		if len(command) >= 3 && command[2] == "update" && !replaced {
			replaced = true
			return replaceExecutable(sudoPath)
		}
		return nil
	}}
	err := runDependencySetup(context.Background(), SetupOptions{
		Input:       strings.NewReader("yes\n"),
		Output:      io.Discard,
		ErrorOutput: io.Discard,
	}, linuxTestPlatform(), runner, dependenciesWithMissing("screen"))
	if err == nil || !strings.Contains(err.Error(), "changed after preparation") {
		t.Fatalf("error = %v", err)
	}
	if len(runner.commands) != 1 {
		t.Fatalf("commands = %q, want only apt-get update", runner.commands)
	}
}

func TestRunDependencySetupMissingOptionalCodexIsHealthyNonInteractive(t *testing.T) {
	runner := &dependencyRecordingRunner{}
	var output bytes.Buffer
	err := runDependencySetup(context.Background(), SetupOptions{
		NonInteractive: true,
		Output:         &output,
		ErrorOutput:    io.Discard,
	}, linuxTestPlatform(), runner, dependenciesWithMissing("codex"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(output.String(), CodexManualInstallCommand) {
		t.Fatalf("output = %q, want optional manual guidance", output.String())
	}
	if len(runner.commands) != 0 {
		t.Fatalf("commands = %q, want none", runner.commands)
	}
}

func TestRunDependencySetupReportsAllMissingDependencies(t *testing.T) {
	runner := &dependencyRecordingRunner{}
	err := runDependencySetup(context.Background(), SetupOptions{
		NonInteractive: true,
		Output:         io.Discard,
	}, linuxTestPlatform(), runner, dependenciesWithMissing("screen", "claude", "codex", "cliproxyapi"))
	if err == nil {
		t.Fatal("runDependencySetup() succeeded with missing required dependencies")
	}
	for _, want := range []string{
		"screen",
		ClaudeManualInstallCommand,
		CLIProxyManualInstallCommand,
	} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("error = %q, want %q", err, want)
		}
	}
	if len(runner.commands) != 0 {
		t.Fatalf("commands = %q, want none", runner.commands)
	}
}

func TestRunDependencySetupHomebrewFailureReportsAllMissingDependencies(t *testing.T) {
	brewPath := filepath.Join(t.TempDir(), "brew")
	if err := os.WriteFile(brewPath, []byte("#!/bin/sh\nexit 0\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	brew, err := trustExecutablePath(brewPath)
	if err != nil {
		t.Fatal(err)
	}
	installErr := errors.New("simulated Homebrew failure")
	runner := &dependencyRecordingRunner{onRun: func([]string) error {
		return installErr
	}}
	resolveCalls := 0
	resolveHomebrew := func(string, ...string) (trustedExecutable, error) {
		resolveCalls++
		return brew, nil
	}
	var output bytes.Buffer
	err = runDependencySetupWithHomebrewResolver(
		context.Background(),
		SetupOptions{Output: &output},
		Platform{OS: "darwin", Arch: "arm64", Distribution: "macos", Supported: true},
		runner,
		dependenciesWithMissing("screen", "claude", "codex", "cliproxyapi"),
		resolveHomebrew,
	)
	if err == nil {
		t.Fatal("runDependencySetupWithHomebrewResolver() succeeded")
	}
	for _, want := range []string{
		"install GNU Screen with Homebrew",
		"install CLIProxyAPI with Homebrew",
		ClaudeManualInstallCommand,
	} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("error = %q, want %q", err, want)
		}
	}
	if !strings.Contains(output.String(), CodexManualInstallCommand) {
		t.Fatalf("output = %q, want optional Codex guidance", output.String())
	}
	if resolveCalls != 2 {
		t.Fatalf("Homebrew resolver calls = %d, want 2", resolveCalls)
	}
	wantCommands := [][]string{
		{brew.path(), "install", "screen"},
		{brew.path(), "install", "cliproxyapi"},
	}
	if !reflect.DeepEqual(runner.commands, wantCommands) {
		t.Fatalf("commands = %q, want %q", runner.commands, wantCommands)
	}
}

func TestDetectLinuxDistributionSupportsOnlyExactDebianAndUbuntu(t *testing.T) {
	for _, test := range []struct {
		name    string
		content string
		want    string
	}{
		{name: "Debian", content: "ID=debian\n", want: "debian"},
		{name: "Ubuntu", content: "ID=ubuntu\n", want: "ubuntu"},
		{name: "Linux Mint", content: "ID=linuxmint\nID_LIKE=ubuntu\n", want: "linuxmint"},
		{name: "Pop OS", content: "ID=pop\nID_LIKE=\"ubuntu debian\"\n", want: "pop"},
		{name: "Kali", content: "ID=kali\nID_LIKE=debian\n", want: "kali"},
	} {
		t.Run(test.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "os-release")
			if err := os.WriteFile(path, []byte(test.content), 0o600); err != nil {
				t.Fatal(err)
			}
			if got := detectLinuxDistribution(path); got != test.want {
				t.Fatalf("detectLinuxDistribution() = %q, want %q", got, test.want)
			}
		})
	}
}

func TestValidateSetupPlatformRejectsUnsupportedLinuxArchitecture(t *testing.T) {
	for _, platform := range []Platform{
		{OS: "linux", Arch: "riscv64", Distribution: "debian"},
		{OS: "linux", Arch: "arm", Distribution: "fedora"},
		{OS: "freebsd", Arch: "amd64", Distribution: "freebsd"},
	} {
		if err := validateSetupPlatform(platform); err == nil {
			t.Fatalf("validateSetupPlatform(%+v) succeeded", platform)
		}
	}
	for _, platform := range []Platform{
		{OS: "linux", Arch: "amd64", Distribution: "fedora"},
		{OS: "linux", Arch: "arm64", Distribution: "linux"},
		{OS: "darwin", Arch: "arm64", Distribution: "macos", Supported: true},
	} {
		if err := validateSetupPlatform(platform); err != nil {
			t.Fatalf("validateSetupPlatform(%+v) error = %v", platform, err)
		}
	}
}

func TestRunDependencySetupUnsupportedLinuxPrintsGuidanceWithoutGuessing(t *testing.T) {
	runner := &dependencyRecordingRunner{}
	platform := Platform{OS: "linux", Arch: "amd64", Distribution: "fedora"}
	var output bytes.Buffer
	err := runDependencySetup(context.Background(), SetupOptions{
		DryRun: true,
		Output: &output,
	}, platform, runner, dependenciesWithMissing("screen", "claude", "codex", "cliproxyapi"))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"unsupported Linux distribution fedora",
		"trusted package manager",
		ClaudeManualInstallCommand,
		CodexManualInstallCommand,
		CLIProxyManualInstallCommand,
	} {
		if !strings.Contains(output.String(), want) {
			t.Fatalf("output = %q, want %q", output.String(), want)
		}
	}
	if len(runner.commands) != 0 {
		t.Fatalf("commands = %q, want none", runner.commands)
	}
}

func TestResolveTrustedExecutableAtPathsIgnoresPATH(t *testing.T) {
	dir := t.TempDir()
	pathExecutable := filepath.Join(dir, "brew")
	trustedExecutablePath := filepath.Join(dir, "trusted-brew")
	for _, path := range []string{pathExecutable, trustedExecutablePath} {
		if err := os.WriteFile(path, []byte("#!/bin/sh\nexit 0\n"), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("PATH", dir)

	resolved, err := resolveTrustedExecutableAtPaths("brew", filepath.Join(dir, "missing"), trustedExecutablePath)
	if err != nil {
		t.Fatal(err)
	}
	want, err := filepath.EvalSymlinks(trustedExecutablePath)
	if err != nil {
		t.Fatal(err)
	}
	if resolved.path() != want {
		t.Fatalf("resolved = %q, want trusted path %q", resolved.path(), want)
	}
}

func TestResolveTrustedHomebrewExecutableRejectsEscapedBin(t *testing.T) {
	prefix := filepath.Join(t.TempDir(), "homebrew")
	externalBin := filepath.Join(t.TempDir(), "bin")
	if err := os.MkdirAll(externalBin, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(prefix, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(externalBin, filepath.Join(prefix, "bin")); err != nil {
		t.Fatal(err)
	}
	brew := filepath.Join(externalBin, "brew")
	if err := os.WriteFile(brew, []byte("#!/bin/sh\nexit 0\n"), 0o700); err != nil {
		t.Fatal(err)
	}

	if _, err := resolveTrustedHomebrewExecutableAtPaths(
		"brew",
		filepath.Join(prefix, "bin", "brew"),
	); !errors.Is(err, exec.ErrNotFound) {
		t.Fatalf("escaped Homebrew executable error = %v", err)
	}
}

func TestResolveActiveHomebrewPrefixIgnoresPATH(t *testing.T) {
	dir := t.TempDir()
	pathBrew := filepath.Join(dir, "brew")
	prefixDir := filepath.Join(t.TempDir(), "trusted-prefix")
	trustedBrew := filepath.Join(prefixDir, "bin", "brew")
	if err := os.WriteFile(pathBrew, []byte("#!/bin/sh\nprintf 'wrong\\n'\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(trustedBrew), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(trustedBrew, []byte("#!/bin/sh\nprintf '/trusted/prefix\\n'\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir)

	prefix, err := activeHomebrewPrefixWithPaths(trustedBrew)
	if err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(prefix) != "/trusted/prefix" {
		t.Fatalf("prefix = %q, want trusted prefix", prefix)
	}
}

func TestRunDependencySetupNeverExecutesMutableInstallers(t *testing.T) {
	tests := []struct {
		name           string
		missing        string
		dryRun         bool
		nonInteractive bool
		wantError      bool
		wantCommand    string
	}{
		{name: "Claude", missing: "claude", wantError: true, wantCommand: ClaudeManualInstallCommand},
		{name: "Claude dry run", missing: "claude", dryRun: true, wantCommand: ClaudeManualInstallCommand},
		{name: "Codex", missing: "codex", wantCommand: CodexManualInstallCommand},
		{name: "Codex dry run", missing: "codex", dryRun: true, wantCommand: CodexManualInstallCommand},
		{name: "CLIProxyAPI", missing: "cliproxyapi", wantError: true, wantCommand: CLIProxyManualInstallCommand},
		{name: "CLIProxyAPI dry run", missing: "cliproxyapi", dryRun: true, wantCommand: CLIProxyManualInstallCommand},
		{name: "Codex noninteractive", missing: "codex", nonInteractive: true, wantCommand: CodexManualInstallCommand},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			runner := &dependencyRecordingRunner{}
			var output bytes.Buffer
			err := runDependencySetup(context.Background(), SetupOptions{
				DryRun:         test.dryRun,
				NonInteractive: test.nonInteractive,
				Output:         &output,
				ErrorOutput:    io.Discard,
			}, linuxTestPlatform(), runner, dependenciesWithMissing(test.missing))
			if test.wantError {
				if err == nil || !strings.Contains(err.Error(), test.wantCommand) {
					t.Fatalf("error = %v, want manual command %q", err, test.wantCommand)
				}
				if !strings.Contains(err.Error(), "does not download or execute") {
					t.Fatalf("error = %v, want no-execution policy", err)
				}
			} else {
				if err != nil {
					t.Fatal(err)
				}
				if !strings.Contains(output.String(), test.wantCommand) {
					t.Fatalf("output = %q, want manual command %q", output.String(), test.wantCommand)
				}
				if !strings.Contains(output.String(), "did not download or execute") {
					t.Fatalf("output = %q, want no-execution policy", output.String())
				}
			}
			if len(runner.commands) != 0 {
				t.Fatalf("commands = %q, want none", runner.commands)
			}
		})
	}
}

func TestRunSetupValidatesBeforeExplicitExecutableAccess(t *testing.T) {
	err := RunSetup(context.Background(), SetupOptions{
		ManagedProxyExplicit: true,
		SkipProxy:            true,
		CLIProxyExecutable:   "/definitely/missing/proxy",
	})
	if err == nil || !strings.Contains(err.Error(), "cannot combine --skip-proxy") {
		t.Fatalf("error = %v", err)
	}
}

type replaceExecutableAfterRead struct {
	io.Reader
	path string
	done bool
}

func (r *replaceExecutableAfterRead) Read(data []byte) (int, error) {
	n, err := r.Reader.Read(data)
	if !r.done {
		r.done = true
		if replaceErr := replaceExecutable(r.path); replaceErr != nil {
			return n, replaceErr
		}
	}
	return n, err
}

func installFakeAdministratorCommands(t *testing.T) (string, string) {
	t.Helper()
	dir := t.TempDir()
	sudoPath := filepath.Join(dir, "sudo")
	aptGetPath := filepath.Join(dir, "apt-get")
	for _, path := range []string{sudoPath, aptGetPath} {
		if err := os.WriteFile(path, []byte("#!/bin/sh\nexit 0\n"), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("PATH", dir)
	resolvedSudo, err := filepath.EvalSymlinks(sudoPath)
	if err != nil {
		t.Fatal(err)
	}
	resolvedAptGet, err := filepath.EvalSymlinks(aptGetPath)
	if err != nil {
		t.Fatal(err)
	}
	return resolvedSudo, resolvedAptGet
}

func replaceExecutable(path string) error {
	replacement := path + ".replacement"
	if err := os.WriteFile(replacement, []byte("#!/bin/sh\nexit 1\n"), 0o700); err != nil {
		return err
	}
	return os.Rename(replacement, path)
}

func linuxTestPlatform() Platform {
	return Platform{OS: "linux", Arch: "amd64", Distribution: "debian", Supported: true}
}

func dependenciesWithMissing(commands ...string) []Dependency {
	missing := make(map[string]bool, len(commands))
	for _, command := range commands {
		missing[command] = true
	}
	dependencies := []Dependency{
		{Name: "GNU Screen", Command: "screen", Installed: true, Required: true},
		{Name: "Claude Code", Command: "claude", Installed: true, Required: true},
		{Name: "Codex CLI", Command: "codex", Installed: true, Required: false},
		{Name: "CLIProxyAPI", Command: "cliproxyapi", Installed: true, Required: true},
	}
	for index := range dependencies {
		dependencies[index].Installed = !missing[dependencies[index].Command]
	}
	return dependencies
}
