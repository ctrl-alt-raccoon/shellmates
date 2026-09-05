package config

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/ctrl-alt-raccoon/shellmates/internal/fssecure"
)

const (
	SchemaVersion         = 2
	ProxyCredentialSchema = 1
	ManagedProxyBaseURL   = "http://127.0.0.1:8317"
)

var beforePrivateConfigPublish func(string) error
var afterPrivateConfigPublish func(string) error

type Runtime struct {
	SchemaVersion      int      `json:"schema_version"`
	EnabledBackends    []string `json:"enabled_backends,omitempty"`
	RealClaude         string   `json:"real_claude"`
	RealCodex          string   `json:"real_codex,omitempty"`
	ClaudexMode        string   `json:"claudex_mode"`
	RealClaudex        string   `json:"real_claudex,omitempty"`
	ScreenPath         string   `json:"screen_path"`
	CLIProxyExecutable string   `json:"cliproxyapi_executable,omitempty"`
	CLIProxyConfig     string   `json:"cliproxyapi_config,omitempty"`
	ProxyCredential    string   `json:"proxy_credential,omitempty"`
	ManagedSettings    string   `json:"managed_settings,omitempty"`
	CLIProxyService    string   `json:"cliproxyapi_service"`
	CLIProxySystemUnit bool     `json:"cliproxyapi_system_unit,omitempty"`
}

type ProxyCredential struct {
	SchemaVersion int    `json:"schema_version"`
	BaseURL       string `json:"base_url"`
	APIKey        string `json:"api_key"`
	CreatedAt     string `json:"created_at,omitempty"`
}

type Paths struct {
	ConfigFile      string
	Credential      string
	ManagedSettings string
	StateRoot       string
	SessionsDir     string
	LaunchDir       string
	InstallState    string
	DataRoot        string
}

func DefaultPaths() (Paths, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return Paths{}, err
	}
	return PathsFor(home, os.Getenv), nil
}

// PathsFor computes the runtime paths without consulting process-global state.
// It is useful to callers that already have an environment snapshot and to tests.
func PathsFor(home string, getenv func(string) string) Paths {
	if getenv == nil {
		getenv = func(string) string { return "" }
	}
	configHome := xdgHome(getenv("XDG_CONFIG_HOME"), home, ".config")
	stateHome := xdgHome(getenv("XDG_STATE_HOME"), home, ".local", "state")
	dataHome := xdgHome(getenv("XDG_DATA_HOME"), home, ".local", "share")
	configRoot := filepath.Join(configHome, "sclaude")
	stateRoot := filepath.Join(stateHome, "sclaude")
	return Paths{
		ConfigFile:      filepath.Join(configRoot, "config.json"),
		Credential:      filepath.Join(configRoot, "proxy-credential.json"),
		ManagedSettings: filepath.Join(configRoot, "managed-settings.json"),
		StateRoot:       stateRoot,
		SessionsDir:     filepath.Join(stateRoot, "sessions"),
		LaunchDir:       filepath.Join(stateRoot, "launch"),
		InstallState:    filepath.Join(stateRoot, "install"),
		DataRoot:        filepath.Join(dataHome, "sclaude"),
	}
}

func Load(path string) (Runtime, error) {
	data, err := readExactPrivateRegular(path, "sclaude configuration")
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return Runtime{}, fmt.Errorf("sclaude is not configured; run `sclaude setup`: %w", err)
		}
		return Runtime{}, err
	}
	var runtime Runtime
	if err := decodeOneStrict(data, &runtime); err != nil {
		return Runtime{}, errors.New("parse sclaude configuration: invalid JSON document")
	}
	if err := runtime.Validate(); err != nil {
		return Runtime{}, err
	}
	return runtime, nil
}

func Save(path string, runtime Runtime) error {
	data, err := EncodeRuntime(runtime)
	if err != nil {
		return err
	}
	if err := savePrivateAtomic(path, data); err != nil {
		return fmt.Errorf("save sclaude configuration: %w", err)
	}
	return nil
}

func EncodeRuntime(runtime Runtime) ([]byte, error) {
	if err := runtime.Validate(); err != nil {
		return nil, err
	}
	data, err := encodeIndented(runtime)
	if err != nil {
		return nil, errors.New("encode sclaude configuration")
	}
	return data, nil
}

