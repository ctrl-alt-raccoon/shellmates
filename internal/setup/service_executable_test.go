package setup

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ctrl-alt-raccoon/sclaude/internal/config"
)

func TestValidateCLIProxyServiceExecutableAcceptsActiveHomebrewExecutable(t *testing.T) {
	prefix := filepath.Join(t.TempDir(), "homebrew")
	activeExecutable := filepath.Join(prefix, "bin", "cliproxyapi")
	if err := os.MkdirAll(filepath.Dir(activeExecutable), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(activeExecutable, nil, 0o700); err != nil {
		t.Fatal(err)
	}
	activePrefix := func() (string, error) { return prefix + "\n", nil }

	if err := validateCLIProxyServiceExecutable("brew", activeExecutable, activePrefix); err != nil {
		t.Fatal(err)
	}

	alias := filepath.Join(t.TempDir(), "cliproxyapi")
	if err := os.Symlink(activeExecutable, alias); err != nil {
		t.Fatal(err)
	}
	if err := validateCLIProxyServiceExecutable("brew", alias, activePrefix); err != nil {
		t.Fatalf("active executable symlink was rejected: %v", err)
	}
}

func TestValidateCLIProxyServiceExecutableRejectsExternalHardLink(t *testing.T) {
	prefix := filepath.Join(t.TempDir(), "homebrew")
	activeExecutable := filepath.Join(prefix, "bin", "cliproxyapi")
	if err := os.MkdirAll(filepath.Dir(activeExecutable), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(activeExecutable, nil, 0o700); err != nil {
		t.Fatal(err)
	}
	hardLink := filepath.Join(t.TempDir(), "cliproxyapi")
	if err := os.Link(activeExecutable, hardLink); err != nil {
		t.Fatal(err)
	}

	if err := validateCLIProxyServiceExecutable("brew", hardLink, func() (string, error) {
		return prefix, nil
	}); err == nil || !strings.Contains(err.Error(), "active brew --prefix") {
		t.Fatalf("external hard link was accepted: %v", err)
	}
}

func TestValidateCLIProxyServiceExecutableRejectsEscapedHomebrewBin(t *testing.T) {
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
	executable := filepath.Join(externalBin, "cliproxyapi")
	if err := os.WriteFile(executable, nil, 0o700); err != nil {
		t.Fatal(err)
	}

	if err := validateCLIProxyServiceExecutable("brew", executable, func() (string, error) {
		return prefix, nil
	}); err == nil || !strings.Contains(err.Error(), "active brew --prefix") {
		t.Fatalf("escaped Homebrew bin was accepted: %v", err)
	}
}

func TestValidateCLIProxyServiceExecutableRejectsCustomExecutable(t *testing.T) {
	prefix := filepath.Join(t.TempDir(), "homebrew")
	activeExecutable := filepath.Join(prefix, "bin", "cliproxyapi")
	customExecutable := filepath.Join(t.TempDir(), "secret-custom-proxy")
	if err := os.MkdirAll(filepath.Dir(activeExecutable), 0o700); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{activeExecutable, customExecutable} {
		if err := os.WriteFile(path, nil, 0o700); err != nil {
			t.Fatal(err)
		}
	}

	err := validateCLIProxyServiceExecutable("brew", customExecutable, func() (string, error) {
		return prefix, nil
	})
	if err == nil || !strings.Contains(err.Error(), "active brew --prefix") {
		t.Fatalf("expected custom executable rejection, got %v", err)
	}
	if strings.Contains(err.Error(), customExecutable) || strings.Contains(err.Error(), "secret-custom-proxy") {
		t.Fatalf("error leaked custom executable path: %v", err)
	}
}

func TestValidateCLIProxyServiceExecutableLeavesNonBrewManagersUntouched(t *testing.T) {
	unusedPrefix := func() (string, error) {
		t.Fatal("Homebrew prefix lookup should not run for non-brew managers")
		return "", nil
	}
	for _, manager := range []string{"none", "systemd", "docker"} {
		if err := validateCLIProxyServiceExecutable(manager, "/missing/custom-proxy", unusedPrefix); err != nil {
			t.Fatalf("manager %q: %v", manager, err)
		}
	}
}

func TestValidateCLIProxyServiceExecutableReportsPrefixLookupFailure(t *testing.T) {
	err := validateCLIProxyServiceExecutable("brew", "/missing/custom-proxy", func() (string, error) {
		return "", errors.New("brew unavailable")
	})
	if err == nil || !strings.Contains(err.Error(), "brew unavailable") {
		t.Fatalf("expected Homebrew prefix error, got %v", err)
	}
}

func TestRunWorkflowRejectsCustomExecutableBeforeProxyMutation(t *testing.T) {
	home := t.TempDir()
	binDir := filepath.Join(home, "bin")
	prefix := filepath.Join(home, "homebrew")
	activeExecutable := filepath.Join(prefix, "bin", "cliproxyapi")
	customExecutable := filepath.Join(home, "custom-proxy")
	proxyConfig := filepath.Join(prefix, "etc", "cliproxyapi.conf")
	for _, directory := range []string{binDir, filepath.Dir(activeExecutable), filepath.Dir(proxyConfig)} {
		if err := os.MkdirAll(directory, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	for _, path := range []string{activeExecutable, customExecutable} {
		if err := os.WriteFile(path, nil, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	for _, command := range []string{"claude", "screen", "curl", "git", "codex"} {
		if err := os.WriteFile(filepath.Join(binDir, command), nil, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	brew := filepath.Join(binDir, "brew")
	if err := os.WriteFile(brew, []byte("#!/bin/sh\nprintf '%s\\n' '"+prefix+"'\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	original := []byte("host: \"\"\n")
	if err := os.WriteFile(proxyConfig, original, 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, "config"))
	t.Setenv("PATH", binDir)

	_, err := runWorkflow(context.Background(), SetupOptions{
		NonInteractive:     true,
		SkipCodex:          true,
		CLIProxyExecutable: customExecutable,
		CLIProxyConfig:     proxyConfig,
		CLIProxyService:    "brew",
		Input:              strings.NewReader(""),
		Output:             io.Discard,
		ErrorOutput:        io.Discard,
	}, &recordingRunner{})
	if err == nil || !strings.Contains(err.Error(), "active brew --prefix") {
		t.Fatalf("expected custom executable rejection, got %v", err)
	}
	data, readErr := os.ReadFile(proxyConfig)
	if readErr != nil {
		t.Fatal(readErr)
	}
	if string(data) != string(original) {
		t.Fatalf("proxy config mutated: %q", data)
	}
	info, statErr := os.Stat(proxyConfig)
	if statErr != nil {
		t.Fatal(statErr)
	}
	if info.Mode().Perm() != 0o644 {
		t.Fatalf("proxy config mode mutated to %o", info.Mode().Perm())
	}
	paths, pathErr := config.DefaultPaths()
	if pathErr != nil {
		t.Fatal(pathErr)
	}
	if _, statErr := os.Stat(paths.Credential); !os.IsNotExist(statErr) {
		t.Fatalf("credential was created before executable rejection: %v", statErr)
	}
}
