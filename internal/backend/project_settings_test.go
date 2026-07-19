package backend

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

type projectPluginSettings struct {
	ExtraKnownMarketplaces map[string]projectMarketplace `json:"extraKnownMarketplaces"`
	EnabledPlugins         map[string]bool               `json:"enabledPlugins"`
}

type projectMarketplace struct {
	Source projectMarketplaceSource `json:"source"`
}

type projectMarketplaceSource struct {
	Source string `json:"source"`
	Repo   string `json:"repo"`
	Ref    string `json:"ref"`
}

func TestProjectCodexPluginDeclaration(t *testing.T) {
	path := filepath.Join("..", "..", ".claude", "settings.json")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var settings projectPluginSettings
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&settings); err != nil {
		t.Fatalf("parse %s: %v", path, err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		t.Fatalf("parse %s: expected one JSON document", path)
	}
	want := projectPluginSettings{
		ExtraKnownMarketplaces: map[string]projectMarketplace{
			"openai-codex": {
				Source: projectMarketplaceSource{
					Source: "github",
					Repo:   "openai/codex-plugin-cc",
					Ref:    "v1.0.6",
				},
			},
		},
		EnabledPlugins: map[string]bool{
			"codex@openai-codex": true,
		},
	}
	if !reflect.DeepEqual(settings, want) {
		t.Fatalf("project plugin declaration = %#v, want %#v", settings, want)
	}
}

func TestManagedProxyPreservesDefaultSettingSources(t *testing.T) {
	args, err := managedArguments(nil, "/tmp/managed-settings.json")
	if err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(args, "\n")
	if strings.Contains(joined, "--setting-sources") {
		t.Fatalf("managed arguments disabled normal setting sources: %q", args)
	}
}
