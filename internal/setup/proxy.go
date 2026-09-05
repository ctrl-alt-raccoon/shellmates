package setup

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/ctrl-alt-raccoon/sclaude/internal/config"
	"gopkg.in/yaml.v3"
)

const (
	ProxyHost = "127.0.0.1"
	ProxyPort = 8317
)

type codexModelAlias struct {
	CodexModel  string
	ClaudeAlias string
	Fork        bool
}

var requiredCodexModelAliases = []codexModelAlias{
	{CodexModel: "gpt-5.6-sol", ClaudeAlias: "claude-opus-4-8", Fork: true},
	{CodexModel: "gpt-5.6-sol", ClaudeAlias: "claude-fable-5", Fork: true},
	{CodexModel: "gpt-5.6-terra", ClaudeAlias: "claude-sonnet-5", Fork: true},
	{CodexModel: "gpt-5.6-luna", ClaudeAlias: "claude-haiku-4-5-20251001", Fork: true},
}

type ProxyCredential = config.ProxyCredential

type preparedProxyConfig struct {
	result         ProxyConfigResult
	credential     config.ProxyCredential
	credentialData []byte
	original       []byte
	updated        []byte
	configMode     os.FileMode
}

type ProxyConfigResult struct {
	ConfigPath       string
	CredentialPath   string
	APIKeySHA256     string
	CreatedAPIKey    bool
	ChangedConfig    bool
	BackupPath       string
	AuthenticationOK bool
}

func FindExternalClaudex(selfExecutables ...string) (string, bool) {
	for _, path := range validatedCommandCandidates("claudex", validateExecutablePath) {
		isSelf := false
		for _, self := range selfExecutables {
			if sameExecutable(path.Resolved, self) {
				isSelf = true
				break
			}
		}
		if !isSelf {
			return path.Candidate, true
		}
	}
	return "", false
}

func DiscoverCLIProxyExecutable() (string, error) {
	return discoverCLIProxyExecutable(
		homebrewExecutablePaths("cliproxyapi"),
		os.UserHomeDir,
	)
}

func discoverCLIProxyExecutable(
	homebrewPaths []string,
	userHomeDir func() (string, error),
) (string, error) {
	if paths := validatedCommandPaths("cliproxyapi", ValidateCLIProxyExecutable); len(paths) > 0 {
		return paths[0], nil
	}
	home, err := userHomeDir()
	if err == nil {
		candidate := filepath.Join(home, "cliproxyapi", "cli-proxy-api")
		if path, validateErr := ValidateCLIProxyExecutable(candidate); validateErr == nil {
			return path, nil
		}
	}
	for _, candidate := range homebrewPaths {
		prefix := filepath.Dir(filepath.Dir(filepath.Clean(candidate)))
		if path, err := validateContainedPath(
			prefix,
			candidate,
			ValidateCLIProxyExecutable,
		); err == nil {
			return path, nil
		}
	}
	return "", errors.New("CLIProxyAPI executable not found")
}

func ValidateCLIProxyExecutable(path string) (string, error) {
	if path == "" {
		return "", errors.New("CLIProxyAPI executable path is missing")
	}
	absolute, err := filepath.Abs(path)
	if err != nil {
		return "", fmt.Errorf("resolve CLIProxyAPI executable: %w", err)
	}
	resolved, err := filepath.EvalSymlinks(absolute)
	if err != nil {
		return "", fmt.Errorf("resolve CLIProxyAPI executable: %w", err)
	}
	info, err := os.Stat(resolved)
	if err != nil {
		return "", fmt.Errorf("inspect CLIProxyAPI executable: %w", err)
	}
	if !info.Mode().IsRegular() || !executableByCurrentUser(info) {
		return "", errors.New("CLIProxyAPI executable must be a regular executable file")
	}
	return resolved, nil
}

func DiscoverCLIProxyConfig() (string, error) {
	return discoverCLIProxyConfig(runtime.GOOS, os.UserHomeDir, resolveActiveHomebrewPrefix)
}

func discoverCLIProxyConfig(goos string, userHomeDir, homebrewPrefix func() (string, error)) (string, error) {
	candidates, candidateErr := cliProxyConfigPaths(goos, userHomeDir, homebrewPrefix)
	for _, candidate := range candidates {
		if resolved, err := validateRegularFilePath(candidate); err == nil {
			return resolved, nil
		}
	}
	if candidateErr != nil {
		return "", candidateErr
	}
	return "", fmt.Errorf("CLIProxyAPI configuration not found in %s", strings.Join(candidates, ", "))
}

func cliProxyConfigPaths(goos string, userHomeDir, homebrewPrefix func() (string, error)) ([]string, error) {
	candidates := []string{}
	var candidateErr error
	if goos == "darwin" {
		prefix, err := homebrewPrefix()
		if err != nil {
			candidateErr = err
		} else if prefix = strings.TrimSpace(prefix); prefix == "" || !filepath.IsAbs(prefix) {
			candidateErr = errors.New("Homebrew prefix must be an absolute path")
		} else {
			candidates = append(candidates, filepath.Join(prefix, "etc", "cliproxyapi.conf"))
		}
	}
	if home, err := userHomeDir(); err == nil {
		candidates = append(candidates, filepath.Join(home, "cliproxyapi", "config.yaml"))
	}
	return candidates, candidateErr
}

