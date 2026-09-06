package backend

import (
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestProjectAgentGuidance(t *testing.T) {
	root := filepath.Join("..", "..")
	source, err := os.ReadFile(filepath.Join(root, "project.md"))
	if err != nil {
		t.Fatal(err)
	}
	body := strings.TrimSpace(string(source)) + "\n"
	want := fmt.Sprintf("<!-- agent-harness generated sha256=%x; edit sources -->\n%s", sha256.Sum256([]byte(body)), body)
	for _, name := range []string{"CLAUDE.md", "AGENTS.md"} {
		got, err := os.ReadFile(filepath.Join(root, name))
		if err != nil {
			t.Fatal(err)
		}
		if string(got) != want {
			t.Errorf("%s is stale: regenerate ordinary project projections from project.md (no private global preferences)", name)
		}
	}
}
