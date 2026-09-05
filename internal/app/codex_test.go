package app

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/ctrl-alt-raccoon/sclaude/internal/config"
)

func TestCodexClassification(t *testing.T) {
	for _, test := range []struct {
		args []string
		want Policy
	}{
		{nil, PolicyManaged}, {[]string{"-p", "work"}, PolicyManaged}, {[]string{"-pwork"}, PolicyManaged},
		{[]string{"--profile=exec", "a prompt"}, PolicyManaged}, {[]string{"-p", "exec"}, PolicyManaged},
		{[]string{"-m", "review", "-C", "exec"}, PolicyManaged},
		{[]string{"-c", `model="exec"`, "resume", "--last"}, PolicyManaged},
		{[]string{"fork", "--last", "-p", "work"}, PolicyManaged},
		{[]string{"--", "exec", "--help"}, PolicyManaged},
		{[]string{"exec", "--json", "prompt"}, PolicyDirect}, {[]string{"e", "resume", "--last"}, PolicyDirect},
		{[]string{"-p", "work", "exec", "prompt"}, PolicyDirect}, {[]string{"-pwork", "review"}, PolicyDirect},
		{[]string{"resume", "--help"}, PolicyDirect}, {[]string{"login", "--device-auth"}, PolicyDirect},
		{[]string{"update"}, PolicyDirect}, {[]string{"doctor"}, PolicyDirect}, {[]string{"mcp-server"}, PolicyDirect},
		{[]string{"features", "list"}, PolicyDirect}, {[]string{"--version"}, PolicyDirect},
		{[]string{"--future-option", "exec"}, PolicyDirect}, {[]string{"--profile"}, PolicyDirect}, {[]string{"-p", "--help"}, PolicyDirect},
	} {
		if got := ClassifyBackend("codex", test.args, true, true, nil); got != test.want {
			t.Errorf("%q: got=%s want=%s", test.args, got, test.want)
		}
		if test.want == PolicyDirect {
			if got := ClassifyBackend("codex", test.args, true, true, map[string]string{"SCLAUDE_FORCE": "1"}); got != PolicyDirect {
				t.Errorf("forced native command was wrapped: %q", test.args)
			}
		}
	}
	if got := ClassifyBackend("claude", []string{"-p", "work"}, true, true, nil); got != PolicyDirect {
		t.Fatal("Claude print semantics changed")
	}
	if got := ClassifyBackend("codex", nil, false, true, nil); got != PolicyDirect {
		t.Fatal("non-TTY Codex wrapped")
	}
	for _, env := range []map[string]string{{"STY": "existing"}, {"SCLAUDE_MANAGED": "1"}, {"SCLAUDE_BYPASS": "1"}} {
		if ClassifyBackend("codex", nil, true, true, env) != PolicyDirect {
			t.Fatalf("nested/bypass launch wrapped: %v", env)
		}
	}
}

func TestScodexCommandNamespaceAndNewArguments(t *testing.T) {
	for _, command := range []string{"new", "setup", "sessions", "attach", "stop", "prune", "_run-session"} {
		name, managed := classifyInvocation("scodex", []string{command})
		if name != "codex" || !managed {
			t.Fatalf("%s: %s %t", command, name, managed)
		}
	}
	for _, command := range []string{"exec", "review", "resume", "fork", "doctor", "help", "update", "login", "logout"} {
		name, managed := classifyInvocation("scodex", []string{command})
		if name != "codex" || managed {
			t.Fatalf("vendor command intercepted: %s", command)
		}
	}
	args := []string{"--topic", "Profile work", "--", "-p", "work", "--", "literal prompt"}
	opts, ok, code := parseNewCommandForBackend(args, &bytes.Buffer{}, "codex")
	if !ok || code != 0 || opts.Backend != "codex" || !reflect.DeepEqual(opts.BackendArgs, args[3:]) {
		t.Fatalf("opts=%+v ok=%t code=%d", opts, ok, code)
	}
}