func validateRegularFilePath(path string) (string, error) {
	if path == "" {
		return "", errors.New("file path is missing")
	}
	absolute, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	resolved, err := filepath.EvalSymlinks(absolute)
	if err != nil {
		return "", err
	}
	info, err := os.Stat(resolved)
	if err != nil {
		return "", err
	}
	if !info.Mode().IsRegular() {
		return "", errors.New("path must be a regular file")
	}
	return resolved, nil
}

func sameRegularFile(left, right string) bool {
	leftResolved, err := validateRegularFilePath(left)
	if err != nil {
		return false
	}
	rightResolved, err := validateRegularFilePath(right)
	if err != nil {
		return false
	}
	if leftResolved == rightResolved {
		return true
	}
	leftInfo, err := os.Stat(leftResolved)
	if err != nil {
		return false
	}
	rightInfo, err := os.Stat(rightResolved)
	return err == nil && os.SameFile(leftInfo, rightInfo)
}

func ValidateCLIProxyServiceConfig(manager, configPath string) error {
	return validateCLIProxyServiceConfig(manager, configPath, resolveActiveHomebrewPrefix)
}

func validateCLIProxyServiceConfig(manager, configPath string, homebrewPrefix func() (string, error)) error {
	resolved, err := ResolveServiceManager(manager)
	if err != nil {
		return err
	}
	if resolved != "brew" {
		return nil
	}
	prefix, err := homebrewPrefix()
	if err != nil {
		return err
	}
	if prefix = strings.TrimSpace(prefix); prefix == "" || !filepath.IsAbs(prefix) {
		return errors.New("Homebrew prefix must be an absolute path")
	}
	expected := filepath.Join(prefix, "etc", "cliproxyapi.conf")
	if sameContainedRegularFile(
		prefix,
		configPath,
		expected,
		validateRegularFilePath,
	) {
		return nil
	}
	return errors.New("Homebrew service management requires the CLIProxyAPI config under the active brew --prefix; use --proxy-service none for a custom --proxy-config")
}

// A dependency seam for isolated workflow tests; production still resolves
// only the fixed, trusted Homebrew locations, never PATH.
var resolveActiveHomebrewPrefix = func() (string, error) {
	return activeHomebrewPrefixWithPaths("/opt/homebrew/bin/brew", "/usr/local/bin/brew")
}

func activeHomebrewPrefixWithPaths(paths ...string) (string, error) {
	brew, err := resolveTrustedHomebrewExecutableAtPaths("brew", paths...)
	if err != nil {
		return "", fmt.Errorf("resolve Homebrew executable: %w", err)
	}
	brewPath, err := brew.revalidate()
	if err != nil {
		return "", err
	}
	output, err := ExecRunner{}.Output(context.Background(), brewPath, "--prefix")
	if err != nil {
		return "", fmt.Errorf("determine active Homebrew prefix: %w", err)
	}
	return string(output), nil
}

// EnsureProxyConfig applies the documented simple top-level CLIProxyAPI fields.
// It refuses YAML features it cannot preserve safely instead of rewriting them.
func EnsureProxyConfig(configPath, credentialPath string) (ProxyConfigResult, error) {
	prepared, err := prepareProxyConfig(configPath, credentialPath)
	if err != nil {
		return prepared.result, err
	}
	if prepared.result.CreatedAPIKey {
		if err := writePrivateFileSynced(credentialPath, prepared.credentialData, 0o600); err != nil {
			return prepared.result, err
		}
	}
	if !prepared.result.ChangedConfig && prepared.configMode == 0o600 {
		return prepared.result, nil
	}
	if prepared.result.ChangedConfig {
		prepared.result.BackupPath = uniqueBackupPath(configPath, time.Now().UTC())
		if err := writePrivateFileSynced(prepared.result.BackupPath, prepared.original, 0o600); err != nil {
			return prepared.result, fmt.Errorf("back up CLIProxyAPI config: %w", err)
		}
	}
	if err := writePrivateFileSynced(configPath, prepared.updated, 0o600); err != nil {
		return prepared.result, fmt.Errorf("write CLIProxyAPI config: %w", err)
	}
	return prepared.result, nil
}

