package managedsettings

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestEnvironmentManagedValues(t *testing.T) {
	env := Environment("http://127.0.0.1:8317", "sk-test-secret")
	value, ok := env["CLAUDE_CODE_SIMPLE"]
	if !ok || value != "" {
		t.Fatalf("CLAUDE_CODE_SIMPLE = %q, present=%t; want one authoritative empty value", value, ok)
	}
	if value := env["CLAUDE_CODE_MAX_WEB_SEARCHES_PER_SESSION"]; value != MaxWebSearchesPerSession {
		t.Fatalf("CLAUDE_CODE_MAX_WEB_SEARCHES_PER_SESSION = %q, want %q", value, MaxWebSearchesPerSession)
	}
	overlay := New("http://127.0.0.1:8317", "sk-test-secret").Env
	if value := overlay["CLAUDE_CODE_SIMPLE"]; value != "" {
		t.Fatalf("overlay CLAUDE_CODE_SIMPLE = %q, want empty", value)
	}
	if value := overlay["CLAUDE_CODE_MAX_WEB_SEARCHES_PER_SESSION"]; value != MaxWebSearchesPerSession {
		t.Fatalf("overlay CLAUDE_CODE_MAX_WEB_SEARCHES_PER_SESSION = %q, want %q", value, MaxWebSearchesPerSession)
	}
}

func TestValidatePrivateFileRequiresExactOverlay(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "settings.json")
	secret := "sk-never-print"
	data, err := json.Marshal(New("http://127.0.0.1:8317", secret))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := ValidatePrivateFile(path, "http://127.0.0.1:8317", secret); err != nil {
		t.Fatal(err)
	}
	var exact Overlay
	if err := json.Unmarshal(data, &exact); err != nil {
		t.Fatal(err)
	}
	missingWebSearch := Overlay{Env: make(map[string]string, len(exact.Env)-1)}
	for key, value := range exact.Env {
		if key != "CLAUDE_CODE_MAX_WEB_SEARCHES_PER_SESSION" {
			missingWebSearch.Env[key] = value
		}
	}
	missingWebSearchData, err := json.Marshal(missingWebSearch)
	if err != nil {
		t.Fatal(err)
	}
	changedWebSearch := New("http://127.0.0.1:8317", secret)
	changedWebSearch.Env["CLAUDE_CODE_MAX_WEB_SEARCHES_PER_SESSION"] = "999"
	changedWebSearchData, err := json.Marshal(changedWebSearch)
	if err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		name string
		body []byte
	}{
		{"unknown field", append(append([]byte(nil), data[:len(data)-1]...), []byte(`,"extra":true}`)...)},
		{"trailing object", append(append([]byte(nil), data...), []byte(`{}`)...)},
		{"missing web search limit", missingWebSearchData},
		{"changed web search limit", changedWebSearchData},
		{"wrong overlay", []byte(`{"env":{"ANTHROPIC_AUTH_TOKEN":"wrong"}}`)},
		{"invalid env type", []byte(`{"env":{"ANTHROPIC_AUTH_TOKEN":1}}`)},
	} {
		t.Run(test.name, func(t *testing.T) {
			if err := os.WriteFile(path, test.body, 0o600); err != nil {
				t.Fatal(err)
			}
			err := ValidatePrivateFile(path, "http://127.0.0.1:8317", secret)
			if err == nil || strings.Contains(err.Error(), secret) {
				t.Fatalf("unsafe validation error = %v", err)
			}
		})
	}
}

func TestValidatePrivateFileRejectsSymlinkAndNonExactMode(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "settings.json")
	data, _ := json.Marshal(New("http://127.0.0.1:8317", "sk-test"))
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, 0o400); err != nil {
		t.Fatal(err)
	}
	if err := ValidatePrivateFile(path, "http://127.0.0.1:8317", "sk-test"); err == nil || !strings.Contains(err.Error(), "0600") {
		t.Fatalf("mode error = %v", err)
	}
	if err := os.Chmod(path, 0o600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, "settings-link.json")
	if err := os.Symlink(path, link); err != nil {
		t.Fatal(err)
	}
	if err := ValidatePrivateFile(link, "http://127.0.0.1:8317", "sk-test"); err == nil || !strings.Contains(err.Error(), "symbolic link") {
		t.Fatalf("symlink error = %v", err)
	}
}
