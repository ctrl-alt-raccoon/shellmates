package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestPathsForDefaults(t *testing.T) {
	home := filepath.Join(string(filepath.Separator), "home", "alice")
	paths := PathsFor(home, nil)
	want := map[string]string{
		"config": filepath.Join(home, ".config", "sclaude", "config.json"),
		"state":  filepath.Join(home, ".local", "state", "sclaude"),
		"data":   filepath.Join(home, ".local", "share", "sclaude"),
	}
	if paths.ConfigFile != want["config"] || paths.StateRoot != want["state"] || paths.DataRoot != want["data"] {
		t.Fatalf("paths = %#v, want config=%q state=%q data=%q", paths, want["config"], want["state"], want["data"])
	}
	if paths.SessionsDir != filepath.Join(paths.StateRoot, "sessions") || paths.LaunchDir != filepath.Join(paths.StateRoot, "launch") {
		t.Fatalf("state subdirectories do not share state root: %#v", paths)
	}
	if paths.ManagedSettings != filepath.Join(home, ".config", "sclaude", "managed-settings.json") {
		t.Fatalf("managed settings path = %q", paths.ManagedSettings)
	}
}

func TestPathsForXDGOverrides(t *testing.T) {
	env := map[string]string{
		"XDG_CONFIG_HOME": filepath.Join(string(filepath.Separator), "xdg", "config"),
		"XDG_STATE_HOME":  filepath.Join(string(filepath.Separator), "xdg", "state"),
		"XDG_DATA_HOME":   filepath.Join(string(filepath.Separator), "xdg", "data"),
	}
	paths := PathsFor("/ignored", func(key string) string { return env[key] })
	if paths.ConfigFile != filepath.Join(env["XDG_CONFIG_HOME"], "sclaude", "config.json") {
		t.Fatalf("config path = %q", paths.ConfigFile)
	}
	if paths.StateRoot != filepath.Join(env["XDG_STATE_HOME"], "sclaude") {
		t.Fatalf("state path = %q", paths.StateRoot)
	}
	if paths.DataRoot != filepath.Join(env["XDG_DATA_HOME"], "sclaude") {
		t.Fatalf("data path = %q", paths.DataRoot)
	}
}

func TestLoadStrictCanonicalDocument(t *testing.T) {
	valid := `{"schema_version":1,"real_claude":"/bin/claude","claudex_mode":"external","real_claudex":"/bin/claudex","screen_path":"/usr/bin/screen","cliproxyapi_service":"none"}`
	tests := []struct {
		name string
		body string
		want string
	}{
		{"valid", valid, ""},
		{"missing schema", strings.Replace(valid, `"schema_version":1,`, "", 1), "schema version 0"},
		{"unknown field", strings.TrimSuffix(valid, "}") + `,"extra":true}`, "invalid JSON document"},
		{"trailing value", valid + `{}`, "invalid JSON document"},
		{"trailing garbage", valid + ` garbage`, "invalid JSON document"},
		{"auto service", strings.Replace(valid, `"none"`, `"auto"`, 1), "concrete"},
		{"relative executable", strings.Replace(valid, `"/bin/claude"`, `"bin/claude"`, 1), "absolute path"},
		{"external managed field", strings.TrimSuffix(valid, "}") + `,"proxy_credential":"/tmp/credential"}`, "managed proxy fields"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "config.json")
			if err := os.WriteFile(path, []byte(test.body), 0o600); err != nil {
				t.Fatal(err)
			}
			_, err := Load(path)
			if test.want == "" {
				if err != nil {
					t.Fatal(err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("Load error = %v, want containing %q", err, test.want)
			}
		})
	}
}

func TestLoadRequiresExactPrivateRegularFile(t *testing.T) {
	runtime := Runtime{SchemaVersion: 1, RealClaude: "/bin/claude", ClaudexMode: "external", RealClaudex: "/bin/claudex", ScreenPath: "/usr/bin/screen", CLIProxyService: "none"}
	data, err := EncodeRuntime(runtime)
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	for _, test := range []struct {
		name string
		make func(string) error
		want string
	}{
		{
			name: "permissive mode",
			make: func(path string) error { return os.WriteFile(path, data, 0o644) },
			want: "mode 0600",
		},
		{
			name: "symlink",
			make: func(path string) error {
				target := filepath.Join(filepath.Dir(path), "target")
				if err := os.WriteFile(target, data, 0o600); err != nil {
					return err
				}
				return os.Symlink(target, path)
			},
			want: "symbolic link",
		},
		{
			name: "directory",
			make: func(path string) error { return os.Mkdir(path, 0o700) },
			want: "regular",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			parent := filepath.Join(root, strings.ReplaceAll(test.name, " ", "-"))
			if err := os.Mkdir(parent, 0o700); err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(parent, "config.json")
			if err := test.make(path); err != nil {
				t.Fatal(err)
			}
			if _, err := Load(path); err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("Load error = %v, want containing %q", err, test.want)
			}
		})
	}
}

