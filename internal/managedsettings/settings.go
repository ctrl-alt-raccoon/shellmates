package managedsettings

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"reflect"
)

const (
	Model                    = "gpt-5.6-sol(xhigh)"
	OpusModel                = Model
	SonnetModel              = "gpt-5.6-sol(high)"
	HaikuModel               = "gpt-5.6-luna(low)"
	SubagentModel            = SonnetModel
	AutoCompactWindow        = "300000"
	AutoCompactPercent       = "60"
	MaxOutputTokens          = "64000"
	MaxWebSearchesPerSession = "1000"
)

type Overlay struct {
	Env map[string]string `json:"env"`
}

func New(baseURL, apiKey string) Overlay {
	return Overlay{Env: Environment(baseURL, apiKey)}
}

func Encode(baseURL, apiKey string) ([]byte, error) {
	data, err := json.MarshalIndent(New(baseURL, apiKey), "", "  ")
	if err != nil {
		return nil, errors.New("encode private managed settings")
	}
	return append(data, '\n'), nil
}

func Environment(baseURL, apiKey string) map[string]string {
	return map[string]string{
		"ANTHROPIC_API_KEY":                        "",
		"ANTHROPIC_AUTH_TOKEN":                     apiKey,
		"ANTHROPIC_BASE_URL":                       baseURL,
		"ANTHROPIC_BEDROCK_BASE_URL":               "",
		"ANTHROPIC_DEFAULT_HAIKU_MODEL":            HaikuModel,
		"ANTHROPIC_DEFAULT_OPUS_MODEL":             OpusModel,
		"ANTHROPIC_DEFAULT_SONNET_MODEL":           SonnetModel,
		"ANTHROPIC_FOUNDRY_BASE_URL":               "",
		"ANTHROPIC_VERTEX_BASE_URL":                "",
		"CLAUDE_AUTOCOMPACT_PCT_OVERRIDE":          AutoCompactPercent,
		"CLAUDE_CODE_AUTO_COMPACT_WINDOW":          AutoCompactWindow,
		"CLAUDE_CODE_MAX_OUTPUT_TOKENS":            MaxOutputTokens,
		"CLAUDE_CODE_MAX_WEB_SEARCHES_PER_SESSION": MaxWebSearchesPerSession,
		"CLAUDE_CODE_SIMPLE":                       "",
		"CLAUDE_CODE_SUBAGENT_MODEL":               SubagentModel,
		"CLAUDE_CODE_USE_BEDROCK":                  "",
		"CLAUDE_CODE_USE_FOUNDRY":                  "",
		"CLAUDE_CODE_USE_VERTEX":                   "",
	}
}

func ValidatePrivateFile(path, baseURL, apiKey string) error {
	if path == "" {
		return errors.New("private managed settings path is missing")
	}
	before, err := os.Lstat(path)
	if err != nil {
		return fmt.Errorf("read private managed settings: %w", err)
	}
	if before.Mode()&os.ModeSymlink != 0 {
		return errors.New("private managed settings must not be a symbolic link")
	}
	if !before.Mode().IsRegular() {
		return errors.New("private managed settings is not a regular file")
	}
	if !isExactPrivateMode(before.Mode()) {
		return errors.New("private managed settings must have mode 0600")
	}
	file, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("read private managed settings: %w", err)
	}
	defer file.Close()
	after, err := file.Stat()
	if err != nil {
		return fmt.Errorf("read private managed settings metadata: %w", err)
	}
	if !os.SameFile(before, after) || !after.Mode().IsRegular() || !isExactPrivateMode(after.Mode()) {
		return errors.New("private managed settings changed while opening")
	}
	data, err := io.ReadAll(file)
	if err != nil {
		return fmt.Errorf("read private managed settings: %w", err)
	}

	var overlay Overlay
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&overlay); err != nil {
		return errors.New("parse private managed settings: invalid JSON document")
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return errors.New("parse private managed settings: invalid JSON document")
	}
	if !reflect.DeepEqual(overlay, New(baseURL, apiKey)) {
		return errors.New("private managed settings does not match the managed proxy configuration; run `sclaude setup`")
	}
	return nil
}

func isExactPrivateMode(mode os.FileMode) bool {
	const special = os.ModeSetuid | os.ModeSetgid | os.ModeSticky
	return mode.Perm() == 0o600 && mode&special == 0
}
