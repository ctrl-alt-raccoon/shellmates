package setup

import (
	"bytes"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

func TestMutateProxyYAMLStructuredAndIdempotent(t *testing.T) {
	original := []byte(`# provider comment
provider: &provider
  endpoint: https://example.test
  metadata: {region: us-east-1}
provider-copy: *provider
host: 0.0.0.0
port: 9000
auth-dir: /custom/auth
api-keys: [your-api-key-placeholder, keep-me, managed-key, managed-key]
oauth-model-alias:
  gemini:
    - {name: gemini-model, alias: custom-gemini}
  codex:
    - name: old-model
      alias: claude-opus-4-8
      fork: false
    - {name: custom-model, alias: custom-alias, fork: false}
`)

	updated, changed, err := mutateProxyYAML(original, "managed-key")
	if err != nil {
		t.Fatal(err)
	}
	if !changed {
		t.Fatal("expected YAML mutation")
	}
	if !bytes.Contains(updated, []byte("# provider comment")) || !bytes.Contains(updated, []byte("&provider")) || !bytes.Contains(updated, []byte("*provider")) {
		t.Fatalf("unmanaged comments or anchors were not preserved:\n%s", updated)
	}

	document := decodeTestDocument(t, updated)
	root := document.Content[0]
	if value, ok := scalarMappingString(root, "auth-dir"); !ok || value != "/custom/auth" {
		t.Fatalf("auth-dir was not preserved: %q, %v", value, ok)
	}
	_, apiKeys, _ := mappingValue(root, "api-keys")
	var managed, kept int
	for _, item := range apiKeys.Content {
		switch item.Value {
		case "managed-key":
			managed++
		case "keep-me":
			kept++
		}
		if strings.HasPrefix(item.Value, "your-api-key-") {
			t.Fatalf("placeholder key remains: %q", item.Value)
		}
	}
	if managed != 1 || kept != 1 {
		t.Fatalf("unexpected API keys: managed=%d kept=%d", managed, kept)
	}
	_, oauth, _ := mappingValue(root, "oauth-model-alias")
	_, codex, _ := mappingValue(oauth, "codex")
	if aliasCount(codex, "custom-alias") != 1 {
		t.Fatal("unrelated codex alias was not preserved")
	}
	for _, required := range requiredCodexModelAliases {
		if aliasCount(codex, required.ClaudeAlias) != 1 {
			t.Fatalf("required alias %q not replaced exactly once", required.ClaudeAlias)
		}
	}

	second, secondChanged, err := mutateProxyYAML(updated, "managed-key")
	if err != nil {
		t.Fatal(err)
	}
	if secondChanged || !bytes.Equal(updated, second) {
		t.Fatalf("mutation is not idempotent:\nfirst:\n%s\nsecond:\n%s", updated, second)
	}
}

func TestMutateProxyYAMLPreservesCustomAliasesWithMissingOptionalFields(t *testing.T) {
	original := []byte(`api-keys: []
oauth-model-alias:
  codex:
    - {name: custom-name}
    - {alias: custom-alias}
    - {fork: false}
`)
	updated, _, err := mutateProxyYAML(original, "managed-key")
	if err != nil {
		t.Fatal(err)
	}
	for _, fragment := range [][]byte{
		[]byte("name: custom-name"),
		[]byte("alias: custom-alias"),
		[]byte("fork: false"),
	} {
		if !bytes.Contains(updated, fragment) {
			t.Fatalf("custom alias fragment %q was not preserved:\n%s", fragment, updated)
		}
	}
}

func TestValidateManagedProxyYAMLMissingHostReturnsError(t *testing.T) {
	data := []byte(`port: 8317
api-keys: [managed-key]
oauth-model-alias:
  codex:
    - {name: gpt-5.6-sol, alias: claude-opus-4-8, fork: true}
    - {name: gpt-5.6-sol, alias: claude-fable-5, fork: true}
    - {name: gpt-5.6-terra, alias: claude-sonnet-5, fork: true}
    - {name: gpt-5.6-luna, alias: claude-haiku-4-5-20251001, fork: true}
`)
	if err := validateMutatedProxyYAML(data, "managed-key"); err == nil || !strings.Contains(err.Error(), "host is invalid") {
		t.Fatalf("validation error = %v", err)
	}
}

func TestMutateProxyYAMLFlowCollectionsAndDefaultAuthDir(t *testing.T) {
	original := []byte(`{provider: {url: https://example.test}, api-keys: [old], oauth-model-alias: {codex: [{name: custom, alias: custom-alias, fork: false}]}}`)
	updated, _, err := mutateProxyYAML(original, "managed-key")
	if err != nil {
		t.Fatal(err)
	}
	document := decodeTestDocument(t, updated)
	root := document.Content[0]
	if value, ok := scalarMappingString(root, "auth-dir"); !ok || value != "~/.cli-proxy-api" {
		t.Fatalf("missing documented auth-dir default: %q, %v", value, ok)
	}
	if value, ok := scalarMappingString(root, "host"); !ok || value != ProxyHost {
		t.Fatalf("unexpected host: %q, %v", value, ok)
	}
	_, port, found := mappingValue(root, "port")
	if !found || port.Value != "8317" || port.Tag != "!!int" {
		t.Fatalf("unexpected port node: %#v", port)
	}
}

func TestValidateProxyConfigYAMLRejectsDocumentsDuplicatesAndWrongKinds(t *testing.T) {
	tests := []struct {
		name string
		yaml string
		want string
	}{
		{name: "empty", yaml: "", want: "top-level mapping"},
		{name: "multiple documents", yaml: "host: localhost\n---\nport: 1\n", want: "exactly one"},
		{name: "top level sequence", yaml: "- host\n", want: "top-level mapping"},
		{name: "quoted duplicate", yaml: "host: localhost\n\"host\": elsewhere\n", want: "duplicate top-level"},
		{name: "structural duplicate", yaml: "oauth-model-alias:\n  codex: []\n  \"codex\": []\n", want: "duplicate oauth-model-alias.codex"},
		{name: "host kind", yaml: "host: [localhost]\n", want: "host must be a string scalar"},
		{name: "port kind", yaml: "port: \"8317\"\n", want: "port must be an integer scalar"},
		{name: "api keys kind", yaml: "api-keys: key\n", want: "api-keys must be a sequence"},
		{name: "api key entry", yaml: "api-keys: [1]\n", want: "entries must be string scalars"},
		{name: "oauth kind", yaml: "oauth-model-alias: []\n", want: "must be a mapping"},
		{name: "codex kind", yaml: "oauth-model-alias: {codex: {}}\n", want: "codex must be a sequence"},
		{name: "codex entry kind", yaml: "oauth-model-alias: {codex: [bad]}\n", want: "entries must be mappings"},
		{name: "alias field kind", yaml: "oauth-model-alias: {codex: [{alias: 1}]}\n", want: "alias must be a string scalar"},
		{name: "fork field kind", yaml: "oauth-model-alias: {codex: [{fork: yes}]}\n", want: "fork must be a boolean scalar"},
		{name: "entry duplicate", yaml: "oauth-model-alias:\n  codex:\n    - alias: one\n      \"alias\": two\n", want: "duplicate \"alias\""},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := ValidateProxyConfigYAML([]byte(test.yaml))
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("ValidateProxyConfigYAML() error = %v, want substring %q", err, test.want)
			}
		})
	}
}

