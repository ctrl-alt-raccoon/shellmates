package app

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ctrl-alt-raccoon/sclaude/internal/config"
	"github.com/ctrl-alt-raccoon/sclaude/internal/managedsettings"
	"github.com/ctrl-alt-raccoon/sclaude/internal/session"
	"github.com/ctrl-alt-raccoon/sclaude/internal/stateroot"
)

func TestClassify(t *testing.T) {
	tests := []struct {
		name             string
		args             []string
		stdinTTY, outTTY bool
		env              map[string]string
		want             Policy
	}{
		{"interactive", nil, true, true, map[string]string{}, PolicyManaged},
		{"print", []string{"-p", "hi"}, true, true, map[string]string{}, PolicyDirect},
		{"long print value", []string{"--print=hi"}, true, true, map[string]string{}, PolicyDirect},
		{"flag after delimiter is vendor data", []string{"--", "--help"}, true, true, map[string]string{}, PolicyManaged},
		{"piped", nil, false, true, map[string]string{}, PolicyDirect},
		{"existing screen", nil, true, true, map[string]string{"STY": "123.x"}, PolicyDirect},
		{"force nested", nil, true, true, map[string]string{"STY": "123.x", "SCLAUDE_FORCE_NEST": "1"}, PolicyManaged},
		{"bypass", nil, true, true, map[string]string{"SCLAUDE_BYPASS": "1"}, PolicyDirect},
		{"force non tty", nil, false, false, map[string]string{"SCLAUDE_FORCE": "1"}, PolicyManaged},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := Classify(test.args, test.stdinTTY, test.outTTY, test.env); got != test.want {
				t.Fatalf("got %q want %q", got, test.want)
			}
		})
	}
}

func TestClassifyInvocationKeepsProductCommandsIsolated(t *testing.T) {
	for _, name := range []string{
		"sclaude",
		"sclaudex",
		"sclaude_darwin_amd64",
		"sclaude_darwin_arm64",
		"sclaude_linux_amd64",
		"sclaude_linux_arm64",
	} {
		backend, product := classifyInvocation(name, []string{"sessions"})
		if !product {
			t.Fatalf("%s sessions was not classified as a product command", name)
		}
		wantBackend := "claude"
		if name == "sclaudex" {
			wantBackend = "claudex"
		}
		if backend != wantBackend {
			t.Fatalf("%s backend = %q want %q", name, backend, wantBackend)
		}
	}
	for _, vendor := range []string{
		"claude",
		"claudex",
		"codex",
		"sclaude_darwin_riscv64",
		"sclaude_linux_arm64.exe",
	} {
		if _, product := classifyInvocation(vendor, []string{"sessions"}); product {
			t.Fatalf("non-product command %s was intercepted", vendor)
		}
	}
}

func TestParseAge(t *testing.T) {
	tests := map[string]time.Duration{
		"30d": 30 * 24 * time.Hour,
		"2h":  2 * time.Hour,
		"0":   0,
	}
	for input, want := range tests {
		got, err := parseAge(input)
		if err != nil || got != want {
			t.Fatalf("parseAge(%q) = %v, %v; want %v", input, got, err, want)
		}
	}
	for _, input := range []string{"", "-1h", "later"} {
		if _, err := parseAge(input); err == nil {
			t.Fatalf("parseAge(%q) succeeded", input)
		}
	}
}

