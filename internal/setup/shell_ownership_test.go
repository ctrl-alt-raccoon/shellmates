package setup

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestConfigureShellPATHCreatesActiveShellFile(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("SHELL", "/bin/zsh")

	edits, err := ConfigureShellPATH("/tmp/bin with space", SetupOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if len(edits) != 1 || edits[0].Path != filepath.Join(home, ".zshrc") || !edits[0].Created || edits[0].Digest == "" {
		t.Fatalf("unexpected edits: %+v", edits)
	}
	if _, err := os.Lstat(filepath.Join(home, ".config", "fish", "conf.d", "sclaude.fish")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("inactive fish shell file was created: %v", err)
	}
}

func TestOwnedShellRemovalRequiresExactBlock(t *testing.T) {
	path := filepath.Join(t.TempDir(), ".profile")
	edit, err := EnsureShellPATH(path, "/tmp/bin", "sh")
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	modified := strings.Replace(string(data), "_sclaude_found=", "_sclaude_found=modified", 1)
	if err := os.WriteFile(path, []byte(modified), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := prepareOwnedShellRemoval(path, ShellBlockOwnership{Digest: edit.Digest, Created: true}); err == nil || !strings.Contains(err.Error(), "modified") {
		t.Fatalf("modified block error = %v", err)
	}
}

func TestOwnedShellRemovalDeletesCreatedEmptyFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), ".profile")
	edit, err := EnsureShellPATH(path, "/tmp/bin", "sh")
	if err != nil {
		t.Fatal(err)
	}
	plan, err := prepareOwnedShellRemoval(path, ShellBlockOwnership{Digest: edit.Digest, Created: edit.Created})
	if err != nil {
		t.Fatal(err)
	}
	if !plan.remove {
		t.Fatal("created otherwise-empty file was not prepared for deletion")
	}
	if err := applyOwnedShellRemoval(plan); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("created file remains: %v", err)
	}
}

func TestOwnedShellRemovalPreservesOtherContent(t *testing.T) {
	path := filepath.Join(t.TempDir(), ".profile")
	if err := os.WriteFile(path, []byte("export OTHER=1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	edit, err := EnsureShellPATH(path, "/tmp/bin", "sh")
	if err != nil {
		t.Fatal(err)
	}
	plan, err := prepareOwnedShellRemoval(path, ShellBlockOwnership{Digest: edit.Digest, Created: false})
	if err != nil {
		t.Fatal(err)
	}
	if plan.remove {
		t.Fatal("existing file was prepared for deletion")
	}
	if err := applyOwnedShellRemoval(plan); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "export OTHER=1\n" {
		t.Fatalf("unexpected shell content %q", data)
	}
}

func TestShellPATHBlockHandlesMetacharacterDirectory(t *testing.T) {
	block, err := shellPATHBlock("/tmp/bin [*?)] with ' quote", "sh")
	if err != nil {
		t.Fatal(err)
	}
	for _, wanted := range []string{"set -f", "IFS=:", `if [ "$_sclaude_component" = "$_sclaude_bin" ]`} {
		if !strings.Contains(block, wanted) {
			t.Fatalf("block missing %q:\n%s", wanted, block)
		}
	}
}
