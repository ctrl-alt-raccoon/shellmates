package backend

import (
	"reflect"
	"testing"

	"github.com/ctrl-alt-raccoon/shellmates/internal/config"
)

func TestCodexPassesArgumentsAndEnvironmentWithoutClaudePolicy(t *testing.T) {
	runtime := config.Runtime{SchemaVersion: 2, EnabledBackends: []string{"codex"}, RealCodex: "/bin/native-codex", ProxyCredential: "/must-not-be-read", ManagedSettings: "/must-not-be-read"}
	args := []string{"resume", "--last", "-p", "work", "-c", `model_reasoning_effort="high"`, "--model", "chosen-model", "--", "a prompt with spaces"}
	env := []string{"HOME=/synthetic/home", "CODEX_HOME=/synthetic/codex", "OPENAI_BASE_URL=https://example.invalid", "ANTHROPIC_BASE_URL=untouched", "SCLAUDE_MANAGED=1"}
	command, gotArgs, gotEnv, err := Command(runtime, "codex", args, env)
	if err != nil || command != runtime.RealCodex || !reflect.DeepEqual(args, gotArgs) || !reflect.DeepEqual(env, gotEnv) {
		t.Fatalf("native command changed: %q %v %v %v", command, gotArgs, gotEnv, err)
	}
	gotArgs[0], gotEnv[0] = "changed", "changed"
	if args[0] != "resume" || env[0] != "HOME=/synthetic/home" {
		t.Fatal("output aliases caller slices")
	}
	if _, _, _, err := Command(runtime, "claude", nil, nil); err == nil {
		t.Fatal("disabled Claude backend accepted")
	}
	if _, _, _, err := Command(config.Runtime{RealCodex: "/bin/codex"}, "codex", nil, nil); err == nil {
		t.Fatal("legacy configuration implicitly enabled Codex")
	}
}

func TestResumeArgumentsUseNativePickers(t *testing.T) {
	for _, name := range []string{"claude", "claudex", "codex"} {
		want := []string{"--resume"}
		if name == "codex" {
			want = []string{"resume"}
		}
		if got := ResumeArguments(name); !reflect.DeepEqual(got, want) {
			t.Fatalf("%s: %v", name, got)
		}
	}
}
