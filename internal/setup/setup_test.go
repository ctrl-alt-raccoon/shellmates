package setup

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	configpkg "github.com/ctrl-alt-raccoon/shellmates/internal/config"
	"github.com/ctrl-alt-raccoon/shellmates/internal/managedsettings"
	screenpkg "github.com/ctrl-alt-raccoon/shellmates/internal/screen"
)

type recordingRunner struct {
	commands       [][]string
	outputCommands [][]string
	outputs        map[string][]byte
}

func (r *recordingRunner) Run(_ context.Context, name string, args ...string) error {
	r.commands = append(r.commands, append([]string{name}, args...))
	return nil
}

func (r *recordingRunner) Output(_ context.Context, name string, args ...string) ([]byte, error) {
	arguments := append([]string{name}, args...)
	r.outputCommands = append(r.outputCommands, arguments)
	command := strings.Join(arguments, "\x00")
	if output, ok := r.outputs[command]; ok {
		return append([]byte(nil), output...), nil
	}
	switch strings.Join(append([]string{filepath.Base(name)}, args...), " ") {
	case "systemctl --user is-active cliproxyapi.service":
		return []byte("inactive\n"), errors.New("exit status 3")
	case "systemctl --user is-enabled cliproxyapi.service":
		return []byte("disabled\n"), errors.New("exit status 1")
	case "brew services list --json":
		return []byte("[]\n"), nil
	default:
		return nil, nil
	}
}

type failingRunner struct {
	err error
}

func (r failingRunner) Run(context.Context, string, ...string) error {
	return r.err
}

func (r failingRunner) Output(_ context.Context, name string, args ...string) ([]byte, error) {
	command := strings.Join(append([]string{filepath.Base(name)}, args...), " ")
	switch command {
	case "systemctl --user is-active cliproxyapi.service":
		return []byte("inactive\n"), errors.New("exit status 3")
	case "systemctl --user is-enabled cliproxyapi.service":
		return []byte("disabled\n"), errors.New("exit status 1")
	default:
		return nil, r.err
	}
}

type stageRunner struct {
	commands [][]string
	runErrs  map[string]error
}

func (r *stageRunner) Run(_ context.Context, name string, args ...string) error {
	command := append([]string{name}, args...)
	r.commands = append(r.commands, command)
	return r.runErrs[strings.Join(command, " ")]
}

func (r *stageRunner) Output(_ context.Context, name string, args ...string) ([]byte, error) {
	switch strings.Join(append([]string{filepath.Base(name)}, args...), " ") {
	case "systemctl --user is-active cliproxyapi.service":
		return []byte("inactive\n"), errors.New("exit status 3")
	case "systemctl --user is-enabled cliproxyapi.service":
		return []byte("disabled\n"), errors.New("exit status 1")
	case "brew services list --json":
		return []byte("[]\n"), nil
	default:
		return nil, nil
	}
}

func TestEnsureProxyConfigPreservesKeys(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.yaml")
	credentialPath := filepath.Join(dir, "private", "proxy.json")
	original := "host: \"\"\nport: 9000\nauth-dir: ~/.cli-proxy-api\napi-keys:\n  - existing-key\ndebug: true\n"
	if err := os.WriteFile(configPath, []byte(original), 0o600); err != nil {
		t.Fatal(err)
	}
	result, err := EnsureProxyConfig(configPath, credentialPath)
	if err != nil {
		t.Fatal(err)
	}
	if !result.ChangedConfig || !result.CreatedAPIKey || result.APIKeySHA256 == "" {
		t.Fatalf("unexpected result: %+v", result)
	}
	data, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatal(err)
	}
	text := string(data)
	for _, wanted := range []string{
		`host: "127.0.0.1"`,
		"port: 8317",
		"  - existing-key",
		"debug: true",
		"sk-",
		"oauth-model-alias:",
		`alias: "claude-opus-4-8"`,
		`alias: "claude-fable-5"`,
		`alias: "claude-sonnet-5"`,
		`alias: "claude-haiku-4-5-20251001"`,
		"fork: true",
	} {
		if !strings.Contains(text, wanted) {
			t.Fatalf("missing %q in:\n%s", wanted, text)
		}
	}
	info, err := os.Stat(credentialPath)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("credential mode = %o", info.Mode().Perm())
	}
	configInfo, err := os.Stat(configPath)
	if err != nil {
		t.Fatal(err)
	}
	if configInfo.Mode().Perm() != 0o600 {
		t.Fatalf("config mode = %o", configInfo.Mode().Perm())
	}
	backupInfo, err := os.Stat(result.BackupPath)
	if err != nil {
		t.Fatal(err)
	}
	if backupInfo.Mode().Perm() != 0o600 {
		t.Fatalf("backup mode = %o", backupInfo.Mode().Perm())
	}
	second, err := EnsureProxyConfig(configPath, credentialPath)
	if err != nil {
		t.Fatal(err)
	}
	if second.ChangedConfig || second.CreatedAPIKey {
		t.Fatalf("second run should be idempotent: %+v", second)
	}
}

func TestEnsureProxyConfigRemovesPlaceholderKeysAndPreservesOtherAliases(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.yaml")
	credentialPath := filepath.Join(dir, "credential.json")
	original := `host: ""
api-keys:
  - "your-api-key-1"
  - "kept-key"
oauth-model-alias:
  codex:
    - { name: "gpt-old", alias: "claude-opus-4-8", fork: false }
    - { name: "gpt-custom", alias: "custom-model", fork: true }
  gemini:
    - { name: "gemini-model", alias: "gemini-alias", fork: true }
`
	if err := os.WriteFile(configPath, []byte(original), 0o644); err != nil {
		t.Fatal(err)
	}
	result, err := EnsureProxyConfig(configPath, credentialPath)
	if err != nil {
		t.Fatal(err)
	}
	if !result.ChangedConfig {
		t.Fatal("expected config change")
	}
	data, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatal(err)
	}
	text := string(data)
	for _, unwanted := range []string{"your-api-key-1", `name: "gpt-old"`} {
		if strings.Contains(text, unwanted) {
			t.Fatalf("unexpected %q in:\n%s", unwanted, text)
		}
	}
	for _, wanted := range []string{"kept-key", "custom-model", "gemini:", "gemini-alias", `name: "gpt-5.6-sol"`, `alias: "claude-opus-4-8"`, "fork: true"} {
		if !strings.Contains(text, wanted) {
			t.Fatalf("missing %q in:\n%s", wanted, text)
		}
	}
	info, err := os.Stat(configPath)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("config mode = %o", info.Mode().Perm())
	}
}

func TestEnsureProxyConfigRetightensPermissionsWithoutContentChange(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.yaml")
	credentialPath := filepath.Join(dir, "credential.json")
	if err := os.WriteFile(configPath, []byte("host: \"\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := EnsureProxyConfig(configPath, credentialPath); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(configPath, 0o644); err != nil {
		t.Fatal(err)
	}
	second, err := EnsureProxyConfig(configPath, credentialPath)
	if err != nil {
		t.Fatal(err)
	}
	if second.ChangedConfig {
		t.Fatalf("expected no content change: %+v", second)
	}
	info, err := os.Stat(configPath)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("config mode = %o, want 600", info.Mode().Perm())
	}
}

func TestEnsureProxyConfigRemovesSpecialModeBits(t *testing.T) {
	for _, test := range []struct {
		name string
		bit  os.FileMode
	}{
		{name: "setuid", bit: os.ModeSetuid},
		{name: "setgid", bit: os.ModeSetgid},
		{name: "sticky", bit: os.ModeSticky},
	} {
		t.Run(test.name, func(t *testing.T) {
			dir := t.TempDir()
			configPath := filepath.Join(dir, "config.yaml")
			credentialPath := filepath.Join(dir, "credential.json")
			if err := os.WriteFile(configPath, []byte("host: \"\"\n"), 0o600); err != nil {
				t.Fatal(err)
			}
			if _, err := EnsureProxyConfig(configPath, credentialPath); err != nil {
				t.Fatal(err)
			}
			if err := os.Chmod(configPath, 0o600|test.bit); err != nil {
				t.Fatal(err)
			}
			before, err := os.Lstat(configPath)
			if err != nil {
				t.Fatal(err)
			}
			if before.Mode()&test.bit == 0 {
				t.Fatalf("special mode bit was not set: %v", before.Mode())
			}

			result, err := EnsureProxyConfig(configPath, credentialPath)
			if err != nil {
				t.Fatal(err)
			}
			if result.ChangedConfig {
				t.Fatalf("expected a mode-only repair: %+v", result)
			}
			after, err := os.Lstat(configPath)
			if err != nil {
				t.Fatal(err)
			}
			if after.Mode() != 0o600 {
				t.Fatalf("config mode = %v, want -rw-------", after.Mode())
			}
		})
	}
}

func TestEnsureProxyConfigRemovesUnquotedRequiredAlias(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.yaml")
	original := "host: \"\"\noauth-model-alias:\n  codex:\n    - { name: gpt-old, alias: claude-opus-4-8, fork: false }\n"
	if err := os.WriteFile(configPath, []byte(original), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := EnsureProxyConfig(configPath, filepath.Join(dir, "credential.json")); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "gpt-old") {
		t.Fatalf("stale unquoted alias entry survived:\n%s", data)
	}
	if strings.Count(string(data), `"claude-opus-4-8"`) != 1 {
		t.Fatalf("expected exactly one claude-opus-4-8 alias:\n%s", data)
	}
}

func TestEnsureProxyConfigReplacesBlockStyleRequiredAlias(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.yaml")
	original := "host: \"\"\noauth-model-alias:\n  codex:\n    - name: gpt-old\n      alias: claude-opus-4-8\n      fork: false\n"
	if err := os.WriteFile(configPath, []byte(original), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := EnsureProxyConfig(configPath, filepath.Join(dir, "credential.json")); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "gpt-old") {
		t.Fatalf("stale block-style alias survived:\n%s", data)
	}
	if strings.Count(string(data), `"claude-opus-4-8"`) != 1 {
		t.Fatalf("expected exactly one required alias:\n%s", data)
	}
}

func TestEnsureProxyConfigRejectsDuplicateManagedTopLevelKeysBeforeMutation(t *testing.T) {
	for _, key := range []string{"host", "port", "auth-dir", "api-keys", "oauth-model-alias"} {
		t.Run(key, func(t *testing.T) {
			assertProxyConfigRejectedWithoutMutation(t, key+":\n\""+key+"\":\n", "duplicate top-level", key)
		})
	}
}

func TestEnsureProxyConfigAcceptsSupportedManagedYAMLSyntax(t *testing.T) {
	for _, test := range []struct {
		name     string
		original string
	}{
		{name: "explicit key", original: "? host\n: 0.0.0.0\n"},
		{name: "tagged key", original: "!!str host: 0.0.0.0\n"},
		{name: "auth-dir literal block", original: "auth-dir: |\n  ~/.cli-proxy-api\n"},
		{name: "top level flow map", original: `{host: "0.0.0.0", port: 9000}` + "\n"},
		{name: "document flow map", original: `--- {host: "0.0.0.0", port: 9000}` + "\n"},
	} {
		t.Run(test.name, func(t *testing.T) {
			dir := t.TempDir()
			configPath := filepath.Join(dir, "config.yaml")
			if err := os.WriteFile(configPath, []byte(test.original), 0o600); err != nil {
				t.Fatal(err)
			}
			if _, err := EnsureProxyConfig(configPath, filepath.Join(dir, "credential.json")); err != nil {
				t.Fatal(err)
			}
			data, err := os.ReadFile(configPath)
			if err != nil {
				t.Fatal(err)
			}
			if err := ValidateProxyConfigYAML(data); err != nil {
				t.Fatalf("updated YAML is invalid: %v\n%s", err, data)
			}
		})
	}
}

func TestEnsureProxyConfigRejectsUnsafeOrInvalidManagedYAMLSyntaxBeforeMutation(t *testing.T) {
	for _, test := range []struct {
		name     string
		original string
		want     string
	}{
		{name: "anchored key", original: "&k host: 0.0.0.0\n", want: "host contains a YAML anchor"},
		{name: "host mapping value", original: "host:\n  interface: eth0\n", want: "host must be a string scalar"},
		{name: "port sequence value", original: "port:\n  - 8317\n", want: "port must be an integer scalar"},
		{name: "duplicate codex quoted", original: "oauth-model-alias:\n  codex: []\n  \"codex\": []\n", want: "duplicate oauth-model-alias.codex"},
		{name: "duplicate codex single quoted", original: "oauth-model-alias:\n  'codex': []\n  codex: []\n", want: "duplicate oauth-model-alias.codex"},
	} {
		t.Run(test.name, func(t *testing.T) {
			assertProxyConfigRejectedWithoutMutation(t, test.original, test.want)
		})
	}
}

func assertProxyConfigRejectedWithoutMutation(t *testing.T, original string, wantError ...string) {
	t.Helper()
	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.yaml")
	credentialPath := filepath.Join(dir, "credential.json")
	if err := os.WriteFile(configPath, []byte(original), 0o644); err != nil {
		t.Fatal(err)
	}

	_, err := EnsureProxyConfig(configPath, credentialPath)
	if err == nil {
		t.Fatal("expected unsafe YAML rejection")
	}
	for _, wanted := range wantError {
		if !strings.Contains(err.Error(), wanted) {
			t.Fatalf("error = %v, want substring %q", err, wanted)
		}
	}
	data, readErr := os.ReadFile(configPath)
	if readErr != nil {
		t.Fatal(readErr)
	}
	if string(data) != original {
		t.Fatalf("config mutated: %q", data)
	}
	info, statErr := os.Stat(configPath)
	if statErr != nil {
		t.Fatal(statErr)
	}
	if info.Mode().Perm() != 0o644 {
		t.Fatalf("config mode mutated to %o", info.Mode().Perm())
	}
	if _, statErr := os.Stat(credentialPath); !os.IsNotExist(statErr) {
		t.Fatalf("credential was created before YAML rejection: %v", statErr)
	}
	matches, globErr := filepath.Glob(configPath + ".sclaude-backup-*")
	if globErr != nil {
		t.Fatal(globErr)
	}
	if len(matches) != 0 {
		t.Fatalf("backup was created before YAML rejection: %v", matches)
	}
}