func TestManagedProxyOptionWasSet(t *testing.T) {
	for _, test := range []struct {
		name string
		args []string
		want bool
	}{
		{name: "none"},
		{name: "unrelated", args: []string{"--yes"}},
		{name: "executable", args: []string{"--proxy-executable", "/tmp/proxy"}, want: true},
		{name: "config", args: []string{"--proxy-config=/tmp/config.yaml"}, want: true},
		{name: "explicit auto service", args: []string{"--proxy-service", "auto"}, want: true},
		{name: "system service false", args: []string{"--proxy-system-service=false"}, want: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			fs := flag.NewFlagSet("setup", flag.ContinueOnError)
			var yes, systemService bool
			var executable, config, service string
			fs.BoolVar(&yes, "yes", false, "")
			fs.StringVar(&executable, "proxy-executable", "", "")
			fs.StringVar(&config, "proxy-config", "", "")
			fs.StringVar(&service, "proxy-service", "auto", "")
			fs.BoolVar(&systemService, "proxy-system-service", false, "")
			if err := fs.Parse(test.args); err != nil {
				t.Fatal(err)
			}
			if got := managedProxyOptionWasSet(fs); got != test.want {
				t.Fatalf("managedProxyOptionWasSet() = %v, want %v", got, test.want)
			}
		})
	}
}

func TestRunVersionFromProductBinaries(t *testing.T) {
	for _, name := range []string{"sclaudex", "sclaude_darwin_arm64", "sclaude_linux_amd64"} {
		t.Run(name, func(t *testing.T) {
			var out, errOut bytes.Buffer
			code := Run(context.Background(), filepath.Join("/tmp", name), []string{"version"}, IO{Out: &out, Err: &errOut}, "v1.2.3")
			if code != 0 || out.String() != "v1.2.3\n" || errOut.Len() != 0 {
				t.Fatalf("code=%d stdout=%q stderr=%q", code, out.String(), errOut.String())
			}
		})
	}
}

func TestRunProductHelpAndVersionRejectExtraArguments(t *testing.T) {
	for _, args := range [][]string{{"--help", "extra"}, {"--version", "extra"}, {"version", "extra"}} {
		var out, errOut bytes.Buffer
		code := Run(context.Background(), "/tmp/sclaude", args, IO{Out: &out, Err: &errOut}, "v1.2.3")
		if code != 2 {
			t.Fatalf("Run(%q) code = %d, want 2; stdout=%q stderr=%q", args, code, out.String(), errOut.String())
		}
	}
}

func TestRunInstallReleaseRejectsMissingAndExtraArguments(t *testing.T) {
	for _, args := range [][]string{
		{"_install-release"},
		{"_install-release", "--source", "/tmp/source"},
		{"_install-release", "--source", "/tmp/source", "--version", "v1.2.3", "extra"},
	} {
		var out, errOut bytes.Buffer
		code := Run(context.Background(), "/tmp/sclaude", args, IO{Out: &out, Err: &errOut}, "v1.2.3")
		if code != 2 {
			t.Fatalf("Run(%q) code = %d, want 2; stdout=%q stderr=%q", args, code, out.String(), errOut.String())
		}
	}
}

func TestRunUpdateRejectsInvalidArguments(t *testing.T) {
	for _, args := range [][]string{
		{"update", "extra"},
		{"update", "--unknown"},
		{"update", "--version"},
		{"update", "--repo"},
	} {
		var out, errOut bytes.Buffer
		code := Run(context.Background(), "/tmp/sclaude", args, IO{Out: &out, Err: &errOut}, "v1.2.3")
		if code != 2 {
			t.Fatalf("Run(%q) code = %d, want 2; stdout=%q stderr=%q", args, code, out.String(), errOut.String())
		}
	}
}