func TestLoadAllowsSymlinkedAncestor(t *testing.T) {
	root := t.TempDir()
	realParent := filepath.Join(root, "real", "sclaude")
	if err := os.MkdirAll(realParent, 0o700); err != nil {
		t.Fatal(err)
	}
	ancestor := filepath.Join(root, "config")
	if err := os.Symlink(filepath.Join(root, "real"), ancestor); err != nil {
		t.Fatal(err)
	}
	runtime := Runtime{SchemaVersion: 1, RealClaude: "/bin/claude", ClaudexMode: "external", RealClaudex: "/bin/claudex", ScreenPath: "/usr/bin/screen", CLIProxyService: "none"}
	path := filepath.Join(ancestor, "sclaude", "config.json")
	if err := Save(path, runtime); err != nil {
		t.Fatal(err)
	}
	loaded, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if loaded != runtime {
		t.Fatalf("Load = %#v, want %#v", loaded, runtime)
	}
}

func TestRuntimeValidateModeContradictions(t *testing.T) {
	managed := Runtime{
		SchemaVersion:      1,
		RealClaude:         "/bin/claude",
		ClaudexMode:        "managed_proxy",
		ScreenPath:         "/usr/bin/screen",
		CLIProxyExecutable: "/usr/bin/cliproxyapi",
		CLIProxyConfig:     "/etc/cliproxyapi.yaml",
		ProxyCredential:    "/etc/sclaude/credential.json",
		ManagedSettings:    "/etc/sclaude/settings.json",
		CLIProxyService:    "brew",
	}
	if err := managed.Validate(); err != nil {
		t.Fatal(err)
	}
	managed.RealClaudex = "/bin/claudex"
	if err := managed.Validate(); err == nil || !strings.Contains(err.Error(), "external claudex") {
		t.Fatalf("managed contradiction error = %v", err)
	}
	managed.RealClaudex = ""
	managed.CLIProxySystemUnit = true
	if err := managed.Validate(); err == nil || !strings.Contains(err.Error(), "systemd") {
		t.Fatalf("system unit contradiction error = %v", err)
	}
}

func TestSaveIsDeterministicPrivateAndAtomic(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nested", "config.json")
	runtime := Runtime{SchemaVersion: 1, RealClaude: "/bin/claude", ClaudexMode: "external", RealClaudex: "/bin/claudex", ScreenPath: "/usr/bin/screen", CLIProxyService: "none"}
	if err := Save(path, runtime); err != nil {
		t.Fatal(err)
	}
	first, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(first) == 0 || first[len(first)-1] != '\n' {
		t.Fatalf("saved config lacks one final newline: %q", first)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("saved mode = %04o, want 0600", info.Mode().Perm())
	}
	if err := Save(path, runtime); err != nil {
		t.Fatal(err)
	}
	second, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(first) != string(second) {
		t.Fatalf("Save output changed:\nfirst=%s\nsecond=%s", first, second)
	}
}

func TestSaveRejectsInvalidPathBeforeMutation(t *testing.T) {
	root := t.TempDir()
	parent := filepath.Join(root, "parent")
	path := parent + string(filepath.Separator) + ".." + string(filepath.Separator) + "config.json"
	runtime := Runtime{SchemaVersion: 1, RealClaude: "/bin/claude", ClaudexMode: "external", RealClaudex: "/bin/claudex", ScreenPath: "/usr/bin/screen", CLIProxyService: "none"}

	if err := Save(path, runtime); err == nil || !strings.Contains(err.Error(), "absolute clean file path") {
		t.Fatalf("Save error = %v", err)
	}
	if _, err := os.Lstat(parent); !os.IsNotExist(err) {
		t.Fatalf("invalid path caused mutation: %v", err)
	}
}

func TestSaveSecuresNonprivateConfigurationParent(t *testing.T) {
	root := t.TempDir()
	parent := filepath.Join(root, "config")
	if err := os.Mkdir(parent, 0o755); err != nil {
		t.Fatal(err)
	}
	runtime := Runtime{SchemaVersion: 1, RealClaude: "/bin/claude", ClaudexMode: "external", RealClaudex: "/bin/claudex", ScreenPath: "/usr/bin/screen", CLIProxyService: "none"}
	path := filepath.Join(parent, "config.json")
	if err := Save(path, runtime); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(parent)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode() != os.ModeDir|0o700 {
		t.Fatalf("configuration parent mode = %v, want 0700 directory", info.Mode())
	}
}

