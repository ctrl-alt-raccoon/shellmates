package app

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestHarnessDispatchDoesNotLoadRuntime(t *testing.T) {
	if _, err := exec.LookPath("python3"); err != nil {
		t.Skip("Python 3.9+ is required for the harness fixture")
	}
	root := t.TempDir()
	for _, key := range []string{"HOME", "XDG_CONFIG_HOME", "XDG_STATE_HOME", "XDG_DATA_HOME", "CODEX_HOME", "CLAUDE_CONFIG_DIR"} {
		t.Setenv(key, filepath.Join(root, key))
	}
	var out, errs bytes.Buffer
	if code := Run(context.Background(), "sclaude", []string{"harness", "--help"}, IO{Out: &out, Err: &errs}, "test"); code != 0 {
		t.Fatalf("code %d: %s", code, errs.String())
	}
	if !strings.Contains(out.String(), "--project") || !strings.Contains(out.String(), "remove") {
		t.Fatalf("missing harness help: %s", out.String())
	}
	entries, err := os.ReadDir(root)
	if err != nil || len(entries) != 0 {
		t.Fatalf("harness help touched runtime/profile directories: %v %v", entries, err)
	}
	_, manager := classifyInvocation("scodex", []string{"harness"})
	if manager {
		t.Fatal("new manager feature stole a native Codex argument")
	}
}