func TestCodexDispatchProcess(t *testing.T) {
	if os.Getenv("SCLAUDE_TEST_CODEX_PROCESS") != "1" {
		return
	}
	var args []string
	if err := json.Unmarshal([]byte(os.Getenv("SCLAUDE_TEST_CODEX_ARGS")), &args); err != nil {
		os.Exit(99)
	}
	os.Exit(Run(context.Background(), "scodex", args, IO{}, "test"))
}

func TestScodexDirectCommandsPreserveArgvStreamsAndExit(t *testing.T) {
	root := t.TempDir()
	for _, key := range []string{"HOME", "XDG_CONFIG_HOME", "XDG_STATE_HOME", "XDG_DATA_HOME"} {
		t.Setenv(key, filepath.Join(root, key))
	}
	paths, _ := config.DefaultPaths()
	native := filepath.Join(root, "native-codex")
	if err := os.WriteFile(native, []byte("#!/bin/sh\nprintf '%s\\0' \"$@\"\n/bin/cat\nprintf 'native stderr\\n' >&2\nexit 7\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := config.Save(paths.ConfigFile, config.Runtime{SchemaVersion: 2, EnabledBackends: []string{"codex"}, RealCodex: native, ScreenPath: "/must-not-run-screen", CLIProxyService: "none"}); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{{"exec", "--json", "literal prompt"}, {"-p", "work", "exec", "-"}, {"review", "--uncommitted"}, {"doctor"}, {"update"}, {"--help"}, {"--version"}, {"--", "-p", "work", "--", "literal --help"}} {
		encoded, _ := json.Marshal(args)
		cmd := exec.Command(os.Args[0], "-test.run=^TestCodexDispatchProcess$")
		cmd.Env = append(os.Environ(), "SCLAUDE_TEST_CODEX_PROCESS=1", "SCLAUDE_TEST_CODEX_ARGS="+string(encoded), "SCLAUDE_FORCE=1", "STY=unrelated-screen")
		cmd.Stdin = strings.NewReader("synthetic stdin\n")
		var out, errOut bytes.Buffer
		cmd.Stdout, cmd.Stderr = &out, &errOut
		err := cmd.Run()
		if exit, ok := err.(*exec.ExitError); !ok || exit.ExitCode() != 7 {
			t.Fatalf("%q: exit=%v stderr=%q", args, err, errOut.String())
		}
		wantArgs := args
		if args[0] == "--" {
			wantArgs = args[1:]
		}
		want := strings.Join(wantArgs, "\x00") + "\x00synthetic stdin\n"
		if out.String() != want || errOut.String() != "native stderr\n" {
			t.Fatalf("%q: stdout=%q stderr=%q", args, out.String(), errOut.String())
		}
	}
	if _, err := os.Stat(paths.SessionsDir); !os.IsNotExist(err) {
		t.Fatalf("direct dispatch created sessions: %v", err)
	}
}

func TestCodexLeadingDelimiterPreservesSignalExitStatus(t *testing.T) {
	root := t.TempDir()
	for _, key := range []string{"HOME", "XDG_CONFIG_HOME", "XDG_STATE_HOME", "XDG_DATA_HOME"} {
		t.Setenv(key, filepath.Join(root, key))
	}
	paths, _ := config.DefaultPaths()
	native := filepath.Join(root, "native-codex")
	if err := os.WriteFile(native, []byte("#!/bin/sh\nkill -TERM $$\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := config.Save(paths.ConfigFile, config.Runtime{SchemaVersion: 2, EnabledBackends: []string{"codex"}, RealCodex: native, ScreenPath: "/must-not-run-screen", CLIProxyService: "none"}); err != nil {
		t.Fatal(err)
	}
	var output bytes.Buffer
	if code := Run(context.Background(), "scodex", []string{"--", "exec", "synthetic"}, IO{In: strings.NewReader(""), Out: &output, Err: &output}, "test"); code != 143 || output.Len() != 0 {
		t.Fatalf("code=%d output=%q", code, output.String())
	}
}