func prepareProxyConfig(configPath, credentialPath string) (preparedProxyConfig, error) {
	prepared := preparedProxyConfig{
		result: ProxyConfigResult{
			ConfigPath:     configPath,
			CredentialPath: credentialPath,
		},
	}
	original, info, err := readStableRegularFile(configPath)
	if err != nil {
		return prepared, err
	}
	if err := validateProxyYAMLStructure(original); err != nil {
		return prepared, err
	}
	prepared.original = original
	prepared.configMode = info.Mode()

	credential, data, created, err := prepareProxyCredential(credentialPath)
	if err != nil {
		return prepared, err
	}
	prepared.credential = credential
	prepared.credentialData = data
	prepared.result.CreatedAPIKey = created
	digest := sha256.Sum256([]byte(credential.APIKey))
	prepared.result.APIKeySHA256 = hex.EncodeToString(digest[:])

	updated, changed, err := mutateProxyYAML(original, credential.APIKey)
	if err != nil {
		return prepared, err
	}
	prepared.updated = updated
	prepared.result.ChangedConfig = changed
	return prepared, nil
}

func mutateProxyYAML(original []byte, apiKey string) ([]byte, bool, error) {
	document, err := parseProxyYAML(original)
	if err != nil {
		return nil, false, err
	}
	root := document.Content[0]

	setMappingValue(root, "host", quotedStringNode(ProxyHost))
	setMappingValue(root, "port", intNode(ProxyPort))
	if _, _, found := mappingValue(root, "auth-dir"); !found {
		appendMappingValue(root, stringNode("auth-dir"), quotedStringNode("~/.cli-proxy-api"))
	}

	apiKeys := ensureMappingSequence(root, "api-keys")
	filteredKeys := make([]*yaml.Node, 0, len(apiKeys.Content)+1)
	for _, node := range apiKeys.Content {
		if strings.HasPrefix(node.Value, "your-api-key-") || node.Value == apiKey {
			continue
		}
		filteredKeys = append(filteredKeys, node)
	}
	filteredKeys = append(filteredKeys, quotedStringNode(apiKey))
	apiKeys.Content = filteredKeys

	oauthAliases := ensureMappingMap(root, "oauth-model-alias")
	codexAliases := ensureMappingSequence(oauthAliases, "codex")
	required := requiredClaudeAliasSet()
	filteredAliases := make([]*yaml.Node, 0, len(codexAliases.Content)+len(requiredCodexModelAliases))
	for _, entry := range codexAliases.Content {
		alias, ok := aliasEntryString(entry, "alias")
		if ok && required[alias] {
			continue
		}
		filteredAliases = append(filteredAliases, entry)
	}
	for _, modelAlias := range requiredCodexModelAliases {
		filteredAliases = append(filteredAliases, modelAliasNode(modelAlias))
	}
	codexAliases.Content = filteredAliases

	var output bytes.Buffer
	encoder := yaml.NewEncoder(&output)
	encoder.SetIndent(2)
	if err := encoder.Encode(document); err != nil {
		return nil, false, fmt.Errorf("encode CLIProxyAPI config: %w", err)
	}
	if err := encoder.Close(); err != nil {
		return nil, false, fmt.Errorf("encode CLIProxyAPI config: %w", err)
	}
	updated := output.Bytes()
	if err := validateMutatedProxyYAML(updated, apiKey); err != nil {
		return nil, false, fmt.Errorf("validate updated CLIProxyAPI config: %w", err)
	}
	return updated, !bytes.Equal(original, updated), nil
}

// ValidateProxyConfigYAML performs the same structural validation used before a
// config rewrite without changing the supplied YAML.
func ValidateProxyConfigYAML(data []byte) error {
	_, err := parseProxyYAML(data)
	return err
}

// ValidateCLIProxyConfig reads and validates a CLIProxyAPI config without
// mutating it, making it suitable for doctor and verification commands.
func ValidateCLIProxyConfig(configPath string) error {
	data, _, err := readStableRegularFile(configPath)
	if err != nil {
		return err
	}
	return ValidateProxyConfigYAML(data)
}

// ValidateManagedCLIProxyConfig additionally proves that the configured
// loopback listener, managed API key, and required Codex aliases agree with the
// private credential used by sclaudex.
func ValidateManagedCLIProxyConfig(configPath, apiKey string) error {
	data, info, err := readStableRegularFile(configPath)
	if err != nil {
		return err
	}
	if info.Mode() != 0o600 {
		return errors.New("private CLIProxyAPI config must have mode 0600")
	}
	return validateMutatedProxyYAML(data, apiKey)
}

func validateProxyYAMLStructure(original []byte) error {
	return ValidateProxyConfigYAML(original)
}

func parseProxyYAML(data []byte) (*yaml.Node, error) {
	decoder := yaml.NewDecoder(bytes.NewReader(data))
	var document yaml.Node
	if err := decoder.Decode(&document); err != nil {
		if err == io.EOF {
			return nil, errors.New("CLIProxyAPI config must be a single YAML document with a top-level mapping")
		}
		return nil, fmt.Errorf("parse CLIProxyAPI config: %w", err)
	}
	var extra yaml.Node
	if err := decoder.Decode(&extra); err != io.EOF {
		if err == nil {
			return nil, errors.New("CLIProxyAPI config must contain exactly one YAML document")
		}
		return nil, fmt.Errorf("parse CLIProxyAPI config: %w", err)
	}
	if document.Kind != yaml.DocumentNode || len(document.Content) != 1 || document.Content[0].Kind != yaml.MappingNode {
		return nil, errors.New("CLIProxyAPI config must be a single YAML document with a top-level mapping")
	}
	if err := validateManagedProxyNodes(document.Content[0]); err != nil {
		return nil, err
	}
	return &document, nil
}