func TestSaveRejectsParentReplacementBeforePublish(t *testing.T) {
	root := t.TempDir()
	parent := filepath.Join(root, "config")
	if err := os.Mkdir(parent, 0o700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(parent, "config.json")
	if err := os.WriteFile(path, []byte("original\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	originalParent := filepath.Join(root, "original-config")
	beforePrivateConfigPublish = func(publishPath string) error {
		if publishPath != path {
			t.Fatalf("publish path = %q, want %q", publishPath, path)
		}
		if err := os.Rename(parent, originalParent); err != nil {
			return err
		}
		return os.Mkdir(parent, 0o700)
	}
	t.Cleanup(func() { beforePrivateConfigPublish = nil })

	runtime := Runtime{SchemaVersion: 1, RealClaude: "/bin/claude", ClaudexMode: "external", RealClaudex: "/bin/claudex", ScreenPath: "/usr/bin/screen", CLIProxyService: "none"}
	err := Save(path, runtime)
	beforePrivateConfigPublish = nil
	if err == nil || !strings.Contains(err.Error(), "parent changed") {
		t.Fatalf("Save error = %v", err)
	}
	data, err := os.ReadFile(filepath.Join(originalParent, "config.json"))
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "original\n" {
		t.Fatalf("original target changed: %q", data)
	}
	if _, err := os.Lstat(path); !os.IsNotExist(err) {
		t.Fatalf("replacement parent was mutated: %v", err)
	}
}

func TestSaveRejectsTargetReplacementBeforePublish(t *testing.T) {
	parent := t.TempDir()
	path := filepath.Join(parent, "config.json")
	if err := os.WriteFile(path, []byte("original\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	beforePrivateConfigPublish = func(string) error {
		replacement := filepath.Join(parent, "replacement")
		if err := os.WriteFile(replacement, []byte("external\n"), 0o600); err != nil {
			return err
		}
		return os.Rename(replacement, path)
	}
	t.Cleanup(func() { beforePrivateConfigPublish = nil })

	runtime := Runtime{SchemaVersion: 1, RealClaude: "/bin/claude", ClaudexMode: "external", RealClaudex: "/bin/claudex", ScreenPath: "/usr/bin/screen", CLIProxyService: "none"}
	err := Save(path, runtime)
	beforePrivateConfigPublish = nil
	if err == nil || !strings.Contains(err.Error(), "target changed") {
		t.Fatalf("Save error = %v", err)
	}
	data, readErr := os.ReadFile(path)
	if readErr != nil {
		t.Fatal(readErr)
	}
	if string(data) != "external\n" {
		t.Fatalf("external target changed: %q", data)
	}
}

func TestSaveRejectsTargetReplacementAfterPublish(t *testing.T) {
	parent := t.TempDir()
	path := filepath.Join(parent, "config.json")
	if err := os.WriteFile(path, []byte("original\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	afterPrivateConfigPublish = func(string) error {
		replacement := filepath.Join(parent, "replacement")
		if err := os.WriteFile(replacement, []byte("external\n"), 0o600); err != nil {
			return err
		}
		return os.Rename(replacement, path)
	}
	t.Cleanup(func() { afterPrivateConfigPublish = nil })

	runtime := Runtime{SchemaVersion: 1, RealClaude: "/bin/claude", ClaudexMode: "external", RealClaudex: "/bin/claudex", ScreenPath: "/usr/bin/screen", CLIProxyService: "none"}
	err := Save(path, runtime)
	afterPrivateConfigPublish = nil
	if err == nil || !strings.Contains(err.Error(), "changed after publication") {
		t.Fatalf("Save error = %v", err)
	}
	data, readErr := os.ReadFile(path)
	if readErr != nil {
		t.Fatal(readErr)
	}
	if string(data) != "external\n" {
		t.Fatalf("external target changed: %q", data)
	}
}

func TestSaveRetainsDisplacedPrivateConfiguration(t *testing.T) {
	parent := t.TempDir()
	path := filepath.Join(parent, "config.json")
	if err := os.WriteFile(path, []byte("historical-private-configuration\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	runtime := Runtime{SchemaVersion: 1, RealClaude: "/bin/claude", ClaudexMode: "external", RealClaudex: "/bin/claudex", ScreenPath: "/usr/bin/screen", CLIProxyService: "none"}
	if err := Save(path, runtime); err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(parent)
	if err != nil {
		t.Fatal(err)
	}
	var tombstones []os.DirEntry
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), ".sclaude-") {
			tombstones = append(tombstones, entry)
		}
	}
	if len(tombstones) != 1 {
		t.Fatalf("private configuration tombstones = %v, want one", tombstones)
	}
	info, err := tombstones[0].Info()
	if err != nil {
		t.Fatal(err)
	}
	if !info.Mode().IsRegular() || info.Size() != int64(len("historical-private-configuration\n")) {
		t.Fatalf("private configuration tombstone mode = %v size = %d", info.Mode(), info.Size())
	}
	data, err := os.ReadFile(filepath.Join(parent, tombstones[0].Name()))
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "historical-private-configuration\n" {
		t.Fatalf("private configuration tombstone contents = %q", data)
	}
}

func TestSaveRejectsSymlinkAndNonregularTargets(t *testing.T) {
	dir := t.TempDir()
	runtime := Runtime{SchemaVersion: 1, RealClaude: "/bin/claude", ClaudexMode: "external", RealClaudex: "/bin/claudex", ScreenPath: "/usr/bin/screen", CLIProxyService: "none"}
	target := filepath.Join(dir, "target")
	if err := os.WriteFile(target, []byte("unchanged"), 0o600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, "config.json")
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	if err := Save(link, runtime); err == nil || !strings.Contains(err.Error(), "non-symlink") {
		t.Fatalf("symlink Save error = %v", err)
	}
	data, _ := os.ReadFile(target)
	if string(data) != "unchanged" {
		t.Fatalf("symlink target changed: %q", data)
	}
	if err := Save(dir, runtime); err == nil || !strings.Contains(err.Error(), "regular file") {
		t.Fatalf("directory Save error = %v", err)
	}
}

func TestProxyCredentialStrictLoadAndEncoding(t *testing.T) {
	credential, err := ParseProxyCredential([]byte(`{"schema_version":1,"base_url":"http://127.0.0.1:8317/","api_key":"sk-test","created_at":"now"}`))
	if err != nil {
		t.Fatal(err)
	}
	if credential.BaseURL != ManagedProxyBaseURL {
		t.Fatalf("base URL = %q", credential.BaseURL)
	}
	encoded, err := EncodeProxyCredential(credential)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasSuffix(string(encoded), "\n") || strings.Contains(string(encoded), ManagedProxyBaseURL+`/"`) {
		t.Fatalf("noncanonical encoding: %s", encoded)
	}
	for _, body := range []string{
		`{"base_url":"http://127.0.0.1:8317","api_key":"sk"}`,
		`{"schema_version":1,"base_url":"http://127.0.0.1:8317","api_key":"sk","extra":true}`,
		`{"schema_version":1,"base_url":"http://127.0.0.1:8317","api_key":"sk"}{}`,
		`{"schema_version":1,"base_url":"https://example.test","api_key":"sk"}`,
		`{"schema_version":1,"base_url":"http://127.0.0.1:8317","api_key":" "}`,
	} {
		if _, err := ParseProxyCredential([]byte(body)); err == nil {
			t.Fatalf("ParseProxyCredential accepted %s", body)
		}
	}
}

func TestLoadProxyCredentialRequiresExactPrivateRegularFile(t *testing.T) {
	dir := t.TempDir()
	body := []byte(`{"schema_version":1,"base_url":"http://127.0.0.1:8317","api_key":"sk-secret"}`)
	path := filepath.Join(dir, "credential.json")
	if err := os.WriteFile(path, body, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadProxyCredential(path); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, 0o400); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadProxyCredential(path); err == nil || !strings.Contains(err.Error(), "0600") {
		t.Fatalf("mode error = %v", err)
	}
	if err := os.Chmod(path, 0o600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, "credential-link.json")
	if err := os.Symlink(path, link); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadProxyCredential(link); err == nil || !strings.Contains(err.Error(), "symbolic link") {
		t.Fatalf("symlink error = %v", err)
	}
}

func TestLoadDoesNotExposeConfigContents(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	secret := "sk-do-not-print"
	if err := os.WriteFile(path, []byte(`{"real_claude":"`+secret), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := Load(path)
	if err == nil {
		t.Fatal("Load succeeded for invalid JSON")
	}
	if strings.Contains(err.Error(), secret) {
		t.Fatalf("error exposed config contents: %v", err)
	}
}
