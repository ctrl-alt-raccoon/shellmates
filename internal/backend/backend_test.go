package backend

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/ctrl-alt-raccoon/sclaude/internal/config"
	"github.com/ctrl-alt-raccoon/sclaude/internal/managedsettings"
)

func managedRuntime(t *testing.T, credentialPath string) config.Runtime {
	t.Helper()
	data, err := os.ReadFile(credentialPath)
	if err != nil {
		t.Fatal(err)
	}
	var credential config.ProxyCredential
	if err := json.Unmarshal(data, &credential); err != nil {
		credential.BaseURL = "http://127.0.0.1:8317"
		credential.APIKey = "invalid-credential-placeholder"
	}
	settingsPath := filepath.Join(filepath.Dir(credentialPath), "managed settings.json")
	settingsData, err := json.Marshal(managedsettings.New(credential.BaseURL, credential.APIKey))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(settingsPath, settingsData, 0o600); err != nil {
		t.Fatal(err)
	}
	return config.Runtime{RealClaude: "/bin/claude", ClaudexMode: "managed_proxy", ProxyCredential: credentialPath, ManagedSettings: settingsPath}
}

func TestManagedProxyCommand(t *testing.T) {
	path := filepath.Join(t.TempDir(), "credential.json")
	data, _ := json.Marshal(map[string]any{"schema_version": 1, "base_url": "http://127.0.0.1:8317", "api_key": "sk-test-secret"})
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	runtime := managedRuntime(t, path)
	command, args, env, err := Command(runtime, "claudex", []string{"--continue"}, []string{"HOME=/tmp"})
	if err != nil {
		t.Fatal(err)
	}
	wantArgs := []string{"--settings", runtime.ManagedSettings, "--model", ManagedModel, "--disallowedTools=" + ManagedDisallowedClaudeAPISkill, "--continue"}
	if command != "/bin/claude" || !reflect.DeepEqual(args, wantArgs) {
		t.Fatalf("command=%q args=%q", command, args)
	}
	if strings.Contains(strings.Join(args, "\n"), "sk-test-secret") {
		t.Fatalf("managed argv exposed credential: %q", args)
	}
	joined := strings.Join(env, "\n")
	for _, wanted := range []string{
		"ANTHROPIC_BASE_URL=http://127.0.0.1:8317",
		"ANTHROPIC_AUTH_TOKEN=sk-test-secret",
		"ANTHROPIC_DEFAULT_OPUS_MODEL=gpt-5.6-sol(xhigh)",
		"ANTHROPIC_DEFAULT_SONNET_MODEL=gpt-5.6-sol(high)",
		"ANTHROPIC_DEFAULT_HAIKU_MODEL=gpt-5.6-luna(low)",
		"CLAUDE_CODE_AUTO_COMPACT_WINDOW=300000",
		"CLAUDE_AUTOCOMPACT_PCT_OVERRIDE=60",
		"CLAUDE_CODE_MAX_OUTPUT_TOKENS=64000",
		"CLAUDE_CODE_SUBAGENT_MODEL=gpt-5.6-sol(high)",
	} {
		if !strings.Contains(joined, wanted) {
			t.Fatalf("missing %q in env", wanted)
		}
	}
}

func TestExternalClaudexIsOpaque(t *testing.T) {
	runtime := config.Runtime{RealClaude: "/bin/claude", ClaudexMode: "external", RealClaudex: "/private/claudex"}
	command, args, _, err := Command(runtime, "claudex", []string{"-p", "x"}, nil)
	if err != nil || command != "/private/claudex" || strings.Join(args, " ") != "-p x" {
		t.Fatalf("command=%q args=%q err=%v", command, args, err)
	}
}