func TestStrictCommandParsing(t *testing.T) {
	tests := []struct {
		name     string
		parse    func([]string, *bytes.Buffer) (bool, int)
		args     []string
		wantOK   bool
		wantCode int
		wantErr  string
	}{
		{
			name: "sessions accepts supported flags",
			args: []string{"--active", "--json"},
			parse: func(args []string, output *bytes.Buffer) (bool, int) {
				_, ok, code := parseSessionsCommand("sessions", args, output)
				return ok, code
			},
			wantOK: true,
		},
		{
			name: "sessions rejects conflicting filters",
			args: []string{"--active", "--stopped"},
			parse: func(args []string, output *bytes.Buffer) (bool, int) {
				_, ok, code := parseSessionsCommand("sessions", args, output)
				return ok, code
			},
			wantCode: 2,
			wantErr:  "sclaude sessions: --active and --stopped cannot be used together\n",
		},
		{
			name: "sessions rejects positional",
			args: []string{"extra"},
			parse: func(args []string, output *bytes.Buffer) (bool, int) {
				_, ok, code := parseSessionsCommand("sessions", args, output)
				return ok, code
			},
			wantCode: 2,
			wantErr:  "sclaude sessions: does not accept positional arguments\n",
		},
		{
			name: "attach accepts flag after selector",
			args: []string{"session", "--multi"},
			parse: func(args []string, output *bytes.Buffer) (bool, int) {
				opts, ok, code := parseAttachCommand(args, output)
				if ok && (opts.Selector != "session" || opts.Mode != "multi") {
					return false, 99
				}
				return ok, code
			},
			wantOK: true,
		},
		{
			name: "attach rejects conflicting modes",
			args: []string{"--multi", "--takeover", "session"},
			parse: func(args []string, output *bytes.Buffer) (bool, int) {
				_, ok, code := parseAttachCommand(args, output)
				return ok, code
			},
			wantCode: 2,
			wantErr:  "sclaude attach: --multi and --takeover cannot be used together\n",
		},
		{
			name: "stop requires exactly one selector",
			args: []string{"one", "two", "--yes"},
			parse: func(args []string, output *bytes.Buffer) (bool, int) {
				_, ok, code := parseStopCommand(args, output)
				return ok, code
			},
			wantCode: 2,
			wantErr:  "sclaude stop: requires exactly one session selector\n",
		},
		{
			name: "help exits zero",
			args: []string{"--help"},
			parse: func(args []string, output *bytes.Buffer) (bool, int) {
				return parseNoArgs("verify", args, output)
			},
			wantCode: 0,
			wantErr:  "Usage of verify:\n",
		},
		{
			name: "no argument command rejects bare terminator",
			args: []string{"--"},
			parse: func(args []string, output *bytes.Buffer) (bool, int) {
				return parseNoArgs("verify", args, output)
			},
			wantCode: 2,
			wantErr:  "sclaude verify: does not accept arguments\n",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var output bytes.Buffer
			ok, code := test.parse(test.args, &output)
			if ok != test.wantOK || code != test.wantCode || output.String() != test.wantErr {
				t.Fatalf("ok=%v code=%d stderr=%q; want ok=%v code=%d stderr=%q", ok, code, output.String(), test.wantOK, test.wantCode, test.wantErr)
			}
		})
	}
}

func TestParseNewCommandBoundary(t *testing.T) {
	tests := []struct {
		name        string
		args        []string
		wantOK      bool
		wantCode    int
		wantBackend []string
		wantErr     string
	}{
		{name: "no backend arguments", args: []string{"--topic", "work"}, wantOK: true},
		{name: "forwards suffix", args: []string{"--topic", "work", "--", "-p", "hello"}, wantOK: true, wantBackend: []string{"-p", "hello"}},
		{name: "preserves later delimiter", args: []string{"--", "-p", "hello", "--", "vendor"}, wantOK: true, wantBackend: []string{"-p", "hello", "--", "vendor"}},
		{name: "rejects positional before boundary", args: []string{"hello", "--", "-p"}, wantCode: 2, wantErr: "sclaude new: backend arguments must follow --\n"},
		{name: "rejects unknown command flag", args: []string{"--unknown", "--", "value"}, wantCode: 2, wantErr: "sclaude new: flag provided but not defined: -unknown\n"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var output bytes.Buffer
			opts, ok, code := parseNewCommand(test.args, &output)
			if ok != test.wantOK || code != test.wantCode || output.String() != test.wantErr {
				t.Fatalf("ok=%v code=%d stderr=%q; want ok=%v code=%d stderr=%q", ok, code, output.String(), test.wantOK, test.wantCode, test.wantErr)
			}
			if len(opts.BackendArgs) != len(test.wantBackend) {
				t.Fatalf("backend args=%q want %q", opts.BackendArgs, test.wantBackend)
			}
			for index := range test.wantBackend {
				if opts.BackendArgs[index] != test.wantBackend[index] {
					t.Fatalf("backend args=%q want %q", opts.BackendArgs, test.wantBackend)
				}
			}
		})
	}
}