func validateManagedProxyNodes(root *yaml.Node) error {
	managed := map[string]bool{
		"host": true, "port": true, "auth-dir": true,
		"api-keys": true, "oauth-model-alias": true,
	}
	seen := make(map[string]bool, len(managed))
	for index := 0; index < len(root.Content); index += 2 {
		key := root.Content[index]
		if key.Value == "<<" || key.Tag == "!!merge" {
			return errors.New("CLIProxyAPI top-level mapping contains a YAML merge key")
		}
		name, managedKey := managedScalarKey(key, managed)
		if !managedKey {
			continue
		}
		if err := rejectUnsafeManagedYAML(key, name); err != nil {
			return err
		}
		if !isStringScalar(key) {
			return fmt.Errorf("CLIProxyAPI %s key must be a string scalar", name)
		}
		if seen[name] {
			return fmt.Errorf("CLIProxyAPI config has duplicate top-level %q keys", name)
		}
		seen[name] = true
	}
	for index := 0; index < len(root.Content); index += 2 {
		key, value := root.Content[index], root.Content[index+1]
		name, decoded := decodedMappingKey(key)
		if !decoded || !managed[name] {
			continue
		}
		if name == "oauth-model-alias" {
			if err := rejectUnsafeManagedNode(value, name); err != nil {
				return err
			}
		} else if err := rejectUnsafeManagedYAML(value, name); err != nil {
			return err
		}
		switch name {
		case "host", "auth-dir":
			if !isStringScalar(value) {
				return fmt.Errorf("CLIProxyAPI %s must be a string scalar", name)
			}
		case "port":
			if value.Kind != yaml.ScalarNode || value.Tag != "!!int" {
				return errors.New("CLIProxyAPI port must be an integer scalar")
			}
		case "api-keys":
			if value.Kind != yaml.SequenceNode {
				return errors.New("CLIProxyAPI api-keys must be a sequence")
			}
			for _, apiKey := range value.Content {
				if !isStringScalar(apiKey) {
					return errors.New("CLIProxyAPI api-keys entries must be string scalars")
				}
			}
		case "oauth-model-alias":
			if value.Kind != yaml.MappingNode {
				return errors.New("CLIProxyAPI oauth-model-alias must be a mapping")
			}
			if err := validateOAuthModelAliases(value); err != nil {
				return err
			}
		}
	}
	return nil
}

func validateOAuthModelAliases(aliases *yaml.Node) error {
	codexSeen := false
	for index := 0; index < len(aliases.Content); index += 2 {
		key, value := aliases.Content[index], aliases.Content[index+1]
		if key.Value == "<<" || key.Tag == "!!merge" {
			return errors.New("CLIProxyAPI oauth-model-alias contains a YAML merge key")
		}
		name, managedKey := managedScalarKey(key, map[string]bool{"codex": true})
		if !managedKey {
			continue
		}
		if err := rejectUnsafeManagedYAML(key, "oauth-model-alias."+name); err != nil {
			return err
		}
		if !isStringScalar(key) {
			return errors.New("CLIProxyAPI oauth-model-alias.codex key must be a string scalar")
		}
		if codexSeen {
			return errors.New("CLIProxyAPI config has duplicate oauth-model-alias.codex keys")
		}
		codexSeen = true
		if err := rejectUnsafeManagedYAML(value, "oauth-model-alias.codex"); err != nil {
			return err
		}
		if value.Kind != yaml.SequenceNode {
			return errors.New("CLIProxyAPI oauth-model-alias.codex must be a sequence")
		}
		for _, entry := range value.Content {
			if entry.Kind != yaml.MappingNode {
				return errors.New("CLIProxyAPI oauth-model-alias.codex entries must be mappings")
			}
			seenFields := make(map[string]bool)
			for field := 0; field < len(entry.Content); field += 2 {
				fieldKey, fieldValue := entry.Content[field], entry.Content[field+1]
				if fieldKey.Value == "<<" || fieldKey.Tag == "!!merge" {
					return errors.New("CLIProxyAPI oauth-model-alias.codex entry contains a YAML merge key")
				}
				fieldName, decoded := decodedMappingKey(fieldKey)
				if !decoded {
					return errors.New("CLIProxyAPI oauth-model-alias.codex entry keys must be string scalars")
				}
				if seenFields[fieldName] {
					return fmt.Errorf("CLIProxyAPI oauth-model-alias.codex entry has duplicate %q keys", fieldName)
				}
				seenFields[fieldName] = true
				switch fieldName {
				case "name", "alias":
					if !isStringScalar(fieldValue) {
						return fmt.Errorf("CLIProxyAPI oauth-model-alias.codex %s must be a string scalar", fieldName)
					}
				case "fork":
					if fieldValue.Kind != yaml.ScalarNode || fieldValue.Tag != "!!bool" {
						return errors.New("CLIProxyAPI oauth-model-alias.codex fork must be a boolean scalar")
					}
				}
			}
		}
	}
	return nil
}