func TestManagedProxyRemovesConflictingProviderEnvironment(t *testing.T) {
	path := filepath.Join(t.TempDir(), "credential.json")
	data, _ := json.Marshal(map[string]any{"schema_version": 1, "base_url": "http://127.0.0.1:8317", "api_key": "sk-test-secret"})
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	runtime := managedRuntime(t, path)
	inherited := []string{
		"ANTHROPIC_API_KEY=stale",
		"ANTHROPIC_AUTH_TOKEN=old",
		"CLAUDE_CODE_USE_BEDROCK=1",
		"CLAUDE_CODE_SIMPLE=1",
		"CLAUDE_CODE_SIMPLE=stale",
		"CLAUDE_CODE_USE_VERTEX=1",
		"CLAUDE_CODE_USE_FOUNDRY=1",
		"ANTHROPIC_BEDROCK_BASE_URL=http://127.0.0.1:9991",
		"ANTHROPIC_VERTEX_BASE_URL=http://127.0.0.1:9992",
		"ANTHROPIC_FOUNDRY_BASE_URL=http://127.0.0.1:9993",
	}
	_, _, env, err := Command(runtime, "claudex", nil, inherited)
	if err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(env, "\n")
	values := map[string][]string{}
	for _, item := range env {
		key, value, _ := strings.Cut(item, "=")
		values[key] = append(values[key], value)
	}
	for _, key := range []string{
		"ANTHROPIC_API_KEY",
		"CLAUDE_CODE_SIMPLE",
		"CLAUDE_CODE_USE_BEDROCK",
		"CLAUDE_CODE_USE_VERTEX",
		"CLAUDE_CODE_USE_FOUNDRY",
		"ANTHROPIC_BEDROCK_BASE_URL",
		"ANTHROPIC_VERTEX_BASE_URL",
		"ANTHROPIC_FOUNDRY_BASE_URL",
	} {
		if !reflect.DeepEqual(values[key], []string{""}) {
			t.Fatalf("provider selector was not neutralized exactly once: key=%s values=%q", key, values[key])
		}
	}
	if strings.Count(joined, "ANTHROPIC_AUTH_TOKEN=") != 1 || !strings.Contains(joined, "ANTHROPIC_AUTH_TOKEN=sk-test-secret") {
		t.Fatalf("auth token was not replaced exactly once: %s", joined)
	}
}