func TestRunHelpCommand(t *testing.T) {
	var out, errOut bytes.Buffer
	code := Run(context.Background(), "/tmp/sclaude", []string{"help"}, IO{Out: &out, Err: &errOut}, "v1.2.3")
	if code != 0 || errOut.Len() != 0 || !bytes.Contains(out.Bytes(), []byte("Usage:")) {
		t.Fatalf("code=%d stdout=%q stderr=%q", code, out.String(), errOut.String())
	}
}

func TestRunSessionStartsWhileCreatorHoldsStateRootAdmission(t *testing.T) {
	home := t.TempDir()
	configHome := filepath.Join(home, "config")
	stateHome := filepath.Join(home, "state")
	dataHome := filepath.Join(home, "data")
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", configHome)
	t.Setenv("XDG_STATE_HOME", stateHome)
	t.Setenv("XDG_DATA_HOME", dataHome)

	paths, err := config.DefaultPaths()
	if err != nil {
		t.Fatal(err)
	}
	backendPath := filepath.Join(home, "backend")
	if err := os.WriteFile(backendPath, []byte("#!/bin/sh\nexit 0\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	runtimeConfig := config.Runtime{
		SchemaVersion:   config.SchemaVersion,
		RealClaude:      backendPath,
		ClaudexMode:     "external",
		RealClaudex:     backendPath,
		ScreenPath:      "/usr/bin/screen",
		CLIProxyService: "none",
	}
	if err := config.Save(paths.ConfigFile, runtimeConfig); err != nil {
		t.Fatal(err)
	}

	store := session.NewStore(paths.StateRoot)
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	record, err := session.NewRecord("Admission regression", "claude", cwd, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if err := store.SaveLaunch(session.LaunchRequest{
		SchemaVersion: 1,
		SessionID:     record.ID,
	}); err != nil {
		t.Fatal(err)
	}
	if err := store.Save(record); err != nil {
		t.Fatal(err)
	}
	if err := stateroot.Prepare(paths.StateRoot, true); err != nil {
		t.Fatal(err)
	}
	admission, err := stateroot.Acquire(paths.StateRoot, true, stateroot.Hooks{})
	if err != nil {
		t.Fatal(err)
	}
	released := false
	defer func() {
		if !released {
			_ = admission.Release()
		}
	}()

	done := make(chan int, 1)
	go func() {
		done <- Run(
			context.Background(),
			"/tmp/sclaude",
			[]string{"_run-session", record.ID},
			IO{Out: &bytes.Buffer{}, Err: &bytes.Buffer{}},
			"test",
		)
	}()
	select {
	case code := <-done:
		if err := admission.Release(); err != nil {
			t.Fatal(err)
		}
		released = true
		if code != 0 {
			t.Fatalf("_run-session code = %d, want 0", code)
		}
	case <-time.After(time.Second):
		if err := admission.Release(); err != nil {
			t.Fatal(err)
		}
		released = true
		select {
		case <-done:
		case <-time.After(5 * time.Second):
		}
		t.Fatal("_run-session blocked on the creator-held state-root admission lock")
	}

	loaded, err := store.Load(record.ID)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.State != session.StateStopped || loaded.ExitCode == nil || *loaded.ExitCode != 0 {
		t.Fatalf("record = %+v", loaded)
	}
	if _, err := os.Lstat(filepath.Join(paths.LaunchDir, record.ID+".json")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("launch request remains: %v", err)
	}
}

func TestRunRejectsInvalidArgumentsBeforeLoadingConfiguration(t *testing.T) {
	t.Setenv("HOME", "")
	for _, test := range []struct {
		args       []string
		wantPrefix string
	}{
		{args: []string{"setup", "extra"}, wantPrefix: "sclaude setup:"},
		{args: []string{"doctor", "extra"}, wantPrefix: "sclaude doctor:"},
		{args: []string{"doctor", "--"}, wantPrefix: "sclaude doctor:"},
		{args: []string{"verify", "extra"}, wantPrefix: "sclaude verify:"},
		{args: []string{"verify", "--"}, wantPrefix: "sclaude verify:"},
		{args: []string{"rollback", "extra"}, wantPrefix: "sclaude rollback:"},
		{args: []string{"uninstall", "extra"}, wantPrefix: "sclaude uninstall:"},
		{args: []string{"_run-session", "one", "two"}, wantPrefix: "sclaude _run-session:"},
	} {
		var out, errOut bytes.Buffer
		code := Run(context.Background(), "/tmp/sclaude", test.args, IO{Out: &out, Err: &errOut}, "v1.2.3")
		if code != 2 || !bytes.HasPrefix(errOut.Bytes(), []byte(test.wantPrefix)) {
			t.Fatalf("Run(%q) code=%d stdout=%q stderr=%q", test.args, code, out.String(), errOut.String())
		}
	}
}

func TestCommandVerifyRequiresConfiguredManagedMode(t *testing.T) {
	for _, test := range []struct {
		name    string
		prepare func(*testing.T, string)
		want    string
	}{
		{
			name:    "missing runtime",
			prepare: func(*testing.T, string) {},
			want:    "not configured",
		},
		{
			name: "malformed runtime",
			prepare: func(t *testing.T, path string) {
				writeVerifyFile(t, path, []byte("{not-json\n"), 0o600)
			},
			want: "invalid JSON document",
		},
		{
			name: "external mode",
			prepare: func(t *testing.T, path string) {
				if err := config.Save(path, config.Runtime{
					SchemaVersion:   config.SchemaVersion,
					RealClaude:      "/bin/claude",
					ClaudexMode:     "external",
					RealClaudex:     "/bin/claudex",
					ScreenPath:      "/usr/bin/screen",
					CLIProxyService: "none",
				}); err != nil {
					t.Fatal(err)
				}
			},
			want: "not applicable",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			dir := t.TempDir()
			paths := config.Paths{
				ConfigFile: filepath.Join(dir, "config.json"),
				StateRoot:  filepath.Join(dir, "state"),
			}
			test.prepare(t, paths.ConfigFile)
			transport := &verifyCountingTransport{}
			var out, errOut bytes.Buffer
			code := commandVerifyWithClient(
				context.Background(),
				IO{Out: &out, Err: &errOut},
				paths,
				&http.Client{Transport: transport},
			)
			if code != 1 || out.Len() != 0 || !strings.Contains(errOut.String(), test.want) {
				t.Fatalf("code=%d stdout=%q stderr=%q", code, out.String(), errOut.String())
			}
			if got := transport.requests.Load(); got != 0 {
				t.Fatalf("HTTP requests = %d want 0", got)
			}
		})
	}
}

func TestCommandVerifyUsesOnlyRuntimeConfiguredArtifacts(t *testing.T) {
	fixture := newCommandVerifyFixture(t)
	writeVerifyFile(t, fixture.paths.Credential, []byte("invalid fallback credential"), 0o600)
	writeVerifyFile(t, fixture.paths.ManagedSettings, []byte("invalid fallback settings"), 0o600)

	var modelsRequests atomic.Int32
	var messagesRequests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		if got, want := request.Header.Get("Authorization"), "Bearer "+fixture.secret; got != want {
			t.Errorf("Authorization = %q want %q", got, want)
		}
		switch request.URL.Path {
		case "/v1/models":
			modelsRequests.Add(1)
			if request.Method != http.MethodGet {
				t.Errorf("models method = %q want GET", request.Method)
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"data": []map[string]string{
				{"id": "gpt-5.6-sol"}, {"id": "gpt-5.6-terra"}, {"id": "gpt-5.6-luna"},
				{"id": "claude-opus-4-8"}, {"id": "claude-fable-5"}, {"id": "claude-sonnet-5"}, {"id": "claude-haiku-4-5-20251001"},
			}})
		case "/v1/messages":
			messagesRequests.Add(1)
			if request.Method != http.MethodPost {
				t.Errorf("messages method = %q want POST", request.Method)
			}
			if got := request.Header.Get("Anthropic-Version"); got != "2023-06-01" {
				t.Errorf("Anthropic-Version = %q", got)
			}
			_ = json.NewEncoder(w).Encode(map[string]any{
				"type": "message", "role": "assistant", "model": "gpt-5.6-sol",
				"content": []map[string]string{{"type": "text", "text": "OK"}},
			})
		default:
			http.NotFound(w, request)
		}
	}))
	defer server.Close()

	client := *server.Client()
	client.Transport = verifyRewriteTransport{base: server.Client().Transport, serverURL: server.URL}
	var out, errOut bytes.Buffer
	code := commandVerifyWithClient(context.Background(), IO{Out: &out, Err: &errOut}, fixture.paths, &client)
	if code != 0 || errOut.Len() != 0 || !strings.Contains(out.String(), "7 models available") {
		t.Fatalf("code=%d stdout=%q stderr=%q", code, out.String(), errOut.String())
	}
	if got := modelsRequests.Load(); got != 1 {
		t.Fatalf("models requests = %d want 1", got)
	}
	if got := messagesRequests.Load(); got != 1 {
		t.Fatalf("messages requests = %d want 1", got)
	}
}