func rejectUnsafeManagedYAML(node *yaml.Node, path string) error {
	if err := rejectUnsafeManagedNode(node, path); err != nil {
		return err
	}
	if node.Kind == yaml.MappingNode {
		for index := 0; index < len(node.Content); index += 2 {
			key := node.Content[index]
			if key.Value == "<<" || key.Tag == "!!merge" {
				return fmt.Errorf("CLIProxyAPI %s contains a YAML merge key", path)
			}
		}
	}
	for _, child := range node.Content {
		if err := rejectUnsafeManagedYAML(child, path); err != nil {
			return err
		}
	}
	return nil
}

func rejectUnsafeManagedNode(node *yaml.Node, path string) error {
	if node.Kind == yaml.AliasNode {
		return fmt.Errorf("CLIProxyAPI %s contains a YAML alias", path)
	}
	if node.Anchor != "" {
		return fmt.Errorf("CLIProxyAPI %s contains a YAML anchor", path)
	}
	if strings.HasPrefix(node.Tag, "!") && !strings.HasPrefix(node.Tag, "!!") {
		return fmt.Errorf("CLIProxyAPI %s contains custom YAML tag %q", path, node.Tag)
	}
	return nil
}

func managedScalarKey(node *yaml.Node, managed map[string]bool) (string, bool) {
	if node == nil || node.Kind != yaml.ScalarNode || !managed[node.Value] {
		return "", false
	}
	return node.Value, true
}

func decodedMappingKey(node *yaml.Node) (string, bool) {
	return node.Value, isStringScalar(node)
}

func validateMutatedProxyYAML(data []byte, apiKey string) error {
	document, err := parseProxyYAML(data)
	if err != nil {
		return err
	}
	root := document.Content[0]
	if value, ok := scalarMappingString(root, "host"); !ok || value != ProxyHost {
		return errors.New("updated CLIProxyAPI host is invalid")
	}
	_, port, found := mappingValue(root, "port")
	if !found || port.Kind != yaml.ScalarNode || port.Tag != "!!int" || port.Value != fmt.Sprint(ProxyPort) {
		return errors.New("updated CLIProxyAPI port is invalid")
	}
	_, apiKeys, found := mappingValue(root, "api-keys")
	if !found {
		return errors.New("updated CLIProxyAPI api-keys is missing")
	}
	matches := 0
	for _, item := range apiKeys.Content {
		if item.Value == apiKey {
			matches++
		}
		if strings.HasPrefix(item.Value, "your-api-key-") {
			return errors.New("updated CLIProxyAPI api-keys retains a placeholder key")
		}
	}
	if matches != 1 {
		return errors.New("updated CLIProxyAPI api-keys must contain the managed key exactly once")
	}
	_, oauth, found := mappingValue(root, "oauth-model-alias")
	if !found {
		return errors.New("updated CLIProxyAPI oauth-model-alias is missing")
	}
	_, codex, found := mappingValue(oauth, "codex")
	if !found {
		return errors.New("updated CLIProxyAPI oauth-model-alias.codex is missing")
	}
	for _, requiredAlias := range requiredCodexModelAliases {
		matches = 0
		for _, entry := range codex.Content {
			name, nameOK := aliasEntryString(entry, "name")
			alias, aliasOK := aliasEntryString(entry, "alias")
			fork, forkOK := aliasEntryBool(entry, "fork")
			if aliasOK && alias == requiredAlias.ClaudeAlias {
				matches++
				if !nameOK || name != requiredAlias.CodexModel || !forkOK || fork != requiredAlias.Fork {
					return fmt.Errorf("updated CLIProxyAPI alias %q has invalid fields", alias)
				}
			}
		}
		if matches != 1 {
			return fmt.Errorf("updated CLIProxyAPI alias %q must appear exactly once", requiredAlias.ClaudeAlias)
		}
	}
	return nil
}

func mappingValue(mapping *yaml.Node, wanted string) (*yaml.Node, *yaml.Node, bool) {
	if mapping == nil || mapping.Kind != yaml.MappingNode {
		return nil, nil, false
	}
	for index := 0; index+1 < len(mapping.Content); index += 2 {
		if key := mapping.Content[index]; key.Kind == yaml.ScalarNode && key.Value == wanted {
			return key, mapping.Content[index+1], true
		}
	}
	return nil, nil, false
}

func scalarMappingString(mapping *yaml.Node, wanted string) (string, bool) {
	_, value, found := mappingValue(mapping, wanted)
	if !found || !isStringScalar(value) {
		return "", false
	}
	return value.Value, true
}

