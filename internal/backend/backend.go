package backend

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/ctrl-alt-raccoon/sclaude/internal/config"
	"github.com/ctrl-alt-raccoon/sclaude/internal/managedsettings"
)

const (
	ManagedModel                    = managedsettings.Model
	ManagedOpusModel                = managedsettings.OpusModel
	ManagedSonnetModel              = managedsettings.SonnetModel
	ManagedHaikuModel               = managedsettings.HaikuModel
	ManagedSubagentModel            = managedsettings.SubagentModel
	ManagedAutoCompactWindow        = managedsettings.AutoCompactWindow
	ManagedAutoCompactPercent       = managedsettings.AutoCompactPercent
	ManagedMaxOutputTokens          = managedsettings.MaxOutputTokens
	ManagedDisallowedClaudeAPISkill = "Skill(claude-api)"
)

// Command resolves an enabled backend into exact argv and environment values.
// Native Codex owns its configuration/authentication; only managed claudex reads
// the project's proxy credential and injects Claude-specific arguments.
func Command(runtime config.Runtime, backend string, args []string, env []string) (string, []string, []string, error) {
	if config.ValidBackend(backend) && !runtime.BackendEnabled(backend) {
		return "", nil, nil, fmt.Errorf("backend %q is disabled; enable it with sclaude setup --backends", backend)
	}
	switch backend {
	case "codex":
		if runtime.RealCodex == "" {
			return "", nil, nil, errors.New("Codex CLI executable path is missing")
		}
		return runtime.RealCodex, clone(args), clone(env), nil
	case "claude":
		if runtime.RealClaude == "" {
			return "", nil, nil, errors.New("Claude Code executable path is missing")
		}
		return runtime.RealClaude, clone(args), clone(env), nil
	case "claudex":
		if runtime.ClaudexMode == "external" {
			if runtime.RealClaudex == "" {
				return "", nil, nil, errors.New("external claudex path is missing")
			}
			return runtime.RealClaudex, clone(args), clone(env), nil
		}
		if runtime.ClaudexMode != "managed_proxy" {
			return "", nil, nil, errors.New("invalid claudex mode")
		}
		if runtime.RealClaude == "" {
			return "", nil, nil, errors.New("Claude Code executable path is missing")
		}
		credential, err := config.LoadProxyCredential(runtime.ProxyCredential)
		if err != nil {
			return "", nil, nil, err
		}
		if err := managedsettings.ValidatePrivateFile(runtime.ManagedSettings, credential.BaseURL, credential.APIKey); err != nil {
			return "", nil, nil, err
		}
		env = managedEnvironment(env, credential)
		managedArgs, err := managedArguments(args, runtime.ManagedSettings)
		if err != nil {
			return "", nil, nil, err
		}
		return runtime.RealClaude, managedArgs, env, nil
	default:
		return "", nil, nil, fmt.Errorf("unknown backend %q", backend)
	}
}

func ResolveReal(command string, excludedRoots ...string) (string, error) {
	path, err := exec.LookPath(command)
	if err != nil {
		return "", err
	}
	absolute, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	resolved, err := filepath.EvalSymlinks(absolute)
	if err != nil {
		resolved = absolute
	}
	info, err := os.Stat(resolved)
	if err != nil {
		return "", err
	}
	if info.IsDir() || info.Mode()&0o111 == 0 {
		return "", fmt.Errorf("resolved %s is not executable", command)
	}
	for _, root := range excludedRoots {
		if root == "" {
			continue
		}
		rootAbs, absErr := filepath.Abs(root)
		if absErr != nil {
			continue
		}
		rootResolved, evalErr := filepath.EvalSymlinks(rootAbs)
		if evalErr != nil {
			rootResolved = rootAbs
		}
		if pathWithin(resolved, rootResolved) {
			return "", fmt.Errorf("resolved %s inside excluded project path", command)
		}
	}
	return resolved, nil
}

// ResumeArguments opens the native conversation picker. A Screen record ID is
// deliberately never passed as a vendor conversation ID.
func ResumeArguments(name string) []string {
	if name == "codex" {
		return []string{"resume"}
	}
	return []string{"--resume"}
}

func managedArguments(args []string, settingsPath string) ([]string, error) {
	for _, arg := range args {
		switch {
		case arg == "--":
			return nil, errors.New("managed sclaudex does not accept the -- argument delimiter")
		case arg == "--bare" || strings.HasPrefix(arg, "--bare="):
			return nil, errors.New("managed sclaudex does not accept --bare")
		case arg == "--settings" || strings.HasPrefix(arg, "--settings="):
			return nil, errors.New("managed sclaudex does not allow overriding --settings")
		case arg == "--setting-sources" || strings.HasPrefix(arg, "--setting-sources="):
			return nil, errors.New("managed sclaudex does not allow overriding --setting-sources")
		case arg == "--disallowedTools",
			arg == "--disallowed-tools",
			strings.HasPrefix(arg, "--disallowedTools="),
			strings.HasPrefix(arg, "--disallowed-tools="):
			return nil, errors.New("managed sclaudex does not allow overriding --disallowedTools")
		}
	}
	managed := []string{"--settings", settingsPath, "--model", ManagedModel, "--disallowedTools=" + ManagedDisallowedClaudeAPISkill}
	return append(managed, args...), nil
}

func managedEnvironment(env []string, credential config.ProxyCredential) []string {
	for key, value := range managedsettings.Environment(credential.BaseURL, credential.APIKey) {
		env = setEnv(env, key, value)
	}
	return env
}

func setEnv(env []string, key, value string) []string {
	return append(unsetEnv(env, key), key+"="+value)
}

func unsetEnv(env []string, key string) []string {
	prefix := key + "="
	result := make([]string, 0, len(env))
	for _, item := range env {
		if !strings.HasPrefix(item, prefix) {
			result = append(result, item)
		}
	}
	return result
}

func clone(values []string) []string {
	return append([]string(nil), values...)
}

func pathWithin(path, root string) bool {
	rel, err := filepath.Rel(root, path)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}