func TestCommandVerifyRejectsInvalidArtifactsBeforeHTTP(t *testing.T) {
	for _, test := range []struct {
		name   string
		mutate func(*testing.T, commandVerifyFixture)
		want   string
	}{
		{
			name: "credential mode",
			mutate: func(t *testing.T, fixture commandVerifyFixture) {
				if err := os.Chmod(fixture.credentialPath, 0o644); err != nil {
					t.Fatal(err)
				}
			},
			want: "mode 0600",
		},
		{
			name: "proxy config mode",
			mutate: func(t *testing.T, fixture commandVerifyFixture) {
				if err := os.Chmod(fixture.proxyConfigPath, 0o644); err != nil {
					t.Fatal(err)
				}
			},
			want: "mode 0600",
		},
		{
			name: "settings mismatch",
			mutate: func(t *testing.T, fixture commandVerifyFixture) {
				data, err := managedsettings.Encode(config.ManagedProxyBaseURL, "other-secret")
				if err != nil {
					t.Fatal(err)
				}
				writeVerifyFile(t, fixture.settingsPath, data, 0o600)
			},
			want: "does not match",
		},
		{
			name: "proxy key mismatch",
			mutate: func(t *testing.T, fixture commandVerifyFixture) {
				data := strings.ReplaceAll(string(commandVerifyProxyYAML(fixture.secret)), fixture.secret, "other-secret")
				writeVerifyFile(t, fixture.proxyConfigPath, []byte(data), 0o600)
			},
			want: "managed key exactly once",
		},
		{
			name: "proxy missing host",
			mutate: func(t *testing.T, fixture commandVerifyFixture) {
				data := strings.Replace(
					string(commandVerifyProxyYAML(fixture.secret)),
					`host: "127.0.0.1"
`,
					"",
					1,
				)
				writeVerifyFile(t, fixture.proxyConfigPath, []byte(data), 0o600)
			},
			want: "host is invalid",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			fixture := newCommandVerifyFixture(t)
			test.mutate(t, fixture)
			transport := &verifyCountingTransport{}
			var out, errOut bytes.Buffer
			code := commandVerifyWithClient(
				context.Background(),
				IO{Out: &out, Err: &errOut},
				fixture.paths,
				&http.Client{Transport: transport},
			)
			if code != 1 || out.Len() != 0 || !strings.Contains(errOut.String(), test.want) {
				t.Fatalf("code=%d stdout=%q stderr=%q", code, out.String(), errOut.String())
			}
			if got := transport.requests.Load(); got != 0 {
				t.Fatalf("HTTP requests = %d want 0", got)
			}
			for _, secret := range []string{fixture.secret, "other-secret"} {
				if strings.Contains(errOut.String(), secret) {
					t.Fatalf("stderr exposed secret %q: %q", secret, errOut.String())
				}
			}
		})
	}
}

