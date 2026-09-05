package app

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/ctrl-alt-raccoon/shellmates/internal/session"
)

// Run only inside the disposable Linux container described in testdata/ssh.
// Root is needed for sshd's privilege separation, not for the application:
// every remote command and backend runs as the unprivileged fixture account.
func TestLinuxSSHReconnect(t *testing.T) {
	if os.Getenv("SCLAUDE_SSH_INTEGRATION") != "1" {
		t.Skip("opt in inside the disposable testdata/ssh container; no vendor backend is invoked")
	}
	if runtime.GOOS != "linux" || os.Geteuid() != 0 {
		t.Fatal("SSH integration requires root inside the disposable Linux fixture container")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	// Keep AuthorizedKeysFile under the account's real passwd home. A path
	// under /tmp traverses a world-writable ancestor and fails StrictModes.
	root, err := os.MkdirTemp("/home/sclaude-test", "sc-ssh-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := os.RemoveAll(root); err != nil {
			t.Error(err)
		}
	})
	// The fixture account must traverse this parent to read its public key and
	// executables. Private keys and application state have their own modes.
	if err := os.Chmod(root, 0o755); err != nil {
		t.Fatal(err)
	}
	runLocal := func(name string, args ...string) []byte {
		t.Helper()
		cmd := exec.CommandContext(ctx, name, args...)
		output, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("fixture command %s: %v\n%s", filepath.Base(name), err, output)
		}
		return output
	}
	binary := filepath.Join(root, "sclaude")
	runLocal("go", "build", "-o", binary, "../../cmd/sclaude")
	launcher := filepath.Join(root, "scodex")
	if err := os.Symlink(binary, launcher); err != nil {
		t.Fatal(err)
	}
	fake := filepath.Join(root, "fake-codex")
	stopFile := filepath.Join(root, "home", "fixture-stop")
	fakeScript := "#!/bin/sh\n[ -t 0 ] && [ -t 1 ] || exit 91\n" +
		"trap 'exit 143' HUP TERM\n" +
		"while IFS= read -r input; do\n" +
		"  printf 'fixture-reply:%s\\n' \"$input\"\n" +
		"  [ \"$input\" = second-connection ] && break\n" +
		"done\n" +
		"count=0\nwhile [ ! -e \"$HOME/fixture-stop\" ] && [ \"$count\" -lt 1200 ]; do /bin/sleep 0.1; count=$((count+1)); done\n"
	if err := os.WriteFile(fake, []byte(fakeScript), 0o755); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"home", "config", "state", "data", "screen"} {
		dir := filepath.Join(root, name)
		if err := os.Mkdir(dir, 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.Chown(dir, 1000, 1000); err != nil {
			t.Fatal(err)
		}
	}
	hostKey, clientKey := filepath.Join(root, "host-key"), filepath.Join(root, "client-key")
	for _, path := range []string{hostKey, clientKey} {
		runLocal("ssh-keygen", "-q", "-t", "ed25519", "-N", "", "-f", path)
	}
	if err := os.Chown(clientKey+".pub", 1000, 1000); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(clientKey+".pub", 0o600); err != nil {
		t.Fatal(err)
	}
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := listener.Addr().(*net.TCPAddr).Port
	if err := listener.Close(); err != nil {
		t.Fatal(err)
	}
	sshdConfig := filepath.Join(root, "sshd_config")
	serverConfig := fmt.Sprintf("Port %d\nListenAddress 127.0.0.1\nHostKey %s\nPidFile %s\nAuthorizedKeysFile %s.pub\nAllowUsers sclaude-test\nPasswordAuthentication no\nKbdInteractiveAuthentication no\nPermitRootLogin no\nUsePAM no\nPrintMotd no\nStrictModes yes\nLogLevel VERBOSE\n", port, hostKey, filepath.Join(root, "sshd.pid"), clientKey)
	if err := os.WriteFile(sshdConfig, []byte(serverConfig), 0o600); err != nil {
		t.Fatal(err)
	}
	var serverLog sshFixtureBuffer
	server := exec.CommandContext(ctx, "/usr/sbin/sshd", "-D", "-e", "-f", sshdConfig)
	server.Stdout, server.Stderr = &serverLog, &serverLog
	if err := server.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := server.Process.Kill(); err != nil && !errors.Is(err, os.ErrProcessDone) {
			t.Error(err)
		}
		_ = server.Wait()
	})
	publicKey, err := os.ReadFile(hostKey + ".pub")
	if err != nil {
		t.Fatal(err)
	}
	knownHosts := filepath.Join(root, "known_hosts")
	if err := os.WriteFile(knownHosts, []byte(fmt.Sprintf("[127.0.0.1]:%d %s", port, publicKey)), 0o600); err != nil {
		t.Fatal(err)
	}
	sshArgs := []string{"-F", "/dev/null", "-p", strconv.Itoa(port), "-i", clientKey,
		"-o", "IdentitiesOnly=yes", "-o", "IdentityAgent=none", "-o", "BatchMode=yes",
		"-o", "StrictHostKeyChecking=yes", "-o", "UserKnownHostsFile=" + knownHosts,
		"-o", "GlobalKnownHostsFile=/dev/null", "-o", "ConnectTimeout=5", "-o", "LogLevel=ERROR"}
	quote := func(value string) string { return "'" + strings.ReplaceAll(value, "'", "'\\''") + "'" }
	env := []string{"PATH=/usr/bin:/bin", "TERM=xterm-256color", "SHELL=/bin/sh",
		"HOME=" + filepath.Join(root, "home"), "XDG_CONFIG_HOME=" + filepath.Join(root, "config"),
		"XDG_STATE_HOME=" + filepath.Join(root, "state"), "XDG_DATA_HOME=" + filepath.Join(root, "data"),
		"SCREENDIR=" + filepath.Join(root, "screen")}
	remoteCommand := func(args ...string) string {
		words := append([]string{"env", "-i"}, env...)
		words = append(words, launcher)
		words = append(words, args...)
		for i := range words {
			words[i] = quote(words[i])
		}
		return strings.Join(words, " ")
	}
	newSSH := func(tty bool, args ...string) *exec.Cmd {
		commandArgs := append([]string{}, sshArgs...)
		if tty {
			commandArgs = append(commandArgs, "-tt")
		}
		commandArgs = append(commandArgs, "sclaude-test@127.0.0.1", remoteCommand(args...))
		cmd := exec.CommandContext(ctx, "ssh", commandArgs...)
		cmd.WaitDelay = time.Second
		return cmd
	}
	waitFor := func(description string, condition func() bool) {
		t.Helper()
		deadline := time.Now().Add(15 * time.Second)
		for !condition() {
			if time.Now().After(deadline) || ctx.Err() != nil {
				t.Fatalf("timed out: %s; sshd: %s", description, serverLog.String())
			}
			time.Sleep(50 * time.Millisecond)
		}
	}
	waitFor("fixture sshd listens", func() bool {
		conn, err := net.DialTimeout("tcp4", fmt.Sprintf("127.0.0.1:%d", port), time.Second)
		if err != nil {
			return false
		}
		_ = conn.Close()
		return true
	})
	runRemote := func(args ...string) []byte {
		t.Helper()
		var stderr bytes.Buffer
		cmd := newSSH(false, args...)
		cmd.Stderr = &stderr
		output, err := cmd.Output()
		if err != nil || stderr.Len() != 0 {
			t.Fatalf("SSH scodex %q: %v; stderr=%s stdout=%s sshd=%s", args, err, stderr.Bytes(), output, serverLog.String())
		}
		return output
	}
	runRemote("setup", "--non-interactive", "--no-modify-path", "--codex-executable", fake)
	var created session.Record
	t.Cleanup(func() {
		if err := os.WriteFile(stopFile, nil, 0o600); err != nil {
			t.Error(err)
		}
		if created.ID != "" {
			cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cleanupCancel()
			args := append(append([]string{}, sshArgs...), "sclaude-test@127.0.0.1", remoteCommand("stop", "--yes", created.ID))
			cmd := exec.CommandContext(cleanupCtx, "ssh", args...)
			cmd.WaitDelay = time.Second
			if output, err := cmd.CombinedOutput(); err != nil {
				t.Errorf("fixture session cleanup: %v %s", err, output)
			}
		}
	})
	readRecords := func() []session.Record {
		t.Helper()
		var records []session.Record
		if err := json.Unmarshal(runRemote("sessions", "--json"), &records); err != nil {
			t.Fatal(err)
		}
		return records
	}
	runRemote("new", "--topic", "SSH fixture", "--cwd", filepath.Join(root, "home"), "--detach")
	records := readRecords()
	if len(records) != 1 || records[0].State != session.StateRunning || records[0].BackendPID <= 0 {
		t.Fatalf("creation after SSH command exits: %+v", records)
	}
	created = records[0]
	for _, input := range []string{"first-connection", "second-connection"} {
		var terminal sshFixtureBuffer
		attach := newSSH(true, "attach", created.ID)
		attach.Stdout, attach.Stderr = &terminal, &terminal
		stdin, err := attach.StdinPipe()
		if err != nil {
			t.Fatal(err)
		}
		if err := attach.Start(); err != nil {
			t.Fatal(err)
		}
		ended := false
		disconnect := func() {
			if ended {
				return
			}
			ended = true
			// Kill only the locally started SSH transport handle, not any PID
			// read from session metadata. This models an abrupt lost connection.
			_ = attach.Process.Kill()
			_ = stdin.Close()
			_ = attach.Wait()
		}
		t.Cleanup(disconnect)
		waitFor("SSH attaches the same Screen session", func() bool {
			records := readRecords()
			return len(records) == 1 && records[0].ID == created.ID && records[0].ScreenStatus == session.ScreenAttached
		})
		if _, err := fmt.Fprintln(stdin, input); err != nil {
			t.Fatal(err)
		}
		waitFor("interactive backend round trip: "+input, func() bool {
			return strings.Contains(terminal.String(), "fixture-reply:"+input)
		})
		disconnect()
		waitFor("backend survives disconnected transport", func() bool {
			records := readRecords()
			return len(records) == 1 && records[0].ID == created.ID && records[0].BackendPID == created.BackendPID &&
				records[0].State == session.StateRunning && records[0].ScreenStatus == session.ScreenDetached && !records[0].BackendExited
		})
	}
	runRemote("stop", "--yes", created.ID)
	records = readRecords()
	if len(records) != 1 || records[0].Active() || !records[0].BackendExited || records[0].ScreenPID != 0 || records[0].ExitCode == nil {
		t.Fatalf("stop acknowledgement: %+v", records)
	}
	waitFor("direct backend no longer exists", func() bool {
		return errors.Is(syscall.Kill(created.BackendPID, 0), syscall.ESRCH)
	})
	runRemote("prune", "--all-stopped")
	if records := readRecords(); len(records) != 0 {
		t.Fatalf("records after prune: %+v", records)
	}
	created = session.Record{}
	t.Log("unprivileged setup, detached creation, two abrupt SSH disconnects, same-backend reattach/input, acknowledged stop and prune passed")
}

type sshFixtureBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (b *sshFixtureBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.b.Write(p)
}

func (b *sshFixtureBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.b.String()
}