func (runtime Runtime) Validate() error {
	if runtime.SchemaVersion != 1 && runtime.SchemaVersion != SchemaVersion {
		return fmt.Errorf("unsupported sclaude configuration schema version %d", runtime.SchemaVersion)
	}
	if runtime.SchemaVersion == 1 && (runtime.EnabledBackends != nil || runtime.RealCodex != "") {
		return errors.New("native backend configuration requires schema version 2")
	}
	seen := map[string]bool{}
	for _, name := range runtime.Backends() {
		if !ValidBackend(name) || seen[name] {
			return errors.New("enabled_backends contains an unknown or duplicate backend")
		}
		seen[name] = true
	}
	if len(seen) == 0 {
		return errors.New("at least one backend must be enabled")
	}
	if runtime.BackendEnabled("claude") || runtime.ClaudexMode == "managed_proxy" || runtime.RealClaude != "" {
		if err := requireAbsolute("real_claude", runtime.RealClaude); err != nil {
			return err
		}
	}
	if runtime.BackendEnabled("codex") {
		if err := requireAbsolute("real_codex", runtime.RealCodex); err != nil {
			return err
		}
	} else if runtime.RealCodex != "" {
		return errors.New("real_codex is configured but the codex backend is disabled")
	}
	if err := requireAbsolute("screen_path", runtime.ScreenPath); err != nil {
		return err
	}
	switch runtime.CLIProxyService {
	case "brew", "systemd", "docker", "none":
	case "auto":
		return errors.New("sclaude configuration must persist a concrete cliproxyapi_service, not auto")
	default:
		return errors.New("sclaude configuration has an invalid cliproxyapi_service")
	}
	if runtime.CLIProxySystemUnit && runtime.CLIProxyService != "systemd" {
		return errors.New("sclaude configuration enables cliproxyapi_system_unit without the systemd service")
	}

	mode := runtime.ClaudexMode
	if !runtime.BackendEnabled("claudex") {
		if mode != "" && mode != "disabled" {
			return errors.New("claudex_mode is configured but the claudex backend is disabled")
		}
		mode = "disabled"
	}
	switch mode {
	case "disabled":
		if runtime.BackendEnabled("claudex") {
			return errors.New("enabled claudex backend has no routing mode")
		}
		if runtime.RealClaudex != "" || runtime.hasManagedProxyFields() {
			return errors.New("disabled claudex configuration contains proxy fields")
		}
	case "external":
		if err := requireAbsolute("real_claudex", runtime.RealClaudex); err != nil {
			return err
		}
		if runtime.hasManagedProxyFields() {
			return errors.New("sclaude external claudex configuration contains managed proxy fields")
		}
	case "managed_proxy":
		if runtime.RealClaudex != "" {
			return errors.New("sclaude managed proxy configuration contains an external claudex path")
		}
		for _, field := range []struct {
			name  string
			value string
		}{
			{"cliproxyapi_executable", runtime.CLIProxyExecutable},
			{"cliproxyapi_config", runtime.CLIProxyConfig},
			{"proxy_credential", runtime.ProxyCredential},
			{"managed_settings", runtime.ManagedSettings},
		} {
			if err := requireAbsolute(field.name, field.value); err != nil {
				return err
			}
		}
	default:
		return errors.New("sclaude configuration has an invalid claudex_mode")
	}
	return nil
}

func ValidBackend(name string) bool {
	return name == "claude" || name == "claudex" || name == "codex"
}

// Omitted selection retains the original Claude + claudex contract. Loading a
// legacy document never rewrites it or implicitly enables a new provider.
func (runtime Runtime) Backends() []string {
	if runtime.EnabledBackends == nil {
		return []string{"claude", "claudex"}
	}
	return append([]string(nil), runtime.EnabledBackends...)
}

func (runtime Runtime) BackendEnabled(name string) bool {
	for _, enabled := range runtime.Backends() {
		if enabled == name {
			return true
		}
	}
	return false
}

func (runtime Runtime) hasManagedProxyFields() bool {
	return runtime.CLIProxyExecutable != "" || runtime.CLIProxyConfig != "" || runtime.ProxyCredential != "" || runtime.ManagedSettings != "" || runtime.CLIProxyService != "none" || runtime.CLIProxySystemUnit
}

func LoadProxyCredential(path string) (ProxyCredential, error) {
	data, err := readExactPrivateRegular(path, "private proxy credential")
	if err != nil {
		return ProxyCredential{}, err
	}
	return ParseProxyCredential(data)
}

func ParseProxyCredential(data []byte) (ProxyCredential, error) {
	var credential ProxyCredential
	if err := decodeOneStrict(data, &credential); err != nil {
		return ProxyCredential{}, errors.New("parse private proxy credential: invalid JSON document")
	}
	if err := credential.normalizeAndValidate(); err != nil {
		return ProxyCredential{}, err
	}
	return credential, nil
}

func EncodeProxyCredential(credential ProxyCredential) ([]byte, error) {
	if err := credential.normalizeAndValidate(); err != nil {
		return nil, err
	}
	data, err := encodeIndented(credential)
	if err != nil {
		return nil, errors.New("encode private proxy credential")
	}
	return data, nil
}