func setMappingValue(mapping *yaml.Node, wanted string, value *yaml.Node) {
	_, existing, found := mappingValue(mapping, wanted)
	if found {
		*existing = *value
		return
	}
	appendMappingValue(mapping, stringNode(wanted), value)
}

func appendMappingValue(mapping, key, value *yaml.Node) {
	mapping.Content = append(mapping.Content, key, value)
}

func ensureMappingSequence(mapping *yaml.Node, wanted string) *yaml.Node {
	_, value, found := mappingValue(mapping, wanted)
	if found {
		return value
	}
	value = &yaml.Node{Kind: yaml.SequenceNode, Tag: "!!seq"}
	appendMappingValue(mapping, stringNode(wanted), value)
	return value
}

func ensureMappingMap(mapping *yaml.Node, wanted string) *yaml.Node {
	_, value, found := mappingValue(mapping, wanted)
	if found {
		return value
	}
	value = &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
	appendMappingValue(mapping, stringNode(wanted), value)
	return value
}

func stringNode(value string) *yaml.Node {
	return &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: value}
}

func quotedStringNode(value string) *yaml.Node {
	return &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: value, Style: yaml.DoubleQuotedStyle}
}

func intNode(value int) *yaml.Node {
	return &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!int", Value: fmt.Sprint(value)}
}

func boolNode(value bool) *yaml.Node {
	return &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!bool", Value: fmt.Sprint(value)}
}

func modelAliasNode(modelAlias codexModelAlias) *yaml.Node {
	node := &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map", Style: yaml.FlowStyle}
	appendMappingValue(node, stringNode("name"), quotedStringNode(modelAlias.CodexModel))
	appendMappingValue(node, stringNode("alias"), quotedStringNode(modelAlias.ClaudeAlias))
	appendMappingValue(node, stringNode("fork"), boolNode(modelAlias.Fork))
	return node
}

func aliasEntryString(entry *yaml.Node, field string) (string, bool) {
	_, value, found := mappingValue(entry, field)
	if !found || !isStringScalar(value) {
		return "", false
	}
	return value.Value, true
}

func aliasEntryBool(entry *yaml.Node, field string) (bool, bool) {
	_, value, found := mappingValue(entry, field)
	if !found || value == nil || value.Kind != yaml.ScalarNode || value.Tag != "!!bool" {
		return false, false
	}
	return value.Value == "true", true
}

func isStringScalar(node *yaml.Node) bool {
	return node != nil && node.Kind == yaml.ScalarNode && node.Tag == "!!str"
}

func requiredClaudeAliasSet() map[string]bool {
	aliases := make(map[string]bool, len(requiredCodexModelAliases))
	for _, modelAlias := range requiredCodexModelAliases {
		aliases[modelAlias.ClaudeAlias] = true
	}
	return aliases
}

func prepareProxyCredential(path string) (config.ProxyCredential, []byte, bool, error) {
	credential, err := config.LoadProxyCredential(path)
	if err == nil {
		data, encodeErr := config.EncodeProxyCredential(credential)
		return credential, data, false, encodeErr
	}
	if !errors.Is(err, os.ErrNotExist) {
		return config.ProxyCredential{}, nil, false, err
	}
	secret := make([]byte, 24)
	if _, err := rand.Read(secret); err != nil {
		return config.ProxyCredential{}, nil, false, err
	}
	credential = config.ProxyCredential{
		SchemaVersion: config.ProxyCredentialSchema,
		BaseURL:       config.ManagedProxyBaseURL,
		APIKey:        "sk-" + hex.EncodeToString(secret),
		CreatedAt:     time.Now().UTC().Format(time.RFC3339Nano),
	}
	data, err := config.EncodeProxyCredential(credential)
	if err != nil {
		return config.ProxyCredential{}, nil, false, err
	}
	return credential, data, true, nil
}

func LoadProxyCredential(path string) (config.ProxyCredential, error) {
	return config.LoadProxyCredential(path)
}

func OAuthCommand(executable, configPath string, headless bool) []string {
	args := []string{executable, "--config", configPath}
	if headless {
		return append(args, "--codex-device-login")
	}
	return append(args, "--codex-login")
}

func ServiceCommand(action string) ([]string, error) {
	manager, err := ResolveServiceManager("auto")
	if err != nil {
		return nil, err
	}
	executable, err := prepareServiceExecutable(manager)
	if err != nil {
		return nil, err
	}
	return serviceCommandFor(action, manager, false, executable.path())
}

// ResolveServiceManager maps "" / "auto" to the platform's manager so callers
// can persist and reuse the concrete choice.
func ResolveServiceManager(manager string) (string, error) {
	if manager == "" || manager == "auto" {
		if runtime.GOOS == "darwin" {
			return "brew", nil
		}
		if runtime.GOOS == "linux" {
			return "systemd", nil
		}
		return "", fmt.Errorf("unsupported platform %s", runtime.GOOS)
	}
	return manager, nil
}