func TestManagedProxyCallerModelOverridesDefault(t *testing.T) {
	path := filepath.Join(t.TempDir(), "credential.json")
	data, _ := json.Marshal(map[string]any{"schema_version": 1, "base_url": "http://127.0.0.1:8317", "api_key": "sk-test-secret"})
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	runtime := managedRuntime(t, path)
	_, args, _, err := Command(runtime, "claudex", []string{"--model", "other"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(args, " ")
	if joined != "--settings "+runtime.ManagedSettings+" --model gpt-5.6-sol(xhigh) --disallowedTools=Skill(claude-api) --model other" {
		t.Fatalf("managed defaults or caller model override are out of order: %q", joined)
	}
}

func TestManagedProxyPlacesDisallowedToolsBeforeCallerArguments(t *testing.T) {
	prefix := []string{
		"--settings", "/tmp/managed-settings.json",
		"--model", ManagedModel,
		"--disallowedTools=" + ManagedDisallowedClaudeAPISkill,
	}
	for _, test := range []struct {
		name string
		args []string
	}{
		{name: "value-taking argument", args: []string{"--append-system-prompt", "follow these instructions"}},
		{name: "variadic argument", args: []string{"--allowedTools", "Read", "Bash"}},
		{name: "positional prompt", args: []string{"explain this code"}},
		{name: "subcommand", args: []string{"auth", "status"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			args, err := managedArguments(test.args, "/tmp/managed-settings.json")
			if err != nil {
				t.Fatal(err)
			}
			want := append(append([]string(nil), prefix...), test.args...)
			if !reflect.DeepEqual(args, want) {
				t.Fatalf("managed arguments = %q, want %q", args, want)
			}
		})
	}
}

func TestManagedProxyRejectsBare(t *testing.T) {
	for _, args := range [][]string{
		{"--bare"},
		{"--bare=true"},
		{"--bare=false"},
		{"--bare=1"},
	} {
		_, err := managedArguments(args, "/tmp/managed-settings.json")
		if err == nil || !strings.Contains(err.Error(), "does not accept --bare") {
			t.Fatalf("args=%q expected bare error, got %v", args, err)
		}
	}
}

func TestManagedProxyRejectsDisallowedToolsOverride(t *testing.T) {
	path := filepath.Join(t.TempDir(), "credential.json")
	data, _ := json.Marshal(map[string]any{"schema_version": 1, "base_url": "http://127.0.0.1:8317", "api_key": "sk-test-secret"})
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	runtime := managedRuntime(t, path)
	for _, args := range [][]string{
		{"--disallowedTools", "none"},
		{"--disallowedTools=none"},
		{"--disallowed-tools", "none"},
		{"--disallowed-tools=none"},
	} {
		_, _, _, err := Command(runtime, "claudex", args, nil)
		if err == nil || !strings.Contains(err.Error(), "does not allow overriding") {
			t.Fatalf("args=%q expected disallowed-tools override error, got %v", args, err)
		}
	}
}

func TestManagedProxyRejectsSettingsOverrides(t *testing.T) {
	path := filepath.Join(t.TempDir(), "credential.json")
	if err := os.WriteFile(path, []byte(`{"schema_version":1,"base_url":"http://127.0.0.1:8317","api_key":"sk-test-secret"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	runtime := managedRuntime(t, path)
	for _, args := range [][]string{
		{"--settings", "/tmp/other.json"},
		{"--settings=/tmp/other.json"},
		{"--setting-sources", "project"},
		{"--setting-sources=user,project"},
	} {
		_, _, _, err := Command(runtime, "claudex", args, nil)
		if err == nil || !strings.Contains(err.Error(), "does not allow overriding") {
			t.Fatalf("args=%q expected settings override error, got %v", args, err)
		}
	}
}

func TestManagedProxyRejectsBroadOrMismatchedSettingsWithoutSecret(t *testing.T) {
	path := filepath.Join(t.TempDir(), "credential.json")
	secret := "sk-never-log-this"
	if err := os.WriteFile(path, []byte(`{"schema_version":1,"base_url":"http://127.0.0.1:8317","api_key":"`+secret+`"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	runtime := managedRuntime(t, path)
	if err := os.Chmod(runtime.ManagedSettings, 0o644); err != nil {
		t.Fatal(err)
	}
	_, args, _, err := Command(runtime, "claudex", nil, nil)
	if err == nil || !strings.Contains(err.Error(), "0600") || strings.Contains(err.Error(), secret) || strings.Contains(strings.Join(args, "\n"), secret) {
		t.Fatalf("unsafe broad-settings result: args=%q err=%v", args, err)
	}
	if err := os.Chmod(runtime.ManagedSettings, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(runtime.ManagedSettings, []byte(`{"env":{"ANTHROPIC_AUTH_TOKEN":"wrong"}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	_, args, _, err = Command(runtime, "claudex", nil, nil)
	if err == nil || !strings.Contains(err.Error(), "does not match") || strings.Contains(err.Error(), secret) || strings.Contains(strings.Join(args, "\n"), secret) {
		t.Fatalf("unsafe mismatched-settings result: args=%q err=%v", args, err)
	}
	data, err := json.Marshal(map[string]any{
		"env":         managedsettings.Environment("http://127.0.0.1:8317", secret),
		"permissions": map[string]any{"allow": []string{"Bash(*)"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(runtime.ManagedSettings, data, 0o600); err != nil {
		t.Fatal(err)
	}
	_, args, _, err = Command(runtime, "claudex", nil, nil)
	if err == nil || !strings.Contains(err.Error(), "invalid JSON document") || strings.Contains(err.Error(), secret) || strings.Contains(strings.Join(args, "\n"), secret) {
		t.Fatalf("unsafe extra-settings result: args=%q err=%v", args, err)
	}
}

func TestManagedProxyRejectsArgumentDelimiter(t *testing.T) {
	path := filepath.Join(t.TempDir(), "credential.json")
	data, _ := json.Marshal(map[string]any{"schema_version": 1, "base_url": "http://127.0.0.1:8317", "api_key": "sk-test-secret"})
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	runtime := managedRuntime(t, path)
	_, _, _, err := Command(runtime, "claudex", []string{"--", "prompt"}, nil)
	if err == nil || !strings.Contains(err.Error(), "argument delimiter") {
		t.Fatalf("expected argument delimiter error, got %v", err)
	}
}

func TestManagedProxyRejectsNonLoopbackCredential(t *testing.T) {
	path := filepath.Join(t.TempDir(), "credential.json")
	if err := os.WriteFile(path, []byte(`{"schema_version":1,"base_url":"https://example.test","api_key":"sk-test"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	runtime := managedRuntime(t, path)
	_, _, _, err := Command(runtime, "claudex", nil, nil)
	if err == nil || !strings.Contains(err.Error(), "127.0.0.1:8317") {
		t.Fatalf("expected managed endpoint error, got %v", err)
	}
}

func TestManagedProxyRejectsWrongLoopbackPort(t *testing.T) {
	path := filepath.Join(t.TempDir(), "credential.json")
	if err := os.WriteFile(path, []byte(`{"schema_version":1,"base_url":"http://127.0.0.1:9999","api_key":"sk-test"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	runtime := managedRuntime(t, path)
	_, _, _, err := Command(runtime, "claudex", nil, nil)
	if err == nil || !strings.Contains(err.Error(), "127.0.0.1:8317") {
		t.Fatalf("expected managed port error, got %v", err)
	}
}

func TestManagedProxyRequiresPrivateCredential(t *testing.T) {
	path := filepath.Join(t.TempDir(), "credential.json")
	secret := "sk-never-log-this"
	if err := os.WriteFile(path, []byte(`{"schema_version":1,"base_url":"http://127.0.0.1:8317","api_key":"`+secret+`"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	runtime := managedRuntime(t, path)
	_, _, _, err := Command(runtime, "claudex", nil, nil)
	if err == nil || !strings.Contains(err.Error(), "0600") {
		t.Fatalf("error = %v, want private permissions failure", err)
	}
	if strings.Contains(err.Error(), secret) {
		t.Fatalf("error exposed credential: %v", err)
	}
}

func TestManagedProxyInvalidJSONDoesNotExposeSecret(t *testing.T) {
	path := filepath.Join(t.TempDir(), "credential.json")
	secret := "sk-never-log-this"
	if err := os.WriteFile(path, []byte(`{"schema_version":1,"api_key":"`+secret), 0o600); err != nil {
		t.Fatal(err)
	}
	runtime := managedRuntime(t, path)
	_, _, _, err := Command(runtime, "claudex", nil, nil)
	if err == nil || strings.Contains(err.Error(), secret) {
		t.Fatalf("unsafe parse error: %v", err)
	}
}

func TestClaudeCommandDoesNotReadManagedCredential(t *testing.T) {
	runtime := config.Runtime{RealClaude: "/bin/claude", ClaudexMode: "managed_proxy", ProxyCredential: filepath.Join(t.TempDir(), "missing")}
	command, args, env, err := Command(runtime, "claude", []string{"--version"}, []string{"X=1"})
	if err != nil || command != "/bin/claude" || strings.Join(args, " ") != "--version" || strings.Join(env, " ") != "X=1" {
		t.Fatalf("command=%q args=%q env=%q err=%v", command, args, env, err)
	}
}

func TestResolveRealRejectsExcludedRoot(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "claude")
	if err := os.WriteFile(path, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", root)
	if _, err := ResolveReal("claude", root); err == nil || !strings.Contains(err.Error(), "excluded project path") {
		t.Fatalf("ResolveReal error = %v", err)
	}
}
