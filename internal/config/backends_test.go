package config

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

func TestNativeBackendConfiguration(t *testing.T) {
	for _, test := range []struct {
		name    string
		runtime Runtime
		valid   bool
	}{
		{"codex only", Runtime{SchemaVersion: 2, EnabledBackends: []string{"codex"}, RealCodex: "/bin/codex", ScreenPath: "/bin/screen", CLIProxyService: "none"}, true},
		{"claude only", Runtime{SchemaVersion: 2, EnabledBackends: []string{"claude"}, RealClaude: "/bin/claude", ScreenPath: "/bin/screen", CLIProxyService: "none"}, true},
		{"external only", Runtime{SchemaVersion: 2, EnabledBackends: []string{"claudex"}, ClaudexMode: "external", RealClaudex: "/bin/opaque", ScreenPath: "/bin/screen", CLIProxyService: "none"}, true},
		{"mixed native", Runtime{SchemaVersion: 2, EnabledBackends: []string{"claude", "codex"}, RealClaude: "/bin/claude", RealCodex: "/bin/codex", ScreenPath: "/bin/screen", CLIProxyService: "none"}, true},
		{"missing codex", Runtime{SchemaVersion: 2, EnabledBackends: []string{"codex"}, ScreenPath: "/bin/screen", CLIProxyService: "none"}, false},
		{"relative codex", Runtime{SchemaVersion: 2, EnabledBackends: []string{"codex"}, RealCodex: "bin/codex", ScreenPath: "/bin/screen", CLIProxyService: "none"}, false},
		{"duplicate", Runtime{SchemaVersion: 2, EnabledBackends: []string{"codex", "codex"}, RealCodex: "/bin/codex", ScreenPath: "/bin/screen", CLIProxyService: "none"}, false},
		{"empty", Runtime{SchemaVersion: 2, EnabledBackends: []string{}, ScreenPath: "/bin/screen", CLIProxyService: "none"}, false},
		{"unknown", Runtime{SchemaVersion: 2, EnabledBackends: []string{"other"}, ScreenPath: "/bin/screen", CLIProxyService: "none"}, false},
		{"legacy cannot enable codex", Runtime{SchemaVersion: 1, EnabledBackends: []string{"codex"}, RealCodex: "/bin/codex", ScreenPath: "/bin/screen", CLIProxyService: "none"}, false},
		{"disabled proxy leak", Runtime{SchemaVersion: 2, EnabledBackends: []string{"codex"}, RealCodex: "/bin/codex", ScreenPath: "/bin/screen", CLIProxyService: "none", ProxyCredential: "/private/secret"}, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			if err := test.runtime.Validate(); (err == nil) != test.valid {
				t.Fatalf("valid=%t err=%v", test.valid, err)
			}
		})
	}
}

func TestLoadingLegacyRuntimeDoesNotRewriteOrEnableCodex(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	legacy := Runtime{SchemaVersion: 1, RealClaude: "/bin/claude", ClaudexMode: "external", RealClaudex: "/bin/claudex", ScreenPath: "/bin/screen", CLIProxyService: "none"}
	if err := Save(path, legacy); err != nil {
		t.Fatal(err)
	}
	before, _ := os.ReadFile(path)
	loaded, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.BackendEnabled("codex") || !loaded.BackendEnabled("claude") || !loaded.BackendEnabled("claudex") {
		t.Fatalf("legacy selection changed: %+v", loaded.Backends())
	}
	after, _ := os.ReadFile(path)
	if !bytes.Equal(before, after) {
		t.Fatal("loading rewrote legacy config")
	}
}