func TestCommandVerifyDoesNotExposeEndpointResponseBody(t *testing.T) {
	fixture := newCommandVerifyFixture(t)
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		requests.Add(1)
		http.Error(w, "response contains "+fixture.secret, http.StatusUnauthorized)
	}))
	defer server.Close()
	client := *server.Client()
	client.Transport = verifyRewriteTransport{base: server.Client().Transport, serverURL: server.URL}
	var out, errOut bytes.Buffer
	code := commandVerifyWithClient(context.Background(), IO{Out: &out, Err: &errOut}, fixture.paths, &client)
	if code != 1 || out.Len() != 0 || !strings.Contains(errOut.String(), "401 Unauthorized") {
		t.Fatalf("code=%d stdout=%q stderr=%q", code, out.String(), errOut.String())
	}
	if got := requests.Load(); got != 1 {
		t.Fatalf("HTTP requests = %d want 1", got)
	}
	if strings.Contains(errOut.String(), fixture.secret) {
		t.Fatalf("stderr exposed secret: %q", errOut.String())
	}
}

func TestCommandDoctorReportsRuntimeLoadFailure(t *testing.T) {
	dir := t.TempDir()
	paths := config.Paths{
		ConfigFile: filepath.Join(dir, "missing-runtime.json"),
		StateRoot:  filepath.Join(dir, "state"),
	}
	var out, errOut bytes.Buffer
	code := commandDoctor(context.Background(), false, IO{Out: &out, Err: &errOut}, paths)
	if code != 1 || errOut.Len() != 0 {
		t.Fatalf("code=%d stdout=%q stderr=%q", code, out.String(), errOut.String())
	}
	if !strings.Contains(out.String(), "runtime configuration") || !strings.Contains(out.String(), "not configured") {
		t.Fatalf("stdout = %q", out.String())
	}
	if strings.Contains(out.String(), "GNU Screen") || strings.Contains(out.String(), "Claude Code") {
		t.Fatalf("doctor probed dependencies after runtime failure: %q", out.String())
	}
}

