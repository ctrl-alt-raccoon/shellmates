package harness

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestEmbeddedProjectLifecycle(t *testing.T) {
	if _, err := exec.LookPath("python3"); err != nil {
		t.Skip("Python 3.9+ is required for the harness fixture")
	}
	dir := t.TempDir()
	for _, key := range []string{"HOME", "XDG_CONFIG_HOME", "XDG_STATE_HOME", "XDG_DATA_HOME", "CODEX_HOME", "CLAUDE_CONFIG_DIR"} {
		t.Setenv(key, filepath.Join(dir, "unused-"+key))
	}
	repo := filepath.Join(dir, "project with spaces; literal")
	if err := os.MkdirAll(repo, 0700); err != nil {
		t.Fatal(err)
	}
	if output, err := exec.Command("git", "init", "--quiet", "--template=", repo).CombinedOutput(); err != nil {
		t.Fatalf("initialize fixture: %v %s", err, output)
	}
	for _, action := range []string{"install", "check", "update", "remove"} {
		var out, errs bytes.Buffer
		if code := Run(context.Background(), []string{action, "--project", repo}, nil, &out, &errs); code != 0 {
			t.Fatalf("%s: %d\n%s\n%s", action, code, out.String(), errs.String())
		}
		if action == "install" {
			body, err := os.ReadFile(filepath.Join(repo, "AGENTS.md"))
			if err != nil || !strings.Contains(string(body), "YAGNI") {
				t.Fatalf("embedded instructions missing: %v", err)
			}
		}
	}
	if _, err := os.Stat(filepath.Join(repo, "AGENTS.md")); !os.IsNotExist(err) {
		t.Fatalf("remove left generated entry: %v", err)
	}
	for _, key := range []string{"HOME", "XDG_CONFIG_HOME", "XDG_STATE_HOME", "XDG_DATA_HOME", "CODEX_HOME", "CLAUDE_CONFIG_DIR"} {
		if _, err := os.Stat(os.Getenv(key)); !os.IsNotExist(err) {
			t.Fatalf("touched %s: %v", key, err)
		}
	}
}