func TestConfigureShellPATHDryRunWritesNothing(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("HOME", dir)
	if err := os.WriteFile(filepath.Join(dir, ".zshrc"), []byte("export OTHER=1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	edits, err := ConfigureShellPATH("/tmp/bin", SetupOptions{DryRun: true})
	if err != nil || len(edits) != 0 {
		t.Fatalf("edits=%v err=%v", edits, err)
	}
	data, err := os.ReadFile(filepath.Join(dir, ".zshrc"))
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "export OTHER=1\n" {
		t.Fatalf("dry-run modified .zshrc: %q", data)
	}
	if _, err := os.Stat(filepath.Join(dir, ".config", "fish", "conf.d", "sclaude.fish")); !os.IsNotExist(err) {
		t.Fatalf("dry-run created fish conf: %v", err)
	}
}

func TestEnsureProxyConfigRejectsNonLoopbackExistingCredential(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.yaml")
	credentialPath := filepath.Join(dir, "credential.json")
	if err := os.WriteFile(configPath, []byte("host: \"\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(credentialPath, []byte(`{"schema_version":1,"base_url":"https://example.test","api_key":"sk-test"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := EnsureProxyConfig(configPath, credentialPath)
	if err == nil || !strings.Contains(err.Error(), "127.0.0.1") {
		t.Fatalf("expected loopback credential error, got %v", err)
	}
}

func TestEnsureProxyConfigRejectsBroadExistingCredential(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.yaml")
	credentialPath := filepath.Join(dir, "credential.json")
	if err := os.WriteFile(configPath, []byte("host: \"\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(credentialPath, []byte(`{"schema_version":1,"base_url":"http://127.0.0.1:8317","api_key":"sk-test"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := EnsureProxyConfig(configPath, credentialPath)
	if err == nil || !strings.Contains(err.Error(), "0600") {
		t.Fatalf("expected private credential error, got %v", err)
	}
}

func TestEnsureProxyConfigRejectsInlineKeys(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(configPath, []byte("api-keys: nope\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := EnsureProxyConfig(configPath, filepath.Join(dir, "credential.json"))
	if err == nil || !strings.Contains(err.Error(), "api-keys must be a sequence") {
		t.Fatalf("expected sequence error, got %v", err)
	}
}

func TestShellPATHIdempotentAndRemovable(t *testing.T) {
	path := filepath.Join(t.TempDir(), ".zshrc")
	if err := os.WriteFile(path, []byte("export OTHER=1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	first, err := EnsureShellPATH(path, "/tmp/bin with space", "zsh")
	if err != nil || !first.Changed {
		t.Fatalf("first ensure: %+v %v", first, err)
	}
	second, err := EnsureShellPATH(path, "/tmp/bin with space", "zsh")
	if err != nil || second.Changed {
		t.Fatalf("second ensure: %+v %v", second, err)
	}
	removed, err := RemoveShellPATH(path)
	if err != nil || !removed.Changed {
		t.Fatalf("remove: %+v %v", removed, err)
	}
	data, _ := os.ReadFile(path)
	if string(data) != "export OTHER=1\n" {
		t.Fatalf("unexpected content %q", data)
	}
}

func TestInstallRollbackAndUninstall(t *testing.T) {
	dir := t.TempDir()
	layout := InstallLayout{DataDir: filepath.Join(dir, "data"), StateDir: filepath.Join(dir, "state"), BinDir: filepath.Join(dir, "bin")}
	source1 := filepath.Join(dir, "v1")
	source2 := filepath.Join(dir, "v2")
	if err := os.WriteFile(source1, []byte("one"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(source2, []byte("two"), 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := InstallBinary(source1, "v1.0.0", layout); err != nil {
		t.Fatal(err)
	}
	ledger, err := InstallBinary(source2, "v2.0.0", layout)
	if err != nil {
		t.Fatal(err)
	}
	if ledger.Current != "v2.0.0" || ledger.Previous != "v1.0.0" {
		t.Fatalf("unexpected ledger: %+v", ledger)
	}
	rolled, err := Rollback(layout)
	if err != nil {
		t.Fatal(err)
	}
	if rolled.Current != "v1.0.0" {
		t.Fatalf("rollback current: %+v", rolled)
	}
	if err := Uninstall(layout, false); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(filepath.Join(layout.BinDir, "sclaude")); !os.IsNotExist(err) {
		t.Fatalf("sclaude link remains: %v", err)
	}
}

func TestExplicitProxyExecutableSatisfiesDependencySetup(t *testing.T) {
	dir := t.TempDir()
	proxy := filepath.Join(dir, "cli-proxy-api")
	if err := os.WriteFile(proxy, nil, 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir)

	for _, command := range []string{"curl", "git", "screen", "claude", "codex"} {
		if err := os.WriteFile(filepath.Join(dir, command), nil, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	if err := RunSetup(context.Background(), SetupOptions{
		CLIProxyExecutable: proxy,
		NonInteractive:     true,
		SkipCodex:          true,
	}); err != nil {
		t.Fatalf("explicit proxy executable did not suppress dependency installation: %v", err)
	}

	dependencies := checkDependencies(SetupOptions{CLIProxyExecutable: proxy})
	for _, dependency := range dependencies {
		if dependency.Command != "cliproxyapi" {
			continue
		}
		want, err := filepath.EvalSymlinks(proxy)
		if err != nil {
			t.Fatal(err)
		}
		if !dependency.Installed || dependency.Path != want {
			t.Fatalf("proxy dependency = %+v, want path %q", dependency, want)
		}
		resolved, err := ValidateCLIProxyExecutable(dependency.Path)
		if err != nil {
			t.Fatal(err)
		}
		if resolved != want {
			t.Fatalf("resolved proxy = %q, want %q", resolved, want)
		}
		return
	}
	t.Fatal("CLIProxyAPI dependency was not reported")
}

func TestExplicitInvalidProxyExecutableIsMissingDependency(t *testing.T) {
	proxy := filepath.Join(t.TempDir(), "cli-proxy-api")
	if err := os.WriteFile(proxy, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	for _, dependency := range checkDependencies(SetupOptions{CLIProxyExecutable: proxy}) {
		if dependency.Command != "cliproxyapi" {
			continue
		}
		if dependency.Installed || dependency.Path != "" {
			t.Fatalf("proxy dependency = %+v", dependency)
		}
		return
	}
	t.Fatal("CLIProxyAPI dependency was not reported")
}

func TestRunSetupRejectsInvalidExplicitProxyExecutableInDryRun(t *testing.T) {
	proxy := filepath.Join(t.TempDir(), "cli-proxy-api")
	if err := os.WriteFile(proxy, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	var output bytes.Buffer
	err := RunSetup(context.Background(), SetupOptions{
		CLIProxyExecutable: proxy,
		DryRun:             true,
		SkipProxy:          true,
		Output:             &output,
		ErrorOutput:        io.Discard,
	})
	if err == nil || !strings.Contains(err.Error(), "regular executable file") {
		t.Fatalf("error = %v", err)
	}
	if output.Len() != 0 {
		t.Fatalf("setup performed work before rejecting proxy: %q", output.String())
	}
}

type doctorScreenStub struct {
	lists    [][]screenpkg.Socket
	listErrs []error
	startErr error
	stopErr  error
	started  []string
	stopped  []string
}

func (s *doctorScreenStub) List(context.Context) ([]screenpkg.Socket, error) {
	var err error
	if len(s.listErrs) > 0 {
		err = s.listErrs[0]
		s.listErrs = s.listErrs[1:]
	}
	if len(s.lists) == 0 {
		return nil, err
	}
	result := s.lists[0]
	s.lists = s.lists[1:]
	return result, err
}

func (s *doctorScreenStub) StartDetached(_ context.Context, name string) error {
	s.started = append(s.started, name)
	return s.startErr
}

func (s *doctorScreenStub) Stop(_ context.Context, name string) error {
	s.stopped = append(s.stopped, name)
	return s.stopErr
}

type doctorCanceledContextScreen struct {
	doctorScreenStub
	cancel                   context.CancelFunc
	listCalls                int
	cleanupContextActive     bool
	cleanupListContextActive bool
}

type doctorBlockingListScreen struct {
	doctorScreenStub
	listContextCanceled bool
}

func (screen *doctorBlockingListScreen) List(ctx context.Context) ([]screenpkg.Socket, error) {
	<-ctx.Done()
	screen.listContextCanceled = true
	return nil, ctx.Err()
}

func (screen *doctorCanceledContextScreen) List(ctx context.Context) ([]screenpkg.Socket, error) {
	screen.listCalls++
	if screen.listCalls == 1 {
		screen.cancel()
		<-ctx.Done()
		return nil, ctx.Err()
	}
	screen.cleanupListContextActive = ctx.Err() == nil
	if screen.listCalls == 2 {
		return []screenpkg.Socket{{PID: 1234, Name: "doctor"}}, nil
	}
	return nil, nil
}

func (screen *doctorCanceledContextScreen) Stop(ctx context.Context, name string) error {
	screen.cleanupContextActive = ctx.Err() == nil
	return screen.doctorScreenStub.Stop(ctx, name)
}

func TestDoctorExternalModeUsesConfiguredExecutables(t *testing.T) {
	dir := t.TempDir()
	claude := filepath.Join(dir, "claude-opaque")
	claudex := filepath.Join(dir, "claudex-opaque")
	screenPath := filepath.Join(dir, "screen-opaque")
	for _, path := range []string{claude, claudex, screenPath} {
		if err := os.WriteFile(path, nil, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	runtimePath := filepath.Join(dir, "runtime.json")
	if err := configpkg.Save(runtimePath, configpkg.Runtime{
		SchemaVersion:   configpkg.SchemaVersion,
		RealClaude:      claude,
		ClaudexMode:     "external",
		RealClaudex:     claudex,
		ScreenPath:      screenPath,
		CLIProxyService: "none",
	}); err != nil {
		t.Fatal(err)
	}
	screen := &doctorScreenStub{lists: [][]screenpkg.Socket{{{PID: 1234, Name: "doctor"}}, nil}}
	paths := configpkg.Paths{ConfigFile: runtimePath, StateRoot: filepath.Join(dir, "state")}
	report := RunDoctorWithOptions(context.Background(), paths, DoctorOptions{Screen: screen, SessionName: "doctor", PollInterval: time.Millisecond, PollTimeout: time.Second})
	checks := map[string]Check{}
	for _, check := range report.Checks {
		checks[check.Name] = check
	}
	for name, path := range map[string]string{"Claude Code": claude, "claudex": claudex, "GNU Screen": screenPath} {
		if check, ok := checks[name]; !ok || !check.OK || check.Detail != path {
			t.Fatalf("%s check = %+v", name, check)
		}
	}
	if check := checks["GNU Screen lifecycle"]; !check.OK {
		t.Fatalf("screen lifecycle check = %+v", check)
	}
	for _, name := range []string{"curl", "git", "Codex CLI", "CLIProxyAPI", "CLIProxyAPI config", "private proxy credential", "CLIProxyAPI service"} {
		if _, ok := checks[name]; ok {
			t.Fatalf("external doctor unexpectedly included %q", name)
		}
	}
}

func TestDoctorReportsRuntimeLoadFailure(t *testing.T) {
	dir := t.TempDir()
	report := RunDoctor(context.Background(), configpkg.Paths{
		ConfigFile: filepath.Join(dir, "missing.json"),
		StateRoot:  filepath.Join(dir, "state"),
	})
	if report.OK() || len(report.Checks) != 2 || report.Checks[1].Name != "runtime configuration" || report.Checks[1].OK {
		t.Fatalf("report = %+v", report)
	}
}

func TestDoctorScreenLifecycleUsesFreshCleanupContextAfterCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	screen := &doctorCanceledContextScreen{cancel: cancel}
	check := checkScreenLifecycle(ctx, DoctorOptions{
		Screen:         screen,
		SessionName:    "doctor",
		PollInterval:   time.Millisecond,
		PollTimeout:    time.Second,
		CleanupTimeout: time.Second,
	})
	if check.OK || !strings.Contains(check.Detail, "observe: context canceled") {
		t.Fatalf("check = %+v", check)
	}
	if len(screen.started) != 1 || screen.started[0] != "doctor" {
		t.Fatalf("started = %q", screen.started)
	}
	if len(screen.stopped) != 1 || screen.stopped[0] != "1234.doctor" {
		t.Fatalf("stopped = %q", screen.stopped)
	}
	if !screen.cleanupContextActive {
		t.Fatal("cleanup Stop received the canceled observation context")
	}
	if !screen.cleanupListContextActive {
		t.Fatal("cleanup List received the canceled observation context")
	}
}

func TestDoctorScreenLifecyclePollTimeoutCancelsBlockingList(t *testing.T) {
	screen := &doctorBlockingListScreen{}
	started := time.Now()
	check := checkScreenLifecycle(context.Background(), DoctorOptions{
		Screen:         screen,
		SessionName:    "doctor",
		PollInterval:   time.Millisecond,
		PollTimeout:    20 * time.Millisecond,
		CleanupTimeout: 20 * time.Millisecond,
	})
	if check.OK || !strings.Contains(check.Detail, "observe: timed out waiting for disposable session to be observable") {
		t.Fatalf("check = %+v", check)
	}
	if !screen.listContextCanceled {
		t.Fatal("blocking Screen List did not receive a canceled poll context")
	}
	if elapsed := time.Since(started); elapsed > time.Second {
		t.Fatalf("blocking Screen List exceeded bounded time: %s", elapsed)
	}
}

func TestDoctorScreenLifecycleReportsStagesAndIsolation(t *testing.T) {
	for _, test := range []struct {
		name       string
		screen     *doctorScreenStub
		want       string
		wantStarts int
		wantStops  int
	}{
		{name: "start", screen: &doctorScreenStub{startErr: errors.New("start failed")}, want: "start: start failed", wantStarts: 1},
		{
			name: "observe",
			screen: &doctorScreenStub{
				lists:    [][]screenpkg.Socket{nil, {{PID: 1234, Name: "doctor"}}, nil},
				listErrs: []error{errors.New("list failed")},
			},
			want:       "observe: list failed",
			wantStarts: 1,
			wantStops:  1,
		},
		{name: "stop", screen: &doctorScreenStub{lists: [][]screenpkg.Socket{{{PID: 1234, Name: "doctor"}}, nil}, stopErr: errors.New("stop failed")}, want: "stop: stop failed", wantStarts: 1, wantStops: 1},
		{name: "cleanup", screen: &doctorScreenStub{lists: [][]screenpkg.Socket{{{PID: 1234, Name: "doctor"}}, nil}, listErrs: []error{nil, errors.New("cleanup failed")}}, want: "cleanup: cleanup failed", wantStarts: 1, wantStops: 1},
	} {
		t.Run(test.name, func(t *testing.T) {
			check := checkScreenLifecycle(context.Background(), DoctorOptions{Screen: test.screen, SessionName: "doctor", PollInterval: time.Millisecond, PollTimeout: time.Second})
			if check.OK || !strings.Contains(check.Detail, test.want) {
				t.Fatalf("check = %+v, want containing %q", check, test.want)
			}
			if len(test.screen.started) != test.wantStarts || len(test.screen.stopped) != test.wantStops {
				t.Fatalf("started=%q stopped=%q", test.screen.started, test.screen.stopped)
			}
		})
	}
}

func TestDoctorManagedModeValidatesConfiguredArtifactsAndEndpoints(t *testing.T) {
	dir := t.TempDir()
	claude := writeDoctorExecutable(t, dir, "claude")
	screenPath := writeDoctorExecutable(t, dir, "screen")
	proxy := writeDoctorExecutable(t, dir, "cliproxyapi")
	credentialPath := filepath.Join(dir, "credential.json")
	managedSettingsPath := filepath.Join(dir, "managed-settings.json")
	proxyConfigPath := filepath.Join(dir, "proxy.yaml")
	runtimePath := filepath.Join(dir, "runtime.json")
	credential := configpkg.ProxyCredential{SchemaVersion: configpkg.ProxyCredentialSchema, BaseURL: configpkg.ManagedProxyBaseURL, APIKey: "sk-doctor-secret"}
	credentialData, err := configpkg.EncodeProxyCredential(credential)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(credentialPath, credentialData, 0o600); err != nil {
		t.Fatal(err)
	}
	settingsData, err := managedsettings.Encode(credential.BaseURL, credential.APIKey)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(managedSettingsPath, settingsData, 0o600); err != nil {
		t.Fatal(err)
	}
	proxyData, _, err := mutateProxyYAML([]byte("host: 0.0.0.0\nport: 9000\napi-keys: []\noauth-model-alias: {codex: []}\n"), credential.APIKey)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(proxyConfigPath, proxyData, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := configpkg.Save(runtimePath, configpkg.Runtime{
		SchemaVersion:      configpkg.SchemaVersion,
		RealClaude:         claude,
		ClaudexMode:        "managed_proxy",
		ScreenPath:         screenPath,
		CLIProxyExecutable: proxy,
		CLIProxyConfig:     proxyConfigPath,
		ProxyCredential:    credentialPath,
		ManagedSettings:    managedSettingsPath,
		CLIProxyService:    "none",
	}); err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer "+credential.APIKey {
			t.Errorf("authorization = %q", r.Header.Get("Authorization"))
		}
		switch r.URL.Path {
		case "/v1/models":
			_ = json.NewEncoder(w).Encode(map[string]any{"data": []map[string]string{
				{"id": "gpt-5.6-sol"}, {"id": "gpt-5.6-terra"}, {"id": "gpt-5.6-luna"},
				{"id": "claude-opus-4-8"}, {"id": "claude-fable-5"}, {"id": "claude-sonnet-5"}, {"id": "claude-haiku-4-5-20251001"},
			}})
		case "/v1/messages":
			_ = json.NewEncoder(w).Encode(map[string]any{"type": "message", "role": "assistant", "model": "gpt-5.6-sol", "content": []map[string]string{{"type": "text", "text": "OK"}}})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	credential.BaseURL = server.URL

	screen := &doctorScreenStub{lists: [][]screenpkg.Socket{{{PID: 1234, Name: "doctor"}}, nil}}
	paths := configpkg.Paths{ConfigFile: runtimePath, StateRoot: filepath.Join(dir, "state")}
	report := RunDoctorWithOptions(context.Background(), paths, DoctorOptions{
		Screen: screen, HTTPClient: rewriteDoctorClient(server.Client(), server.URL), SessionName: "doctor", PollInterval: time.Millisecond, PollTimeout: time.Second,
	})
	checks := doctorCheckMap(report)
	for _, name := range []string{"runtime configuration", "Claude Code", "GNU Screen", "GNU Screen lifecycle", "private proxy credential", "private managed settings", "CLIProxyAPI config", "CLIProxyAPI", "CLIProxyAPI service manager", "CLIProxyAPI service identity", "CLIProxyAPI service", "CLIProxyAPI models", "CLIProxyAPI messages"} {
		if check, ok := checks[name]; !ok || !check.OK {
			t.Fatalf("%s check = %+v; report=%+v", name, check, report)
		}
	}
	for _, name := range []string{"curl", "git", "Codex CLI"} {
		if _, ok := checks[name]; ok {
			t.Fatalf("managed doctor included setup-only dependency %q", name)
		}
	}
}

func TestDoctorSystemdIdentityFailureSkipsProxyEndpoints(t *testing.T) {
	dir := t.TempDir()
	binDir := filepath.Join(dir, "bin")
	if err := os.MkdirAll(binDir, 0o700); err != nil {
		t.Fatal(err)
	}
	claude := writeDoctorExecutable(t, binDir, "claude")
	screenPath := writeDoctorExecutable(t, binDir, "screen")
	proxy := writeDoctorExecutable(t, binDir, "cliproxyapi")
	otherProxy := writeDoctorExecutable(t, binDir, "other-proxy")
	systemctl := writeDoctorExecutable(t, binDir, "systemctl")
	busctl := writeDoctorExecutable(t, binDir, "busctl")
	for name, path := range map[string]*string{
		"systemctl": &systemctl,
		"busctl":    &busctl,
	} {
		resolved, err := filepath.EvalSymlinks(*path)
		if err != nil {
			t.Fatalf("resolve %s: %v", name, err)
		}
		*path = resolved
	}
	t.Setenv("PATH", binDir)

	credentialPath := filepath.Join(dir, "credential.json")
	managedSettingsPath := filepath.Join(dir, "managed-settings.json")
	proxyConfigPath := filepath.Join(dir, "proxy.yaml")
	runtimePath := filepath.Join(dir, "runtime.json")
	credential := configpkg.ProxyCredential{
		SchemaVersion: configpkg.ProxyCredentialSchema,
		BaseURL:       configpkg.ManagedProxyBaseURL,
		APIKey:        "sk-doctor-systemd-secret",
	}
	credentialData, err := configpkg.EncodeProxyCredential(credential)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(credentialPath, credentialData, 0o600); err != nil {
		t.Fatal(err)
	}
	settingsData, err := managedsettings.Encode(credential.BaseURL, credential.APIKey)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(managedSettingsPath, settingsData, 0o600); err != nil {
		t.Fatal(err)
	}
	proxyData, _, err := mutateProxyYAML(
		[]byte("host: 0.0.0.0\nport: 9000\napi-keys: []\noauth-model-alias: {codex: []}\n"),
		credential.APIKey,
	)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(proxyConfigPath, proxyData, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := configpkg.Save(runtimePath, configpkg.Runtime{
		SchemaVersion:      configpkg.SchemaVersion,
		RealClaude:         claude,
		ClaudexMode:        "managed_proxy",
		ScreenPath:         screenPath,
		CLIProxyExecutable: proxy,
		CLIProxyConfig:     proxyConfigPath,
		ProxyCredential:    credentialPath,
		ManagedSettings:    managedSettingsPath,
		CLIProxyService:    "systemd",
	}); err != nil {
		t.Fatal(err)
	}

	busctlCommand := []string{
		busctl,
		"--user",
		"--json=short",
		"get-property",
		"org.freedesktop.systemd1",
		cliProxySystemdServiceObjectPath,
		"org.freedesktop.systemd1.Service",
		"ExecStart",
	}
	runner := &recordingRunner{outputs: map[string][]byte{
		strings.Join(busctlCommand, "\x00"): systemdExecStartJSON(
			t,
			otherProxy,
			[]string{otherProxy, "--config", proxyConfigPath},
		),
		strings.Join([]string{systemctl, "--user", "is-active", "cliproxyapi.service"}, "\x00"):  []byte("active\n"),
		strings.Join([]string{systemctl, "--user", "is-enabled", "cliproxyapi.service"}, "\x00"): []byte("enabled\n"),
	}}
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		requests++
	}))
	defer server.Close()
	report := RunDoctorWithOptions(
		context.Background(),
		configpkg.Paths{ConfigFile: runtimePath, StateRoot: filepath.Join(dir, "state")},
		DoctorOptions{
			Screen:       &doctorScreenStub{lists: [][]screenpkg.Socket{{{PID: 1234, Name: "doctor"}}, nil}},
			Runner:       runner,
			HTTPClient:   rewriteDoctorClient(server.Client(), server.URL),
			SessionName:  "doctor",
			PollInterval: time.Millisecond,
			PollTimeout:  time.Second,
		},
	)
	checks := doctorCheckMap(report)
	identity := checks["CLIProxyAPI service identity"]
	if identity.OK || !strings.Contains(identity.Detail, "executable does not match") {
		t.Fatalf("identity check = %+v", identity)
	}
	if service := checks["CLIProxyAPI service"]; !service.OK {
		t.Fatalf("service check = %+v", service)
	}
	for _, name := range []string{"CLIProxyAPI models", "CLIProxyAPI messages"} {
		check := checks[name]
		if check.OK || check.Detail != "not run: managed proxy prerequisites failed" {
			t.Fatalf("%s check = %+v", name, check)
		}
	}
	if requests != 0 {
		t.Fatalf("proxy endpoint requests = %d", requests)
	}
	wantCommands := [][]string{
		busctlCommand,
		{systemctl, "--user", "is-active", "cliproxyapi.service"},
		{systemctl, "--user", "is-enabled", "cliproxyapi.service"},
	}
	if !reflect.DeepEqual(runner.outputCommands, wantCommands) {
		t.Fatalf("inspection commands = %q, want %q", runner.outputCommands, wantCommands)
	}
	if len(runner.commands) != 0 {
		t.Fatalf("mutating service commands = %q", runner.commands)
	}
}

func TestDoctorUserSystemdUsesCanonicalTrustedExecutable(t *testing.T) {
	dir := t.TempDir()
	binDir := filepath.Join(dir, "bin")
	if err := os.MkdirAll(binDir, 0o700); err != nil {
		t.Fatal(err)
	}
	systemctl := writeDoctorExecutable(t, binDir, "systemctl")
	systemctl, err := filepath.EvalSymlinks(systemctl)
	if err != nil {
		t.Fatal(err)
	}
	alternateDir := filepath.Join(dir, "alternate")
	if err := os.MkdirAll(alternateDir, 0o700); err != nil {
		t.Fatal(err)
	}
	alternate := writeDoctorExecutable(t, alternateDir, "systemctl")
	if err := os.Remove(alternate); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(alternate, []byte("replacement"), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", strings.Join([]string{binDir, alternateDir}, string(os.PathListSeparator)))

	runner := &recordingRunner{outputs: map[string][]byte{
		strings.Join([]string{systemctl, "--user", "is-active", "cliproxyapi.service"}, "\x00"):  []byte("active\n"),
		strings.Join([]string{systemctl, "--user", "is-enabled", "cliproxyapi.service"}, "\x00"): []byte("enabled\n"),
	}}
	check := checkDoctorService(
		context.Background(),
		configpkg.Runtime{},
		"systemd",
		runner,
	)
	if !check.OK || check.Detail != "configured systemd service is active and enabled or scheduled" {
		t.Fatalf("service check = %+v", check)
	}
	want := [][]string{
		{systemctl, "--user", "is-active", "cliproxyapi.service"},
		{systemctl, "--user", "is-enabled", "cliproxyapi.service"},
	}
	if !reflect.DeepEqual(runner.outputCommands, want) {
		t.Fatalf("service inspection commands = %q, want %q", runner.outputCommands, want)
	}
}

func TestDoctorServiceRequiresActiveAndScheduledState(t *testing.T) {
	dir := t.TempDir()
	binDir := filepath.Join(dir, "bin")
	if err := os.MkdirAll(binDir, 0o700); err != nil {
		t.Fatal(err)
	}
	systemctl := writeDoctorExecutable(t, binDir, "systemctl")
	systemctl, err := filepath.EvalSymlinks(systemctl)
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", binDir)

	for _, test := range []struct {
		name        string
		active      string
		enabled     string
		system      bool
		wantOK      bool
		wantDetail  string
		wantCommand [][]string
	}{
		{
			name:       "user transitional",
			active:     "activating\n",
			enabled:    "enabled\n",
			wantDetail: "configured systemd service is inactive",
			wantCommand: [][]string{
				{systemctl, "--user", "is-active", "cliproxyapi.service"},
				{systemctl, "--user", "is-enabled", "cliproxyapi.service"},
			},
		},
		{
			name:       "user active disabled",
			active:     "active\n",
			enabled:    "disabled\n",
			wantDetail: "configured systemd service is active but not enabled or scheduled",
			wantCommand: [][]string{
				{systemctl, "--user", "is-active", "cliproxyapi.service"},
				{systemctl, "--user", "is-enabled", "cliproxyapi.service"},
			},
		},
		{
			name:       "system active enabled",
			active:     "active\n",
			enabled:    "enabled\n",
			system:     true,
			wantOK:     true,
			wantDetail: "configured system systemd service is active and enabled or scheduled",
			wantCommand: [][]string{
				{systemctl, "is-active", "cliproxyapi.service"},
				{systemctl, "is-enabled", "cliproxyapi.service"},
			},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			activeArgs := []string{"--user", "is-active", "cliproxyapi.service"}
			enabledArgs := []string{"--user", "is-enabled", "cliproxyapi.service"}
			if test.system {
				activeArgs = []string{"is-active", "cliproxyapi.service"}
				enabledArgs = []string{"is-enabled", "cliproxyapi.service"}
			}
			runner := &recordingRunner{outputs: map[string][]byte{
				strings.Join(append([]string{systemctl}, activeArgs...), "\x00"):  []byte(test.active),
				strings.Join(append([]string{systemctl}, enabledArgs...), "\x00"): []byte(test.enabled),
			}}
			check := checkDoctorService(
				context.Background(),
				configpkg.Runtime{CLIProxySystemUnit: test.system},
				"systemd",
				runner,
			)
			if check.OK != test.wantOK || check.Detail != test.wantDetail {
				t.Fatalf("service check = %+v", check)
			}
			if !reflect.DeepEqual(runner.outputCommands, test.wantCommand) {
				t.Fatalf("service inspection commands = %q, want %q", runner.outputCommands, test.wantCommand)
			}
		})
	}
}

func TestDoctorManagedConfigMissingHostReturnsFailedCheck(t *testing.T) {
	dir := t.TempDir()
	credential := configpkg.ProxyCredential{
		SchemaVersion: configpkg.ProxyCredentialSchema,
		BaseURL:       configpkg.ManagedProxyBaseURL,
		APIKey:        "doctor-missing-host-secret",
	}
	credentialData, err := configpkg.EncodeProxyCredential(credential)
	if err != nil {
		t.Fatal(err)
	}
	credentialPath := filepath.Join(dir, "credential.json")
	if err := os.WriteFile(credentialPath, credentialData, 0o600); err != nil {
		t.Fatal(err)
	}
	settingsData, err := managedsettings.Encode(credential.BaseURL, credential.APIKey)
	if err != nil {
		t.Fatal(err)
	}
	settingsPath := filepath.Join(dir, "managed-settings.json")
	if err := os.WriteFile(settingsPath, settingsData, 0o600); err != nil {
		t.Fatal(err)
	}
	proxyConfigPath := filepath.Join(dir, "proxy.yaml")
	proxyData := []byte(`port: 8317
api-keys: [doctor-missing-host-secret]
oauth-model-alias:
  codex:
    - {name: gpt-5.6-sol, alias: claude-opus-4-8, fork: true}
    - {name: gpt-5.6-sol, alias: claude-fable-5, fork: true}
    - {name: gpt-5.6-terra, alias: claude-sonnet-5, fork: true}
    - {name: gpt-5.6-luna, alias: claude-haiku-4-5-20251001, fork: true}
`)
	if err := os.WriteFile(proxyConfigPath, proxyData, 0o600); err != nil {
		t.Fatal(err)
	}
	runtimePath := filepath.Join(dir, "runtime.json")
	if err := configpkg.Save(runtimePath, configpkg.Runtime{
		SchemaVersion:      configpkg.SchemaVersion,
		RealClaude:         writeDoctorExecutable(t, dir, "claude"),
		ClaudexMode:        "managed_proxy",
		ScreenPath:         writeDoctorExecutable(t, dir, "screen"),
		CLIProxyExecutable: writeDoctorExecutable(t, dir, "proxy"),
		CLIProxyConfig:     proxyConfigPath,
		ProxyCredential:    credentialPath,
		ManagedSettings:    settingsPath,
		CLIProxyService:    "none",
	}); err != nil {
		t.Fatal(err)
	}
	report := RunDoctorWithOptions(
		context.Background(),
		configpkg.Paths{
			ConfigFile: runtimePath,
			StateRoot:  filepath.Join(dir, "state"),
		},
		DoctorOptions{
			Screen: &doctorScreenStub{
				lists: [][]screenpkg.Socket{{{PID: 1234, Name: "doctor"}}, nil},
			},
			SessionName: "doctor",
		},
	)
	checks := doctorCheckMap(report)
	configCheck := checks["CLIProxyAPI config"]
	if configCheck.OK || !strings.Contains(configCheck.Detail, "host is invalid") {
		t.Fatalf("config check = %+v", configCheck)
	}
	for _, name := range []string{"CLIProxyAPI models", "CLIProxyAPI messages"} {
		if check := checks[name]; check.OK || !strings.HasPrefix(check.Detail, "not run:") {
			t.Fatalf("%s check = %+v", name, check)
		}
	}
}

func TestDoctorManagedPrerequisiteFailureReportsNotRunChecks(t *testing.T) {
	dir := t.TempDir()
	runtimePath := filepath.Join(dir, "runtime.json")
	if err := configpkg.Save(runtimePath, configpkg.Runtime{
		SchemaVersion:      configpkg.SchemaVersion,
		RealClaude:         writeDoctorExecutable(t, dir, "claude"),
		ClaudexMode:        "managed_proxy",
		ScreenPath:         writeDoctorExecutable(t, dir, "screen"),
		CLIProxyExecutable: writeDoctorExecutable(t, dir, "proxy"),
		CLIProxyConfig:     filepath.Join(dir, "missing.yaml"),
		ProxyCredential:    filepath.Join(dir, "missing-credential.json"),
		ManagedSettings:    filepath.Join(dir, "missing-settings.json"),
		CLIProxyService:    "none",
	}); err != nil {
		t.Fatal(err)
	}
	paths := configpkg.Paths{ConfigFile: runtimePath, StateRoot: filepath.Join(dir, "state")}
	report := RunDoctorWithOptions(context.Background(), paths, DoctorOptions{Screen: &doctorScreenStub{lists: [][]screenpkg.Socket{{{PID: 1234, Name: "doctor"}}, nil}}, SessionName: "doctor"})
	checks := doctorCheckMap(report)
	if checks["private proxy credential"].OK {
		t.Fatalf("credential check = %+v", checks["private proxy credential"])
	}
	for _, name := range []string{"private managed settings", "CLIProxyAPI config", "CLIProxyAPI models", "CLIProxyAPI messages"} {
		if check := checks[name]; check.OK || !strings.HasPrefix(check.Detail, "not run:") {
			t.Fatalf("%s check = %+v", name, check)
		}
	}
}

func writeDoctorExecutable(t *testing.T, dir, name string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, nil, 0o700); err != nil {
		t.Fatal(err)
	}
	return path
}

func doctorCheckMap(report DoctorReport) map[string]Check {
	checks := make(map[string]Check, len(report.Checks))
	for _, check := range report.Checks {
		checks[check.Name] = check
	}
	return checks
}

type doctorRoundTripper struct {
	base http.RoundTripper
	url  string
}

func (r doctorRoundTripper) RoundTrip(request *http.Request) (*http.Response, error) {
	clone := request.Clone(request.Context())
	serverURL, err := http.NewRequest(http.MethodGet, r.url, nil)
	if err != nil {
		return nil, err
	}
	clone.URL.Scheme = serverURL.URL.Scheme
	clone.URL.Host = serverURL.URL.Host
	return r.base.RoundTrip(clone)
}

func rewriteDoctorClient(client *http.Client, serverURL string) *http.Client {
	clone := *client
	clone.Transport = doctorRoundTripper{base: client.Transport, url: serverURL}
	return &clone
}

func TestDiscoverClaudeExecutableFindsUserLocalInstallOutsidePATH(t *testing.T) {
	home := t.TempDir()
	localBin := filepath.Join(home, ".local", "bin")
	if err := os.MkdirAll(localBin, 0o700); err != nil {
		t.Fatal(err)
	}
	releaseDir := filepath.Join(home, ".local", "share", "claude", "versions")
	if err := os.MkdirAll(releaseDir, 0o700); err != nil {
		t.Fatal(err)
	}
	release := filepath.Join(releaseDir, "v1.0.0")
	if err := os.WriteFile(release, nil, 0o700); err != nil {
		t.Fatal(err)
	}
	claude := filepath.Join(localBin, "claude")
	if err := os.Symlink(release, claude); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", home)
	t.Setenv("PATH", t.TempDir())

	resolved, err := discoverClaudeExecutable()
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Clean(claude)
	if resolved != want {
		t.Fatalf("resolved Claude Code = %q, want stable path %q", resolved, want)
	}
}

func TestRunWorkflowFindsUserLocalClaudeOutsidePATH(t *testing.T) {
	home := t.TempDir()
	localBin := filepath.Join(home, ".local", "bin")
	pathBin := filepath.Join(home, "path-bin")
	if err := os.MkdirAll(localBin, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(pathBin, 0o700); err != nil {
		t.Fatal(err)
	}
	claude := filepath.Join(localBin, "claude")
	claudex := filepath.Join(pathBin, "claudex")
	paths := []string{claude, claudex}
	for _, command := range []string{"curl", "git", "screen", "codex"} {
		paths = append(paths, filepath.Join(pathBin, command))
	}
	for _, path := range paths {
		if err := os.WriteFile(path, nil, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, "config"))
	t.Setenv("PATH", pathBin)

	result, err := RunWorkflow(context.Background(), SetupOptions{
		Yes:            true,
		SkipCodex:      true,
		SkipProxy:      true,
		NonInteractive: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	wantClaude := filepath.Clean(claude)
	if result.Config.RealClaude != wantClaude {
		t.Fatalf("real Claude Code = %q, want stable path %q", result.Config.RealClaude, wantClaude)
	}
	if !result.ExternalClaudex || result.Config.RealClaudex != claudex {
		t.Fatalf("unexpected external claudex config: %+v", result.Config)
	}
}

func TestRunWorkflowExternalClaudexDoesNotSkipCodexByDefault(t *testing.T) {
	dir := t.TempDir()
	for _, command := range []string{"curl", "git", "screen", "claude", "claudex"} {
		if err := os.WriteFile(filepath.Join(dir, command), nil, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("HOME", t.TempDir())
	t.Setenv("PATH", dir)

	var output bytes.Buffer
	result, err := RunWorkflow(context.Background(), SetupOptions{
		DryRun:         true,
		NonInteractive: true,
		Output:         &output,
		ErrorOutput:    io.Discard,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !result.ExternalClaudex {
		t.Fatal("external claudex was not selected")
	}
	if !strings.Contains(output.String(), CodexManualInstallCommand) ||
		!strings.Contains(output.String(), "did not download or execute") {
		t.Fatalf("missing Codex manual guidance in %q", output.String())
	}
	if strings.Contains(output.String(), "<downloaded ") {
		t.Fatalf("dry run claimed remote installer execution: %q", output.String())
	}

	output.Reset()
	_, err = RunWorkflow(context.Background(), SetupOptions{
		DryRun:         true,
		NonInteractive: true,
		SkipCodex:      true,
		Output:         &output,
		ErrorOutput:    io.Discard,
	})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(output.String(), CodexManualInstallCommand) {
		t.Fatalf("explicit SkipCodex did not suppress guidance: %q", output.String())
	}
}

func TestFindExternalClaudexRejectsSelfAlias(t *testing.T) {
	dir := t.TempDir()
	self := filepath.Join(dir, "sclaude")
	alias := filepath.Join(dir, "claudex")
	if err := os.WriteFile(self, nil, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(self, alias); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir)

	if path, ok := FindExternalClaudex(self); ok || path != "" {
		t.Fatalf("self alias discovered as external: path=%q ok=%v", path, ok)
	}
}

func TestFindExternalClaudexContinuesPastSelfAlias(t *testing.T) {
	firstDir := t.TempDir()
	secondDir := t.TempDir()
	self := filepath.Join(firstDir, "sclaude")
	alias := filepath.Join(firstDir, "claudex")
	external := filepath.Join(secondDir, "claudex")
	for _, path := range []string{self, external} {
		if err := os.WriteFile(path, nil, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Symlink(self, alias); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", strings.Join([]string{firstDir, secondDir}, string(os.PathListSeparator)))

	path, ok := FindExternalClaudex(self)
	if !ok {
		t.Fatal("external claudex after self alias was not discovered")
	}
	if path != external {
		t.Fatalf("external claudex = %q, want %q", path, external)
	}
}

func TestFindExternalClaudexPreservesStableAlias(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "claudex-v2")
	alias := filepath.Join(dir, "claudex")
	if err := os.WriteFile(target, nil, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, alias); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir)

	path, ok := FindExternalClaudex()
	if !ok || path != alias {
		t.Fatalf("external claudex = %q, %v; want stable alias %q", path, ok, alias)
	}
}

func TestFindExternalClaudexRejectsHardLinkedSelf(t *testing.T) {
	dir := t.TempDir()
	self := filepath.Join(dir, "sclaude")
	alias := filepath.Join(dir, "claudex")
	if err := os.WriteFile(self, nil, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Link(self, alias); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir)

	if path, ok := FindExternalClaudex(self); ok || path != "" {
		t.Fatalf("hard-linked self discovered as external: path=%q ok=%v", path, ok)
	}
}

func TestFindExternalClaudexAcceptsDistinctExecutableBesideSelf(t *testing.T) {
	dir := t.TempDir()
	self := filepath.Join(dir, "sclaude")
	external := filepath.Join(dir, "claudex")
	for _, path := range []string{self, external} {
		if err := os.WriteFile(path, nil, 0o100); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("PATH", dir)

	path, ok := FindExternalClaudex(self)
	if !ok {
		t.Fatal("distinct colocated claudex was rejected")
	}
	if path != external {
		t.Fatalf("external claudex = %q, want %q", path, external)
	}
}

func TestRunWorkflowSkipsLedgerOwnedClaudexAndFindsExternal(t *testing.T) {
	home := t.TempDir()
	ownedBin := filepath.Join(home, "owned-bin")
	externalBin := filepath.Join(home, "external-bin")
	dataDir := filepath.Join(home, "data")
	stateDir := filepath.Join(home, "state")
	for _, directory := range []string{ownedBin, externalBin} {
		if err := os.MkdirAll(directory, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	oldRelease := filepath.Join(dataDir, "sclaude", "releases", "v1", "sclaude")
	if err := os.MkdirAll(filepath.Dir(oldRelease), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(oldRelease, nil, 0o700); err != nil {
		t.Fatal(err)
	}
	ownedSclaudex := filepath.Join(ownedBin, "sclaudex")
	if err := os.Symlink(oldRelease, ownedSclaudex); err != nil {
		t.Fatal(err)
	}
	ownedClaudexAlias := filepath.Join(ownedBin, "claudex")
	if err := os.Symlink(ownedSclaudex, ownedClaudexAlias); err != nil {
		t.Fatal(err)
	}
	digest, err := digestPath(ownedSclaudex)
	if err != nil {
		t.Fatal(err)
	}
	ledgerPath := filepath.Join(stateDir, "sclaude", "install", "ledger.json")
	if err := writeJSONPrivate(ledgerPath, InstallLedger{
		SchemaVersion: 1,
		Current:       "v1",
		BinDir:        ownedBin,
		Files:         map[string]string{ownedSclaudex: digest},
	}); err != nil {
		t.Fatal(err)
	}
	external := filepath.Join(externalBin, "claudex")
	if err := os.WriteFile(external, nil, 0o700); err != nil {
		t.Fatal(err)
	}
	for _, command := range []string{"claude", "screen", "curl", "git", "codex"} {
		if err := os.WriteFile(filepath.Join(externalBin, command), nil, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, "config"))
	t.Setenv("XDG_DATA_HOME", dataDir)
	t.Setenv("XDG_STATE_HOME", stateDir)
	t.Setenv("PATH", strings.Join([]string{ownedBin, externalBin}, string(os.PathListSeparator)))

	result, err := RunWorkflow(context.Background(), SetupOptions{
		Yes:            true,
		SkipCodex:      true,
		SkipProxy:      true,
		NonInteractive: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !result.ExternalClaudex || result.Config.RealClaudex != external {
		t.Fatalf("unexpected external claudex config: %+v", result.Config)
	}
}

func TestRunWorkflowIgnoresModifiedLedgerClaudex(t *testing.T) {
	home := t.TempDir()
	binDir := filepath.Join(home, "bin")
	if err := os.MkdirAll(binDir, 0o700); err != nil {
		t.Fatal(err)
	}
	claudex := filepath.Join(binDir, "claudex")
	if err := os.WriteFile(claudex, nil, 0o700); err != nil {
		t.Fatal(err)
	}
	stateDir := filepath.Join(home, "state")
	ledgerPath := filepath.Join(stateDir, "sclaude", "install", "ledger.json")
	if err := writeJSONPrivate(ledgerPath, InstallLedger{
		SchemaVersion: 1,
		Current:       "v1",
		BinDir:        binDir,
		Files:         map[string]string{claudex: "stale-digest"},
	}); err != nil {
		t.Fatal(err)
	}
	for _, command := range []string{"claude", "screen", "curl", "git", "codex"} {
		if err := os.WriteFile(filepath.Join(binDir, command), nil, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, "config"))
	t.Setenv("XDG_STATE_HOME", stateDir)
	t.Setenv("PATH", binDir)

	result, err := RunWorkflow(context.Background(), SetupOptions{
		Yes:            true,
		SkipCodex:      true,
		SkipProxy:      true,
		NonInteractive: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !result.ExternalClaudex || result.Config.RealClaudex != claudex {
		t.Fatalf("modified ledger path was trusted as project-owned: %+v", result.Config)
	}
}

func TestRunWorkflowExplicitManagedProxyOutranksExternalClaudex(t *testing.T) {
	home := t.TempDir()
	binDir := filepath.Join(home, "bin")
	if err := os.MkdirAll(binDir, 0o700); err != nil {
		t.Fatal(err)
	}
	proxy := filepath.Join(binDir, "cli-proxy-api")
	proxyConfig := filepath.Join(home, "config.yaml")
	for _, path := range []string{
		proxy,
		filepath.Join(binDir, "claude"),
		filepath.Join(binDir, "claudex"),
		filepath.Join(binDir, "screen"),
		filepath.Join(binDir, "curl"),
		filepath.Join(binDir, "git"),
		filepath.Join(binDir, "codex"),
	} {
		if err := os.WriteFile(path, nil, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(proxyConfig, []byte("host: \"\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, "sclaude-config"))
	t.Setenv("PATH", binDir)

	result, err := runWorkflow(context.Background(), SetupOptions{
		NonInteractive:       true,
		SkipCodex:            true,
		CLIProxyExecutable:   proxy,
		CLIProxyConfig:       proxyConfig,
		CLIProxyService:      "none",
		ManagedProxyExplicit: true,
		Input:                strings.NewReader(""),
		Output:               io.Discard,
		ErrorOutput:          io.Discard,
	}, &recordingRunner{})
	if err != nil {
		t.Fatal(err)
	}
	if result.ExternalClaudex || result.Config.ClaudexMode != "managed_proxy" {
		t.Fatalf("explicit managed proxy was ignored: %+v", result)
	}
	resolvedProxy, err := filepath.EvalSymlinks(proxy)
	if err != nil {
		t.Fatal(err)
	}
	if result.Config.CLIProxyExecutable != resolvedProxy || result.Config.CLIProxyConfig != proxyConfig {
		t.Fatalf("managed proxy config = %+v", result.Config)
	}
	if result.Proxy.BackupPath == "" {
		t.Fatal("managed workflow did not retain the original proxy config")
	}
	backup, err := os.ReadFile(result.Proxy.BackupPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(backup) != "host: \"\"\n" {
		t.Fatalf("retained proxy backup = %q", backup)
	}
	backupInfo, err := os.Lstat(result.Proxy.BackupPath)
	if err != nil {
		t.Fatal(err)
	}
	if !backupInfo.Mode().IsRegular() || backupInfo.Mode().Perm() != 0o600 {
		t.Fatalf("retained proxy backup mode = %v", backupInfo.Mode())
	}
}

func TestRunWorkflowRollsBackRetainedProxyBackupOnLaterFailure(t *testing.T) {
	home := t.TempDir()
	binDir := filepath.Join(home, "bin")
	if err := os.MkdirAll(binDir, 0o700); err != nil {
		t.Fatal(err)
	}
	proxy := filepath.Join(binDir, "cli-proxy-api")
	proxyConfig := filepath.Join(home, "config.yaml")
	for _, path := range []string{proxy, filepath.Join(binDir, "claude"), filepath.Join(binDir, "screen"), filepath.Join(binDir, "systemctl")} {
		if err := os.WriteFile(path, nil, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	original := "host: \"\"\n"
	if err := os.WriteFile(proxyConfig, []byte(original), 0o640); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, "config"))
	t.Setenv("XDG_STATE_HOME", filepath.Join(home, "state"))
	t.Setenv("XDG_DATA_HOME", filepath.Join(home, "data"))
	t.Setenv("PATH", binDir)

	primary := errors.New("injected service failure")
	_, err := runWorkflow(context.Background(), SetupOptions{
		NonInteractive:     true,
		SkipCodex:          true,
		CLIProxyExecutable: proxy,
		CLIProxyConfig:     proxyConfig,
		CLIProxyService:    "systemd",
	}, failingRunner{err: primary})
	if !errors.Is(err, primary) {
		t.Fatalf("workflow error = %v", err)
	}
	assertFileState(t, proxyConfig, original, 0o640)
	matches, globErr := filepath.Glob(proxyConfig + ".sclaude-backup-*")
	if globErr != nil {
		t.Fatal(globErr)
	}
	if len(matches) != 0 {
		t.Fatalf("retained proxy backup survived rollback: %q", matches)
	}
	paths, pathErr := configpkg.DefaultPaths()
	if pathErr != nil {
		t.Fatal(pathErr)
	}
	for _, path := range []string{paths.Credential, paths.ManagedSettings, paths.ConfigFile} {
		if _, statErr := os.Lstat(path); !errors.Is(statErr, os.ErrNotExist) {
			t.Fatalf("setup-owned file survived rollback at %s: %v", path, statErr)
		}
	}
}

func TestRunWorkflowRollsBackManagedApplyStages(t *testing.T) {
	for _, stage := range []string{
		setupStageRetainedBackup,
		setupStageProxyConfig,
		setupStageProxyCredential,
		setupStageManagedSettings,
	} {
		t.Run(stage, func(t *testing.T) {
			fixture := setupManagedWorkflowFixture(t)
			primary := errors.New("injected " + stage + " failure")
			afterSetupWorkflowStage = func(completed string) error {
				if completed == stage {
					return primary
				}
				return nil
			}
			t.Cleanup(func() { afterSetupWorkflowStage = nil })

			_, err := runWorkflow(context.Background(), fixture.options, &stageRunner{})
			if !errors.Is(err, primary) {
				t.Fatalf("workflow error = %v", err)
			}
			assertManagedWorkflowRollback(t, fixture)
		})
	}
}

func TestRunWorkflowSerializesConcurrentSetupTransactions(t *testing.T) {
	fixture := setupManagedWorkflowFixture(t)
	reachedStage := make(chan struct{})
	releaseStage := make(chan struct{})
	var pauseFirst sync.Once
	afterSetupWorkflowStage = func(stage string) error {
		if stage == setupStageProxyConfig {
			pauseFirst.Do(func() {
				close(reachedStage)
				<-releaseStage
			})
		}
		return nil
	}
	t.Cleanup(func() { afterSetupWorkflowStage = nil })

	type workflowOutcome struct {
		result WorkflowResult
		err    error
	}
	firstDone := make(chan workflowOutcome, 1)
	go func() {
		result, err := runWorkflow(context.Background(), fixture.options, &stageRunner{})
		firstDone <- workflowOutcome{result: result, err: err}
	}()
	select {
	case <-reachedStage:
	case <-time.After(5 * time.Second):
		t.Fatal("first workflow did not reach the proxy-config stage")
	}

	paths, err := configpkg.DefaultPaths()
	if err != nil {
		t.Fatal(err)
	}
	firstJournal, err := loadSetupJournal(setupJournalPath(paths.StateRoot))
	if err != nil {
		t.Fatal(err)
	}
	secondDone := make(chan workflowOutcome, 1)
	go func() {
		result, err := runWorkflow(context.Background(), fixture.options, &stageRunner{})
		secondDone <- workflowOutcome{result: result, err: err}
	}()
	select {
	case outcome := <-secondDone:
		t.Fatalf("second workflow completed while the first held the setup lock: %v", outcome.err)
	case <-time.After(150 * time.Millisecond):
	}
	stillLive, err := loadSetupJournal(setupJournalPath(paths.StateRoot))
	if err != nil {
		t.Fatal(err)
	}
	if stillLive.Token != firstJournal.Token {
		t.Fatalf("live setup journal was replaced: got token %q, want %q", stillLive.Token, firstJournal.Token)
	}
	assertFileState(t, fixture.proxyConfig, string(firstJournalEntryAfter(t, stillLive, fixture.proxyConfig)), 0o600)

	close(releaseStage)
	for name, done := range map[string]<-chan workflowOutcome{"first": firstDone, "second": secondDone} {
		select {
		case outcome := <-done:
			if outcome.err != nil {
				t.Fatalf("%s workflow error = %v", name, outcome.err)
			}
		case <-time.After(5 * time.Second):
			t.Fatalf("%s workflow did not complete", name)
		}
	}
	if _, err := os.Lstat(setupJournalPath(paths.StateRoot)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("setup journal survived serialized workflows: %v", err)
	}
}

func firstJournalEntryAfter(t *testing.T, journal setupJournal, path string) []byte {
	t.Helper()
	for _, entry := range journal.Entries {
		if entry.Path == path {
			data, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			if hexDigest(data) != entry.AfterDigest {
				t.Fatalf("setup target %s does not match the live journal", path)
			}
			return data
		}
	}
	t.Fatalf("setup target %s is absent from the live journal", path)
	return nil
}

func TestRunWorkflowRestoresServiceAfterCompletedStageFailures(t *testing.T) {
	for _, stage := range []string{setupStageServiceEnable, setupStageServiceAction} {
		t.Run(stage, func(t *testing.T) {
			fixture := setupManagedWorkflowFixture(t)
			fixture.options.CLIProxyService = "systemd"
			primary := errors.New("injected " + stage + " failure")
			afterSetupWorkflowStage = func(completed string) error {
				if completed == stage {
					return primary
				}
				return nil
			}
			t.Cleanup(func() { afterSetupWorkflowStage = nil })

			runner := &stageRunner{}
			_, err := runWorkflow(context.Background(), fixture.options, runner)
			if !errors.Is(err, primary) {
				t.Fatalf("workflow error = %v", err)
			}
			assertManagedWorkflowRollback(t, fixture)

			want := [][]string{
				{fixture.systemctl, "--user", "enable", "cliproxyapi.service"},
			}
			if stage == setupStageServiceEnable {
				want = append(want,
					[]string{fixture.systemctl, "--user", "disable", "cliproxyapi.service"},
				)
			} else {
				want = append(want,
					[]string{fixture.systemctl, "--user", "restart", "cliproxyapi.service"},
					[]string{fixture.systemctl, "--user", "stop", "cliproxyapi.service"},
					[]string{fixture.systemctl, "--user", "disable", "cliproxyapi.service"},
				)
			}
			if !reflect.DeepEqual(runner.commands, want) {
				t.Fatalf("commands = %q, want %q", runner.commands, want)
			}
		})
	}
}

func TestRunWorkflowRestoresServiceWhenActionFailsAfterEnable(t *testing.T) {
	fixture := setupManagedWorkflowFixture(t)
	fixture.options.CLIProxyService = "systemd"
	primary := errors.New("service action failed")
	runner := &stageRunner{runErrs: map[string]error{
		fixture.systemctl + " --user restart cliproxyapi.service": primary,
	}}

	_, err := runWorkflow(context.Background(), fixture.options, runner)
	if !errors.Is(err, primary) {
		t.Fatalf("workflow error = %v", err)
	}
	assertManagedWorkflowRollback(t, fixture)
	want := [][]string{
		{fixture.systemctl, "--user", "enable", "cliproxyapi.service"},
		{fixture.systemctl, "--user", "restart", "cliproxyapi.service"},
		{fixture.systemctl, "--user", "stop", "cliproxyapi.service"},
		{fixture.systemctl, "--user", "disable", "cliproxyapi.service"},
	}
	if !reflect.DeepEqual(runner.commands, want) {
		t.Fatalf("commands = %q, want %q", runner.commands, want)
	}
}

func TestRunWorkflowRejectsServiceExecutableReplacementBeforeAction(t *testing.T) {
	fixture := setupManagedWorkflowFixture(t)
	fixture.options.CLIProxyService = "systemd"
	afterSetupWorkflowStage = func(stage string) error {
		if stage != setupStageServiceEnable {
			return nil
		}
		replacement := fixture.systemctl + ".replacement"
		if err := os.WriteFile(replacement, []byte("#!/bin/sh\nexit 1\n"), 0o700); err != nil {
			return err
		}
		return os.Rename(replacement, fixture.systemctl)
	}
	t.Cleanup(func() { afterSetupWorkflowStage = nil })
	runner := &stageRunner{}

	_, err := runWorkflow(context.Background(), fixture.options, runner)
	if err == nil || !strings.Contains(err.Error(), "trusted executable changed") {
		t.Fatalf("workflow error = %v", err)
	}
	want := [][]string{
		{fixture.systemctl, "--user", "enable", "cliproxyapi.service"},
	}
	if !reflect.DeepEqual(runner.commands, want) {
		t.Fatalf("commands = %q, want only the pre-replacement enable command %q", runner.commands, want)
	}
}

func TestRunWorkflowRejectsProxyReplacementBeforeOAuth(t *testing.T) {
	fixture := setupManagedWorkflowFixture(t)
	fixture.options.Yes = true
	fixture.options.NonInteractive = false
	afterSetupWorkflowStage = func(stage string) error {
		if stage != setupStageOAuthReady {
			return nil
		}
		replacement := fixture.proxy + ".replacement"
		if err := os.WriteFile(replacement, []byte("#!/bin/sh\nexit 1\n"), 0o700); err != nil {
			return err
		}
		if err := os.Rename(replacement, fixture.proxy); err != nil {
			return err
		}
		return nil
	}
	t.Cleanup(func() { afterSetupWorkflowStage = nil })
	runner := &stageRunner{}

	_, err := runWorkflow(context.Background(), fixture.options, runner)
	if err == nil || !strings.Contains(err.Error(), "trusted executable changed") {
		t.Fatalf("workflow error = %v", err)
	}
	assertManagedWorkflowRollback(t, fixture)
	for _, command := range runner.commands {
		if len(command) > 0 && command[0] == fixture.proxy {
			t.Fatalf("replacement executable was invoked: %q", runner.commands)
		}
	}
}

func TestRunWorkflowRestoresServiceWhenOAuthFails(t *testing.T) {
	fixture := setupManagedWorkflowFixture(t)
	fixture.options.CLIProxyService = "systemd"
	fixture.options.Yes = true
	fixture.options.NonInteractive = false
	primary := errors.New("OAuth failed")
	resolvedProxy, err := ValidateCLIProxyExecutable(fixture.proxy)
	if err != nil {
		t.Fatal(err)
	}
	runner := &stageRunner{runErrs: map[string]error{
		strings.Join(OAuthCommand(resolvedProxy, fixture.proxyConfig, false), " "): primary,
	}}

	_, err = runWorkflow(context.Background(), fixture.options, runner)
	if !errors.Is(err, primary) || !strings.Contains(err.Error(), "cannot be rolled back automatically") {
		t.Fatalf("workflow error = %v", err)
	}
	assertManagedWorkflowRollback(t, fixture)
	want := [][]string{
		{fixture.systemctl, "--user", "enable", "cliproxyapi.service"},
		{fixture.systemctl, "--user", "restart", "cliproxyapi.service"},
		OAuthCommand(resolvedProxy, fixture.proxyConfig, false),
		{fixture.systemctl, "--user", "stop", "cliproxyapi.service"},
		{fixture.systemctl, "--user", "disable", "cliproxyapi.service"},
	}
	if !reflect.DeepEqual(runner.commands, want) {
		t.Fatalf("commands = %q, want %q", runner.commands, want)
	}
}

func TestRunWorkflowRollsBackWhenOAuthFails(t *testing.T) {
	fixture := setupManagedWorkflowFixture(t)
	fixture.options.Yes = true
	fixture.options.NonInteractive = false
	primary := errors.New("OAuth failed")
	resolvedProxy, err := ValidateCLIProxyExecutable(fixture.proxy)
	if err != nil {
		t.Fatal(err)
	}
	runner := &stageRunner{runErrs: map[string]error{
		strings.Join(OAuthCommand(resolvedProxy, fixture.proxyConfig, false), " "): primary,
	}}

	_, err = runWorkflow(context.Background(), fixture.options, runner)
	if !errors.Is(err, primary) || !strings.Contains(err.Error(), "cannot be rolled back automatically") {
		t.Fatalf("workflow error = %v", err)
	}
	assertManagedWorkflowRollback(t, fixture)
}

func TestRunWorkflowReportsOAuthAfterVerificationFailure(t *testing.T) {
	fixture := setupManagedWorkflowFixture(t)
	fixture.options.Yes = true
	fixture.options.NonInteractive = false
	primary := errors.New("verification failed")
	verifySetupProxyModels = func(context.Context, string) ([]string, error) {
		return nil, primary
	}
	t.Cleanup(func() { verifySetupProxyModels = WaitForProxyModels })

	_, err := runWorkflow(context.Background(), fixture.options, &stageRunner{})
	if !errors.Is(err, primary) || !strings.Contains(err.Error(), "cannot be rolled back automatically") {
		t.Fatalf("workflow error = %v", err)
	}
	assertManagedWorkflowRollback(t, fixture)
}

func TestRunWorkflowRestoresServiceAfterVerificationFailure(t *testing.T) {
	fixture := setupManagedWorkflowFixture(t)
	fixture.options.CLIProxyService = "systemd"
	fixture.options.Yes = true
	fixture.options.NonInteractive = false
	primary := errors.New("verification failed")
	verifySetupProxyModels = func(context.Context, string) ([]string, error) {
		return nil, primary
	}
	t.Cleanup(func() { verifySetupProxyModels = WaitForProxyModels })
	resolvedProxy, err := ValidateCLIProxyExecutable(fixture.proxy)
	if err != nil {
		t.Fatal(err)
	}
	runner := &stageRunner{}

	_, err = runWorkflow(context.Background(), fixture.options, runner)
	if !errors.Is(err, primary) || !strings.Contains(err.Error(), "cannot be rolled back automatically") {
		t.Fatalf("workflow error = %v", err)
	}
	assertManagedWorkflowRollback(t, fixture)
	want := [][]string{
		{fixture.systemctl, "--user", "enable", "cliproxyapi.service"},
		{fixture.systemctl, "--user", "restart", "cliproxyapi.service"},
		OAuthCommand(resolvedProxy, fixture.proxyConfig, false),
		{fixture.systemctl, "--user", "stop", "cliproxyapi.service"},
		{fixture.systemctl, "--user", "disable", "cliproxyapi.service"},
	}
	if !reflect.DeepEqual(runner.commands, want) {
		t.Fatalf("commands = %q, want %q", runner.commands, want)
	}
}

func TestRunWorkflowRollsBackAfterVerificationShellAndRuntimeStages(t *testing.T) {
	for _, stage := range []string{
		setupStageVerification,
		setupStageShellOwnership,
		setupStageRuntime,
	} {
		t.Run(stage, func(t *testing.T) {
			fixture := setupManagedWorkflowFixture(t)
			fixture.options.Yes = true
			fixture.options.NonInteractive = false
			if stage == setupStageShellOwnership {
				fixture.options.BinDir = filepath.Join(fixture.home, "managed-bin")
			}
			verifySetupProxyModels = func(context.Context, string) ([]string, error) {
				return []string{"model"}, nil
			}
			t.Cleanup(func() { verifySetupProxyModels = WaitForProxyModels })
			primary := errors.New("injected " + stage + " failure")
			afterSetupWorkflowStage = func(completed string) error {
				if completed == stage {
					return primary
				}
				return nil
			}
			t.Cleanup(func() { afterSetupWorkflowStage = nil })

			_, err := runWorkflow(context.Background(), fixture.options, &stageRunner{})
			if !errors.Is(err, primary) || !strings.Contains(err.Error(), "cannot be rolled back automatically") {
				t.Fatalf("workflow error = %v", err)
			}
			assertManagedWorkflowRollback(t, fixture)
			if stage == setupStageShellOwnership {
				assertFileState(t, fixture.profile, fixture.originalProfile, 0o640)
			}
		})
	}
}

type managedWorkflowFixture struct {
	home            string
	proxy           string
	systemctl       string
	proxyConfig     string
	profile         string
	originalProxy   string
	originalProfile string
	options         SetupOptions
}

func setupManagedWorkflowFixture(t *testing.T) managedWorkflowFixture {
	t.Helper()
	home := t.TempDir()
	binDir := filepath.Join(home, "bin")
	if err := os.MkdirAll(binDir, 0o700); err != nil {
		t.Fatal(err)
	}
	proxy := filepath.Join(binDir, "cli-proxy-api")
	systemctl := filepath.Join(binDir, "systemctl")
	for _, path := range []string{
		proxy,
		systemctl,
		filepath.Join(binDir, "claude"),
		filepath.Join(binDir, "screen"),
	} {
		if err := os.WriteFile(path, nil, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	systemctl, err := filepath.EvalSymlinks(systemctl)
	if err != nil {
		t.Fatal(err)
	}
	proxyConfig := filepath.Join(home, "config.yaml")
	originalProxy := "host: \"\"\n"
	if err := os.WriteFile(proxyConfig, []byte(originalProxy), 0o640); err != nil {
		t.Fatal(err)
	}
	profile := filepath.Join(home, ".zshrc")
	originalProfile := "export OTHER=1\n"
	if err := os.WriteFile(profile, []byte(originalProfile), 0o640); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, "config"))
	t.Setenv("XDG_STATE_HOME", filepath.Join(home, "state"))
	t.Setenv("XDG_DATA_HOME", filepath.Join(home, "data"))
	t.Setenv("SHELL", "/bin/zsh")
	t.Setenv("PATH", binDir)
	return managedWorkflowFixture{
		home:            home,
		proxy:           proxy,
		systemctl:       systemctl,
		proxyConfig:     proxyConfig,
		profile:         profile,
		originalProxy:   originalProxy,
		originalProfile: originalProfile,
		options: SetupOptions{
			NonInteractive:     true,
			SkipCodex:          true,
			CLIProxyExecutable: proxy,
			CLIProxyConfig:     proxyConfig,
			CLIProxyService:    "none",
			Input:              strings.NewReader(""),
			Output:             io.Discard,
			ErrorOutput:        io.Discard,
		},
	}
}

func assertManagedWorkflowRollback(t *testing.T, fixture managedWorkflowFixture) {
	t.Helper()
	assertFileState(t, fixture.proxyConfig, fixture.originalProxy, 0o640)
	matches, err := filepath.Glob(fixture.proxyConfig + ".sclaude-backup-*")
	if err != nil {
		t.Fatal(err)
	}
	if len(matches) != 0 {
		t.Fatalf("retained proxy backup survived rollback: %q", matches)
	}
	paths, err := configpkg.DefaultPaths()
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{paths.Credential, paths.ManagedSettings, paths.ConfigFile} {
		if _, err := os.Lstat(path); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("setup-owned file survived rollback at %s: %v", path, err)
		}
	}
	if _, err := os.Lstat(setupJournalPath(paths.StateRoot)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("setup journal survived rollback: %v", err)
	}
}

func TestRunWorkflowSystemServiceReturnsAdministratorCommandsWithoutExecutingSystemctl(t *testing.T) {
	tests := []struct {
		name           string
		nonInteractive bool
		yes            bool
		input          string
		wantOAuthRun   bool
		wantAuth       bool
	}{
		{name: "noninteractive headless", nonInteractive: true, wantAuth: true},
		{name: "interactive deferred", input: "n\n", wantAuth: true},
		{name: "interactive authenticated", yes: true, wantOAuthRun: true},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			home := t.TempDir()
			binDir := filepath.Join(home, "bin")
			if err := os.MkdirAll(binDir, 0o700); err != nil {
				t.Fatal(err)
			}
			proxy := filepath.Join(binDir, "cli-proxy-api")
			systemctl := filepath.Join(binDir, "systemctl")
			proxyConfig := filepath.Join(home, "proxy config.yaml")
			for _, path := range []string{proxy, filepath.Join(binDir, "claude"), filepath.Join(binDir, "curl"), filepath.Join(binDir, "git"), filepath.Join(binDir, "screen"), systemctl} {
				if err := os.WriteFile(path, nil, 0o700); err != nil {
					t.Fatal(err)
				}
			}
			systemctl, err := filepath.EvalSymlinks(systemctl)
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(proxyConfig, []byte("host: \"\"\n"), 0o600); err != nil {
				t.Fatal(err)
			}
			t.Setenv("HOME", home)
			t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, "config"))
			t.Setenv("PATH", binDir)

			runner := &recordingRunner{}
			result, err := runWorkflow(context.Background(), SetupOptions{
				Yes:                   test.yes,
				Headless:              true,
				NonInteractive:        test.nonInteractive,
				SkipCodex:             true,
				SkipSmoke:             true,
				CLIProxyExecutable:    proxy,
				CLIProxyConfig:        proxyConfig,
				CLIProxyService:       "systemd",
				CLIProxySystemService: true,
				Input:                 strings.NewReader(test.input),
				Output:                io.Discard,
				ErrorOutput:           io.Discard,
			}, runner)
			if err != nil {
				t.Fatal(err)
			}
			if !result.ServicePending || result.AuthPending != test.wantAuth {
				t.Fatalf("unexpected pending state: %+v", result)
			}
			wantAdmin := [][]string{
				{"sudo", systemctl, "enable", "cliproxyapi.service"},
				{"sudo", systemctl, "restart", "cliproxyapi.service"},
			}
			if !reflect.DeepEqual(result.ServiceAdministratorCommands, wantAdmin) {
				t.Fatalf("administrator commands = %q, want %q", result.ServiceAdministratorCommands, wantAdmin)
			}
			for _, command := range runner.commands {
				if command[0] == "systemctl" || command[0] == "sudo" {
					t.Fatalf("system service command was executed: %q", command)
				}
			}
			resolvedProxy, err := filepath.EvalSymlinks(proxy)
			if err != nil {
				t.Fatal(err)
			}
			gotOAuthRun := len(runner.commands) == 1 && runner.commands[0][0] == resolvedProxy
			if gotOAuthRun != test.wantOAuthRun {
				t.Fatalf("commands = %q, want OAuth run %v", runner.commands, test.wantOAuthRun)
			}
			if test.wantAuth {
				wantAuth := []string{resolvedProxy, "--config", proxyConfig, "--codex-device-login"}
				if !reflect.DeepEqual(result.AuthenticationCommand, wantAuth) {
					t.Fatalf("authentication command = %q, want %q", result.AuthenticationCommand, wantAuth)
				}
			}
		})
	}
}

func TestRunWorkflowUserSystemdStillExecutesAutomatically(t *testing.T) {
	home := t.TempDir()
	binDir := filepath.Join(home, "bin")
	if err := os.MkdirAll(binDir, 0o700); err != nil {
		t.Fatal(err)
	}
	proxy := filepath.Join(binDir, "cli-proxy-api")
	systemctl := filepath.Join(binDir, "systemctl")
	proxyConfig := filepath.Join(home, "config.yaml")
	for _, path := range []string{proxy, filepath.Join(binDir, "claude"), filepath.Join(binDir, "screen"), systemctl} {
		if err := os.WriteFile(path, nil, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	systemctl, err := filepath.EvalSymlinks(systemctl)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(proxyConfig, []byte("host: \"\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, "config"))
	t.Setenv("PATH", binDir)

	runner := &recordingRunner{}
	result, err := runWorkflow(context.Background(), SetupOptions{
		NonInteractive:     true,
		SkipCodex:          true,
		CLIProxyExecutable: proxy,
		CLIProxyConfig:     proxyConfig,
		CLIProxyService:    "systemd",
		Input:              strings.NewReader(""),
		Output:             io.Discard,
		ErrorOutput:        io.Discard,
	}, runner)
	if err != nil {
		t.Fatal(err)
	}
	if result.ServicePending || len(result.ServiceAdministratorCommands) != 0 {
		t.Fatalf("user service was reported as pending: %+v", result)
	}
	want := [][]string{
		{systemctl, "--user", "enable", "cliproxyapi.service"},
		{systemctl, "--user", "restart", "cliproxyapi.service"},
	}
	if !reflect.DeepEqual(runner.commands, want) {
		t.Fatalf("commands = %q, want %q", runner.commands, want)
	}
}

func TestRunWorkflowRecoversInterruptedTransactionBeforeBootstrap(t *testing.T) {
	fixture := setupManagedWorkflowFixture(t)
	paths, err := configpkg.DefaultPaths()
	if err != nil {
		t.Fatal(err)
	}
	transaction, err := newSetupTransaction(paths.StateRoot)
	if err != nil {
		t.Fatal(err)
	}
	index, err := transaction.addFile(fixture.proxyConfig, []byte("interrupted\n"), 0o600)
	if err != nil {
		t.Fatal(err)
	}
	if err := transaction.persist(); err != nil {
		t.Fatal(err)
	}
	if err := transaction.applyFile(index); err != nil {
		t.Fatal(err)
	}

	bootstrapObserved := false
	runSetupDependencies = func(_ context.Context, _ SetupOptions) error {
		bootstrapObserved = true
		data, err := os.ReadFile(fixture.proxyConfig)
		if err != nil {
			return err
		}
		if string(data) != fixture.originalProxy {
			return fmt.Errorf("bootstrap observed interrupted proxy config %q", data)
		}
		if _, err := os.Lstat(transaction.journalPath); !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("bootstrap observed setup journal: %v", err)
		}
		return errors.New("stop after bootstrap ordering check")
	}
	t.Cleanup(func() { runSetupDependencies = RunSetup })
	_, err = runWorkflow(context.Background(), fixture.options, &stageRunner{})
	if err == nil || !strings.Contains(err.Error(), "stop after bootstrap ordering check") {
		t.Fatalf("workflow error = %v", err)
	}
	if !bootstrapObserved {
		t.Fatal("workflow did not reach dependency bootstrap")
	}
}

func TestRunWorkflowDryRunReportsInterruptedTransactionWithoutMutation(t *testing.T) {
	fixture := setupManagedWorkflowFixture(t)
	paths, err := configpkg.DefaultPaths()
	if err != nil {
		t.Fatal(err)
	}
	transaction, err := newSetupTransaction(paths.StateRoot)
	if err != nil {
		t.Fatal(err)
	}
	index, err := transaction.addFile(fixture.proxyConfig, []byte("interrupted\n"), 0o600)
	if err != nil {
		t.Fatal(err)
	}
	if err := transaction.persist(); err != nil {
		t.Fatal(err)
	}
	if err := transaction.applyFile(index); err != nil {
		t.Fatal(err)
	}
	journalBefore, err := os.ReadFile(transaction.journalPath)
	if err != nil {
		t.Fatal(err)
	}
	fixture.options.DryRun = true
	_, err = runWorkflow(context.Background(), fixture.options, &stageRunner{})
	if err == nil || !strings.Contains(err.Error(), "interrupted setup transaction requires recovery") {
		t.Fatalf("dry-run error = %v", err)
	}
	assertFileState(t, fixture.proxyConfig, "interrupted\n", 0o600)
	journalAfter, err := os.ReadFile(transaction.journalPath)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(journalAfter, journalBefore) {
		t.Fatal("dry-run mutated the interrupted setup journal")
	}
}

func TestRunWorkflowDryRunWritesNothing(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("HOME", dir)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(dir, "config"))
	var output bytes.Buffer
	result, err := RunWorkflow(context.Background(), SetupOptions{
		Yes:         true,
		DryRun:      true,
		SkipCodex:   true,
		SkipProxy:   true,
		Output:      &output,
		ErrorOutput: &output,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !result.DryRun {
		t.Fatalf("unexpected result: %+v", result)
	}
	paths, err := configpkg.DefaultPaths()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(paths.ConfigFile); !os.IsNotExist(err) {
		t.Fatalf("dry-run wrote config: %v", err)
	}
}

func TestPrintWorkflowResultReportsPendingSystemServiceAndAuthentication(t *testing.T) {
	var output bytes.Buffer
	PrintWorkflowResult(&output, WorkflowResult{
		ServicePending: true,
		ServiceAdministratorCommands: [][]string{
			{"sudo", "systemctl", "enable", "cliproxyapi.service"},
			{"sudo", "systemctl", "restart", "cliproxyapi.service"},
		},
		AuthPending:           true,
		AuthenticationCommand: []string{"/tmp/proxy tool", "--config", "/tmp/user's config.yaml", "--codex-device-login"},
	})
	want := "CLIProxyAPI is configured. An administrator must apply the system service changes:\n" +
		"  'sudo' 'systemctl' 'enable' 'cliproxyapi.service'\n" +
		"  'sudo' 'systemctl' 'restart' 'cliproxyapi.service'\n" +
		"Authentication remains: '/tmp/proxy tool' '--config' '/tmp/user'\"'\"'s config.yaml' '--codex-device-login'\n" +
		"After authentication and the administrator commands complete, run 'sclaude' 'verify'.\n"
	if output.String() != want {
		t.Fatalf("output = %q, want %q", output.String(), want)
	}
}

func TestPrintWorkflowResultShellQuotesHeadlessCommand(t *testing.T) {
	var output bytes.Buffer
	PrintWorkflowResult(&output, WorkflowResult{
		AuthPending: true,
		AuthenticationCommand: []string{
			"/tmp/proxy tool",
			"--config",
			"/tmp/user's config.yaml",
			"--codex-device-login",
		},
	})
	want := "'/tmp/proxy tool' '--config' '/tmp/user'\"'\"'s config.yaml' '--codex-device-login'"
	if !strings.Contains(output.String(), want) {
		t.Fatalf("output = %q, want command %q", output.String(), want)
	}
	if !strings.Contains(output.String(), "After authentication completes, run 'sclaude' 'verify'.") {
		t.Fatalf("output = %q, missing verify follow-up", output.String())
	}
}

func TestPrintWorkflowResultReportsVerificationSkipFollowUp(t *testing.T) {
	var output bytes.Buffer
	PrintWorkflowResult(&output, WorkflowResult{VerificationSkipped: true})
	for _, wanted := range []string{
		"CLIProxyAPI authentication completed; model verification was skipped.",
		"Run 'sclaude' 'verify' to verify the configured proxy.",
	} {
		if !strings.Contains(output.String(), wanted) {
			t.Fatalf("output = %q, missing %q", output.String(), wanted)
		}
	}
}

func TestPrintWorkflowResultReportsPendingSystemServiceAfterAuthentication(t *testing.T) {
	var output bytes.Buffer
	PrintWorkflowResult(&output, WorkflowResult{
		ServicePending: true,
		ServiceAdministratorCommands: [][]string{
			{"sudo", "systemctl", "enable", "cliproxyapi.service"},
			{"sudo", "systemctl", "start", "cliproxyapi.service"},
		},
		VerificationSkipped: true,
	})
	for _, wanted := range []string{
		"'sudo' 'systemctl' 'enable' 'cliproxyapi.service'",
		"'sudo' 'systemctl' 'start' 'cliproxyapi.service'",
		"After the administrator commands complete, run 'sclaude' 'verify'.",
	} {
		if !strings.Contains(output.String(), wanted) {
			t.Fatalf("output = %q, missing %q", output.String(), wanted)
		}
	}
	if strings.Contains(output.String(), "model verification was skipped") {
		t.Fatalf("pending service output implied setup was complete: %q", output.String())
	}
}

func TestVerifyProxyModelsRequiresManagedModels(t *testing.T) {
	for _, test := range []struct {
		name      string
		models    []string
		wantError string
	}{
		{name: "all required", models: []string{"gpt-5.6-sol", "gpt-5.6-terra", "gpt-5.6-luna", "claude-opus-4-8", "claude-fable-5", "claude-sonnet-5", "claude-haiku-4-5-20251001"}},
		{name: "effort suffix", models: []string{"gpt-5.6-sol(xhigh)", "gpt-5.6-terra", "gpt-5.6-luna(low)", "claude-opus-4-8", "claude-fable-5", "claude-sonnet-5", "claude-haiku-4-5-20251001"}},
		{name: "missing codex", models: []string{"gpt-5.6-sol", "claude-opus-4-8", "claude-fable-5", "claude-sonnet-5", "claude-haiku-4-5-20251001"}, wantError: "gpt-5.6-terra, gpt-5.6-luna"},
		{name: "missing alias", models: []string{"gpt-5.6-sol", "gpt-5.6-terra", "gpt-5.6-luna", "claude-opus-4-8", "claude-fable-5", "claude-sonnet-5"}, wantError: "claude-haiku-4-5-20251001"},
	} {
		t.Run(test.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Header.Get("Authorization") != "Bearer sk-test" {
					t.Errorf("authorization = %q", r.Header.Get("Authorization"))
				}
				switch r.URL.Path {
				case "/v1/models":
					data := make([]map[string]string, 0, len(test.models))
					for _, model := range test.models {
						data = append(data, map[string]string{"id": model})
					}
					_ = json.NewEncoder(w).Encode(map[string]any{"data": data})
				case "/v1/messages":
					if r.Method != http.MethodPost || r.Header.Get("Anthropic-Version") == "" {
						t.Errorf("unexpected inference request: %s headers=%v", r.Method, r.Header)
					}
					var payload map[string]any
					if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
						t.Errorf("decode payload: %v", err)
					} else if payload["model"] != "claude-opus-4-8(xhigh)" {
						t.Errorf("model = %v", payload["model"])
					}
					_ = json.NewEncoder(w).Encode(map[string]any{
						"type":  "message",
						"role":  "assistant",
						"model": "gpt-5.6-sol",
						"content": []map[string]string{
							{"type": "text", "text": "OK"},
						},
					})
				default:
					http.NotFound(w, r)
				}
			}))
			defer server.Close()

			models, err := verifyProxyModels(
				context.Background(),
				server.Client(),
				ProxyCredential{SchemaVersion: 1, BaseURL: server.URL, APIKey: "sk-test"},
			)
			if test.wantError == "" {
				if err != nil || len(models) != len(test.models) {
					t.Fatalf("models=%q err=%v", models, err)
				}
			} else if err == nil || !strings.Contains(err.Error(), test.wantError) {
				t.Fatalf("error = %v", err)
			}
		})
	}
}

func TestFetchProxyModelsRejectsOversizedOrTrailingJSON(t *testing.T) {
	const (
		apiKey           = "sk-models-secret"
		responseSecret   = "body-models-secret"
		maxResponseSize  = 4 << 20
		validModelsReply = `{"data":[{"id":"gpt-5.6-sol"}]}`
	)
	for _, test := range []struct {
		name string
		body string
		want string
	}{
		{name: "trailing second value", body: validModelsReply + ` {"secret":"` + responseSecret + `"}`, want: "invalid JSON"},
		{name: "oversized trailing bytes", body: validModelsReply + strings.Repeat(" ", maxResponseSize-len(validModelsReply)+1), want: "too large"},
	} {
		t.Run(test.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if got := r.Header.Get("Authorization"); got != "Bearer "+apiKey {
					t.Errorf("authorization = %q", got)
				}
				_, _ = io.WriteString(w, test.body)
			}))
			defer server.Close()

			_, err := fetchProxyModels(
				context.Background(),
				server.Client(),
				ProxyCredential{SchemaVersion: 1, BaseURL: server.URL, APIKey: apiKey},
			)
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("error = %v, want substring %q", err, test.want)
			}
			for _, secret := range []string{apiKey, responseSecret} {
				if strings.Contains(err.Error(), secret) {
					t.Fatalf("error leaked secret %q: %v", secret, err)
				}
			}
		})
	}
}

func TestAuthenticatedProxyVerificationRejectsRedirectsWithoutForwardingAuthorization(t *testing.T) {
	const apiKey = "sk-redirect-secret"
	for _, test := range []struct {
		name   string
		verify func(context.Context, *http.Client, ProxyCredential) error
	}{
		{
			name: "models",
			verify: func(ctx context.Context, client *http.Client, credential ProxyCredential) error {
				_, err := fetchProxyModels(ctx, client, credential)
				return err
			},
		},
		{
			name:   "messages",
			verify: verifyProxyInference,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			var targetRequests int
			target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
				targetRequests++
				if authorization := request.Header.Get("Authorization"); authorization != "" {
					t.Errorf("redirect target received authorization %q", authorization)
				}
				http.Error(w, "unexpected redirect target", http.StatusInternalServerError)
			}))
			defer target.Close()

			origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
				if got := request.Header.Get("Authorization"); got != "Bearer "+apiKey {
					t.Errorf("origin authorization = %q", got)
				}
				http.Redirect(w, request, target.URL+request.URL.Path, http.StatusTemporaryRedirect)
			}))
			defer origin.Close()

			err := test.verify(
				context.Background(),
				origin.Client(),
				ProxyCredential{SchemaVersion: 1, BaseURL: origin.URL, APIKey: apiKey},
			)
			if err == nil || !strings.Contains(err.Error(), "does not follow redirects") {
				t.Fatalf("error = %v", err)
			}
			if targetRequests != 0 {
				t.Fatalf("redirect target requests = %d want 0", targetRequests)
			}
			if strings.Contains(err.Error(), apiKey) {
				t.Fatalf("error leaked API key: %v", err)
			}
		})
	}
}

func TestVerifyProxyInferenceValidatesResponse(t *testing.T) {
	const apiKey = "sk-credential-secret"
	tests := []struct {
		name       string
		body       string
		wantError  bool
		bodySecret string
	}{
		{
			name: "requested alias with effort",
			body: `{"type":"message","role":"assistant","model":"claude-opus-4-8(xhigh)","content":[{"type":"text","text":"OK"}]}`,
		},
		{
			name: "requested alias without effort",
			body: `{"type":"message","role":"assistant","model":"claude-opus-4-8","content":[{"type":"text","text":"OK"}]}`,
		},
		{
			name: "underlying model",
			body: `{"type":"message","role":"assistant","model":"gpt-5.6-sol","content":[{"type":"text","text":"OK"}]}`,
		},
		{
			name: "underlying model with effort and whitespace",
			body: `{"type":"message","role":"assistant","model":"gpt-5.6-sol(xhigh)","content":[{"type":"text","text":"  OK\n"}]}`,
		},
		{
			name: "thinking and multiple text blocks",
			body: `{"type":"message","role":"assistant","model":"gpt-5.6-sol","content":[{"type":"thinking","thinking":"body-thinking-secret"},{"type":"text","text":" O"},{"type":"text","text":"K "}]}`,
		},
		{
			name:      "empty object",
			body:      `{}`,
			wantError: true,
		},
		{
			name:       "malformed JSON",
			body:       `{"type":"message","response_secret":"body-malformed-secret"`,
			wantError:  true,
			bodySecret: "body-malformed-secret",
		},
		{
			name:       "wrong content",
			body:       `{"type":"message","role":"assistant","model":"gpt-5.6-sol","content":[{"type":"text","text":"NOT OK body-content-secret"}]}`,
			wantError:  true,
			bodySecret: "body-content-secret",
		},
		{
			name:       "no text blocks",
			body:       `{"type":"message","role":"assistant","model":"gpt-5.6-sol","content":[{"type":"thinking","thinking":"body-thinking-secret"}]}`,
			wantError:  true,
			bodySecret: "body-thinking-secret",
		},
		{
			name:       "multiple text blocks concatenate wrong content",
			body:       `{"type":"message","role":"assistant","model":"gpt-5.6-sol","content":[{"type":"text","text":"OK"},{"type":"thinking","thinking":"ignored"},{"type":"text","text":" body-extra-secret"}]}`,
			wantError:  true,
			bodySecret: "body-extra-secret",
		},
		{
			name:       "wrong model",
			body:       `{"type":"message","role":"assistant","model":"body-model-secret","content":[{"type":"text","text":"OK"}]}`,
			wantError:  true,
			bodySecret: "body-model-secret",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if got := r.Header.Get("Authorization"); got != "Bearer "+apiKey {
					t.Errorf("authorization = %q", got)
				}
				_, _ = w.Write([]byte(test.body))
			}))
			defer server.Close()

			err := verifyProxyInference(
				context.Background(),
				server.Client(),
				ProxyCredential{SchemaVersion: 1, BaseURL: server.URL, APIKey: apiKey},
			)
			if test.wantError {
				if err == nil {
					t.Fatal("expected response validation error")
				}
				if strings.Contains(err.Error(), apiKey) {
					t.Fatalf("error leaked API key: %v", err)
				}
				if test.bodySecret != "" && strings.Contains(err.Error(), test.bodySecret) {
					t.Fatalf("error leaked response body: %v", err)
				}
			} else if err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestServiceCommandVariants(t *testing.T) {
	systemctl := filepath.Join(t.TempDir(), "systemctl")
	if got, err := serviceCommandFor("start", "systemd", true, systemctl); err != nil || !reflect.DeepEqual(got, []string{systemctl, "start", "cliproxyapi.service"}) {
		t.Fatalf("system service = %q, %v", got, err)
	}
	if got, err := serviceCommandFor("restart", "systemd", false, systemctl); err != nil || !reflect.DeepEqual(got, []string{systemctl, "--user", "restart", "cliproxyapi.service"}) {
		t.Fatalf("user service restart = %q, %v", got, err)
	}
	if got, err := serviceCommandFor("enable", "systemd", false, systemctl); err != nil || !reflect.DeepEqual(got, []string{systemctl, "--user", "enable", "cliproxyapi.service"}) {
		t.Fatalf("user service enable = %q, %v", got, err)
	}
	if got, err := serviceCommandFor("restart", "docker", false, ""); err != nil || len(got) != 0 {
		t.Fatalf("docker service = %q, %v", got, err)
	}
}

func TestOAuthCommand(t *testing.T) {
	if got := strings.Join(OAuthCommand("/bin/proxy", "/tmp/config.yaml", false), " "); got != "/bin/proxy --config /tmp/config.yaml --codex-login" {
		t.Fatal(got)
	}
	if got := strings.Join(OAuthCommand("/bin/proxy", "/tmp/config.yaml", true), " "); got != "/bin/proxy --config /tmp/config.yaml --codex-device-login" {
		t.Fatal(got)
	}
}

func TestValidateCLIProxyExecutable(t *testing.T) {
	dir := t.TempDir()
	executable := filepath.Join(dir, "cliproxyapi")
	if err := os.WriteFile(executable, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	resolved, err := ValidateCLIProxyExecutable(executable)
	if err != nil {
		t.Fatal(err)
	}
	wantResolved, err := filepath.EvalSymlinks(executable)
	if err != nil {
		t.Fatal(err)
	}
	if resolved != wantResolved {
		t.Fatalf("resolved = %q, want %q", resolved, wantResolved)
	}

	notExecutable := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(notExecutable, []byte("host: localhost\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := ValidateCLIProxyExecutable(notExecutable); err == nil || !strings.Contains(err.Error(), "regular executable file") {
		t.Fatalf("expected executable validation error, got %v", err)
	}
	if _, err := ValidateCLIProxyExecutable(dir); err == nil || !strings.Contains(err.Error(), "regular executable file") {
		t.Fatalf("expected directory validation error, got %v", err)
	}

	otherOnly := filepath.Join(dir, "other-only")
	if err := os.WriteFile(otherOnly, []byte("#!/bin/sh\n"), 0o001); err != nil {
		t.Fatal(err)
	}
	if _, err := ValidateCLIProxyExecutable(otherOnly); err == nil || !strings.Contains(err.Error(), "regular executable file") {
		t.Fatalf("expected current-user executable validation error, got %v", err)
	}
	if ok, _ := configuredExecutable(otherOnly); ok {
		t.Fatal("doctor accepted a current-user-owned file executable only by others")
	}
}

func TestDiscoverCLIProxyConfigUsesActiveHomebrewPrefix(t *testing.T) {
	home := t.TempDir()
	prefix := t.TempDir()
	activeConfig := filepath.Join(prefix, "etc", "cliproxyapi.conf")
	homeConfig := filepath.Join(home, "cliproxyapi", "config.yaml")
	for _, path := range []string{activeConfig, homeConfig} {
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, nil, 0o600); err != nil {
			t.Fatal(err)
		}
	}

	got, err := discoverCLIProxyConfig(
		"darwin",
		func() (string, error) { return home, nil },
		func() (string, error) { return prefix + "\n", nil },
	)
	if err != nil {
		t.Fatal(err)
	}
	want, err := filepath.EvalSymlinks(activeConfig)
	if err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Fatalf("discovered config = %q, want active Homebrew config %q", got, want)
	}
}

func TestDiscoverCLIProxyConfigFallsBackToHome(t *testing.T) {
	home := t.TempDir()
	homeConfig := filepath.Join(home, "cliproxyapi", "config.yaml")
	if err := os.MkdirAll(filepath.Dir(homeConfig), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(homeConfig, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	brewCalls := 0
	got, err := discoverCLIProxyConfig(
		"linux",
		func() (string, error) { return home, nil },
		func() (string, error) { brewCalls++; return "", errors.New("unexpected brew lookup") },
	)
	if err != nil {
		t.Fatal(err)
	}
	if brewCalls != 0 {
		t.Fatalf("brew lookup called %d times on Linux", brewCalls)
	}
	want, err := filepath.EvalSymlinks(homeConfig)
	if err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Fatalf("discovered config = %q, want home config %q", got, want)
	}
}

func TestValidateCLIProxyServiceConfig(t *testing.T) {
	unusedPrefix := func() (string, error) {
		t.Fatal("Homebrew prefix lookup should not run for non-brew managers")
		return "", nil
	}
	if err := validateCLIProxyServiceConfig("none", "/tmp/custom.yaml", unusedPrefix); err != nil {
		t.Fatal(err)
	}
	if err := validateCLIProxyServiceConfig("systemd", "/tmp/custom.yaml", unusedPrefix); err != nil {
		t.Fatal(err)
	}

	prefix := filepath.Join(t.TempDir(), "homebrew")
	activePrefix := func() (string, error) { return prefix + "\n", nil }
	activeConfig := filepath.Join(prefix, "etc", "cliproxyapi.conf")
	if err := os.MkdirAll(filepath.Dir(activeConfig), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(activeConfig, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := validateCLIProxyServiceConfig("brew", activeConfig, activePrefix); err != nil {
		t.Fatal(err)
	}
	alias := filepath.Join(t.TempDir(), "cliproxyapi.conf")
	if err := os.Symlink(activeConfig, alias); err != nil {
		t.Fatal(err)
	}
	if err := validateCLIProxyServiceConfig("brew", alias, activePrefix); err != nil {
		t.Fatalf("active config symlink was rejected: %v", err)
	}
	for _, configPath := range []string{
		"/tmp/custom.yaml",
		"/opt/homebrew/etc/cliproxyapi.conf",
		"/usr/local/etc/cliproxyapi.conf",
	} {
		if filepath.Clean(configPath) == filepath.Clean(activeConfig) {
			continue
		}
		if err := validateCLIProxyServiceConfig("brew", configPath, activePrefix); err == nil || !strings.Contains(err.Error(), "active brew --prefix") {
			t.Fatalf("expected inactive Homebrew config rejection for %q, got %v", configPath, err)
		}
	}
	if err := validateCLIProxyServiceConfig("brew", activeConfig, func() (string, error) {
		return "", errors.New("brew unavailable")
	}); err == nil || !strings.Contains(err.Error(), "brew unavailable") {
		t.Fatalf("expected Homebrew prefix error, got %v", err)
	}
}

func TestValidateCLIProxyServiceConfigRejectsEscapedHomebrewEtc(t *testing.T) {
	prefix := filepath.Join(t.TempDir(), "homebrew")
	externalEtc := filepath.Join(t.TempDir(), "etc")
	if err := os.MkdirAll(externalEtc, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(prefix, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(externalEtc, filepath.Join(prefix, "etc")); err != nil {
		t.Fatal(err)
	}
	configPath := filepath.Join(externalEtc, "cliproxyapi.conf")
	if err := os.WriteFile(configPath, nil, 0o600); err != nil {
		t.Fatal(err)
	}

	if err := validateCLIProxyServiceConfig("brew", configPath, func() (string, error) {
		return prefix, nil
	}); err == nil || !strings.Contains(err.Error(), "active brew --prefix") {
		t.Fatalf("escaped Homebrew config was accepted: %v", err)
	}
}

func TestConfigLoadValidatesManagedSettingsPath(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	runtimeConfig := configpkg.Runtime{
		SchemaVersion:      configpkg.SchemaVersion,
		RealClaude:         "/bin/claude",
		ClaudexMode:        "managed_proxy",
		ScreenPath:         "/usr/bin/screen",
		CLIProxyExecutable: "/bin/cliproxyapi",
		CLIProxyConfig:     "/tmp/cliproxyapi.yaml",
		ProxyCredential:    "/tmp/proxy-credential.json",
		CLIProxyService:    "none",
	}
	if err := writeJSONPrivate(path, runtimeConfig); err != nil {
		t.Fatal(err)
	}
	_, err := configpkg.Load(path)
	if err == nil || !strings.Contains(err.Error(), "managed_settings") {
		t.Fatalf("config.Load error = %v", err)
	}
}

func TestManagedSettingsEncodeWritesCanonicalEnvironment(t *testing.T) {
	credential := ProxyCredential{SchemaVersion: 1, BaseURL: "http://127.0.0.1:8317", APIKey: "sk-overlay-secret"}
	data, err := managedsettings.Encode(credential.BaseURL, credential.APIKey)
	if err != nil {
		t.Fatal(err)
	}
	var document map[string]any
	if err := json.Unmarshal(data, &document); err != nil {
		t.Fatal(err)
	}
	if len(document) != 1 {
		t.Fatalf("managed settings contain non-env keys: %#v", document)
	}
	env, ok := document["env"].(map[string]any)
	if !ok {
		t.Fatalf("managed env = %#v", document["env"])
	}
	for key, want := range map[string]string{
		"ANTHROPIC_BASE_URL":         credential.BaseURL,
		"ANTHROPIC_AUTH_TOKEN":       credential.APIKey,
		"CLAUDE_CODE_USE_BEDROCK":    "",
		"CLAUDE_CODE_USE_VERTEX":     "",
		"CLAUDE_CODE_USE_FOUNDRY":    "",
		"ANTHROPIC_API_KEY":          "",
		"ANTHROPIC_BEDROCK_BASE_URL": "",
	} {
		if env[key] != want {
			t.Fatalf("managed env %s = %#v, want %q", key, env[key], want)
		}
	}
}

func TestRuntimeConfigPathsAndLoad(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("HOME", dir)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(dir, "config"))
	paths, err := configpkg.DefaultPaths()
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(dir, "config", "sclaude", "config.json")
	if paths.ConfigFile != want {
		t.Fatalf("config path %q, want %q", paths.ConfigFile, want)
	}
	if paths.ManagedSettings != filepath.Join(dir, "config", "sclaude", "managed-settings.json") {
		t.Fatalf("managed settings path = %q", paths.ManagedSettings)
	}
	runtimeConfig := configpkg.Runtime{
		SchemaVersion:   configpkg.SchemaVersion,
		RealClaude:      "/bin/claude",
		ClaudexMode:     "external",
		RealClaudex:     "/bin/claudex",
		ScreenPath:      "/usr/bin/screen",
		CLIProxyService: "none",
	}
	if err := configpkg.Save(paths.ConfigFile, runtimeConfig); err != nil {
		t.Fatal(err)
	}
	loaded, err := configpkg.Load(paths.ConfigFile)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.RealClaudex != runtimeConfig.RealClaudex || loaded.ScreenPath != runtimeConfig.ScreenPath {
		t.Fatalf("loaded config: %+v", loaded)
	}
}