type commandVerifyFixture struct {
	paths           config.Paths
	credentialPath  string
	settingsPath    string
	proxyConfigPath string
	secret          string
}

func newCommandVerifyFixture(t *testing.T) commandVerifyFixture {
	t.Helper()
	dir := t.TempDir()
	fixture := commandVerifyFixture{
		paths: config.Paths{
			ConfigFile:      filepath.Join(dir, "runtime.json"),
			Credential:      filepath.Join(dir, "fallback-credential.json"),
			ManagedSettings: filepath.Join(dir, "fallback-settings.json"),
			StateRoot:       filepath.Join(dir, "state"),
		},
		credentialPath:  filepath.Join(dir, "configured-credential.json"),
		settingsPath:    filepath.Join(dir, "configured-settings.json"),
		proxyConfigPath: filepath.Join(dir, "configured-proxy.yaml"),
		secret:          "configured-secret",
	}
	credential := config.ProxyCredential{
		SchemaVersion: config.ProxyCredentialSchema,
		BaseURL:       config.ManagedProxyBaseURL,
		APIKey:        fixture.secret,
	}
	credentialData, err := config.EncodeProxyCredential(credential)
	if err != nil {
		t.Fatal(err)
	}
	writeVerifyFile(t, fixture.credentialPath, credentialData, 0o600)
	settingsData, err := managedsettings.Encode(credential.BaseURL, credential.APIKey)
	if err != nil {
		t.Fatal(err)
	}
	writeVerifyFile(t, fixture.settingsPath, settingsData, 0o600)
	writeVerifyFile(t, fixture.proxyConfigPath, commandVerifyProxyYAML(fixture.secret), 0o600)
	if err := config.Save(fixture.paths.ConfigFile, config.Runtime{
		SchemaVersion:      config.SchemaVersion,
		RealClaude:         "/bin/claude",
		ClaudexMode:        "managed_proxy",
		ScreenPath:         "/usr/bin/screen",
		CLIProxyExecutable: "/bin/cliproxyapi",
		CLIProxyConfig:     fixture.proxyConfigPath,
		ProxyCredential:    fixture.credentialPath,
		ManagedSettings:    fixture.settingsPath,
		CLIProxyService:    "none",
	}); err != nil {
		t.Fatal(err)
	}
	return fixture
}