func (credential *ProxyCredential) normalizeAndValidate() error {
	if credential.SchemaVersion != ProxyCredentialSchema {
		return fmt.Errorf("unsupported private proxy credential schema version %d", credential.SchemaVersion)
	}
	if strings.TrimSpace(credential.APIKey) == "" {
		return errors.New("private proxy credential API key is missing")
	}
	switch credential.BaseURL {
	case ManagedProxyBaseURL:
	case ManagedProxyBaseURL + "/":
		credential.BaseURL = ManagedProxyBaseURL
	default:
		return errors.New("private proxy credential base_url must be http://127.0.0.1:8317")
	}
	return nil
}

func requireAbsolute(name, value string) error {
	if strings.TrimSpace(value) == "" {
		return fmt.Errorf("sclaude configuration is missing %s", name)
	}
	if !filepath.IsAbs(value) {
		return fmt.Errorf("sclaude configuration %s must be an absolute path", name)
	}
	return nil
}

func decodeOneStrict(data []byte, value any) error {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(value); err != nil {
		return err
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		if err == nil {
			return errors.New("trailing JSON value")
		}
		return err
	}
	return nil
}

func encodeIndented(value any) ([]byte, error) {
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(data, '\n'), nil
}

func readExactPrivateRegular(path, description string) ([]byte, error) {
	if path == "" {
		return nil, fmt.Errorf("%s path is missing", description)
	}
	parent, err := fssecure.Open(filepath.Dir(path))
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", description, err)
	}
	defer parent.Close()
	data, info, err := parent.ReadRegular(filepath.Base(path))
	if err != nil {
		if errors.Is(err, os.ErrInvalid) || strings.Contains(err.Error(), "symbolic link") || strings.Contains(err.Error(), "too many levels") {
			return nil, fmt.Errorf("%s must be a regular file, not a symbolic link", description)
		}
		return nil, fmt.Errorf("read %s: %w", description, err)
	}
	if !isExactPrivateMode(info.Mode()) {
		return nil, fmt.Errorf("%s must have mode 0600", description)
	}
	if err := parent.Revalidate(filepath.Dir(path)); err != nil {
		return nil, fmt.Errorf("%s parent changed while reading", description)
	}
	return data, nil
}

func isExactPrivateMode(mode os.FileMode) bool {
	const special = os.ModeSetuid | os.ModeSetgid | os.ModeSticky
	return mode.Perm() == 0o600 && mode&special == 0
}

func savePrivateAtomic(path string, data []byte) error {
	if !filepath.IsAbs(path) ||
		filepath.Clean(path) != path ||
		path == string(filepath.Separator) ||
		filepath.Base(path) == "." ||
		filepath.Base(path) == ".." {
		return errors.New("configuration path must be an absolute clean file path")
	}
	parent := filepath.Dir(path)
	if err := os.MkdirAll(parent, 0o700); err != nil {
		return err
	}
	directory, err := fssecure.Open(parent)
	if err != nil {
		return err
	}
	defer directory.Close()
	if directory.Info().Mode() != os.ModeDir|0o700 {
		if err := directory.Chmod(0o700); err != nil {
			return fmt.Errorf("secure configuration parent: %w", err)
		}
	}
	if err := directory.Revalidate(parent); err != nil {
		return errors.New("configuration parent changed while saving")
	}
	name := filepath.Base(path)
	targetInfo, err := directory.InspectRegular(name)
	if err != nil {
		return fmt.Errorf("configuration target must be a non-symlink regular file: %w", err)
	}
	return directory.WriteAtomicVerified(name, data, 0o600, func() error {
		if beforePrivateConfigPublish != nil {
			if err := beforePrivateConfigPublish(path); err != nil {
				return err
			}
		}
		if err := directory.Revalidate(parent); err != nil {
			return errors.New("configuration parent changed while saving")
		}
		currentInfo, err := directory.InspectRegular(name)
		if err != nil {
			return fmt.Errorf("configuration target changed while saving: %w", err)
		}
		if targetInfo == nil {
			if currentInfo != nil {
				return errors.New("configuration target changed while saving")
			}
		} else if currentInfo == nil || !os.SameFile(targetInfo, currentInfo) {
			return errors.New("configuration target changed while saving")
		}
		return nil
	}, func(publishedInfo os.FileInfo) error {
		if afterPrivateConfigPublish != nil {
			if err := afterPrivateConfigPublish(path); err != nil {
				return err
			}
		}
		if err := directory.Revalidate(parent); err != nil {
			return errors.New("configuration parent changed after saving")
		}
		current, info, err := directory.ReadRegular(name)
		if err != nil {
			return fmt.Errorf("verify saved configuration: %w", err)
		}
		if !os.SameFile(info, publishedInfo) || info.Mode() != 0o600 || !bytes.Equal(current, data) {
			return errors.New("saved configuration changed after publication")
		}
		return nil
	})
}

func xdgHome(value, home string, fallback ...string) string {
	if value != "" {
		return value
	}
	parts := append([]string{home}, fallback...)
	return filepath.Join(parts...)
}
