package setup

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDiscoverClaudeExecutableContinuesPastInvalidPATHHit(t *testing.T) {
	firstDir := t.TempDir()
	secondDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(firstDir, "claude"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	valid := filepath.Join(secondDir, "claude")
	if err := os.WriteFile(valid, nil, 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", strings.Join([]string{firstDir, secondDir}, string(os.PathListSeparator)))

	got, err := discoverClaudeExecutable()
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Clean(valid)
	if got != want {
		t.Fatalf("Claude executable = %q, want stable path %q", got, want)
	}
}

func TestDiscoverClaudeExecutableFallsBackAfterInvalidPATHHit(t *testing.T) {
	home := t.TempDir()
	firstDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(firstDir, "claude"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	fallback := filepath.Join(home, ".local", "bin", "claude")
	if err := os.MkdirAll(filepath.Dir(fallback), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(fallback, nil, 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", home)
	t.Setenv("PATH", firstDir)

	got, err := discoverClaudeExecutable()
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Clean(fallback)
	if got != want {
		t.Fatalf("Claude executable = %q, want stable path %q", got, want)
	}
}

func TestDiscoverCLIProxyExecutableContinuesPastInvalidPATHHit(t *testing.T) {
	firstDir := t.TempDir()
	secondDir := t.TempDir()
	if err := os.Symlink(filepath.Join(firstDir, "missing"), filepath.Join(firstDir, "cliproxyapi")); err != nil {
		t.Fatal(err)
	}
	valid := filepath.Join(secondDir, "cliproxyapi")
	if err := os.WriteFile(valid, nil, 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", strings.Join([]string{firstDir, secondDir}, string(os.PathListSeparator)))

	got, err := DiscoverCLIProxyExecutable()
	if err != nil {
		t.Fatal(err)
	}
	want, err := filepath.EvalSymlinks(valid)
	if err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Fatalf("CLIProxyAPI executable = %q, want %q", got, want)
	}
}

func TestDiscoverCLIProxyExecutableFallsBackAfterInvalidPATHHit(t *testing.T) {
	home := t.TempDir()
	firstDir := t.TempDir()
	if err := os.Mkdir(filepath.Join(firstDir, "cliproxyapi"), 0o700); err != nil {
		t.Fatal(err)
	}
	fallback := filepath.Join(home, "cliproxyapi", "cli-proxy-api")
	if err := os.MkdirAll(filepath.Dir(fallback), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(fallback, nil, 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", home)
	t.Setenv("PATH", firstDir)

	got, err := DiscoverCLIProxyExecutable()
	if err != nil {
		t.Fatal(err)
	}
	want, err := filepath.EvalSymlinks(fallback)
	if err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Fatalf("CLIProxyAPI executable = %q, want %q", got, want)
	}
}

func TestDiscoverScreenExecutableFallsBackToHomebrewOutsidePATH(t *testing.T) {
	prefix := filepath.Join(t.TempDir(), "homebrew")
	fallback := filepath.Join(prefix, "bin", "screen")
	if err := os.MkdirAll(filepath.Dir(fallback), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(fallback, nil, 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", t.TempDir())

	got, err := discoverExecutable("screen", fallback)
	if err != nil {
		t.Fatal(err)
	}
	want, err := filepath.EvalSymlinks(fallback)
	if err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Fatalf("Screen executable = %q, want %q", got, want)
	}
}

func TestDiscoverCLIProxyExecutableFallsBackToHomebrewOutsidePATH(t *testing.T) {
	prefix := filepath.Join(t.TempDir(), "homebrew")
	fallback := filepath.Join(prefix, "bin", "cliproxyapi")
	if err := os.MkdirAll(filepath.Dir(fallback), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(fallback, nil, 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", t.TempDir())

	got, err := discoverCLIProxyExecutable(
		[]string{fallback},
		func() (string, error) { return "", os.ErrNotExist },
	)
	if err != nil {
		t.Fatal(err)
	}
	want, err := filepath.EvalSymlinks(fallback)
	if err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Fatalf("CLIProxyAPI executable = %q, want %q", got, want)
	}
}

func TestValidatedCommandPathsIgnoresEmptyPATHElement(t *testing.T) {
	originalDirectory, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	workingDirectory := t.TempDir()
	if err := os.Chdir(workingDirectory); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := os.Chdir(originalDirectory); err != nil {
			t.Errorf("restore working directory: %v", err)
		}
	})
	command := filepath.Join(workingDirectory, "claude")
	if err := os.WriteFile(command, nil, 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", string(os.PathListSeparator)+t.TempDir())

	paths := validatedCommandPaths("claude", validateExecutablePath)
	if len(paths) != 0 {
		t.Fatalf("validated paths = %q, want empty PATH element ignored", paths)
	}
}