func ServiceCommandFor(action, manager string, systemService bool) ([]string, error) {
	manager, err := ResolveServiceManager(manager)
	if err != nil {
		return nil, err
	}
	executable, err := prepareServiceExecutable(manager)
	if err != nil {
		return nil, err
	}
	return serviceCommandFor(action, manager, systemService, executable.path())
}

func prepareServiceExecutable(manager string) (trustedExecutable, error) {
	switch manager {
	case "brew":
		if runtime.GOOS != "darwin" {
			return trustedExecutable{}, errors.New("Homebrew service management is only available on macOS")
		}
		return resolveTrustedExecutableAtPaths("brew", "/opt/homebrew/bin/brew", "/usr/local/bin/brew")
	case "systemd":
		return resolveTrustedExecutable("systemctl", "/usr/bin/systemctl", "/bin/systemctl")
	case "docker", "none":
		return trustedExecutable{}, nil
	default:
		return trustedExecutable{}, fmt.Errorf("unsupported CLIProxyAPI service manager %q", manager)
	}
}

func serviceCommandFor(action, manager string, systemService bool, executablePath string) ([]string, error) {
	switch manager {
	case "brew":
		if executablePath == "" || !filepath.IsAbs(executablePath) {
			return nil, errors.New("Homebrew service executable path is invalid")
		}
		return []string{executablePath, "services", action, "cliproxyapi"}, nil
	case "systemd":
		if executablePath == "" || !filepath.IsAbs(executablePath) {
			return nil, errors.New("systemctl service executable path is invalid")
		}
		args := []string{executablePath}
		if !systemService {
			args = append(args, "--user")
		}
		args = append(args, action, "cliproxyapi.service")
		return args, nil
	case "docker", "none":
		return nil, nil
	default:
		return nil, fmt.Errorf("unsupported CLIProxyAPI service manager %q", manager)
	}
}

func WaitForProxyModels(ctx context.Context, credentialPath string) ([]string, error) {
	return waitForProxyModels(ctx, credentialPath, 8, 500*time.Millisecond)
}

func waitForProxyModels(ctx context.Context, credentialPath string, attempts int, baseDelay time.Duration) ([]string, error) {
	if attempts < 1 {
		attempts = 1
	}
	var lastErr error
	for attempt := 0; attempt < attempts; attempt++ {
		models, err := VerifyProxyModels(ctx, credentialPath)
		if err == nil {
			return models, nil
		}
		lastErr = err
		if attempt == attempts-1 {
			break
		}
		timer := time.NewTimer(time.Duration(attempt+1) * baseDelay)
		select {
		case <-ctx.Done():
			timer.Stop()
			return nil, ctx.Err()
		case <-timer.C:
		}
	}
	return nil, fmt.Errorf("CLIProxyAPI did not become ready: %w", lastErr)
}

type ProxyVerificationResult struct {
	Models []string
}

func VerifyProxyModels(ctx context.Context, credentialPath string) ([]string, error) {
	credential, err := LoadProxyCredential(credentialPath)
	if err != nil {
		return nil, err
	}
	result, err := VerifyProxy(ctx, &http.Client{Timeout: 15 * time.Second}, credential)
	return result.Models, err
}

func VerifyProxy(ctx context.Context, client *http.Client, credential ProxyCredential) (ProxyVerificationResult, error) {
	result := ProxyVerificationResult{}
	models, err := VerifyProxyModelsEndpoint(ctx, client, credential)
	if err != nil {
		return result, err
	}
	result.Models = models
	if err := VerifyProxyMessagesEndpoint(ctx, client, credential); err != nil {
		return result, err
	}
	return result, nil
}

func VerifyProxyModelsEndpoint(ctx context.Context, client *http.Client, credential ProxyCredential) ([]string, error) {
	models, err := fetchProxyModels(ctx, client, credential)
	if err != nil {
		return nil, err
	}
	if err := verifyRequiredProxyModels(models); err != nil {
		return nil, err
	}
	return models, nil
}

func VerifyProxyMessagesEndpoint(ctx context.Context, client *http.Client, credential ProxyCredential) error {
	return verifyProxyInference(ctx, client, credential)
}

func verifyProxyModels(ctx context.Context, client *http.Client, credential ProxyCredential) ([]string, error) {
	result, err := VerifyProxy(ctx, client, credential)
	return result.Models, err
}

func fetchProxyModels(ctx context.Context, client *http.Client, credential ProxyCredential) ([]string, error) {
	const maxResponseSize = 4 << 20

	client = authenticatedProxyClient(client)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(credential.BaseURL, "/")+"/v1/models", nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+credential.APIKey)
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
		return nil, fmt.Errorf("CLIProxyAPI models endpoint returned HTTP %s", resp.Status)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseSize+1))
	if err != nil {
		return nil, errors.New("read CLIProxyAPI models response")
	}
	if len(data) > maxResponseSize {
		return nil, errors.New("CLIProxyAPI models response is too large")
	}
	var body struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if err := json.Unmarshal(data, &body); err != nil {
		return nil, errors.New("CLIProxyAPI models endpoint returned invalid JSON")
	}
	models := make([]string, 0, len(body.Data))
	for _, item := range body.Data {
		if item.ID != "" {
			models = append(models, item.ID)
		}
	}
	if len(models) == 0 {
		return nil, errors.New("CLIProxyAPI returned no models")
	}
	return models, nil
}