func commandVerifyProxyYAML(secret string) []byte {
	return []byte(`host: "127.0.0.1"
port: 8317
api-keys:
  - "` + secret + `"
oauth-model-alias:
  codex:
    - {name: gpt-5.6-sol, alias: claude-opus-4-8, fork: true}
    - {name: gpt-5.6-sol, alias: claude-fable-5, fork: true}
    - {name: gpt-5.6-terra, alias: claude-sonnet-5, fork: true}
    - {name: gpt-5.6-luna, alias: claude-haiku-4-5-20251001, fork: true}
`)
}

func writeVerifyFile(t *testing.T, path string, data []byte, mode os.FileMode) {
	t.Helper()
	if err := os.WriteFile(path, data, mode); err != nil {
		t.Fatal(err)
	}
}

type verifyCountingTransport struct {
	requests atomic.Int32
}

func (transport *verifyCountingTransport) RoundTrip(*http.Request) (*http.Response, error) {
	transport.requests.Add(1)
	return nil, errors.New("unexpected HTTP request")
}

type verifyRewriteTransport struct {
	base      http.RoundTripper
	serverURL string
}

func (r verifyRewriteTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	serverRequest, err := http.NewRequest(http.MethodGet, r.serverURL, nil)
	if err != nil {
		return nil, err
	}
	clone := request.Clone(request.Context())
	clone.URL.Scheme = serverRequest.URL.Scheme
	clone.URL.Host = serverRequest.URL.Host
	return r.base.RoundTrip(clone)
}

func TestRunInstallReleaseFromPlatformAsset(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_DATA_HOME", filepath.Join(home, "data"))
	source := filepath.Join(t.TempDir(), "sclaude_darwin_arm64")
	if err := os.WriteFile(source, []byte("release"), 0o700); err != nil {
		t.Fatal(err)
	}
	binDir := filepath.Join(home, "bin")
	var out, errOut bytes.Buffer
	code := Run(context.Background(), source, []string{
		"_install-release",
		"--source", source,
		"--version", "v1.2.3",
		"--bin-dir", binDir,
	}, IO{Out: &out, Err: &errOut}, "v1.2.3")
	if code != 0 || errOut.Len() != 0 {
		t.Fatalf("code=%d stdout=%q stderr=%q", code, out.String(), errOut.String())
	}
	for _, name := range []string{"sclaude", "sclaudex"} {
		path := filepath.Join(binDir, name)
		info, err := os.Stat(path)
		if err != nil {
			t.Fatalf("stat %s: %v", name, err)
		}
		if info.Mode()&0o111 == 0 {
			t.Fatalf("%s is not executable", name)
		}
	}
}
