package app

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/ctrl-alt-raccoon/sclaude/internal/config"
	"github.com/ctrl-alt-raccoon/sclaude/internal/screen"
	"github.com/ctrl-alt-raccoon/sclaude/internal/session"
)

// This test builds the real runner but invokes only a local fake harness. It
// verifies the Screen -> runner -> native argv boundary, not vendor inference.
func TestNativeCodexScreenLifecycle(t *testing.T) {
	if os.Getenv("SCLAUDE_SCREEN_INTEGRATION") != "1" {
		t.Skip("opt in with SCLAUDE_SCREEN_INTEGRATION=1; no real backend is invoked")
	}
	screenPath, err := exec.LookPath("screen")
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	binary := filepath.Join(root, "sclaude")
	build := exec.Command("go", "build", "-o", binary, "./cmd/sclaude")
	build.Dir = filepath.Join("..", "..")
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build: %v\n%s", err, output)
	}
	launcher := filepath.Join(root, "scodex")
	if err := os.Symlink(binary, launcher); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"HOME", "XDG_CONFIG_HOME", "XDG_STATE_HOME", "XDG_DATA_HOME"} {
		t.Setenv(key, filepath.Join(root, key))
	}
	socketDir, err := os.MkdirTemp("/tmp", "sc-native-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := os.RemoveAll(socketDir); err != nil {
			t.Error(err)
		}
	})
	t.Setenv("SCREENDIR", socketDir)
	t.Setenv("STY", "")
	t.Setenv("SCLAUDE_MANAGED", "")
	t.Setenv("SCLAUDE_BYPASS", "")
	t.Setenv("SCLAUDE_FAKE_ARGV", filepath.Join(root, "synthetic-argv"))
	stopFile := filepath.Join(root, "fixture-stop")
	inputFile := filepath.Join(root, "fixture-input")
	t.Setenv("SCLAUDE_FAKE_STOP", stopFile)
	t.Setenv("SCLAUDE_FAKE_INPUT", inputFile)
	fake := filepath.Join(root, "fake-codex")
	if err := os.WriteFile(fake, []byte("#!/bin/sh\n[ -t 0 ] && [ -t 1 ] || exit 91\nprintf '%s\\0' \"$@\" > \"$SCLAUDE_FAKE_ARGV\"\ntrap 'exit 143' HUP TERM\nIFS= read -r input\nprintf '%s' \"$input\" > \"$SCLAUDE_FAKE_INPUT\"\ncount=0\nwhile [ ! -e \"$SCLAUDE_FAKE_STOP\" ] && [ \"$count\" -lt 600 ]; do /bin/sleep 0.1; count=$((count+1)); done\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	paths, err := config.DefaultPaths()
	if err != nil {
		t.Fatal(err)
	}
	if err := config.Save(paths.ConfigFile, config.Runtime{SchemaVersion: 2, EnabledBackends: []string{"codex"}, RealCodex: fake, ScreenPath: screenPath, CLIProxyService: "none"}); err != nil {
		t.Fatal(err)
	}
	client := screen.Client{Path: screenPath}
	var backendPID int
	t.Cleanup(func() {
		// A fixture-owned control file works even if both manager and Screen
		// cleanup regress. Never signal a PID loaded from a session record.
		if err := os.WriteFile(stopFile, []byte("stop"), 0o600); err != nil {
			t.Error(err)
		}
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		sockets, err := client.List(ctx)
		if err != nil {
			t.Error(err)
			return
		}
		for _, socket := range sockets {
			if err := client.Stop(ctx, socket.Name); err != nil {
				t.Error(err)
			}
		}
		if backendPID > 0 {
			for !errors.Is(syscall.Kill(backendPID, 0), syscall.ESRCH) {
				if ctx.Err() != nil {
					t.Error("fixture cleanup did not observe backend exit")
					break
				}
				time.Sleep(20 * time.Millisecond)
			}
		}
	})
	run := func(args ...string) []byte {
		t.Helper()
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		cmd := exec.CommandContext(ctx, launcher, args...)
		var stderr bytes.Buffer
		cmd.Stderr = &stderr
		output, err := cmd.Output()
		if err != nil || stderr.Len() != 0 {
			t.Fatalf("scodex %q: %v stderr=%s stdout=%s", args, err, stderr.Bytes(), output)
		}
		return output
	}
	wantArgs := []string{"-p", "work", "-c", `model_reasoning_effort="high"`, "resume", "--last"}
	created := run(append([]string{"new", "--topic", "Native fixture", "--cwd", root, "--detach", "--"}, wantArgs...)...)
	var records []session.Record
	if err := json.Unmarshal(run("sessions", "--json"), &records); err != nil {
		t.Fatal(err)
	}
	if len(records) != 1 {
		t.Fatalf("records=%+v", records)
	}
	record := records[0]
	backendPID = record.BackendPID
	if record.Backend != "codex" || record.State != session.StateRunning || record.ScreenStatus != session.ScreenDetached || record.ScreenPID <= 0 || record.BackendPID <= 0 || !bytes.HasPrefix(created, []byte(record.ID[:8])) {
		t.Fatalf("created=%s record=%+v", created, record)
	}
	deadline := time.Now().Add(5 * time.Second)
	stuff := exec.Command(screenPath, "-S", record.ScreenName, "-p", "0", "-X", "stuff", "ssh-fixture-input\r")
	if output, err := stuff.CombinedOutput(); err != nil {
		t.Fatalf("terminal input: %v %s", err, output)
	}
	for {
		data, err := os.ReadFile(os.Getenv("SCLAUDE_FAKE_ARGV"))
		input, inputErr := os.ReadFile(inputFile)
		if err == nil && inputErr == nil && string(input) == "ssh-fixture-input" && reflect.DeepEqual(strings.Split(strings.TrimSuffix(string(data), "\x00"), "\x00"), wantArgs) {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("native argv=%q error=%v input=%q input error=%v", data, err, input, inputErr)
		}
		time.Sleep(20 * time.Millisecond)
	}
	launchFiles, err := os.ReadDir(paths.LaunchDir)
	if err != nil || len(launchFiles) != 0 {
		t.Fatalf("launch request retained: %v %v", launchFiles, err)
	}
	durable, err := os.ReadFile(filepath.Join(paths.SessionsDir, record.ID+".json"))
	if err != nil || bytes.Contains(durable, []byte("model_reasoning_effort")) {
		t.Fatalf("durable arguments leaked: %v", err)
	}
	run("stop", "--yes", record.ID[:8])
	// Decode into fresh records: omitted zero-valued JSON fields must not reuse
	// values from the prior, running-state slice in this test.
	records = nil
	if err := json.Unmarshal(run("sessions", "--json"), &records); err != nil {
		t.Fatal(err)
	}
	if len(records) != 1 || records[0].Active() || records[0].ScreenPID != 0 || !records[0].BackendExited || records[0].ExitCode == nil {
		t.Fatalf("stop record=%+v", records)
	}
	deadline = time.Now().Add(5 * time.Second)
	for {
		if err := syscall.Kill(record.BackendPID, 0); errors.Is(err, syscall.ESRCH) {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("fake backend survived stop")
		}
		time.Sleep(20 * time.Millisecond)
	}
	run("prune", "--all-stopped")
	records = nil
	if err := json.Unmarshal(run("sessions", "--json"), &records); err != nil || len(records) != 0 {
		t.Fatalf("prune records=%+v error=%v", records, err)
	}
	// External Screen shutdown must not strand the runner if HUP is swallowed
	// by an intermediate login process. The runner has its own absence probe.
	run("new", "--topic", "Socket loss", "--cwd", root, "--detach", "--", "-p", "work")
	records = nil
	if err := json.Unmarshal(run("sessions", "--json"), &records); err != nil || len(records) != 1 {
		t.Fatalf("second session: %+v %v", records, err)
	}
	record = records[0]
	backendPID = record.BackendPID
	if err := client.Stop(context.Background(), record.ScreenName); err != nil {
		t.Fatal(err)
	}
	deadline = time.Now().Add(8 * time.Second)
	for {
		current, err := session.NewStore(paths.StateRoot).Load(record.ID)
		if err == nil && current.BackendExited && current.State == session.StateStopped && errors.Is(syscall.Kill(backendPID, 0), syscall.ESRCH) {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("socket loss stranded backend: %+v %v", current, err)
		}
		time.Sleep(25 * time.Millisecond)
	}
}