func TestValidateProxyConfigYAMLRejectsUnsafeManagedFeatures(t *testing.T) {
	tests := []struct {
		name string
		yaml string
		want string
	}{
		{name: "managed anchor", yaml: "host: &managed localhost\n", want: "anchor"},
		{name: "managed alias", yaml: "source: &source localhost\nhost: *source\n", want: "alias"},
		{name: "managed custom value tag", yaml: "host: !hostname localhost\n", want: "custom YAML tag"},
		{name: "managed custom key tag", yaml: "!managed host: localhost\n", want: "custom YAML tag"},
		{name: "tagged managed key duplicate", yaml: "!managed host: localhost\nhost: elsewhere\n", want: "custom YAML tag"},
		{name: "nested managed custom key tag", yaml: "oauth-model-alias:\n  !managed codex: []\n", want: "custom YAML tag"},
		{name: "top level merge", yaml: "defaults: &defaults {host: localhost}\n<<: *defaults\n", want: "merge key"},
		{name: "codex merge", yaml: "entry: &entry {name: model}\noauth-model-alias:\n  codex:\n    - <<: *entry\n      alias: custom\n", want: "merge key"},
		{name: "codex anchor", yaml: "oauth-model-alias:\n  codex: &aliases []\n", want: "anchor"},
		{name: "codex custom tag", yaml: "oauth-model-alias:\n  codex: !aliases []\n", want: "custom YAML tag"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := ValidateProxyConfigYAML([]byte(test.yaml))
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("ValidateProxyConfigYAML() error = %v, want substring %q", err, test.want)
			}
		})
	}
}

func TestValidateProxyConfigYAMLPreservesUnsafeUnmanagedFeatures(t *testing.T) {
	data := []byte(`provider-defaults: &defaults
  url: !endpoint https://example.test
!provider provider:
  <<: *defaults
host: localhost
port: 8317
`)
	if err := ValidateProxyConfigYAML(data); err != nil {
		t.Fatalf("unmanaged YAML features should be accepted: %v", err)
	}
	updated, _, err := mutateProxyYAML(data, "managed-key")
	if err != nil {
		t.Fatal(err)
	}
	for _, preserved := range [][]byte{[]byte("&defaults"), []byte("!endpoint"), []byte("!provider"), []byte("*defaults")} {
		if !bytes.Contains(updated, preserved) {
			t.Fatalf("unmanaged YAML feature %q was not preserved:\n%s", preserved, updated)
		}
	}
}

func decodeTestDocument(t *testing.T, data []byte) *yaml.Node {
	t.Helper()
	var document yaml.Node
	if err := yaml.Unmarshal(data, &document); err != nil {
		t.Fatal(err)
	}
	return &document
}

func aliasCount(sequence *yaml.Node, wanted string) int {
	count := 0
	for _, entry := range sequence.Content {
		if alias, ok := aliasEntryString(entry, "alias"); ok && alias == wanted {
			count++
		}
	}
	return count
}