func verifyRequiredProxyModels(models []string) error {
	required := make([]string, 0, len(requiredCodexModelAliases)*2)
	seen := make(map[string]bool, len(requiredCodexModelAliases)*2)
	for _, modelAlias := range requiredCodexModelAliases {
		for _, model := range []string{modelAlias.CodexModel, modelAlias.ClaudeAlias} {
			if !seen[model] {
				required = append(required, model)
				seen[model] = true
			}
		}
	}
	missing := make([]string, 0, len(required))
	for _, wanted := range required {
		found := false
		for _, model := range models {
			if model == wanted || strings.HasPrefix(model, wanted+"(") {
				found = true
				break
			}
		}
		if !found {
			missing = append(missing, wanted)
		}
	}
	if len(missing) > 0 {
		return fmt.Errorf("CLIProxyAPI is missing required Codex models or Claude aliases: %s", strings.Join(missing, ", "))
	}
	return nil
}

func authenticatedProxyClient(client *http.Client) *http.Client {
	if client == nil {
		client = http.DefaultClient
	}
	clone := *client
	clone.CheckRedirect = func(*http.Request, []*http.Request) error {
		return errors.New("authenticated CLIProxyAPI verification does not follow redirects")
	}
	return &clone
}

func verifyProxyInference(ctx context.Context, client *http.Client, credential ProxyCredential) error {
	const maxResponseSize = 1 << 20
	client = authenticatedProxyClient(client)
	requestedAlias := requiredCodexModelAliases[0]
	requestedModel := requestedAlias.ClaudeAlias + "(xhigh)"
	payload, err := json.Marshal(map[string]any{
		"model":      requestedModel,
		"max_tokens": 16,
		"messages": []map[string]string{
			{"role": "user", "content": "Reply with exactly OK"},
		},
	})
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(credential.BaseURL, "/")+"/v1/messages", bytes.NewReader(payload))
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+credential.APIKey)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Anthropic-Version", "2023-06-01")
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("CLIProxyAPI inference request: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, maxResponseSize))
		return fmt.Errorf("CLIProxyAPI Claude alias inference returned HTTP %s", resp.Status)
	}

	data, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseSize+1))
	if err != nil {
		return errors.New("read CLIProxyAPI Claude alias inference response")
	}
	if len(data) > maxResponseSize {
		return errors.New("CLIProxyAPI Claude alias inference response is too large")
	}
	var message struct {
		Type    string `json:"type"`
		Role    string `json:"role"`
		Model   string `json:"model"`
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
	}
	if err := json.Unmarshal(data, &message); err != nil {
		return errors.New("CLIProxyAPI Claude alias inference returned invalid JSON")
	}
	if message.Type != "message" || message.Role != "assistant" {
		return errors.New("CLIProxyAPI Claude alias inference returned an invalid assistant message")
	}
	if !validInferenceModel(message.Model, requestedModel, requestedAlias) {
		return errors.New("CLIProxyAPI Claude alias inference returned an unexpected model")
	}
	var text strings.Builder
	textBlocks := 0
	for _, block := range message.Content {
		if block.Type != "text" {
			continue
		}
		textBlocks++
		text.WriteString(block.Text)
	}
	if textBlocks == 0 || strings.TrimSpace(text.String()) != "OK" {
		return errors.New("CLIProxyAPI Claude alias inference returned unexpected content")
	}
	return nil
}

func validInferenceModel(model, requested string, modelAlias codexModelAlias) bool {
	return model == requested ||
		model == strings.TrimSuffix(requested, "(xhigh)") ||
		model == modelAlias.CodexModel ||
		model == modelAlias.CodexModel+"(xhigh)"
}

func writePrivateFile(path string, data []byte, mode os.FileMode) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".sclaude-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	if err := tmp.Chmod(mode.Perm()); err != nil {
		tmp.Close()
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmpName, path)
}

func fileModeOr(path string, fallback os.FileMode) os.FileMode {
	if info, err := os.Stat(path); err == nil {
		return info.Mode().Perm()
	}
	return fallback
}

func uniqueBackupPath(configPath string, now time.Time) string {
	base := configPath + ".sclaude-backup-" + now.Format("20060102T150405Z")
	if _, err := os.Lstat(base); errors.Is(err, os.ErrNotExist) {
		return base
	}
	for suffix := 2; ; suffix++ {
		candidate := fmt.Sprintf("%s-%d", base, suffix)
		if _, err := os.Lstat(candidate); errors.Is(err, os.ErrNotExist) {
			return candidate
		}
	}
}
